package handlers

// Database tests for the Trade Assist checks. They run ONLY against a throwaway local MySQL
// loaded from schema.sql, never the production database:
//
//	TRADE_TEST_DB=127.0.0.1:33099/fresh56 go test ./internal/handlers/ -run TradeDB
//
// The address must be 127.0.0.1 or localhost; anything else is refused. Without the variable
// the tests are skipped, so a normal `go test ./...` never needs a database.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func tradeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	spec := os.Getenv("TRADE_TEST_DB")
	if spec == "" {
		t.Skip("TRADE_TEST_DB not set (a throwaway local MySQL, host:port/dbname)")
	}
	addr, name, ok := strings.Cut(spec, "/")
	if !ok || name == "" || !(strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:")) {
		t.Fatalf("TRADE_TEST_DB must be a LOCAL throwaway database (127.0.0.1:port/name), got %q", spec)
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Net, cfg.Addr, cfg.DBName, cfg.ParseTime = "root", "tcp", addr, name, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// user_follows and user_blocks exist only in migrate.sql (known schema drift), so a
	// database built from schema.sql lacks them.
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS user_follows (user_id INT UNSIGNED NOT NULL, friend_id INT UNSIGNED NOT NULL,
		   created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (user_id, friend_id),
		   FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		   FOREIGN KEY (friend_id) REFERENCES users(id) ON DELETE CASCADE) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS user_blocks (user_id INT UNSIGNED NOT NULL, blocked_id INT UNSIGNED NOT NULL,
		   created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (user_id, blocked_id),
		   FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		   FOREIGN KEY (blocked_id) REFERENCES users(id) ON DELETE CASCADE) ENGINE=InnoDB`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// tradeUsers makes n fresh trainers for one test and deletes them (cascading their rows) after.
func tradeUsers(t *testing.T, db *sql.DB, n int) []uint {
	t.Helper()
	ids := make([]uint, n)
	tag := fmt.Sprintf("%s%d", strings.ToLower(t.Name()), time.Now().UnixNano()%1e9)
	if len(tag) > 24 {
		tag = tag[len(tag)-24:]
	}
	for i := range ids {
		name := fmt.Sprintf("%s_%d", tag, i)
		res, err := db.Exec(`INSERT INTO users (username, email, password) VALUES (?, ?, 'x')`, name, name+"@example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids[i] = uint(id)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			db.Exec(`DELETE FROM trade_threads WHERE a_user = ? OR b_user = ?`, id, id)
			db.Exec(`DELETE FROM users WHERE id = ?`, id)
		}
	})
	return ids
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestTradeDBRelation(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 3)
	a, b, c := u[0], u[1], u[2]

	mustExec(t, db, `INSERT INTO user_follows (user_id, friend_id) VALUES (?, ?), (?, ?), (?, ?)`, a, b, b, a, a, c)
	if rel, err := h.relation(a, b); err != nil || !rel.Friends || rel.Blocked {
		t.Errorf("mutual follow: %+v %v, want friends", rel, err)
	}
	if rel, _ := h.relation(a, c); rel.Friends {
		t.Error("a one way follow counted as friends")
	}
	mustExec(t, db, `INSERT INTO user_blocks (user_id, blocked_id) VALUES (?, ?)`, c, a)
	if rel, _ := h.relation(a, c); !rel.Blocked {
		t.Error("being blocked by the other trainer did not count")
	}
	if rel, _ := h.relation(c, a); !rel.Blocked {
		t.Error("blocking the other trainer did not count")
	}
}

func TestTradeDBCanTradeAndContact(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	now := time.Now().UTC().Truncate(time.Second)
	u := tradeUsers(t, db, 5)
	me, friend, contact, stranger, gone := u[0], u[1], u[2], u[3], u[4]

	mustExec(t, db, `INSERT INTO user_follows (user_id, friend_id) VALUES (?, ?), (?, ?)`, me, friend, friend, me)
	mustExec(t, db, `INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'accepted', ?, ?)`,
		contact, me, now.Add(-time.Hour), now.Add(tradeContactTTL))
	mustExec(t, db, `UPDATE users SET deleted_at = ? WHERE id = ?`, now, gone)

	for _, c := range []struct {
		name   string
		target uint
		want   bool
	}{{"friend", friend, true}, {"accepted contact", contact, true}, {"stranger", stranger, false}} {
		if got, err := h.canTrade(me, c.target, now); err != nil || got != c.want {
			t.Errorf("canTrade %s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	// An accepted contact past its 15 days no longer counts.
	mustExec(t, db, `UPDATE trade_contacts SET expires_at = ? WHERE from_user = ?`, now.Add(-time.Minute), contact)
	if got, _ := h.canTrade(me, contact, now); got {
		t.Error("an expired contact still allowed a trade")
	}

	if err := h.canContact(me, me, now); err != errTradeSelf {
		t.Errorf("self: %v", err)
	}
	if err := h.canContact(me, gone, now); err != errTradeNoUser {
		t.Errorf("deleted account: %v", err)
	}
	if err := h.canContact(me, stranger, now); err != nil {
		t.Errorf("stranger: %v, want allowed to ask", err)
	}
	mustExec(t, db, `INSERT INTO user_blocks (user_id, blocked_id) VALUES (?, ?)`, stranger, me)
	if err := h.canContact(me, stranger, now); err != errTradeBlocked {
		t.Errorf("blocked: %v", err)
	}
	mustExec(t, db, `INSERT INTO trade_cooldowns (user_id, until_at, reason, created_at) VALUES (?, ?, 'staff', ?)`, me, now.Add(time.Hour), now)
	if err := h.canContact(me, friend, now); err != errTradeCooldown {
		t.Errorf("in a cooldown: %v", err)
	}
}

func TestTradeDBRequestCounts(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	now := time.Now().UTC().Truncate(time.Second)
	u := tradeUsers(t, db, 5)
	me, x, y, z, w := u[0], u[1], u[2], u[3], u[4]

	// Today: a pending contact, an expired pending contact (old), and two conversations, one
	// answered. Yesterday: an old contact that no longer counts toward today.
	mustExec(t, db, `INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES
		(?, ?, 'pending', ?, ?), (?, ?, 'pending', ?, ?)`,
		me, x, now.Add(-time.Hour), now.Add(tradeContactTTL),
		me, y, now.Add(-20*24*time.Hour), now.Add(-5*24*time.Hour))
	mustExec(t, db, `INSERT INTO trade_threads (a_user, b_user, a_name, b_name, created_at, last_message_at) VALUES
		(?, ?, 'me', 'z', ?, ?), (?, ?, 'me', 'w', ?, ?)`,
		me, z, now.Add(-2*time.Hour), now.Add(-2*time.Hour), me, w, now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	var answered int64
	db.QueryRow(`SELECT id FROM trade_threads WHERE a_user = ? AND b_user = ?`, me, w).Scan(&answered)
	mustExec(t, db, `INSERT INTO trade_messages (thread_id, from_user, preset_key, created_at) VALUES (?, ?, 'agree', ?)`, answered, w, now)

	if n, err := h.requestsToday(me, now); err != nil || n != 3 {
		t.Errorf("requestsToday = %d %v, want 3 (one contact, two conversations)", n, err)
	}
	if n, err := h.requestsWaiting(me, now); err != nil || n != 2 {
		t.Errorf("requestsWaiting = %d %v, want 2 (pending contact, unanswered conversation)", n, err)
	}

	// Review finding 2026-11-18: requests to a trainer who is gone hold no slot. Deleting x
	// frees the pending contact; purging w (b_user and their reply go NULL) must not turn the
	// answered conversation back into a waiting one.
	mustExec(t, db, `UPDATE users SET deleted_at = ? WHERE id = ?`, now, x)
	mustExec(t, db, `DELETE FROM users WHERE id = ?`, w)
	if n, err := h.requestsWaiting(me, now); err != nil || n != 1 {
		t.Errorf("after x deleted and w purged: requestsWaiting = %d %v, want 1 (only z)", n, err)
	}
}

func TestTradeDBCooldownEscalation(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 1)
	me := u[0]
	now := time.Now().UTC().Truncate(time.Second)

	// Each trip shortly after the last ends climbs one level.
	for want := 1; want <= 5; want++ {
		level, err := h.startAutoCooldown(me, now)
		if err != nil || level != want {
			t.Fatalf("trip %d: level %d %v", want, level, err)
		}
		until, _ := h.tradeCooldownUntil(me, now)
		if got := until.Sub(now); got != tradeCooldownFor(want) {
			t.Fatalf("level %d lasts %v, want %v", want, got, tradeCooldownFor(want))
		}
		now = until.Add(time.Minute) // the next trip comes a minute after this one ends
	}
	// A quiet day after it ends starts over at level 1.
	now = now.Add(25 * time.Hour)
	if level, _ := h.startAutoCooldown(me, now); level != 1 {
		t.Errorf("after a quiet day: level %d, want 1", level)
	}
	// A running staff cooldown is never replaced by an automatic one.
	mustExec(t, db, `UPDATE trade_cooldowns SET reason = 'staff', until_at = ? WHERE user_id = ?`, now.Add(48*time.Hour), me)
	if level, _ := h.startAutoCooldown(me, now); level != 0 {
		t.Errorf("staff cooldown replaced at level %d", level)
	}
	var reason string
	db.QueryRow(`SELECT reason FROM trade_cooldowns WHERE user_id = ?`, me).Scan(&reason)
	if reason != "staff" {
		t.Errorf("reason now %q, want staff", reason)
	}
}

func TestTradeDBRequestLimits(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 12)
	me := u[0]
	now := time.Now().UTC().Truncate(time.Second)

	// Pin the community size so the smallest tier (10 a day, 30 waiting) applies.
	tradeGrowth.Lock()
	tradeGrowth.members, tradeGrowth.checked = 7, now
	tradeGrowth.Unlock()

	// Ten contacts spread over the day (slow enough never to trip the rapidity watch).
	for i := 1; i <= 10; i++ {
		mustExec(t, db, `INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'pending', ?, ?)`,
			me, u[i], now.Add(-time.Duration(i)*time.Hour), now.Add(tradeContactTTL))
	}
	if err := h.checkRequestLimits(me, now); err != errTradeDailyLimit {
		t.Errorf("after 10 today: %v, want the daily limit", err)
	}
	// The same ten made yesterday no longer count toward today, and 10 waiting is under 30.
	mustExec(t, db, `UPDATE trade_contacts SET created_at = created_at - INTERVAL 1 DAY WHERE from_user = ?`, me)
	if err := h.checkRequestLimits(me, now.Add(time.Hour)); err != nil {
		t.Errorf("a day later: %v, want allowed", err)
	}
	tradeAttempts.Lock()
	delete(tradeAttempts.by, me)
	tradeAttempts.Unlock()
}

func TestTradeDBTFor(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 1)
	if got := h.tFor(u[0], "admin.pages.trades"); got != "Trade Assist and Messaging (section)" {
		t.Errorf("tFor = %q", got)
	}
	if got := h.tFor(0, "admin.pages.trades"); got != "Trade Assist and Messaging (section)" {
		t.Errorf("tFor unknown user = %q, want English", got)
	}
}

func TestTradeDBOfferRemoved(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 2)
	a, b := u[0], u[1]
	now := time.Now().UTC().Truncate(time.Second)

	shiny := func(owner uint) uint64 {
		res, err := db.Exec(`INSERT INTO user_shinies (user_id, pokemon_id, for_trade) VALUES (?, 'Pikachu', 1)`, owner)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return uint64(id)
	}
	sa, sb, other := shiny(a), shiny(b), shiny(b)
	thread := func(state string) int64 {
		res, err := db.Exec(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, a_offer_shiny_id, b_offer_shiny_id,
			a_offer_label, b_offer_label, a_confirm, b_confirm, created_at, last_message_at)
			VALUES (?, ?, 'a', 'b', ?, ?, ?, 'Pikachu', 'Eevee', 'done', 'none', ?, ?)`, a, b, state, sa, sb, now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	agreed, done := thread("agreed"), thread("completed")

	if err := h.tradeOfferRemoved(sb, now); err != nil {
		t.Fatal(err)
	}
	var state, aConfirm, bLabel string
	var bOffer sql.NullInt64
	db.QueryRow(`SELECT state, a_confirm, b_offer_shiny_id, b_offer_label FROM trade_threads WHERE id = ?`, agreed).Scan(&state, &aConfirm, &bOffer, &bLabel)
	if state != "open" || aConfirm != "none" || bOffer.Valid || bLabel != "" {
		t.Errorf("agreed thread after removal: state=%s a_confirm=%s b_offer=%v label=%q, want open, none, NULL, empty", state, aConfirm, bOffer, bLabel)
	}
	var key, params string
	db.QueryRow(`SELECT preset_key, params FROM trade_messages WHERE thread_id = ? AND from_user IS NULL`, agreed).Scan(&key, &params)
	if key != "sys_offer_removed" || !strings.Contains(params, "Eevee") {
		t.Errorf("server message = %q %s, want sys_offer_removed naming Eevee", key, params)
	}
	// A finished trade is history and is left exactly as it was.
	db.QueryRow(`SELECT state, b_offer_shiny_id FROM trade_threads WHERE id = ?`, done).Scan(&state, &bOffer)
	if state != "completed" || !bOffer.Valid {
		t.Errorf("completed thread was touched: state=%s b_offer=%v", state, bOffer)
	}
	// The A side, in a plain open thread: the offer goes and the side detection picks A. A hidden
	// conversation offering the same shiny is left exactly as it is (Phase 8: hiding ends a trade,
	// so edits and removals never touch hidden ones).
	sa2 := shiny(a)
	old := now.Add(-20 * 24 * time.Hour)
	res, err := db.Exec(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, a_offer_shiny_id, a_offer_label,
		created_at, last_message_at) VALUES (?, ?, 'a', 'b', 'open', ?, 'Mew', ?, ?)`, a, b, sa2, now, now)
	if err != nil {
		t.Fatal(err)
	}
	openThread, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, a_offer_shiny_id, a_offer_label,
		created_at, last_message_at, hidden_at) VALUES (?, ?, 'a', 'b', 'cancelled', ?, 'Mew', ?, ?, ?)`, a, b, sa2, old, old, now)
	if err != nil {
		t.Fatal(err)
	}
	hiddenThread, _ := res.LastInsertId()
	if err := h.tradeOfferRemoved(sa2, now); err != nil {
		t.Fatal(err)
	}
	var aOffer sql.NullInt64
	var lastMsg time.Time
	db.QueryRow(`SELECT a_offer_shiny_id FROM trade_threads WHERE id = ?`, openThread).Scan(&aOffer)
	if aOffer.Valid {
		t.Error("the A side offer was not removed")
	}
	params = ""
	db.QueryRow(`SELECT params FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_offer_removed'`, openThread).Scan(&params)
	if !strings.Contains(params, "Mew") {
		t.Errorf("A side message params = %s, want the A label Mew", params)
	}
	aOffer = sql.NullInt64{}
	db.QueryRow(`SELECT a_offer_shiny_id, last_message_at FROM trade_threads WHERE id = ?`, hiddenThread).Scan(&aOffer, &lastMsg)
	if !aOffer.Valid || !lastMsg.Equal(old) {
		t.Errorf("the hidden conversation was touched: offer=%v last=%v", aOffer, lastMsg)
	}

	// A shiny no trade offers changes nothing.
	var before, after int
	db.QueryRow(`SELECT COUNT(*) FROM trade_messages`).Scan(&before)
	if err := h.tradeOfferRemoved(other, now); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT COUNT(*) FROM trade_messages`).Scan(&after)
	if after != before {
		t.Errorf("removing an unoffered shiny wrote %d messages", after-before)
	}
}

func TestTradeDBOfferModified(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 2)
	a, b := u[0], u[1]
	now := time.Now().UTC().Truncate(time.Second)

	res, err := db.Exec(`INSERT INTO user_shinies (user_id, pokemon_id, for_trade) VALUES (?, 'Pikachu', 1)`, a)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, a_offer_shiny_id, a_offer_label,
		a_confirm, b_confirm, created_at, last_message_at) VALUES (?, ?, 'a', 'b', 'agreed', ?, 'Pikachu', 'done', 'done', ?, ?)`,
		a, b, sid, now, now)
	if err != nil {
		t.Fatal(err)
	}
	thread, _ := res.LastInsertId()

	// An edit that leaves the label as it was (say, only the catch date) changes nothing.
	if n, err := h.tradeOfferModified(uint64(sid), "Pikachu", now); err != nil || n != 0 {
		t.Fatalf("unchanged label: %d %v, want 0", n, err)
	}
	after := tradeOfferLabel("Pikachu", "", "", "Party Hat", "")
	if after != "Pikachu (Party Hat)" {
		t.Fatalf("label = %q", after)
	}
	if n, err := h.tradeOfferModified(uint64(sid), after, now); err != nil || n != 1 {
		t.Fatalf("costume edit: %d %v, want 1 trade modified", n, err)
	}
	var state, aConfirm, bConfirm, label string
	var offer sql.NullInt64
	db.QueryRow(`SELECT state, a_confirm, b_confirm, a_offer_label, a_offer_shiny_id FROM trade_threads WHERE id = ?`, thread).
		Scan(&state, &aConfirm, &bConfirm, &label, &offer)
	if state != "open" || aConfirm != "none" || bConfirm != "none" || label != after || !offer.Valid {
		t.Errorf("after the edit: state=%s confirms=%s/%s label=%q offer=%v, want open, none/none, the new label, still offered",
			state, aConfirm, bConfirm, label, offer)
	}
	var params string
	db.QueryRow(`SELECT params FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_offer_modified'`, thread).Scan(&params)
	var got map[string]string
	if err := json.Unmarshal([]byte(params), &got); err != nil {
		t.Fatalf("message params %q: %v", params, err)
	}
	if got["before"] != "Pikachu" || got["after"] != "Pikachu (Party Hat)" || got["side"] != "a" {
		t.Errorf("message params = %v, want before Pikachu, after Pikachu (Party Hat), side a", got)
	}
}

