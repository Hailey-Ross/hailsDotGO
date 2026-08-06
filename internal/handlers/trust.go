package handlers

import (
	"log"
	"math"
	"time"

	"pogo.hails.cc/internal/auth"
)

// Trust engine. trust_events is the source of truth; users.trust_score is an
// incrementally maintained cache of SUM(applied_delta). Peer reports are
// weighted by the actor's report reliability (users.rater_weight, repurposed
// from the old star-rating consensus weight) and an account-age factor.
// Staff reports bypass both and apply a fixed heavy weight.

const (
	trustDeltaCommend          = 3.0
	trustDeltaDislike          = -4.0
	trustDeltaConfirmTimeout   = -2.0
	trustDeltaInviteWindowFail = -1.0
	trustDeltaHostUnfulfilled  = -1.0
	trustDeltaLeftEarly        = -3.0
	trustDeltaRaidSuccess      = 1.0

	// Per-event applied delta bounds.
	trustAppliedMin = -8.0
	trustAppliedMax = 6.0

	// Staff reports are non-negotiable: fixed weight, no reliability/age factors.
	trustStaffWeight = 2.0

	// Read-time bonus for any active store purchase; never persisted, so it
	// disappears on expiry and a single weighted dislike outweighs it.
	trustStoreBonus = 5.0

	// Public tier thresholds on effective trust.
	trustTierCautionBelow = -10.0
	trustTierTrustedFrom  = 25.0

	// Accounts younger than this with fewer completed lobbies than the
	// minimum always display the neutral tier (anti alt-farming).
	trustNewAccountDays    = 14
	trustNewAccountLobbies = 3

	// Confirm timeouts are logged but free this many times per rolling 24h.
	trustTimeoutFreePer24h = 2

	// Repeat POSITIVE trust between the same two accounts decays.
	//
	// uk_te_vote only makes a vote unique per lobby, so two accounts could raid
	// together over and over, commend each other every time, and each new lobby was
	// a fresh full-weight event. Trust was farmable without bound by a pair.
	//
	// The first trustPairFreeInteractions in the window count in full, because
	// raiding repeatedly with the same people is exactly what real users do. After
	// that each further positive event from the same actor to the same subject is
	// worth half the last, floored so the event is still recorded and visible rather
	// than silently dropped. The series converges, so one pair contributes at most
	// roughly trustPairFreeInteractions + 1 events' worth of trust per window no
	// matter how many raids they stage.
	//
	// Negative events are deliberately NOT decayed: damping repeated dislikes would
	// let a genuinely bad actor outrun their own reputation.
	trustPairWindowDays       = 30
	trustPairFreeInteractions = 3
	trustPairDecayFloor       = 0.05

	// Host lobbies that end without the raid ever starting (expired while
	// open, or cancelled by the host before raiding) are logged but free this
	// many times per rolling window; past that, each further one applies the
	// minor delta.
	trustHostUnfulfilledFreeCount   = 6
	trustHostUnfulfilledWindowHours = 4

	// Reliability recompute window and minimum sample size.
	trustReliabilityWindowDays = 90
	trustReliabilityMinReports = 5
)

// ── Rank ladder (raid XP progression, unchanged from v1) ──────

