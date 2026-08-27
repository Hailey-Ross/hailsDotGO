package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// Event reminder subscriptions.
//
// The app owns the UI (a bell per upcoming event card, a per-event lead time, a
// default in Settings) and schedules nothing itself: it PUTs a subscription and
// this file decides when the push goes out. Everything below hangs off one hard
// fact about the upstream feed, recorded at length on pogodata.ParseFeedTime:
// most events carry a FLOATING wall clock start with no zone at all, because a
// Spotlight Hour is 6pm wherever the trainer is standing. Only GO Battle League
// rotations, which begin at the same moment worldwide, carry a Z.
//
// Nothing in the schema knows a trainer's timezone, so the app sends an IANA id
// with every subscription and re-sends all of them when the device moves. That id
// is the only thing that turns "6pm" into an instant, which is why an unknown one
// is a 400 here rather than a quiet fall back to the server's own zone.
//
// Times: every DATETIME in event_subscriptions is UTC. The MySQL driver runs with
// no explicit loc, so it converts on the way in and parses on the way out as UTC,
// and the SQL below compares against UTC_TIMESTAMP() rather than NOW() so the
// sweep does not depend on how the database server's session zone is set.

// eventReminderLeadMax is a week, the longest lead time the app offers. The app
// picks from a fixed list (0, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880,
// 10080) but this range checks rather than enumerating, so a new entry in that
// list does not need a server release to work.
const eventReminderLeadMax = 7 * 24 * 60

// eventSweepInterval is how often due reminders are looked for. A minute is
// plenty: the shortest lead the app offers is five, and the feed these times come
// from is only refreshed every thirty.
const eventSweepInterval = time.Minute

// Push identifiers. Spell them exactly: the app switches on the type string and
// creates the channel under this id, and a name that exists only in a design note
// is how the confirm_30s_warning near miss happened.
const (
	pushChannelEvents     = "events"
	pushTypeEventReminder = "event_reminder"
	pushTypeEventStarting = "event_starting"
)

// eventSubscription is the wire shape, shared by all three routes.
//
// remind_at and starts_at are the absolute instants the server resolved, sent
// back purely so the app can render "Reminder arrives ...". remind_at is null
// when the lead time is zero, which is a real choice in the picker and means
// "no advance warning, just tell me when it starts".
type eventSubscription struct {
	EventID     string  `json:"event_id"`
	LeadMinutes int     `json:"lead_minutes"`
	Timezone    string  `json:"timezone"`
	RemindAt    *string `json:"remind_at"`
	StartsAt    string  `json:"starts_at"`
}

// eventFeedIndex returns the current feed keyed by event id.
//
// The feed can legitimately carry the same id twice; first entry wins, matching
// what planDetailRefresh does with its active set. Returns nil when the feed is
// missing or unparseable, and every caller treats nil as "know nothing", never as
// "no events exist". The difference matters, because the reconcile below CANCELS
// subscriptions for ids that are absent.
func (h *Handlers) eventFeedIndex() map[string]icsEvent {
	raw := h.store.Events()
	if len(raw) == 0 {
		return nil
	}
	var events []icsEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		log.Printf("event subs: feed parse: %v", err)
		return nil
	}
	idx := make(map[string]icsEvent, len(events))
	for _, e := range events {
		if e.EventID == "" {
			continue
		}
		if _, dup := idx[e.EventID]; dup {
			continue
		}
		idx[e.EventID] = e
	}
	return idx
}

// eventStartFingerprint parses a feed start string as UTC whichever kind it is,
// giving a zone-independent record of what upstream said.
//
// This is NOT the instant the event begins for anybody; resolveEventStart is. It
// exists to answer one question on refresh: did upstream move this event, or is
// this a different occurrence reusing the slug? Recurring Spotlight Hours put
// their date in the id, so each one is already a distinct subscription, but GO
// Battle League ids do not: gbl-forever-forward_great-league_ultra-league names
// the league split and comes round again on a later rotation. Without this, that
// second rotation would re-fire a reminder the trainer never asked for.
func eventStartFingerprint(raw string) (time.Time, bool) {
	t, _, ok := pogodata.ParseFeedTime(raw, time.UTC)
	return t.UTC(), ok
}

