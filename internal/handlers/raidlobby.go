package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
)

// Raid Finder v2: per-boss matchmaking queues feeding host lobbies with
// server-enforced timers. Timers are enforced lazily on read plus by a
// background sweeper, since polling clients can vanish at any moment.

var (
	lobbyTTL          = 10 * time.Minute
	confirmWindow     = 2 * time.Minute
	inviteWindow      = 4 * time.Minute
	queueHeartbeatTTL = 45 * time.Second
	raidSweepInterval = 15 * time.Second
)

// Leave-churn cooldown (carried over from v1) and XP awards.
const (
	raidLeaveCooldownTrigger  = 4
	raidLeaveCooldownDuration = 10 * time.Minute
	xpHostComplete            = 20
	xpJoinComplete            = 15
)

func init() {
	// Dev-only: shrink timers so timeout paths are testable without waiting.
	if os.Getenv("RAID_FAST_TIMERS") == "1" {
		confirmWindow = 15 * time.Second
		inviteWindow = 30 * time.Second
		raidSweepInterval = 3 * time.Second
	}
}

func (h *Handlers) raidGuard(w http.ResponseWriter, r *http.Request) *auth.User {
	if !h.maintenanceSettings().RaidFinderEnabled {
		writeJSONError(w, h.t(r, "error.lobby_maintenance"), http.StatusServiceUnavailable)
		return nil
	}
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return nil
	}
	var name, code string
	var banned bool
	h.db.QueryRow(`SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,''), raid_banned FROM users WHERE id = ?`, u.ID).
		Scan(&name, &code, &banned)
	if banned {
		writeJSONError(w, h.t(r, "error.lobby_banned"), http.StatusForbidden)
		return nil
	}
	if name == "" || code == "" {
		writeJSONError(w, h.t(r, "error.lobby_trainer_required"), http.StatusForbidden)
		return nil
	}
	return u
}

func (h *Handlers) currentBossTiers() map[string]uint8 {
	var tiers map[string][]struct {
		PokemonName string `json:"pokemon_name"`
	}
	if err := json.Unmarshal(h.store.Raids(), &tiers); err != nil {
		return map[string]uint8{}
	}
	out := map[string]uint8{}
	for tierStr, bosses := range tiers {
		t, _ := strconv.Atoi(tierStr)
		for _, b := range bosses {
			out[b.PokemonName] = uint8(t)
		}
	}
	return out
}

