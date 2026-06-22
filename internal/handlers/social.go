package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type friendEntry struct {
	Username    string
	TrainerName string
	Avatar      string
	AvatarURL   string
}

type friendsPageData struct {
	Friends []friendEntry
}

func (h *Handlers) FriendsPage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)

	trainerClasses := h.store.TrainerClasses()
	avatarURLBySlug := make(map[string]string, len(trainerClasses))
	for _, tc := range trainerClasses {
		avatarURLBySlug[tc.Slug] = tc.SpriteURL
	}

	var friends []friendEntry
	rows, err := h.db.Query(`
		SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
		FROM user_friends uf
		JOIN users u ON u.id = uf.friend_id
		WHERE uf.user_id = ?
		ORDER BY uf.created_at DESC`, u.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var fe friendEntry
			if rows.Scan(&fe.Username, &fe.TrainerName, &fe.Avatar) == nil {
				fe.AvatarURL = avatarURLBySlug[fe.Avatar]
				friends = append(friends, fe)
			}
		}
	}
	if friends == nil {
		friends = []friendEntry{}
	}

	h.render(w, r, "friends", friendsPageData{Friends: friends})
}

func (h *Handlers) APIGetSocialState(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"is_self":true,"is_friend":false,"is_blocked":false,"they_blocked_you":false}`))
		return
	}

	var isFriend, isBlocked, theyBlockedYou bool
	var count int

	h.db.QueryRow(`SELECT COUNT(*) FROM user_friends WHERE user_id = ? AND friend_id = ?`, u.ID, targetID).Scan(&count)
	isFriend = count > 0

	count = 0
	h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, u.ID, targetID).Scan(&count)
	isBlocked = count > 0

	count = 0
	h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, targetID, u.ID).Scan(&count)
	theyBlockedYou = count > 0

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"is_self":          false,
		"is_friend":        isFriend,
		"is_blocked":       isBlocked,
		"they_blocked_you": theyBlockedYou,
	})
}

func (h *Handlers) APIFriend(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		writeJSONError(w, "cannot friend yourself", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Check if they have blocked the viewer -- cannot friend someone who blocked you
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, targetID, u.ID).Scan(&count)
		if count > 0 {
			writeJSONError(w, "unable to add friend", http.StatusForbidden)
			return
		}
		h.db.Exec(`INSERT IGNORE INTO user_friends (user_id, friend_id) VALUES (?, ?)`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	case http.MethodDelete:
		h.db.Exec(`DELETE FROM user_friends WHERE user_id = ? AND friend_id = ?`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) APIBlock(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ?`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		writeJSONError(w, "cannot block yourself", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Remove friendship in both directions when blocking
		h.db.Exec(`DELETE FROM user_friends WHERE (user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)`,
			u.ID, targetID, targetID, u.ID)
		h.db.Exec(`INSERT IGNORE INTO user_blocks (user_id, blocked_id) VALUES (?, ?)`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	case http.MethodDelete:
		h.db.Exec(`DELETE FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
