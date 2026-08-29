package handlers

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// A reminder is a promise about a moment, and every way of getting one wrong is
// silent: the trainer simply does not get told, or gets told at the wrong hour,
// and nothing anywhere logs it. These tests pin the two halves that decide the
// moment (resolving a feed start in the subscriber's zone, and deciding whether a
// due flag survives the feed moving under it) plus the rule that a reminder is
// never fired late.

// The whole reason the app sends an IANA zone with every subscription. A
// floating start is a wall clock reading that means a different instant in every
// zone; a Z start is one instant everywhere. Reading a floating time as UTC is
// the exact bug ParseFeedTime was written to end, and doing it here would put
// every Spotlight Hour reminder hours out for most of the planet.
func TestResolveEventStartHonoursTheSubscribersZone(t *testing.T) {
	const floating = "2026-08-27T18:00:00.000" // 6pm wherever the trainer is
	const absolute = "2026-08-27T18:00:00.000Z"

	cases := []struct {
		zone string
		want string // the floating start, as a UTC instant
	}{
		{"UTC", "2026-08-27T18:00:00Z"},
		{"Europe/London", "2026-08-27T17:00:00Z"}, // BST, UTC+1
		{"America/Denver", "2026-08-28T00:00:00Z"},
		{"Australia/Sydney", "2026-08-27T08:00:00Z"},
		{"Pacific/Kiritimati", "2026-08-27T04:00:00Z"},
	}
	for _, c := range cases {
		loc, ok := loadEventTimezone(c.zone)
		if !ok {
			t.Fatalf("loadEventTimezone(%q) failed; is the zoneinfo database reachable?", c.zone)
		}
		got, ok := resolveEventStart(floating, loc)
		if !ok {
			t.Fatalf("resolveEventStart(%q, %s) not ok", floating, c.zone)
		}
		if got.Format(time.RFC3339) != c.want {
			t.Errorf("floating start in %s = %s, want %s", c.zone, got.Format(time.RFC3339), c.want)
		}

		// A Z start is already an instant. The subscription's zone must not touch
		// it, or every GO Battle League rotation moves per subscriber.
		got, ok = resolveEventStart(absolute, loc)
		if !ok {
			t.Fatalf("resolveEventStart(%q, %s) not ok", absolute, c.zone)
		}
		if got.Format(time.RFC3339) != "2026-08-27T18:00:00Z" {
			t.Errorf("absolute start in %s = %s, want it unmoved", c.zone, got.Format(time.RFC3339))
		}
	}
}

// The fingerprint answers "did upstream move this event", so it has to be the
// same value for every subscriber regardless of where they are standing.
// Comparing resolved instants instead would report a move every time two
// trainers in different zones subscribed to the same Spotlight Hour.
func TestEventStartFingerprintIsZoneIndependent(t *testing.T) {
	got, ok := eventStartFingerprint("2026-08-27T18:00:00.000")
	if !ok {
		t.Fatal("fingerprint not ok")
	}
	if got.Format(time.RFC3339) != "2026-08-27T18:00:00Z" {
		t.Errorf("fingerprint = %s, want the feed reading parsed as UTC", got.Format(time.RFC3339))
	}
	if _, ok := eventStartFingerprint("not a time"); ok {
		t.Error("fingerprint accepted a value that is not a timestamp")
	}
}

// "Local" and "" are the two values time.LoadLocation accepts that mean "some
// zone nobody chose". Letting either through is the silent wrong-hour reminder
// the 400 exists to prevent.
func TestLoadEventTimezoneRefusesTheServersOwnZone(t *testing.T) {
	for _, name := range []string{"", "Local", "Europe/Nowhere", "../etc/passwd"} {
		if _, ok := loadEventTimezone(name); ok {
			t.Errorf("loadEventTimezone(%q) was accepted", name)
		}
	}
	if _, ok := loadEventTimezone("Europe/London"); !ok {
		t.Error("loadEventTimezone rejected a real IANA id")
	}
}

// Zero is a real choice in the picker: "no advance reminder, only tell me when it
// starts". It must produce a null remind_at rather than a reminder due at the
// start instant, which would double up with the start push.
func TestEventRemindAt(t *testing.T) {
	start := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	if got := eventRemindAt(start, 0); got != nil {
		t.Errorf("lead 0 produced a reminder at %v, want none", got)
	}
	for _, lead := range []int{5, 30, 60, 1440, 10080} {
		got := eventRemindAt(start, lead)
		if got == nil {
			t.Fatalf("lead %d produced no reminder", lead)
		}
		if want := start.Add(-time.Duration(lead) * time.Minute); !got.Equal(want) {
			t.Errorf("lead %d = %v, want %v", lead, got, want)
		}
	}
}