var raidRanks = []struct {
	xp           int
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

// ── Trust events ──────────────────────────────────────────────

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pairDecay returns a weight multiplier that shrinks as the same actor keeps
// awarding positive trust to the same subject, so a colluding pair cannot farm an
// unbounded score by raiding together repeatedly. See trustPairFreeInteractions.
//
// Returns 1.0 unchanged for negative events, for system events (actorID 0), and
// for self-directed rows, so only the farming case is affected.
func (h *Handlers) pairDecay(userID, actorID uint, rawDelta float64) float64 {
	if rawDelta <= 0 || actorID == 0 || actorID == userID {
		return 1.0
	}

	var prior int
	if err := h.db.QueryRow(`
		SELECT COUNT(*) FROM trust_events
		WHERE actor_id = ? AND user_id = ? AND applied_delta > 0
		  AND created_at > DATE_SUB(NOW(), INTERVAL ? DAY)`,
		actorID, userID, trustPairWindowDays).Scan(&prior); err != nil {
		// Fail closed on the safe side: full weight, same as before this existed.
		log.Printf("pairDecay %d->%d: %v", actorID, userID, err)
		return 1.0
	}

	return pairDecayFactor(prior)
}

// pairDecayFactor is the arithmetic half of pairDecay, split out so the curve can
// be tested without a database. prior is how many positive events this actor has
// already sent this subject inside the window.
func pairDecayFactor(prior int) float64 {
	if prior < trustPairFreeInteractions {
		return 1.0
	}
	factor := math.Pow(0.5, float64(prior-trustPairFreeInteractions+1))
	if factor < trustPairDecayFloor {
		return trustPairDecayFloor
	}
	return factor
}

// insertTrustEvent records an event and bumps the subject's cached score.
// actorID 0 means a system event; lobbyID 0 means no lobby context.
// INSERT IGNORE makes votes idempotent via uk_te_vote: a duplicate
// commend/dislike is dropped silently and the score is not double counted.
func (h *Handlers) insertTrustEvent(userID, actorID uint, lobbyID uint64, eventType string, rawDelta, weight float64, note string) {
	weight *= h.pairDecay(userID, actorID, rawDelta)
	applied := clampF(rawDelta*weight, trustAppliedMin, trustAppliedMax)

	var actor, lobby any
	if actorID > 0 {
		actor = actorID
	}
	if lobbyID > 0 {
		lobby = lobbyID
	}
	res, err := h.db.Exec(`
		INSERT IGNORE INTO trust_events (user_id, actor_id, lobby_id, event_type, raw_delta, weight, applied_delta, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, actor, lobby, eventType, rawDelta, weight, applied, note)
	if err != nil {
		log.Printf("insertTrustEvent %s for user %d: %v", eventType, userID, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // duplicate vote, ignored
	}
	if applied != 0 {
		h.db.Exec(`UPDATE users SET trust_score = trust_score + ? WHERE id = ?`, applied, userID)
	}
}

// logConfirmTimeout records a queue-confirm timeout. The first
// trustTimeoutFreePer24h timeouts in any rolling 24h window are logged with
// zero weight (audit trail, no penalty); further ones apply the full delta.
func (h *Handlers) logConfirmTimeout(userID uint, lobbyID uint64) {
	var recent int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM trust_events
		WHERE user_id = ? AND event_type = 'confirm_timeout'
		  AND created_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)`,
		userID).Scan(&recent)
	weight := 1.0
	note := "confirm window lapsed"
	if recent < trustTimeoutFreePer24h {
		weight = 0
		note = "confirm window lapsed (free pass)"
	}
	h.insertTrustEvent(userID, 0, lobbyID, "confirm_timeout", trustDeltaConfirmTimeout, weight, note)
}

// logHostUnfulfilled records a host lobby that ended without the raid ever
// starting. Lobbies that simply never fill are normal, so the first
// trustHostUnfulfilledFreeCount in any rolling window are logged with zero
// weight (audit trail, no penalty); a host churning through more than that
// takes the minor delta on each further one. Invite-window failures are not
// routed here: they already carry their own per-event penalty.
func (h *Handlers) logHostUnfulfilled(hostID uint, lobbyID uint64) {
	var recent int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM trust_events
		WHERE user_id = ? AND event_type = 'host_unfulfilled'
		  AND created_at > DATE_SUB(NOW(), INTERVAL ? HOUR)`,
		hostID, trustHostUnfulfilledWindowHours).Scan(&recent)
	weight := 1.0
	note := "lobby ended without raid starting (repeated)"
	if recent < trustHostUnfulfilledFreeCount {
		weight = 0
		note = "lobby ended without raid starting (free pass)"
	}
	h.insertTrustEvent(hostID, 0, lobbyID, "host_unfulfilled", trustDeltaHostUnfulfilled, weight, note)
}

func (h *Handlers) peerReportWeight(actor *auth.User, subjectID uint) float64 {
	if actor.IsMod() {
		return trustStaffWeight
	}

	var actorCreated, subjectCreated time.Time
	var reliability float64
	if err := h.db.QueryRow(`SELECT created_at, COALESCE(rater_weight, 1.0) FROM users WHERE id = ?`, actor.ID).
		Scan(&actorCreated, &reliability); err != nil {
		return 1.0
	}
	if err := h.db.QueryRow(`SELECT created_at FROM users WHERE id = ?`, subjectID).
		Scan(&subjectCreated); err != nil {
		return reliability
	}

	actorDays := time.Since(actorCreated).Hours() / 24
	subjectDays := time.Since(subjectCreated).Hours() / 24

	base := clampF(actorDays/90, 0.25, 1.0)
	relative := 1.0
	if actorDays < subjectDays && subjectDays > 0 {
		relative = clampF(actorDays/subjectDays, 0.5, 1.0)
	}
	return clampF(reliability, 0.1, 1.5) * base * relative
}

func roleWeightCaps(u *auth.User) (mul, cap float64) {
	switch {
	case u.IsSuperAdmin() || u.IsAdmin():
		return 1.5, 1.5
	case u.IsMod():
		return 1.4, 1.4
	case u.IsTester():
		return 1.2, 1.2
	}
	return 1.0, 1.0
}

// recomputeReliability updates the actor's report reliability
// (users.rater_weight) after they give a commend/dislike. A user who
// mass-dislikes while being generally liked themselves loses report weight
// quickly, but their own trust score is untouched: their targets are
// protected because every report they give is multiplied by this weight.
func (h *Handlers) recomputeReliability(u *auth.User) {
	var given, dislikes int
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(event_type = 'dislike'), 0)
		FROM trust_events
		WHERE actor_id = ? AND event_type IN ('commend','dislike')
		  AND created_at > DATE_SUB(NOW(), INTERVAL ? DAY)`,
		u.ID, trustReliabilityWindowDays).Scan(&given, &dislikes)
	if given < trustReliabilityMinReports {
		return
	}

	var received float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(applied_delta), 0)
		FROM trust_events
		WHERE user_id = ? AND event_type IN ('commend','dislike')
		  AND created_at > DATE_SUB(NOW(), INTERVAL ? DAY)`,
		u.ID, trustReliabilityWindowDays).Scan(&received)

	negRatio := float64(dislikes) / float64(given)
	slope := 1.2
	if received >= 0 {
		slope = 2.0
	}
	penalty := math.Max(0, negRatio-0.40) * slope

	roleMul, roleCap := roleWeightCaps(u)
	reliability := clampF(1.0-penalty, 0.1, 1.0) * roleMul
	if reliability > roleCap {
		reliability = roleCap
	}
	h.db.Exec(`UPDATE users SET rater_weight = ? WHERE id = ?`, reliability, u.ID)
}

// recomputeTrustScore re-sums the event log into the cached column (admin repair).
func (h *Handlers) recomputeTrustScore(userID uint) {
	h.db.Exec(`
		UPDATE users SET trust_score =
			(SELECT COALESCE(SUM(applied_delta), 0) FROM trust_events WHERE user_id = ?)
		WHERE id = ?`, userID, userID)
}

func (h *Handlers) hasAnyActivePurchase(userID uint) bool {
	var count int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM purchases
		WHERE user_id = ? AND status = 'completed'
		  AND (expires_at IS NULL OR expires_at > NOW())`,
		userID).Scan(&count)
	return count > 0
}

