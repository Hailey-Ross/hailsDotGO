package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/i18n"
)

const translateMaxLen = 2000

func editableLang(lang string) bool {
	return lang != "en" && i18n.IsSupported(lang)
}

func (h *Handlers) TranslatePage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		h.render(w, r, "translate", translatePageData{State: "landing"})
		return
	}
	if u.IsTranslator() {
		h.render(w, r, "translate", translatePageData{State: "workspace"})
		return
	}
	app, err := h.loadTranslatorApplication(u.ID)
	if err != nil {
		log.Printf("TranslatePage: %v", err)
		http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if app != nil {
		h.render(w, r, "translate", translatePageData{State: "status", Application: app})
		return
	}
	if !h.maintenanceSettings().TranslatorAppsEnabled {
		h.render(w, r, "translate", translatePageData{State: "closed"})
		return
	}
	h.render(w, r, "translate", translatePageData{State: "form"})
}

type pendingEdit struct {
	ID   uint   `json:"id"`
	Text string `json:"text"`
}

type translateKeyRecord struct {
	Key          string        `json:"key"`
	En           string        `json:"en"`
	Current      string        `json:"current"`
	PendingEdits []pendingEdit `json:"pending_edits,omitempty"`
	RejectReason string        `json:"reject_reason,omitempty"`
}

