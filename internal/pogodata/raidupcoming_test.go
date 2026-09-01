package pogodata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The up next strip, the window ladder that labels a card, and the two readers
// that decide which day a scraped roster belongs to.
//
// The defect these were written for: buildUpcoming published the soonest not yet
// started rotation PER TIER and threw the rest away. On 2026-09-01 the served list
// held three entries while the scrapers held sixteen dated rotations running out
// to 6 October, and Mega Ascension's own page named a different Mega for every day
// of the week with only Thursday's reaching the page. Nothing looked broken from
// the outside, because a rotation that was parsed and then dropped here is
// indistinguishable from one that was never scraped at all.

// ── Range headings ───────────────────────────────────────────────────────────

// TestResolveHeadingDaysClosesARangeOutsideTheEventSpan is the Mega Squads shape,
// verbatim: its page heads two rosters "September 8-15" while its feed entry ends
// on the 14th at 20:00. resolveHeadingDay refuses any day outside the event's own
// span, so the 15th resolved to nothing, the range collapsed to its first day, and
// seven days of Mega Beedrill were thrown away.
//
// If this fails, a range headed roster is back to being a one day window.
func TestResolveHeadingDaysClosesARangeOutsideTheEventSpan(t *testing.T) {
	// The feed's own span for mega-squads. Note the end is INSIDE the range both
	// headings name, which is the whole difficulty.
	evStart, _, ok := ParseFeedTime("2026-09-08T10:00:00.000", time.UTC)
	if !ok {
		t.Fatal("fixture start will not parse")
	}
	evEnd, _, ok := ParseFeedTime("2026-09-14T20:00:00.000", time.UTC)
	if !ok {
		t.Fatal("fixture end will not parse")
	}

	cases := []struct {
		heading    string
		start, end string
	}{
		{"September 8-15", "2026-09-08", "2026-09-15"},
		{"September 11-15", "2026-09-11", "2026-09-15"},
	}
	for _, c := range cases {
		t.Run(c.heading, func(t *testing.T) {
			start, end, ok := resolveHeadingDays(c.heading, evStart, evEnd)
			if !ok {
				t.Fatalf("refused %q", c.heading)
			}
			got, gotEnd := start.Format("2006-01-02"), end.Format("2006-01-02")
			if got != c.start || gotEnd != c.end {
				t.Errorf("got %s .. %s, want %s .. %s", got, gotEnd, c.start, c.end)
			}
			if gotEnd == got {
				t.Errorf("the range collapsed onto its first day again, which is the whole defect")
			}
		})
	}

	// The end really is outside the span, so this is not passing by accident.
	if _, ok := resolveHeadingDay("September 15", evStart, evEnd); ok {
		t.Error("the 15th resolves inside the event span, so these cases no longer exercise the fix")
	}
}

// TestResolveHeadingDaysAwkwardRanges pins the shapes that must NOT become a
// rotation with an invented span. The end of a range no longer needs the event's
// own dates, so the only things stopping a misread heading from producing a window
// that never expires are the year roll, the real date check and the 60 day cap.
func TestResolveHeadingDaysAwkwardRanges(t *testing.T) {
	cases := []struct {
		name       string
		heading    string
		evStart    string
		evEnd      string
		start, end string
	}{
		{
			// The year rolls once, so a range crossing New Year closes on the
			// January rather than refusing.
			name: "year rollover", heading: "December 28-January 3",
			evStart: "2026-12-28", evEnd: "2026-12-31",
			start: "2026-12-28", end: "2027-01-03",
		},
		{
			// February 30 is not a date in any year, so the range will not close
			// and the heading falls back to its first day, which is never worse
			// than nothing.
			name: "a date that does not exist", heading: "September 8-February 30",
			evStart: "2026-09-08", evEnd: "2026-09-14",
			start: "2026-09-08", end: "2026-09-08",
		},
		{
			// Backwards. The stated end is before the stated start, and the roll
			// forward lands a year away, past the cap.
			name: "backwards", heading: "September 15-8",
			evStart: "2026-09-08", evEnd: "2026-09-20",
			start: "2026-09-15", end: "2026-09-15",
		},
		{
			// Three months is a misread, not a raid roster, so the cap refuses it
			// and the heading is one day again.
			name: "past the 60 day cap", heading: "September 8 to December 8",
			evStart: "2026-09-08", evEnd: "2026-09-14",
			start: "2026-09-08", end: "2026-09-08",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end, ok := resolveHeadingDays(c.heading, day(t, c.evStart), day(t, c.evEnd))
			if !ok {
				t.Fatalf("refused %q entirely; a heading that names a real first day still has to resolve", c.heading)
			}
			got, gotEnd := start.Format("2006-01-02"), end.Format("2006-01-02")
			if got != c.start || gotEnd != c.end {
				t.Errorf("got %s .. %s, want %s .. %s", got, gotEnd, c.start, c.end)
			}
			if end.Before(start) {
				t.Errorf("resolved to a backwards span, which raidWindowSpan then refuses outright")
			}
		})
	}
}

// TestResolveHeadingDaysReadsAWeekdayQualifiedRange: upstream writes weekday
// qualified dates as a matter of course, mega-ascension heads all five of its day
// rosters that way, and a weekday before the SECOND date used to fail the range
// pattern outright. The heading then fell through to the single day reader, resolved
// as its first day, and scoped the second day's roster to the first. Found in review.
func TestResolveHeadingDaysReadsAWeekdayQualifiedRange(t *testing.T) {
	evStart, evEnd := day(t, "2026-09-05"), day(t, "2026-09-06")
	for _, heading := range []string{
		"Saturday, September 5 - Sunday, September 6",
		"Saturday, September 5 – Sunday, September 6",
		"Saturday, September 5 to Sunday, September 6",
		"Saturday, September 5 - September 6",
		"September 5 - September 6",
	} {
		start, end, ok := resolveHeadingDays(heading, evStart, evEnd)
		if !ok {
			t.Errorf("%q was refused", heading)
			continue
		}
		if start.Format("2006-01-02") != "2026-09-05" || end.Format("2006-01-02") != "2026-09-06" {
			t.Errorf("%q gave %s .. %s, want the whole weekend",
				heading, start.Format("2006-01-02"), end.Format("2006-01-02"))
		}
	}

	// And the weekday is still CHECKED on both sides, which is the other half of the
	// fix: the range path used to discard it, so the one defence against a wrong year
	// was absent on exactly the path that reaches furthest forward.
	if _, _, ok := resolveHeadingDays("Monday, September 5 - Sunday, September 6", evStart, evEnd); ok {
		t.Error("accepted a range calling 5 September a Monday, but it is a Saturday")
	}
}

