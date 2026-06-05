package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const raidPostTTL = 2 * time.Hour
const raidRateWindow = 24 * time.Hour
const raidLeaveCooldownTrigger = 4
const raidLeaveCooldownDuration = 10 * time.Minute
const raidLeaveWindow = 1 * time.Hour

type joinerInfo struct {
	ID          uint64 `json:"id"`
	Username    string `json:"username"`
	StaffBadge  string `json:"staff_badge,omitempty"`
	Confirmed   bool   `json:"confirmed"`
	HostInvited bool   `json:"host_invited"`
}

type raidPost struct {
	ID             uint64       `json:"id"`
	Username       string       `json:"username"`
	StaffBadge     string       `json:"staff_badge,omitempty"`
	HostCode       string       `json:"host_code"`
	BossName       string       `json:"boss_name"`
	Note           string       `json:"note"`
	PlayersNeeded  uint8        `json:"players_needed"`
	WeatherBoosted bool         `json:"weather_boosted"`
	CreatedAt      time.Time    `json:"created_at"`
	ExpiresAt      time.Time    `json:"expires_at"`
	Expired        bool         `json:"expired"`
	IsOwn          bool         `json:"is_own"`
	HasJoined      bool         `json:"has_joined"`
	JoinConfirmed  bool         `json:"join_confirmed"`
	HostInvited    bool         `json:"host_invited"`
	JoinCount      int          `json:"join_count"`
	Joiners        []joinerInfo `json:"joiners"`
	HostRating     float64      `json:"host_rating"`
	RatingCount    int          `json:"rating_count"`
	MyRating       int          `json:"my_rating"`
}

