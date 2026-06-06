package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
const xpHostComplete = 20
const xpJoinComplete = 15

var raidRanks = []struct {
	xp          int
	label, class string
}{
	{200000, "League Master V", "league-master"},
	{150000, "League Master IV", "league-master"},
	{100000, "League Master III", "league-master"},
	{75000, "League Master II", "league-master"},
	{50000, "League Master I", "league-master"},
	{42000, "Champion V", "champion"},
	{34000, "Champion IV", "champion"},
	{26000, "Champion III", "champion"},
	{18000, "Champion II", "champion"},
	{10000, "Champion I", "champion"},
	{8400, "Elite Four V", "elite-four"},
	{6800, "Elite Four IV", "elite-four"},
	{5200, "Elite Four III", "elite-four"},
	{3600, "Elite Four II", "elite-four"},
	{2000, "Elite Four I", "elite-four"},
	{1700, "Gym Leader V", "gym-leader"},
	{1400, "Gym Leader IV", "gym-leader"},
	{1100, "Gym Leader III", "gym-leader"},
	{800, "Gym Leader II", "gym-leader"},
	{500, "Gym Leader I", "gym-leader"},
	{420, "Trainer V", "trainer"},
	{340, "Trainer IV", "trainer"},
	{260, "Trainer III", "trainer"},
	{180, "Trainer II", "trainer"},
	{100, "Trainer I", "trainer"},
	{80, "Youngster V", "youngster"},
	{60, "Youngster IV", "youngster"},
	{40, "Youngster III", "youngster"},
	{20, "Youngster II", "youngster"},
	{0, "Youngster I", "youngster"},
}

func raidRankLabel(xp int, role string) string {
	if role == "tester" {
		return "PKMN Scientist"
	}
	if role == "moderator" {
		return "PKMN Researcher"
	}
	if role == "admin" || role == "superadmin" {
		return "PKMN PROF"
	}
	for _, r := range raidRanks {
		if xp >= r.xp {
			return r.label
		}
	}
	return "Youngster I"
}

func raidRankClass(xp int, role string) string {
	if role == "tester" {
		return "pkmn-scientist"
	}
	if role == "moderator" {
		return "pkmn-researcher"
	}
	if role == "admin" || role == "superadmin" {
		return "pkmn-prof"
	}
	for _, r := range raidRanks {
		if xp >= r.xp {
			return r.class
		}
	}
	return "youngster"
}

type joinerInfo struct {
	ID           uint64 `json:"id"`
	Username     string `json:"username"`
	StaffBadge   string `json:"staff_badge,omitempty"`
	Status       string `json:"status"`
	RaidRank     string `json:"raid_rank"`
	Confirmed    bool   `json:"confirmed"`
	HostInvited  bool   `json:"host_invited"`
	SuperDonator bool   `json:"super_donator,omitempty"`
}

type raidPost struct {
	ID             uint64       `json:"id"`
	Username       string       `json:"username"`
	StaffBadge     string       `json:"staff_badge,omitempty"`
	HostCode       string       `json:"host_code"`
	BossName       string       `json:"boss_name"`
	BossTier       uint8        `json:"boss_tier"`
	Note           string       `json:"note"`
	PlayersNeeded  uint8        `json:"players_needed"`
	WeatherBoosted bool         `json:"weather_boosted"`
	CreatedAt      time.Time    `json:"created_at"`
	ExpiresAt      time.Time    `json:"expires_at"`
	Expired        bool         `json:"expired"`
	IsOwn          bool         `json:"is_own"`
	HasJoined      bool         `json:"has_joined"`
	JoinStatus     string       `json:"join_status"`
	JoinConfirmed  bool         `json:"join_confirmed"`
	HostInvited    bool         `json:"host_invited"`
	JoinCount      int          `json:"join_count"`
	Joiners        []joinerInfo `json:"joiners"`
	HostRating     float64      `json:"host_rating"`
	RatingDecayed  bool         `json:"rating_decayed"`
	RatingCount    int          `json:"rating_count"`
	HostRank       string       `json:"host_rank"`
	HostXP         int          `json:"host_xp"`
	MyRating       int          `json:"my_rating"`
	QueuePosition  int          `json:"queue_position,omitempty"`
	SuperDonator   bool         `json:"super_donator,omitempty"`
}

// ── List ──────────────────────────────────────────────────────