// Phase 4 review: a block ends connect requests and connections both ways, and an unblock does
// not bring them back.
func TestTradeDBBlockEndsContacts(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 3)
	a, b, c := u[0], u[1], u[2]
	now := time.Now().UTC().Truncate(time.Second)
	mustExec(t, db, `INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES
		(?, ?, 'accepted', ?, ?), (?, ?, 'pending', ?, ?), (?, ?, 'pending', ?, ?)`,
		a, b, now, now.Add(tradeContactTTL), b, a, now, now.Add(tradeContactTTL), a, c, now, now.Add(tradeContactTTL))
	if ok, _ := h.canTrade(a, b, now); !ok {
		t.Fatal("setup: a and b should be connected")
	}
	if err := h.endTradeContacts(b, a, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	if ok, _ := h.canTrade(a, b, later); ok {
		t.Error("still connected after the block ended it")
	}
	if n, _ := h.requestsWaiting(a, later); n != 1 {
		t.Errorf("a's waiting = %d, want 1 (only the request to c)", n)
	}
	var cState string
	db.QueryRow(`SELECT state FROM trade_contacts WHERE from_user = ? AND to_user = ?`, a, c).Scan(&cState)
	if cState != "pending" {
		t.Errorf("an unrelated request became %q", cState)
	}
}

