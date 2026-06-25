package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (h *Handlers) AdminAwardsList(w http.ResponseWriter, r *http.Request) {
	awards := []awardEntry{}
	rows, err := h.db.Query(`SELECT id, slug, name, description, icon, color, active, sort_order, min_grant_rank FROM awards ORDER BY sort_order, name`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var a awardEntry
		if rows.Scan(&a.ID, &a.Slug, &a.Name, &a.Description, &a.Icon, &a.Color, &a.Active, &a.SortOrder, &a.MinGrantRank) == nil {
			awards = append(awards, a)
		}
	}
	rows.Close()

	type grantRow struct {
		ID        uint   `json:"id"`
		AwardName string `json:"award_name"`
		Icon      string `json:"icon"`
		Recipient string `json:"recipient"`
		Granter   string `json:"granter"`
		Note      string `json:"note"`
		CreatedAt string `json:"created_at"`
	}
	grants := []grantRow{}
	grows, err := h.db.Query(`
		SELECT g.id, a.name, a.icon, ru.username, COALESCE(gu.username, ''), g.note, g.created_at
		FROM award_grants g
		JOIN awards a ON a.id = g.award_id
		JOIN users ru ON ru.id = g.recipient_id
		LEFT JOIN users gu ON gu.id = g.granter_id
		ORDER BY g.created_at DESC LIMIT 100`)
	if err == nil {
		for grows.Next() {
			var g grantRow
			if grows.Scan(&g.ID, &g.AwardName, &g.Icon, &g.Recipient, &g.Granter, &g.Note, &g.CreatedAt) == nil {
				grants = append(grants, g)
			}
		}
		grows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"awards": awards, "grants": grants})
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (h *Handlers) AdminAwardCreate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	var body struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Icon         string `json:"icon"`
		Color        string `json:"color"`
		SortOrder    int    `json:"sort_order"`
		MinGrantRank int    `json:"min_grant_rank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 64 {
		writeJSONError(w, h.t(r, "error.rv2_award_name"), http.StatusBadRequest)
		return
	}
	slug := slugify(name)
	if slug == "" {
		writeJSONError(w, h.t(r, "error.rv2_award_name"), http.StatusBadRequest)
		return
	}
	icon := strings.TrimSpace(body.Icon)
	if icon == "" {
		icon = "🏆"
	}
	color := strings.TrimSpace(body.Color)
	if color == "" {
		color = "#f0b429"
	}
	if body.MinGrantRank < 0 || body.MinGrantRank > 5 {
		body.MinGrantRank = 0
	}
	res, err := h.db.Exec(`
		INSERT INTO awards (slug, name, description, icon, color, sort_order, min_grant_rank, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, strings.TrimSpace(body.Description), icon, color, body.SortOrder, body.MinGrantRank, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.rv2_award_exists"), http.StatusConflict)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

func (h *Handlers) AdminAwardUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Icon         string `json:"icon"`
		Color        string `json:"color"`
		Active       bool   `json:"active"`
		SortOrder    int    `json:"sort_order"`
		MinGrantRank int    `json:"min_grant_rank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 64 {
		writeJSONError(w, h.t(r, "error.rv2_award_name"), http.StatusBadRequest)
		return
	}
	if body.MinGrantRank < 0 || body.MinGrantRank > 5 {
		body.MinGrantRank = 0
	}
	if _, err := h.db.Exec(`
		UPDATE awards SET name = ?, description = ?, icon = ?, color = ?, active = ?, sort_order = ?, min_grant_rank = ?
		WHERE id = ?`,
		name, strings.TrimSpace(body.Description), strings.TrimSpace(body.Icon),
		strings.TrimSpace(body.Color), body.Active, body.SortOrder, body.MinGrantRank, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminAwardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM awards WHERE id = ?`, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminAwardGrantDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM award_grants WHERE id = ?`, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTrustEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var trustScore float64
	h.db.QueryRow(`SELECT COALESCE(trust_score, 0) FROM users WHERE id = ?`, id).Scan(&trustScore)
	effective := h.effectiveTrust(uint(id))

	type trustEventRow struct {
		ID           uint    `json:"id"`
		EventType    string  `json:"event_type"`
		Actor        string  `json:"actor"`
		LobbyID      uint64  `json:"lobby_id"`
		RawDelta     float64 `json:"raw_delta"`
		Weight       float64 `json:"weight"`
		AppliedDelta float64 `json:"applied_delta"`
		Note         string  `json:"note"`
		CreatedAt    string  `json:"created_at"`
	}
	events := []trustEventRow{}
	rows, err := h.db.Query(`
		SELECT e.id, e.event_type, COALESCE(u.username, 'system'), COALESCE(e.lobby_id, 0),
		       e.raw_delta, e.weight, e.applied_delta, e.note, e.created_at
		FROM trust_events e
		LEFT JOIN users u ON u.id = e.actor_id
		WHERE e.user_id = ?
		ORDER BY e.created_at DESC LIMIT 100`, id)
	if err == nil {
		for rows.Next() {
			var e trustEventRow
			if rows.Scan(&e.ID, &e.EventType, &e.Actor, &e.LobbyID, &e.RawDelta, &e.Weight, &e.AppliedDelta, &e.Note, &e.CreatedAt) == nil {
				events = append(events, e)
			}
		}
		rows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"trust_score": trustScore,
		"effective":   effective,
		"tier":        h.trustTierFor(uint(id)),
		"events":      events,
	})
}

// AdminTrustAdjust inserts an auditable staff_adjust event instead of
// editing the cached column directly.
func (h *Handlers) AdminTrustAdjust(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Delta float64 `json:"delta"`
		Note  string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.Delta < -100 || body.Delta > 100 || body.Delta == 0 {
		writeJSONError(w, h.t(r, "error.rv2_delta_range"), http.StatusBadRequest)
		return
	}
	var exists int
	if h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&exists); exists == 0 {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	// staff_adjust bypasses the per-event clamp: raw delta with weight 1.0,
	// inserted directly so admins can apply corrections of any size.
	if _, err := h.db.Exec(`
		INSERT INTO trust_events (user_id, actor_id, event_type, raw_delta, weight, applied_delta, note)
		VALUES (?, ?, 'staff_adjust', ?, 1.0, ?, ?)`,
		id, u.ID, body.Delta, body.Delta, strings.TrimSpace(body.Note)); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	h.db.Exec(`UPDATE users SET trust_score = trust_score + ? WHERE id = ?`, body.Delta, id)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTrustRecompute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	h.recomputeTrustScore(uint(id))
	var score float64
	h.db.QueryRow(`SELECT COALESCE(trust_score, 0) FROM users WHERE id = ?`, id).Scan(&score)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "trust_score": score})
}

func (h *Handlers) AdminRaidLobbiesList(w http.ResponseWriter, r *http.Request) {
	type lobbyRow struct {
		ID        uint64 `json:"id"`
		Host      string `json:"host"`
		BossName  string `json:"boss_name"`
		BossTier  uint8  `json:"boss_tier"`
		IsCustom  bool   `json:"is_custom"`
		State     string `json:"state"`
		Members   int    `json:"members"`
		Max       int    `json:"max_members"`
		CreatedAt string `json:"created_at"`
	}
	out := []lobbyRow{}
	rows, err := h.db.Query(`
		SELECT l.id, u.username, l.boss_name, l.boss_tier, l.is_custom, l.state, l.max_members, l.created_at,
		       (SELECT COUNT(*) FROM raid_lobby_members lm WHERE lm.lobby_id = l.id AND lm.state IN ('matched','confirmed'))
		FROM raid_lobbies l JOIN users u ON u.id = l.host_id
		WHERE l.state IN ('open','full','raiding') AND l.expires_at > NOW()
		ORDER BY l.created_at DESC`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var l lobbyRow
		if rows.Scan(&l.ID, &l.Host, &l.BossName, &l.BossTier, &l.IsCustom, &l.State, &l.Max, &l.CreatedAt, &l.Members) == nil {
			out = append(out, l)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
