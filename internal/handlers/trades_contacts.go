package handlers

// Trade Assist, Phase 4: connect requests. Every non friend goes through one before a trade can
// be requested (owner decision). Plan: .claude/trade-assist-build-plan-11-18-26.md.
//
// One trade_contacts row per direction (UNIQUE from_user, to_user). Its life:
//
//	pending   asked, waiting for an answer; expires 15 days after it was sent (owner decision C7)
//	accepted  the two may start trades until expires_at, 15 days after accepting (owner decision)
//	declined  the receiver said no; the sender only ever sees a neutral "not available"
//	expired   set by the Phase 8 sweep; until then a past expires_at already reads as expired
//
// A new request to the same trainer is only possible once the previous one has expired. That
// reuses the row, so every timestamp is reset to now (Phase 2 review, plan 4.0).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// tradeErrorText maps the shared trade errors to a status, a stable code for clients, and the text
// a trainer sees. Blocks and missing accounts read the same on purpose: neither may be revealed.
func (h *Handlers) writeTradeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errTradeCooldown):
		writeJSONErrorCode(w, h.t(r, "trade.err.slow_down"), "slow_down", http.StatusTooManyRequests)
	case errors.Is(err, errTradeDailyLimit):
		writeJSONErrorCode(w, h.t(r, "trade.err.daily_limit"), "daily_limit", http.StatusTooManyRequests)
	case errors.Is(err, errTradeWaitLimit):
		writeJSONErrorCode(w, h.t(r, "trade.err.wait_limit"), "wait_limit", http.StatusTooManyRequests)
	case errors.Is(err, errTradeSelf):
		writeJSONErrorCode(w, h.t(r, "trade.err.self"), "self", http.StatusBadRequest)
	case errors.Is(err, errTradeBlocked), errors.Is(err, errTradeNoUser):
		writeJSONErrorCode(w, h.t(r, "trade.err.unavailable"), "unavailable", http.StatusNotFound)
	default:
		log.Printf("trades: %v", err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
	}
}

// endTradeContacts retires every connect request and connection between two trainers, both
// directions, when one blocks the other (plan 9.1). Unblocking restores nothing: as with follows,
// they would have to ask again. Retired, not deleted, so staff history stays.
func (h *Handlers) endTradeContacts(a, b uint, now time.Time) error {
	_, err := h.db.Exec(`UPDATE trade_contacts SET state = 'expired', expires_at = ?
		WHERE ((from_user = ? AND to_user = ?) OR (from_user = ? AND to_user = ?)) AND expires_at > ?`,
		now.UTC(), a, b, b, a, now.UTC())
	return err
}

