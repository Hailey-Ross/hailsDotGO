package handlers

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gorilla/csrf"
	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/i18n"
	"pogo.hails.cc/internal/pogodata"
)

type Handlers struct {
	store     *pogodata.Store
	tmpl      map[string]*template.Template
	db        *sql.DB
	startTime time.Time
}

// PageData is the root template data passed to every page.
// User is nil for unauthenticated requests.
type PageData struct {
	User         *auth.User
	Data         any
	CSRFToken    string
	StoreEnabled bool
	Lang         string
}

func New(store *pogodata.Store, db *sql.DB) *Handlers {
	h := &Handlers{store: store, db: db, startTime: time.Now()}
	h.loadTemplates()
	return h
}

var tmplFuncs = template.FuncMap{
	"divf": func(a, b int) float64 { return float64(a) / float64(b) },
	"T":    func(key string) string { return key }, // replaced per-request via Clone+Funcs
}

func (h *Handlers) loadTemplates() {
	h.tmpl = make(map[string]*template.Template)
	pages := []string{
		"home", "raids", "dps", "pvp", "events", "credits",
		"login", "register", "shinies", "admin", "settings", "trainers", "store",
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

func (h *Handlers) detectLang(r *http.Request) string {
	if u := h.currentUser(r); u != nil && i18n.Supported[u.Lang] {
		return u.Lang
	}
	if c, err := r.Cookie("lang"); err == nil && i18n.Supported[c.Value] {
		return c.Value
	}
	return "en"
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t, ok := h.tmpl[page]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	u := h.currentUser(r)
	if u != nil {
		h.db.Exec(`UPDATE users SET last_seen_at = NOW() WHERE id = ?`, u.ID)
	}

	lang := h.detectLang(r)
	clone, err := t.Clone()
	if err != nil {
		log.Printf("render clone %q: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clone.Funcs(template.FuncMap{"T": i18n.TFunc(lang)})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pd := PageData{User: u, Data: data, CSRFToken: csrf.Token(r), StoreEnabled: h.storeEnabled(), Lang: lang}
	if err := clone.ExecuteTemplate(w, "base", pd); err != nil {
		log.Printf("render %q: %v", page, err)
	}
}

// Page handlers

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

// API handlers

func (h *Handlers) APIData(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.AllData())
}

func (h *Handlers) APIRaids(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.Raids())
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
