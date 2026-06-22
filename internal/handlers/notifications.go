package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

type friendRaidNotif struct {
	LobbyID         uint   `json:"lobby_id"`
	BossName        string `json:"boss_name"`
	BossTier        uint8  `json:"boss_tier"`
	State           string `json:"state"`
	HostUsername    string `json:"host_username"`
	HostTrainerName string `json:"host_trainer_name"`
	CreatedAt       string `json:"created_at"`
}

func (h *Handlers) NotificationsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "notifications", nil)
}

func (h *Handlers) APIGetNotifications(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)

	rows, err := h.db.Query(`
		SELECT rl.id, rl.boss_name, rl.boss_tier, rl.state, rl.created_at,
		       u.username, COALESCE(u.trainer_name, '')
		FROM raid_lobbies rl
		JOIN users u ON u.id = rl.host_id
		WHERE rl.host_id IN (
			SELECT friend_id FROM user_friends WHERE user_id = ?
		)
		AND rl.state IN ('open', 'full', 'raiding')
		ORDER BY rl.created_at DESC`, u.ID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	notifs := []friendRaidNotif{}
	for rows.Next() {
		var n friendRaidNotif
		var createdAt time.Time
		if rows.Scan(&n.LobbyID, &n.BossName, &n.BossTier, &n.State, &createdAt, &n.HostUsername, &n.HostTrainerName) == nil {
			n.CreatedAt = createdAt.UTC().Format(time.RFC3339)
			notifs = append(notifs, n)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notifs)
}
