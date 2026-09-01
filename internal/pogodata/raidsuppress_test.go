package pogodata

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// megaAscensionNote is the seasonal schedule note as it actually appeared on both
// the mega-ascension and pokemon-go-fest-2026-mega-finale pages on 2026-09-01,
// copied out of cache/event_details.json.
//
// Inlined rather than kept in testdata because the exact wording is the thing under
// test: a reader has to be able to see what the parser is being asked to survive.
const megaAscensionNote = `<h2 id="seasonal-schedule-note">Seasonal schedule note</h2>` +
	`<p>Daily Discoveries, Seasonal Mega Raids, Seasonal Five-Star Raids, Seasonal Shadow Raids, ` +
	`Seasonal Raid Hours, and Seasonal Spotlight Hours will not take place during the Mega Ascension ` +
	`event and Pokémon GO Fest 2026: Mega Finale. Mega Ascension and Mega Finale Raids will take ` +
	`their place starting Monday, August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.</p>`

// megaAscensionRaidsProse is the pair of paragraphs at the top of the same page's
// Raids section. The second is the reason event page windows are additive, and it
// must never be mistaken for a note or read as contradicting one.
const megaAscensionRaidsProse = `<p>The following Mega-Evolved Pokémon will appear in raids during the Mega Ascension event.</p>` +
	`<p>Mega Raids will make up the majority of raids during the Mega Ascension event. Seasonal Raid Bosses may also appear during this time.</p>`

// noteSpan is the sentence that carries the dates, appended to a one line category
// test so every row differs only in the categories it names.
const noteSpan = ` Mega Ascension Raids will take their place starting Monday, August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.`

func megaAscensionEntry() raidFeedEntry {
	return raidFeedEntry{
		EventID: "mega-ascension", Name: "Mega Ascension", EventType: "event",
		Start: "2026-08-31T10:00:00.000", End: "2026-09-04T23:59:00.000",
	}
}

func TestParseSuppressionNoteMegaAscension(t *testing.T) {
	notes := parseSuppressionNotes(megaAscensionNote, day(t, "2026-08-31"), day(t, "2026-09-04"))
	if len(notes) != 1 {
		t.Fatalf("parsed %d notes, want 1", len(notes))
	}
	n := notes[0]
	if want := []string{"5", "5:shadow", "6"}; !reflect.DeepEqual(n.groups, want) {
		t.Errorf("groups %v, want %v", n.groups, want)
	}
	// The first date writes no year, so 2026 comes from the event span. The second
	// writes its own.
	if n.rawStart != "2026-08-31T00:01:00.000" {
		t.Errorf("rawStart %q, want 2026-08-31T00:01:00.000", n.rawStart)
	}
	// The note OUTLASTS its own event, which ends 2026-09-04. This is why
	// resolveHeadingDay cannot be reused: it clamps to the event span, and clamping
	// here would put every seasonal rotation back on the grid for the GO Fest
	// weekend, which is the half of the week the note exists to cover.
	if n.rawEnd != "2026-09-06T23:59:00.000" {
		t.Errorf("rawEnd %q, want 2026-09-06T23:59:00.000", n.rawEnd)
	}
}

func TestParseSuppressionNotesIgnoresTheRaidsSectionProse(t *testing.T) {
	evStart, evEnd := day(t, "2026-08-31"), day(t, "2026-09-04")

	if got := parseSuppressionNotes(megaAscensionRaidsProse, evStart, evEnd); len(got) != 0 {
		t.Fatalf("the Raids section prose produced %d notes, want none", len(got))
	}
	// And standing next to the real note it neither adds a group nor cancels one.
	notes := parseSuppressionNotes(megaAscensionRaidsProse+megaAscensionNote, evStart, evEnd)
	if len(notes) != 1 {
		t.Fatalf("prose plus note produced %d notes, want 1", len(notes))
	}
	if want := []string{"5", "5:shadow", "6"}; !reflect.DeepEqual(notes[0].groups, want) {
		t.Errorf("groups %v, want %v", notes[0].groups, want)
	}
}