func (h *Handlers) APITranslateKeys(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	lang := r.URL.Query().Get("lang")
	if !editableLang(lang) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}

	en := i18n.EnglishBundle()
	current := i18n.Bundle(lang)

	pending := map[string][]pendingEdit{}
	rejected := map[string]string{}
	rows, err := h.db.Query(`
		SELECT id, t_key, new_text, status, reject_reason
		FROM translation_edits
		WHERE user_id = ? AND lang = ? AND status IN ('pending','rejected')
		ORDER BY created_at ASC`,
		u.ID, lang)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uint
		var key, text, status, reason string
		if rows.Scan(&id, &key, &text, &status, &reason) != nil {
			continue
		}
		if status == "pending" {
			pending[key] = append(pending[key], pendingEdit{id, text})
		} else {
			rejected[key] = reason // ASC order leaves the most recent rejection
		}
	}

	out := make([]translateKeyRecord, 0, len(en))
	for key, enText := range en {
		rec := translateKeyRecord{Key: key, En: enText, Current: current[key]}
		if edits, ok := pending[key]; ok {
			rec.PendingEdits = edits
		} else if reason, ok := rejected[key]; ok {
			rec.RejectReason = reason
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// Locale strings are PLAIN TEXT. Nothing in them may be markup.
//
// This is not a style preference, it is what keeps a translator from taking over
// the admin panel. Every template ships its locale bundle into a JavaScript object
// (STRINGS, JSC, SH, RF2 and friends), and the client concatenates those values
// into innerHTML in over a hundred places, including
// placeholder="${STRINGS.passwordPh}" in the admin panel itself. Go's templates
// escape the strings correctly on the way out; the JavaScript that consumes them
// afterwards does not. So an approved translation was live markup on every page,
// and the review UI showed the reviewer a correctly escaped preview, meaning the
// payload looked harmless right up until it was approved.
//
// Enforced at submission AND at approval: an edit submitted before this existed is
// still sitting in translation_edits waiting for a reviewer, and a submit-time check
// alone would wave it straight through.
//
// The character set was chosen by measurement, not guesswork. Across all five
// shipped locales (4475 strings) there is not one single "<" or ">", so rejecting
// them costs nothing. Double quotes appear in six strings in total, and they are
// rejected too because an attribute context such as placeholder="..." is broken out
// of with a quote alone, no angle bracket required. Apostrophes are NOT rejected:
// French alone uses 141 of them.
var translationBannedChars = []struct {
	r    rune
	name string
}{
	{'<', "<"},
	{'>', ">"},
	{'"', `"`},
}

// sanitizeTranslation normalises a submitted string and reports what is wrong with
// it, returning the cleaned text and an error message key when it must be refused.
func sanitizeTranslation(text string) (string, string) {
	// Strip control characters outright. They are invisible, they have no place in a
	// UI string, and they are a standard way to smuggle a payload past eyeball review.
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
	cleaned = strings.TrimSpace(cleaned)

	for _, banned := range translationBannedChars {
		if strings.ContainsRune(cleaned, banned.r) {
			return cleaned, "error.tl_markup_not_allowed"
		}
	}
	return cleaned, ""
}

func (h *Handlers) APITranslateSubmit(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	var body struct {
		Lang    string `json:"lang"`
		Key     string `json:"key"`
		NewText string `json:"new_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if !editableLang(body.Lang) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}
	if !i18n.HasKey(body.Key) {
		writeJSONError(w, h.t(r, "error.tl_unknown_key"), http.StatusBadRequest)
		return
	}
	text, badKey := sanitizeTranslation(body.NewText)
	if text == "" {
		writeJSONError(w, h.t(r, "error.tl_empty"), http.StatusBadRequest)
		return
	}
	if badKey != "" {
		writeJSONError(w, h.t(r, badKey), http.StatusBadRequest)
		return
	}
	if len(text) > translateMaxLen {
		writeJSONError(w, h.t(r, "error.tl_too_long"), http.StatusBadRequest)
		return
	}

	old := i18n.Bundle(body.Lang)[body.Key]

	ins, err := h.db.Exec(`
		INSERT INTO translation_edits (user_id, lang, t_key, old_text, new_text)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, body.Lang, body.Key, old, text)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	id, _ := ins.LastInsertId()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

func (h *Handlers) APITranslateWithdraw(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	res, err := h.db.Exec(
		`DELETE FROM translation_edits WHERE id = ? AND user_id = ? AND status = 'pending'`,
		id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

var localeCodeRe = regexp.MustCompile(`^[a-z]{2}$`)

type localeRecord struct {
	Code       string `json:"code"`
	Enabled    bool   `json:"enabled"`
	Embedded   bool   `json:"embedded"`
	Translated int    `json:"translated"`
	Total      int    `json:"total"`
}

func (h *Handlers) APITranslateLocales(w http.ResponseWriter, r *http.Request) {
	total := len(i18n.EnglishBundle())

	enabled := map[string]bool{}
	rows, err := h.db.Query(`SELECT code, enabled FROM locales`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var code string
			var on bool
			if rows.Scan(&code, &on) == nil {
				enabled[code] = on
			}
		}
	} else {
		// Pre-migration fallback: the historical languages are public.
		log.Printf("APITranslateLocales: %v", err)
		enabled["es"], enabled["fr"], enabled["de"] = true, true, true
	}

	out := []localeRecord{}
	for _, lang := range i18n.Langs() {
		if lang == "en" {
			continue
		}
		out = append(out, localeRecord{
			Code:       lang,
			Enabled:    enabled[lang],
			Embedded:   i18n.IsEmbedded(lang),
			Translated: i18n.OverlayCount(lang),
			Total:      total,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// APITranslateLocaleCreate registers a new runtime locale. It starts disabled
// (hidden from the public switcher) but is immediately translatable.
func (h *Handlers) APITranslateLocaleCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	code := strings.ToLower(strings.TrimSpace(body.Code))
	if !localeCodeRe.MatchString(code) || code == "en" {
		writeJSONError(w, h.t(r, "error.tl_locale_code"), http.StatusBadRequest)
		return
	}
	if i18n.IsSupported(code) {
		writeJSONError(w, h.t(r, "error.tl_locale_exists"), http.StatusConflict)
		return
	}

	if _, err := h.db.Exec(
		`INSERT INTO locales (code, enabled, created_by) VALUES (?, 0, ?)`, code, u.ID,
	); err != nil {
		log.Printf("locale create %q: %v", code, err)
		writeJSONError(w, h.t(r, "error.tl_locale_create"), http.StatusConflict)
		return
	}
	if err := i18n.RegisterLocale(code); err != nil {
		log.Printf("locale create %q: register: %v", code, err)
	}
	h.reloadLangs()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "code": code})
}

func (h *Handlers) AdminLocaleEnable(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "en" || !i18n.IsSupported(code) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	// Embedded locales may predate the locales table; upsert so enabling
	// them works without a seeded row.
	res, err := h.db.Exec(`UPDATE locales SET enabled = ? WHERE code = ?`, body.Enabled, code)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM locales WHERE code = ?)`, code).Scan(&exists)
		if !exists {
			if _, err := h.db.Exec(
				`INSERT INTO locales (code, enabled) VALUES (?, ?)`, code, body.Enabled,
			); err != nil {
				writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
				return
			}
		}
	}
	h.reloadLangs()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminLocaleDelete removes a disabled runtime locale: the overlay file is
// archived to backup/, the registration and row are dropped, and the
// locale's pending edits are deleted (approved/rejected history is kept).
func (h *Handlers) AdminLocaleDelete(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "en" || !i18n.IsSupported(code) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}
	if i18n.IsEmbedded(code) {
		writeJSONError(w, h.t(r, "error.tl_locale_embedded"), http.StatusBadRequest)
		return
	}
	if h.langEnabled(code) {
		writeJSONError(w, h.t(r, "error.tl_locale_disable_first"), http.StatusBadRequest)
		return
	}

	if err := i18n.DropLocale(code); err != nil {
		log.Printf("locale delete %q: %v", code, err)
		writeJSONError(w, h.t(r, "error.tl_locale_delete"), http.StatusInternalServerError)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM locales WHERE code = ?`, code); err != nil {
		log.Printf("locale delete %q: row delete: %v", code, err)
	}
	if _, err := h.db.Exec(
		`DELETE FROM translation_edits WHERE lang = ? AND status = 'pending'`, code,
	); err != nil {
		log.Printf("locale delete %q: pending edits: %v", code, err)
	}
	h.reloadLangs()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type translationEditRecord struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	Lang         string `json:"lang"`
	Key          string `json:"key"`
	En           string `json:"en"`
	OldText      string `json:"old_text"`
	NewText      string `json:"new_text"`
	Current      string `json:"current"`
	Status       string `json:"status"`
	RejectReason string `json:"reject_reason"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handlers) AdminTranslationsList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	valid := map[string]bool{"pending": true, "approved": true, "rejected": true}
	if !valid[status] {
		writeJSONError(w, h.t(r, "error.tl_invalid_status"), http.StatusBadRequest)
		return
	}

	rows, err := h.db.Query(`
		SELECT te.id, u.username, te.lang, te.t_key, te.old_text, te.new_text,
		       te.status, te.reject_reason, te.created_at
		FROM translation_edits te JOIN users u ON u.id = te.user_id
		WHERE te.status = ?
		ORDER BY te.lang, te.t_key, te.created_at ASC`,
		status)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	en := i18n.EnglishBundle()
	bundles := map[string]map[string]string{}
	out := []translationEditRecord{}
	for rows.Next() {
		var rec translationEditRecord
		var createdAt time.Time
		if rows.Scan(&rec.ID, &rec.Username, &rec.Lang, &rec.Key, &rec.OldText,
			&rec.NewText, &rec.Status, &rec.RejectReason, &createdAt) != nil {
			continue
		}
		rec.CreatedAt = createdAt.Format("2006-01-02")
		rec.En = en[rec.Key]
		b, ok := bundles[rec.Lang]
		if !ok {
			b = i18n.Bundle(rec.Lang)
			bundles[rec.Lang] = b
		}
		rec.Current = b[rec.Key]
		out = append(out, rec)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) AdminTranslationApprove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var lang, key, newText string
	if err := h.db.QueryRow(
		`SELECT lang, t_key, new_text FROM translation_edits WHERE id = ? AND status = 'pending'`, id,
	).Scan(&lang, &key, &newText); err != nil {
		writeJSONError(w, h.t(r, "error.tl_already_processed"), http.StatusNotFound)
		return
	}

	// Re-check at approval, not just at submission. Anything already queued when the
	// submit-time rule landed has never been through it, and this is the last gate
	// before the string becomes live markup on every page.
	cleanText, badKey := sanitizeTranslation(newText)
	if badKey != "" || cleanText == "" {
		writeJSONError(w, h.t(r, "error.tl_markup_not_allowed"), http.StatusBadRequest)
		return
	}
	newText = cleanText

	// Apply first: backup + atomic file write + in-memory reload. If this
	// fails the row stays pending and nothing changed on disk or in memory.
	if err := i18n.ApplyOverride(lang, key, newText); err != nil {
		log.Printf("translation approve %d: %v", id, err)
		writeJSONError(w, h.t(r, "error.tl_apply_failed"), http.StatusInternalServerError)
		return
	}

	if _, err := h.db.Exec(
		`UPDATE translation_edits SET status = 'approved', reviewed_by = ?, reviewed_at = NOW() WHERE id = ?`,
		actor.ID, id,
	); err != nil {
		// The translation is live but the row stayed pending; re-approval is
		// idempotent, so log loudly and report the inconsistency.
		log.Printf("translation approve %d: applied but row update failed: %v", id, err)
		writeJSONError(w, h.t(r, "error.tl_approve_retry"), http.StatusInternalServerError)
		return
	}

	// Mirror the new approved override to GitHub so it is not stranded only on
	// this server's disk. Debounced and best-effort; never blocks the response.
	triggerTranslationSync()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTranslationReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeJSONError(w, h.t(r, "error.tl_reason_required"), http.StatusBadRequest)
		return
	}
	reason = truncRunes(reason, 255)

	res, err := h.db.Exec(
		`UPDATE translation_edits SET status = 'rejected', reject_reason = ?, reviewed_by = ?, reviewed_at = NOW()
		 WHERE id = ? AND status = 'pending'`,
		reason, actor.ID, id)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.tl_already_processed"), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTranslationsExport(w http.ResponseWriter, r *http.Request) {
	lang := chi.URLParam(r, "lang")
	if !i18n.IsSupported(lang) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}
	data, err := json.MarshalIndent(i18n.Bundle(lang), "", "  ")
	if err != nil {
		writeJSONError(w, h.t(r, "error.tl_export_failed"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lang+`.json"`)
	w.Write(data)
}

func (h *Handlers) AdminToggleTranslator(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Translator bool `json:"translator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.tl_superadmin_translator"), http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET translator = ? WHERE id = ?`, body.Translator, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