// TestResolveRangeEndCapAndRoll takes the closer on its own, because the cap is the
// only thing standing between a mangled heading and a rotation that never expires,
// and a boundary is exactly where a cap stops being enforced.
func TestResolveRangeEndCapAndRoll(t *testing.T) {
	start := day(t, "2026-09-08")
	cases := []struct {
		name    string
		weekday string
		month   string
		dom     int
		want    string // "" means it must be refused
	}{
		{"the same month", "", "september", 15, "2026-09-15"},
		{"the next month", "", "october", 6, "2026-10-06"},
		{"exactly the cap, 60 days out", "", "november", 7, "2026-11-07"},
		{"one day past the cap", "", "november", 8, ""},
		{"rolls the year once", "", "january", 3, ""}, // more than 60 days away either way
		{"a day that does not exist", "", "february", 30, ""},
		{"a day before the start", "", "september", 1, ""},
		{"a month nobody writes", "", "smarch", 3, ""},
		{"a day out of range", "", "september", 44, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveRangeEnd(start, c.weekday, c.month, c.dom)
			if c.want == "" {
				if ok {
					t.Fatalf("closed on %s, want a refusal", got.Format("2006-01-02"))
				}
				return
			}
			if !ok {
				t.Fatalf("refused, want %s", c.want)
			}
			if got.Format("2006-01-02") != c.want {
				t.Errorf("closed on %s, want %s", got.Format("2006-01-02"), c.want)
			}
		})
	}

	// The weekday, when the heading states one, is checked against the calendar the
	// same way the single day reader checks it. 15 September 2026 is a Tuesday.
	if _, ok := resolveRangeEnd(start, "tuesday", "september", 15); !ok {
		t.Error("a range end whose weekday agrees with the calendar was refused")
	}
	if got, ok := resolveRangeEnd(start, "monday", "september", 15); ok {
		t.Errorf("closed on %s for a heading that calls it a Monday, but it is a Tuesday",
			got.Format("2006-01-02"))
	}

	// The roll itself, from a start late enough in the year that January is inside
	// the cap. Without it "December 28 to January 3" is a refusal.
	if got, ok := resolveRangeEnd(day(t, "2026-12-28"), "", "january", 3); !ok {
		t.Error("a range crossing New Year was refused, so every year end roster loses its span")
	} else if got.Format("2006-01-02") != "2027-01-03" {
		t.Errorf("closed on %s, want 2027-01-03", got.Format("2006-01-02"))
	}
}

// ── preferRaidWindow ─────────────────────────────────────────────────────────

// preferWindow builds one window from the stated wall clock strings alone, which
// is the reading the ladder compares on. The UTC fields come from raidWindowSpan
// so a test window is never wider or narrower than a real one.
func preferWindow(t *testing.T, id, rawStart, rawEnd string, additive bool) RaidWindow {
	t.Helper()
	return suppressWindow(t, id, "6", false, additive, rawStart, rawEnd, "Mega Raichu X")
}

// TestPreferRaidWindowLadder walks all four rules, and asserts each answer from
// BOTH argument orders. activeBosses folds a boss by whichever window it happens
// to meet first, so a ladder that answers differently depending on which source
// the rebuild appended first would put a different date on the card every restart.
func TestPreferRaidWindowLadder(t *testing.T) {
	cases := []struct {
		name          string
		at            string
		winner, loser RaidWindow
		why           string
	}{
		{
			name:   "rule 1, the same last day, so the feed takes it",
			at:     "2026-09-10T12:00:00Z",
			winner: preferWindow(t, "mega-beedrill-in-mega-raids-september-2026", "2026-09-08T06:00:00.000", "2026-09-15T22:00:00.000", false),
			loser:  preferWindow(t, "mega-squads", "2026-09-08T00:01:00.000", "2026-09-15T23:59:00.000", true),
			why:    "the feed entry names the rotation itself rather than the event it sits inside",
		},
		{
			name:   "rule 1, the same last day and the same kind of source, so the wider span wins",
			at:     "2026-09-12T12:00:00Z",
			winner: preferWindow(t, "wider", "2026-09-08T06:00:00.000", "2026-09-15T22:00:00.000", false),
			loser:  preferWindow(t, "narrower", "2026-09-11T06:00:00.000", "2026-09-15T22:00:00.000", false),
			why:    "the same last day is a tie on the only thing the pill says, so the longer rotation is the more useful label",
		},
		{
			name:   "rule 2, one span contains the other",
			at:     "2026-10-01T12:00:00Z",
			winner: preferWindow(t, "mega-victreebel-in-mega-raids-september-2026", "2026-09-30T06:00:00.000", "2026-10-06T22:00:00.000", false),
			loser:  preferWindow(t, "one-day-inside-it", "2026-10-01T00:01:00.000", "2026-10-01T23:59:00.000", true),
			why:    "a Mega an event lists for one day is not gone the next, so the pill should say the longer of the two",
		},
		{
			name:   "rule 3, two spans that do not touch, so the one in force wins",
			at:     "2026-09-04T12:00:00Z",
			winner: preferWindow(t, "mega-ascension", "2026-09-04T00:01:00.000", "2026-09-04T23:59:00.000", true),
			loser:  preferWindow(t, "pokemon-go-fest-2026-mega-finale", "2026-09-05T00:01:00.000", "2026-09-05T23:59:00.000", true),
			why:    "this is the fix: under a plain ends last rule the 26 hour overlap handed Friday's card to Saturday",
		},
		{
			name:   "rule 4, overlapping without containment, so ending last wins",
			at:     "2026-09-12T12:00:00Z",
			winner: preferWindow(t, "later-ending", "2026-09-10T06:00:00.000", "2026-09-20T22:00:00.000", false),
			loser:  preferWindow(t, "earlier-ending", "2026-09-08T06:00:00.000", "2026-09-15T22:00:00.000", false),
			why:    "neither contains the other, which is what this rule did for every case before the ladder existed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := utc(t, c.at)
			// Both have to be live at the instant, or the comparison is not the one
			// activeBosses ever makes and the case proves nothing.
			if !c.winner.Active(now) || !c.loser.Active(now) {
				t.Fatalf("at %s: winner active %v, loser active %v", c.at, c.winner.Active(now), c.loser.Active(now))
			}
			if !preferRaidWindow(c.winner, c.loser, now) {
				t.Errorf("%s did not displace %s: %s", c.winner.EventID, c.loser.EventID, c.why)
			}
			if preferRaidWindow(c.loser, c.winner, now) {
				t.Errorf("%s displaced %s from the other argument order, so the answer depends on append order: %s",
					c.loser.EventID, c.winner.EventID, c.why)
			}
		})
	}
}

// TestPreferRaidWindowFallsBackToEndsUTC covers a window built with no feed strings
// at all, which is what several older tests construct and what a source with no
// stated wall clock would produce. The ladder cannot read a calendar day off those,
// so it compares EndsUTC, which is the whole of what this used to do.
func TestPreferRaidWindowFallsBackToEndsUTC(t *testing.T) {
	base := utc(t, "2026-09-04T12:00:00Z")
	bare := func(id string, ends time.Duration, additive bool) RaidWindow {
		return RaidWindow{EventID: id, Tier: "6", Additive: additive,
			StartsUTC: base.Add(-24 * time.Hour), EndsUTC: base.Add(ends)}
	}

	later, sooner := bare("later", 48*time.Hour, false), bare("sooner", 6*time.Hour, false)
	if !preferRaidWindow(later, sooner, base) {
		t.Error("the later ending window did not win with no feed strings to read")
	}
	if preferRaidWindow(sooner, later, base) {
		t.Error("the sooner ending window won from the other argument order")
	}

	// Ends at the same instant, so the feed still takes it off the event page.
	feed, page := bare("feed", 24*time.Hour, false), bare("page", 24*time.Hour, true)
	if !preferRaidWindow(feed, page, base) {
		t.Error("an exact tie on EndsUTC did not go to the feed window")
	}
	if preferRaidWindow(page, feed, base) {
		t.Error("the additive window displaced the feed window on an exact tie")
	}

	// Half a window is still not a day range: one raw string is not enough, or the
	// comparison would be reading a day off one side and nothing off the other.
	half := bare("half", 48*time.Hour, false)
	half.RawStart = "2026-09-04T00:01:00.000"
	if !preferRaidWindow(half, sooner, base) {
		t.Error("a window with only one raw string stopped falling back to EndsUTC")
	}
}