// tradeJSON answers 200 with v as JSON.
func tradeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// APITradeContactCreate sends a connect request.
//
//	POST /api/trades/contacts   {"username": "..."}
//
// Answers {"ok":true,"state":"pending"}, or "accepted" when the other trainer had already asked
// the caller (both wanted it, so it is accepted on the spot and counts as no new request).
func (h *Handlers) APITradeContactCreate(w http.ResponseWriter, r *http.Request) {
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
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	targetID, _, err := h.tradeTarget(body.Username)
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	// A cooldown is left to checkRequestLimits below: accepting a request the other trainer already
	// sent is not a new request, so it must work during a cooldown, as the accept button does.
	if err := h.canContact(u.ID, targetID, now); err != nil && !errors.Is(err, errTradeCooldown) {
		h.writeTradeError(w, r, err)
		return
	}
	rel, err := h.relation(u.ID, targetID)
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	if rel.Friends {
		writeJSONErrorCode(w, h.t(r, "trade.err.already_friends"), "already_friends", http.StatusConflict)
		return
	}

	// Either direction still live? Accepted means already connected. The caller's own unexpired
	// request (pending or declined) means they already asked. The other trainer's pending request
	// means both want it: accept theirs now.
	mine, err := h.tradeContactRow(u.ID, targetID, now)
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	theirs, err := h.tradeContactRow(targetID, u.ID, now)
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	if (mine.found && mine.state == "accepted") || (theirs.found && theirs.state == "accepted") {
		writeJSONErrorCode(w, h.t(r, "trade.err.already_connected"), "already_connected", http.StatusConflict)
		return
	}
	if theirs.found && theirs.state == "pending" {
		if _, err := h.db.Exec(`UPDATE trade_contacts SET state = 'accepted', answered_at = ?, expires_at = ?
			WHERE id = ? AND state = 'pending'`, now, now.Add(tradeContactTTL), theirs.id); err != nil {
			h.writeTradeError(w, r, err)
			return
		}
		h.notifyTrade(u.ID, targetID, "contact_accepted", theirs.id)
		tradeJSON(w, map[string]any{"ok": true, "state": "accepted"})
		return
	}
	if mine.found && (mine.state == "pending" || mine.state == "declined") {
		writeJSONErrorCode(w, h.t(r, "trade.err.already_asked"), "already_asked", http.StatusConflict)
		return
	}

	if err := h.checkRequestLimits(u.ID, now); err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	// New, or reusing the row of a request that has expired: every timestamp starts over, so the
	// daily count sees it (plan 4.0).
	res, err := h.db.Exec(`
		INSERT INTO trade_contacts (from_user, to_user, state, created_at, answered_at, expires_at)
		VALUES (?, ?, 'pending', ?, NULL, ?)
		ON DUPLICATE KEY UPDATE state = 'pending', created_at = VALUES(created_at),
		                        answered_at = NULL, expires_at = VALUES(expires_at)`,
		u.ID, targetID, now, now.Add(tradeContactTTL))
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		id = int64(mine.id)
	}
	// Both trainers may have asked each other at the same moment, each before seeing the other's
	// row (Phase 4 review). Settle it the same way from both sides: the older request (lower id)
	// becomes the accepted connection and the newer one is retired, so the pair ends up with one.
	if theirs, err := h.tradeContactRow(targetID, u.ID, now); err == nil && theirs.found && theirs.state == "pending" {
		keep, drop := uint64(id), theirs.id
		if theirs.id < keep {
			keep, drop = theirs.id, uint64(id)
		}
		h.db.Exec(`UPDATE trade_contacts SET state = 'accepted', answered_at = ?, expires_at = ? WHERE id = ? AND state = 'pending'`,
			now, now.Add(tradeContactTTL), keep)
		h.db.Exec(`UPDATE trade_contacts SET state = 'expired', expires_at = ? WHERE id = ? AND state = 'pending'`, now, drop)
		h.notifyTrade(u.ID, targetID, "contact_accepted", keep)
		tradeJSON(w, map[string]any{"ok": true, "state": "accepted"})
		return
	}
	h.notifyTrade(u.ID, targetID, "contact_request", uint64(id))
	tradeJSON(w, map[string]any{"ok": true, "state": "pending"})
}

// tradeContactRow is the request one trainer sent another, if any. found is true only while it
// has not expired; id is filled in either way, for reusing the row.
type tradeContactRowView struct {
	id      uint64
	state   string
	expires time.Time
	found   bool
}

