package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/costumes"
	"pogo.hails.cc/internal/i18n"
	"pogo.hails.cc/internal/mail"
	"pogo.hails.cc/internal/pogodata"
)

type Handlers struct {
	store        *pogodata.Store
	tmpl         map[string]*template.Template
	db           *sql.DB
	startTime    time.Time
	assetVersion string
	notifier     *pushNotifier
	mailer       *mail.Mailer

	langMu       sync.RWMutex
	enabledLangs []string // "en" first, then enabled locales rows, sorted

	// raidMu serializes queue matching and raid timer processing so the
	// matcher never double-assigns a queue slot (single-instance app).
	raidMu sync.Mutex

	// eventSubMu serializes the event reminder sweep against the reconcile that
	// runs after every events feed refresh, so one is never re-pinning a row the
	// other is about to fire. Separate from raidMu on purpose: that lock has a
	// documented job, and the two sweeps share nothing.
	eventSubMu sync.Mutex
}

// PageMaintenance holds the enabled/disabled state for each page and section.
type PageMaintenance struct {
	RaidsEnabled            bool
	DPSEnabled              bool
	PVPEnabled              bool
	EventsEnabled           bool
	IVEnabled               bool
	TrainersEnabled         bool
	TrainerDirectoryEnabled bool
	RaidFinderEnabled       bool
	ShiniesEnabled          bool
	TranslatorAppsEnabled   bool
}

// PageData is the root template data passed to every page.
// User is nil for unauthenticated requests.
type PageData struct {
	User         *auth.User
	Data         any
	CSRFToken    string
	StoreEnabled bool
	Maintenance  PageMaintenance
	Lang         string
	Langs        []string
	AssetVersion string
	Path         string
	ReportCount  int // bug reports the user participates in, any status (drives the persistent Reports nav link)
	ReportUnread int // of those, how many have unseen activity (drives the badge)

	// CostumeLabels is the merged costume label set, injected into the pages whose JS resolves
	// costumes client-side. ts/shared/costumes.ts compiles labels.json in, so without this a
	// costume named in the admin panel would show on public profiles (resolved in Go) but not in
	// the picker, until a redeploy. Empty on pages that do not need it.
	CostumeLabels template.JS
}

func New(store *pogodata.Store, db *sql.DB) *Handlers {
	h := &Handlers{
		store:        store,
		db:           db,
		startTime:    time.Now(),
		assetVersion: computeAssetVersion(),
		notifier:     newPushNotifier(),
		mailer:       mail.New(),
	}
	h.loadTemplates()
	h.reloadLangs()
	// After store.Start() has loaded the fallback and cache, so the first rebuild already has the
	// admin overrides in hand. Later background refreshes re-enter the same rebuild and keep them.
	h.reloadShinyOverrides()
	return h
}

// reloadLangs refreshes the cached public language switcher list from the
// locales table. Falls back to the historical es/fr/de set when the table is
// missing or empty so a pre-migration boot keeps the switcher intact.
func (h *Handlers) reloadLangs() {
	langs := []string{"en"}
	rows, err := h.db.Query(`SELECT code FROM locales WHERE enabled = 1 ORDER BY code`)
	if err != nil {
		// Pre-migration boot: keep the historical switcher working.
		log.Printf("reloadLangs: %v (falling back to es/fr/de)", err)
		langs = append(langs, "de", "es", "fr")
	} else {
		defer rows.Close()
		for rows.Next() {
			var code string
			if rows.Scan(&code) == nil && i18n.IsSupported(code) {
				langs = append(langs, code)
			}
		}
	}
	h.langMu.Lock()
	h.enabledLangs = langs
	h.langMu.Unlock()
}

func (h *Handlers) publicLangs() []string {
	h.langMu.RLock()
	defer h.langMu.RUnlock()
	return append([]string(nil), h.enabledLangs...)
}

func (h *Handlers) langEnabled(code string) bool {
	h.langMu.RLock()
	defer h.langMu.RUnlock()
	return slices.Contains(h.enabledLangs, code)
}

// computeAssetVersion hashes the contents of the static JS/CSS bundles so the value changes
// only when a bundle changes. It is appended to every /static asset URL as ?v=... to bust
// browser caches on each deploy. Falls back to a timestamp if no asset files can be read.
func computeAssetVersion() string {
	hasher := sha256.New()
	count := 0
	for _, dir := range []string{"static/css", "static/js"} {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()
			if _, err := io.Copy(hasher, f); err == nil {
				count++
			}
			return nil
		})
	}
	if count == 0 {
		return strconv.FormatInt(time.Now().Unix(), 10)
	}
	return hex.EncodeToString(hasher.Sum(nil))[:8]
}

