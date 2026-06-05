package handlers

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"path/filepath"

	"pogo.hails.cc/internal/pogodata"
)

type Handlers struct {
	store *pogodata.Store
	tmpl  map[string]*template.Template
}

func New(store *pogodata.Store) *Handlers {
	h := &Handlers{store: store}
	h.loadTemplates()
	return h
}

func (h *Handlers) loadTemplates() {
	h.tmpl = make(map[string]*template.Template)
	for _, page := range []string{"home", "raids", "dps", "pvp", "events", "changelog"} {
		t, err := template.ParseFiles(
			filepath.Join("templates", "base.html"),
			filepath.Join("templates", page+".html"),
		)
		if err != nil {
			log.Fatalf("handlers: template %q: %v", page, err)
		}
		h.tmpl[page] = t
	}
}

func (h *Handlers) render(w http.ResponseWriter, page string, data any) {
	t, ok := h.tmpl[page]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("handlers: render %q: %v", page, err)
	}
}

// Page handlers

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, "home", nil)
}

func (h *Handlers) Raids(w http.ResponseWriter, r *http.Request) {
	h.render(w, "raids", nil)
}

func (h *Handlers) DPS(w http.ResponseWriter, r *http.Request) {
	h.render(w, "dps", nil)
}

func (h *Handlers) PVP(w http.ResponseWriter, r *http.Request) {
	h.render(w, "pvp", nil)
}

func (h *Handlers) Events(w http.ResponseWriter, r *http.Request) {
	h.render(w, "events", nil)
}

func (h *Handlers) Changelog(w http.ResponseWriter, r *http.Request) {
	h.render(w, "changelog", nil)
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