// Phase 8: one sweep over rows of every age does exactly what the rules say, and a second sweep
// changes nothing more.
func TestTradeDBSweep(t *testing.T) {
	db := tradeTestDB(t)
	h := &Handlers{db: db}
	u := tradeUsers(t, db, 2)
	a, b := u[0], u[1]
	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour
	ins := func(q string, args ...any) int64 {
		t.Helper()
		res, err := db.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	// Connect requests: one past its 15 days, one still running.
	oldC := ins(`INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'pending', ?, ?)`, a, b, now.Add(-16*day), now.Add(-day))
	liveC := ins(`INSERT INTO trade_contacts (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'accepted', ?, ?)`, b, a, now.Add(-day), now.Add(10*day))
	thread := func(state, aC, bC string, last time.Time, hidden any) int64 {
		return ins(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, a_confirm, b_confirm, created_at, last_message_at, hidden_at)
			VALUES (?, ?, 'a', 'b', ?, ?, ?, ?, ?, ?)`, a, b, state, aC, bC, last, last, hidden)
	}
	msg := func(th int64, key string, at time.Time) {
		ins(`INSERT INTO trade_messages (thread_id, from_user, preset_key, params, created_at) VALUES (?, NULL, ?, '{"side":"a"}', ?)`, th, key, at)
	}
	remind := thread("agreed", "done", "none", now.Add(-4*day), nil)
	msg(remind, "sys_confirmed", now.Add(-4*day))
	closeIt := thread("agreed", "done", "none", now.Add(-8*day), nil)
	msg(closeIt, "sys_confirmed", now.Add(-8*day))
	idle := thread("open", "none", "none", now.Add(-15*day), nil)
	fresh := thread("open", "none", "none", now.Add(-day), nil)
	ancient := thread("cancelled", "none", "none", now.Add(-500*day), now.Add(-400*day))
	reported := thread("cancelled", "none", "none", now.Add(-500*day), now.Add(-400*day))
	ins(`INSERT INTO bug_reports (type, reporter_id, reported_user_id, subject, trade_thread_id) VALUES ('player', ?, ?, 'test', ?)`, a, b, reported)
	t.Cleanup(func() { db.Exec(`DELETE FROM bug_reports WHERE trade_thread_id = ?`, reported) })
	// Cooldowns: an automatic one ended 2 hours ago (kept: escalation history), one ended 2 days
	// ago (removed); b's staff one ended an hour ago (removed).
	ins(`INSERT INTO trade_cooldowns (user_id, until_at, reason, created_at) VALUES (?, ?, 'auto_rapid_2', ?)`, a, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	ins(`INSERT INTO trade_cooldowns (user_id, until_at, reason, created_at) VALUES (?, ?, 'staff', ?)`, b, now.Add(-time.Hour), now.Add(-day))

	n, err := h.sweepTrades(now)
	if err != nil {
		t.Fatal(err)
	}
	str := func(q string, args ...any) string {
		var s sql.NullString
		db.QueryRow(q, args...).Scan(&s)
		return s.String
	}
	num := func(q string, args ...any) int {
		var x int
		db.QueryRow(q, args...).Scan(&x)
		return x
	}
	if got := str(`SELECT state FROM trade_contacts WHERE id = ?`, oldC); got != "expired" {
		t.Errorf("old contact %q, want expired", got)
	}
	if got := str(`SELECT state FROM trade_contacts WHERE id = ?`, liveC); got != "accepted" {
		t.Errorf("live contact %q, want accepted", got)
	}
	if got := num(`SELECT COUNT(*) FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_reminder'`, remind); got != 1 {
		t.Errorf("reminders on the 4 day trade = %d, want 1", got)
	}
	if got := str(`SELECT state FROM trade_threads WHERE id = ?`, remind); got != "agreed" {
		t.Errorf("the 4 day trade is %q, want still agreed", got)
	}
	if got := str(`SELECT state FROM trade_threads WHERE id = ?`, closeIt); got != "unconfirmed" {
		t.Errorf("the 8 day trade is %q, want unconfirmed", got)
	}
	if got := num(`SELECT COUNT(*) FROM trade_unverified WHERE thread_id = ? AND user_id = ? AND kind = 'silent'`, closeIt, b); got != 1 {
		t.Errorf("silent records for b = %d, want 1", got)
	}
	if got := str(`SELECT hidden_at FROM trade_threads WHERE id = ?`, idle); got == "" {
		t.Error("the 15 day idle conversation was not hidden")
	}
	if got := str(`SELECT state FROM trade_threads WHERE id = ?`, idle); got != "cancelled" {
		t.Errorf("the idle open trade is %q after hiding, want cancelled (its offers freed)", got)
	}
	// The reminder is not news to the confirmer (a): no notice for that trade (Phase 8 review).
	items, _, _, err := h.tradeNoticesFor(a, func(k string) string { return k }, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ThreadID == uint64(remind) {
			t.Errorf("the confirmer got a %q notice for the reminder", it.Kind)
		}
	}
	if got := str(`SELECT hidden_at FROM trade_threads WHERE id = ?`, fresh); got != "" {
		t.Error("a 1 day old conversation was hidden")
	}
	if got := num(`SELECT COUNT(*) FROM trade_threads WHERE id = ?`, ancient); got != 0 {
		t.Error("a conversation hidden 400 days ago was kept")
	}
	if got := num(`SELECT COUNT(*) FROM trade_threads WHERE id = ?`, reported); got != 1 {
		t.Error("a reported conversation was deleted")
	}
	if got := str(`SELECT reason FROM trade_cooldowns WHERE user_id = ?`, a); got != "auto_rapid_2" {
		t.Errorf("a's recent automatic cooldown row is %q, want kept", got)
	}
	if got := num(`SELECT COUNT(*) FROM trade_cooldowns WHERE user_id = ?`, b); got != 0 {
		t.Error("b's ended staff cooldown was kept")
	}
	t.Logf("first sweep: %v", n)

	// A second sweep the same minute changes nothing: no second reminder, no second record.
	n2, err := h.sweepTrades(now)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range n2 {
		if v != 0 {
			t.Errorf("second sweep changed %s: %d", k, v)
		}
	}
	// Two sweeps at the same moment send one reminder (Phase 8 review).
	race := thread("agreed", "done", "none", now.Add(-4*day), nil)
	msg(race, "sys_confirmed", now.Add(-4*day))
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- h.sweepSilentConfirms(now, map[string]int{}) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := num(`SELECT COUNT(*) FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_reminder'`, race); got != 1 {
		t.Errorf("two simultaneous sweeps sent %d reminders, want 1", got)
	}
	// Two days later the old automatic cooldown goes too.
	if _, err := h.sweepTrades(now.Add(2 * day)); err != nil {
		t.Fatal(err)
	}
	if got := num(`SELECT COUNT(*) FROM trade_cooldowns WHERE user_id = ?`, a); got != 0 {
		t.Error("an automatic cooldown ended over a day ago was kept")
	}
}
