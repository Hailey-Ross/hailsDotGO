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

// editableLang reports whether translators may submit edits for lang.
func editableLang(lang string) bool {
	return lang != "en" && i18n.IsSupported(lang)
}

// ── Translator workspace ──────────────────────────────────────

func (h *Handlers) TranslatePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "translate", nil)
}

type translateKeyRecord struct {
	Key          string `json:"key"`
	En           string `json:"en"`
	Current      string `json:"current"`
	PendingID    uint   `json:"pending_id,omitempty"`
	PendingText  string `json:"pending_text,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
}

func (h *Handlers) APITranslateKeys(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	lang := r.URL.Query().Get("lang")
	if !editableLang(lang) {
		writeJSONError(w, h.t(r, "error.tl_invalid_lang"), http.StatusBadRequest)
		return
	}

	en := i18n.EnglishBundle()
	current := i18n.Bundle(lang)

	type pendingRow struct {
		id   uint
		text string
	}
	pending := map[string]pendingRow{}
	rejected := map[string]string{}
	rows, err := h.db.Query(`
		SELECT id, t_key, new_text, status, reject_reason
		FROM translation_edits
		WHERE user_id = ? AND lang = ? AND status IN ('pending','rejected')
		ORDER BY updated_at ASC`,
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
			pending[key] = pendingRow{id, text}
		} else {
			rejected[key] = reason // ASC order leaves the most recent rejection
		}
	}

	out := make([]translateKeyRecord, 0, len(en))
	for key, enText := range en {
		rec := translateKeyRecord{Key: key, En: enText, Current: current[key]}
		if p, ok := pending[key]; ok {
			rec.PendingID = p.id
			rec.PendingText = p.text
		} else if reason, ok := rejected[key]; ok {
			rec.RejectReason = reason
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) APITranslateSubmit(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
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
	text := strings.TrimSpace(body.NewText)
	if text == "" {
		writeJSONError(w, h.t(r, "error.tl_empty"), http.StatusBadRequest)
		return
	}
	if len(text) > translateMaxLen {
		writeJSONError(w, h.t(r, "error.tl_too_long"), http.StatusBadRequest)
		return
	}

	old := i18n.Bundle(body.Lang)[body.Key]

	// One pending row per (user, lang, key): update in place, insert if absent.
	res, err := h.db.Exec(`
		UPDATE translation_edits SET new_text = ?, old_text = ?
		WHERE user_id = ? AND lang = ? AND t_key = ? AND status = 'pending'`,
		text, old, u.ID, body.Lang, body.Key)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	var id int64
	if n, _ := res.RowsAffected(); n == 0 {
		ins, err := h.db.Exec(`
			INSERT INTO translation_edits (user_id, lang, t_key, old_text, new_text)
			VALUES (?, ?, ?, ?, ?)`,
			u.ID, body.Lang, body.Key, old, text)
		if err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		id, _ = ins.LastInsertId()
	} else {
		h.db.QueryRow(`
			SELECT id FROM translation_edits
			WHERE user_id = ? AND lang = ? AND t_key = ? AND status = 'pending'`,
			u.ID, body.Lang, body.Key).Scan(&id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

func (h *Handlers) APITranslateWithdraw(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
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

// ── Locale management ─────────────────────────────────────────

var localeCodeRe = regexp.MustCompile(`^[a-z]{2}$`)

type localeRecord struct {
	Code       string `json:"code"`
	Enabled    bool   `json:"enabled"`
	Embedded   bool   `json:"embedded"`
	Translated int    `json:"translated"`
	Total      int    `json:"total"`
}

// APITranslateLocales lists every supported locale except en, including
// disabled ones, for the translate page buttons and the admin Locales panel.
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
	u := h.currentUser(r)
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

// AdminLocaleEnable toggles a locale's visibility in the public switcher.
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

// ── Admin review ──────────────────────────────────────────────

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
	if len(reason) > 255 {
		reason = reason[:255]
	}

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

// AdminTranslationsExport serves the merged (embedded + approved overrides)
// locale file so the repo copies can be synced before the next build.
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

// ── Translator permission toggle (superadmin only) ────────────

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
