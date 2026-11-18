package handlers

// Trade Assist and Messaging: the shared checks every trade endpoint uses.
//
// Plan: .claude/trade-assist-build-plan-11-18-26.md (Phase 2). Trainers mark shinies Willing to
// Trade and arrange trades through preset messages. Nothing a trainer types is ever stored: a
// message is a key from tradePresets plus params the server validated and filled in.
//
// Every query on another trainer goes through tradeLiveUser, so a disabled or deleted account
// never shows up in a trade list, a conversation partner lookup or a request.

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"pogo.hails.cc/internal/i18n"
)

// ---------- Limits ----------

// tradeContactTTL is how long a connect request waits for an answer, and how long an accepted
// one lets the two trainers start trades (owner decision: 15 days).
const tradeContactTTL = 15 * 24 * time.Hour

// Times: every trade query compares against Go's now.UTC(), so every trade write must pass its
// timestamps explicitly from Go as UTC too, never rely on CURRENT_TIMESTAMP. The production
// server's time_zone is SYSTEM (UTC today); explicit values keep it right if that ever changes.

// ---------- Growth monitor ----------
//
// The request limits grow with the community (owner, 2026-11-18): small limits suit a small
// community, a larger one would suffer under them. Size is the number of live accounts
// (registered, not deleted or disabled). users.last_seen_at only moves on signed in web page
// views, not app use, so it would undercount.
//
// tradeLimitTiers is the whole policy: the first tier whose Members the community is BELOW
// applies. Daily counts connect requests and new conversations together (owner decision C4);
// Waiting is how many of those can wait for an answer at once.

type tradeLimitTier struct {
	Members int // applies while the community has fewer live accounts than this
	Daily   int
	Waiting int
}

var tradeLimitTiers = []tradeLimitTier{
	{Members: 250, Daily: 10, Waiting: 30}, // the owner's limits for today's small community
	{Members: 1000, Daily: 15, Waiting: 45},
	{Members: 5000, Daily: 20, Waiting: 60},
	{Members: 20000, Daily: 30, Waiting: 90},
	{Members: math.MaxInt, Daily: 50, Waiting: 150},
}

// tradeLimitsFor is the tier for a community of this many live accounts.
func tradeLimitsFor(members int) tradeLimitTier {
	for _, t := range tradeLimitTiers {
		if members < t.Members {
			return t
		}
	}
	return tradeLimitTiers[len(tradeLimitTiers)-1]
}

// tradeGrowth caches the community size; counting accounts on every request would be waste.
var tradeGrowth = struct {
	sync.Mutex
	members int
	checked time.Time
}{}

const tradeGrowthRefresh = time.Hour

// tradeLimits returns the limits for the community's current size, recounted at most hourly.
// If the count fails, the last known size is kept (and the smallest tier before any count).
func (h *Handlers) tradeLimits(now time.Time) tradeLimitTier {
	tradeGrowth.Lock()
	defer tradeGrowth.Unlock()
	if tradeGrowth.checked.IsZero() || now.Sub(tradeGrowth.checked) >= tradeGrowthRefresh {
		var n int
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM users u WHERE ` + tradeLiveUser).Scan(&n); err == nil {
			tradeGrowth.members = n
		}
		tradeGrowth.checked = now
	}
	return tradeLimitsFor(tradeGrowth.members)
}

// tradeLiveUser is the condition for an account that still exists as far as the site is
// concerned. Use it with the users table aliased as u.
const tradeLiveUser = "u.disabled = 0 AND u.deleted_at IS NULL"

// Errors the endpoints turn into responses. Kept as values so a handler can switch on them.
var (
	errTradeSelf       = errors.New("trade: cannot trade with yourself")
	errTradeNoUser     = errors.New("trade: no such trainer")
	errTradeBlocked    = errors.New("trade: blocked")
	errTradeCooldown   = errors.New("trade: cooling down")
	errTradeDailyLimit = errors.New("trade: daily limit reached")
	errTradeWaitLimit  = errors.New("trade: too many waiting")
	errTradeNotAllowed = errors.New("trade: not friends or connected")
	errTradeBadPreset  = errors.New("trade: unknown message")
	errTradeBadParams  = errors.New("trade: invalid message details")
	errTradeSystemOnly = errors.New("trade: server message")
)

// ---------- Who the two trainers are to each other ----------

// tradeRelation is how two trainers stand: friends means a mutual follow, blocked means
// either one has blocked the other.
type tradeRelation struct {
	Friends bool
	Blocked bool
}

// relation answers friends and blocked-either-way in one query. social.go and trainers.go
// still have their own copies of these checks; they are left alone for now.
func (h *Handlers) relation(viewerID, targetID uint) (tradeRelation, error) {
	var rel tradeRelation
	err := h.db.QueryRow(`
		SELECT
		  EXISTS (SELECT 1 FROM user_follows WHERE user_id = ? AND friend_id = ?)
		  AND EXISTS (SELECT 1 FROM user_follows WHERE user_id = ? AND friend_id = ?),
		  EXISTS (SELECT 1 FROM user_blocks
		          WHERE (user_id = ? AND blocked_id = ?) OR (user_id = ? AND blocked_id = ?))`,
		viewerID, targetID, targetID, viewerID,
		viewerID, targetID, targetID, viewerID,
	).Scan(&rel.Friends, &rel.Blocked)
	return rel, err
}

// tradeTarget looks up a live trainer by username for a trade action.
func (h *Handlers) tradeTarget(username string) (id uint, name string, err error) {
	err = h.db.QueryRow(`SELECT u.id, u.username FROM users u WHERE u.username = ? AND `+tradeLiveUser, username).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errTradeNoUser
	}
	return id, name, err
}

// isLiveUser reports whether this account still exists and is not disabled.
func (h *Handlers) isLiveUser(id uint) (bool, error) {
	var ok bool
	err := h.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = ? AND `+tradeLiveUser+`)`, id).Scan(&ok)
	return ok, err
}