var tmplFuncs = template.FuncMap{
	"divf":  func(a, b int) float64 { return float64(a) / float64(b) },
	"upper": strings.ToUpper,
	"T":     func(key string) string { return key }, // replaced per-request via Clone+Funcs
	"pathActive": func(currentPath, prefix string) bool {
		return currentPath == prefix || strings.HasPrefix(currentPath, prefix+"/")
	},
}

func (h *Handlers) loadTemplates() {
	h.tmpl = make(map[string]*template.Template)
	pages := []string{
		"home", "raids", "dps", "pvp", "events", "iv", "box", "credits", "maintenance",
		"login", "register", "shinies", "admin", "settings", "trainers", "store",
		"translate", "raidfinder", "trainer", "social", "notifications", "reports",
		"forgot_password", "reset_password", "verify_email", "privacy",
	}
	for _, page := range pages {
		t, err := template.New("base.html").Funcs(tmplFuncs).ParseFiles(
			filepath.Join("templates", "base.html"),
			filepath.Join("templates", page+".html"),
		)
		if err != nil {
			log.Fatalf("template %q: %v", page, err)
		}
		h.tmpl[page] = t
	}
}

// previewLang returns the locale a translator is previewing, or "" if no
// preview applies. The ?tl_preview= query param is honored on any request;
// the tl_preview cookie only when the request is rendered inside an iframe
// (Sec-Fetch-Dest), so in-iframe navigation keeps the preview locale without
// affecting the translator's own top-level browsing. Callers must verify the
// user IsTranslator() before honoring the result.
func previewLang(r *http.Request) string {
	lang := r.URL.Query().Get("tl_preview")
	if lang == "" && r.Header.Get("Sec-Fetch-Dest") == "iframe" {
		if c, err := r.Cookie("tl_preview"); err == nil {
			lang = c.Value
		}
	}
	if lang == "" || lang == "en" || !i18n.IsSupported(lang) {
		return ""
	}
	return lang
}

func (h *Handlers) pendingOverrides(userID uint, lang string) map[string]string {
	rows, err := h.db.Query(
		`SELECT t_key, new_text FROM translation_edits WHERE user_id = ? AND lang = ? AND status = 'pending'`,
		userID, lang,
	)
	if err != nil {
		log.Printf("pendingOverrides: %v", err)
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			out[k] = v
		}
	}
	return out
}

func (h *Handlers) detectLang(r *http.Request) string {
	if u := h.currentUser(r); u != nil && h.langEnabled(u.Lang) {
		return u.Lang
	}
	if c, err := r.Cookie("lang"); err == nil && h.langEnabled(c.Value) {
		return c.Value
	}
	return "en"
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t, ok := h.tmpl[page]
	if !ok {
		http.Error(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}

	m := h.maintenanceSettings()
	if !pageEnabled(page, m) {
		h.serveMaintenance(w, r, page, m)
		return
	}

	u := h.currentUser(r)
	if u != nil {
		h.db.Exec(`UPDATE users SET last_seen_at = NOW() WHERE id = ?`, u.ID)
	}

	lang := h.detectLang(r)
	var overrides map[string]string
	if u != nil && u.IsTranslator() {
		if pl := previewLang(r); pl != "" {
			lang = pl
			overrides = h.pendingOverrides(u.ID, pl)
		}
	}
	clone, err := t.Clone()
	if err != nil {
		log.Printf("render clone %q: %v", page, err)
		http.Error(w, h.t(r, "error.server"), http.StatusInternalServerError)
		return
	}
	clone.Funcs(template.FuncMap{"T": i18n.TFuncWithOverrides(lang, overrides)})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	pd := PageData{User: u, Data: data, CSRFToken: csrf.Token(r), StoreEnabled: h.storeEnabled(), Maintenance: m, Lang: lang, Langs: h.publicLangs(), AssetVersion: h.assetVersion, Path: r.URL.Path}
	if u != nil {
		pd.ReportCount, pd.ReportUnread = h.reportCounts(u.ID)
	}
	// Only the two pages that resolve costumes in the browser; it is a few KB, so there is no
	// reason to ship it with every page. "null" rather than empty on failure, so the injected
	// object is still valid JS and the client falls back to its compiled-in labels.
	if page == "shinies" || page == "trainer" {
		pd.CostumeLabels = template.JS("null")
		if b, err := costumes.LabelsJSON(); err == nil {
			pd.CostumeLabels = template.JS(scriptSafeJSON(b))
		} else {
			log.Printf("costumes: marshal labels for %s: %v", page, err)
		}
	}
	if err := clone.ExecuteTemplate(w, "base", pd); err != nil {
		log.Printf("render %q: %v", page, err)
	}
}

// scriptSafeJSON escapes the three characters that let a JSON blob break out of the
// <script> block it is inlined into.
//
// costumes.LabelsJSON reproduces labels.json BYTE FOR BYTE on purpose, because the
// GitHub sync PRs its output straight back into the file and any re-marshal would
// rewrite the whole thing (TestLabelsJSONMatchesTheFileVerbatim pins this). A
// side effect is that it does not HTML-escape, and the file's _comment blocks
// already contain literal "<p>" and "<CODE>". Nothing exploitable today, but a
// "</script>" reaching that file would end the block and everything after it would
// parse as markup. Escaping here rather than in LabelsJSON keeps the byte-stability
// guarantee intact.
//
// Safe as a blind replace: in well-formed JSON these three characters can only ever
// occur inside string literals, where \uXXXX is an equivalent encoding.
var scriptJSONEscaper = strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "&", `\u0026`)

