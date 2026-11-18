package handlers

// Trade Assist, Phase 9: safety and staff. Plan: .claude/trade-assist-build-plan-11-18-26.md.
//
//   - Blocking someone ends every open or agreed conversation between the two (endTradeThreads,
//     called from APIBlock beside endTradeContacts). A blocker who is the silent side of a trade the
//     partner already confirmed gets the 'denied' record: a block cannot dodge it.
//   - A player report can point at the conversation it is about (CreatePlayerReport).
//   - Staff (moderators and up) get /admin/trades: trainers flagged as possibly padding their
//     collection (owner decision C2: 3 uncleared unverified records within 90 days), possible spam,
//     running cooldowns (start one, or clear one), the unverified records (clear one), and a
//     read only view of any conversation, hidden ones included.
//
// Nothing here is shown to players, and the cooldown's growing lengths are never shown to anyone
// (owner, 2026-11-18): staff see only "possible spam or griefer" and whether a cooldown is running.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/i18n"
)

const (
	tradeFlagCount  = 3
	tradeFlagWindow = 90 * 24 * time.Hour
)

// endTradeThreads closes every open or agreed conversation between two trainers when one blocks the
// other: a closing message, then hidden from both (staff still see it). If the blocker was the
// silent side of a trade their partner had already confirmed, it closes as unconfirmed with the
// blocker's 'denied' record instead of a plain cancel.
func (h *Handlers) endTradeThreads(blocker, blocked uint, now time.Time) error {
	now = now.UTC().Truncate(time.Second)
	rows, err := h.db.Query(`SELECT `+tradeThreadCols+` FROM trade_threads
		WHERE state IN ('open', 'agreed')
		  AND ((a_user = ? AND b_user = ?) OR (a_user = ? AND b_user = ?))`, blocker, blocked, blocked, blocker)
	if err != nil {
		return err
	}
	var list []tradeThread
	for rows.Next() {
		t, err := scanTradeThread(rows)
		if err != nil {
			rows.Close()
			return err
		}
		list = append(list, t)
	}
	rows.Close()
	for _, t := range list {
		s := t.side(blocker)
		tx, err := h.db.Begin()
		if err != nil {
			return err
		}
		// Decided under a row lock, so a confirm landing meanwhile is seen (Phase 9 review).
		var cur, aC, bC string
		if err := tx.QueryRow(`SELECT state, a_confirm, b_confirm FROM trade_threads WHERE id = ? FOR UPDATE`, t.ID).Scan(&cur, &aC, &bC); err != nil {
			tx.Rollback()
			return err
		}
		if cur != "open" && cur != "agreed" {
			tx.Rollback()
			continue
		}
		t.State = cur
		dodge := cur == "agreed" && pick(otherSide(s), aC, bC) == "done" && pick(s, aC, bC) == "none"
		state := "cancelled"
		if dodge {
			state = "unconfirmed"
		}
		res, err := tx.Exec(`UPDATE trade_threads SET state = ?, hidden_at = ? WHERE id = ? AND state = ?`, state, now, t.ID, t.State)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				tx.Rollback()
				continue
			}
		}
		if err == nil && dodge {
			_, err = tx.Exec(`INSERT INTO trade_unverified (user_id, thread_id, kind, created_at) VALUES (?, ?, 'denied', ?)`, blocker, t.ID, now)
		}
		if err == nil {
			err = addThreadMessage(tx, t.ID, 0, "sys_closed_block", nil, now)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// alertStaffTradeSpam tells staff a trainer may be spamming or griefing, for monitoring: a push to
// the moderators' bucket and a row on the staff Trades page. It says nothing about levels or
// lengths (owner, 2026-11-18).
func (h *Handlers) alertStaffTradeSpam(userID uint) {
	var username string
	h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, userID).Scan(&username)
	log.Printf("trades: possible spam or griefer, user %d, for staff monitoring", userID)
	go h.sendPushToUsersOnChannel(h.bucketStaffIDs(), "Possible spam or griefer",
		"@"+username+" is sending trade requests very quickly. Worth keeping an eye on.",
		map[string]string{"type": "trade_spam", "username": username}, pushChannelAdmin)
}

// ---------- The staff page ----------

type staffTradeUser struct {
	UserID   uint64
	Username string
	Count    int
	Last     time.Time
}

