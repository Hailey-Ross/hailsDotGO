package handlers

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"path/filepath"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/pogodata"
)

type Handlers struct {
	store *pogodata.Store
	tmpl  map[string]*template.Template
	db    *sql.DB
}

// PageData is the root template data passed to every page.
// User is nil for unauthenticated requests.
type PageData struct {
	User *auth.User
	Data any
}

func New(store *pogodata.Store, db *sql.DB) *Handlers {
	h := &Handlers{store: store, db: db}
	h.loadTemplates()
	return h
}

func (h *Handlers) loadTemplates() {
	h.tmpl = make(map[string]*template.Template)
	pages := []string{
		"home", "raids", "dps", "pvp", "events", "changelog",
		"login", "register", "shinies", "admin",
	}
	for _, page := range pages {
		t, err := template.ParseFiles(
			filepath.Join("templates", "base.html"),
			filepath.Join("templates", page+".html"),
		)
		if err != nil {
			log.Fatalf("template %q: %v", page, err)
		}
		h.tmpl[page] = t
	}
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t, ok := h.tmpl[page]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", PageData{User: h.currentUser(r), Data: data}); err != nil {
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

func (h *Handlers) Changelog(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "changelog", nil)
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

func writeJSON(w http.ResponseWriter, data json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if data == nil {
		w.Write([]byte("null"))
		return
	}
	w.Write(data)
}
