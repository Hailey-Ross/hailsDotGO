package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// Event reminder subscriptions.
//
// The app owns the UI (a bell per upcoming event card, per-event lead times, a
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
// A subscription carries a SET of reminders, not one: a day's warning to plan
// around and a thirty minute one to stop what you are doing are different
// requests about the same event. They live in event_subscription_reminders, one
// row each, and the sweep flag lives with them. Put that flag back on the parent
// and the first push of the evening silently eats the rest.
//
// Times: every DATETIME in both tables is UTC. The MySQL driver runs with no
// explicit loc, so it converts on the way in and parses on the way out as UTC,
// and the SQL below compares against UTC_TIMESTAMP() rather than NOW() so the
// sweep does not depend on how the database server's session zone is set.

// eventReminderLeadMax is a week, the longest lead time the app offers. The app
// picks from a fixed list (0, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880,
// 10080) but this range checks rather than enumerating, so a new entry in that
// list does not need a server release to work.
const eventReminderLeadMax = 7 * 24 * 60

// eventReminderMaxPerEvent caps how many reminders one subscription may carry.
// The app caps its own picker, but this endpoint is reachable by any Bearer
// holder, and this is what stops a single event fanning out to a hundred pushes.
const eventReminderMaxPerEvent = 5

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

// leadSet is the request's lead_minutes, which accepts EITHER a bare int or an
// array of them.
//
// Both shapes are live at once and that is not negotiable: build 15 of the app is
// installed on testers' phones today and sends a bare int. Dropping the scalar
// form turns every one of those installs into a subscription that cannot be
// written. The two forms are the same request, so `30` and `[30]` must reach the
// database identically.
type leadSet []int

func (l *leadSet) UnmarshalJSON(b []byte) error {
	var many []int
	if err := json.Unmarshal(b, &many); err == nil {
		*l = many
		return nil
	}
	var one int
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*l = []int{one}
	return nil
}

// normalize de-duplicates, range checks and orders a requested set, returning the
// reason it is unacceptable if it is.
//
// Ordering is longest lead first, which is also chronological: the day-before
// reminder both has the larger lead and arrives first. The app takes the head as
// "the one that arrives next" and shows it, so the order is part of the contract
// rather than a tidiness.
//
// De-duplication happens BEFORE the cap, so a client that repeats itself is
// corrected rather than rejected, while a genuine request for six distinct
// reminders is still refused.
func (l leadSet) normalize() ([]int, string) {
	if len(l) == 0 {
		// Clearing every reminder is a DELETE, which the app sends. An empty
		// array arriving here is a malformed write, not an instruction to
		// unsubscribe, and treating it as one would turn a client bug into a
		// silently cancelled reminder.
		return nil, "lead_minutes must not be empty"
	}
	for _, m := range l {
		if m < 0 || m > eventReminderLeadMax {
			return nil, "lead_minutes out of range"
		}
	}
	out := slices.Clone([]int(l))
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > eventReminderMaxPerEvent {
		return nil, "too many reminders for one event"
	}
	slices.Reverse(out) // longest lead first
	return out, ""
}

// eventReminder is one notification on a subscription, on the wire.
type eventReminder struct {
	LeadMinutes int     `json:"lead_minutes"`
	RemindAt    *string `json:"remind_at"`
}

// eventSubscription is the wire shape, shared by all three routes.
//
// remind_at and starts_at are the absolute instants the server resolved, sent
// back purely so the app can render "Reminder arrives ...". A reminder's
// remind_at is null when that lead time is already past, and a lead time of zero
// is a real choice in the picker meaning "no advance warning, just tell me when
// it starts".
//
// LeadMinutes and RemindAt at the top level are the FIRST reminder, repeated.
// They are there for build 15, which predates the array and reads that pair; they
// are written by nothing else and read by nothing newer. They come out once no
// install below 16 is in use.
type eventSubscription struct {
	EventID   string          `json:"event_id"`
	Reminders []eventReminder `json:"reminders"`
	Timezone  string          `json:"timezone"`
	StartsAt  string          `json:"starts_at"`

	LeadMinutes int     `json:"lead_minutes"`
	RemindAt    *string `json:"remind_at"`
}

// withLegacyFields fills the two build-15 fields from the head of the array, so
// there is exactly one place that decides what "the first reminder" means.
func (s *eventSubscription) withLegacyFields() {
	if s.Reminders == nil {
		s.Reminders = []eventReminder{}
	}
	if len(s.Reminders) == 0 {
		return
	}
	s.LeadMinutes = s.Reminders[0].LeadMinutes
	s.RemindAt = s.Reminders[0].RemindAt
}

