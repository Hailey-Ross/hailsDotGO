package handlers

// Trade Assist, Phase 8: the sweep. Plan: .claude/trade-assist-build-plan-11-18-26.md.
//
// Every tradeSweepInterval, for the life of the process:
//   - connect requests and connections past their 15 days become expired;
//   - owner decision C1: a trade one trainer confirmed and the other has not answered gets a
//     reminder to the silent trainer after 3 days, and after 7 closes as "unverified, possibly
//     completed" (state unconfirmed, nothing moves, a trade_unverified 'silent' row for the silent
//     trainer); the confirmer is never held back by it;
//   - conversations with no message for 14 days are hidden from both trainers (staff still see them);
//   - hidden conversations older than a year are deleted, unless a player report points at one;
//   - ended cooldowns are deleted, but an automatic one only a day after it ended: the row is the
//     escalation history (Phase 2 review).
//
// Like StartUserPurge, it does not run at startup: nothing here is urgent, and a restart loop
// must not become a sweep loop. Every step is a guarded UPDATE or DELETE, so two processes
// running it at once (a deploy overlap) do no harm.

import (
	"database/sql"
	"log"
	"time"
)

const (
	tradeSweepInterval = 15 * time.Minute
	tradeRemindAfter   = 3 * 24 * time.Hour
	tradeSilentClose   = 7 * 24 * time.Hour
	tradeIdleHide      = 14 * 24 * time.Hour
	tradeRetention     = 365 * 24 * time.Hour
)

// StartTradeSweep runs sweepTrades on a ticker.
func (h *Handlers) StartTradeSweep() {
	go func() {
		for range time.NewTicker(tradeSweepInterval).C {
			n, err := h.sweepTrades(time.Now())
			if err != nil {
				log.Printf("trade sweep: %v", err)
			}
			for k, v := range n {
				if v > 0 {
					log.Printf("trade sweep: %s %d", k, v)
				}
			}
		}
	}()
}

// sweepTrades does one pass and says how many rows each step changed.
func (h *Handlers) sweepTrades(now time.Time) (map[string]int, error) {
	now = now.UTC().Truncate(time.Second)
	n := map[string]int{}
	exec := func(k, q string, args ...any) error {
		res, err := h.db.Exec(q, args...)
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		n[k] += int(c)
		return nil
	}

	if err := exec("contacts expired", `UPDATE trade_contacts SET state = 'expired'
		WHERE state IN ('pending', 'accepted', 'declined') AND expires_at <= ?`, now); err != nil {
		return n, err
	}
	if err := h.sweepSilentConfirms(now, n); err != nil {
		return n, err
	}
	// An idle trade that is still open or agreed ends as cancelled when it is hidden, so the shinies
	// it offers are no longer tied to a trade nobody can reach (Phase 8 review). MySQL applies SET
	// left to right, so state is decided before hidden_at changes.
	if err := exec("conversations hidden", `UPDATE trade_threads
		SET state = IF(state IN ('open', 'agreed'), 'cancelled', state), hidden_at = ?
		WHERE hidden_at IS NULL AND last_message_at <= ?`, now, now.Add(-tradeIdleHide)); err != nil {
		return n, err
	}
	if err := exec("conversations deleted", `DELETE FROM trade_threads
		WHERE hidden_at IS NOT NULL AND hidden_at <= ?
		  AND NOT EXISTS (SELECT 1 FROM bug_reports b WHERE b.trade_thread_id = trade_threads.id)`,
		now.Add(-tradeRetention)); err != nil {
		return n, err
	}
	if err := exec("cooldowns removed", `DELETE FROM trade_cooldowns
		WHERE (reason LIKE 'auto\_rapid\_%' AND until_at <= ?)
		   OR (reason NOT LIKE 'auto\_rapid\_%' AND until_at <= ?)`, now.Add(-tradeRunReset), now); err != nil {
		return n, err
	}
	return n, nil
}