func (h *Handlers) tradeContactRow(from, to uint, now time.Time) (tradeContactRowView, error) {
	var v tradeContactRowView
	err := h.db.QueryRow(`SELECT id, state, expires_at FROM trade_contacts WHERE from_user = ? AND to_user = ?`, from, to).
		Scan(&v.id, &v.state, &v.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.found = v.expires.After(now)
	return v, nil
}

// APITradeContactAnswer accepts or declines a connect request sent to the caller.
//
//	POST /api/trades/contacts/{id}/accept
//	POST /api/trades/contacts/{id}/decline
//
// Only the receiver can answer, only while it is pending and unexpired. Anything else is a 404,
// the same as a request that does not exist.
func (h *Handlers) APITradeContactAnswer(w http.ResponseWriter, r *http.Request) {
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
	accept := chi.URLParam(r, "answer") == "accept"
	now := time.Now().UTC().Truncate(time.Second)

	var from uint
	err = h.db.QueryRow(`SELECT from_user FROM trade_contacts WHERE id = ? AND to_user = ? AND state = 'pending' AND expires_at > ?`,
		id, u.ID, now).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	if accept {
		// Accepting is contacting them back, so the same rules apply: a live sender, no block.
		if err := h.canContact(u.ID, from, now); err != nil && !errors.Is(err, errTradeCooldown) {
			h.writeTradeError(w, r, err)
			return
		}
		_, err = h.db.Exec(`UPDATE trade_contacts SET state = 'accepted', answered_at = ?, expires_at = ?
			WHERE id = ? AND state = 'pending'`, now, now.Add(tradeContactTTL), id)
	} else {
		// The expiry stays as it was, so the sender cannot ask again until the original request
		// would have expired.
		_, err = h.db.Exec(`UPDATE trade_contacts SET state = 'declined', answered_at = ? WHERE id = ? AND state = 'pending'`, now, id)
	}
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	if accept {
		h.notifyTrade(u.ID, from, "contact_accepted", id)
		tradeJSON(w, map[string]any{"ok": true, "state": "accepted"})
		return
	}
	// A decline sends nothing to the sender: they only ever see "not available".
	tradeJSON(w, map[string]any{"ok": true, "state": "declined"})
}

// tradeContactView is one row of the caller's connect requests.
type tradeContactView struct {
	ID          uint64    `json:"id"`
	Username    string    `json:"username"`
	TrainerName string    `json:"trainer_name"`
	State       string    `json:"state"` // incoming: pending; outgoing: waiting, not_available; connected: connected
	SentAt      time.Time `json:"sent_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	DaysLeft    int       `json:"days_left"`
}

// APITradeContactsList is the caller's connect requests.
//
//	GET /api/trades/contacts  ->  {"incoming": [...], "outgoing": [...], "connected": [...]}
//
// Only live, unblocked trainers appear, and only unexpired requests. A declined request shows to
// its sender as not_available, never as declined.
func (h *Handlers) APITradeContactsList(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	rows, err := h.db.Query(`
		SELECT c.id, c.from_user, c.state, c.created_at, c.expires_at, u.username, IF(u.profile_public = 1, u.trainer_name, '')
		FROM trade_contacts c
		JOIN users u ON u.id = IF(c.from_user = ?, c.to_user, c.from_user)
		WHERE (c.from_user = ? OR c.to_user = ?) AND c.expires_at > ?
		  AND c.state IN ('pending', 'accepted', 'declined')
		  AND `+tradeLiveUser+`
		  AND NOT EXISTS (SELECT 1 FROM user_blocks b
		                  WHERE (b.user_id = ? AND b.blocked_id = u.id) OR (b.user_id = u.id AND b.blocked_id = ?))
		ORDER BY c.created_at DESC`,
		u.ID, u.ID, u.ID, now, u.ID, u.ID)
	if err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	defer rows.Close()
	out := map[string][]tradeContactView{"incoming": {}, "outgoing": {}, "connected": {}}
	for rows.Next() {
		var v tradeContactView
		var from uint
		if err := rows.Scan(&v.ID, &from, &v.State, &v.SentAt, &v.ExpiresAt, &v.Username, &v.TrainerName); err != nil {
			h.writeTradeError(w, r, err)
			return
		}
		v.DaysLeft = int(math.Ceil(v.ExpiresAt.Sub(now).Hours() / 24))
		mine := from == u.ID
		switch {
		case v.State == "accepted":
			v.State = "connected"
			out["connected"] = append(out["connected"], v)
		case mine && v.State == "pending":
			v.State = "waiting"
			out["outgoing"] = append(out["outgoing"], v)
		case mine && v.State == "declined":
			v.State = "not_available"
			out["outgoing"] = append(out["outgoing"], v)
		case !mine && v.State == "pending":
			out["incoming"] = append(out["incoming"], v)
		}
		// A request the caller declined is not shown back to them.
	}
	if err := rows.Err(); err != nil {
		h.writeTradeError(w, r, err)
		return
	}
	// A connection makes any other row about the same trainer noise: an old "not available"
	// request beside it, or a second connection from a simultaneous request (Phase 4 review).
	connected := map[string]bool{}
	var conn []tradeContactView
	for _, v := range out["connected"] {
		if !connected[v.Username] {
			connected[v.Username] = true
			conn = append(conn, v)
		}
	}
	out["connected"] = append([]tradeContactView{}, conn...)
	for _, k := range []string{"incoming", "outgoing"} {
		kept := []tradeContactView{}
		for _, v := range out[k] {
			if !connected[v.Username] {
				kept = append(kept, v)
			}
		}
		out[k] = kept
	}
	tradeJSON(w, out)
}
