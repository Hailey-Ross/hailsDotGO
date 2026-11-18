package handlers

// Trade Assist, Phase 7: notices. Plan: .claude/trade-assist-build-plan-11-18-26.md.
//
// Two ways a trainer hears about a trade:
//   - a push to their phones, in their own language, on the Android channel "trades";
//   - notices: GET /api/trades/notices, the bell's trade items with their text already written in
//     the caller's language, and the unread counts for the bell and the Messaging badge.
//
// Read state lives on the server (trade_threads.*_last_read_at: opening a conversation reads it;
// a connect request stays a notice until it is answered), so it follows the trainer across
// devices. /api/notifications is deliberately left alone: it is a bare list of raid lobbies that
// the Android app also parses, and mixing trade items into it would put strange entries there.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pogo.hails.cc/internal/i18n"
)

// tradeDisplayName is how a trainer is named in a notice or push: their trainer name when their
// profile is public, else their username. A private profile hides its trainer name everywhere
// else on the site, so it does here too (Phase 7 review).
func (h *Handlers) tradeDisplayName(userID uint) string {
	var username, trainer string
	var public bool
	h.db.QueryRow(`SELECT username, trainer_name, profile_public FROM users WHERE id = ?`, userID).Scan(&username, &trainer, &public)
	if public && strings.TrimSpace(trainer) != "" {
		return trainer
	}
	return username
}

// notifyTrade tells trainer `to` that trainer `from` did something in Trade Assist: a push to their
// devices in their language. kind is one of the trade.push.<kind> keys in the locale files. ref
// is the conversation id, or the connect request id for contact_* kinds. The send runs in the
// background; a failure is logged and never reaches the caller. The bell needs nothing here: its
// notices are read from the tables.
func (h *Handlers) notifyTrade(from, to uint, kind string, ref uint64) {
	if to == 0 || to == from {
		return
	}
	go func() {
		name := h.tradeDisplayName(from)
		// A kind without its own text (an unusual state from a race) reads as a new message, never
		// as a raw key.
		if !i18n.HasKey("trade.push." + kind + ".title") {
			kind = "trade_message"
		}
		title := strings.ReplaceAll(h.tFor(to, "trade.push."+kind+".title"), "{name}", name)
		body := strings.ReplaceAll(h.tFor(to, "trade.push."+kind+".body"), "{name}", name)
		data := map[string]string{"type": "trade", "kind": kind}
		if strings.HasPrefix(kind, "contact_") {
			data["contact_id"] = strconv.FormatUint(ref, 10)
		} else {
			data["thread_id"] = strconv.FormatUint(ref, 10)
		}
		res := h.sendPushToUsersOnChannel([]uint{to}, title, body, data, "trades")
		log.Printf("trades: push %s to user %d (%d of %d devices)", kind, to, res.Delivered, res.Devices)
	}()
}

// tradeNotice is one bell item.
type tradeNotice struct {
	Kind      string    `json:"kind"` // contact_request, trade_request, message, agreed, confirm_needed, modified, completed, closed
	Text      string    `json:"text"`
	ThreadID  uint64    `json:"thread_id,omitempty"`
	ContactID uint64    `json:"contact_id,omitempty"`
	At        time.Time `json:"at"`
	Unread    bool      `json:"unread"`
}