// TestPreferRaidWindowIsATotalOrder is the property the four named rules exist to
// have, and the one a case by case test cannot see.
//
// An earlier version of this comparator was a ladder of pairwise rules (contains,
// disjoint, ends last) that each optimised a different quantity. It was antisymmetric,
// so every pairwise test passed, and it was NOT transitive: an adversarial sweep found
// 5544 triples of day ranges that can be live at once where the winner of the fold in
// activeBosses depended on the order the windows happened to be appended in. One of
// the three possible answers was the exact "labelled with tomorrow's window" bug the
// pass exists to fix, so upstream reordering its own events array could have
// reintroduced it with no schedule change at all.
//
// A comparator that can cycle cannot be tested, because the answer is a property of
// the slice rather than of the windows. So this sweeps every day range pair and
// triple that is genuinely live together, at every hour across ten days, and asserts
// there is no mutual preference and no cycle.
func TestPreferRaidWindowIsATotalOrder(t *testing.T) {
	var all []RaidWindow
	n := 0
	for start := 1; start <= 8; start++ {
		for end := start; end <= 8; end++ {
			for _, additive := range []bool{false, true} {
				all = append(all, preferWindow(t,
					fmt.Sprintf("e%03d", n),
					fmt.Sprintf("2026-09-%02dT00:01:00.000", start),
					fmt.Sprintf("2026-09-%02dT23:59:00.000", end), additive))
				n++
			}
		}
	}

	triples, mutual, cycles := 0, 0, 0
	for h := 0; h < 24*10; h += 2 {
		now := utc(t, "2026-08-31T00:00:00Z").Add(time.Duration(h) * time.Hour)
		var live []RaidWindow
		for _, w := range all {
			if w.Active(now) {
				live = append(live, w)
			}
		}
		for i := 0; i < len(live); i++ {
			for j := i + 1; j < len(live); j++ {
				if preferRaidWindow(live[i], live[j], now) && preferRaidWindow(live[j], live[i], now) {
					mutual++
				}
				for k := j + 1; k < len(live); k++ {
					triples++
					a, b, c := live[i], live[j], live[k]
					if preferRaidWindow(a, b, now) && preferRaidWindow(b, c, now) && !preferRaidWindow(a, c, now) {
						cycles++
						if cycles <= 3 {
							t.Errorf("not transitive at %s: %s..%s beats %s..%s beats %s..%s, but not the first over the last",
								now.Format(time.RFC3339),
								a.RawStart[:10], a.RawEnd[:10], b.RawStart[:10], b.RawEnd[:10], c.RawStart[:10], c.RawEnd[:10])
						}
					}
				}
			}
		}
	}
	if triples < 100000 {
		t.Fatalf("only %d triples were live together, so this swept almost nothing", triples)
	}
	if mutual > 0 {
		t.Errorf("%d pairs preferred each other, so the fold keeps whichever it saw last", mutual)
	}
	t.Logf("checked %d ordered triples: %d cycles", triples, cycles)
}

// TestPreferRaidWindowPicksTheDayInForce is the measured case, driven all the way
// through reconcileRaids so the assertion is on the SERVED card rather than on the
// helper.
//
// On Friday 4 September, at noon UTC, Mega Raichu X is named by the Mega Ascension
// Friday roster and by the GO Fest SATURDAY habitat list. raidWindowSpan reads a
// start in UTC+14 and an end in UTC-12, so both windows are Active for 26 hours,
// and "ends last" handed the card to tomorrow: the live grid said Mega Raichu X ran
// on the 5th while Mega Raichu Y, which is on the SUNDAY list instead, kept its
// Friday window. Two bosses off one roster, dated a day apart.
func TestPreferRaidWindowPicksTheDayInForce(t *testing.T) {
	now := utc(t, "2026-09-04T12:00:00Z")
	ascension := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-04T00:01:00.000", "2026-09-04T23:59:00.000", "Mega Raichu X", "Mega Raichu Y")
	gofest := suppressWindow(t, "pokemon-go-fest-2026-mega-finale", "6", false, true,
		"2026-09-05T00:01:00.000", "2026-09-05T23:59:00.000", "Mega Raichu X")

	// Both really are live at this instant, or the fold never happens and this test
	// proves nothing.
	if !ascension.Active(now) || !gofest.Active(now) {
		t.Fatalf("the 26 hour overlap is gone: ascension active %v, go fest active %v",
			ascension.Active(now), gofest.Active(now))
	}

	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Raichu X"},{"pokemon_name":"Mega Raichu Y"}]}`)
	for _, windows := range [][]RaidWindow{{ascension, gofest}, {gofest, ascension}} {
		served, _, _ := reconcileRaids(upstream, windows, nil, now, megaOnlyLookup, defaultRaidCPMs)
		cards := servedTier(t, served, "6")
		if len(cards) != 2 {
			t.Fatalf("tier 6 has %d cards, want both Raichu forms: %v", len(cards), servedNames(t, served, "6"))
		}
		for _, b := range cards {
			if b.EventID != "mega-ascension" {
				t.Errorf("%s labelled %q, want mega-ascension: it is running today, the GO Fest window opens tomorrow",
					b.PokemonName, b.EventID)
			}
			if b.StartsAt != "2026-09-04T00:01:00.000" || b.EndsAt != "2026-09-04T23:59:00.000" {
				t.Errorf("%s dated %q to %q, want Friday the 4th", b.PokemonName, b.StartsAt, b.EndsAt)
			}
		}
	}
}

// ── buildUpcoming ────────────────────────────────────────────────────────────

// upcomingNow is the instant the published schedule was measured at.
const upcomingNow = "2026-09-01T21:00:00Z"

