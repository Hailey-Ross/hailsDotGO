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

type socialPageData struct {
	TrainerName  string
	Username     string
	Friends      []friendEntry
	Followers    []friendEntry
	Following    []friendEntry
	IsOwnProfile bool
}

func (h *Handlers) SocialPage(w http.ResponseWriter, r *http.Request) {
	targetUsername := chi.URLParam(r, "username")

	trainerClasses := h.store.TrainerClasses()
	avatarURLBySlug := make(map[string]string, len(trainerClasses))
	for _, tc := range trainerClasses {
		avatarURLBySlug[tc.Slug] = tc.SpriteURL
	}

	var targetID uint
	var trainerName string
	if err := h.db.QueryRow(
		`SELECT id, COALESCE(trainer_name,'') FROM users WHERE username = ? AND disabled = 0`,
		targetUsername,
	).Scan(&targetID, &trainerName); err != nil {
		http.NotFound(w, r)
		return
	}

	u := h.currentUser(r)
	isOwn := u != nil && u.ID == targetID

	queryList := func(query string, arg uint) []friendEntry {
		rows, err := h.db.Query(query, arg)
		if err != nil {
			return []friendEntry{}
		}
		defer rows.Close()
		var out []friendEntry
		for rows.Next() {
			var fe friendEntry
			if rows.Scan(&fe.Username, &fe.TrainerName, &fe.Avatar) == nil {
				fe.AvatarURL = avatarURLBySlug[fe.Avatar]
				out = append(out, fe)
			}
		}
		if out == nil {
			return []friendEntry{}
		}
		return out
	}

	following := queryList(`
		SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
		FROM user_follows uf
		JOIN users u ON u.id = uf.friend_id
		WHERE uf.user_id = ?
		ORDER BY uf.created_at DESC`, targetID)

	followers := queryList(`
		SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
		FROM user_follows uf
		JOIN users u ON u.id = uf.user_id
		WHERE uf.friend_id = ?
		ORDER BY uf.created_at DESC`, targetID)

	friends := queryList(`
		SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
		FROM user_follows uf1
		JOIN user_follows uf2 ON uf2.user_id = uf1.friend_id AND uf2.friend_id = uf1.user_id
		JOIN users u ON u.id = uf1.friend_id
		WHERE uf1.user_id = ?
		ORDER BY uf1.created_at DESC`, targetID)

	h.render(w, r, "social", socialPageData{
		TrainerName:  trainerName,
		Username:     targetUsername,
		Friends:      friends,
		Followers:    followers,
		Following:    following,
		IsOwnProfile: isOwn,
	})
}

func (h *Handlers) FriendsRedirect(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserPage(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, "/social/"+u.Username, http.StatusMovedPermanently)
}

func (h *Handlers) APIGetSocialState(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"is_self":true,"is_following":false,"follows_me":false,"is_friend":false,"is_blocked":false,"they_blocked_you":false}`))
		return
	}

	var isFollowing, followsMe, isBlocked, theyBlockedYou bool
	var count int

	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, u.ID, targetID).Scan(&count)
	isFollowing = count > 0

	count = 0
	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, targetID, u.ID).Scan(&count)
	followsMe = count > 0

	count = 0
	h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, u.ID, targetID).Scan(&count)
	isBlocked = count > 0

	count = 0
	h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, targetID, u.ID).Scan(&count)
	theyBlockedYou = count > 0

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"is_self":          false,
		"is_following":     isFollowing,
		"follows_me":       followsMe,
		"is_friend":        isFollowing && followsMe,
		"is_blocked":       isBlocked,
		"they_blocked_you": theyBlockedYou,
	})
}

func (h *Handlers) APIFriend(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		writeJSONError(w, "cannot follow yourself", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Check if they have blocked the viewer -- cannot follow someone who blocked you
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, targetID, u.ID).Scan(&count)
		if count > 0 {
			writeJSONError(w, "unable to follow user", http.StatusForbidden)
			return
		}
		h.db.Exec(`INSERT IGNORE INTO user_follows (user_id, friend_id) VALUES (?, ?)`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	case http.MethodDelete:
		h.db.Exec(`DELETE FROM user_follows WHERE user_id = ? AND friend_id = ?`, u.ID, targetID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) APIBlock(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
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
		// Remove follows in both directions when blocking
		h.db.Exec(`DELETE FROM user_follows WHERE (user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)`,
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
