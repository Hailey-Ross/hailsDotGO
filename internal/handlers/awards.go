package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
)

type awardEntry struct {
	ID           uint   `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Icon         string `json:"icon"`
	Color        string `json:"color"`
	Active       bool   `json:"active"`
	SortOrder    int    `json:"sort_order"`
	MinGrantRank int    `json:"min_grant_rank"`
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
		SELECT id, slug, name, description, icon, color, active, sort_order, min_grant_rank
		FROM awards WHERE active = 1 ORDER BY sort_order, name`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []awardEntry{}
	for rows.Next() {
		var a awardEntry
		if rows.Scan(&a.ID, &a.Slug, &a.Name, &a.Description, &a.Icon, &a.Color, &a.Active, &a.SortOrder, &a.MinGrantRank) == nil {
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

// APIUsersSearch autocompletes usernames for the award grant and report invite
// fields.
//
// The auth check is the handler's own, not the route's. On the web this sits
// behind RequireAuth, which answers a 303 to /login and would hand a Bearer
// client a login page to parse as JSON, so the mobile alias is registered
// bare and this check is what gates it. Do not remove it on the grounds that
// the web route already wraps it: this endpoint is a user enumeration oracle,
// and one of its two registrations has no wrapper at all.
func (h *Handlers) APIUsersSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUserAPI(w, r); !ok {
		return
	}
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

// userAwardGrantRank returns the effective grant rank for a user.
// Ranks: 0=User, 1=Trusted, 2=ContentCreator, 3=Translator, 4=Tester, 5=Moderator, 100=Admin.
// Checked top-down so multi-attribute users get their highest rank.
func userAwardGrantRank(u *auth.User) int {
	switch {
	case u.IsAdmin():
		return 100
	case u.Role == "moderator":
		return 5
	case u.IsTester():
		return 4
	case u.Translator: // raw field; IsTranslator() also matches superadmin which IsAdmin() already covers
		return 3
	case u.SpecialRank == "content_creator":
		return 2
	case u.SpecialRank == "trusted":
		return 1
	}
	return 0
}

func (h *Handlers) APIAwardGrant(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
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
	var minGrantRank int
	if err := h.db.QueryRow(`SELECT active, min_grant_rank FROM awards WHERE id = ?`, awardID).Scan(&active, &minGrantRank); err != nil || !active {
		writeJSONError(w, h.t(r, "error.award_not_found"), http.StatusNotFound)
		return
	}

	if !u.IsMod() {
		if !h.settingBool("awards_community_grants_enabled") {
			writeJSONError(w, h.t(r, "error.award_community_disabled"), http.StatusForbidden)
			return
		}
		if userAwardGrantRank(u) < minGrantRank {
			writeJSONError(w, h.t(r, "error.award_rank_too_low"), http.StatusForbidden)
			return
		}
		// awards_grant_min_trust was seeded in the schema from the start and read by
		// nothing, so a setting that looks like it gates community award granting was
		// doing nothing at all. Worse than having no control, because it invites the
		// belief that one is in place.
		//
		// Measured on effective trust, matching every other trust gate in the app, so
		// the store bonus and the same decay rules apply here too.
		if minTrust := h.settingFloat("awards_grant_min_trust", 50); minTrust > 0 {
			if h.effectiveTrust(u.ID) < minTrust {
				writeJSONError(w, h.t(r, "error.award_trust_too_low"), http.StatusForbidden)
				return
			}
		}
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
