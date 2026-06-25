package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type shinyRecord struct {
	ID        uint      `json:"id"`
	PokemonID string    `json:"pokemon_id"`
	Form      string    `json:"form"`
	Costume   string    `json:"costume"`
	EventTag  string    `json:"event_tag"`
	Method    string    `json:"method"`
	CaughtAt  time.Time `json:"caught_at"`
}

func (h *Handlers) ShiniesPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "shinies", nil)
}

func (h *Handlers) APIShiniesGet(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, pokemon_id, form, costume, event_tag, method, caught_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC`,
		u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []shinyRecord{}
	for rows.Next() {
		var s shinyRecord
		if err := rows.Scan(&s.ID, &s.PokemonID, &s.Form, &s.Costume, &s.EventTag, &s.Method, &s.CaughtAt); err != nil {
			continue
		}
		out = append(out, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) APIShiniesAdd(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body struct {
		PokemonID string `json:"pokemon_id"`
		Form      string `json:"form"`
		Costume   string `json:"costume"`
		EventTag  string `json:"event_tag"`
		Method    string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.PokemonID = strings.TrimSpace(body.PokemonID)
	if body.PokemonID == "" {
		writeJSONError(w, h.t(r, "error.shiny_pokemon_required"), http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(`
		INSERT INTO user_shinies (user_id, pokemon_id, form, costume, event_tag, method)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, body.PokemonID, body.Form, body.Costume, body.EventTag, body.Method,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	id, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "ok": true})
}

func (h *Handlers) APIShiniesUpdate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Form     string `json:"form"`
		Costume  string `json:"costume"`
		EventTag string `json:"event_tag"`
		Method   string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	_, err = h.db.Exec(
		`UPDATE user_shinies SET form = ?, costume = ?, event_tag = ?, method = ? WHERE id = ? AND user_id = ?`,
		body.Form, body.Costume, body.EventTag, body.Method, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIShiniesDelete(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM user_shinies WHERE id = ? AND user_id = ?`, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type publicShinyRecord struct {
	PokemonID string    `json:"pokemon_id"`
	Form      string    `json:"form"`
	Costume   string    `json:"costume"`
	EventTag  string    `json:"event_tag"`
	Method    string    `json:"method"`
	SpriteURL string    `json:"sprite_url"`
	CaughtAt  time.Time `json:"caught_at"`
}

func (h *Handlers) APIShiniesOfUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")

	var userID int
	var profilePublic, shiniesHidden int
	err := h.db.QueryRow(`
		SELECT id, COALESCE(profile_public,0), COALESCE(shinies_hidden,0)
		FROM users WHERE username = ? AND disabled = 0`, username,
	).Scan(&userID, &profilePublic, &shiniesHidden)
	if err != nil || profilePublic == 0 || shiniesHidden == 1 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	rows, err := h.db.Query(`
		SELECT pokemon_id, form, costume, event_tag, method, caught_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC`, userID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []publicShinyRecord{}
	for rows.Next() {
		var s publicShinyRecord
		if err := rows.Scan(&s.PokemonID, &s.Form, &s.Costume, &s.EventTag, &s.Method, &s.CaughtAt); err != nil {
			continue
		}
		if id := h.store.PokemonDexID(s.PokemonID); id != 0 {
			s.SpriteURL = pokemonSpriteURL(id, "shiny")
		}
		out = append(out, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
