package pogodata

import (
	"encoding/json"
	"testing"
)

// Regressions found by adversarially reviewing raidsuppress.go. Every one of these
// was a live defect: a note that emptied a tier on prose saying the opposite, a span
// taken from a neighbouring sentence, or a replacement deleted by the very note that
// promised it. They are kept together so it stays obvious what the parser is under
// pressure to keep getting right.

// TestSuppressionSpanComesFromTheTriggerSentenceOnwards: an unrelated date earlier in
// the same block used to become the suppression's start, which both silenced a week
// the note says nothing about and left the week it does describe running.
func TestSuppressionSpanComesFromTheTriggerSentenceOnwards(t *testing.T) {
	const withEarlierDate = `<p>Adventure Effects run from Monday, August 24, 2026, to Sunday, August 30, 2026. ` +
		`Daily Discoveries, Seasonal Mega Raids, Seasonal Five-Star Raids, and Seasonal Shadow Raids ` +
		`will not take place during the Mega Ascension event. Mega Ascension Raids will take their ` +
		`place starting Monday, August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.</p>`

	notes := parseSuppressionNotes(withEarlierDate, day(t, "2026-08-31"), day(t, "2026-09-04"))
	if len(notes) != 1 {
		t.Fatalf("parsed %d notes, want 1", len(notes))
	}
	if notes[0].rawStart != "2026-08-31T00:01:00.000" || notes[0].rawEnd != "2026-09-06T23:59:00.000" {
		t.Errorf("span %s .. %s, want the note's own dates rather than the sentence before it",
			notes[0].rawStart, notes[0].rawEnd)
	}
}

// TestSuppressionSurvivesAContainingElement: a td, li or blockquote holding the note
// plus a neighbouring paragraph used to swallow that neighbour's dates and produce a
// SECOND, bogus suppression the dedupe could not see, because its span differed.
func TestSuppressionSurvivesAContainingElement(t *testing.T) {
	const nested = `<td><p>Adventure Effects run from Monday, August 24, 2026, to Sunday, August 30, 2026.</p>` +
		`<p>Seasonal Mega Raids and Seasonal Shadow Raids will not take place during the Mega Ascension event. ` +
		`Mega Ascension Raids will take their place starting Monday, August 31, at 12:01 a.m. to ` +
		`Sunday, September 6, 2026, at 11:59 p.m. local time.</p></td>`

	notes := parseSuppressionNotes(nested, day(t, "2026-08-31"), day(t, "2026-09-04"))
	if len(notes) != 1 {
		t.Fatalf("parsed %d notes, want 1 after the dedupe: %+v", len(notes), notes)
	}
	if notes[0].rawStart != "2026-08-31T00:01:00.000" {
		t.Errorf("rawStart %q, want the note's own start", notes[0].rawStart)
	}
}

// TestSuppressionNeedsARealSentenceBoundary: the cached pages are minified, so
// "</p><p>" carries no whitespace and goquery's Text() used to fuse two paragraphs
// into one sentence. That let a category in one paragraph pair with a trigger in the
// next, which is exactly what the same-sentence rule exists to prevent.
func TestSuppressionNeedsARealSentenceBoundary(t *testing.T) {
	const minified = `<ul><li><p>Seasonal Mega Raids will continue as normal.</p><p>Daily Adventure Incense ` +
		`will not take place starting Monday, August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.</p></li></ul>`

	if got := parseSuppressionNotes(minified, day(t, "2026-08-31"), day(t, "2026-09-04")); len(got) != 0 {
		t.Errorf("emptied %v on prose that says the Mega tier continues as normal", got[0].groups)
	}
}

// TestFindNoteDatesRefusesABareMonthDay: "may" is also an ordinary English verb, and
// a date this reader believes is a date it will suspend three tiers on.
func TestFindNoteDatesRefusesABareMonthDay(t *testing.T) {
	for _, prose := range []string{"Trainers may 2 times over.", "August 31", "September 6"} {
		if got := findNoteDates(prose); len(got) != 0 {
			t.Errorf("%q read as %d date(s); a date needs a weekday, a year or a clock", prose, len(got))
		}
	}
}