type staffTradeCooldown struct {
	UserID    uint64
	Username  string
	Until     time.Time // shown only for a staff cooldown; an automatic one shows only that it runs
	Automatic bool
	SetBy     string
}

type staffTradeRecord struct {
	ID       uint64
	Username string
	Kind     string
	ThreadID uint64
	At       time.Time
}

type staffTradeMessage struct {
	From string
	Text string
	Key  string
	At   time.Time
}

type staffTradeThread struct {
	ID            uint64
	A, B          string
	State         string
	AOffer        string
	BOffer        string
	AConfirm      string
	BConfirm      string
	Hidden        bool
	Created, Last time.Time
	Messages      []staffTradeMessage
	Reports       []uint64
}

type staffTradesData struct {
	Flagged    []staffTradeUser
	Spam       []staffTradeUser
	Cooldowns  []staffTradeCooldown
	Records    []staffTradeRecord
	Thread     *staffTradeThread
	Message    string
	MessageOK  bool
	FlagCount  int
	FlagWindow int
}

// AdminTradesPage is the staff Trades page (RequireMod).
func (h *Handlers) AdminTradesPage(w http.ResponseWriter, r *http.Request) {
	d, err := h.staffTradesData(time.Now())
	if err != nil {
		log.Printf("admin trades: %v", err)
		http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if m, ok := adminTradesMessages[r.URL.Query().Get("m")]; ok {
		d.Message, d.MessageOK = m.text, m.ok
	}
	h.render(w, r, "admin_trades", d)
}

func (h *Handlers) staffTradesData(now time.Time) (staffTradesData, error) {
	now = now.UTC()
	d := staffTradesData{FlagCount: tradeFlagCount, FlagWindow: int(tradeFlagWindow.Hours() / 24)}
	rows, err := h.db.Query(`
		SELECT tu.user_id, u.username, COUNT(*), MAX(tu.created_at)
		FROM trade_unverified tu JOIN users u ON u.id = tu.user_id
		WHERE tu.cleared_by IS NULL AND tu.created_at >= ?
		GROUP BY tu.user_id, u.username HAVING COUNT(*) >= ?
		ORDER BY COUNT(*) DESC, MAX(tu.created_at) DESC`, now.Add(-tradeFlagWindow), tradeFlagCount)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var x staffTradeUser
		if err := rows.Scan(&x.UserID, &x.Username, &x.Count, &x.Last); err != nil {
			rows.Close()
			return d, err
		}
		d.Flagged = append(d.Flagged, x)
	}
	rows.Close()

	// Possible spam: trainers whose automatic cooldowns reached the stage staff are told about. The
	// level itself is never shown.
	rows, err = h.db.Query(`SELECT c.user_id, u.username, c.reason, c.created_at, c.until_at, COALESCE(s.username, '')
		FROM trade_cooldowns c JOIN users u ON u.id = c.user_id LEFT JOIN users s ON s.id = c.set_by
		ORDER BY c.until_at DESC`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var id uint64
		var username, reason, setBy string
		var created, until time.Time
		if err := rows.Scan(&id, &username, &reason, &created, &until, &setBy); err != nil {
			rows.Close()
			return d, err
		}
		level := parseAutoCooldownLevel(reason)
		// Only that the system alerted, never when: "alerted" next to "until" would give away the
		// cooldown's length, which nobody may see (owner decision C3, Phase 9 review).
		if level >= tradeAlertLevel {
			d.Spam = append(d.Spam, staffTradeUser{UserID: id, Username: username})
		}
		if until.After(now) {
			d.Cooldowns = append(d.Cooldowns, staffTradeCooldown{UserID: id, Username: username, Until: until, Automatic: level > 0, SetBy: setBy})
		}
	}
	rows.Close()

	rows, err = h.db.Query(`SELECT tu.id, u.username, tu.kind, tu.thread_id, tu.created_at
		FROM trade_unverified tu JOIN users u ON u.id = tu.user_id
		WHERE tu.cleared_by IS NULL AND tu.created_at >= ?
		ORDER BY tu.created_at DESC LIMIT 200`, now.Add(-tradeFlagWindow))
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var x staffTradeRecord
		if err := rows.Scan(&x.ID, &x.Username, &x.Kind, &x.ThreadID, &x.At); err != nil {
			rows.Close()
			return d, err
		}
		d.Records = append(d.Records, x)
	}
	rows.Close()
	return d, nil
}