func TestSuppressionCategories(t *testing.T) {
	cases := []struct {
		sentence string
		want     []string
	}{
		{"Seasonal Mega Raids will not take place.", []string{"6"}},
		{"Seasonal Five-Star Raids will not take place.", []string{"5"}},
		{"Seasonal 5-Star Raids will not take place.", []string{"5"}},
		{"Seasonal Shadow Raids will not take place.", []string{"5:shadow"}},
		{"Seasonal Mega Raids and Seasonal Shadow Raids will not be taking place.", []string{"5:shadow", "6"}},
		// Named by the live note, but none is a rotation this app schedules, so
		// there is nothing for them to suppress.
		{"Seasonal Raid Hours and Seasonal Spotlight Hours will not take place.", nil},
		{"Daily Discoveries will not take place during this event.", nil},
		// No trigger. This is the sentence that sits in the Raids section.
		{"Seasonal Raid Bosses may also appear during this time.", nil},
		{"Mega Raids will make up the majority of raids during this event.", nil},
		// The false positive that matters: without the mandatory "seasonal" this
		// would empty the entire Mega tier on the strength of a footnote.
		{"Mega Raids will not take place at EX Gyms.", nil},
		// Category and trigger in different sentences.
		{"Seasonal Mega Raids will continue as normal. Daily Adventure Incense will not take place.", nil},
	}
	for _, c := range cases {
		t.Run(c.sentence, func(t *testing.T) {
			notes := parseSuppressionNotes("<p>"+c.sentence+noteSpan+"</p>", day(t, "2026-08-31"), day(t, "2026-09-04"))
			if len(c.want) == 0 {
				if len(notes) != 0 {
					t.Fatalf("produced %d notes (%v), want none", len(notes), notes[0].groups)
				}
				return
			}
			if len(notes) != 1 {
				t.Fatalf("produced %d notes, want 1", len(notes))
			}
			if !reflect.DeepEqual(notes[0].groups, c.want) {
				t.Errorf("groups %v, want %v", notes[0].groups, c.want)
			}
		})
	}
}

func TestResolveNoteDate(t *testing.T) {
	evStart, evEnd := day(t, "2026-08-31"), day(t, "2026-09-04")
	cases := []struct {
		prose string
		want  string // empty means the date must not resolve
	}{
		{"Monday, August 31", "2026-08-31"},         // year from the span, weekday agrees
		{"Sunday, September 6, 2026", "2026-09-06"}, // outside the span and must not be clamped
		{"September 6, 2026", "2026-09-06"},         // no weekday
		{"August 31, at 6:00 a.m.", "2026-08-31"},   // no weekday, one candidate year
		{"Tuesday, August 31", ""},                  // weekday contradicts the date
		{"Sunday, February 30, 2026", ""},           // a date that does not exist
		// An explicit year beats the span, and this row proves it: 31 August is a
		// Sunday in 2025 and a Monday in 2026, so the span silently winning would
		// have failed the weekday check instead of returning 2025.
		{"Sunday, August 31, 2025", "2025-08-31"},
	}
	for _, c := range cases {
		t.Run(c.prose, func(t *testing.T) {
			dates := findNoteDates(c.prose)
			if len(dates) != 1 {
				t.Fatalf("found %d dates in %q, want 1", len(dates), c.prose)
			}
			got, ok := resolveNoteDate(dates[0], evStart, evEnd)
			if c.want == "" {
				if ok {
					t.Fatalf("resolved to %s, want a refusal", got.Format("2006-01-02"))
				}
				return
			}
			if !ok {
				t.Fatalf("refused %q, want %s", c.prose, c.want)
			}
			if got.Format("2006-01-02") != c.want {
				t.Errorf("resolved to %s, want %s", got.Format("2006-01-02"), c.want)
			}
		})
	}
}