// TestSuppressionReplacementVetoSurvivesAnOverlappingNote is the two note case. Both
// live pages carry identical wording today, but the moment the Mega Finale page
// states its own weekend instead, Armored Mewtwo opens BEFORE that note while being
// the raid it is promising. Being a replacement has to veto, not vote.
func TestSuppressionReplacementVetoSurvivesAnOverlappingNote(t *testing.T) {
	now := utc(t, "2026-09-05T18:00:00Z")
	wide := liveSuppression(t) // 08-31 to 09-06, the Mega Ascension wording
	narrow := wide
	narrow.EventID = "pokemon-go-fest-2026-mega-finale"
	narrow.RawStart = "2026-09-05T00:01:00.000"
	start, end, ok := suppressionSpan(narrow.RawStart, narrow.RawEnd)
	if !ok {
		t.Fatal("the narrower note's span would not resolve")
	}
	narrow.StartsUTC, narrow.EndsUTC = start, end

	armored := suppressWindow(t, "armored-mewtwo-in-5-star-raid-battles-september-2026",
		"5", false, false, "2026-09-05T10:00:00.000", "2026-09-06T18:00:00.000", "Armored Mewtwo")
	if !armored.Active(now) {
		t.Fatal("test window is not active when it should be")
	}
	if silencedBy([]RaidSuppression{wide, narrow}, armored, now) {
		t.Error("an overlapping note deleted the replacement the other note promised")
	}
	// And the seasonal rotation both notes really mean is still silenced by the pair.
	regis := suppressWindow(t, "regis", "5", false, false, "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000", "Regirock")
	if !silencedBy([]RaidSuppression{wide, narrow}, regis, now) {
		t.Error("the seasonal rotation survived two notes that both name its group")
	}
}

// TestSuppressionExemptsARotationOpeningOnTheNotesOwnDay is the 26 hour boundary.
//
// A rotation's StartsUTC is the UTC+14 reading of its wall clock and a note's is the
// UTC-12 reading of its own, so comparing those two fields directly shifted the
// replacement test a full day late. The note says "starting Monday, August 31", so a
// replacement announced for that very day is the most ordinary shape there is.
func TestSuppressionExemptsARotationOpeningOnTheNotesOwnDay(t *testing.T) {
	sup := liveSuppression(t) // states 2026-08-31T00:01
	now := utc(t, "2026-09-01T12:00:00Z")
	cases := []struct {
		rawStart string
		silenced bool
	}{
		{"2026-08-26T06:00:00.000", true},  // a genuine seasonal rotation, well before
		{"2026-08-30T22:00:00.000", true},  // the day before the note opens
		{"2026-08-31T00:01:00.000", false}, // exactly the note's own start
		{"2026-08-31T06:00:00.000", false}, // later the same day
		{"2026-09-01T00:01:00.000", false}, // the next day
	}
	for _, c := range cases {
		t.Run(c.rawStart, func(t *testing.T) {
			w := suppressWindow(t, "w", "5", false, false, c.rawStart, "2026-09-08T22:00:00.000", "Regirock")
			if !w.Active(now) {
				t.Fatal("test window is not active")
			}
			if got := silencedBy([]RaidSuppression{sup}, w, now); got != c.silenced {
				t.Errorf("silenced = %v, want %v", got, c.silenced)
			}
		})
	}
}

// TestSuppressionDisarmAlsoClearsTheUpcomingStrip: the circuit breaker has to clear
// BOTH the group set the drop rule reads and the slice buildUpcoming reads. Clearing
// only the first leaves a tier missing from the grid and from the strip, which is
// worse than the behaviour the disarm exists to restore. No existing test could see
// this: deleting the line left the whole suite green.
func TestSuppressionDisarmAlsoClearsTheUpcomingStrip(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	// A live rotation whose boss cannot be carded, so the strip is its only route to
	// the page, and no additive window anywhere: the disarm case.
	unknown := suppressWindow(t, "mega-unknown", "6", false, false,
		"2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000", "Mega Nothingatall")
	_, upcoming, stats := reconcileRaids(json.RawMessage(`{"6":[]}`), []RaidWindow{unknown},
		[]RaidSuppression{liveSuppression(t)}, now, suppressLookup(t), testCPMs(t))

	if !stats.SuppressionDisarmed {
		t.Fatal("the breaker did not fire on a suppression that empties everything")
	}
	if !advertises(upcoming, "mega-unknown") {
		t.Errorf("disarmed, but the strip is still silenced: %+v", upcoming)
	}
}
