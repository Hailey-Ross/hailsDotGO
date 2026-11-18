package handlers

// Trade Assist endpoints. The checks they share live in trades.go.
// Plan: .claude/trade-assist-build-plan-11-18-26.md.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"pogo.hails.cc/internal/auth"
)

// tradesOn says whether Trade Assist is open to this trainer: switched on in admin, or the
// trainer is staff, so staff can try it before it is switched on (plan 0.3).
func (h *Handlers) tradesOn(u *auth.User) bool {
	return h.maintenanceSettings().TradesEnabled || (u != nil && u.IsMod())
}

// tradeReasons are the quick reasons a trainer can give for marking a shiny. Empty is allowed.
var tradeReasons = map[string]bool{"": true, "duplicate": true, "not_for_me": true, "event_spare": true}

// APIShiniesTrade marks or unmarks one of the caller's own shinies as Willing to Trade.
//
//	POST /api/shinies/{id}/trade   {"for_trade": true, "reason": "duplicate"}
//
// It is its own endpoint on purpose: APIShiniesUpdate rewrites every field of the entry, and a
// client that does not know about trading must never be able to clear a mark by saving an edit.
//
// The answer carries profile_private when nobody else can see the mark (private profile, or the
// shiny collection hidden), so the client can say so and point at private sharing instead.
func (h *Handlers) APIShiniesTrade(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
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
		ForTrade *bool  `json:"for_trade"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ForTrade == nil || !tradeReasons[body.Reason] {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	reason := body.Reason
	if !*body.ForTrade {
		reason = "" // an unmarked shiny keeps no reason
		// Unmarking a shiny whose trade is waiting on answers would wipe them (final review).
		if locked, err := h.shinyLockedInTrade(id); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		} else if locked {
			writeJSONErrorCode(w, h.t(r, "trade.err.trade_confirming"), "trade_confirming", http.StatusConflict)
			return
		}
	}

	// The ownership check and the write in one statement. RowsAffected is 0 both for someone
	// else's row and for a mark that is already as asked, so a miss is told apart by a read.
	res, err := h.db.Exec(`UPDATE user_shinies SET for_trade = ?, trade_reason = ? WHERE id = ? AND user_id = ?`,
		*body.ForTrade, reason, id, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var owned bool
		if err := h.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM user_shinies WHERE id = ? AND user_id = ?)`, id, u.ID).Scan(&owned); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		if !owned {
			writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
			return
		}
	}
	// Unmarking takes the shiny out of any open trade it was offered in.
	if !*body.ForTrade {
		if err := h.tradeOfferRemoved(id, time.Now()); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
	}

	var profilePublic, shiniesHidden bool
	if err := h.db.QueryRow(`SELECT profile_public, shinies_hidden FROM users WHERE id = ?`, u.ID).Scan(&profilePublic, &shiniesHidden); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":              true,
		"for_trade":       *body.ForTrade,
		"trade_reason":    reason,
		"profile_private": !profilePublic || shiniesHidden,
	})
}

// tradeOfferRemoved takes a shiny out of every open or agreed trade that offers it, with a
// server message so both trainers see why. An agreed trade goes back to open: what was agreed
// no longer exists. Called when the shiny is unmarked, evolved or deleted, BEFORE a delete,
// since the delete nulls the offer column and the label would be all that is left.
func (h *Handlers) tradeOfferRemoved(shinyID uint64, now time.Time) error {
	return h.tradeOfferRemovedExcept(shinyID, 0, now)
}