// sweepSilentConfirms applies owner decision C1 to agreed trades that one trainer confirmed. The
// clock starts at that confirmation, which writes a sys_confirmed message (APITradeConfirm).
func (h *Handlers) sweepSilentConfirms(now time.Time, n map[string]int) error {
	rows, err := h.db.Query(`
		SELECT t.id, t.a_user, t.b_user, t.a_confirm, t.b_confirm,
		       (SELECT MAX(m.created_at) FROM trade_messages m WHERE m.thread_id = t.id AND m.preset_key = 'sys_confirmed'),
		       (SELECT MAX(m.created_at) FROM trade_messages m WHERE m.thread_id = t.id AND m.preset_key = 'sys_reminder')
		FROM trade_threads t
		WHERE t.state = 'agreed'
		  AND ((t.a_confirm = 'done' AND t.b_confirm = 'none') OR (t.b_confirm = 'done' AND t.a_confirm = 'none'))`)
	if err != nil {
		return err
	}
	type due struct {
		id                  uint64
		confirmer, silent   sql.NullInt64
		silentSide          string
		confirmed, reminded sql.NullTime
	}
	var list []due
	for rows.Next() {
		var d due
		var a, b sql.NullInt64
		var ac, bc string
		if err := rows.Scan(&d.id, &a, &b, &ac, &bc, &d.confirmed, &d.reminded); err != nil {
			rows.Close()
			return err
		}
		if ac == "done" {
			d.confirmer, d.silent, d.silentSide = a, b, "b"
		} else {
			d.confirmer, d.silent, d.silentSide = b, a, "a"
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, d := range list {
		if !d.confirmed.Valid {
			continue // confirmed before confirmations were timestamped: nothing to count from
		}
		waited := now.Sub(d.confirmed.Time)
		switch {
		case waited >= tradeSilentClose:
			closed, err := h.closeSilentTrade(d.id, d.silentSide, d.silent, now)
			if err != nil {
				return err
			}
			if closed {
				n["silent trades closed"]++
				if d.confirmer.Valid {
					from := uint(0)
					if d.silent.Valid {
						from = uint(d.silent.Int64)
					}
					h.notifyTrade(from, uint(d.confirmer.Int64), "trade_unconfirmed", d.id)
				}
			}
		case waited >= tradeRemindAfter && (!d.reminded.Valid || d.reminded.Time.Before(d.confirmed.Time)):
			sent, err := h.sendTradeReminder(d.id, d.silentSide, d.confirmed.Time, now)
			if err != nil {
				return err
			}
			if !sent {
				continue
			}
			n["reminders sent"]++
			if d.silent.Valid && d.confirmer.Valid {
				h.notifyTrade(uint(d.confirmer.Int64), uint(d.silent.Int64), "trade_reminder", d.id)
			}
		}
	}
	return nil
}

// closeSilentTrade closes one agreed trade whose silent side never answered: unconfirmed, nothing
// moves, and an internal 'silent' record for the silent trainer. The UPDATE rechecks the state, so
// an answer that arrived meanwhile wins.
func (h *Handlers) closeSilentTrade(id uint64, silentSide string, silent sql.NullInt64, now time.Time) (bool, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// silentSide is "a" or "b" from the caller, never from input.
	res, err := tx.Exec(`UPDATE trade_threads SET state = 'unconfirmed'
		WHERE id = ? AND state = 'agreed' AND `+silentSide+`_confirm = 'none'`, id)
	if err != nil {
		return false, err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return false, nil
	}
	if silent.Valid {
		if _, err := tx.Exec(`INSERT INTO trade_unverified (user_id, thread_id, kind, created_at) VALUES (?, ?, 'silent', ?)`,
			silent.Int64, id, now); err != nil {
			return false, err
		}
	}
	if err := addThreadMessage(tx, id, 0, "sys_unconfirmed", map[string]any{"silent": true}, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// sendTradeReminder writes the C1 reminder once: under a lock on the conversation it checks again
// that the trade is still agreed and no reminder followed this confirmation, so two sweeps at
// the same moment send one (Phase 8 review). The reminder is the confirmer's doing as far as
// notices go, so it stays read for them when they were caught up.
func (h *Handlers) sendTradeReminder(id uint64, silentSide string, confirmed, now time.Time) (bool, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRow(`SELECT state FROM trade_threads WHERE id = ? FOR UPDATE`, id).Scan(&state); err != nil {
		return false, err
	}
	if state != "agreed" {
		return false, nil
	}
	var reminded sql.NullTime
	tx.QueryRow(`SELECT MAX(created_at) FROM trade_messages WHERE thread_id = ? AND preset_key = 'sys_reminder'`, id).Scan(&reminded)
	if reminded.Valid && !reminded.Time.Before(confirmed) {
		return false, nil
	}
	confirmer := otherSide(silentSide) // "a" or "b", never input
	if _, err := tx.Exec(`UPDATE trade_threads SET `+confirmer+`_last_read_at = ?
		WHERE id = ? AND `+confirmer+`_last_read_at >= last_message_at`, now, id); err != nil {
		return false, err
	}
	if err := addThreadMessage(tx, id, 0, "sys_reminder", map[string]any{"side": silentSide}, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