// AdminTradeThread is the read only view of one conversation for staff (RequireMod), hidden ones
// included, with every message in English.
func (h *Handlers) AdminTradeThread(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	t, err := scanTradeThread(h.db.QueryRow(`SELECT `+tradeThreadCols+` FROM trade_threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	en := i18n.TFunc("en")
	v := &staffTradeThread{ID: t.ID, A: t.AName, B: t.BName, State: t.State, AOffer: t.ALabel, BOffer: t.BLabel,
		AConfirm: t.AConfirm, BConfirm: t.BConfirm, Hidden: t.Hidden.Valid, Created: t.Created, Last: t.LastMessage}
	rows, err := h.db.Query(`SELECT from_user, preset_key, params, created_at FROM trade_messages WHERE thread_id = ? ORDER BY id`, t.ID)
	if err != nil {
		http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var from sql.NullInt64
		var key string
		var params sql.NullString
		var at time.Time
		if err := rows.Scan(&from, &key, &params, &at); err != nil {
			rows.Close()
			http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		who := "System"
		switch {
		case from.Valid && t.A.Valid && from.Int64 == t.A.Int64:
			who = t.AName
		case from.Valid && t.B.Valid && from.Int64 == t.B.Int64:
			who = t.BName
		case from.Valid:
			who = "(account removed)"
		}
		if !from.Valid && !strings.HasPrefix(key, "sys_") {
			who = "(account removed)"
		}
		v.Messages = append(v.Messages, staffTradeMessage{From: who, Key: key, At: at,
			Text: tradePresetText(en, key, withSideName(params.String, t.AName, t.BName))})
	}
	rows.Close()
	rr, err := h.db.Query(`SELECT id FROM bug_reports WHERE trade_thread_id = ? ORDER BY id`, t.ID)
	if err == nil {
		for rr.Next() {
			var rid uint64
			if rr.Scan(&rid) == nil {
				v.Reports = append(v.Reports, rid)
			}
		}
		rr.Close()
	}
	h.render(w, r, "admin_trades", staffTradesData{Thread: v})
}

// withSideName adds "name" to a server message's params from its "side", for the staff view.
func withSideName(params, aName, bName string) string {
	var p map[string]any
	if params == "" || json.Unmarshal([]byte(params), &p) != nil {
		return params
	}
	switch p["side"] {
	case "a":
		p["name"] = "@" + aName
	case "b":
		p["name"] = "@" + bName
	default:
		return params
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// tradePresetText renders one message in a language: the preset's text with its params filled in,
// or, until a preset has text (Phase 11), its key and params as they are.
func tradePresetText(t func(string) string, key, params string) string {
	var p map[string]any
	if params != "" {
		json.Unmarshal([]byte(params), &p)
	}
	k := "trade.preset." + key
	text := t(k)
	if text == k {
		text = key
		if params != "" {
			text += " " + params
		}
		return text
	}
	for name, val := range p {
		text = strings.ReplaceAll(text, "{"+name+"}", toString(val))
	}
	return text
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------- Staff actions (POST forms, RequireMod, CSRF) ----------

// adminTradesMessages are the only texts the page shows after an action. The link carries a
// code, never the text, so a crafted link cannot put words on a staff page (Phase 9 review).
var adminTradesMessages = map[string]struct {
	text string
	ok   bool
}{
	"hours":       {"Pick between 1 and 720 hours.", false},
	"no_user":     {"No trainer with that username.", false},
	"staff":       {"Staff cannot be put in a cooldown.", false},
	"self":        {"You cannot clear your own cooldown or records. Ask another staff member.", false},
	"save":        {"Could not save. Please try again.", false},
	"started":     {"The cooldown is running.", true},
	"cleared":     {"Cooldown cleared.", true},
	"record":      {"Record cleared.", true},
	"record_done": {"That record was already cleared.", false},
	"bad_input":   {"That request was not valid.", false},
}

func (h *Handlers) adminTradesRedirect(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/admin/trades?m="+url.QueryEscape(code), http.StatusSeeOther)
}

// AdminTradeCooldownStart puts a trainer in a staff cooldown for some hours (1 to 720). Staff and the
// superadmin can never be targeted: the check uses the username STORED in the database, so a
// different spelling of the superadmin's name cannot slip past it (Phase 9 review). A running
// automatic cooldown keeps its level (the escalation history) and runs at least as long as asked.
func (h *Handlers) AdminTradeCooldownStart(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Error(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	hours, err := strconv.Atoi(r.FormValue("hours"))
	if err != nil || hours < 1 || hours > 720 {
		h.adminTradesRedirect(w, r, "hours")
		return
	}
	var id uint
	var username, role string
	if err := h.db.QueryRow(`SELECT id, username, role FROM users WHERE username = ?`, strings.TrimSpace(r.FormValue("username"))).
		Scan(&id, &username, &role); err != nil {
		h.adminTradesRedirect(w, r, "no_user")
		return
	}
	target := auth.User{Username: username, Role: role}
	if target.IsMod() || strings.EqualFold(username, auth.SuperadminUser) {
		h.adminTradesRedirect(w, r, "staff")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Duration(hours) * time.Hour)
	var reason string
	var curUntil time.Time
	err = h.db.QueryRow(`SELECT reason, until_at FROM trade_cooldowns WHERE user_id = ?`, id).Scan(&reason, &curUntil)
	switch {
	case err == nil && parseAutoCooldownLevel(reason) > 0:
		// Keep the automatic row (its level is the escalation history); only lengthen it.
		if curUntil.Before(until) {
			_, err = h.db.Exec(`UPDATE trade_cooldowns SET until_at = ?, set_by = ? WHERE user_id = ?`, until, u.ID, id)
		} else {
			err = nil
		}
	case err == nil || errors.Is(err, sql.ErrNoRows):
		_, err = h.db.Exec(`INSERT INTO trade_cooldowns (user_id, until_at, reason, set_by, created_at) VALUES (?, ?, 'staff', ?, ?)
			ON DUPLICATE KEY UPDATE until_at = VALUES(until_at), reason = 'staff', set_by = VALUES(set_by), created_at = VALUES(created_at)`,
			id, until, u.ID, now)
	}
	if err != nil {
		h.adminTradesRedirect(w, r, "save")
		return
	}
	log.Printf("admin trades: %s put @%s in a %dh cooldown", u.Username, username, hours)
	h.adminTradesRedirect(w, r, "started")
}

// AdminTradeCooldownClear ends a trainer's cooldown. A staff cooldown is removed; an automatic one
// is ended but its row stays, so the escalation history is not wiped by a clear (Phase 9 review).
// Nobody clears their own.
func (h *Handlers) AdminTradeCooldownClear(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Error(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(r.FormValue("user_id"), 10, 64)
	if err != nil {
		h.adminTradesRedirect(w, r, "bad_input")
		return
	}
	if uint(id) == u.ID {
		h.adminTradesRedirect(w, r, "self")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := h.db.Exec(`UPDATE trade_cooldowns SET until_at = ? WHERE user_id = ? AND reason LIKE 'auto\_rapid\_%' AND until_at > ?`, now, id, now); err != nil {
		h.adminTradesRedirect(w, r, "save")
		return
	}
	if _, err := h.db.Exec(`DELETE FROM trade_cooldowns WHERE user_id = ? AND reason NOT LIKE 'auto\_rapid\_%'`, id); err != nil {
		h.adminTradesRedirect(w, r, "save")
		return
	}
	log.Printf("admin trades: %s cleared the cooldown of user %d", u.Username, id)
	h.adminTradesRedirect(w, r, "cleared")
}

// AdminTradeRecordClear clears one "possibly traded, unverified" record (it then no longer counts
// toward a flag). The row stays, with who cleared it.
func (h *Handlers) AdminTradeRecordClear(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Error(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(r.FormValue("id"), 10, 64)
	if err != nil {
		h.adminTradesRedirect(w, r, "bad_input")
		return
	}
	// Never one's own record: a moderator must not be able to clear their own flag (Phase 9 review).
	res, err := h.db.Exec(`UPDATE trade_unverified SET cleared_by = ? WHERE id = ? AND cleared_by IS NULL AND user_id <> ?`, u.ID, id, u.ID)
	if err != nil {
		h.adminTradesRedirect(w, r, "save")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var owner uint
		if h.db.QueryRow(`SELECT user_id FROM trade_unverified WHERE id = ?`, id).Scan(&owner) == nil && owner == u.ID {
			h.adminTradesRedirect(w, r, "self")
			return
		}
		h.adminTradesRedirect(w, r, "record_done")
		return
	}
	log.Printf("admin trades: %s cleared unverified record %d", u.Username, id)
	h.adminTradesRedirect(w, r, "record")
}