// ── List ──────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsList(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)

	var userID uint = 0
	loggedIn := 0
	if u != nil {
		userID = u.ID
		loggedIn = 1
	}

	// Active posts + recently-expired posts the user can still rate
	rows, err := h.db.Query(`
		SELECT rp.id, u.username, u.role,
		       CASE WHEN ? = 1 THEN COALESCE(u.trainer_code,'') ELSE '' END,
		       rp.boss_name, rp.note, rp.players_needed, rp.weather_boosted,
		       rp.created_at, rp.expires_at, rp.user_id,
		       CASE WHEN rp.expires_at <= NOW() THEN 1 ELSE 0 END AS expired,
		       (SELECT COUNT(*) FROM raid_joins rj WHERE rj.post_id = rp.id) AS join_count,
		       COALESCE((SELECT rj.confirmed    FROM raid_joins rj WHERE rj.post_id = rp.id AND rj.joiner_id = ? LIMIT 1), 0),
		       COALESCE((SELECT rj.host_invited FROM raid_joins rj WHERE rj.post_id = rp.id AND rj.joiner_id = ? LIMIT 1), 0),
		       CASE WHEN EXISTS(SELECT 1 FROM raid_joins rj WHERE rj.post_id = rp.id AND rj.joiner_id = ?) THEN 1 ELSE 0 END,
		       COALESCE((SELECT score FROM raid_ratings WHERE post_id = rp.id AND rater_id = ? LIMIT 1), 0),
		       COALESCE((SELECT AVG(score) FROM raid_ratings WHERE rated_id = u.id), 0),
		       COALESCE((SELECT COUNT(*) FROM raid_ratings WHERE rated_id = u.id), 0)
		FROM raid_posts rp
		JOIN users u ON rp.user_id = u.id
		WHERE rp.expires_at > NOW()
		   OR (
		     rp.expires_at <= NOW()
		     AND rp.expires_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)
		     AND ? = 1
		     AND NOT EXISTS(SELECT 1 FROM raid_ratings WHERE post_id = rp.id AND rater_id = ?)
		     AND (
		       EXISTS(SELECT 1 FROM raid_joins rj WHERE rj.post_id = rp.id AND rj.joiner_id = ? AND rj.confirmed = 1)
		       OR rp.user_id = ?
		     )
		   )
		ORDER BY expired ASC, rp.created_at DESC`,
		loggedIn,
		userID, userID, userID, userID,
		loggedIn, userID, userID, userID,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := []raidPost{}
	for rows.Next() {
		var p raidPost
		var ownerID uint
		var hostRole string
		var joinConfirmed, hostInvited, hasJoined, weatherBoosted int
		if err := rows.Scan(
			&p.ID, &p.Username, &hostRole, &p.HostCode,
			&p.BossName, &p.Note, &p.PlayersNeeded, &weatherBoosted,
			&p.CreatedAt, &p.ExpiresAt, &ownerID, &p.Expired,
			&p.JoinCount, &joinConfirmed, &hostInvited, &hasJoined,
			&p.MyRating, &p.HostRating, &p.RatingCount,
		); err != nil {
			continue
		}
		p.StaffBadge = staffBadge(p.Username, hostRole)
		p.IsOwn = u != nil && u.ID == ownerID
		p.HasJoined = hasJoined > 0
		p.JoinConfirmed = joinConfirmed > 0
		p.HostInvited = hostInvited > 0
		p.WeatherBoosted = weatherBoosted > 0
		p.Joiners = []joinerInfo{}

		if p.IsOwn {
			jrows, err := h.db.Query(`
				SELECT rj.joiner_id, u.username, u.role, rj.confirmed, rj.host_invited
				FROM raid_joins rj JOIN users u ON rj.joiner_id = u.id
				WHERE rj.post_id = ? ORDER BY rj.joined_at ASC`, p.ID)
			if err == nil {
				for jrows.Next() {
					var ji joinerInfo
					var jRole string
					var conf, inv int
					if jrows.Scan(&ji.ID, &ji.Username, &jRole, &conf, &inv) == nil {
						ji.StaffBadge = staffBadge(ji.Username, jRole)
						ji.Confirmed = conf > 0
						ji.HostInvited = inv > 0
						p.Joiners = append(p.Joiners, ji)
					}
				}
				jrows.Close()
			}
		}

		posts = append(posts, p)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(posts)
}

// ── Create ────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsCreate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		BossName       string `json:"boss_name"`
		Note           string `json:"note"`
		PlayersNeeded  uint8  `json:"players_needed"`
		WeatherBoosted bool   `json:"weather_boosted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	bossName := strings.TrimSpace(body.BossName)
	note := strings.TrimSpace(body.Note)

	if bossName == "" {
		writeJSONError(w, "boss_name required", http.StatusBadRequest)
		return
	}
	if len(bossName) > 64 {
		writeJSONError(w, "boss name too long", http.StatusBadRequest)
		return
	}
	if len(note) > 160 {
		writeJSONError(w, "note too long (max 160 characters)", http.StatusBadRequest)
		return
	}
	if body.PlayersNeeded > 20 {
		writeJSONError(w, "players_needed max is 20", http.StatusBadRequest)
		return
	}

	var raidBanned bool
	h.db.QueryRow(`SELECT raid_banned FROM users WHERE id = ?`, u.ID).Scan(&raidBanned)
	if raidBanned {
		writeJSONError(w, "you are banned from hosting or joining raids", http.StatusForbidden)
		return
	}

	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM raid_posts WHERE user_id = ? AND expires_at > NOW()`, u.ID).Scan(&count)
	if count >= 1 {
		writeJSONError(w, "you already have an active post; delete it before posting again", http.StatusConflict)
		return
	}

	weatherBoostedInt := 0
	if body.WeatherBoosted {
		weatherBoostedInt = 1
	}
	expires := time.Now().Add(raidPostTTL)
	result, err := h.db.Exec(`
		INSERT INTO raid_posts (user_id, boss_name, note, players_needed, weather_boosted, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, bossName, note, body.PlayersNeeded, weatherBoostedInt, expires,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	id, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

// ── Delete ────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsDelete(w http.ResponseWriter, r *http.Request) {
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

	var rowsAffected int64
	if u.IsMod() {
		res, e := h.db.Exec(`DELETE FROM raid_posts WHERE id = ?`, id)
		if e != nil {
			writeJSONError(w, "db error", http.StatusInternalServerError)
			return
		}
		rowsAffected, _ = res.RowsAffected()
	} else {
		res, e := h.db.Exec(`DELETE FROM raid_posts WHERE id = ? AND user_id = ?`, id, u.ID)
		if e != nil {
			writeJSONError(w, "db error", http.StatusInternalServerError)
			return
		}
		rowsAffected, _ = res.RowsAffected()
	}

	if rowsAffected == 0 {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Join ──────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsJoin(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var raidBanned bool
	h.db.QueryRow(`SELECT raid_banned FROM users WHERE id = ?`, u.ID).Scan(&raidBanned)
	if raidBanned {
		writeJSONError(w, "you are banned from hosting or joining raids", http.StatusForbidden)
		return
	}

	// Check cooldown
	var cooldownUntil time.Time
	if err := h.db.QueryRow(
		`SELECT until FROM raid_join_cooldowns WHERE user_id = ? AND until > NOW()`, u.ID,
	).Scan(&cooldownUntil); err == nil {
		remaining := time.Until(cooldownUntil).Round(time.Second)
		writeJSONError(w, fmt.Sprintf("join cooldown active for another %s", remaining), http.StatusTooManyRequests)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var ownerID uint
	if err := h.db.QueryRow(
		`SELECT user_id FROM raid_posts WHERE id = ? AND expires_at > NOW()`, id,
	).Scan(&ownerID); err != nil {
		writeJSONError(w, "post not found or expired", http.StatusNotFound)
		return
	}
	if ownerID == u.ID {
		writeJSONError(w, "cannot join your own post", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(
		`INSERT IGNORE INTO raid_joins (post_id, joiner_id) VALUES (?, ?)`, id, u.ID,
	); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Leave ─────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsLeave(w http.ResponseWriter, r *http.Request) {
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

	res, err := h.db.Exec(`DELETE FROM raid_joins WHERE post_id = ? AND joiner_id = ?`, id, u.ID)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
		return
	}

	// Log the leave and check for cooldown trigger
	h.db.Exec(`INSERT INTO raid_leave_log (user_id) VALUES (?)`, u.ID)

	var leaveCount int
	h.db.QueryRow(
		`SELECT COUNT(*) FROM raid_leave_log WHERE user_id = ? AND left_at > DATE_SUB(NOW(), INTERVAL 1 HOUR)`,
		u.ID,
	).Scan(&leaveCount)

	if leaveCount >= raidLeaveCooldownTrigger {
		until := time.Now().Add(raidLeaveCooldownDuration)
		h.db.Exec(`
			INSERT INTO raid_join_cooldowns (user_id, until) VALUES (?, ?)
			ON DUPLICATE KEY UPDATE until = ?`, u.ID, until, until)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Confirm (joiner confirms friend request sent) ─────────────

func (h *Handlers) APIRaidPostsConfirm(w http.ResponseWriter, r *http.Request) {
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
		`UPDATE raid_joins SET confirmed = 1 WHERE post_id = ? AND joiner_id = ?`, id, u.ID)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "not joined", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Mark invited (host confirms they sent an in-game invite) ──

func (h *Handlers) APIRaidPostsMarkInvited(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	postID, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		JoinerID uint64 `json:"joiner_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	var ownerID uint
	if err := h.db.QueryRow(`SELECT user_id FROM raid_posts WHERE id = ?`, postID).Scan(&ownerID); err != nil {
		writeJSONError(w, "post not found", http.StatusNotFound)
		return
	}
	if uint(ownerID) != u.ID {
		writeJSONError(w, "not your post", http.StatusForbidden)
		return
	}

	h.db.Exec(`UPDATE raid_joins SET host_invited = 1 WHERE post_id = ? AND joiner_id = ?`, postID, body.JoinerID)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Rate ──────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsRate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	postID, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Score   int  `json:"score"`
		RatedID uint `json:"rated_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Score < 1 || body.Score > 5 {
		writeJSONError(w, "score must be 1 to 5", http.StatusBadRequest)
		return
	}
	if body.RatedID == u.ID {
		writeJSONError(w, "cannot rate yourself", http.StatusBadRequest)
		return
	}

	// Must be a confirmed joiner or the post owner rating a confirmed joiner
	var ownerID uint
	if err := h.db.QueryRow(`SELECT user_id FROM raid_posts WHERE id = ?`, postID).Scan(&ownerID); err != nil {
		writeJSONError(w, "post not found", http.StatusNotFound)
		return
	}

	if uint(ownerID) == u.ID {
		// Host rating a joiner: joiner must be confirmed
		var cnt int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_joins WHERE post_id = ? AND joiner_id = ? AND confirmed = 1`,
			postID, body.RatedID).Scan(&cnt)
		if cnt == 0 {
			writeJSONError(w, "that joiner has not confirmed", http.StatusForbidden)
			return
		}
	} else {
		// Joiner rating the host: must be a confirmed joiner
		var cnt int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_joins WHERE post_id = ? AND joiner_id = ? AND confirmed = 1`,
			postID, u.ID).Scan(&cnt)
		if cnt == 0 {
			writeJSONError(w, "must confirm your friend request before rating", http.StatusForbidden)
			return
		}
		if body.RatedID != uint(ownerID) {
			writeJSONError(w, "you can only rate the host", http.StatusForbidden)
			return
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO raid_ratings (post_id, rater_id, rated_id, score) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE score = ?`,
		postID, u.ID, body.RatedID, body.Score, body.Score)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