// upcomingSchedule is the shape of the live schedule on 2026-09-01, trimmed to the
// rotations that make the fold do something: two Mega Ascension days, the GO Fest
// Saturday habitat list, the tier 5 rotation that proves this is not one entry per
// tier, the Mega Beedrill rotation and the Mega Squads window that also names it,
// and the Mega Victreebel rotation at the end of the month.
func upcomingSchedule(t *testing.T) []RaidWindow {
	t.Helper()
	return []RaidWindow{
		suppressWindow(t, "mega-ascension", "6", false, true,
			"2026-09-03T00:01:00.000", "2026-09-03T23:59:00.000", "Mega Starmie"),
		suppressWindow(t, "mega-ascension", "6", false, true,
			"2026-09-04T00:01:00.000", "2026-09-04T23:59:00.000", "Mega Raichu X", "Mega Raichu Y"),
		suppressWindow(t, "pokemon-go-fest-2026-mega-finale", "6", false, true,
			"2026-09-05T00:01:00.000", "2026-09-05T23:59:00.000", "Mega Victreebel", "Mega Beedrill", "Mega Starmie"),
		suppressWindow(t, "armored-mewtwo-in-5-star-raid-battles-september-2026", "5", false, false,
			"2026-09-05T10:00:00.000", "2026-09-06T18:00:00.000", "Armored Mewtwo"),
		suppressWindow(t, "mega-beedrill-in-mega-raids-september-2026", "6", false, false,
			"2026-09-08T06:00:00.000", "2026-09-15T22:00:00.000", "Mega Beedrill"),
		suppressWindow(t, "mega-squads", "6", false, true,
			"2026-09-08T00:01:00.000", "2026-09-15T23:59:00.000", "Mega Beedrill"),
		suppressWindow(t, "mega-victreebel-in-mega-raids-september-2026", "6", false, false,
			"2026-09-30T06:00:00.000", "2026-10-06T22:00:00.000", "Mega Victreebel"),
	}
}

// upcomingIndex maps "event id|start day" to the entry, which is the pair the fold
// key is built on and so the pair a failure needs to name.
func upcomingIndex(list []UpcomingRaid) map[string]UpcomingRaid {
	out := make(map[string]UpcomingRaid, len(list))
	for _, u := range list {
		key := u.EventID
		if len(u.StartsAt) >= 10 {
			key += "|" + u.StartsAt[:10]
		}
		out[key] = u
	}
	return out
}

func upcomingBossNames(u UpcomingRaid) []string {
	out := make([]string, 0, len(u.Bosses))
	for _, b := range u.Bosses {
		out = append(out, b.Name)
	}
	return out
}

// TestBuildUpcomingPublishesEveryFutureWindow is the defect itself. One entry per
// tier meant the site advertised Mega Beedrill on the 8th while the scrapers held a
// different Mega for every day in between, and nothing anywhere said so.
func TestBuildUpcomingPublishesEveryFutureWindow(t *testing.T) {
	now := utc(t, upcomingNow)
	got := buildUpcoming(upcomingSchedule(t), nil, map[string]bool{}, now, nil)

	// Six of the seven windows survive: mega-squads loses its only boss, which is
	// the subject of the test below.
	if len(got) != 6 {
		t.Fatalf("published %d rotations, want 6: %v", len(got), upcomingIndex(got))
	}
	byKey := upcomingIndex(got)
	for _, want := range []string{
		"mega-ascension|2026-09-03",
		"mega-ascension|2026-09-04",
		"pokemon-go-fest-2026-mega-finale|2026-09-05",
		"armored-mewtwo-in-5-star-raid-battles-september-2026|2026-09-05",
		"mega-beedrill-in-mega-raids-september-2026|2026-09-08",
		"mega-victreebel-in-mega-raids-september-2026|2026-09-30",
	} {
		if _, ok := byKey[want]; !ok {
			t.Errorf("%s was not published: %v", want, byKey)
		}
	}

	// Five of the six are tier 6. One per tier is exactly what this was.
	tier6 := 0
	for _, u := range got {
		if u.Tier == "6" {
			tier6++
		}
	}
	if tier6 != 5 {
		t.Errorf("published %d tier 6 rotations, want 5; a fold that collapses to one per tier looks identical to a scraper that has stopped finding anything", tier6)
	}

	// Soonest first, because the strip is read top down.
	for i := 1; i < len(got); i++ {
		if got[i-1].StartsAt > got[i].StartsAt {
			t.Fatalf("the strip is not in date order: %s then %s", got[i-1].StartsAt, got[i].StartsAt)
		}
	}
}

// TestBuildUpcomingFoldsOneBossOnOneDay: an event page and a raid-battles entry
// covering the same week is the normal case, not an exotic one, and publishing both
// puts the same boss on the strip twice on the same day. The feed wins, because its
// hours are the real ones and its event id opens the rotation's own modal, and the
// window left with no bosses drops out rather than becoming an empty card.
func TestBuildUpcomingFoldsOneBossOnOneDay(t *testing.T) {
	now := utc(t, upcomingNow)
	got := buildUpcoming(upcomingSchedule(t), nil, map[string]bool{}, now, nil)
	byKey := upcomingIndex(got)

	beedrill, ok := byKey["mega-beedrill-in-mega-raids-september-2026|2026-09-08"]
	if !ok {
		t.Fatalf("the Mega Beedrill rotation was not published: %v", byKey)
	}
	// The feed's real 06:00 to 22:00, not the event page's whole day approximation.
	if beedrill.StartsAt != "2026-09-08T06:00:00.000" || beedrill.EndsAt != "2026-09-15T22:00:00.000" {
		t.Errorf("Mega Beedrill runs %q to %q, want the feed's own hours", beedrill.StartsAt, beedrill.EndsAt)
	}
	if _, ok := byKey["mega-squads|2026-09-08"]; ok {
		t.Errorf("the Mega Squads window survived with no bosses left to it: %v", byKey)
	}

	// Nothing is published twice on one day in one group.
	seen := map[string]bool{}
	for _, u := range got {
		for _, b := range u.Bosses {
			key := raidGroupKey(u.Tier, u.Shadow) + "|" + bossKey(b.Name, u.Shadow) + "|" + u.StartsAt[:10]
			if seen[key] {
				t.Errorf("%s is on the strip twice for %s", b.Name, u.StartsAt[:10])
			}
			seen[key] = true
		}
	}
}

// TestBuildUpcomingKeepsABossOnTwoDifferentDays is the other half of the fold, and
// the reason its key carries the day. The same boss on two DIFFERENT days is two
// real appearances: Mega Victreebel is on the GO Fest Saturday habitat list for the
// 5th and is the Mega rotation from the 30th, and folding on the boss alone would
// silently delete one of them.
func TestBuildUpcomingKeepsABossOnTwoDifferentDays(t *testing.T) {
	now := utc(t, upcomingNow)
	got := buildUpcoming(upcomingSchedule(t), nil, map[string]bool{}, now, nil)

	days := map[string][]string{}
	for _, u := range got {
		for _, b := range u.Bosses {
			days[b.Name] = append(days[b.Name], u.StartsAt[:10])
		}
	}
	for name, want := range map[string][]string{
		"Mega Victreebel": {"2026-09-05", "2026-09-30"},
		"Mega Starmie":    {"2026-09-03", "2026-09-05"},
		"Mega Beedrill":   {"2026-09-05", "2026-09-08"},
	} {
		got := days[name]
		if len(got) != len(want) {
			t.Errorf("%s appears on %v, want %v", name, got, want)
			continue
		}
		for _, d := range want {
			found := false
			for _, g := range got {
				if g == d {
					found = true
				}
			}
			if !found {
				t.Errorf("%s is missing its %s appearance: %v", name, d, got)
			}
		}
	}
}