func scriptSafeJSON(b []byte) string {
	return scriptJSONEscaper.Replace(string(b))
}

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "home", nil)
}

func (h *Handlers) Raids(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "raids", nil)
}

func (h *Handlers) DPS(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "dps", nil)
}

func (h *Handlers) PVP(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "pvp", nil)
}

func (h *Handlers) Events(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "events", nil)
}

func (h *Handlers) Credits(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "credits", nil)
}

// Privacy is a static document. It is public and unauthenticated on purpose:
// the Android app links to it from its own settings, and someone deciding
// whether to install must be able to read it without an account.
func (h *Handlers) Privacy(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "privacy", nil)
}

func (h *Handlers) APIData(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.AllData())
}

func (h *Handlers) APIRaids(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.Raids())
}

func (h *Handlers) APIMaxBattles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.MaxBattles())
}

func (h *Handlers) APIEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.Events())
}

// eventIDPattern is defined once, in events_ics.go, next to the other event id
// handling. It used to have a second copy here that omitted the underscore.
func (h *Handlers) APIEventDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !eventIDPattern.MatchString(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid event id"}`))
		return
	}
	detail, ok := h.store.EventDetail(id)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
		return
	}
	writeJSON(w, detail)
}

func (h *Handlers) APIPokemon(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.Pokemon())
}

func (h *Handlers) APIMoves(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.Moves())
}

