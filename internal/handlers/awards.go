package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Awards: Pokemon-themed community recognitions. Staff can always grant;
// community granting unlocks behind site_settings awards_community_grants_enabled
// for users at or above awards_grant_min_trust effective trust.

type awardEntry struct {
	ID          uint   `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	Active      bool   `json:"active"`
	SortOrder   int    `json:"sort_order"`
}

type awardGrantEntry struct {
	ID        uint   `json:"id"`
	AwardID   uint   `json:"award_id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	Granter   string `json:"granter"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

func (h *Handlers) settingBool(key string) bool {
	var v string
	h.db.QueryRow(`SELECT setting_value FROM site_settings WHERE setting_key = ?`, key).Scan(&v)
	return v == "1"
}

func (h *Handlers) APIAwardsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, slug, name, description, icon, color, active, sort_order
		FROM awards WHERE active = 1 ORDER BY sort_order, name`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []awardEntry{}
	for rows.Next() {
		var a awardEntry
		if rows.Scan(&a.ID, &a.Slug, &a.Name, &a.Description, &a.Icon, &a.Color, &a.Active, &a.SortOrder) == nil {
			out = append(out, a)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) APIAwardsOf(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var userID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, username).Scan(&userID); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	rows, err := h.db.Query(`
		SELECT g.id, a.id, a.name, a.icon, a.color, COALESCE(u.username, ''), g.note, g.created_at
		FROM award_grants g
		JOIN awards a ON a.id = g.award_id
		LEFT JOIN users u ON u.id = g.granter_id
		WHERE g.recipient_id = ?
		ORDER BY g.created_at DESC`, userID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []awardGrantEntry{}
	for rows.Next() {
		var g awardGrantEntry
		var createdAt string
		if rows.Scan(&g.ID, &g.AwardID, &g.Name, &g.Icon, &g.Color, &g.Granter, &g.Note, &createdAt) == nil {
			g.CreatedAt = createdAt
			out = append(out, g)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

type userSearchResult struct {
	Username    string `json:"username"`
	TrainerName string `json:"trainer_name"`
}

func (h *Handlers) APIUsersSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	like := q + "%"
	rows, err := h.db.Query(`
		SELECT username, COALESCE(trainer_name, '')
		FROM users
		WHERE disabled = 0
		  AND (username LIKE ? OR (trainer_name != '' AND trainer_name LIKE ?))
		ORDER BY username
		LIMIT 10`, like, like)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []userSearchResult{}
	for rows.Next() {
		var u userSearchResult
		if rows.Scan(&u.Username, &u.TrainerName) == nil {
			out = append(out, u)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) APIAwardGrant(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	if !u.IsMod() {
		if !h.settingBool("awards_community_grants_enabled") {
			writeJSONError(w, h.t(r, "error.award_community_disabled"), http.StatusForbidden)
			return
		}
		if h.effectiveTrust(u.ID) < h.settingFloat("awards_grant_min_trust", 50) {
			writeJSONError(w, h.t(r, "error.award_trust_too_low"), http.StatusForbidden)
			return
		}
	}

	awardID, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Username string `json:"username"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > 160 {
		writeJSONError(w, h.t(r, "error.award_note_length"), http.StatusBadRequest)
		return
	}

	var active bool
	if err := h.db.QueryRow(`SELECT active FROM awards WHERE id = ?`, awardID).Scan(&active); err != nil || !active {
		writeJSONError(w, h.t(r, "error.award_not_found"), http.StatusNotFound)
		return
	}
	var recipientID uint
	input := strings.TrimSpace(body.Username)
	if err := h.db.QueryRow(`
		SELECT id FROM users
		WHERE disabled = 0 AND (username = ? OR (trainer_name != '' AND trainer_name = ?))
		LIMIT 1`, input, input).Scan(&recipientID); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if recipientID == u.ID {
		writeJSONError(w, h.t(r, "error.award_self"), http.StatusBadRequest)
		return
	}

	res, err := h.db.Exec(`
		INSERT IGNORE INTO award_grants (award_id, recipient_id, granter_id, note)
		VALUES (?, ?, ?, ?)`, awardID, recipientID, u.ID, note)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.award_duplicate"), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