// plannedReminder is one reminder as resolved against a start time, on its way to
// or from the database.
type plannedReminder struct {
	lead       int
	remindAt   *time.Time
	remindedAt *time.Time
}

// planReminders resolves a normalized lead set against a start instant.
//
// Skip, do not fire late: a lead time whose moment has already gone is stored
// with its flag already set, so the sweep never picks it up. Subscribing to an
// event that starts in ten minutes with a one hour lead sends nothing now. The
// at-start push is a separate question and still fires normally.
func planReminders(startsAt time.Time, leads []int, now time.Time) []plannedReminder {
	out := make([]plannedReminder, 0, len(leads))
	for _, lead := range leads {
		at := eventRemindAt(startsAt, lead)
		out = append(out, plannedReminder{lead: lead, remindAt: at, remindedAt: sentIfPast(at, now)})
	}
	return out
}

// wireReminders renders resolved reminders for the response.
func wireReminders(rs []plannedReminder) []eventReminder {
	out := make([]eventReminder, 0, len(rs))
	for _, r := range rs {
		w := eventReminder{LeadMinutes: r.lead}
		if r.remindAt != nil {
			v := r.remindAt.UTC().Format(time.RFC3339)
			w.RemindAt = &v
		}
		out = append(out, w)
	}
	return out
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
// Two queries and a join in Go rather than one query per subscription: a trainer
// with thirty bells would otherwise cost thirty round trips on every Events
// screen load, and the app loads this at start as well.
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
		SELECT id, event_id, timezone, starts_at
		FROM event_subscriptions
		WHERE user_id = ?
		ORDER BY starts_at ASC`, u.ID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	// [] not nil, so an empty result marshals as [] rather than null.
	subs := []eventSubscription{}
	atIndex := map[uint64]int{}
	for rows.Next() {
		var id uint64
		var s eventSubscription
		var startsAt time.Time
		if rows.Scan(&id, &s.EventID, &s.Timezone, &startsAt) != nil {
			continue
		}
		s.StartsAt = startsAt.UTC().Format(time.RFC3339)
		s.Reminders = []eventReminder{}
		atIndex[id] = len(subs)
		subs = append(subs, s)
	}
	rows.Close()
	if len(subs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(subs)
		return
	}

	// Ordered longest lead first, so the app can take the head as the next one to
	// arrive without sorting.
	remRows, err := h.db.Query(`
		SELECT r.subscription_id, r.lead_minutes, r.remind_at
		FROM event_subscription_reminders r
		JOIN event_subscriptions s ON s.id = r.subscription_id
		WHERE s.user_id = ?
		ORDER BY r.lead_minutes DESC`, u.ID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	for remRows.Next() {
		var subID uint64
		var er eventReminder
		var remindAt sql.NullTime
		if remRows.Scan(&subID, &er.LeadMinutes, &remindAt) != nil {
			continue
		}
		i, ok := atIndex[subID]
		if !ok {
			continue
		}
		if remindAt.Valid {
			v := remindAt.Time.UTC().Format(time.RFC3339)
			er.RemindAt = &v
		}
		subs[i].Reminders = append(subs[i].Reminders, er)
	}
	remRows.Close()

	for i := range subs {
		subs[i].withLegacyFields()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(subs)
}

// APIEventSubscribe upserts one subscription and REPLACES its set of reminders.
//
// Replace, not merge: the app sends the complete set it wants, so a reminder the
// trainer removed has to disappear rather than survive as a merge artefact. That
// also keeps the call idempotent, which matters because it is the call the app
// re-sends for EVERY subscription when the device's timezone changes, and it only
// records the new zone locally once all of them have succeeded.
//
// The whole write is one transaction. A half applied set is a trainer getting a
// reminder they believe they turned off, which is exactly the kind of bug nobody
// reports because it looks like they mis-tapped.
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
		LeadMinutes *leadSet `json:"lead_minutes"`
		Timezone    string   `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	// A pointer, so an absent field is told apart from an empty array. They are
	// different mistakes and 0 is neither: it is a real choice in the picker.
	if body.LeadMinutes == nil {
		writeJSONError(w, "lead_minutes required", http.StatusBadRequest)
		return
	}
	leads, badReason := body.LeadMinutes.normalize()
	if badReason != "" {
		writeJSONError(w, badReason, http.StatusBadRequest)
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
	planned := planReminders(startsAt, leads, now)
	if err := h.writeEventSubscription(u.ID, eventID, body.Timezone, fingerprint, startsAt, planned, now); err != nil {
		log.Printf("event subs: upsert user=%d event=%s: %v", u.ID, eventID, err)
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}

	out := eventSubscription{
		EventID:   eventID,
		Reminders: wireReminders(planned),
		Timezone:  body.Timezone,
		StartsAt:  startsAt.Format(time.RFC3339),
	}
	out.withLegacyFields()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// writeEventSubscription upserts the parent row and replaces its reminder set, in
// one transaction.
func (h *Handlers) writeEventSubscription(userID uint, eventID, timezone string, fingerprint, startsAt time.Time, planned []plannedReminder, now time.Time) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once Commit has run

	// The parent's lead_minutes, remind_at and reminded_at are a mirror of the
	// first reminder, written for build 15 and read by nothing on the server. The
	// reminder rows below are the source of truth.
	var firstLead int
	var firstRemindAt, firstReminded *time.Time
	if len(planned) > 0 {
		firstLead = planned[0].lead
		firstRemindAt = planned[0].remindAt
		firstReminded = planned[0].remindedAt
	}
	if _, err := tx.Exec(`
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
		userID, eventID, firstLead, timezone, fingerprint, firstRemindAt, startsAt,
		firstReminded, sentIfPast(&startsAt, now),
	); err != nil {
		return err
	}

	// LastInsertId is not reliable through ON DUPLICATE KEY UPDATE unless the
	// update clause reassigns the key, so read the id back instead. Inside the
	// transaction, so it cannot be a row somebody else has since replaced.
	var subID uint64
	if err := tx.QueryRow(
		`SELECT id FROM event_subscriptions WHERE user_id = ? AND event_id = ?`, userID, eventID,
	).Scan(&subID); err != nil {
		return err
	}

	// Drop the reminders that are no longer in the set, keep the rest by upserting
	// them. Deleting all of them and re-inserting would work too, but it would
	// throw away created_at and churn ids for rows that did not change.
	del := `DELETE FROM event_subscription_reminders WHERE subscription_id = ?`
	args := []any{subID}
	if len(planned) > 0 {
		holes := make([]string, len(planned))
		for i, r := range planned {
			holes[i] = "?"
			args = append(args, r.lead)
		}
		del += ` AND lead_minutes NOT IN (` + strings.Join(holes, ",") + `)`
	}
	if _, err := tx.Exec(del, args...); err != nil {
		return err
	}

	for _, r := range planned {
		if _, err := tx.Exec(`
			INSERT INTO event_subscription_reminders
				(subscription_id, lead_minutes, remind_at, reminded_at)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				remind_at   = VALUES(remind_at),
				reminded_at = VALUES(reminded_at)`,
			subID, r.lead, r.remindAt, r.remindedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// APIEventUnsubscribe turns a bell off, reminders and all (the child rows go with
// the parent on the foreign key's ON DELETE CASCADE). Idempotent: a row that was
// already gone is still a 204, so the app does not have to care whether its list
// was stale.
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

// eventRemindAt is the instant one reminder is due, or nil for a zero lead time,
// which means the trainer wants only the start notification.
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
//
// Two sweeps with different grains. The reminder sweep is per REMINDER row, so a
// trainer who asked for a day's warning and a thirty minute one gets both, and
// firing one leaves the other pending. The at-start sweep is per SUBSCRIPTION and
// fires once however many reminders hang off it.
func (h *Handlers) processEventReminders() {
	// Its own mutex, not raidMu. That one is documented as serializing queue
	// matching against raid timer processing, and widening its job would couple
	// two sweeps that have nothing to do with each other.
	h.eventSubMu.Lock()
	defer h.eventSubMu.Unlock()

	feed := h.eventFeedIndex()

	h.dispatchDueEventPushes(feed,
		`SELECT r.id, s.user_id, s.event_id, r.lead_minutes
		 FROM event_subscription_reminders r
		 JOIN event_subscriptions s ON s.id = r.subscription_id
		 WHERE r.remind_at IS NOT NULL AND r.reminded_at IS NULL AND r.remind_at <= UTC_TIMESTAMP()`,
		`UPDATE event_subscription_reminders SET reminded_at = UTC_TIMESTAMP() WHERE id = ? AND reminded_at IS NULL`,
		pushTypeEventReminder)

	h.dispatchDueEventPushes(feed,
		`SELECT id, user_id, event_id, lead_minutes FROM event_subscriptions
		 WHERE started_at_sent IS NULL AND starts_at <= UTC_TIMESTAMP()`,
		`UPDATE event_subscriptions SET started_at_sent = UTC_TIMESTAMP() WHERE id = ? AND started_at_sent IS NULL`,
		pushTypeEventStarting)
}

// dispatchDueEventPushes runs one of the two sweeps. Caller holds eventSubMu.
//
// The id selected and the id marked are the same row, which is a reminder row for
// one sweep and a subscription row for the other; that is the only difference
// between them.
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
		// A zero row count means the row went away or was claimed between the
		// select and here. That is a real case now: a PUT that replaces the set can
		// delete a reminder row this sweep had already picked up.
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			continue
		}

		title := "Pokémon GO event"
		if ev, ok := feed[d.eventID]; ok && ev.Name != "" {
			title = ev.Name
		}
		body := "Starting now"
		if pushType == pushTypeEventReminder {
			// This reminder's own lead time, not the subscription's, so two
			// reminders on one event read "Starts in 1 day" and "Starts in 30
			// minutes" rather than both quoting the same number.
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
// recomputed, every reminder on them individually, because LeekDuck does shift
// events and a reminder pinned at subscribe time goes stale.
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
		SELECT id, event_id, timezone, event_start, starts_at, started_at_sent
		FROM event_subscriptions`)
	if err != nil {
		log.Printf("event subs: reconcile query: %v", err)
		return
	}
	type sub struct {
		id          uint64
		eventID     string
		timezone    string
		eventStart  time.Time
		startsAt    time.Time
		startedSent sql.NullTime
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if rows.Scan(&s.id, &s.eventID, &s.timezone, &s.eventStart, &s.startsAt, &s.startedSent) == nil {
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
		// A start that moved LATER un-sends flags whose new moment is still ahead,
		// so those pushes fire again at the new time. One that moved earlier past
		// the post keeps them: a trainer should not be told an event starts in
		// thirty minutes because the schedule shifted under a reminder that already
		// went out. Applied per reminder, so a fired day-before warning stays fired
		// while an unfired thirty minute one is rescheduled.
		later := startsAt.After(s.startsAt.UTC())
		if err := h.repinReminders(s.id, startsAt, later, now); err != nil {
			log.Printf("event subs: reconcile reminders id=%d: %v", s.id, err)
			continue
		}
		if _, err := h.db.Exec(`
			UPDATE event_subscriptions
			SET event_start = ?, starts_at = ?, started_at_sent = ?
			WHERE id = ?`,
			fingerprint, startsAt, carryFlag(s.startedSent, &startsAt, later, now), s.id,
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

// repinReminders recomputes every reminder on one subscription against a start
// that has moved. Caller holds eventSubMu.
//
// Not a transaction, and deliberately called BEFORE the parent's event_start is
// updated: a failure part way through leaves the parent still pinned to the old
// start, so the next reconcile sees the same mismatch and does the whole thing
// again. Re-running is safe because carryFlag reaches the same answer from a
// half-applied state as from an untouched one.
//
// The parent's legacy mirror columns are refreshed from the head of the set at the
// same time, so build 15 does not read a remind_at pointing at the old start.
func (h *Handlers) repinReminders(subID uint64, startsAt time.Time, movedLater bool, now time.Time) error {
	rows, err := h.db.Query(`
		SELECT id, lead_minutes, reminded_at
		FROM event_subscription_reminders
		WHERE subscription_id = ?
		ORDER BY lead_minutes DESC`, subID)
	if err != nil {
		return err
	}
	type rem struct {
		id       uint64
		lead     int
		reminded sql.NullTime
	}
	var rs []rem
	for rows.Next() {
		var r rem
		if rows.Scan(&r.id, &r.lead, &r.reminded) == nil {
			rs = append(rs, r)
		}
	}
	rows.Close()

	for i, r := range rs {
		remindAt := eventRemindAt(startsAt, r.lead)
		reminded := carryFlag(r.reminded, remindAt, movedLater, now)
		if _, err := h.db.Exec(
			`UPDATE event_subscription_reminders SET remind_at = ?, reminded_at = ? WHERE id = ?`,
			remindAt, reminded, r.id,
		); err != nil {
			return err
		}
		if i == 0 {
			if _, err := h.db.Exec(
				`UPDATE event_subscriptions SET lead_minutes = ?, remind_at = ?, reminded_at = ? WHERE id = ?`,
				r.lead, remindAt, reminded, subID,
			); err != nil {
				return err
			}
		}
	}
	return nil
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