// tradeNoticesFor builds the caller's trade notices, newest first, and the unread counts.
// messages counts conversations with something unread or a confirm waiting on the caller;
// contacts counts connect requests waiting for the caller's answer.
func (h *Handlers) tradeNoticesFor(userID uint, t func(string) string, now time.Time) ([]tradeNotice, int, int, error) {
	items := []tradeNotice{}
	messages, contacts := 0, 0

	rows, err := h.db.Query(`
		SELECT c.id, c.created_at, u.username, IF(u.profile_public = 1, u.trainer_name, '')
		FROM trade_contacts c JOIN users u ON u.id = c.from_user
		WHERE c.to_user = ? AND c.state = 'pending' AND c.expires_at > ? AND `+tradeLiveUser+`
		  AND NOT EXISTS (SELECT 1 FROM user_blocks b
		                  WHERE (b.user_id = ? AND b.blocked_id = u.id) OR (b.user_id = u.id AND b.blocked_id = ?))
		ORDER BY c.created_at DESC LIMIT 50`, userID, now.UTC(), userID, userID)
	if err != nil {
		return nil, 0, 0, err
	}
	for rows.Next() {
		var n tradeNotice
		var username, trainer string
		if err := rows.Scan(&n.ContactID, &n.At, &username, &trainer); err != nil {
			rows.Close()
			return nil, 0, 0, err
		}
		if strings.TrimSpace(trainer) == "" {
			trainer = username
		}
		n.Kind, n.Unread = "contact_request", true
		n.Text = strings.ReplaceAll(t("trade.notice.contact_request"), "{name}", trainer)
		items = append(items, n)
		contacts++
	}
	rows.Close()

	// Only conversations that can produce a notice (unread, or agreed and maybe waiting on a
	// confirm), so the limit never hides an old unread one behind newer read ones (Phase 7 review).
	threads, err := h.db.Query(`SELECT `+tradeThreadCols+` FROM trade_threads
		WHERE hidden_at IS NULL AND (
		      (a_user = ? AND (state = 'agreed' OR a_last_read_at IS NULL OR a_last_read_at < last_message_at))
		   OR (b_user = ? AND (state = 'agreed' OR b_last_read_at IS NULL OR b_last_read_at < last_message_at)))
		ORDER BY last_message_at DESC LIMIT 50`, userID, userID)
	if err != nil {
		return nil, 0, 0, err
	}
	var list []tradeThread
	for threads.Next() {
		th, err := scanTradeThread(threads)
		if err != nil {
			threads.Close()
			return nil, 0, 0, err
		}
		list = append(list, th)
	}
	threads.Close()

	for _, th := range list {
		s := th.side(userID)
		other := otherSide(s)
		// A blocked partner (either way) or a disabled or deleted one makes no notices. A purged
		// one has a NULL id and keeps its saved name: the conversation is still the caller's.
		if partner := pick(other, th.A, th.B); partner.Valid {
			if err := h.canContact(userID, uint(partner.Int64), now); err != nil && !errors.Is(err, errTradeCooldown) {
				continue
			}
		}
		confirmNeeded := th.State == "agreed" && pick(other, th.AConfirm, th.BConfirm) == "done" && pick(s, th.AConfirm, th.BConfirm) == "none"
		unread := th.unread(s)
		if !unread && !confirmNeeded {
			continue
		}
		var key string
		var from sql.NullInt64
		var params sql.NullString
		h.db.QueryRow(`SELECT preset_key, from_user, params FROM trade_messages WHERE thread_id = ? ORDER BY id DESC LIMIT 1`, th.ID).
			Scan(&key, &from, &params)
		fromMe := from.Valid && uint(from.Int64) == userID
		if !confirmNeeded && fromMe {
			continue // the caller's own last word is nothing to be told about
		}
		kind := "message"
		switch {
		case confirmNeeded:
			kind = "confirm_needed"
		case key == "req_trade_for" || key == "req_trade_open":
			kind = "trade_request"
		case key == "sys_agreed":
			kind = "agreed"
		case key == "sys_reminder" || key == "sys_confirmed":
			// sys_confirmed is about the confirmer, sys_reminder is sent to the silent one: the
			// waiting one already shows as confirm_needed above, so neither is news to anyone.
			continue
		case key == "sys_offer_removed":
			kind = "modified"
		case key == "sys_offer_modified":
			var p map[string]string
			json.Unmarshal([]byte(params.String), &p)
			if p["side"] == s {
				continue // the caller's own edit
			}
			kind = "modified"
		case key == "sys_completed":
			kind = "completed"
		case key == "sys_unconfirmed" || key == "cancel" || key == "decline" || key == "sys_closed_block":
			kind = "closed"
		}
		name := pick(other, th.AName, th.BName)
		if partner := pick(other, th.A, th.B); partner.Valid {
			name = h.tradeDisplayName(uint(partner.Int64))
		}
		items = append(items, tradeNotice{
			Kind: kind, ThreadID: th.ID, At: th.LastMessage, Unread: true,
			Text: strings.ReplaceAll(t("trade.notice."+kind), "{name}", name),
		})
		messages++
	}
	// Newest first across both kinds.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].At.After(items[j-1].At); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items, messages, contacts, nil
}

// APITradeNotices is the caller's trade notices for the bell and the Messaging badge.
//
//	GET /api/trades/notices  ->  {"unread": {"messages": N, "contacts": N, "total": N}, "items": [...]}
//
// Each item's text is already in the caller's language. Reading the notices marks nothing read:
// opening the conversation does, and answering a connect request clears its notice.
func (h *Handlers) APITradeNotices(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !h.tradesOn(u) {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	items, messages, contacts, err := h.tradeNoticesFor(u.ID, func(k string) string { return h.t(r, k) }, time.Now())
	if err != nil {
		h.writeThreadError(w, r, err)
		return
	}
	tradeJSON(w, map[string]any{
		"unread": map[string]int{"messages": messages, "contacts": contacts, "total": messages + contacts},
		"items":  items,
	})
}

// MessagesPage is the Messaging page (/messages). Trade Assist must be open to the trainer (switched
// on, or staff); otherwise the page does not exist for them.
func (h *Handlers) MessagesPage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil || !h.tradesOn(u) {
		http.NotFound(w, r)
		return
	}
	h.render(w, r, "messages", nil)
}
