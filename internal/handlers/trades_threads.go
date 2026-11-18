package handlers

// Trade Assist, Phase 5: trade conversations, preset messages, agreeing and confirming.
// Plan: .claude/trade-assist-build-plan-11-18-26.md.
//
// A conversation (trade_threads row) is between a_user, who opened it, and b_user. Each side has an
// offer: the shiny THAT side gives (a_offer_shiny_id is what a gives, b_offer_shiny_id what b gives),
// with a label snapshot. States:
//
//	open        talking; offers can change
//	agreed      both offers set and the trainer who did NOT make the last offer change said "agree"
//	completed   both confirmed the in-game trade; the shinies moved between the two collections
//	unconfirmed one confirmed and the other denied (or, after 7 days, stayed silent: Phase 8)
//	declined    the asked trainer declined
//	cancelled   either one cancelled, or both said it did not happen
//
// Every message is a preset key from tradePresets plus params the server built. Nothing typed by a
// trainer is stored.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// More errors the conversation endpoints map to responses (see writeThreadError).
var (
	errThreadNotFound         = errors.New("trade: no such conversation")
	errThreadClosed           = errors.New("trade: conversation closed")
	errThreadNotOnList        = errors.New("trade: shiny not available for trade")
	errThreadNeedOffer        = errors.New("trade: both offers needed")
	errThreadOwnOffer         = errors.New("trade: cannot agree to own offer")
	errThreadNoCode           = errors.New("trade: no trainer code")
	errThreadNotAgreed        = errors.New("trade: not agreed")
	errThreadAnswered         = errors.New("trade: already answered")
	errThreadOfferChanged     = errors.New("trade: an offered shiny changed")
	errThreadPartnerConfirmed = errors.New("trade: partner already confirmed")
)

// tradeThreadPerMinute caps one trainer's messages in one conversation.
const tradeThreadPerMinute = 6

// writeThreadError extends writeTradeError with the conversation errors.
func (h *Handlers) writeThreadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errThreadNotFound):
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
	case errors.Is(err, errTradeNotAllowed):
		writeJSONErrorCode(w, h.t(r, "trade.err.not_allowed"), "not_allowed", http.StatusForbidden)
	case errors.Is(err, errThreadClosed):
		writeJSONErrorCode(w, h.t(r, "trade.err.closed"), "closed", http.StatusConflict)
	case errors.Is(err, errThreadNotOnList):
		writeJSONErrorCode(w, h.t(r, "trade.err.not_on_list"), "not_on_list", http.StatusNotFound)
	case errors.Is(err, errThreadNeedOffer):
		writeJSONErrorCode(w, h.t(r, "trade.err.need_offers"), "need_offers", http.StatusConflict)
	case errors.Is(err, errThreadOwnOffer):
		writeJSONErrorCode(w, h.t(r, "trade.err.wait_partner"), "wait_partner", http.StatusConflict)
	case errors.Is(err, errThreadNoCode):
		writeJSONErrorCode(w, h.t(r, "trade.err.no_code"), "no_code", http.StatusConflict)
	case errors.Is(err, errThreadNotAgreed):
		writeJSONErrorCode(w, h.t(r, "trade.err.not_agreed"), "not_agreed", http.StatusConflict)
	case errors.Is(err, errThreadOfferChanged):
		writeJSONErrorCode(w, h.t(r, "trade.err.offer_changed"), "offer_changed", http.StatusConflict)
	case errors.Is(err, errThreadPartnerConfirmed):
		writeJSONErrorCode(w, h.t(r, "trade.err.partner_confirmed"), "partner_confirmed", http.StatusConflict)
	case errors.Is(err, errThreadAnswered):
		writeJSONErrorCode(w, h.t(r, "trade.err.already_answered"), "already_answered", http.StatusConflict)
	case errors.Is(err, errTradeBadPreset), errors.Is(err, errTradeBadParams), errors.Is(err, errTradeSystemOnly):
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
	default:
		h.writeTradeError(w, r, err)
	}
}

// tradeThread is one conversation row.
type tradeThread struct {
	ID                 uint64
	A, B               sql.NullInt64
	AName, BName       string
	State              string
	AOffer, BOffer     sql.NullInt64
	ALabel, BLabel     string
	AConfirm, BConfirm string
	AShares, BShares   bool
	ARead, BRead       sql.NullTime
	LastMessage        time.Time
	Hidden             sql.NullTime
	Created            time.Time
}

const tradeThreadCols = `id, a_user, b_user, a_name, b_name, state, a_offer_shiny_id, b_offer_shiny_id,
	a_offer_label, b_offer_label, a_confirm, b_confirm, a_shares_list, b_shares_list,
	a_last_read_at, b_last_read_at, last_message_at, hidden_at, created_at`

type rowScanner interface{ Scan(...any) error }

func scanTradeThread(s rowScanner) (tradeThread, error) {
	var t tradeThread
	err := s.Scan(&t.ID, &t.A, &t.B, &t.AName, &t.BName, &t.State, &t.AOffer, &t.BOffer, &t.ALabel, &t.BLabel,
		&t.AConfirm, &t.BConfirm, &t.AShares, &t.BShares, &t.ARead, &t.BRead, &t.LastMessage, &t.Hidden, &t.Created)
	return t, err
}

// side is "a" or "b" for this trainer, "" when they are not in the conversation.
func (t tradeThread) side(userID uint) string {
	switch {
	case t.A.Valid && uint(t.A.Int64) == userID:
		return "a"
	case t.B.Valid && uint(t.B.Int64) == userID:
		return "b"
	}
	return ""
}

func otherSide(s string) string {
	if s == "a" {
		return "b"
	}
	return "a"
}

// pick returns the a or b value of a pair.
func pick[T any](side string, a, b T) T {
	if side == "a" {
		return a
	}
	return b
}