// TestBuildUpcomingLivePlaceholderAndSilencedWindow covers the other branch: a
// rotation that is already open but has no card on the grid still gets named, so
// the tier does not read as empty, and a rotation an event page says is not running
// does not, or the strip would advertise the very bosses a note has just removed.
func TestBuildUpcomingLivePlaceholderAndSilencedWindow(t *testing.T) {
	now := utc(t, upcomingNow)
	// Both of these opened on 26 August and run to the 8th, which is what the note
	// in liveSuppression suspends.
	const seasonStart, seasonEnd = "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000"
	gyarados := suppressWindow(t, "mega-gyarados-in-mega-raids-august-2026", "6", false, false, seasonStart, seasonEnd, "Mega Gyarados")
	regis := suppressWindow(t, "regirock-regice-registeel-in-5-star-raid-battles-august-2026", "5", false, false, seasonStart, seasonEnd, "Regirock", "Regice", "Registeel")
	windows := append(upcomingSchedule(t), gyarados, regis)

	t.Run("live but not carded is named", func(t *testing.T) {
		got := buildUpcoming(windows, nil, map[string]bool{}, now, nil)
		var live []UpcomingRaid
		for _, u := range got {
			if u.Live {
				live = append(live, u)
			}
		}
		if len(live) != 2 {
			t.Fatalf("published %d live placeholders, want one per governed group with nothing on the grid: %v", len(live), upcomingIndex(got))
		}
		for _, u := range live {
			if len(u.Bosses) == 0 {
				t.Errorf("%s is a placeholder naming nothing at all", u.EventID)
			}
		}
	})

	t.Run("a window whose bosses are all on the grid needs no placeholder", func(t *testing.T) {
		carded := map[string]bool{
			bossKey("Mega Gyarados", false): true,
			bossKey("Regirock", false):      true,
			bossKey("Regice", false):        true,
			bossKey("Registeel", false):     true,
		}
		for _, u := range buildUpcoming(windows, nil, carded, now, nil) {
			if u.Live {
				t.Errorf("%s was advertised as live while every one of its bosses is on the grid: %v",
					u.EventID, upcomingBossNames(u))
			}
		}
	})

	// The correction to the above, found in review. Skipping the whole window as
	// soon as ANY one of its bosses was carded hid the rest of it, and the rest can
	// be sixteen Megas: the GO Fest Sunday habitat list holds seventeen, one of which
	// is Mega Falinks, which upstream also lists on tier 6. With the Mega table
	// lagging a debut the other sixteen are pending on the grid AND skipped here,
	// which is invisible on the whole public payload. Measured at 2026-09-05T18:00Z
	// with the Mega table emptied: 32 pending, 16 reachable nowhere.
	t.Run("a partly carded window still names the bosses with no card", func(t *testing.T) {
		carded := map[string]bool{bossKey("Regirock", false): true}
		var named []string
		for _, u := range buildUpcoming(windows, nil, carded, now, nil) {
			if u.Live && u.EventID == regis.EventID {
				named = upcomingBossNames(u)
			}
		}
		if len(named) != 2 {
			t.Fatalf("the Regi placeholder named %v, want the two with no card", named)
		}
		for _, n := range named {
			if n == "Regirock" {
				t.Error("named a boss that already has a card, which the card says better")
			}
		}
	})

	t.Run("a silenced live window is not advertised", func(t *testing.T) {
		sups := []RaidSuppression{liveSuppression(t)}
		if !sups[0].Active(now) {
			t.Fatal("the fixture note is not in force at this instant, so this test proves nothing")
		}
		got := buildUpcoming(windows, sups, map[string]bool{}, now, nil)
		for _, u := range got {
			if u.Live {
				t.Errorf("%s was advertised as live under a note that says it is not running: %v", u.EventID, upcomingBossNames(u))
			}
		}
		// And the future half is untouched by the note, deliberately: a rotation
		// that has not opened can only be reached by a note it opens INSIDE, and
		// those are the replacements the note is promising. Testing here anyway is
		// what hid the whole Mega Ascension week once already.
		if len(got) != 6 {
			t.Errorf("published %d future rotations under a live note, want the same 6: %v", len(got), upcomingIndex(got))
		}
	})
}

// TestBuildUpcomingDropsARunDescribedTwice: a rotation whose stated span sits wholly
// inside another naming the same boss is not a second appearance, it is the same
// uninterrupted run described again by a shorter source.
//
// The measured case, from the real corpus at 2026-08-25: the strip published Mega
// Gyarados twice, once for its own 26 August to 8 September rotation and once for the
// GO Fest Sunday habitat list on 6 September, which is a day in the middle of the
// first. The day keyed fold that shipped first could not see it, because the two
// start days differ.
func TestBuildUpcomingDropsARunDescribedTwice(t *testing.T) {
	now := utc(t, "2026-08-25T00:00:00Z")
	season := suppressWindow(t, "mega-gyarados-in-mega-raids-august-2026", "6", false, false,
		"2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000", "Mega Gyarados")
	habitat := suppressWindow(t, "pokemon-go-fest-2026-mega-finale", "6", false, true,
		"2026-09-06T00:01:00.000", "2026-09-06T23:59:00.000", "Mega Gyarados", "Mega Skarmory")

	got := buildUpcoming([]RaidWindow{season, habitat}, nil, map[string]bool{}, now, nil)

	var runs []string
	for _, u := range got {
		for _, b := range u.Bosses {
			if bossKey(b.Name, u.Shadow) == bossKey("Mega Gyarados", false) {
				runs = append(runs, u.EventID+" "+u.StartsAt[:10])
			}
		}
	}
	if len(runs) != 1 {
		t.Errorf("Mega Gyarados published %d times, want once: %v", len(runs), runs)
	} else if runs[0] != "mega-gyarados-in-mega-raids-august-2026 2026-08-26" {
		t.Errorf("kept %q, want the rotation that spans the whole run", runs[0])
	}

	// The habitat window keeps its OTHER boss, which is a genuine separate
	// appearance. Dropping the whole window would lose Mega Skarmory with it.
	kept := false
	for _, u := range got {
		if u.EventID != habitat.EventID {
			continue
		}
		for _, b := range u.Bosses {
			if b.Name == "Mega Skarmory" {
				kept = true
			}
			if b.Name == "Mega Gyarados" {
				t.Error("the contained run survived on the habitat window")
			}
		}
	}
	if !kept {
		t.Error("Mega Skarmory was lost with the contained run, but its own appearance is not described anywhere else")
	}
}