// TestResolveNoteDateRefusesAnUnsettleableYear covers the shape that spans New
// Year: with no year in the prose and no weekday to check it against, both
// candidate years fit and the date is refused rather than guessed.
func TestResolveNoteDateRefusesAnUnsettleableYear(t *testing.T) {
	dates := findNoteDates("August 31, at 6:00 a.m.")
	if _, ok := resolveNoteDate(dates[0], day(t, "2025-12-01"), day(t, "2026-01-31")); ok {
		t.Error("resolved a date that fits two candidate years, want a refusal")
	}
}

func TestNoteClock(t *testing.T) {
	cases := []struct{ prose, want string }{
		{"August 1, at 12:01 a.m.", "T00:01:00.000"},
		{"August 1, at 11:59 p.m.", "T23:59:00.000"},
		{"August 1, at 12:00 p.m.", "T12:00:00.000"},
		{"August 1, at 12:00 a.m.", "T00:00:00.000"},
		{"August 1, at 6:00 a.m.", "T06:00:00.000"},
		{"August 1, at 10:00 A.M.", "T10:00:00.000"},
		{"Monday, August 31", "FALLBACK"}, // no clock stated
	}
	for _, c := range cases {
		t.Run(c.prose, func(t *testing.T) {
			dates := findNoteDates(c.prose)
			if len(dates) != 1 {
				t.Fatalf("found %d dates, want 1", len(dates))
			}
			if got := noteClock(dates[0], "FALLBACK"); got != c.want {
				t.Errorf("noteClock = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSuppressionSpanIsTheNarrowReading pins the safety argument. A rotation is read
// at its widest so nobody loses a boss they can still raid; a suppression is read at
// its narrowest for the same reason, from the other side.
func TestSuppressionSpanIsTheNarrowReading(t *testing.T) {
	const rawStart, rawEnd = "2026-08-31T00:01:00.000", "2026-09-06T23:59:00.000"

	start, end, ok := suppressionSpan(rawStart, rawEnd)
	if !ok {
		t.Fatal("the live note's span would not resolve")
	}
	if want := utc(t, "2026-08-31T12:01:00Z"); !start.Equal(want) {
		t.Errorf("start %s, want %s", start.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if want := utc(t, "2026-09-06T09:59:00Z"); !end.Equal(want) {
		t.Errorf("end %s, want %s", end.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// Strictly inside the window reading of the very same strings, by 26 hours at
	// each edge. This is what fails if anyone simplifies either span to one zone.
	wStart, wEnd, ok := raidWindowSpan(rawStart, rawEnd)
	if !ok {
		t.Fatal("raidWindowSpan refused the same strings")
	}
	if !start.After(wStart) || !end.Before(wEnd) {
		t.Errorf("suppression %s..%s is not inside window %s..%s",
			start.Format(time.RFC3339), end.Format(time.RFC3339),
			wStart.Format(time.RFC3339), wEnd.Format(time.RFC3339))
	}

	// The documented limitation, asserted rather than discovered: a span shorter
	// than the 26 hour overlap inverts and suppresses nothing.
	if _, _, ok := suppressionSpan("2026-09-05T10:00:00.000", "2026-09-05T18:00:00.000"); ok {
		t.Error("a one afternoon span resolved, want a refusal")
	}

	// No absolute-instant case here on purpose. parseSuppressionNotes builds every
	// rawStart and rawEnd itself out of a resolved date plus noteClock, so a "Z"
	// string can never reach suppressionSpan and a test for one would be asserting
	// against a path nothing walks.
}

func TestParseSuppressionNotesFailsOpen(t *testing.T) {
	evStart, evEnd := day(t, "2026-08-31"), day(t, "2026-09-04")
	cases := []struct{ name, html string }{
		{"empty", ""},
		{"whitespace", "<p>   \n  </p>"},
		{"no dates at all", "<p>Seasonal Mega Raids will not take place during this event. Mega Ascension Raids will take their place starting sometime next week.</p>"},
		{"only one date", "<p>Seasonal Mega Raids will not take place during this event. Raids will take their place starting Monday, August 31, at 12:01 a.m.</p>"},
		{"dates reversed", "<p>Seasonal Mega Raids will not take place during this event. Raids take their place starting Sunday, September 6, 2026, at 11:59 p.m. to Monday, August 31, at 12:01 a.m. local time.</p>"},
		// The block rule's own limit, written down rather than pretended away: a
		// note split across two paragraphs is not read.
		{"split across paragraphs", "<p>Seasonal Mega Raids will not take place during the Mega Ascension event.</p><p>Mega Ascension Raids will take their place starting Monday, August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.</p>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSuppressionNotes(c.html, evStart, evEnd); len(got) != 0 {
				t.Errorf("produced %d notes, want none", len(got))
			}
		})
	}
}

func TestEventPageSuppressionsAttributesTheEvent(t *testing.T) {
	// The note twice over, as a nested block or a repeated section would produce.
	sups := eventPageSuppressions(megaAscensionEntry(), megaAscensionNote+megaAscensionNote)
	if len(sups) != 1 {
		t.Fatalf("built %d suppressions, want 1 after the dedupe", len(sups))
	}
	s := sups[0]
	if s.EventID != "mega-ascension" || s.Name != "Mega Ascension" {
		t.Errorf("attributed to %q / %q", s.EventID, s.Name)
	}
	if !s.Suppresses("5") || !s.Suppresses("5:shadow") || !s.Suppresses("6") || s.Suppresses("3") {
		t.Errorf("Suppresses is wrong for groups %v", s.Groups)
	}
	if !s.Active(utc(t, "2026-09-01T12:00:00Z")) {
		t.Error("not active on 2026-09-01, the day the site was still showing the Regis")
	}
	if s.Active(utc(t, "2026-08-31T06:00:00Z")) || s.Active(utc(t, "2026-09-06T12:00:00Z")) {
		t.Error("active outside its own narrow span")
	}
	if s.Text == "" {
		t.Error("kept no text; the admin check has nothing to show")
	}

	// An unparseable feed span yields nothing, mirroring eventPageRaidWindows.
	bad := megaAscensionEntry()
	bad.Start = "not a time"
	if got := eventPageSuppressions(bad, megaAscensionNote); len(got) != 0 {
		t.Errorf("built %d suppressions off an unusable event span", len(got))
	}
}

// ── Reconcile ────────────────────────────────────────────────────────────────

// suppressedUpstream is exactly what cache/raids.json held on 2026-09-01: the two
// bosses pokemon-go-api was still serving after the game had stopped running them.
// The Regi trio is deliberately absent, because upstream never listed it at all.
const suppressedUpstream = `{"5":[{"pokemon_name":"Shadow Giratina (Altered Forme)"}],"6":[{"pokemon_name":"Mega Gyarados"}]}`

// suppressWindow builds one live rotation from the raw strings the feed sends, so
// the spans in these tests are the ones raidWindowSpan really produces.
func suppressWindow(t *testing.T, id, tier string, shadow, additive bool, rawStart, rawEnd string, bosses ...string) RaidWindow {
	t.Helper()
	start, end, ok := raidWindowSpan(rawStart, rawEnd)
	if !ok {
		t.Fatalf("test window %s has an unusable span", id)
	}
	w := RaidWindow{
		EventID: id, Name: id, Tier: tier, Shadow: shadow, Additive: additive,
		RawStart: rawStart, RawEnd: rawEnd, StartsUTC: start, EndsUTC: end,
	}
	for _, b := range bosses {
		w.Bosses = append(w.Bosses, WindowBoss{Name: b})
	}
	return w
}

// suppressedWeek is the live schedule on 2026-09-01: the three seasonal rotations
// the feed still had running to the 8th, plus the one Mega the event page supplied
// for that day.
func suppressedWeek(t *testing.T) []RaidWindow {
	t.Helper()
	const seasonStart, seasonEnd = "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000"
	return []RaidWindow{
		suppressWindow(t, "regis", "5", false, false, seasonStart, seasonEnd, "Regirock", "Regice", "Registeel"),
		suppressWindow(t, "shadow-giratina", "5", true, false, seasonStart, seasonEnd, "Giratina (Altered)"),
		suppressWindow(t, "mega-gyarados", "6", false, false, seasonStart, seasonEnd, "Mega Gyarados"),
		suppressWindow(t, "mega-ascension", "6", false, true, "2026-09-01T00:01:00.000", "2026-09-01T23:59:00.000", "Mega Victreebel"),
	}
}

func liveSuppression(t *testing.T) RaidSuppression {
	t.Helper()
	sups := eventPageSuppressions(megaAscensionEntry(), megaAscensionNote)
	if len(sups) != 1 {
		t.Fatalf("fixture note produced %d suppressions", len(sups))
	}
	return sups[0]
}

// suppressLookup knows the Regis, from the embedded species data, and the two Megas
// the reconcile tests use. A card being built proves a window reached the
// synthesizer, so a card NOT being built means the rule stopped it.
func suppressLookup(t *testing.T) speciesLookup {
	t.Helper()
	base := testLookup(t)
	return func(name string) (speciesStats, bool) {
		if s, ok := megaOnlyLookup(name); ok {
			return s, true
		}
		return base(name)
	}
}

// TestSuppressionDropsTheSeasonalRotations is the bug of 2026-09-01, with the shapes
// that actually produced it.
func TestSuppressionDropsTheSeasonalRotations(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	sups := []RaidSuppression{liveSuppression(t)}

	served, _, stats := reconcileRaids(json.RawMessage(suppressedUpstream), suppressedWeek(t), sups, now, suppressLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	// Tier 5 goes empty by two different mechanisms at once. Shadow Giratina is
	// dropped from the upstream blob, which still listed it; the Regis are never
	// synthesized, because they only ever existed on the grid as a synthesis of a
	// feed window nobody had corrected.
	if got := names(tiers["5"]); len(got) != 0 {
		t.Errorf("tier 5 served %v, want nothing", got)
	}
	// Tier 6 keeps the day's Mega Ascension boss and loses the seasonal rotation.
	if got, want := names(tiers["6"]), []string{"Mega Victreebel"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tier 6 served %v, want %v", got, want)
	}
	if stats.Suppressed != 3 || stats.SuppressedWindows != 3 {
		t.Errorf("Suppressed %d groups / %d windows, want 3 and 3", stats.Suppressed, stats.SuppressedWindows)
	}
	if stats.Dropped != 2 || stats.Synthesized != 1 || stats.Pending != 0 {
		t.Errorf("Dropped %d, Synthesized %d, Pending %d; want 2, 1, 0", stats.Dropped, stats.Synthesized, stats.Pending)
	}
	if stats.SuppressionDisarmed {
		t.Error("the circuit breaker fired on a suppression that left tier 6 populated")
	}
}

// TestSuppressionIsInertOutsideItsSpan is the other half of the narrow reading: for
// a day either side of the note, the seasonal rotations really are still running for
// somebody, and the grid says so.
func TestSuppressionIsInertOutsideItsSpan(t *testing.T) {
	sups := []RaidSuppression{liveSuppression(t)}
	for _, now := range []string{"2026-08-31T06:00:00Z", "2026-09-06T12:00:00Z"} {
		t.Run(now, func(t *testing.T) {
			served, _, stats := reconcileRaids(json.RawMessage(suppressedUpstream), suppressedWeek(t), sups,
				utc(t, now), suppressLookup(t), testCPMs(t))
			tiers := decodeTiers(t, served)
			if stats.Suppressed != 0 {
				t.Fatalf("Suppressed %d groups outside the note's own span", stats.Suppressed)
			}
			for _, want := range []string{"Shadow Giratina (Altered Forme)", "Regirock", "Regice", "Registeel"} {
				if !hasName(tiers["5"], want) {
					t.Errorf("tier 5 lost %s: %v", want, names(tiers["5"]))
				}
			}
			if !hasName(tiers["6"], "Mega Gyarados") {
				t.Errorf("tier 6 lost Mega Gyarados: %v", names(tiers["6"]))
			}
		})
	}
}

// TestSuppressionNeverRemovesAnAdditiveBoss covers the case the whole exemption
// exists for: the page that suspends a tier is the same page that repopulates it.
func TestSuppressionNeverRemovesAnAdditiveBoss(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Victreebel"}]}`)
	windows := []RaidWindow{
		suppressWindow(t, "mega-ascension", "6", false, true, "2026-09-01T00:01:00.000", "2026-09-01T23:59:00.000", "Mega Victreebel"),
	}
	served, _, stats := reconcileRaids(upstream, windows, []RaidSuppression{liveSuppression(t)}, now, suppressLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if got, want := names(tiers["6"]), []string{"Mega Victreebel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tier 6 served %v, want %v", got, want)
	}
	if tiers["6"][0].EventID != "mega-ascension" {
		t.Errorf("card not annotated with the window that saved it: %q", tiers["6"][0].EventID)
	}
	if stats.Dropped != 0 {
		t.Errorf("Dropped %d, want 0", stats.Dropped)
	}
}

// TestSuppressionDoesNotAdvertiseASilencedRotation covers the strip under the grid,
// which would otherwise announce the rotation the grid has just deleted.
func TestSuppressionDoesNotAdvertiseASilencedRotation(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	_, upcoming, _ := reconcileRaids(json.RawMessage(suppressedUpstream), suppressedWeek(t),
		[]RaidSuppression{liveSuppression(t)}, now, suppressLookup(t), testCPMs(t))

	for _, u := range upcoming {
		if u.Live {
			t.Errorf("advertised %q as the live placeholder for a silenced group", u.EventID)
		}
	}
}

// TestSuppressionDoesNotSilenceItsOwnReplacements is the regression that mattered
// most, because getting it wrong is worse than the bug the note exists to fix.
//
// Armored Mewtwo opens on the Mega Finale Saturday, inside the Mega Ascension note,
// and it IS one of the raids that note promises will take the seasonal ones' place.
// Reading it as a casualty of the note deleted the whole weekend's five star raid
// from the grid AND from the up next strip, so it appeared nowhere at all.
func TestSuppressionDoesNotSilenceItsOwnReplacements(t *testing.T) {
	sups := []RaidSuppression{liveSuppression(t)}
	// The real shape: its own raid-battles feed entry, opening on the Saturday.
	armored := suppressWindow(t, "armored-mewtwo-in-5-star-raid-battles-september-2026",
		"5", false, false, "2026-09-05T10:00:00.000", "2026-09-06T18:00:00.000", "Armored Mewtwo")

	// Announced days ahead, while the note is in force: it is the answer to "what is
	// next", not something to hide.
	_, upcoming, _ := reconcileRaids(json.RawMessage(suppressedUpstream), append(suppressedWeek(t), armored),
		sups, utc(t, "2026-09-01T12:00:00Z"), suppressLookup(t), testCPMs(t))
	if !advertises(upcoming, armored.EventID) {
		t.Errorf("did not advertise a rotation that opens inside the note: %+v", upcoming)
	}

	// And once it opens it is genuinely live, so it may not be silenced either.
	now := utc(t, "2026-09-05T12:00:00Z")
	if !armored.Active(now) {
		t.Fatal("test window is not active when it should be")
	}
	if silencedBy(sups, armored, now) {
		t.Error("silenced the rotation the note itself promised as a replacement")
	}
	// The seasonal rotations it replaces started before the note and stay silenced.
	for _, w := range suppressedWeek(t) {
		if w.Additive || !w.Active(now) {
			continue
		}
		if !silencedBy(sups, w, now) {
			t.Errorf("seasonal rotation %q survived the note", w.EventID)
		}
	}
}

// TestSuppressionAdvertisesTheRotationAfterItLifts: nothing about a note reaches a
// rotation scheduled entirely after it.
func TestSuppressionAdvertisesTheRotationAfterItLifts(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	after := suppressWindow(t, "after", "5", false, false, "2026-09-10T06:00:00.000", "2026-09-15T22:00:00.000", "Zacian (Hero of Many Battles)")
	_, upcoming, _ := reconcileRaids(json.RawMessage(suppressedUpstream), append(suppressedWeek(t), after),
		[]RaidSuppression{liveSuppression(t)}, now, suppressLookup(t), testCPMs(t))
	if !advertises(upcoming, "after") {
		t.Errorf("did not advertise the rotation that opens after the note lifts: %+v", upcoming)
	}
}

func advertises(upcoming []UpcomingRaid, eventID string) bool {
	for _, u := range upcoming {
		if u.EventID == eventID {
			return true
		}
	}
	return false
}

func TestNextRaidBoundaryIncludesSuppressionEdges(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	windows := suppressedWeek(t)
	sups := []RaidSuppression{liveSuppression(t)}

	// Without the suppression the next edge is the additive window shutting.
	bare := nextRaidBoundary(windows, nil, now)
	if bare.IsZero() {
		t.Fatal("no boundary at all from the windows alone")
	}
	// The suppression lifts on 2026-09-06T09:59Z, so a schedule whose only remaining
	// edge is later must still rebuild then.
	late := []RaidWindow{suppressWindow(t, "regis", "5", false, false, "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000", "Regirock")}
	if got, want := nextRaidBoundary(late, sups, now), utc(t, "2026-09-06T09:59:00Z"); !got.Equal(want) {
		t.Errorf("boundary %s, want the suppression edge %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got := nextRaidBoundary(late, nil, now); got.Equal(utc(t, "2026-09-06T09:59:00Z")) {
		t.Error("found a suppression edge with no suppressions passed")
	}
}

// TestSuppressionDisarmsWhenItWouldEmptyEverything is the circuit breaker: a note
// parsed while the Raids section reader found nothing means one of the two readers
// is broken, and the safe answer is the behaviour that shipped before either.
func TestSuppressionDisarmsWhenItWouldEmptyEverything(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	const seasonStart, seasonEnd = "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000"
	windows := []RaidWindow{
		suppressWindow(t, "regis", "5", false, false, seasonStart, seasonEnd, "Regirock", "Regice", "Registeel"),
		suppressWindow(t, "shadow-giratina", "5", true, false, seasonStart, seasonEnd, "Giratina (Altered)"),
		suppressWindow(t, "mega-gyarados", "6", false, false, seasonStart, seasonEnd, "Mega Gyarados"),
	}
	served, _, stats := reconcileRaids(json.RawMessage(suppressedUpstream), windows, []RaidSuppression{liveSuppression(t)},
		now, suppressLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if !stats.SuppressionDisarmed || stats.Suppressed != 0 {
		t.Fatalf("SuppressionDisarmed %v, Suppressed %d; want true and 0", stats.SuppressionDisarmed, stats.Suppressed)
	}
	if !hasName(tiers["5"], "Shadow Giratina (Altered Forme)") || !hasName(tiers["6"], "Mega Gyarados") {
		t.Errorf("disarmed but still emptied the grid: 5=%v 6=%v", names(tiers["5"]), names(tiers["6"]))
	}
	if !hasName(tiers["5"], "Regirock") {
		t.Errorf("disarmed but did not synthesize as before: %v", names(tiers["5"]))
	}
}

// TestSuppressionLeavesUngovernedTiersAlone: tiers 1 and 3 rotate rarely and no feed
// carries timing for them, so nothing here may touch them.
func TestSuppressionLeavesUngovernedTiersAlone(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	upstream := json.RawMessage(`{"1":[{"pokemon_name":"Dratini"}],"3":[{"pokemon_name":"Passimian"}],"6":[{"pokemon_name":"Mega Victreebel"}]}`)
	windows := []RaidWindow{
		suppressWindow(t, "mega-ascension", "6", false, true, "2026-09-01T00:01:00.000", "2026-09-01T23:59:00.000", "Mega Victreebel"),
	}
	served, _, stats := reconcileRaids(upstream, windows, []RaidSuppression{liveSuppression(t)}, now, suppressLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if stats.SuppressionDisarmed {
		t.Fatal("the circuit breaker fired; this case is meant to keep the suppression armed")
	}
	if got, want := names(tiers["1"]), []string{"Dratini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tier 1 served %v, want %v", got, want)
	}
	if got, want := names(tiers["3"]), []string{"Passimian"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tier 3 served %v, want %v", got, want)
	}
}

// suppressionEvents is the feed as it stood on 2026-09-01, trimmed to the two
// entries that matter: the rotation that would not end, and the ordinary event
// whose page says it has.
const suppressionEvents = `[
	{"eventID":"regirock-regice-registeel-in-5-star-raid-battles-august-2026","name":"Regirock, Regice, and Registeel in 5-star Raid Battles","eventType":"raid-battles","start":"2026-08-26T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Regirock"},{"name":"Regice"},{"name":"Registeel"}]}}},
	{"eventID":"mega-ascension","name":"Mega Ascension","eventType":"event","start":"2026-08-31T10:00:00.000","end":"2026-09-04T23:59:00.000"}
]`

// TestRebuildRaidsLockedAppliesSuppressions is the store wiring end to end: a note
// on a scraped page reaches the reconciler, is memoized with the windows off the
// same page, and moves the rebuild boundary.
//
// It asserts nothing about which bosses are served, because rebuildRaidsLocked reads
// the real clock and the note's span is a week in the past by now. Whether the rule
// fires at an instant is what the pure reconcile tests above are for.
func TestRebuildRaidsLockedAppliesSuppressions(t *testing.T) {
	pk, err := os.ReadFile("fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	ty, err := os.ReadFile("fallback/pokemon_types.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	s := &Store{}
	s.mu.Lock()
	s.applyResult("raids", json.RawMessage(suppressedUpstream))
	s.applyResult("events", json.RawMessage(suppressionEvents))
	s.applyResult("pokemon", json.RawMessage(pk))
	s.applyResult("pokemon_types", json.RawMessage(ty))
	s.eventDetails = map[string]eventDetail{
		"mega-ascension": {HTML: megaAscensionNote, FetchedAt: time.Now()},
	}
	s.rebuildRaidsLocked()
	s.mu.Unlock()

	if !bytes.Equal(s.raidsUpstream, []byte(suppressedUpstream)) {
		t.Error("raidsUpstream was modified; it has to stay byte faithful for the drift check")
	}
	if len(s.raidSuppressions) != 1 {
		t.Fatalf("store holds %d suppressions, want 1", len(s.raidSuppressions))
	}
	if want := []string{"5", "5:shadow", "6"}; !reflect.DeepEqual(s.raidSuppressions[0].Groups, want) {
		t.Errorf("groups %v, want %v", s.raidSuppressions[0].Groups, want)
	}
	// Memoized beside the windows, under the one key that covers both derivations.
	if got := len(s.eventRaidCache["mega-ascension"].suppressions); got != 1 {
		t.Errorf("memo holds %d suppressions for the page, want 1", got)
	}
	if s.raidsBuiltFor.IsZero() {
		t.Error("no next boundary was recorded, so the timer would rebuild immediately")
	}

	// The accessor hands out a copy: an admin handler ranging over it must not be
	// able to reach into the store.
	got := s.RaidSuppressions()
	if len(got) != 1 {
		t.Fatalf("RaidSuppressions returned %d entries", len(got))
	}
	got[0].Groups = nil
	if s.raidSuppressions[0].Groups == nil {
		t.Error("RaidSuppressions shares its backing array with the store")
	}
}