// canContact says whether the viewer may send this trainer anything at all: a live account,
// not themselves, not blocked either way, and the viewer not in a cooldown.
func (h *Handlers) canContact(viewerID, targetID uint, now time.Time) error {
	if viewerID == targetID {
		return errTradeSelf
	}
	live, err := h.isLiveUser(targetID)
	if err != nil {
		return err
	}
	if !live {
		return errTradeNoUser
	}
	rel, err := h.relation(viewerID, targetID)
	if err != nil {
		return err
	}
	if rel.Blocked {
		return errTradeBlocked
	}
	if until, err := h.tradeCooldownUntil(viewerID, now); err != nil {
		return err
	} else if !until.IsZero() {
		return errTradeCooldown
	}
	return nil
}

// canTrade says whether the two may open a trade conversation: friends, or a connect request
// either way that was accepted and has not expired. Call canContact first.
func (h *Handlers) canTrade(viewerID, targetID uint, now time.Time) (bool, error) {
	rel, err := h.relation(viewerID, targetID)
	if err != nil {
		return false, err
	}
	if rel.Blocked {
		return false, nil
	}
	if rel.Friends {
		return true, nil
	}
	var ok bool
	err = h.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM trade_contacts
		  WHERE state = 'accepted' AND expires_at > ?
		    AND ((from_user = ? AND to_user = ?) OR (from_user = ? AND to_user = ?)))`,
		now.UTC(), viewerID, targetID, targetID, viewerID).Scan(&ok)
	return ok, err
}

// ---------- Request limits ----------

// requestsToday counts the connect requests and trade conversations this trainer started in
// the last 24 hours (owner decision C4: both together). The limit is tradeLimits().Daily.
func (h *Handlers) requestsToday(userID uint, now time.Time) (int, error) {
	since := now.UTC().Add(-24 * time.Hour)
	var n int
	err := h.db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM trade_contacts WHERE from_user = ? AND created_at >= ?)
		     + (SELECT COUNT(*) FROM trade_threads  WHERE a_user    = ? AND created_at >= ?)`,
		userID, since, userID, since).Scan(&n)
	return n, err
}