func (h *Handlers) effectiveTrust(userID uint) float64 {
	var score float64
	h.db.QueryRow(`SELECT COALESCE(trust_score, 0) FROM users WHERE id = ?`, userID).Scan(&score)
	if h.hasAnyActivePurchase(userID) {
		score += trustStoreBonus
	}
	return score
}

func trustTier(effective float64) string {
	switch {
	case effective < trustTierCautionBelow:
		return "caution"
	case effective >= trustTierTrustedFrom:
		return "trusted"
	}
	return "neutral"
}

// trustTierFor is the public display tier: young accounts with few completed
// lobbies always show neutral so alts can't farm (or tank) a visible tier.
func (h *Handlers) trustTierFor(userID uint) string {
	tier := trustTier(h.effectiveTrust(userID))
	if tier == "neutral" {
		return tier
	}
	var ageDays float64
	var completed int
	h.db.QueryRow(`SELECT TIMESTAMPDIFF(HOUR, created_at, NOW()) / 24 FROM users WHERE id = ?`, userID).Scan(&ageDays)
	h.db.QueryRow(`SELECT COUNT(*) FROM raid_lobby_members WHERE user_id = ? AND attended = 1`, userID).Scan(&completed)
	if ageDays < trustNewAccountDays && completed < trustNewAccountLobbies {
		return "neutral"
	}
	return tier
}