func (h *Handlers) settingFloat(key string, def float64) float64 {
	var v string
	if err := h.db.QueryRow(`SELECT setting_value FROM site_settings WHERE setting_key = ?`, key).Scan(&v); err != nil {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// canCustomLobby gates custom-boss lobby creation: staff, the admin-granted
// special ranks, or top effective trust.
func (h *Handlers) canCustomLobby(u *auth.User) bool {
	if u.IsMod() || u.SpecialRank == "trusted" || u.SpecialRank == "content_creator" {
		return true
	}
	return h.effectiveTrust(u.ID) >= h.settingFloat("raid_custom_lobby_min_trust", 50)
}

// canPreQueue gates queueing for bosses with no open lobby yet: staff and
// active super donators may wait for a host; everyone else queues from the
// open lobbies list.
func (h *Handlers) canPreQueue(u *auth.User) bool {
	return u.IsMod() || h.hasActivePurchase(u.ID, "super_donator")
}

func (h *Handlers) hostingLobbyID(userID uint) uint64 {
	var id uint64
	h.db.QueryRow(`SELECT id FROM raid_lobbies WHERE host_id = ? AND state IN ('open','full','raiding') AND expires_at > NOW() LIMIT 1`, userID).Scan(&id)
	return id
}

func (h *Handlers) memberLobbyID(userID uint) uint64 {
	var id uint64
	h.db.QueryRow(`
		SELECT l.id FROM raid_lobby_members lm
		JOIN raid_lobbies l ON l.id = lm.lobby_id
		WHERE lm.user_id = ? AND lm.state IN ('matched','confirmed')
		  AND l.state IN ('open','full','raiding') AND l.expires_at > NOW()
		LIMIT 1`, userID).Scan(&id)
	return id
}

// requeueAtFront returns a lobby member to the queue preserving seniority:
// their matched_at becomes the enqueued_at, placing them ahead of everyone
// who joined the queue after they were matched. No penalty.
func (h *Handlers) requeueAtFront(lobbyID uint64, bossName string, bossTier uint8) {
	rows, err := h.db.Query(`
		SELECT user_id, priority, matched_at FROM raid_lobby_members
		WHERE lobby_id = ? AND state IN ('matched','confirmed')`, lobbyID)
	if err != nil {
		return
	}
	type entry struct {
		userID   uint
		priority int
		at       time.Time
	}
	var members []entry
	for rows.Next() {
		var e entry
		if rows.Scan(&e.userID, &e.priority, &e.at) == nil {
			members = append(members, e)
		}
	}
	rows.Close()
	for _, m := range members {
		h.db.Exec(`
			INSERT INTO raid_queue (user_id, boss_name, boss_tier, priority, enqueued_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, NOW())
			ON DUPLICATE KEY UPDATE boss_name = VALUES(boss_name), boss_tier = VALUES(boss_tier),
				priority = VALUES(priority), enqueued_at = VALUES(enqueued_at), last_seen_at = NOW()`,
			m.userID, bossName, bossTier, m.priority, m.at)
		h.db.Exec(`UPDATE raid_lobby_members SET state = 'requeued' WHERE lobby_id = ? AND user_id = ?`, lobbyID, m.userID)
	}
}

func (h *Handlers) matchQueue(bossName string) {
	h.raidMu.Lock()
	defer h.raidMu.Unlock()
	h.matchQueueLocked(bossName)
}

// matchQueueLocked fills open lobbies for a boss from the queue, oldest lobby
// first; priority purchasers jump ahead, FIFO within the same class. Callers
// must hold raidMu.
func (h *Handlers) matchQueueLocked(bossName string) {
	type lobby struct {
		id     uint64
		hostID uint
		max    int
	}
	rows, err := h.db.Query(`
		SELECT id, host_id, max_members FROM raid_lobbies
		WHERE state = 'open' AND boss_name = ? AND expires_at > NOW()
		ORDER BY created_at ASC`, bossName)
	if err != nil {
		return
	}
	var lobbies []lobby
	for rows.Next() {
		var l lobby
		if rows.Scan(&l.id, &l.hostID, &l.max) == nil {
			lobbies = append(lobbies, l)
		}
	}
	rows.Close()

	for _, l := range lobbies {
		var active int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_lobby_members WHERE lobby_id = ? AND state IN ('matched','confirmed')`, l.id).Scan(&active)
		free := l.max - active
		if free <= 0 {
			continue
		}

		crows, err := h.db.Query(`
			SELECT user_id, priority FROM raid_queue
			WHERE boss_name = ? AND user_id != ?
			ORDER BY priority DESC, enqueued_at ASC LIMIT ?`, bossName, l.hostID, free)
		if err != nil {
			return
		}
		type cand struct {
			userID   uint
			priority int
		}
		var cands []cand
		for crows.Next() {
			var c cand
			if crows.Scan(&c.userID, &c.priority) == nil {
				cands = append(cands, c)
			}
		}
		crows.Close()
		if len(cands) == 0 {
			return // queue empty for this boss
		}

		deadline := time.Now().Add(confirmWindow)
		for _, c := range cands {
			_, err := h.db.Exec(`
				INSERT INTO raid_lobby_members (lobby_id, user_id, state, priority, matched_at, confirm_deadline)
				VALUES (?, ?, 'matched', ?, NOW(), ?)
				ON DUPLICATE KEY UPDATE state = 'matched', priority = VALUES(priority),
					matched_at = NOW(), confirm_deadline = VALUES(confirm_deadline),
					confirmed_at = NULL, attended = NULL, left_early = 0, raid_success = NULL, host_vote = 'none'`,
				l.id, c.userID, c.priority, deadline)
			if err != nil {
				log.Printf("matchQueue insert member %d -> lobby %d: %v", c.userID, l.id, err)
				continue
			}
			h.db.Exec(`DELETE FROM raid_queue WHERE user_id = ?`, c.userID)
		}
	}
}

// StartRaidSweeper launches the background goroutine that enforces lobby and
// queue timers for clients that stopped polling. Call once from server.New.
func (h *Handlers) StartRaidSweeper() {
	go func() {
		t := time.NewTicker(raidSweepInterval)
		for range t.C {
			h.processRaidTimers()
		}
	}()
}

func (h *Handlers) processRaidTimers() {
	h.raidMu.Lock()
	defer h.raidMu.Unlock()

	// 1. Confirm-window timeouts: member is removed entirely (not requeued);
	// repeated timeouts cost trust (first two per 24h are free).
	rows, err := h.db.Query(`
		SELECT lm.lobby_id, lm.user_id FROM raid_lobby_members lm
		JOIN raid_lobbies l ON l.id = lm.lobby_id
		WHERE lm.state = 'matched' AND lm.confirm_deadline < NOW() AND l.state = 'open'`)
	if err == nil {
		type timeoutRow struct {
			lobbyID uint64
			userID  uint
		}
		var timeouts []timeoutRow
		for rows.Next() {
			var t timeoutRow
			if rows.Scan(&t.lobbyID, &t.userID) == nil {
				timeouts = append(timeouts, t)
			}
		}
		rows.Close()
		for _, t := range timeouts {
			h.db.Exec(`UPDATE raid_lobby_members SET state = 'timed_out' WHERE lobby_id = ? AND user_id = ? AND state = 'matched'`, t.lobbyID, t.userID)
			h.logConfirmTimeout(t.userID, t.lobbyID)
		}
	}

	// 2. Invite-window expiry: confirmed members return to the queue front
	// with no penalty; the host takes a minor trust reduction.
	lrows, err := h.db.Query(`
		SELECT id, host_id, boss_name, boss_tier FROM raid_lobbies
		WHERE state = 'full' AND invite_deadline IS NOT NULL AND invite_deadline < NOW()`)
	if err == nil {
		type failedLobby struct {
			id       uint64
			hostID   uint
			bossName string
			bossTier uint8
		}
		var failed []failedLobby
		for lrows.Next() {
			var f failedLobby
			if lrows.Scan(&f.id, &f.hostID, &f.bossName, &f.bossTier) == nil {
				failed = append(failed, f)
			}
		}
		lrows.Close()
		for _, f := range failed {
			h.requeueAtFront(f.id, f.bossName, f.bossTier)
			h.db.Exec(`UPDATE raid_lobbies SET state = 'expired', closed_at = NOW() WHERE id = ?`, f.id)
			h.insertTrustEvent(f.hostID, 0, f.id, "invite_window_fail", trustDeltaInviteWindowFail, 1.0, "invite window lapsed")
		}
	}

	// 3. Hard TTL: open lobbies expire (matched members dropped, no penalty);
	// raiding lobbies that never got a report also close out quietly. Hosts
	// who let too many open lobbies die unstarted take a minor trust hit, so
	// capture them before the expiry UPDATE makes the rows unmatchable.
	type deadLobby struct {
		id     uint64
		hostID uint
	}
	var dead []deadLobby
	orows, err := h.db.Query(`SELECT id, host_id FROM raid_lobbies WHERE state = 'open' AND expires_at < NOW()`)
	if err == nil {
		for orows.Next() {
			var d deadLobby
			if orows.Scan(&d.id, &d.hostID) == nil {
				dead = append(dead, d)
			}
		}
		orows.Close()
	}
	h.db.Exec(`
		UPDATE raid_lobby_members lm JOIN raid_lobbies l ON l.id = lm.lobby_id
		SET lm.state = 'removed'
		WHERE l.state = 'open' AND l.expires_at < NOW() AND lm.state IN ('matched','confirmed')`)
	h.db.Exec(`UPDATE raid_lobbies SET state = 'expired', closed_at = NOW() WHERE state IN ('open','raiding') AND expires_at < NOW()`)
	for _, d := range dead {
		h.logHostUnfulfilled(d.hostID, d.id)
	}

	h.db.Exec(`DELETE FROM raid_queue WHERE last_seen_at < DATE_SUB(NOW(), INTERVAL ? SECOND)`, int(queueHeartbeatTTL.Seconds()))

	brows, err := h.db.Query(`
		SELECT DISTINCT q.boss_name FROM raid_queue q
		JOIN raid_lobbies l ON l.boss_name = q.boss_name AND l.state = 'open' AND l.expires_at > NOW()`)
	if err == nil {
		var bosses []string
		for brows.Next() {
			var b string
			if brows.Scan(&b) == nil {
				bosses = append(bosses, b)
			}
		}
		brows.Close()
		for _, b := range bosses {
			h.matchQueueLocked(b)
		}
	}
}

// lazyExpireFor runs the full timer pass only when something relevant to the
// requesting user has actually lapsed, so countdown edges are exact between
// sweeps without paying the sweep cost on every poll.
func (h *Handlers) lazyExpireFor(userID uint) {
	var due int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM raid_lobby_members lm
		JOIN raid_lobbies l ON l.id = lm.lobby_id
		WHERE (lm.user_id = ? OR l.host_id = ?)
		  AND ((lm.state = 'matched' AND lm.confirm_deadline < NOW() AND l.state = 'open')
		    OR (l.state = 'full' AND l.invite_deadline IS NOT NULL AND l.invite_deadline < NOW()))`,
		userID, userID).Scan(&due)
	if due > 0 {
		h.processRaidTimers()
	}
}

func (h *Handlers) APIRaidOverview(w http.ResponseWriter, r *http.Request) {
	type bossOverview struct {
		BossName    string `json:"boss_name"`
		BossTier    uint8  `json:"boss_tier"`
		OpenLobbies int    `json:"open_lobbies"`
		QueueLen    int    `json:"queue_len"`
	}
	type customLobby struct {
		ID       uint64 `json:"id"`
		BossName string `json:"boss_name"`
		BossTier uint8  `json:"boss_tier"`
		Note     string `json:"note"`
		Host     string `json:"host"`
		QueueLen int    `json:"queue_len"`
	}

	byBoss := map[string]*bossOverview{}
	rows, err := h.db.Query(`
		SELECT boss_name, boss_tier, COUNT(*) FROM raid_lobbies
		WHERE state = 'open' AND expires_at > NOW() AND is_custom = 0
		GROUP BY boss_name, boss_tier`)
	if err == nil {
		for rows.Next() {
			var b bossOverview
			if rows.Scan(&b.BossName, &b.BossTier, &b.OpenLobbies) == nil {
				byBoss[b.BossName] = &b
			}
		}
		rows.Close()
	}
	qrows, err := h.db.Query(`SELECT boss_name, MAX(boss_tier), COUNT(*) FROM raid_queue GROUP BY boss_name`)
	if err == nil {
		for qrows.Next() {
			var name string
			var tier uint8
			var n int
			if qrows.Scan(&name, &tier, &n) == nil {
				if b, ok := byBoss[name]; ok {
					b.QueueLen = n
				} else {
					byBoss[name] = &bossOverview{BossName: name, BossTier: tier, QueueLen: n}
				}
			}
		}
		qrows.Close()
	}
	bosses := []bossOverview{}
	for _, b := range byBoss {
		bosses = append(bosses, *b)
	}

	customs := []customLobby{}
	crows, err := h.db.Query(`
		SELECT l.id, l.boss_name, l.boss_tier, l.note, u.username,
		       (SELECT COUNT(*) FROM raid_queue q WHERE q.boss_name = l.boss_name)
		FROM raid_lobbies l JOIN users u ON u.id = l.host_id
		WHERE l.state = 'open' AND l.expires_at > NOW() AND l.is_custom = 1
		ORDER BY l.created_at ASC`)
	if err == nil {
		for crows.Next() {
			var c customLobby
			if crows.Scan(&c.ID, &c.BossName, &c.BossTier, &c.Note, &c.Host, &c.QueueLen) == nil {
				customs = append(customs, c)
			}
		}
		crows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"bosses": bosses, "custom_lobbies": customs})
}

type raidLobbyMember struct {
	UserID          uint       `json:"user_id"`
	Username        string     `json:"username"`
	StaffBadge      string     `json:"staff_badge,omitempty"`
	SpecialRank     string     `json:"special_rank,omitempty"`
	State           string     `json:"state"`
	TrustTier       string     `json:"trust_tier"`
	RaidRank        string     `json:"raid_rank"`
	TrainerName     string     `json:"trainer_name,omitempty"`
	TrainerCode     string     `json:"trainer_code,omitempty"`
	ConfirmDeadline *time.Time `json:"confirm_deadline,omitempty"`
}

type raidLobbyHost struct {
	Username    string `json:"username"`
	StaffBadge  string `json:"staff_badge,omitempty"`
	SpecialRank string `json:"special_rank,omitempty"`
	TrustTier   string `json:"trust_tier"`
	RaidRank    string `json:"raid_rank"`
	TrainerName string `json:"trainer_name,omitempty"`
	TrainerCode string `json:"trainer_code,omitempty"`
}

type raidLobbyState struct {
	ID             uint64            `json:"id"`
	BossName       string            `json:"boss_name"`
	BossTier       uint8             `json:"boss_tier"`
	IsCustom       bool              `json:"is_custom"`
	Note           string            `json:"note"`
	WeatherBoosted bool              `json:"weather_boosted"`
	MaxMembers     int               `json:"max_members"`
	State          string            `json:"state"`
	InviteDeadline *time.Time        `json:"invite_deadline,omitempty"`
	Host           raidLobbyHost     `json:"host"`
	Members        []raidLobbyMember `json:"members"`
	MyState        string            `json:"my_state,omitempty"`
	MyDeadline     *time.Time        `json:"my_deadline,omitempty"`
}

type raidQueueState struct {
	BossName string `json:"boss_name"`
	BossTier uint8  `json:"boss_tier"`
	Position int    `json:"position"`
	Total    int    `json:"total"`
	Priority bool   `json:"priority"`
}

type raidFeedbackDue struct {
	LobbyID  uint64 `json:"lobby_id"`
	BossName string `json:"boss_name"`
}

func (h *Handlers) APIRaidState(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	h.lazyExpireFor(u.ID)

	out := map[string]any{
		"server_time": time.Now().UTC(),
		"mode":        "idle",
	}

	var cooldownUntil time.Time
	if err := h.db.QueryRow(`SELECT until FROM raid_join_cooldowns WHERE user_id = ? AND until > NOW()`, u.ID).Scan(&cooldownUntil); err == nil {
		out["cooldown_until"] = cooldownUntil
	}

	// Post-raid feedback owed as an attended member, regardless of mode.
	feedback := []raidFeedbackDue{}
	frows, err := h.db.Query(`
		SELECT l.id, l.boss_name FROM raid_lobby_members lm
		JOIN raid_lobbies l ON l.id = lm.lobby_id
		WHERE lm.user_id = ? AND lm.state = 'confirmed' AND lm.attended = 1
		  AND lm.host_vote = 'none' AND lm.raid_success IS NULL
		  AND l.state = 'reported' AND l.closed_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)`, u.ID)
	if err == nil {
		for frows.Next() {
			var f raidFeedbackDue
			if frows.Scan(&f.LobbyID, &f.BossName) == nil {
				feedback = append(feedback, f)
			}
		}
		frows.Close()
	}
	out["feedback_due"] = feedback

	if lobbyID := h.hostingLobbyID(u.ID); lobbyID > 0 {
		out["mode"] = "hosting"
		out["lobby"] = h.buildLobbyState(lobbyID, u, true)
	} else if lobbyID := h.memberLobbyID(u.ID); lobbyID > 0 {
		lb := h.buildLobbyState(lobbyID, u, false)
		if lb != nil && lb.MyState == "matched" {
			out["mode"] = "matched"
		} else {
			out["mode"] = "member"
		}
		out["lobby"] = lb
	} else if q := h.queueState(u.ID); q != nil {
		out["mode"] = "queued"
		out["queue"] = q
	} else if len(feedback) > 0 {
		out["mode"] = "feedback"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// queueState refreshes the heartbeat and returns the user's queue position.
func (h *Handlers) queueState(userID uint) *raidQueueState {
	var q raidQueueState
	var priority int
	var enqueued time.Time
	err := h.db.QueryRow(`SELECT boss_name, boss_tier, priority, enqueued_at FROM raid_queue WHERE user_id = ?`, userID).
		Scan(&q.BossName, &q.BossTier, &priority, &enqueued)
	if err != nil {
		return nil
	}
	h.db.Exec(`UPDATE raid_queue SET last_seen_at = NOW() WHERE user_id = ?`, userID)
	q.Priority = priority > 0
	h.db.QueryRow(`
		SELECT COUNT(*) FROM raid_queue
		WHERE boss_name = ? AND (priority > ? OR (priority = ? AND enqueued_at <= ?))`,
		q.BossName, priority, priority, enqueued).Scan(&q.Position)
	h.db.QueryRow(`SELECT COUNT(*) FROM raid_queue WHERE boss_name = ?`, q.BossName).Scan(&q.Total)
	return &q
}

// buildLobbyState shapes the lobby payload with server-side visibility rules:
// the host's trainer code goes to every matched/confirmed member (they need
// it to send the friend request); a member's trainer name and code go only to
// the host and only after that member confirms.
func (h *Handlers) buildLobbyState(lobbyID uint64, viewer *auth.User, isHost bool) *raidLobbyState {
	var lb raidLobbyState
	var hostID uint
	var inviteDeadline sql.NullTime
	var isCustom, weather int
	var hostRole, hostTrainerName, hostTrainerCode, hostSpecialRank string
	var hostXP int
	err := h.db.QueryRow(`
		SELECT l.id, l.boss_name, l.boss_tier, l.is_custom, l.note, l.weather_boosted,
		       l.max_members, l.state, l.invite_deadline, l.host_id,
		       u.username, u.role, COALESCE(u.special_rank,''), COALESCE(u.raid_xp,0),
		       COALESCE(u.trainer_name,''), COALESCE(u.trainer_code,'')
		FROM raid_lobbies l JOIN users u ON u.id = l.host_id
		WHERE l.id = ?`, lobbyID).
		Scan(&lb.ID, &lb.BossName, &lb.BossTier, &isCustom, &lb.Note, &weather,
			&lb.MaxMembers, &lb.State, &inviteDeadline, &hostID,
			&lb.Host.Username, &hostRole, &hostSpecialRank, &hostXP,
			&hostTrainerName, &hostTrainerCode)
	if err != nil {
		return nil
	}
	lb.IsCustom = isCustom > 0
	lb.WeatherBoosted = weather > 0
	if inviteDeadline.Valid {
		lb.InviteDeadline = &inviteDeadline.Time
	}
	lb.Host.StaffBadge = staffBadge(lb.Host.Username, hostRole)
	lb.Host.SpecialRank = hostSpecialRank
	lb.Host.RaidRank = raidRankLabel(hostXP, badgeRole(lb.Host.Username, hostRole))
	lb.Host.TrustTier = h.trustTierFor(hostID)
	if !isHost {
		// Members always see the host's code; that is the whole point.
		lb.Host.TrainerName = hostTrainerName
		lb.Host.TrainerCode = hostTrainerCode
	}

	lb.Members = []raidLobbyMember{}
	rows, err := h.db.Query(`
		SELECT lm.user_id, u.username, u.role, COALESCE(u.special_rank,''), COALESCE(u.raid_xp,0),
		       lm.state, lm.confirm_deadline,
		       COALESCE(u.trainer_name,''), COALESCE(u.trainer_code,'')
		FROM raid_lobby_members lm JOIN users u ON u.id = lm.user_id
		WHERE lm.lobby_id = ? AND lm.state IN ('matched','confirmed')
		ORDER BY lm.matched_at ASC`, lobbyID)
	if err == nil {
		for rows.Next() {
			var m raidLobbyMember
			var role, tName, tCode string
			var xp int
			var deadline time.Time
			if rows.Scan(&m.UserID, &m.Username, &role, &m.SpecialRank, &xp,
				&m.State, &deadline, &tName, &tCode) != nil {
				continue
			}
			m.StaffBadge = staffBadge(m.Username, role)
			m.RaidRank = raidRankLabel(xp, badgeRole(m.Username, role))
			m.TrustTier = h.trustTierFor(m.UserID)
			if m.State == "matched" {
				d := deadline
				m.ConfirmDeadline = &d
			}
			// Trainer details only for the host, only once confirmed.
			if isHost && m.State == "confirmed" {
				m.TrainerName = tName
				m.TrainerCode = tCode
			}
			if m.UserID == viewer.ID {
				lb.MyState = m.State
				lb.MyDeadline = m.ConfirmDeadline
			}
			lb.Members = append(lb.Members, m)
		}
		rows.Close()
	}
	return &lb
}

// badgeRole feeds the superadmin label through to the rank helpers, which
// special-case "superadmin" alongside "admin".
func badgeRole(username, role string) string {
	if auth.SuperadminUser != "" && username == auth.SuperadminUser {
		return "superadmin"
	}
	return role
}

func (h *Handlers) APIRaidQueueJoin(w http.ResponseWriter, r *http.Request) {
	u := h.raidGuard(w, r)
	if u == nil {
		return
	}

	var body struct {
		BossName string `json:"boss_name"`
		BossTier uint8  `json:"boss_tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	bossName := strings.TrimSpace(body.BossName)
	if bossName == "" || len(bossName) > 64 {
		writeJSONError(w, h.t(r, "error.lobby_boss_name"), http.StatusBadRequest)
		return
	}

	var cooldownUntil time.Time
	if err := h.db.QueryRow(`SELECT until FROM raid_join_cooldowns WHERE user_id = ? AND until > NOW()`, u.ID).Scan(&cooldownUntil); err == nil {
		remaining := time.Until(cooldownUntil).Round(time.Second)
		writeJSONError(w, fmt.Sprintf("%s %s.", h.t(r, "error.lobby_cooldown"), remaining), http.StatusTooManyRequests)
		return
	}

	if h.hostingLobbyID(u.ID) > 0 {
		writeJSONError(w, h.t(r, "error.lobby_hosting_already"), http.StatusConflict)
		return
	}
	if h.memberLobbyID(u.ID) > 0 {
		writeJSONError(w, h.t(r, "error.lobby_in_lobby"), http.StatusConflict)
		return
	}

	tier, isCurrent := h.currentBossTiers()[bossName]
	if isCurrent && !h.canPreQueue(u) {
		var openLobbies int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_lobbies WHERE boss_name = ? AND is_custom = 0 AND state = 'open' AND expires_at > NOW()`, bossName).Scan(&openLobbies)
		if openLobbies == 0 {
			writeJSONError(w, h.t(r, "error.lobby_none_open"), http.StatusConflict)
			return
		}
	}
	if !isCurrent {
		var customOpen int
		h.db.QueryRow(`SELECT COUNT(*) FROM raid_lobbies WHERE boss_name = ? AND is_custom = 1 AND state = 'open' AND expires_at > NOW()`, bossName).Scan(&customOpen)
		if customOpen == 0 {
			writeJSONError(w, h.t(r, "error.lobby_boss_unavailable"), http.StatusBadRequest)
			return
		}
		tier = body.BossTier
	}

	priority := 0
	if h.hasActivePurchase(u.ID, "queue_priority") {
		priority = 1
	}
	if _, err := h.db.Exec(`
		INSERT INTO raid_queue (user_id, boss_name, boss_tier, priority, enqueued_at)
		VALUES (?, ?, ?, ?, NOW(3))
		ON DUPLICATE KEY UPDATE boss_name = VALUES(boss_name), boss_tier = VALUES(boss_tier),
			priority = VALUES(priority), enqueued_at = NOW(3), last_seen_at = NOW()`,
		u.ID, bossName, tier, priority); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	h.matchQueue(bossName)

	q := h.queueState(u.ID)
	position := 0
	if q != nil {
		position = q.Position
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "position": position})
}

func (h *Handlers) APIRaidQueueLeave(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	h.db.Exec(`DELETE FROM raid_queue WHERE user_id = ?`, u.ID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIRaidLobbyCreate(w http.ResponseWriter, r *http.Request) {
	u := h.raidGuard(w, r)
	if u == nil {
		return
	}

	var body struct {
		BossName       string `json:"boss_name"`
		BossTier       uint8  `json:"boss_tier"`
		IsCustom       bool   `json:"is_custom"`
		Note           string `json:"note"`
		MaxMembers     int    `json:"max_members"`
		WeatherBoosted bool   `json:"weather_boosted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	bossName := strings.TrimSpace(body.BossName)
	note := strings.TrimSpace(body.Note)
	if bossName == "" || len(bossName) > 64 {
		writeJSONError(w, h.t(r, "error.lobby_boss_name"), http.StatusBadRequest)
		return
	}
	if len(note) > 160 {
		writeJSONError(w, h.t(r, "error.lobby_note_length"), http.StatusBadRequest)
		return
	}
	if body.MaxMembers < 1 || body.MaxMembers > 20 {
		writeJSONError(w, h.t(r, "error.lobby_max_members"), http.StatusBadRequest)
		return
	}

	tier := body.BossTier
	if body.IsCustom {
		if !h.canCustomLobby(u) {
			writeJSONError(w, h.t(r, "error.lobby_custom_denied"), http.StatusForbidden)
			return
		}
	} else {
		t, ok := h.currentBossTiers()[bossName]
		if !ok {
			writeJSONError(w, h.t(r, "error.lobby_not_current_boss"), http.StatusBadRequest)
			return
		}
		tier = t
	}

	if h.hostingLobbyID(u.ID) > 0 {
		writeJSONError(w, h.t(r, "error.lobby_active_exists"), http.StatusConflict)
		return
	}
	if h.memberLobbyID(u.ID) > 0 {
		writeJSONError(w, h.t(r, "error.lobby_member_already"), http.StatusConflict)
		return
	}
	h.db.Exec(`DELETE FROM raid_queue WHERE user_id = ?`, u.ID)

	isCustomInt := 0
	if body.IsCustom {
		isCustomInt = 1
	}
	weatherInt := 0
	if body.WeatherBoosted {
		weatherInt = 1
	}
	res, err := h.db.Exec(`
		INSERT INTO raid_lobbies (host_id, boss_name, boss_tier, is_custom, note, weather_boosted, max_members, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, bossName, tier, isCustomInt, note, weatherInt, body.MaxMembers, time.Now().Add(lobbyTTL))
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()

	h.matchQueue(bossName)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

func (h *Handlers) APIRaidLobbyCancel(w http.ResponseWriter, r *http.Request) {
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

	var hostID uint
	var bossName, state string
	var bossTier uint8
	if err := h.db.QueryRow(`SELECT host_id, boss_name, boss_tier, state FROM raid_lobbies WHERE id = ?`, id).
		Scan(&hostID, &bossName, &bossTier, &state); err != nil {
		writeJSONError(w, h.t(r, "error.lobby_not_found"), http.StatusNotFound)
		return
	}
	if hostID != u.ID && !u.IsMod() {
		writeJSONError(w, h.t(r, "error.lobby_not_yours"), http.StatusForbidden)
		return
	}
	if state != "open" && state != "full" && state != "raiding" {
		writeJSONError(w, h.t(r, "error.lobby_closed"), http.StatusConflict)
		return
	}

	// Members go back to the front of the queue with no penalty. A host
	// cancelling before the raid started counts toward their unfulfilled
	// lobby tally; staff cancellations and mid-raid cancels do not.
	h.requeueAtFront(id, bossName, bossTier)
	h.db.Exec(`UPDATE raid_lobbies SET state = 'cancelled', closed_at = NOW() WHERE id = ?`, id)
	if hostID == u.ID && state != "raiding" {
		h.logHostUnfulfilled(hostID, id)
	}
	h.matchQueue(bossName)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIRaidLobbyConfirm(w http.ResponseWriter, r *http.Request) {
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

	res, err := h.db.Exec(`
		UPDATE raid_lobby_members lm JOIN raid_lobbies l ON l.id = lm.lobby_id
		SET lm.state = 'confirmed', lm.confirmed_at = NOW()
		WHERE lm.lobby_id = ? AND lm.user_id = ? AND lm.state = 'matched'
		  AND lm.confirm_deadline > NOW() AND l.state = 'open'`, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_confirm_expired"), http.StatusGone)
		return
	}

	// Lobby fills when every slot is confirmed (the matcher never assigns
	// more than max_members active slots).
	var confirmed, maxMembers int
	h.db.QueryRow(`SELECT COUNT(*) FROM raid_lobby_members WHERE lobby_id = ? AND state = 'confirmed'`, id).Scan(&confirmed)
	h.db.QueryRow(`SELECT max_members FROM raid_lobbies WHERE id = ?`, id).Scan(&maxMembers)
	if confirmed >= maxMembers {
		h.db.Exec(`UPDATE raid_lobbies SET state = 'full', invite_deadline = ? WHERE id = ? AND state = 'open'`,
			time.Now().Add(inviteWindow), id)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIRaidLobbyLeave(w http.ResponseWriter, r *http.Request) {
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

	res, err := h.db.Exec(`
		UPDATE raid_lobby_members SET state = 'left'
		WHERE lobby_id = ? AND user_id = ? AND state IN ('matched','confirmed')`, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
		return
	}

	h.reopenIfSlotFreed(id)

	// Leave-churn cooldown, unchanged from v1.
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

func (h *Handlers) APIRaidLobbyKick(w http.ResponseWriter, r *http.Request) {
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
		UserID uint `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	var hostID uint
	if err := h.db.QueryRow(`SELECT host_id FROM raid_lobbies WHERE id = ?`, id).Scan(&hostID); err != nil {
		writeJSONError(w, h.t(r, "error.lobby_not_found"), http.StatusNotFound)
		return
	}
	if hostID != u.ID && !u.IsMod() {
		writeJSONError(w, h.t(r, "error.lobby_not_yours"), http.StatusForbidden)
		return
	}

	h.db.Exec(`UPDATE raid_lobby_members SET state = 'removed' WHERE lobby_id = ? AND user_id = ? AND state IN ('matched','confirmed')`, id, body.UserID)
	h.reopenIfSlotFreed(id)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// reopenIfSlotFreed reverts a full lobby to open (clearing the invite timer)
// when a confirmed slot frees up, then resumes matching.
func (h *Handlers) reopenIfSlotFreed(lobbyID uint64) {
	var bossName, state string
	if h.db.QueryRow(`SELECT boss_name, state FROM raid_lobbies WHERE id = ?`, lobbyID).Scan(&bossName, &state) != nil {
		return
	}
	if state == "full" {
		h.db.Exec(`UPDATE raid_lobbies SET state = 'open', invite_deadline = NULL WHERE id = ? AND state = 'full'`, lobbyID)
	}
	if state == "full" || state == "open" {
		h.matchQueue(bossName)
	}
}

func (h *Handlers) APIRaidLobbyInvited(w http.ResponseWriter, r *http.Request) {
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

	res, err := h.db.Exec(`
		UPDATE raid_lobbies SET state = 'raiding'
		WHERE id = ? AND host_id = ? AND state = 'full'
		  AND (invite_deadline IS NULL OR invite_deadline > NOW())`, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_not_full_or_lapsed"), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIRaidLobbyReport(w http.ResponseWriter, r *http.Request) {
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
		Members []struct {
			UserID    uint `json:"user_id"`
			Attended  bool `json:"attended"`
			LeftEarly bool `json:"left_early"`
		} `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	res, err := h.db.Exec(`UPDATE raid_lobbies SET state = 'reported', closed_at = NOW() WHERE id = ? AND host_id = ? AND state = 'raiding'`, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_no_report_due"), http.StatusConflict)
		return
	}

	attendedAny := false
	for _, m := range body.Members {
		res, err := h.db.Exec(`
			UPDATE raid_lobby_members SET attended = ?, left_early = ?
			WHERE lobby_id = ? AND user_id = ? AND state = 'confirmed'`,
			m.Attended, m.LeftEarly, id, m.UserID)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if m.Attended {
			attendedAny = true
			h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpJoinComplete, m.UserID)
		}
		if m.LeftEarly {
			weight := h.peerReportWeight(u, m.UserID)
			h.insertTrustEvent(m.UserID, u.ID, id, "left_early", trustDeltaLeftEarly, weight, "host marked left early")
		}
	}
	if attendedAny {
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpHostComplete, u.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIRaidLobbyFeedback(w http.ResponseWriter, r *http.Request) {
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
		RaidSuccess bool   `json:"raid_success"`
		HostVote    string `json:"host_vote"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.HostVote != "commend" && body.HostVote != "dislike" && body.HostVote != "none" {
		writeJSONError(w, h.t(r, "error.lobby_invalid_vote"), http.StatusBadRequest)
		return
	}

	var hostID uint
	if err := h.db.QueryRow(`
		SELECT host_id FROM raid_lobbies
		WHERE id = ? AND state = 'reported' AND closed_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)`, id).Scan(&hostID); err != nil {
		writeJSONError(w, h.t(r, "error.lobby_feedback_closed"), http.StatusGone)
		return
	}

	res, err := h.db.Exec(`
		UPDATE raid_lobby_members SET raid_success = ?, host_vote = ?
		WHERE lobby_id = ? AND user_id = ? AND state = 'confirmed' AND attended = 1
		  AND raid_success IS NULL AND host_vote = 'none'`,
		body.RaidSuccess, body.HostVote, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_feedback_done"), http.StatusConflict)
		return
	}

	weight := h.peerReportWeight(u, hostID)
	if body.RaidSuccess {
		h.insertTrustEvent(hostID, u.ID, id, "raid_success", trustDeltaRaidSuccess, weight, "")
	}
	switch body.HostVote {
	case "commend":
		h.insertTrustEvent(hostID, u.ID, id, "commend", trustDeltaCommend, weight, "")
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + 5 WHERE id = ?`, hostID)
	case "dislike":
		h.insertTrustEvent(hostID, u.ID, id, "dislike", trustDeltaDislike, weight, "")
	}
	if body.HostVote != "none" {
		h.recomputeReliability(u)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