// requestsWaiting counts this trainer's requests still waiting for an answer: connect requests
// not yet answered, and open conversations the other trainer has not replied to.
// Requests to a trainer who is no longer live (disabled, deleted or purged, which nulls b_user)
// do not hold a slot: nobody is left to answer them.
func (h *Handlers) requestsWaiting(userID uint, now time.Time) (int, error) {
	var n int
	err := h.db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM trade_contacts c JOIN users u ON u.id = c.to_user
		         WHERE c.from_user = ? AND c.state = 'pending' AND c.expires_at > ?
		           AND `+tradeLiveUser+`)
		     + (SELECT COUNT(*) FROM trade_threads t JOIN users u ON u.id = t.b_user
		         WHERE t.a_user = ? AND t.state = 'open' AND t.hidden_at IS NULL
		           AND `+tradeLiveUser+`
		           AND NOT EXISTS (SELECT 1 FROM trade_messages m
		                           WHERE m.thread_id = t.id AND m.from_user = t.b_user))`,
		userID, now.UTC(), userID).Scan(&n)
	return n, err
}

// checkRequestLimits is the gate before a new connect request or conversation. It records the
// attempt for the rapidity watch FIRST, refused or not: hammering the button after the limit is
// exactly the griefing the cooldown is for.
func (h *Handlers) checkRequestLimits(userID uint, now time.Time) error {
	if until, err := h.tradeCooldownUntil(userID, now); err != nil {
		return err
	} else if !until.IsZero() {
		return errTradeCooldown
	}
	if started, err := h.noteTradeAttempt(userID, now); err != nil {
		return err
	} else if started {
		return errTradeCooldown
	}
	limits := h.tradeLimits(now)
	today, err := h.requestsToday(userID, now)
	if err != nil {
		return err
	}
	if today >= limits.Daily {
		return errTradeDailyLimit
	}
	waiting, err := h.requestsWaiting(userID, now)
	if err != nil {
		return err
	}
	if waiting >= limits.Waiting {
		return errTradeWaitLimit
	}
	return nil
}

// ---------- Cooldowns (owner decision C3) ----------
//
// Cooldowns are about how FAST requests come, and start small, since someone can just be eager
// to trade. Sending tradeWatchBurst requests within tradeWatchWindow marks the trainer as
// watched for tradeWatchFor. While watched, tradeTripBurst requests within tradeTripWindow
// (counted only after the watch began) start a cooldown. Each cooldown in a run is longer than
// the last; a quiet tradeRunReset after one ends starts the trainer over.
//
// What anyone outside this file sees is deliberately flat: a trainer gets one friendly "Slow
// down" message whatever the level, and staff get "possible spam or griefer" once, with no
// levels or lengths. The growing lengths are only a trigger (owner, 2026-11-18).

const (
	tradeWatchBurst  = 10
	tradeWatchWindow = 30 * time.Minute
	tradeWatchFor    = 24 * time.Hour
	tradeTripBurst   = 5
	tradeTripWindow  = 15 * time.Minute
	tradeRunReset    = 24 * time.Hour
	tradeCooldownCap = 7 * 24 * time.Hour
	// tradeAlertLevel is the level where lengths start doubling, and where staff are told.
	tradeAlertLevel = 4
)

// tradeCooldownFor is the length of the cooldown at a level: 1 minute, 10 minutes, 1 hour, then
// doubling from 2 hours, capped at tradeCooldownCap.
func tradeCooldownFor(level int) time.Duration {
	switch {
	case level <= 1:
		return time.Minute
	case level == 2:
		return 10 * time.Minute
	case level == 3:
		return time.Hour
	}
	steps := level - tradeAlertLevel // 0 at the first doubling level
	if steps > 16 {
		return tradeCooldownCap
	}
	d := time.Duration(math.Pow(2, float64(steps+1))) * time.Hour
	if d > tradeCooldownCap {
		return tradeCooldownCap
	}
	return d
}

// nextTradeCooldownLevel is the level of a new automatic cooldown, given the trainer's previous
// automatic level and when that cooldown ended (zero if none). A quiet tradeRunReset after the
// previous one ended starts over at 1.
func nextTradeCooldownLevel(prevLevel int, prevEnded, now time.Time) int {
	if prevLevel <= 0 || prevEnded.IsZero() || now.Sub(prevEnded) > tradeRunReset {
		return 1
	}
	return prevLevel + 1
}

// tradeRapidity is one trainer's rapidity state. watchFrom is when the watch began and never
// moves while it runs: the watch ends tradeWatchFor after it, however many cooldowns it caused.
// countFrom is where counting toward the next cooldown starts: the watch start, then each trip,
// so the attempts behind one cooldown never start another.
type tradeRapidity struct {
	attempts  []time.Time
	watchFrom time.Time // zero when not watched
	countFrom time.Time
}

// tradeAttempts keeps each trainer's recent request attempts in memory, for the rapidity watch.
// A restart forgets them (and so the watch), never an active cooldown, which is a database row.
var tradeAttempts = struct {
	sync.Mutex
	by        map[uint]tradeRapidity
	lastSweep time.Time
}{by: map[uint]tradeRapidity{}}

// countInWindow is how many of the times fall in (end - window, end] and after from.
func countInWindow(times []time.Time, from, end time.Time, window time.Duration) int {
	n := 0
	for _, t := range times {
		if t.After(from) && t.After(end.Add(-window)) && !t.After(end) {
			n++
		}
	}
	return n
}

// rapidityStep records one attempt and decides what it means. It is pure so it can be tested.
func rapidityStep(st tradeRapidity, now time.Time) (tradeRapidity, bool) {
	var kept []time.Time
	cut := now.Add(-tradeWatchFor)
	for _, t := range st.attempts {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Before(kept[j]) })
	st.attempts = kept

	if !st.watchFrom.IsZero() && now.Sub(st.watchFrom) > tradeWatchFor {
		st.watchFrom, st.countFrom = time.Time{}, time.Time{}
	}
	if st.watchFrom.IsZero() {
		// Not watched: this attempt may complete a burst. A burst only starts the watch; it
		// is never counted toward a cooldown itself.
		if countInWindow(kept, time.Time{}, now, tradeWatchWindow) >= tradeWatchBurst {
			st.watchFrom, st.countFrom = now, now
		}
		return st, false
	}
	if countInWindow(kept, st.countFrom, now, tradeTripWindow) >= tradeTripBurst {
		st.countFrom = now
		return st, true
	}
	return st, false
}

// noteTradeAttempt records an attempt and, when it trips, starts the next automatic cooldown.
// Reaching tradeAlertLevel alerts staff here, so no caller can drop the alert on its error path.
func (h *Handlers) noteTradeAttempt(userID uint, now time.Time) (started bool, err error) {
	tradeAttempts.Lock()
	st, trip := rapidityStep(tradeAttempts.by[userID], now)
	tradeAttempts.by[userID] = st
	// Forget trainers who have gone quiet, so the map only holds the last day's senders.
	if now.Sub(tradeAttempts.lastSweep) > time.Hour {
		for id, s := range tradeAttempts.by {
			if len(s.attempts) == 0 || now.Sub(s.attempts[len(s.attempts)-1]) > tradeWatchFor {
				delete(tradeAttempts.by, id)
			}
		}
		tradeAttempts.lastSweep = now
	}
	tradeAttempts.Unlock()
	if !trip {
		return false, nil
	}
	level, err := h.startAutoCooldown(userID, now)
	if err != nil {
		return false, err
	}
	if level == tradeAlertLevel {
		h.alertStaffTradeSpam(userID)
	}
	return true, nil
}

// autoCooldownReason is how the level is stored, in trade_cooldowns.reason.
func autoCooldownReason(level int) string { return fmt.Sprintf("auto_rapid_%d", level) }

// parseAutoCooldownLevel reads a level back; 0 for anything that is not an automatic cooldown.
func parseAutoCooldownLevel(reason string) int {
	var n int
	if _, err := fmt.Sscanf(reason, "auto_rapid_%d", &n); err != nil || n < 1 {
		return 0
	}
	return n
}

// startAutoCooldown writes the next automatic cooldown and returns its level. A staff cooldown
// that is still running is left alone: staff decided that one.
func (h *Handlers) startAutoCooldown(userID uint, now time.Time) (int, error) {
	now = now.UTC()
	var reason string
	var until time.Time
	err := h.db.QueryRow(`SELECT reason, until_at FROM trade_cooldowns WHERE user_id = ?`, userID).Scan(&reason, &until)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if reason == "staff" && until.After(now) {
		return 0, nil
	}
	level := nextTradeCooldownLevel(parseAutoCooldownLevel(reason), until, now)
	_, err = h.db.Exec(`
		INSERT INTO trade_cooldowns (user_id, until_at, reason, set_by, created_at)
		VALUES (?, ?, ?, NULL, ?)
		ON DUPLICATE KEY UPDATE until_at = VALUES(until_at), reason = VALUES(reason),
		                        set_by = NULL, created_at = VALUES(created_at)`,
		userID, now.Add(tradeCooldownFor(level)), autoCooldownReason(level), now)
	return level, err
}

// tradeCooldownUntil is when this trainer's cooldown ends, or zero if none is running.
func (h *Handlers) tradeCooldownUntil(userID uint, now time.Time) (time.Time, error) {
	var until time.Time
	err := h.db.QueryRow(`SELECT until_at FROM trade_cooldowns WHERE user_id = ? AND until_at > ?`, userID, now.UTC()).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return until, err
}

// ---------- Preset messages ----------

// Param kinds a preset can take. The client sends only what the trainer picked; the server
// fills in everything shown to the other side (names, the trainer code) itself.
const (
	tradeParamNone       = ""
	tradeParamTheirShiny = "their_shiny" // an entry on the OTHER trainer's Willing to Trade list
	tradeParamMyShiny    = "my_shiny"    // an entry on the sender's own Willing to Trade list
	tradeParamWhen       = "when"        // one of tradeWhenValues
	tradeParamCode       = "code"        // the sender's trainer code, filled in by the server
)

// tradePreset describes one message a trainer can send. System presets are written only by the
// server. Every key has locale text at trade.preset.<key>.
type tradePreset struct {
	Param  string
	System bool
}

var tradePresets = map[string]tradePreset{
	// Opening a conversation
	"req_trade_for":  {Param: tradeParamTheirShiny},
	"req_trade_open": {},
	// Offers
	"offer_mine":      {Param: tradeParamMyShiny},
	"ask_other":       {Param: tradeParamTheirShiny},
	"share_list":      {},
	"stop_share_list": {},
	"none_fit":        {},
	// Agreeing and planning
	"agree":             {},
	"when":              {Param: tradeParamWhen},
	"when_ok":           {},
	"when_other":        {},
	"friend_code":       {Param: tradeParamCode},
	"need_good_friends": {},
	"need_stardust":     {},
	"ready":             {},
	// Closing
	"thanks_done": {},
	"cancel":      {},
	"decline":     {},
	// Server only
	"sys_offer_removed": {Param: tradeParamTheirShiny, System: true},
	// The giver edited an offered shiny (form, region, costume or event): the agreement is void
	// and the receiver reviews the change. Params: before, after, side (the giver: "a" or "b").
	"sys_offer_modified": {System: true},
	"sys_agreed":         {System: true},
	// One trainer confirmed (params: side) and the reminder to the other (params: side, the one
	// reminded); both drive owner decision C1 in the Phase 8 sweep.
	"sys_confirmed":    {System: true},
	"sys_reminder":     {System: true},
	"sys_completed":    {System: true},
	"sys_unconfirmed":  {System: true},
	"sys_closed_block": {System: true},
}

// tradeWhenValues are the times a trainer can offer with the "when" preset.
var tradeWhenValues = map[string]bool{"today": true, "this_evening": true, "tomorrow": true, "this_weekend": true}

// tradeMessageInput is the shape a client sends: a key and at most one param.
type tradeMessageInput struct {
	Key     string `json:"preset_key"`
	ShinyID uint   `json:"shiny_id,omitempty"`
	When    string `json:"when,omitempty"`
}

// validateTradeMessage checks a client message against the allowlist: the key exists, is not a
// server message, and carries exactly the param it needs and nothing else. Whether a shiny id
// is really on the right list is checked against the database by the caller (Phase 5).
func validateTradeMessage(in tradeMessageInput) (tradePreset, error) {
	p, ok := tradePresets[in.Key]
	if !ok {
		return p, errTradeBadPreset
	}
	if p.System {
		return p, errTradeSystemOnly
	}
	switch p.Param {
	case tradeParamTheirShiny, tradeParamMyShiny:
		if in.ShinyID == 0 || in.When != "" {
			return p, errTradeBadParams
		}
	case tradeParamWhen:
		if !tradeWhenValues[in.When] || in.ShinyID != 0 {
			return p, errTradeBadParams
		}
	default: // none, or the code the server fills in
		if in.ShinyID != 0 || in.When != "" {
			return p, errTradeBadParams
		}
	}
	return p, nil
}

// ---------- Translation for a recipient ----------

// tFor translates a key into another trainer's language (users.lang), for push notifications
// the server sends on someone else's behalf. English when the lookup fails.
func (h *Handlers) tFor(userID uint, key string) string {
	lang := "en"
	var l string
	// Only an enabled language (Phase 7 review): the site can switch one off, and detectLang would
	// then show that trainer English pages, so their pushes follow.
	if err := h.db.QueryRow(`SELECT lang FROM users WHERE id = ?`, userID).Scan(&l); err == nil && l != "" && (l == "en" || h.langEnabled(l)) {
		lang = l
	}
	return i18n.TFunc(lang)(key)
}

// tradeOfferLabel is how an offered shiny is named in a conversation and in its history: the
// species, then whatever makes it this particular one. It is a snapshot, so a trade still reads
// right after the entry is edited, evolved or deleted.
func tradeOfferLabel(pokemon, form, region, costume, eventTag string) string {
	var extra []string
	for _, x := range []string{region, form, costume, eventTag} {
		if x = strings.TrimSpace(x); x != "" {
			extra = append(extra, x)
		}
	}
	label := pokemon
	if len(extra) > 0 {
		label += " (" + strings.Join(extra, ", ") + ")"
	}
	// The label columns are VARCHAR(96); a costume or event tag can be longer (Phase 5 review).
	if r := []rune(label); len(r) > tradeLabelMax {
		label = string(r[:tradeLabelMax-1]) + "…"
	}
	return label
}

// tradeLabelMax is the length of trade_threads.*_offer_label.
const tradeLabelMax = 96
