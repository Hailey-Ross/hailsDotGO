package handlers

import (
	"database/sql"
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