// TestBuildUpcomingGivesAProseReadBossItsSprite: a boss read out of an event page's
// PROSE carries no image, because a sentence has no img tag, and Mega Staraptor on
// Super Mega Raid Day is the only way into that path today.
//
// synthesizeBoss was given this fallback when the prose reader landed, which fixed
// the card and did nothing for the up next entry, so the rotation sat in the
// published schedule as a name with a blank where every other rotation had a sprite.
// It went out to production that way. Fixing one of two publication paths is the
// shape of mistake this file exists to catch.
func TestBuildUpcomingGivesAProseReadBossItsSprite(t *testing.T) {
	now := utc(t, upcomingNow)
	const sprite = "https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm398.icon.png"

	// Exactly what eventPageProseBosses emits: a name and nothing else.
	raidDay := suppressWindow(t, "staraptor-super-mega-raid-day-2026", "6", false, true,
		"2026-09-19T14:00:00.000", "2026-09-19T17:00:00.000", "Mega Staraptor")
	if raidDay.Bosses[0].Image != "" {
		t.Fatal("the fixture already carries a sprite, so this proves nothing")
	}

	lookup := func(name string) (speciesStats, bool) {
		if normalizeBossName(name) != normalizeBossName("Mega Staraptor") {
			return speciesStats{}, false
		}
		return speciesStats{Types: []string{"Flying"}, Atk: 278, Def: 207, Sta: 198, Image: sprite}, true
	}

	got := buildUpcoming([]RaidWindow{raidDay}, nil, map[string]bool{}, now, lookup)
	if len(got) != 1 {
		t.Fatalf("published %d rotations, want the one", len(got))
	}
	if got[0].Bosses[0].Image != sprite {
		t.Errorf("published sprite %q, want the one from the Mega table", got[0].Bosses[0].Image)
	}

	// The window it came from is memoized per event page and shared with the store's
	// raidSchedule, so the fill has to copy rather than write through.
	if raidDay.Bosses[0].Image != "" {
		t.Error("the fill wrote back into the shared window, which corrupts the event page memo")
	}

	// A boss that already has its own sprite keeps it, and a species the lookup has
	// never heard of stays blank rather than borrowing someone else's.
	withOwn := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-04T00:01:00.000", "2026-09-04T23:59:00.000", "Mega Raichu X")
	withOwn.Bosses[0].Image = "https://cdn.leekduck.com/its-own.png"
	unknown := suppressWindow(t, "mystery", "5", false, false,
		"2026-09-20T06:00:00.000", "2026-09-21T22:00:00.000", "Nobody")
	for _, u := range buildUpcoming([]RaidWindow{withOwn, unknown}, nil, map[string]bool{}, now, lookup) {
		switch u.EventID {
		case "mega-ascension":
			if u.Bosses[0].Image != "https://cdn.leekduck.com/its-own.png" {
				t.Errorf("overwrote a sprite the window already had: %q", u.Bosses[0].Image)
			}
		case "mystery":
			if u.Bosses[0].Image != "" {
				t.Errorf("invented a sprite for a species nothing knows: %q", u.Bosses[0].Image)
			}
		}
	}
}

// ── Location limited rosters ─────────────────────────────────────────────────

// TestEventPageLocationLimitedNearMisses adds to TestEventPageLocationLimitedPhrases
// beside it. The rule DELETES a whole roster when it fires, so a false positive is
// far more expensive than a miss, and the "at participating ... locations" pattern
// is the loose one: it allows sixty characters of anything between the two anchors.
func TestEventPageLocationLimitedNearMisses(t *testing.T) {
	cases := []struct {
		name    string
		prose   string
		limited bool
	}{
		// Real. The LEGO page's own two sentences, and the shapes upstream reuses.
		{"the LEGO sentence in full",
			"The following Pokémon will appear more frequently in raids at participating LEGO Store locations. " +
				"You may encounter one with a Special Background!", true},
		{"a single store, not plural",
			"Raids will run at participating GameStop location.", true},

		// Near misses that must NOT fire, because each one would cost a real
		// worldwide roster.
		{"participating trainers, which is every event page",
			"Participating Trainers will receive a bonus in all locations.", false},
		{"a store list far from the word locations",
			"Tickets are available at participating stores across nineteen different countries and regions this month, and raids run everywhere as normal.", false},
		{"the word local about weather, not about raids",
			"Local weather will boost these raids in your area.", false},

		// A clause that names the boilerplate in order to CONTRADICT it. Found in
		// review, and the reason eventPageLocationLimited skips a sentence carrying
		// "but" or "unless": every matcher is a plain substring test with no sense of
		// what a sentence is claiming, so without that guard this read as the
		// boilerplate and deleted the roster.
		{"remote passes named in a clause that then allows them",
			"Remote Raid Passes cannot be used more than five times per day, but they can be used for these raids.", false},
		{"local only, qualified into meaning the opposite",
			"These raids are local only unless you hold a Remote Raid Pass.", false},

		// This one DOES fire and arguably should not: a giveaway sentence has nothing
		// to do with where the raids are. It is recorded as it behaves rather than as
		// it ought to, because the sentence is only reachable if upstream files it
		// under the Raids header, which it does not: the LEGO page carries this exact
		// wording under Sales, and the assertion at the end of this test is what pins
		// that. Widening the phrase to tell a giveaway from a raid would cost more
		// than it removes.
		{"a giveaway sentence that happens to sit inside the Raids section",
			"Supplies are limited and subject to stock remaining at participating locations.", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := `<h2 id="raids" class="event-section-header raids">Raids</h2><p>` + c.prose + `</p>` +
				`<h3>Appearing in 5-Star Raids</h3><ul class="pkmn-list-flex">` +
				`<li class="pkmn-list-item"><div class="pkmn-name">Lunala</div></li></ul>`
			roster := parseEventPageRaids(page, day(t, "2026-08-03"), day(t, "2026-09-30"))
			if got := len(roster) == 0; got != c.limited {
				t.Errorf("%q: read %d bosses, limited=%v want limited=%v", c.prose, len(roster), got, c.limited)
			}
		})
	}

	// The giveaway sentence above is the reason the check is scoped to the Raids
	// section: the same LEGO page carries it under a different section header, and
	// reading the whole document would have deleted a roster over a plastic toy.
	page := `<h2 id="raids" class="event-section-header raids">Raids</h2>` +
		`<h3>Appearing in 5-Star Raids</h3><ul class="pkmn-list-flex">` +
		`<li class="pkmn-list-item"><div class="pkmn-name">Lunala</div></li></ul>` +
		`<h2 id="sales" class="event-section-header sales">Sales</h2>` +
		`<p>Supplies are limited and subject to stock remaining at participating locations.</p>`
	if roster := parseEventPageRaids(page, day(t, "2026-08-03"), day(t, "2026-09-30")); len(roster) != 1 {
		t.Errorf("a sentence in a LATER section deleted the raid roster: read %d bosses", len(roster))
	}
}

// ── Archive export ───────────────────────────────────────────────────────────