// loadThreadFor returns a conversation the trainer is in and can see (not hidden), and their side.
// Anything else is errThreadNotFound, so ids of other people's conversations reveal nothing.
func (h *Handlers) loadThreadFor(id uint64, userID uint) (tradeThread, string, error) {
	t, err := scanTradeThread(h.db.QueryRow(`SELECT `+tradeThreadCols+` FROM trade_threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, "", errThreadNotFound
	}
	if err != nil {
		return t, "", err
	}
	s := t.side(userID)
	if s == "" || t.Hidden.Valid {
		return t, "", errThreadNotFound
	}
	return t, s, nil
}

// tradeableShiny checks that a shiny is on its owner's Willing to Trade list and returns its label.
// mustBeVisible requires the owner's list to be visible to the asker: a public profile with shiny
// collection shown, or the owner sharing their list in this conversation (Phase 6).
func (h *Handlers) tradeableShiny(ownerID uint, shinyID uint64, mustBeVisible, sharedHere bool) (string, error) {
	var pokemon, form, region, costume, event string
	var public, hidden bool
	err := h.db.QueryRow(`
		SELECT s.pokemon_id, s.form, s.region, s.costume, s.event_tag, u.profile_public, u.shinies_hidden
		FROM user_shinies s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.user_id = ? AND s.for_trade = 1 AND `+tradeLiveUser,
		shinyID, ownerID).Scan(&pokemon, &form, &region, &costume, &event, &public, &hidden)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errThreadNotOnList
	}
	if err != nil {
		return "", err
	}
	if mustBeVisible && !sharedHere && (!public || hidden) {
		return "", errThreadNotOnList
	}
	return tradeOfferLabel(h.normalizeStoredSpecies(pokemon), form, region, costume, event), nil
}

// lastOfferChangeBy is the side that last changed what is offered, from the messages: an offer, a
// request for the other's shiny, or an edit of an offered shiny ("" when nobody has).
func (h *Handlers) lastOfferChangeBy(t tradeThread) (string, error) {
	side, _, err := h.lastOfferChange(t)
	return side, err
}

// canAgreeNow says whether this side may send "agree": both offers set, the last change was the
// partner's, and that change is something the partner chose. A partner's edit of their own shiny
// (sys_offer_modified) counts only if the two had agreed before: that is the "Trade modified"
// review. Without that, an edit by a partner who never answered would let the other side agree
// on their behalf (final review).
func (h *Handlers) canAgreeNow(t tradeThread, s string) (bool, error) {
	if !t.AOffer.Valid || !t.BOffer.Valid {
		return false, nil
	}
	// Walk the offer changes newest first. The caller's own edits of their own shiny do not stop
	// them from accepting the partner's offer, so they are skipped; the first other change decides.
	rows, err := h.db.Query(`SELECT preset_key, from_user, params FROM trade_messages
		WHERE thread_id = ? AND preset_key IN ('offer_mine', 'ask_other', 'req_trade_for', 'sys_offer_modified')
		ORDER BY id DESC LIMIT 50`, t.ID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var from sql.NullInt64
		var params sql.NullString
		if err := rows.Scan(&key, &from, &params); err != nil {
			return false, err
		}
		side := ""
		if key == "sys_offer_modified" {
			var p map[string]string
			json.Unmarshal([]byte(params.String), &p)
			side = p["side"]
			if side == s {
				continue // the caller's own edit
			}
			// The partner edited their own shiny. That stands as their choice only if they put it
			// in the trade themselves (offer_mine) or the two had agreed before (the "Trade
			// modified" review); a shiny the caller asked for and the partner never answered is
			// not consent (final review).
			rows.Close()
			return h.partnerChoseOffer(t, s)
		}
		if from.Valid {
			side = t.side(uint(from.Int64))
		}
		return side != "" && side != s, nil
	}
	return false, rows.Err()
}

// lastOfferChange is the side that last changed what is offered, and the message that did it.
func (h *Handlers) lastOfferChange(t tradeThread) (string, string, error) {
	var key string
	var from sql.NullInt64
	var params sql.NullString
	err := h.db.QueryRow(`
		SELECT preset_key, from_user, params FROM trade_messages
		WHERE thread_id = ? AND preset_key IN ('offer_mine', 'ask_other', 'req_trade_for', 'sys_offer_modified')
		ORDER BY id DESC LIMIT 1`, t.ID).Scan(&key, &from, &params)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if key == "sys_offer_modified" {
		var p map[string]string
		json.Unmarshal([]byte(params.String), &p)
		return p["side"], key, nil
	}
	if from.Valid {
		return t.side(uint(from.Int64)), key, nil
	}
	return "", key, nil
}

// addThreadMessage writes one message and moves the conversation's clock. from 0 is the server.
func addThreadMessage(tx *sql.Tx, threadID uint64, from uint, key string, params map[string]any, now time.Time) error {
	var p any
	if len(params) > 0 {
		b, _ := json.Marshal(params)
		p = string(b)
	}
	var f any
	if from != 0 {
		f = from
	}
	if _, err := tx.Exec(`INSERT INTO trade_messages (thread_id, from_user, preset_key, params, created_at) VALUES (?, ?, ?, ?, ?)`,
		threadID, f, key, p, now); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE trade_threads SET last_message_at = ? WHERE id = ?`, now, threadID)
	return err
}

// ---------- Opening a conversation ----------

// APITradeThreadCreate opens a trade conversation, or returns the one already open between the two.
//
//	POST /api/trades/threads   {"username": "...", "shiny_id": 123}   (shiny_id optional)
//
// shiny_id is a shiny on the other trainer's visible Willing to Trade list: the caller is asking
// for it, so it becomes the other side's offer. Answers {"ok":true,"id":N,"existing":bool}.
func (h *Handlers) APITradeThreadCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	var body struct {
		Username string `json:"username"`
		ShinyID  uint64 `json:"shiny_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	targetID, targetName, err := h.tradeTarget(body.Username)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	// The cooldown is left to checkRequestLimits: reopening an existing conversation is no request.
	if err := h.canContact(u.ID, targetID, now); err != nil && !errors.Is(err, errTradeCooldown) {
		h.writeThreadError(w, r, err)
		return
	}
	if allowed, err := h.canTrade(u.ID, targetID, now); err != nil {
		h.writeThreadError(w, r, err)
		return
	} else if !allowed {
		h.writeThreadError(w, r, errTradeNotAllowed)
		return
	}
	// Serialize opening per pair, so two trainers opening to each other at the same moment get
	// one conversation (Phase 5 review: both used to get their own). A named lock on one pooled
	// connection, held until this request is done; both orders of the pair use the same name.
	lo, hi := u.ID, targetID
	if hi < lo {
		lo, hi = hi, lo
	}
	lockName := "trade_pair_" + strconv.FormatUint(uint64(lo), 10) + "_" + strconv.FormatUint(uint64(hi), 10)
	conn, err := h.db.Conn(r.Context())
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer conn.Close()
	var got sql.NullInt64
	if err := conn.QueryRowContext(r.Context(), `SELECT GET_LOCK(?, 5)`, lockName).Scan(&got); err != nil || got.Int64 != 1 {
		h.writeThreadError(w, r, errTradeCooldown) // busy: the flat "Slow down" answer
		return
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK(?)`, lockName)

	var existing uint64
	err = h.db.QueryRow(`SELECT id FROM trade_threads
		WHERE state IN ('open', 'agreed') AND hidden_at IS NULL
		  AND ((a_user = ? AND b_user = ?) OR (a_user = ? AND b_user = ?))
		ORDER BY id DESC LIMIT 1`, u.ID, targetID, targetID, u.ID).Scan(&existing)
	if err == nil {
		tradeJSON(w, map[string]any{"ok": true, "id": existing, "existing": true})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.writeThreadError(w, r, err)
		return
	}
	label := ""
	if body.ShinyID != 0 {
		if label, err = h.tradeableShiny(targetID, body.ShinyID, true, false); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
	}
	if err := h.checkRequestLimits(u.ID, now); err != nil {
		h.writeThreadError(w, r, err)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer tx.Rollback()
	var bOffer any
	if body.ShinyID != 0 {
		bOffer = body.ShinyID
	}
	res, err := tx.Exec(`INSERT INTO trade_threads (a_user, b_user, a_name, b_name, state, b_offer_shiny_id, b_offer_label,
		a_last_read_at, last_message_at, created_at) VALUES (?, ?, ?, ?, 'open', ?, ?, ?, ?, ?)`,
		u.ID, targetID, u.Username, targetName, bOffer, label, now, now, now)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	id64, _ := res.LastInsertId()
	id := uint64(id64)
	if body.ShinyID != 0 {
		err = addThreadMessage(tx, id, u.ID, "req_trade_for", map[string]any{"shiny_id": body.ShinyID, "pokemon": label}, now)
	} else {
		err = addThreadMessage(tx, id, u.ID, "req_trade_open", nil, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	h.notifyTrade(u.ID, targetID, "trade_request", id)
	tradeJSON(w, map[string]any{"ok": true, "id": id, "existing": false})
}

// ---------- Reading ----------

type tradeOfferView struct {
	ShinyID uint64 `json:"shiny_id,omitempty"`
	Label   string `json:"label"`
}

type tradeSideView struct {
	Username   string          `json:"username,omitempty"`
	Offer      *tradeOfferView `json:"offer"`
	Confirm    string          `json:"confirm"`
	SharesList bool            `json:"shares_list"`
	Available  *bool           `json:"available,omitempty"` // partner only: can still be messaged
}

type tradeMessageView struct {
	ID     uint64          `json:"id"`
	From   string          `json:"from"` // me, them, system
	Key    string          `json:"key"`
	Params json.RawMessage `json:"params,omitempty"`
	At     time.Time       `json:"at"`
}

func sideView(t tradeThread, s string) tradeSideView {
	v := tradeSideView{
		Confirm:    pick(s, t.AConfirm, t.BConfirm),
		SharesList: pick(s, t.AShares, t.BShares),
	}
	offer, label := pick(s, t.AOffer, t.BOffer), pick(s, t.ALabel, t.BLabel)
	if offer.Valid {
		v.Offer = &tradeOfferView{ShinyID: uint64(offer.Int64), Label: label}
	}
	return v
}

// unread says whether the trainer has not seen the latest message.
func (t tradeThread) unread(s string) bool {
	read := pick(s, t.ARead, t.BRead)
	return !read.Valid || t.LastMessage.After(read.Time)
}

// APITradeThreadsList is the caller's conversations, newest first.
//
//	GET /api/trades/threads?box=active   (open and agreed; the default)
//	GET /api/trades/threads?box=closed   (completed, unconfirmed, declined, cancelled)
//
// Hidden conversations (idle, Phase 8) are never listed.
func (h *Handlers) APITradeThreadsList(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	states := "'open', 'agreed'"
	if r.URL.Query().Get("box") == "closed" {
		states = "'completed', 'unconfirmed', 'declined', 'cancelled'"
	}
	rows, err := h.db.Query(`SELECT `+tradeThreadCols+` FROM trade_threads
		WHERE (a_user = ? OR b_user = ?) AND hidden_at IS NULL AND state IN (`+states+`)
		ORDER BY last_message_at DESC, id DESC LIMIT 200`, u.ID, u.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID          uint64        `json:"id"`
		State       string        `json:"state"`
		Me          tradeSideView `json:"me"`
		Them        tradeSideView `json:"them"`
		Unread      bool          `json:"unread"`
		LastMessage time.Time     `json:"last_message_at"`
	}
	out := []item{}
	for rows.Next() {
		t, err := scanTradeThread(rows)
		if err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		s := t.side(u.ID)
		me, them := sideView(t, s), sideView(t, otherSide(s))
		them.Username = pick(otherSide(s), t.AName, t.BName)
		if partner := pick(otherSide(s), t.A, t.B); partner.Valid {
			them.Username = h.currentUsername(uint(partner.Int64), them.Username)
		}
		out = append(out, item{ID: t.ID, State: t.State, Me: me, Them: them, Unread: t.unread(s), LastMessage: t.LastMessage})
	}
	if err := rows.Err(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	tradeJSON(w, out)
}

// APITradeThreadGet is one conversation with its messages, and marks it read for the caller.
//
//	GET /api/trades/threads/{id}
//
// can_agree says the caller may send "agree" now. review_modified says the partner edited the
// shiny they offer and the caller has not answered since: show the "Trade modified" card.
func (h *Handlers) APITradeThreadGet(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	t, s, err := h.loadThreadFor(id, u.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	rows, err := h.db.Query(`SELECT id, from_user, preset_key, params, created_at FROM trade_messages WHERE thread_id = ? ORDER BY id`, t.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer rows.Close()
	msgs := []tradeMessageView{}
	reviewModified := false
	for rows.Next() {
		var m tradeMessageView
		var from sql.NullInt64
		var params sql.NullString
		if err := rows.Scan(&m.ID, &from, &m.Key, &params, &m.At); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		switch {
		case !from.Valid && !strings.HasPrefix(m.Key, "sys_"):
			m.From = "them" // the partner's account was purged; their messages stay theirs
		case !from.Valid:
			m.From = "system"
		case uint(from.Int64) == u.ID:
			m.From = "me"
		default:
			m.From = "them"
		}
		if params.Valid {
			m.Params = json.RawMessage(params.String)
		}
		// The partner's edit asks this trainer to review; their own next message answers it.
		if m.Key == "sys_offer_modified" {
			var p map[string]string
			json.Unmarshal([]byte(params.String), &p)
			reviewModified = p["side"] == otherSide(s)
		} else if m.From == "me" {
			reviewModified = false
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	h.db.Exec(`UPDATE trade_threads SET `+s+`_last_read_at = ? WHERE id = ?`, now, t.ID)

	other := otherSide(s)
	me, them := sideView(t, s), sideView(t, other)
	them.Username = pick(other, t.AName, t.BName)
	avail := false
	if partner := pick(other, t.A, t.B); partner.Valid {
		err := h.canContact(u.ID, uint(partner.Int64), now)
		avail = err == nil || errors.Is(err, errTradeCooldown)
		them.Username = h.currentUsername(uint(partner.Int64), them.Username)
	}
	them.Available = &avail
	canAgree := false
	if t.State == "open" {
		canAgree, _ = h.canAgreeNow(t, s)
	}
	tradeJSON(w, map[string]any{
		"id": t.ID, "state": t.State, "me": me, "them": them, "messages": msgs, "my_side": s,
		"can_agree": canAgree, "review_modified": reviewModified,
	})
}

// ---------- Sending a message ----------

// tradePresetStates says in which states a preset may be sent. Unlisted presets: open only.
var tradePresetStates = map[string][]string{
	"when": {"open", "agreed"}, "when_ok": {"open", "agreed"}, "when_other": {"open", "agreed"},
	"friend_code": {"open", "agreed"}, "need_good_friends": {"open", "agreed"}, "need_stardust": {"open", "agreed"},
	"ready": {"agreed"}, "cancel": {"open", "agreed"}, "share_list": {"open", "agreed"},
	"stop_share_list": {"open", "agreed"}, "offer_mine": {"open", "agreed"}, "ask_other": {"open", "agreed"},
	"req_trade_for": {"open", "agreed"}, "thanks_done": {"completed"},
}

func presetAllowedIn(key, state string) bool {
	allowed, ok := tradePresetStates[key]
	if !ok {
		return state == "open"
	}
	for _, s := range allowed {
		if s == state {
			return true
		}
	}
	return false
}

// APITradeMessage sends one preset message.
//
//	POST /api/trades/threads/{id}/messages   {"preset_key": "offer_mine", "shiny_id": 12}
//	                                         {"preset_key": "when", "when": "this_weekend"}
//
// Some presets change the trade: offer_mine sets the sender's offer; ask_other and req_trade_for
// ask for one of the partner's shinies (it becomes the partner's offer); changing an offer while
// agreed reopens the trade. agree needs both offers and must come from the trainer who did not
// make the last offer change. decline and cancel close it. share_list and stop_share_list switch
// the sender's private list sharing. friend_code sends the sender's own trainer code (owner
// decision: even from a private profile).
func (h *Handlers) APITradeMessage(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var in tradeMessageInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if _, err := validateTradeMessage(in); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if in.Key == "req_trade_open" {
		h.writeThreadError(w, r, errTradeBadPreset) // only opens a conversation
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	t, s, err := h.loadThreadFor(id, u.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if !presetAllowedIn(in.Key, t.State) {
		h.writeThreadError(w, r, errThreadClosed)
		return
	}
	// In an agreed trade, cancelling is an answer to "did it happen?" (Phase 5 review): after the
	// partner confirmed, it counts as saying it did not happen, with the record that brings, so it
	// cannot be used to dodge one. Before anyone confirmed it simply cancels.
	if t.State == "agreed" && in.Key == "cancel" {
		h.cancelAgreedTrade(w, r, u.ID, t, s, now)
		return
	}
	other := otherSide(s)
	partner := pick(other, t.A, t.B)
	if !partner.Valid {
		h.writeThreadError(w, r, errTradeNoUser)
		return
	}
	partnerID := uint(partner.Int64)
	if err := h.canContact(u.ID, partnerID, now); err != nil && !errors.Is(err, errTradeCooldown) {
		h.writeThreadError(w, r, err)
		return
	}
	var recent int
	h.db.QueryRow(`SELECT COUNT(*) FROM trade_messages WHERE thread_id = ? AND from_user = ? AND created_at > ?`,
		t.ID, u.ID, now.Add(-time.Minute)).Scan(&recent)
	if recent >= tradeThreadPerMinute {
		h.writeThreadError(w, r, errTradeCooldown)
		return
	}

	params := map[string]any{}
	set := map[string]any{} // trade_threads columns this message changes
	reopen := false
	guardOffers := false // agree: the offers must still be the ones this trainer saw
	switch in.Key {
	case "offer_mine":
		label, err := h.tradeableShiny(u.ID, uint64(in.ShinyID), false, false)
		if err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		params["shiny_id"], params["pokemon"] = in.ShinyID, label
		set[s+"_offer_shiny_id"], set[s+"_offer_label"] = in.ShinyID, label
		reopen = true
	case "ask_other", "req_trade_for":
		label, err := h.tradeableShiny(partnerID, uint64(in.ShinyID), true, pick(other, t.AShares, t.BShares))
		if err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		params["shiny_id"], params["pokemon"] = in.ShinyID, label
		set[other+"_offer_shiny_id"], set[other+"_offer_label"] = in.ShinyID, label
		reopen = true
	case "agree":
		if !t.AOffer.Valid || !t.BOffer.Valid {
			h.writeThreadError(w, r, errThreadNeedOffer)
			return
		}
		if ok, err := h.canAgreeNow(t, s); err != nil {
			h.writeThreadError(w, r, err)
			return
		} else if !ok {
			h.writeThreadError(w, r, errThreadOwnOffer)
			return
		}
		set["state"] = "agreed"
		guardOffers = true
	case "when":
		params["when"] = in.When
	case "friend_code":
		var code string
		h.db.QueryRow(`SELECT trainer_code FROM users WHERE id = ?`, u.ID).Scan(&code)
		if len(code) != 12 {
			h.writeThreadError(w, r, errThreadNoCode)
			return
		}
		params["code"] = code[:4] + " " + code[4:8] + " " + code[8:]
	case "share_list":
		set[s+"_shares_list"] = 1
	case "stop_share_list":
		set[s+"_shares_list"] = 0
	case "decline":
		set["state"] = "declined"
	case "cancel":
		set["state"] = "cancelled"
	}
	if reopen && t.State == "agreed" {
		set["state"] = "open"
	}
	// An offer cannot change under a trainer who already answered whether the trade happened.
	guardConfirms := reopen && t.State == "agreed"
	if reopen || set["state"] == "open" {
		set["a_confirm"], set["b_confirm"] = "none", "none"
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer tx.Rollback()
	if len(set) > 0 {
		q := `UPDATE trade_threads SET `
		var args []any
		first := true
		for col, v := range set { // column names come from the code above, never from input
			if !first {
				q += ", "
			}
			q += col + " = ?"
			args = append(args, v)
			first = false
		}
		// The state the checks above saw must still hold.
		q += ` WHERE id = ? AND state = ?`
		args = append(args, t.ID, t.State)
		if guardConfirms {
			q += ` AND a_confirm = 'none' AND b_confirm = 'none'`
		}
		if guardOffers {
			q += ` AND a_offer_shiny_id <=> ? AND b_offer_shiny_id <=> ?`
			args = append(args, t.AOffer, t.BOffer)
		}
		res, err := tx.Exec(q, args...)
		if err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if guardConfirms {
				h.writeThreadError(w, r, errThreadPartnerConfirmed)
				return
			}
			h.writeThreadError(w, r, errThreadClosed)
			return
		}
	}
	if err := addThreadMessage(tx, t.ID, u.ID, in.Key, params, now); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if set["state"] == "agreed" {
		if err := addThreadMessage(tx, t.ID, 0, "sys_agreed", nil, now); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
	}
	tx.Exec(`UPDATE trade_threads SET `+s+`_last_read_at = ? WHERE id = ?`, now, t.ID)
	if err := tx.Commit(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	kind := "trade_message"
	switch set["state"] {
	case "agreed":
		kind = "trade_agreed"
	case "declined":
		kind = "trade_declined"
	case "cancelled":
		kind = "trade_cancelled"
	}
	h.notifyTrade(u.ID, partnerID, kind, t.ID)
	state := t.State
	if st, ok := set["state"].(string); ok {
		state = st
	}
	tradeJSON(w, map[string]any{"ok": true, "state": state})
}

// ---------- Confirming ----------

// APITradeConfirm records whether the caller's in-game trade happened.
//
//	POST /api/trades/threads/{id}/confirm   {"done": true}   (false: it did not happen)
//
// Only in an agreed trade, once per trainer. Both done: the two shinies move between the
// collections. One done and one not: closed as unconfirmed, and the trainer who said it did not
// happen gets an internal "possibly traded, unverified" record (owner decision). Neither: cancelled.
// One answer alone waits for the other (the Phase 8 sweep handles silence: owner decision C1).
func (h *Handlers) APITradeConfirm(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Done == nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	t, s, err := h.loadThreadFor(id, u.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if t.State != "agreed" {
		h.writeThreadError(w, r, errThreadNotAgreed)
		return
	}
	if pick(s, t.AConfirm, t.BConfirm) != "none" {
		h.writeThreadError(w, r, errThreadAnswered)
		return
	}
	mine := "denied"
	if *body.Done {
		mine = "done"
	}
	partner := pick(otherSide(s), t.A, t.B)

	// Record the answer under a row lock, so two trainers confirming at the same moment see each
	// other: the second waits for the first, reads its answer, and goes on to settle the trade
	// (Phase 5 review: both used to answer "waiting" and the trade stuck forever).
	tx, err := h.db.Begin()
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	var state, aC, bC string
	if err := tx.QueryRow(`SELECT state, a_confirm, b_confirm FROM trade_threads WHERE id = ? FOR UPDATE`, t.ID).Scan(&state, &aC, &bC); err != nil {
		tx.Rollback()
		h.writeThreadError(w, r, err)
		return
	}
	if state != "agreed" {
		tx.Rollback()
		h.writeThreadError(w, r, errThreadNotAgreed)
		return
	}
	if pick(s, aC, bC) != "none" {
		tx.Rollback()
		h.writeThreadError(w, r, errThreadAnswered)
		return
	}
	if _, err := tx.Exec(`UPDATE trade_threads SET `+s+`_confirm = ? WHERE id = ?`, mine, t.ID); err != nil {
		tx.Rollback()
		h.writeThreadError(w, r, err)
		return
	}
	// The first answer starts owner decision C1's clock (reminder at 3 days, close at 7), so it
	// is timestamped as a server message; the confirmer has read it.
	if mine == "done" && pick(otherSide(s), aC, bC) == "none" {
		if err := addThreadMessage(tx, t.ID, 0, "sys_confirmed", map[string]any{"side": s}, now); err != nil {
			tx.Rollback()
			h.writeThreadError(w, r, err)
			return
		}
		tx.Exec(`UPDATE trade_threads SET `+s+`_last_read_at = ? WHERE id = ?`, now, t.ID)
	}
	if err := tx.Commit(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if s == "a" {
		aC = mine
	} else {
		bC = mine
	}
	if aC == "none" || bC == "none" {
		if partner.Valid {
			kind := "trade_confirm_needed"
			if mine == "denied" {
				kind = "trade_answer_needed" // they said it did not happen; never "confirmed your trade"
			}
			h.notifyTrade(u.ID, uint(partner.Int64), kind, t.ID)
		}
		tradeJSON(w, map[string]any{"ok": true, "state": "agreed", "waiting": true})
		return
	}
	state, err = h.settleTrade(t.ID, aC, bC, now)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	// The closing message is the caller's own doing: nothing to be told about (Phase 7 review).
	h.db.Exec(`UPDATE trade_threads SET `+s+`_last_read_at = ? WHERE id = ?`, time.Now().UTC(), t.ID)
	if partner.Valid {
		h.notifyTrade(u.ID, uint(partner.Int64), "trade_"+state, t.ID)
	}
	tradeJSON(w, map[string]any{"ok": true, "state": state})
}

// settleTrade finishes an agreed trade once both have answered. If another request settled it
// first, it answers with the state that request left.
func (h *Handlers) settleTrade(id uint64, aC, bC string, now time.Time) (string, error) {
	t, err := scanTradeThread(h.db.QueryRow(`SELECT `+tradeThreadCols+` FROM trade_threads WHERE id = ?`, id))
	if err != nil {
		return "", err
	}
	state := "completed"
	if aC == "done" && bC == "done" {
		err = h.completeTrade(t, now)
	} else {
		state, err = h.closeTradeUnconfirmed(t, aC, bC, now)
	}
	if errors.Is(err, errThreadClosed) {
		var current string
		h.db.QueryRow(`SELECT state FROM trade_threads WHERE id = ?`, id).Scan(&current)
		return current, nil
	}
	return state, err
}

// completeTrade moves the two offered shinies: each receiver gets a new entry (same Pokemon, form,
// region, costume and event; caught by trade today) and each giver's entry is removed. It is one
// transaction, so nothing changes unless all of it does (Phase 5 review):
//   - the trade must still be agreed on the same two offers;
//   - each offered entry must still be what was agreed: if it was edited or evolved since (its
//     label no longer matches), nothing moves and the trade goes back to open for re-verifying;
//   - other open trades offering either shiny lose that offer, with a note, as for a delete.
func (h *Handlers) completeTrade(t tradeThread, now time.Time) error {
	if !t.AOffer.Valid || !t.BOffer.Valid || !t.A.Valid || !t.B.Valid {
		return errThreadNeedOffer
	}
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var aOffer, bOffer sql.NullInt64
	if err := tx.QueryRow(`SELECT state, a_offer_shiny_id, b_offer_shiny_id FROM trade_threads WHERE id = ? FOR UPDATE`, t.ID).
		Scan(&state, &aOffer, &bOffer); err != nil {
		return err
	}
	if state != "agreed" || aOffer != t.AOffer || bOffer != t.BOffer {
		return errThreadClosed
	}
	moves := []struct {
		shiny      uint64
		giver, get int64
		label      string
	}{{uint64(t.AOffer.Int64), t.A.Int64, t.B.Int64, t.ALabel}, {uint64(t.BOffer.Int64), t.B.Int64, t.A.Int64, t.BLabel}}
	type other struct {
		thread uint64
		side   string
		label  string
	}
	var others []other
	for _, m := range moves {
		var pokemon, form, region, costume, event string
		err := tx.QueryRow(`SELECT pokemon_id, form, region, costume, event_tag FROM user_shinies WHERE id = ? AND user_id = ? FOR UPDATE`,
			m.shiny, m.giver).Scan(&pokemon, &form, &region, &costume, &event)
		if errors.Is(err, sql.ErrNoRows) {
			return errThreadNeedOffer
		}
		if err != nil {
			return err
		}
		if current := tradeOfferLabel(h.normalizeStoredSpecies(pokemon), form, region, costume, event); current != m.label {
			tx.Rollback()
			// Changed since it was agreed: re-verify instead of moving something else.
			h.tradeOfferModified(m.shiny, current, time.Now())
			return errThreadOfferChanged
		}
		rows, err := tx.Query(`SELECT id, a_offer_shiny_id, a_offer_label, b_offer_label FROM trade_threads
			WHERE state IN ('open', 'agreed') AND id <> ? AND (a_offer_shiny_id = ? OR b_offer_shiny_id = ?) FOR UPDATE`, t.ID, m.shiny, m.shiny)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uint64
			var ao sql.NullInt64
			var al, bl string
			if err := rows.Scan(&id, &ao, &al, &bl); err != nil {
				rows.Close()
				return err
			}
			if ao.Valid && uint64(ao.Int64) == m.shiny {
				others = append(others, other{id, "a", al})
			} else {
				others = append(others, other{id, "b", bl})
			}
		}
		rows.Close()
		if _, err := tx.Exec(`INSERT INTO user_shinies (user_id, pokemon_id, form, region, costume, event_tag, method, caught_at)
			VALUES (?, ?, ?, ?, ?, ?, 'trade', ?)`, m.get, pokemon, form, region, costume, event, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM user_shinies WHERE id = ? AND user_id = ?`, m.shiny, m.giver); err != nil {
			return err
		}
	}
	for _, o := range others { // o.side is "a" or "b" from the code above
		if _, err := tx.Exec(`UPDATE trade_threads SET `+o.side+`_offer_label = '', state = 'open', a_confirm = 'none', b_confirm = 'none',
			`+o.side+`_last_read_at = IF(`+o.side+`_last_read_at >= last_message_at AND hidden_at IS NULL, ?, `+o.side+`_last_read_at),
			last_message_at = IF(hidden_at IS NULL, ?, last_message_at) WHERE id = ?`, now, now, o.thread); err != nil {
			return err
		}
		b, _ := json.Marshal(map[string]string{"pokemon": o.label})
		if _, err := tx.Exec(`INSERT INTO trade_messages (thread_id, from_user, preset_key, params, created_at)
			VALUES (?, NULL, 'sys_offer_removed', ?, ?)`, o.thread, string(b), now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE trade_threads SET state = 'completed', a_confirm = 'done', b_confirm = 'done' WHERE id = ?`, t.ID); err != nil {
		return err
	}
	if err := addThreadMessage(tx, t.ID, 0, "sys_completed", nil, now); err != nil {
		return err
	}
	return tx.Commit()
}

// closeTradeUnconfirmed closes an agreed trade the two disagree on (or both say did not happen).
// Nothing moves. A trainer who denied it while the other confirmed gets an internal record.
func (h *Handlers) closeTradeUnconfirmed(t tradeThread, a, b string, now time.Time) (string, error) {
	state := "unconfirmed"
	if a == "denied" && b == "denied" {
		state = "cancelled"
	}
	tx, err := h.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE trade_threads SET state = ?, a_confirm = ?, b_confirm = ? WHERE id = ? AND state = 'agreed'`,
		state, a, b, t.ID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errThreadClosed
	}
	if state == "unconfirmed" {
		denier := t.A
		if b == "denied" {
			denier = t.B
		}
		if denier.Valid {
			if _, err := tx.Exec(`INSERT INTO trade_unverified (user_id, thread_id, kind, created_at) VALUES (?, ?, 'denied', ?)`,
				denier.Int64, t.ID, now); err != nil {
				return "", err
			}
		}
	}
	if err := addThreadMessage(tx, t.ID, 0, "sys_unconfirmed", map[string]any{"both_denied": state == "cancelled"}, now); err != nil {
		return "", err
	}
	return state, tx.Commit()
}

// cancelAgreedTrade handles "cancel" in an agreed trade, under a row lock so it cannot race a
// confirm. Nobody confirmed yet: the trade is cancelled. The partner already confirmed: it is the
// caller saying the trade did not happen (unconfirmed, with the internal record). The caller
// already answered: refused.
func (h *Handlers) cancelAgreedTrade(w http.ResponseWriter, r *http.Request, userID uint, t tradeThread, s string, now time.Time) {
	tx, err := h.db.Begin()
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer tx.Rollback()
	var state, aC, bC string
	if err := tx.QueryRow(`SELECT state, a_confirm, b_confirm FROM trade_threads WHERE id = ? FOR UPDATE`, t.ID).Scan(&state, &aC, &bC); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if state != "agreed" {
		h.writeThreadError(w, r, errThreadClosed)
		return
	}
	if pick(s, aC, bC) != "none" {
		h.writeThreadError(w, r, errThreadAnswered)
		return
	}
	partner := pick(otherSide(s), t.A, t.B)
	if pick(otherSide(s), aC, bC) == "none" {
		if _, err := tx.Exec(`UPDATE trade_threads SET state = 'cancelled' WHERE id = ?`, t.ID); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		if err := addThreadMessage(tx, t.ID, userID, "cancel", nil, now); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		if partner.Valid {
			h.notifyTrade(userID, uint(partner.Int64), "trade_cancelled", t.ID)
		}
		tradeJSON(w, map[string]any{"ok": true, "state": "cancelled"})
		return
	}
	if _, err := tx.Exec(`UPDATE trade_threads SET `+s+`_confirm = 'denied' WHERE id = ?`, t.ID); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	if s == "a" {
		aC = "denied"
	} else {
		bC = "denied"
	}
	state, err = h.settleTrade(t.ID, aC, bC, now)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	// The closing message is the caller's own doing: nothing to be told about (Phase 7 review).
	h.db.Exec(`UPDATE trade_threads SET `+s+`_last_read_at = ? WHERE id = ?`, time.Now().UTC(), t.ID)
	if partner.Valid {
		h.notifyTrade(userID, uint(partner.Int64), "trade_"+state, t.ID)
	}
	tradeJSON(w, map[string]any{"ok": true, "state": state})
}

// ---------- Phase 6: the partner's list inside a conversation ----------

// tradeListEntry is one shiny on the partner's Willing to Trade list.
type tradeListEntry struct {
	ID          uint64 `json:"id"`
	PokemonID   string `json:"pokemon_id"`
	Dex         int    `json:"dex"`
	Form        string `json:"form"`
	Region      string `json:"region"`
	Costume     string `json:"costume"`
	EventTag    string `json:"event_tag"`
	TradeReason string `json:"trade_reason"`
	Label       string `json:"label"`
	SpriteURL   string `json:"sprite_url"`
}

// APITradeThreadList is the partner's Willing to Trade list, for picking what to ask for.
//
//	GET /api/trades/threads/{id}/list   ->  {"source": "public|shared|none", "shinies": [...]}
//
// It is visible when the partner's profile is public with shinies shown ("public"), or when they
// share it in THIS conversation ("shared"; owner decision: a private list can be shared privately
// and the sharing stopped any time). Otherwise, or when the partner cannot be contacted (blocked,
// gone), "none" and an empty list. A privately shared list is reachable only through here.
func (h *Handlers) APITradeThreadList(w http.ResponseWriter, r *http.Request) {
	h.tradeThreadList(w, r, false)
}

// MobileTradeThreadList is APITradeThreadList with absolute sprite URLs, like MobileShiniesOfUser.
func (h *Handlers) MobileTradeThreadList(w http.ResponseWriter, r *http.Request) {
	h.tradeThreadList(w, r, true)
}

func (h *Handlers) tradeThreadList(w http.ResponseWriter, r *http.Request, absolute bool) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	t, s, err := h.loadThreadFor(id, u.ID)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	other := otherSide(s)
	out := map[string]any{"source": "none", "shinies": []tradeListEntry{}}
	partner := pick(other, t.A, t.B)
	// Only while the conversation is open or agreed: nothing can be asked for once it is closed,
	// and "stop sharing" is refused there, so a privately shared list would otherwise stay readable
	// for the whole retention year with no way to stop it (Phase 6 review; owner decision C8).
	if t.State != "open" && t.State != "agreed" {
		tradeJSON(w, out)
		return
	}
	if !partner.Valid {
		tradeJSON(w, out)
		return
	}
	// A cooldown only limits new requests; a block or a gone partner hides the list.
	if err := h.canContact(u.ID, uint(partner.Int64), time.Now()); err != nil && !errors.Is(err, errTradeCooldown) {
		tradeJSON(w, out)
		return
	}
	var public, hidden bool
	if err := h.db.QueryRow(`SELECT profile_public, shinies_hidden FROM users WHERE id = ?`, partner.Int64).Scan(&public, &hidden); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	switch {
	case public && !hidden:
		out["source"] = "public"
	case pick(other, t.AShares, t.BShares):
		out["source"] = "shared"
	default:
		tradeJSON(w, out)
		return
	}
	rows, err := h.db.Query(`SELECT id, pokemon_id, form, region, costume, event_tag, trade_reason
		FROM user_shinies WHERE user_id = ? AND for_trade = 1 ORDER BY caught_at DESC, id DESC`, partner.Int64)
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	defer rows.Close()
	list := []tradeListEntry{}
	for rows.Next() {
		var e tradeListEntry
		if err := rows.Scan(&e.ID, &e.PokemonID, &e.Form, &e.Region, &e.Costume, &e.EventTag, &e.TradeReason); err != nil {
			h.writeThreadError(w, r, err)
			return
		}
		e.PokemonID = h.normalizeStoredSpecies(e.PokemonID)
		e.Dex = h.store.PokemonDexID(e.PokemonID)
		e.Label = tradeOfferLabel(e.PokemonID, e.Form, e.Region, e.Costume, e.EventTag)
		e.SpriteURL = h.resolveShinySpriteURL(e.PokemonID, e.Region, e.Costume)
		if absolute {
			e.SpriteURL = absoluteURL(e.SpriteURL)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	out["shinies"] = list
	tradeJSON(w, out)
}

// currentUsername is a trainer's username now, so a rename since the conversation started does not
// point Block or Report at a name that is no longer theirs (final review). fallback when gone.
func (h *Handlers) currentUsername(id uint, fallback string) string {
	var name string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&name); err != nil || name == "" {
		return fallback
	}
	return name
}

// shinyLockedInTrade says whether a shiny is offered in an agreed trade that someone has already
// answered (confirmed or denied). Such a shiny cannot be unmarked, edited, evolved or deleted until
// the trade is settled: changing it then would wipe the partner's answer and dodge a denial
// (final review, owner rule: a denial cannot be dodged and the confirmer is not held back).
func (h *Handlers) shinyLockedInTrade(shinyID uint64) (bool, error) {
	var locked bool
	err := h.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM trade_threads
		WHERE state = 'agreed' AND hidden_at IS NULL AND (a_offer_shiny_id = ? OR b_offer_shiny_id = ?)
		  AND (a_confirm <> 'none' OR b_confirm <> 'none'))`, shinyID, shinyID).Scan(&locked)
	return locked, err
}

// partnerChoseOffer: the partner's current offer is one they chose, either by offering it
// themselves or by an earlier agreement on this trade.
func (h *Handlers) partnerChoseOffer(t tradeThread, s string) (bool, error) {
	me, partner := pick(s, t.A, t.B), pick(otherSide(s), t.A, t.B)
	var key string
	err := h.db.QueryRow(`SELECT preset_key FROM trade_messages WHERE thread_id = ?
		AND ((preset_key = 'offer_mine' AND from_user <=> ?) OR (preset_key IN ('ask_other', 'req_trade_for') AND from_user <=> ?))
		ORDER BY id DESC LIMIT 1`, t.ID, partner, me).Scan(&key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if key == "offer_mine" {
		return true, nil
	}
	var agreedBefore bool
	err = h.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_agreed')`, t.ID).Scan(&agreedBefore)
	return agreedBefore, err
}