func (h *Handlers) APIRaidPostsList(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)

	superDonators := h.superDonatorSet()

	var userID uint = 0
	loggedIn := 0
	if u != nil {
		userID = u.ID
		loggedIn = 1
	}

	// Active posts + recently-expired posts the user can still rate.
	// Trainer code only revealed to accepted joiners. join_count excludes declined entries.
	rows, err := h.db.Query(`
		SELECT rp.id, u.username, u.role,
		       CASE WHEN EXISTS(SELECT 1 FROM raid_joins WHERE post_id=rp.id AND joiner_id=? AND status='accepted') THEN COALESCE(u.trainer_code,'') ELSE '' END,
		       rp.boss_name, rp.boss_tier, rp.note, rp.players_needed, rp.weather_boosted,
		       rp.created_at, rp.expires_at, rp.user_id,
		       CASE WHEN rp.expires_at <= NOW() THEN 1 ELSE 0 END AS expired,
		       (SELECT COUNT(*) FROM raid_joins rj WHERE rj.post_id=rp.id AND rj.status != 'declined') AS join_count,
		       COALESCE((SELECT rj.confirmed    FROM raid_joins rj WHERE rj.post_id=rp.id AND rj.joiner_id=? AND rj.status='accepted' LIMIT 1), 0),
		       COALESCE((SELECT rj.host_invited FROM raid_joins rj WHERE rj.post_id=rp.id AND rj.joiner_id=? AND rj.status='accepted' LIMIT 1), 0),
		       CASE WHEN EXISTS(SELECT 1 FROM raid_joins rj WHERE rj.post_id=rp.id AND rj.joiner_id=?) THEN 1 ELSE 0 END,
		       COALESCE((SELECT status FROM raid_joins WHERE post_id=rp.id AND joiner_id=? LIMIT 1), ''),
		       COALESCE((SELECT score FROM raid_ratings WHERE post_id=rp.id AND rater_id=? LIMIT 1), 0),
		       COALESCE((
		           SELECT SUM(rr.score * COALESCE(u2.rater_weight, 1.0)) / NULLIF(SUM(COALESCE(u2.rater_weight, 1.0)), 0)
		           FROM raid_ratings rr JOIN users u2 ON rr.rater_id = u2.id
		           WHERE rr.rated_id = u.id
		       ), 0),
		       COALESCE((SELECT COUNT(*) FROM raid_ratings WHERE rated_id=u.id), 0),
		       COALESCE(u.raid_xp, 0),
		       u.last_raid_at
		FROM raid_posts rp
		JOIN users u ON rp.user_id = u.id
		WHERE rp.expires_at > NOW()
		   OR (
		     rp.expires_at <= NOW()
		     AND rp.expires_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)
		     AND ? = 1
		     AND NOT EXISTS(SELECT 1 FROM raid_ratings WHERE post_id=rp.id AND rater_id=?)
		     AND (
		       EXISTS(SELECT 1 FROM raid_joins rj WHERE rj.post_id=rp.id AND rj.joiner_id=? AND rj.status='accepted' AND rj.confirmed=1)
		       OR rp.user_id = ?
		     )
		   )
		ORDER BY expired ASC, rp.created_at DESC`,
		userID,
		userID, userID, userID, userID, userID,
		loggedIn, userID, userID, userID,
	)
	if err != nil {
		log.Printf("APIRaidPostsList query: %v", err)
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := []raidPost{}
	for rows.Next() {
		var p raidPost
		var ownerID uint
		var hostRole, joinStatus string
		var hostXP int
		var lastRaidAt sql.NullTime
		var joinConfirmed, hostInvited, hasJoined, weatherBoosted int
		if err := rows.Scan(
			&p.ID, &p.Username, &hostRole, &p.HostCode,
			&p.BossName, &p.BossTier, &p.Note, &p.PlayersNeeded, &weatherBoosted,
			&p.CreatedAt, &p.ExpiresAt, &ownerID, &p.Expired,
			&p.JoinCount, &joinConfirmed, &hostInvited, &hasJoined,
			&joinStatus,
			&p.MyRating, &p.HostRating, &p.RatingCount,
			&hostXP, &lastRaidAt,
		); err != nil {
			continue
		}
		p.StaffBadge = staffBadge(p.Username, hostRole)
		p.SuperDonator = superDonators[ownerID]
		p.IsOwn = u != nil && u.ID == ownerID
		p.HasJoined = hasJoined > 0
		p.JoinStatus = joinStatus
		p.JoinConfirmed = joinConfirmed > 0
		p.HostInvited = hostInvited > 0
		p.WeatherBoosted = weatherBoosted > 0
		p.HostXP = hostXP
		p.HostRank = raidRankLabel(hostXP, hostRole)
		if lastRaidAt.Valid {
			weeksInactive := time.Since(lastRaidAt.Time).Hours()/(24*7) - 1
			if weeksInactive > 0 && p.HostRating > 0 {
				p.HostRating -= weeksInactive * 0.05
				if p.HostRating < 3.0 {
					p.HostRating = 3.0
				}
				p.RatingDecayed = true
			}
		}
		p.Joiners = []joinerInfo{}

		// Host sees all joiners; accepted non-owners see accepted co-raiders for lobby view
		if p.IsOwn || (p.JoinStatus == "accepted" && !p.Expired) {
			var jQ string
			if p.IsOwn {
				jQ = `SELECT rj.joiner_id, u.username, u.role, rj.confirmed, rj.host_invited, rj.status, COALESCE(u.raid_xp,0)
				      FROM raid_joins rj JOIN users u ON rj.joiner_id = u.id
				      WHERE rj.post_id = ? ORDER BY rj.priority DESC, rj.joined_at ASC`
			} else {
				jQ = `SELECT rj.joiner_id, u.username, u.role, rj.confirmed, rj.host_invited, rj.status, COALESCE(u.raid_xp,0)
				      FROM raid_joins rj JOIN users u ON rj.joiner_id = u.id
				      WHERE rj.post_id = ? AND rj.status = 'accepted' ORDER BY rj.priority DESC, rj.joined_at ASC`
			}
			jrows, err := h.db.Query(jQ, p.ID)
			if err == nil {
				for jrows.Next() {
					var ji joinerInfo
					var jRole string
					var jXP, conf, inv int
					if jrows.Scan(&ji.ID, &ji.Username, &jRole, &conf, &inv, &ji.Status, &jXP) == nil {
						ji.StaffBadge = staffBadge(ji.Username, jRole)
						ji.Confirmed = conf > 0
						ji.HostInvited = inv > 0
						ji.RaidRank = raidRankLabel(jXP, jRole)
						ji.SuperDonator = superDonators[uint(ji.ID)]
						p.Joiners = append(p.Joiners, ji)
					}
				}
				jrows.Close()
			}
		}

		if p.JoinStatus == "pending" && userID > 0 && !p.Expired {
			h.db.QueryRow(`
				SELECT COUNT(*) FROM raid_joins
				WHERE post_id = ? AND status IN ('pending','accepted')
				  AND (priority > (SELECT priority FROM raid_joins WHERE post_id = ? AND joiner_id = ?)
				    OR (priority = (SELECT priority FROM raid_joins WHERE post_id = ? AND joiner_id = ?)
				        AND joined_at <= (SELECT joined_at FROM raid_joins WHERE post_id = ? AND joiner_id = ?)))
			`, p.ID, p.ID, userID, p.ID, userID, p.ID, userID).Scan(&p.QueuePosition)
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
		BossTier       uint8  `json:"boss_tier"`
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

	var hostName, hostCode string
	h.db.QueryRow(`SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,'') FROM users WHERE id = ?`, u.ID).Scan(&hostName, &hostCode)
	if hostName == "" || hostCode == "" {
		writeJSONError(w, "you must set a Trainer Name and Trainer Code in Settings before hosting raids", http.StatusForbidden)
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
		INSERT INTO raid_posts (user_id, boss_name, boss_tier, note, players_needed, weather_boosted, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, bossName, body.BossTier, note, body.PlayersNeeded, weatherBoostedInt, expires,
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

	var joinerName, joinerCode string
	h.db.QueryRow(`SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,'') FROM users WHERE id = ?`, u.ID).Scan(&joinerName, &joinerCode)
	if joinerName == "" || joinerCode == "" {
		writeJSONError(w, "you must set a Trainer Name and Trainer Code in Settings before joining raids", http.StatusForbidden)
		return
	}

	var raidBanned bool
	h.db.QueryRow(`SELECT raid_banned FROM users WHERE id = ?`, u.ID).Scan(&raidBanned)
	if raidBanned {
		writeJSONError(w, "you are banned from hosting or joining raids", http.StatusForbidden)
		return
	}

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
	var playersNeeded uint8
	if err := h.db.QueryRow(
		`SELECT user_id, players_needed FROM raid_posts WHERE id = ? AND expires_at > NOW()`, id,
	).Scan(&ownerID, &playersNeeded); err != nil {
		writeJSONError(w, "post not found or expired", http.StatusNotFound)
		return
	}
	if ownerID == u.ID {
		writeJSONError(w, "cannot join your own post", http.StatusBadRequest)
		return
	}

	// No-op if already pending or accepted
	var existingStatus string
	_ = h.db.QueryRow(`SELECT status FROM raid_joins WHERE post_id = ? AND joiner_id = ?`, id, u.ID).Scan(&existingStatus)
	if existingStatus == "pending" || existingStatus == "accepted" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
		return
	}

	// Enforce queue cap when players_needed is set
	if playersNeeded > 0 {
		var queueSize int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_joins WHERE post_id = ? AND status IN ('pending', 'accepted')`, id).Scan(&queueSize)
		if queueSize >= int(playersNeeded) {
			writeJSONError(w, "this raid's queue is full", http.StatusConflict)
			return
		}
	}

	priority := 0
	if h.hasActivePurchase(u.ID, "queue_priority") {
		priority = 1
	}
	if _, err := h.db.Exec(
		`INSERT INTO raid_joins (post_id, joiner_id, status, priority) VALUES (?, ?, 'pending', ?)
		 ON DUPLICATE KEY UPDATE status = 'pending', priority = VALUES(priority)`, id, u.ID, priority,
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
		`UPDATE raid_joins SET confirmed = 1 WHERE post_id = ? AND joiner_id = ? AND status = 'accepted'`, id, u.ID)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "not joined", http.StatusNotFound)
		return
	}

	h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpJoinComplete, u.ID)

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
	h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpHostComplete, ownerID)

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
		// Host rating a joiner: joiner must be accepted and confirmed
		var cnt int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_joins WHERE post_id = ? AND joiner_id = ? AND status = 'accepted' AND confirmed = 1`,
			postID, body.RatedID).Scan(&cnt)
		if cnt == 0 {
			writeJSONError(w, "that joiner has not been accepted and confirmed", http.StatusForbidden)
			return
		}
	} else {
		// Joiner rating the host: must be accepted and confirmed
		var cnt int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_joins WHERE post_id = ? AND joiner_id = ? AND status = 'accepted' AND confirmed = 1`,
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

	// Recompute rater weight: penalise users who consistently deviate from consensus,
	// then apply a role multiplier so staff/testers carry more signal.
	var avgDev float64
	h.db.QueryRow(`
		SELECT COALESCE(AVG(ABS(rr.score - consensus.avg)), 0)
		FROM raid_ratings rr
		JOIN (
			SELECT rated_id, AVG(score) AS avg
			FROM raid_ratings WHERE rater_id != ?
			GROUP BY rated_id
		) consensus ON consensus.rated_id = rr.rated_id
		WHERE rr.rater_id = ?`, u.ID, u.ID).Scan(&avgDev)
	var roleMul, roleCap float64
	switch {
	case u.IsSuperAdmin() || u.IsAdmin():
		roleMul, roleCap = 1.5, 1.5
	case u.IsMod():
		roleMul, roleCap = 1.4, 1.4
	case u.IsTester():
		roleMul, roleCap = 1.2, 1.2
	default:
		roleMul, roleCap = 1.0, 1.0
	}
	newWeight := (1.0 - (avgDev * 0.3)) * roleMul
	if newWeight < 0.1 {
		newWeight = 0.1
	}
	if newWeight > roleCap {
		newWeight = roleCap
	}
	h.db.Exec(`UPDATE users SET rater_weight = ? WHERE id = ?`, newWeight, u.ID)

	// Bonus XP for high ratings received (4★ = +3 XP, 5★ = +5 XP).
	if body.Score >= 4 {
		bonusXP := 3
		if body.Score == 5 {
			bonusXP = 5
		}
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ? WHERE id = ?`, bonusXP, body.RatedID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Accept (host accepts a queued joiner) ─────────────────────

func (h *Handlers) APIRaidPostsAccept(w http.ResponseWriter, r *http.Request) {
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

	h.db.Exec(`UPDATE raid_joins SET status = 'accepted' WHERE post_id = ? AND joiner_id = ? AND status = 'pending'`, postID, body.JoinerID)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Decline (host declines a queued joiner) ───────────────────

func (h *Handlers) APIRaidPostsDecline(w http.ResponseWriter, r *http.Request) {
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

	h.db.Exec(`UPDATE raid_joins SET status = 'declined' WHERE post_id = ? AND joiner_id = ?`, postID, body.JoinerID)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