// TestRaidArchiveRowsJoinOnTheBossNotJustTheEvent: eventPageRaidWindows emits the
// BARE event id for every group it builds, deliberately, so the client can open the
// event modal with it. mega-ascension alone produces six windows under one id, one
// per day plus the whole event one, so a window map keyed on the event id alone
// handed every day boss the LAST day's window. Mega Skarmory was served correctly
// as the Wednesday boss and archived as Friday, and the fact table is keyed on
// (boss, window start), so the appearance history was written on the wrong day for
// every day scoped roster this reader has ever produced.
func TestRaidArchiveRowsJoinOnTheBossNotJustTheEvent(t *testing.T) {
	wed := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-02T00:01:00.000", "2026-09-02T23:59:00.000", "Mega Skarmory")
	thu := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-03T00:01:00.000", "2026-09-03T23:59:00.000", "Mega Starmie")
	fri := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-04T00:01:00.000", "2026-09-04T23:59:00.000", "Mega Raichu X", "Mega Raichu Y")
	whole := suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-08-31T10:00:00.000", "2026-09-04T23:59:00.000", "Mega Latias", "Mega Latios")

	s := &Store{
		raidSchedule: []RaidWindow{wed, thu, fri, whole},
		raids: json.RawMessage(`{"6":[
			{"pokemon_name":"Mega Skarmory","event_id":"mega-ascension","source":"events"},
			{"pokemon_name":"Mega Starmie","event_id":"mega-ascension","source":"events"},
			{"pokemon_name":"Mega Raichu X","event_id":"mega-ascension","source":"events"},
			{"pokemon_name":"Mega Latios","event_id":"mega-ascension","source":"events"},
			{"pokemon_name":"Mega Spelledifferently","event_id":"mega-ascension","source":"events"}
		]}`),
	}
	rows := map[string]RaidArchiveRow{}
	for _, r := range s.RaidArchiveRows() {
		rows[r.Species] = r
	}

	for _, c := range []struct {
		species string
		window  RaidWindow
	}{
		{"Mega Skarmory", wed},
		{"Mega Starmie", thu},
		{"Mega Raichu X", fri},
		{"Mega Latios", whole},
	} {
		r, ok := rows[c.species]
		if !ok {
			t.Errorf("%s is served but absent from the archive rows", c.species)
			continue
		}
		if !r.WindowStart.Equal(c.window.StartsUTC) || !r.WindowEnd.Equal(c.window.EndsUTC) {
			t.Errorf("%s archived on %s .. %s, want its own day %s .. %s",
				c.species,
				r.WindowStart.Format(time.RFC3339), r.WindowEnd.Format(time.RFC3339),
				c.window.StartsUTC.Format(time.RFC3339), c.window.EndsUTC.Format(time.RFC3339))
		}
	}

	// The defect, named: Wednesday's boss must not carry Friday's window.
	if r := rows["Mega Skarmory"]; r.WindowStart.Equal(fri.StartsUTC) {
		t.Error("Mega Skarmory was archived with the last day's window, which is the bug this join exists to fix")
	}

	// A boss no window under that id names still gets the rotation's own span,
	// because that is a better answer than none. It is the LAST ending window under
	// the id, so the assertion is on that rather than on which one it happens to be.
	fallback, ok := rows["Mega Spelledifferently"]
	if !ok {
		t.Fatal("a boss whose name does not join lost its archive row entirely")
	}
	if !fallback.HasWindow() {
		t.Error("a boss no window names got no window at all, so its appearance cannot be recorded")
	}
	latest := wed.EndsUTC
	for _, w := range []RaidWindow{thu, fri, whole} {
		if w.EndsUTC.After(latest) {
			latest = w.EndsUTC
		}
	}
	if !fallback.WindowEnd.Equal(latest) {
		t.Errorf("the per event fallback ends %s, want the event's last ending window at %s",
			fallback.WindowEnd.Format(time.RFC3339), latest.Format(time.RFC3339))
	}
}

// ── Detail refresh planning ──────────────────────────────────────────────────

// TestPlanDetailRefreshKeepsABlankLinkWeAlreadyHold is a narrow fix with a trap on
// either side of it.
//
// An upstream entry that loses its link for one refresh used to take its scraped
// page down with it, and with it that page's raid windows and any suspension note,
// silently. But marking EVERY blank link entry active is the wrong fix: a junk feed
// is mostly entries shaped exactly like that, the caller reads an EMPTY active set
// as "keep everything", and making the set non empty stops that guard firing and
// evicts every other page instead. Requiring the cached copy to already exist is
// what protects the real case without arming the wipe.
func TestPlanDetailRefreshKeepsABlankLinkWeAlreadyHold(t *testing.T) {
	now := utc(t, "2026-09-01T21:00:00Z")

	t.Run("a blank link on a page we hold stays active and is not queued", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "mega-ascension", Link: ""})
		fetchedAt := map[string]time.Time{"mega-ascension": now.Add(-time.Hour)}
		active, jobs := planDetailRefresh(entries, fetchedAt, now, anywhereOnEarth)
		if !active["mega-ascension"] {
			t.Error("a page we hold was dropped from active, so the eviction loop would delete it and its raid windows with it")
		}
		if len(jobs) != 0 {
			t.Errorf("queued %d jobs for an entry with no link to fetch", len(jobs))
		}
	})

	t.Run("a blank link on a page we do not hold is still not active", func(t *testing.T) {
		// The trap. Marking this active is the obvious fix and it is wrong: it
		// makes the junk feed guard stop firing and wipes every OTHER cached page.
		// TestRefreshEventDetailsKeepsCacheOnJunkFeed catches it in two subtests.
		entries := feed(feedEntry{EventID: "never-scraped", Link: ""})
		active, jobs := planDetailRefresh(entries, nil, now, anywhereOnEarth)
		if len(active) != 0 {
			t.Errorf("active = %v, want empty: the caller reads an empty set as keep everything", active)
		}
		if len(jobs) != 0 {
			t.Errorf("queued %d jobs, want 0", len(jobs))
		}
	})

	t.Run("a page we hold whose event has ended is still evicted", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "over", Link: "", End: strp("2026-08-01T10:00:00.000")})
		fetchedAt := map[string]time.Time{"over": now.Add(-time.Hour)}
		active, _ := planDetailRefresh(entries, fetchedAt, now, anywhereOnEarth)
		if active["over"] {
			t.Error("an event finished a month ago kept its page, so nothing would ever be evicted")
		}
	})
}

// TestDetailRefreshMaxAge: these pages are the ONLY source for a whole class of
// rotation, and those rotations are scoped to a single calendar day. Twelve hours
// against a one day window means our copy can be half a rotation out of date on the
// exact pages a trainer is most likely to be reading.
func TestDetailRefreshMaxAge(t *testing.T) {
	now := utc(t, "2026-09-01T21:00:00Z")
	cases := []struct {
		name  string
		start *string
		want  time.Duration
	}{
		{"already running", strp("2026-08-31T10:00:00.000"), detailRefreshAgeNear},
		{"opens in a few hours", strp("2026-09-02T06:00:00.000"), detailRefreshAgeNear},
		{"opens well beyond the window", strp("2026-09-30T06:00:00.000"), detailRefreshAge},
		{"an absolute timestamp already past", strp("2026-08-31T10:00:00.000Z"), detailRefreshAgeNear},
		{"no start at all", nil, detailRefreshAge},
		{"a start that will not parse", strp("sometime next week"), detailRefreshAge},
		{"an empty start string", strp(""), detailRefreshAge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detailRefreshMaxAge(feedEntry{EventID: "x", Start: c.start}, now); got != c.want {
				t.Errorf("maxAge = %s, want %s", got, c.want)
			}
		})
	}

	// The start is read in the EARLIEST zone, not in the caller's anywhereOnEarth.
	// The two readings are 26 hours apart, and reading a start in UTC-12 would leave
	// an event's page on the slow cycle for its whole opening day in every zone
	// ahead of that one.
	t.Run("a start that has arrived in UTC+14 is already near", func(t *testing.T) {
		near := utc(t, "2026-09-07T12:00:00Z")
		const opens = "2026-09-08T00:01:00.000"
		// Read in UTC-12 this is more than a day away, so the two zones genuinely
		// disagree here and the case is still worth having.
		late, _, ok := ParseFeedTime(opens, anywhereOnEarth)
		if !ok || !late.After(near.Add(detailNearWindow)) {
			t.Fatalf("the two zone readings no longer disagree at this instant (%v), so this case proves nothing", late)
		}
		if got := detailRefreshMaxAge(feedEntry{EventID: "opens-tomorrow", Start: strp(opens)}, near); got != detailRefreshAgeNear {
			t.Errorf("maxAge = %s, want %s: 00:01 tomorrow has already arrived in UTC+14", got, detailRefreshAgeNear)
		}
	})

	// And the age actually reaches planDetailRefresh, or the constant is decoration.
	t.Run("a live event four hours stale is queued and a distant one is not", func(t *testing.T) {
		fetchedAt := map[string]time.Time{"live": now.Add(-4 * time.Hour), "distant": now.Add(-4 * time.Hour)}
		entries := feed(
			feedEntry{EventID: "live", Link: "https://leekduck.com/events/live/", Start: strp("2026-08-31T10:00:00.000")},
			feedEntry{EventID: "distant", Link: "https://leekduck.com/events/distant/", Start: strp("2026-09-30T06:00:00.000")},
		)
		_, jobs := planDetailRefresh(entries, fetchedAt, now, anywhereOnEarth)
		if len(jobs) != 1 || jobs[0].id != "live" {
			t.Errorf("queued %v, want the live event alone: it is inside the 3 hour age and the distant one is inside the 12 hour one", jobs)
		}
	})
}

