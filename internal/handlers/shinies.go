package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"
)

type shinyRecord struct {
	ID        uint      `json:"id"`
	PokemonID string    `json:"pokemon_id"`
	Form      string    `json:"form"`
	Method    string    `json:"method"`
	CaughtAt  time.Time `json:"caught_at"`
}

func (h *Handlers) ShiniesPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "shinies", nil)
}

func (h *Handlers) APIShiniesGet(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, pokemon_id, form, method, caught_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC`,
		u.ID,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []shinyRecord{}
	for rows.Next() {
		var s shinyRecord
		if err := rows.Scan(&s.ID, &s.PokemonID, &s.Form, &s.Method, &s.CaughtAt); err != nil {
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
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		PokemonID string `json:"pokemon_id"`
		Form      string `json:"form"`
		Method    string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	body.PokemonID = strings.TrimSpace(body.PokemonID)
	if body.PokemonID == "" {
		writeJSONError(w, "pokemon_id required", http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(`
		INSERT INTO user_shinies (user_id, pokemon_id, form, method)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE caught_at = caught_at`,
		u.ID, body.PokemonID, body.Form, body.Method,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	id, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "ok": true})
}

func (h *Handlers) APIShiniesUpdate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Form   string `json:"form"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	_, err = h.db.Exec(
		`UPDATE user_shinies SET form = ?, method = ? WHERE id = ? AND user_id = ?`,
		body.Form, body.Method, id, u.ID,
	)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			writeJSONError(w, "already caught that form", http.StatusConflict)
			return
		}
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIShiniesDelete(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM user_shinies WHERE id = ? AND user_id = ?`, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