// Skip, do not fire late. Subscribing to an event that starts in ten minutes with
// a one hour lead must send nothing at all: the flag goes in already set so the
// sweep never sees the row.
func TestSentIfPastPreArmsAMissedReminder(t *testing.T) {
	now := time.Date(2026, 8, 27, 17, 50, 0, 0, time.UTC)
	start := now.Add(10 * time.Minute)

	past := eventRemindAt(start, 60) // due 50 minutes ago
	if sentIfPast(past, now) == nil {
		t.Error("a reminder whose moment has gone was left unsent, so it would fire immediately")
	}
	// The start push is a different question and still fires normally.
	if sentIfPast(&start, now) != nil {
		t.Error("a start still ten minutes away was marked as already sent")
	}
	if sentIfPast(nil, now) != nil {
		t.Error("a zero lead time produced a flag out of nothing")
	}
}

// The feed does move events, and what happens to an already-sent flag when it
// does is the whole difference between a reminder that reschedules itself and one
// that fires twice or fires hours late.
func TestCarryFlag(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)
	past := now.Add(-2 * time.Hour)
	sent := sql.NullTime{Time: now.Add(-3 * time.Hour), Valid: true}
	unsent := sql.NullTime{}

	cases := []struct {
		name       string
		current    sql.NullTime
		due        *time.Time
		movedLater bool
		wantSet    bool
	}{
		// The event was pushed back, so a reminder that already went out has to be
		// allowed to go out again at the new time.
		{"moved later, already sent, still ahead", sent, &future, true, false},
		// It moved earlier and the new moment has gone. Leave it sent rather than
		// firing right now: nobody wants "starts in 30 minutes" about an event that
		// started twenty minutes ago.
		{"moved earlier, already sent, now past", sent, &past, false, true},
		// Never sent, and the new moment is already gone. Same answer, reached the
		// other way: mark it, so the sweep does not fire it late.
		{"moved earlier, never sent, now past", unsent, &past, false, true},
		// Never sent and still ahead: leave it to the sweeper.
		{"never sent, still ahead", unsent, &future, false, false},
		// A zero lead time has nothing to send at all.
		{"no due instant", sent, nil, true, false},
	}
	for _, c := range cases {
		got := carryFlag(c.current, c.due, c.movedLater, now)
		if (got != nil) != c.wantSet {
			t.Errorf("%s: flag set = %v, want %v", c.name, got != nil, c.wantSet)
		}
	}
}

// The body text a trainer actually reads. Every value the picker offers, so a
// reminder never says "120 minutes" or "1 hours".
func TestLeadPhraseCoversThePicker(t *testing.T) {
	want := map[int]string{
		5:     "5 minutes",
		10:    "10 minutes",
		15:    "15 minutes",
		30:    "30 minutes",
		60:    "1 hour",
		120:   "2 hours",
		180:   "3 hours",
		360:   "6 hours",
		720:   "12 hours",
		1440:  "1 day",
		2880:  "2 days",
		10080: "7 days",
		// Not in the picker, but the server range checks rather than enumerating.
		90: "90 minutes",
		1:  "1 minute",
	}
	for mins, phrase := range want {
		if got := leadPhrase(mins); got != phrase {
			t.Errorf("leadPhrase(%d) = %q, want %q", mins, got, phrase)
		}
	}
}

// The longest lead the picker offers has to be inside the accepted range, or the
// top entry 400s and nothing says why.
func TestEventReminderLeadMaxCoversThePicker(t *testing.T) {
	if eventReminderLeadMax < 10080 {
		t.Errorf("eventReminderLeadMax = %d, below the picker's own maximum of 10080", eventReminderLeadMax)
	}
}
// A trainer can now ask for a day's warning AND a thirty minute one for the same
// event. lead_minutes therefore arrives as either a bare int or an array, and
// both shapes have to survive: build 15 of the app is on testers' phones sending
// the scalar, and a tidy-up that deletes that path breaks every one of those
// installs with no error the trainer can act on.
func TestLeadSetAcceptsBothWireShapes(t *testing.T) {
	var scalar, array leadSet
	if err := json.Unmarshal([]byte(`30`), &scalar); err != nil {
		t.Fatalf("bare int rejected: %v", err)
	}
	if err := json.Unmarshal([]byte(`[30]`), &array); err != nil {
		t.Fatalf("array rejected: %v", err)
	}
	gotScalar, bad := scalar.normalize()
	if bad != "" {
		t.Fatalf("scalar 30 rejected: %s", bad)
	}
	gotArray, bad := array.normalize()
	if bad != "" {
		t.Fatalf("array [30] rejected: %s", bad)
	}
	if !slices.Equal(gotScalar, gotArray) {
		t.Errorf("30 stored as %v but [30] stored as %v; the two must be the same request", gotScalar, gotArray)
	}
	if !slices.Equal(gotScalar, []int{30}) {
		t.Errorf("normalize(30) = %v, want [30]", gotScalar)
	}
}