// tradeOfferRemovedExcept is tradeOfferRemoved leaving one conversation alone: the trade that is
// completing and moving this very shiny.
func (h *Handlers) tradeOfferRemovedExcept(shinyID, exceptThread uint64, now time.Time) error {
	now = now.UTC()
	rows, err := h.db.Query(`
		SELECT id, a_offer_shiny_id, a_offer_label, b_offer_label, a_user, b_user
		FROM trade_threads
		WHERE state IN ('open', 'agreed') AND hidden_at IS NULL AND (a_offer_shiny_id = ? OR b_offer_shiny_id = ?) AND id <> ?`, shinyID, shinyID, exceptThread)
	if err != nil {
		return err
	}
	type hit struct {
		thread       uint64
		side         string
		label        string
		giver, taker sql.NullInt64
	}
	var hits []hit
	for rows.Next() {
		var thread uint64
		var aOffer, aUser, bUser sql.NullInt64
		var aLabel, bLabel string
		if err := rows.Scan(&thread, &aOffer, &aLabel, &bLabel, &aUser, &bUser); err != nil {
			rows.Close()
			return err
		}
		if aOffer.Valid && uint64(aOffer.Int64) == shinyID {
			hits = append(hits, hit{thread, "a", aLabel, aUser, bUser})
		} else {
			hits = append(hits, hit{thread, "b", bLabel, bUser, aUser})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, x := range hits {
		params, _ := json.Marshal(map[string]string{"pokemon": x.label})
		tx, err := h.db.Begin()
		if err != nil {
			return err
		}
		// x.side is "a" or "b" from the code above, never from input. The WHERE rechecks what the
		// SELECT saw, since that read was outside this transaction: a thread closed meanwhile is
		// not reopened, and an offer swapped meanwhile is not the one being removed. A hidden
		// thread keeps its idle clock, so the idle and retention sweeps are not moved by a notice
		// nobody sees.
		// The giver caused this note, so it stays read for them if they were caught up; the
		// read time is set before last_message_at moves, as MySQL applies SET left to right.
		res, err := tx.Exec(`UPDATE trade_threads SET `+x.side+`_offer_shiny_id = NULL, `+x.side+`_offer_label = '',
			state = 'open', a_confirm = 'none', b_confirm = 'none',
			`+x.side+`_last_read_at = IF(`+x.side+`_last_read_at >= last_message_at AND hidden_at IS NULL, ?, `+x.side+`_last_read_at),
			last_message_at = IF(hidden_at IS NULL, ?, last_message_at)
			WHERE id = ? AND state IN ('open', 'agreed') AND `+x.side+`_offer_shiny_id = ?`, now, now, x.thread, shinyID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				tx.Rollback()
				continue
			}
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO trade_messages (thread_id, from_user, preset_key, params, created_at)
				VALUES (?, NULL, 'sys_offer_removed', ?, ?)`, x.thread, string(params), now)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if x.giver.Valid && x.taker.Valid {
			h.notifyTrade(uint(x.giver.Int64), uint(x.taker.Int64), "trade_modified", x.thread)
		}
	}
	return nil
}

// tradeOfferModified is called after a trainer edits a shiny that may be offered in an open or
// agreed trade (owner decision 2026-11-18): the shiny stays in the trade, but whatever was agreed
// is void. The label becomes the new one, both confirmations reset, an agreed trade goes back to
// open, and a sys_offer_modified message records before and after so the receiving trainer gets
// a "Trade modified" request to review. It returns how many trades were affected, so the editor
// can be told the shiny is in a pending trade and needs re-verifying with their partner.
func (h *Handlers) tradeOfferModified(shinyID uint64, newLabel string, now time.Time) (int, error) {
	now = now.UTC()
	rows, err := h.db.Query(`
		SELECT id, a_offer_shiny_id, a_offer_label, b_offer_label, a_user, b_user
		FROM trade_threads
		WHERE state IN ('open', 'agreed') AND hidden_at IS NULL AND (a_offer_shiny_id = ? OR b_offer_shiny_id = ?)`, shinyID, shinyID)
	if err != nil {
		return 0, err
	}
	type hit struct {
		thread       uint64
		side         string
		before       string
		giver, taker sql.NullInt64
	}
	var hits []hit
	for rows.Next() {
		var thread uint64
		var aOffer, aUser, bUser sql.NullInt64
		var aLabel, bLabel string
		if err := rows.Scan(&thread, &aOffer, &aLabel, &bLabel, &aUser, &bUser); err != nil {
			rows.Close()
			return 0, err
		}
		if aOffer.Valid && uint64(aOffer.Int64) == shinyID {
			hits = append(hits, hit{thread, "a", aLabel, aUser, bUser})
		} else {
			hits = append(hits, hit{thread, "b", bLabel, bUser, aUser})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	changed := 0
	for _, x := range hits {
		if x.before == newLabel {
			continue // an edit that does not change what is offered (say, only the catch date)
		}
		params, _ := json.Marshal(map[string]string{"before": x.before, "after": newLabel, "side": x.side})
		tx, err := h.db.Begin()
		if err != nil {
			return changed, err
		}
		// x.side is "a" or "b" from the code above, never from input. Same rechecks as
		// tradeOfferRemoved: a thread closed meanwhile, or an offer swapped meanwhile, is left alone.
		res, err := tx.Exec(`UPDATE trade_threads SET `+x.side+`_offer_label = ?,
			state = 'open', a_confirm = 'none', b_confirm = 'none',
			`+x.side+`_last_read_at = IF(`+x.side+`_last_read_at >= last_message_at AND hidden_at IS NULL, ?, `+x.side+`_last_read_at),
			last_message_at = IF(hidden_at IS NULL, ?, last_message_at)
			WHERE id = ? AND state IN ('open', 'agreed') AND `+x.side+`_offer_shiny_id = ?`, newLabel, now, now, x.thread, shinyID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				tx.Rollback()
				continue
			}
			_, err = tx.Exec(`INSERT INTO trade_messages (thread_id, from_user, preset_key, params, created_at)
				VALUES (?, NULL, 'sys_offer_modified', ?, ?)`, x.thread, string(params), now)
		}
		if err != nil {
			tx.Rollback()
			return changed, err
		}
		if err := tx.Commit(); err != nil {
			return changed, err
		}
		changed++
		if x.giver.Valid && x.taker.Valid {
			h.notifyTrade(uint(x.giver.Int64), uint(x.taker.Int64), "trade_modified", x.thread)
		}
	}
	return changed, nil
}