// resolveEventStart turns a feed start into the instant it happens for a trainer
// in loc. A Z-suffixed start is already absolute and loc is ignored for it.
func resolveEventStart(raw string, loc *time.Location) (time.Time, bool) {
	t, _, ok := pogodata.ParseFeedTime(raw, loc)
	return t.UTC(), ok
}

// loadEventTimezone resolves an IANA id sent by the app.
//
// "Local" and the empty string are refused even though time.LoadLocation accepts
// both: they resolve to the server's own zone and to UTC, which is exactly the
// silent wrong-zone answer this whole path exists to avoid. A rejected subscribe
// is something the app can report; a reminder quietly pinned to the wrong hour is
// not.
func loadEventTimezone(name string) (*time.Location, bool) {
	if name == "" || name == "Local" || len(name) > 64 {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// APIEventSubscriptions lists the caller's event reminders.
//
// Deliberately not writeJSON: that helper sets Cache-Control: public, max-age=300,
// which is right for the shared events blob and would invite a shared cache to
// hand one trainer's subscriptions to another.
func (h *Handlers) APIEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	rows, err := h.db.Query(`
		SELECT event_id, lead_minutes, timezone, remind_at, starts_at
		FROM event_subscriptions
		WHERE user_id = ?
		ORDER BY starts_at ASC`, u.ID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// [] not nil, so an empty result marshals as [] rather than null.
	subs := []eventSubscription{}
	for rows.Next() {
		var s eventSubscription
		var remindAt sql.NullTime
		var startsAt time.Time
		if rows.Scan(&s.EventID, &s.LeadMinutes, &s.Timezone, &remindAt, &startsAt) != nil {
			continue
		}
		s.StartsAt = startsAt.UTC().Format(time.RFC3339)
		if remindAt.Valid {
			v := remindAt.Time.UTC().Format(time.RFC3339)
			s.RemindAt = &v
		}
		subs = append(subs, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(subs)
}

// APIEventSubscribe upserts one subscription.
//
// Upsert on purpose: subscribing and changing a lead time are the same call, so a
// retry after a dropped connection is safe to repeat. It is also the call the app
// makes for EVERY subscription when the device's timezone changes, and it only
// records the new zone locally once all of them have succeeded.
func (h *Handlers) APIEventSubscribe(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	// eventIDPattern, the one in events_ics.go, not a second copy. The underscore
	// it allows is load bearing: a class without it 400s every GBL event.
	eventID := chi.URLParam(r, "eventId")
	if !eventIDPattern.MatchString(eventID) {
		writeJSONError(w, "invalid event id", http.StatusBadRequest)
		return
	}

	var body struct {
		LeadMinutes *int   `json:"lead_minutes"`
		Timezone    string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	// A pointer, so a missing field is told apart from an explicit 0. Zero is a
	// real choice in the picker and must not be read as "not supplied".
	if body.LeadMinutes == nil {
		writeJSONError(w, "lead_minutes required", http.StatusBadRequest)
		return
	}
	lead := *body.LeadMinutes
	if lead < 0 || lead > eventReminderLeadMax {
		writeJSONError(w, "lead_minutes out of range", http.StatusBadRequest)
		return
	}
	loc, ok := loadEventTimezone(body.Timezone)
	if !ok {
		writeJSONError(w, "unknown timezone", http.StatusBadRequest)
		return
	}

	ev, found := h.eventFeedIndex()[eventID]
	if !found {
		writeJSONError(w, "unknown event", http.StatusNotFound)
		return
	}
	startsAt, ok := resolveEventStart(ev.Start, loc)
	if !ok {
		writeJSONError(w, "event has no usable start time", http.StatusBadRequest)
		return
	}
	fingerprint, ok := eventStartFingerprint(ev.Start)
	if !ok {
		writeJSONError(w, "event has no usable start time", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	remindAt := eventRemindAt(startsAt, lead)
	// Skip, do not fire late. Subscribing to an event that starts in ten minutes
	// with a one hour lead must send nothing now, so the flag goes in already set
	// and the sweep never picks the row up. The start push still fires normally.
	reminded := sentIfPast(remindAt, now)
	started := sentIfPast(&startsAt, now)

	_, err := h.db.Exec(`
		INSERT INTO event_subscriptions
			(user_id, event_id, lead_minutes, timezone, event_start, remind_at, starts_at, reminded_at, started_at_sent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			lead_minutes    = VALUES(lead_minutes),
			timezone        = VALUES(timezone),
			event_start     = VALUES(event_start),
			remind_at       = VALUES(remind_at),
			starts_at       = VALUES(starts_at),
			reminded_at     = VALUES(reminded_at),
			started_at_sent = VALUES(started_at_sent)`,
		u.ID, eventID, lead, body.Timezone, fingerprint, remindAt, startsAt, reminded, started,
	)
	if err != nil {
		log.Printf("event subs: upsert user=%d event=%s: %v", u.ID, eventID, err)
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}

	out := eventSubscription{
		EventID:     eventID,
		LeadMinutes: lead,
		Timezone:    body.Timezone,
		StartsAt:    startsAt.Format(time.RFC3339),
	}
	if remindAt != nil {
		v := remindAt.Format(time.RFC3339)
		out.RemindAt = &v
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// APIEventUnsubscribe turns a bell off. Idempotent: a row that was already gone
// is still a 204, so the app does not have to care whether its list was stale.
func (h *Handlers) APIEventUnsubscribe(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	eventID := chi.URLParam(r, "eventId")
	if !eventIDPattern.MatchString(eventID) {
		writeJSONError(w, "invalid event id", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(
		`DELETE FROM event_subscriptions WHERE user_id = ? AND event_id = ?`, u.ID, eventID,
	); err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// eventRemindAt is the instant the advance reminder is due, or nil for a zero
// lead time, which means the trainer wants only the start notification.
func eventRemindAt(startsAt time.Time, leadMinutes int) *time.Time {
	if leadMinutes <= 0 {
		return nil
	}
	t := startsAt.Add(-time.Duration(leadMinutes) * time.Minute)
	return &t
}

// sentIfPast returns now when at is non-nil and has already passed, so a flag
// column can be pre-set for a push that must never go out, and nil otherwise.
func sentIfPast(at *time.Time, now time.Time) *time.Time {
	if at == nil || at.After(now) {
		return nil
	}
	n := now
	return &n
}

// StartEventReminderSweeper launches the background goroutine that sends due
// event reminders. Call once from server.New.
//
// Like the raid sweeper, this is a bare `for range ticker.C` with no done
// channel: main shuts the HTTP server down gracefully but these goroutines die
// with the process. A row marked sent whose push had not yet left the building is
// lost. Acceptable for a reminder, and worth knowing.
func (h *Handlers) StartEventReminderSweeper() {
	go func() {
		t := time.NewTicker(eventSweepInterval)
		for range t.C {
			h.processEventReminders()
		}
	}()
}

// processEventReminders sends every reminder and start notification that has come
// due, at most once each.
//
// Shape borrowed wholesale from processRaidTimers: read the due rows, close the
// cursor, then per row set the flag BEFORE dispatching, so a slow or hanging HTTP
// call cannot earn the trainer a second copy on the next tick. The push itself
// goes out under `go` for the same reason.
func (h *Handlers) processEventReminders() {
	// Its own mutex, not raidMu. That one is documented as serializing queue
	// matching against raid timer processing, and widening its job would couple
	// two sweeps that have nothing to do with each other.
	h.eventSubMu.Lock()
	defer h.eventSubMu.Unlock()

	feed := h.eventFeedIndex()

	h.dispatchDueEventPushes(feed,
		`SELECT id, user_id, event_id, lead_minutes FROM event_subscriptions
		 WHERE remind_at IS NOT NULL AND reminded_at IS NULL AND remind_at <= UTC_TIMESTAMP()`,
		`UPDATE event_subscriptions SET reminded_at = UTC_TIMESTAMP() WHERE id = ? AND reminded_at IS NULL`,
		pushTypeEventReminder)

	h.dispatchDueEventPushes(feed,
		`SELECT id, user_id, event_id, lead_minutes FROM event_subscriptions
		 WHERE started_at_sent IS NULL AND starts_at <= UTC_TIMESTAMP()`,
		`UPDATE event_subscriptions SET started_at_sent = UTC_TIMESTAMP() WHERE id = ? AND started_at_sent IS NULL`,
		pushTypeEventStarting)
}

// dispatchDueEventPushes runs one of the two sweeps. Caller holds eventSubMu.
func (h *Handlers) dispatchDueEventPushes(feed map[string]icsEvent, selectSQL, markSQL, pushType string) {
	type due struct {
		id      uint64
		userID  uint
		eventID string
		lead    int
	}
	rows, err := h.db.Query(selectSQL)
	if err != nil {
		log.Printf("event subs: %s sweep: %v", pushType, err)
		return
	}
	var pending []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.userID, &d.eventID, &d.lead) == nil {
			pending = append(pending, d)
		}
	}
	rows.Close()

	for _, d := range pending {
		res, err := h.db.Exec(markSQL, d.id)
		if err != nil {
			log.Printf("event subs: mark %s id=%d: %v", pushType, d.id, err)
			continue
		}
		// A zero row count means something else already claimed it. Nothing writes
		// these columns concurrently today, but skipping costs nothing and a
		// duplicate push is the one failure a trainer actually notices.
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			continue
		}

		title := "Pokémon GO event"
		if ev, ok := feed[d.eventID]; ok && ev.Name != "" {
			title = ev.Name
		}
		body := "Starting now"
		if pushType == pushTypeEventReminder {
			body = "Starts in " + leadPhrase(d.lead)
		}
		// notification AND data together: the app's onMessageReceived returns
		// early when notification.body is absent, so a data-only push renders
		// nothing at all in the foreground.
		//
		// The channel is what keeps a backgrounded reminder off the raids
		// channel, where it would arrive styled and prioritised as a raid alert
		// with nothing logged anywhere to say so.
		go h.sendPushToUsersOnChannel([]uint{d.userID}, title, body,
			map[string]string{"type": pushType, "event_id": d.eventID},
			pushChannelEvents)
	}
}

// leadPhrase renders a lead time the way the notification should read it. The
// input is range checked rather than enumerated, so this handles any minute count
// and not only the thirteen the picker currently offers.
func leadPhrase(minutes int) string {
	switch {
	case minutes >= 1440 && minutes%1440 == 0:
		return pluralUnit(minutes/1440, "day")
	case minutes >= 60 && minutes%60 == 0:
		return pluralUnit(minutes/60, "hour")
	default:
		return pluralUnit(minutes, "minute")
	}
}

func pluralUnit(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// ReconcileEventSubscriptions re-pins every subscription to the feed that has
// just replaced the old one. Registered on the store as its events-applied hook,
// so it runs after each successful refresh (every 30 minutes, plus boot).
//
// Two jobs. Events that have LEFT the feed lose their subscriptions: the feed
// carries only current and upcoming events, and the detail cache is already
// evicted on exactly this rule. Events whose start MOVED get their instants
// recomputed, because LeekDuck does shift events and a reminder pinned at
// subscribe time goes stale.
//
// Note the granularity this inherits: an upstream time change can be up to half
// an hour stale here, which matters for the shortest lead times. Tightening the
// events ticker to fix that would cost more upstream traffic than the case is
// worth.
func (h *Handlers) ReconcileEventSubscriptions() {
	feed := h.eventFeedIndex()
	// Empty means the feed is missing or would not parse, NOT that every event
	// ended. Bailing out is the difference between skipping one pass and deleting
	// every subscription on the site.
	if len(feed) == 0 {
		return
	}

	h.eventSubMu.Lock()
	defer h.eventSubMu.Unlock()

	rows, err := h.db.Query(`
		SELECT id, event_id, lead_minutes, timezone, event_start, starts_at, reminded_at, started_at_sent
		FROM event_subscriptions`)
	if err != nil {
		log.Printf("event subs: reconcile query: %v", err)
		return
	}
	type sub struct {
		id          uint64
		eventID     string
		lead        int
		timezone    string
		eventStart  time.Time
		startsAt    time.Time
		reminded    sql.NullTime
		startedSent sql.NullTime
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if rows.Scan(&s.id, &s.eventID, &s.lead, &s.timezone, &s.eventStart, &s.startsAt, &s.reminded, &s.startedSent) == nil {
			subs = append(subs, s)
		}
	}
	rows.Close()

	now := time.Now().UTC()
	var cancelled, moved int
	for _, s := range subs {
		ev, still := feed[s.eventID]
		if !still {
			if _, err := h.db.Exec(`DELETE FROM event_subscriptions WHERE id = ?`, s.id); err == nil {
				cancelled++
			}
			continue
		}
		fingerprint, ok := eventStartFingerprint(ev.Start)
		if !ok {
			// Fail open, the way the detail cache does with an unparseable end
			// time: an upstream format change should not silently unpin every
			// reminder on the site.
			continue
		}
		if fingerprint.Equal(s.eventStart.UTC()) {
			continue
		}
		loc, ok := loadEventTimezone(s.timezone)
		if !ok {
			// The zone was valid when it was stored, so this is tzdata changing
			// underneath us. Leave the row exactly as it is rather than
			// re-resolving it somewhere else.
			log.Printf("event subs: reconcile: unknown timezone %q on id=%d", s.timezone, s.id)
			continue
		}
		startsAt, ok := resolveEventStart(ev.Start, loc)
		if !ok {
			continue
		}
		remindAt := eventRemindAt(startsAt, s.lead)

		// A start that moved LATER un-sends its flags, so the reminder fires again
		// at the new time. One that moved earlier past the post keeps them: a
		// trainer should not be told an event starts in thirty minutes because the
		// schedule shifted under a reminder that already went out.
		later := startsAt.After(s.startsAt.UTC())
		reminded := carryFlag(s.reminded, remindAt, later, now)
		started := carryFlag(s.startedSent, &startsAt, later, now)

		if _, err := h.db.Exec(`
			UPDATE event_subscriptions
			SET event_start = ?, starts_at = ?, remind_at = ?, reminded_at = ?, started_at_sent = ?
			WHERE id = ?`,
			fingerprint, startsAt, remindAt, reminded, started, s.id,
		); err != nil {
			log.Printf("event subs: reconcile update id=%d: %v", s.id, err)
			continue
		}
		moved++
	}
	if cancelled > 0 || moved > 0 {
		log.Printf("event subs: reconcile: %d re-pinned, %d cancelled", moved, cancelled)
	}
}

// carryFlag decides what a sent-flag becomes once its due instant has moved.
//
//   - due is nil (a zero lead time has no reminder): nothing to send, clear it.
//   - the event moved later and the new instant is still ahead: clear it, so the
//     push fires again when the event actually arrives.
//   - otherwise keep what was there, and if that is "not sent" for an instant now
//     in the past, mark it sent: the moment is gone, and a late reminder is worse
//     than a missed one.
func carryFlag(current sql.NullTime, due *time.Time, movedLater bool, now time.Time) *time.Time {
	if due == nil {
		return nil
	}
	if movedLater && due.After(now) {
		return nil
	}
	if current.Valid {
		t := current.Time
		return &t
	}
	return sentIfPast(due, now)
}