func (h *Handlers) APIRefresh(w http.ResponseWriter, r *http.Request) {
	h.store.Refresh()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"refresh triggered"}`))
}

func staffBadge(username, role string) string {
	if auth.SuperadminUser != "" && username == auth.SuperadminUser {
		return "superadmin"
	}
	switch role {
	case "admin":
		return "admin"
	case "moderator":
		return "moderator"
	case "tester":
		return "tester"
	}
	return ""
}

func (h *Handlers) t(r *http.Request, key string) string {
	return i18n.TFunc(h.detectLang(r))(key)
}

func (h *Handlers) storeEnabled() bool {
	var v string
	h.db.QueryRow(`SELECT setting_value FROM site_settings WHERE setting_key = 'store_enabled'`).Scan(&v)
	return v == "1"
}

func (h *Handlers) maintenanceSettings() PageMaintenance {
	m := PageMaintenance{
		RaidsEnabled:            true,
		DPSEnabled:              true,
		PVPEnabled:              true,
		EventsEnabled:           true,
		IVEnabled:               true,
		TrainersEnabled:         true,
		TrainerDirectoryEnabled: true,
		RaidFinderEnabled:       true,
		ShiniesEnabled:          true,
		TranslatorAppsEnabled:   true,
	}
	rows, err := h.db.Query(`SELECT setting_key, setting_value FROM site_settings
		WHERE setting_key IN (
			'page_raids_enabled','page_dps_enabled','page_pvp_enabled','page_events_enabled',
			'page_iv_enabled','page_trainers_enabled','section_trainer_directory_enabled',
			'section_raid_finder_enabled','page_shinies_enabled',
			'section_translator_apps_enabled'
		)`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		enabled := v == "1"
		switch k {
		case "page_raids_enabled":
			m.RaidsEnabled = enabled
		case "page_dps_enabled":
			m.DPSEnabled = enabled
		case "page_pvp_enabled":
			m.PVPEnabled = enabled
		case "page_events_enabled":
			m.EventsEnabled = enabled
		case "page_iv_enabled":
			m.IVEnabled = enabled
		case "page_trainers_enabled":
			m.TrainersEnabled = enabled
		case "section_trainer_directory_enabled":
			m.TrainerDirectoryEnabled = enabled
		case "section_raid_finder_enabled":
			m.RaidFinderEnabled = enabled
		case "page_shinies_enabled":
			m.ShiniesEnabled = enabled
		case "section_translator_apps_enabled":
			m.TranslatorAppsEnabled = enabled
		}
	}
	return m
}

func pageEnabled(page string, m PageMaintenance) bool {
	switch page {
	case "raids":
		return m.RaidsEnabled
	case "dps":
		return m.DPSEnabled
	case "pvp":
		return m.PVPEnabled
	case "events":
		return m.EventsEnabled
	case "iv":
		return m.IVEnabled
	case "box":
		// The box rides the IV toggle rather than owning one. Without this the
		// nav link would vanish while the page itself carried on serving, so the
		// switch would look like it had worked and would not have.
		return m.IVEnabled
	case "trainers":
		return m.TrainersEnabled
	case "shinies":
		return m.ShiniesEnabled
	}
	return true
}

func (h *Handlers) serveMaintenance(w http.ResponseWriter, r *http.Request, page string, m PageMaintenance) {
	t, ok := h.tmpl["maintenance"]
	if !ok {
		http.Error(w, h.t(r, "error.maintenance"), http.StatusServiceUnavailable)
		return
	}
	lang := h.detectLang(r)
	clone, err := t.Clone()
	if err != nil {
		http.Error(w, h.t(r, "error.server"), http.StatusInternalServerError)
		return
	}
	clone.Funcs(template.FuncMap{"T": i18n.TFunc(lang)})
	type maintenanceData struct{ PageName string }
	u := h.currentUser(r)
	pd := PageData{
		User:         u,
		Data:         maintenanceData{PageName: page},
		CSRFToken:    csrf.Token(r),
		StoreEnabled: h.storeEnabled(),
		Maintenance:  m,
		Lang:         lang,
		AssetVersion: h.assetVersion,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusServiceUnavailable)
	if err := clone.ExecuteTemplate(w, "base", pd); err != nil {
		log.Printf("serveMaintenance %q: %v", page, err)
	}
}

func (h *Handlers) hasActivePurchase(userID uint, benefit string) bool {
	var count int
	// Super donators inherit queue priority perks.
	if benefit == "queue_priority" {
		h.db.QueryRow(`
			SELECT COUNT(*) FROM purchases p
			JOIN store_items i ON i.id = p.item_id
			WHERE p.user_id = ? AND p.status = 'completed'
			  AND i.benefit IN ('queue_priority','super_donator')
			  AND (p.expires_at IS NULL OR p.expires_at > NOW())`,
			userID).Scan(&count)
	} else {
		h.db.QueryRow(`
			SELECT COUNT(*) FROM purchases p
			JOIN store_items i ON i.id = p.item_id
			WHERE p.user_id = ? AND p.status = 'completed' AND i.benefit = ?
			  AND (p.expires_at IS NULL OR p.expires_at > NOW())`,
			userID, benefit).Scan(&count)
	}
	return count > 0
}

func (h *Handlers) superDonatorSet() map[uint]bool {
	rows, err := h.db.Query(`
		SELECT DISTINCT p.user_id FROM purchases p
		JOIN store_items i ON i.id = p.item_id
		WHERE i.benefit = 'super_donator' AND p.status = 'completed'
		  AND (p.expires_at IS NULL OR p.expires_at > NOW())`)
	set := map[uint]bool{}
	if err != nil {
		log.Printf("superDonatorSet: %v", err)
		return set
	}
	defer rows.Close()
	for rows.Next() {
		var id uint
		if rows.Scan(&id) == nil {
			set[id] = true
		}
	}
	return set
}

func writeJSON(w http.ResponseWriter, data json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if data == nil {
		w.Write([]byte("null"))
		return
	}
	w.Write(data)
}

// truncRunes cuts s to at most n characters, counting runes rather than bytes.
//
// Every column this guards is utf8mb4, and MySQL counts VARCHAR lengths in
// characters, so slicing bytes never protected against overflow the way it was
// meant to. It also breaks: a byte cut can land inside a multi-byte rune, MySQL
// rejects the invalid sequence with error 1366, the insert fails, and the
// handler answers 500. The reporter cannot retry past it, and it is invisible to
// anyone writing in English.
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// upperFirst capitalises the first character of s, counting runes rather than
// bytes.
//
// The shape this replaces, strings.ToUpper(s[:1]) + s[1:], splits one byte off
// the front. On a multi-byte first character that byte is not a character at
// all: ToUpper hands back a replacement rune, the remaining bytes stay orphaned,
// and the result is neither the input nor valid UTF-8. Everything fed through
// here happens to be ASCII today, which is exactly why it went unnoticed.
func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return strings.ToUpper(string(r)) + s[size:]
}