// ── Suppression and the page cache ───────────────────────────────────────────

// TestSuppressionHoldsOnlyItsOwnPage: a note may legitimately outlive the event
// that carries it, and the page cache evicts a page the moment its event is over
// anywhere on Earth. Without this hold the Mega Ascension note vanished around 22
// hours before it expired and every seasonal rotation came back onto the grid for
// the rest of the suspension. It must hold ONLY the page the note came from, or a
// single live note would pin the whole cache.
func TestSuppressionHoldsOnlyItsOwnPage(t *testing.T) {
	sup := liveSuppression(t)
	sups := []RaidSuppression{sup}
	inForce := utc(t, "2026-09-01T21:00:00Z")

	if !inForce.Before(sup.EndsUTC) {
		t.Fatalf("the fixture note has already expired at %s, so this test proves nothing", inForce)
	}
	if !suppressionHoldsPage(sups, sup.EventID, inForce) {
		t.Errorf("the page a live note was read from is not held, so eviction would delete the note with it")
	}
	if suppressionHoldsPage(sups, "some-other-event", inForce) {
		t.Error("a live note held a page it has nothing to do with, so the cache would stop shrinking")
	}
	if suppressionHoldsPage(sups, "", inForce) {
		t.Error("an empty event id was held; every cached page would match it")
	}
	if suppressionHoldsPage(nil, sup.EventID, inForce) {
		t.Error("a page was held with no notes in force at all")
	}

	// The hold lifts the instant the note does, at its own end and not at the
	// event's: this note runs to the 6th while its event ends on the 4th.
	if suppressionHoldsPage(sups, sup.EventID, sup.EndsUTC) {
		t.Error("the hold outlived the note, so the page would never be evicted")
	}
	if suppressionHoldsPage(sups, sup.EventID, sup.EndsUTC.Add(time.Hour)) {
		t.Error("the hold outlived the note by an hour")
	}
}

// ── Upstream list shapes ─────────────────────────────────────────────────────

// TestFetchPGAPIGroupedFlatCurrentList: currentList changed from a tier keyed map
// to a flat array in the max battles endpoint, and the guard for that used to fire
// for ANY endpoint. It returns before the "no bosses parsed" check, so the day
// raidboss.json adopts the same shape the store would write {} over cache/raids.json
// and serve it: reconcileRaids fails open on an EMPTY upstream blob and {} is two
// bytes, so tiers 1 and 3, which no window governs, would simply have gone missing
// with nothing logged anywhere.
func TestFetchPGAPIGroupedFlatCurrentList(t *testing.T) {
	type tierRow = struct {
		src    string
		dst    string
		shadow bool
	}
	raidTiers := []tierRow{{"mega", "6", false}, {"lvl5", "5", false}, {"lvl1", "1", false}}
	maxTiers := []tierRow{{"tier_1", "1", false}}

	serve := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
	}

	t.Run("a flat array is an error for the raids endpoint", func(t *testing.T) {
		srv := serve(`{"currentList":[]}`)
		defer srv.Close()
		s := &Store{client: srv.Client()}
		grouped, total, err := s.fetchPGAPIGrouped(srv.URL, raidTiers, false)
		if err == nil {
			t.Fatalf("a flat currentList was accepted: %d bosses in %v", total, grouped)
		}
		if grouped != nil {
			t.Errorf("grouped = %v, want nil so the caller writes nothing over the cached blob", grouped)
		}
	})

	t.Run("a flat array is an empty map for max battles", func(t *testing.T) {
		srv := serve(`{"currentList":[]}`)
		defer srv.Close()
		s := &Store{client: srv.Client()}
		grouped, total, err := s.fetchPGAPIGrouped(srv.URL, maxTiers, true)
		if err != nil {
			t.Fatalf("an empty max battle list errored: %v", err)
		}
		if grouped == nil || len(grouped) != 0 || total != 0 {
			t.Errorf("grouped = %v total = %d, want an empty non nil map and 0", grouped, total)
		}
	})

	// A populated array is refused for the raids endpoint too, not just an empty
	// one: the shape is what is wrong, and a flat list carries no tiers to map.
	t.Run("a populated flat array is still an error for the raids endpoint", func(t *testing.T) {
		srv := serve(`{"currentList":[{"names":{"English":"Lunala"}}]}`)
		defer srv.Close()
		s := &Store{client: srv.Client()}
		if _, _, err := s.fetchPGAPIGrouped(srv.URL, raidTiers, false); err == nil {
			t.Error("a flat currentList with entries in it was accepted")
		}
	})

	// And the ordinary shape still works, so the guard is not simply refusing
	// everything.
	t.Run("a tier keyed map parses", func(t *testing.T) {
		srv := serve(`{"currentList":{"lvl5":[{"names":{"English":"Lunala"},"types":["Psychic","Ghost"],` +
			`"cpRange":[2219,2310],"cpRangeBoost":[2774,2887],"shiny":true}]}}`)
		defer srv.Close()
		s := &Store{client: srv.Client()}
		grouped, total, err := s.fetchPGAPIGrouped(srv.URL, raidTiers, false)
		if err != nil {
			t.Fatalf("a tier keyed currentList errored: %v", err)
		}
		if total != 1 || len(grouped["5"]) != 1 || grouped["5"][0].PokemonName != "Lunala" {
			t.Fatalf("parsed %d bosses into %v, want Lunala under tier 5", total, grouped)
		}
		if grouped["5"][0].CP != 2219 || grouped["5"][0].CPBoostedMax != 2887 {
			t.Errorf("Lunala came back as %+v, want the CP ranges upstream sent", grouped["5"][0])
		}
	})
}