// Zero is a real choice in the picker, not an absent value, and it has to survive
// the scalar path too.
func TestLeadSetKeepsZero(t *testing.T) {
	var l leadSet
	if err := json.Unmarshal([]byte(`0`), &l); err != nil {
		t.Fatalf("0 rejected: %v", err)
	}
	got, bad := l.normalize()
	if bad != "" {
		t.Fatalf("0 rejected by normalize: %s", bad)
	}
	if !slices.Equal(got, []int{0}) {
		t.Errorf("normalize(0) = %v, want [0]", got)
	}
}

func TestLeadSetNormalize(t *testing.T) {
	cases := []struct {
		name    string
		in      []int
		want    []int
		wantBad bool
	}{
		// Longest lead first, which is also the order they arrive in, so the app
		// can take the head as "the next one".
		{"orders longest lead first", []int{30, 1440}, []int{1440, 30}, false},
		{"de-duplicates", []int{30, 30, 1440}, []int{1440, 30}, false},
		// Clearing every reminder is a DELETE. An empty array here is a malformed
		// write, and treating it as an unsubscribe would turn a client bug into a
		// silently cancelled reminder.
		{"refuses empty", []int{}, nil, true},
		{"refuses six distinct", []int{1, 2, 3, 4, 5, 6}, nil, true},
		// Six entries but one real reminder: correct the client rather than
		// refusing it, since nothing fans out.
		{"de-duplicates before capping", []int{30, 30, 30, 30, 30, 30}, []int{30}, false},
		{"accepts five", []int{5, 10, 15, 30, 60}, []int{60, 30, 15, 10, 5}, false},
		{"refuses negative", []int{-1}, nil, true},
		{"refuses beyond a week", []int{10081}, nil, true},
		{"accepts exactly a week", []int{10080}, []int{10080}, false},
	}
	for _, c := range cases {
		got, bad := leadSet(c.in).normalize()
		if (bad != "") != c.wantBad {
			t.Errorf("%s: rejected=%v (%q), want rejected=%v", c.name, bad != "", bad, c.wantBad)
			continue
		}
		if !c.wantBad && !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The set is resolved per reminder, and a lead time whose moment has already gone
// is stored already marked so the sweep never fires it late. The important part
// is that this is decided PER reminder: one past lead time must not pre-mark the
// others.
func TestPlanRemindersMarksOnlyThePastOnes(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	startsAt := now.Add(2 * time.Hour) // 14:00

	planned := planReminders(startsAt, []int{1440, 30}, now)
	if len(planned) != 2 {
		t.Fatalf("got %d reminders, want 2", len(planned))
	}
	// A day before a start two hours away is well past.
	if planned[0].lead != 1440 || planned[0].remindedAt == nil {
		t.Errorf("the day-before reminder should be stored already marked, got %+v", planned[0])
	}
	// Thirty minutes before is still an hour and a half away.
	if planned[1].lead != 30 || planned[1].remindedAt != nil {
		t.Errorf("the 30 minute reminder should still be pending, got %+v", planned[1])
	}
	if planned[1].remindAt == nil || !planned[1].remindAt.Equal(startsAt.Add(-30*time.Minute)) {
		t.Errorf("30 minute remind_at = %v, want %v", planned[1].remindAt, startsAt.Add(-30*time.Minute))
	}
	// A zero lead time has no advance reminder at all, and therefore no flag.
	zero := planReminders(startsAt, []int{0}, now)
	if zero[0].remindAt != nil || zero[0].remindedAt != nil {
		t.Errorf("a zero lead time produced %+v, want no reminder and no flag", zero[0])
	}
}

// build 15 reads the top-level pair off the response and knows nothing about the
// array. It has to see the reminder that arrives first.
func TestLegacyFieldsMirrorTheFirstReminder(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	startsAt := now.Add(72 * time.Hour)
	s := eventSubscription{Reminders: wireReminders(planReminders(startsAt, []int{1440, 30}, now))}
	s.withLegacyFields()

	if s.LeadMinutes != 1440 {
		t.Errorf("legacy lead_minutes = %d, want the first reminder's 1440", s.LeadMinutes)
	}
	if s.RemindAt == nil || s.Reminders[0].RemindAt == nil || *s.RemindAt != *s.Reminders[0].RemindAt {
		t.Error("legacy remind_at does not match the first reminder's")
	}

	// A subscription with no reminders must still marshal its array as [], never
	// null: the app iterates it.
	empty := eventSubscription{}
	empty.withLegacyFields()
	b, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"reminders":[]`) {
		t.Errorf("empty subscription marshalled as %s, want reminders:[]", b)
	}
}
