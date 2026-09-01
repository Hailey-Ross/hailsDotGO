package pogodata

import (
	"strings"
	"testing"
)

// Gaps found by auditing what the three raid sources actually say against what the
// app derives from them. Each of these was a boss, or a whole tier, that reached no
// part of the page.

// TestParseEventPageRaidsReadsAProseOnlyBoss: Super Mega Raid Day's Raids section is
// a heading, one sentence and an EMPTY list element, so Mega Staraptor's GO debut
// existed in no feed and no markup this app could read.
func TestParseEventPageRaidsReadsAProseOnlyBoss(t *testing.T) {
	const page = `<div class="page-content"><h2 id="raids" class="event-section-header raids">Raids</h2>` +
		`<h2 id="featured-pokemon">Featured Pokémon</h2>` +
		`<p>Mega Staraptor will make its Pokémon GO debut in Super Mega Raids.</p>` +
		`<ul class="pkmn-list-flex"></ul></div>`

	roster := parseEventPageRaids(page, day(t, "2026-09-19"), day(t, "2026-09-19"))
	if len(roster) != 1 {
		t.Fatalf("read %d bosses, want 1: %v", len(roster), rosterIndex(roster))
	}
	if roster[0].boss.Name != "Mega Staraptor" || roster[0].tier != "6" {
		t.Errorf("read %q as tier %q, want Mega Staraptor at tier 6", roster[0].boss.Name, roster[0].tier)
	}
	if !roster[0].day.IsZero() {
		t.Errorf("scoped to %s; a sentence carries no day, so it belongs to the whole event", roster[0].day)
	}
}

// TestParseEventPageRaidsPrefersMarkupOverProse: the prose reader is a last resort
// and must never add to a page that has a real roster, or every "will make its debut"
// aside on a normal event page becomes a second card.
func TestParseEventPageRaidsPrefersMarkupOverProse(t *testing.T) {
	const page = `<div class="page-content"><h2 class="event-section-header raids">Raids</h2>` +
		`<p>Mega Victreebel will make its debut in Mega Raids.</p>` +
		`<ul><li class="pkmn-list-item"><span class="pkmn-name">Mega Malamar</span></li></ul></div>`

	roster := parseEventPageRaids(page, day(t, "2026-09-19"), day(t, "2026-09-19"))
	if len(roster) != 1 || roster[0].boss.Name != "Mega Malamar" {
		t.Fatalf("read %v, want only the boss that is actually in the list", rosterIndex(roster))
	}
}

// TestParseEventPageRaidsIgnoresProseItCannotTier: only Mega and Primal carry their
// tier in the name, so a sentence about anything else is not a boss this reader can
// place and must be left alone.
func TestParseEventPageRaidsIgnoresProseItCannotTier(t *testing.T) {
	const page = `<div class="page-content"><h2 class="event-section-header raids">Raids</h2>` +
		`<p>Regigigas will make its debut in Elite Raids.</p><ul class="pkmn-list-flex"></ul></div>`

	if got := parseEventPageRaids(page, day(t, "2026-09-19"), day(t, "2026-09-19")); len(got) != 0 {
		t.Errorf("read %v off a sentence with no tier in it", rosterIndex(got))
	}
}

// TestResolveHeadingDaysReadsARange: a heading naming a week used to collapse to its
// first day, because the single date regex matches once and stops.
func TestResolveHeadingDaysReadsARange(t *testing.T) {
	evStart, evEnd := day(t, "2026-09-08"), day(t, "2026-09-15")
	cases := []struct {
		heading    string
		start, end string
	}{
		{"September 8-15", "2026-09-08", "2026-09-15"},
		{"September 8–15", "2026-09-08", "2026-09-15"}, // en dash, which is what upstream writes
		{"September 8 to September 15", "2026-09-08", "2026-09-15"},
		{"September 11-15", "2026-09-11", "2026-09-15"},
		// A single day still answers with a span, so every caller treats the two
		// shapes the same way.
		{"Tuesday, September 8", "2026-09-08", "2026-09-08"},
	}
	for _, c := range cases {
		t.Run(c.heading, func(t *testing.T) {
			start, end, ok := resolveHeadingDays(c.heading, evStart, evEnd)
			if !ok {
				t.Fatalf("refused %q", c.heading)
			}
			if start.Format("2006-01-02") != c.start || end.Format("2006-01-02") != c.end {
				t.Errorf("got %s .. %s, want %s .. %s",
					start.Format("2006-01-02"), end.Format("2006-01-02"), c.start, c.end)
			}
		})
	}
	if _, _, ok := resolveHeadingDays("Verdant Overgrowth", evStart, evEnd); ok {
		t.Error("read a day span out of a habitat name")
	}
}

// TestEventPageRaidWindowsSpansARangeHeading takes the range all the way through to
// the window, which is the thing that actually reaches the grid.
//
// The span below is Mega Squads' real one, ending on the 14th at 20:00, and that is
// the point: the heading names a day the event's own span does NOT contain. The
// fixture used to end on 2026-09-15T23:59, which contained the whole range, so
// resolveHeadingDay closed it on its own and this test passed without ever reaching
// resolveRangeEnd. It would have gone on passing with the fix reverted.
func TestEventPageRaidWindowsSpansARangeHeading(t *testing.T) {
	const page = `<div class="page-content"><h2 class="event-section-header raids">Raids</h2>` +
		`<h2>September 8-15</h2><h3>Mega Raids</h3>` +
		`<ul><li class="pkmn-list-item"><span class="pkmn-name">Mega Beedrill</span></li></ul></div>`

	e := raidFeedEntry{EventID: "mega-squads", Name: "Mega Squads", EventType: "event",
		Start: "2026-09-08T10:00:00.000", End: "2026-09-14T20:00:00.000"}
	windows := eventPageRaidWindows(e, page)
	if len(windows) != 1 {
		t.Fatalf("built %d windows, want 1", len(windows))
	}
	w := windows[0]
	if !strings.HasPrefix(w.RawStart, "2026-09-08") || !strings.HasPrefix(w.RawEnd, "2026-09-15") {
		t.Errorf("window %s .. %s, want it to span the whole heading", w.RawStart, w.RawEnd)
	}
	if !w.Additive {
		t.Error("an event page window has to stay additive")
	}
}

// TestSpeciesLookupReadsAdjectiveForms: the dataset stores these as form labels while
// both feeds write them as a leading adjective. Armored Mewtwo is the one that was
// costing a live tier, and the regional prefixes fail the same way.
func TestSpeciesLookupReadsAdjectiveForms(t *testing.T) {
	lookup := testLookup(t)
	cases := []struct {
		name     string
		atk      int
		types    string
		resolves bool
	}{
		// The only five star raid of the GO Fest Mega Finale weekend.
		{"Armored Mewtwo", 182, "Psychic", true},
		{"Hisuian Sneasel", 189, "Fighting", true},
		{"Galarian Zapdos", 252, "Fighting", true},
		{"Alolan Marowak", 144, "Fire", true},
		// Three Paldean forms and nothing to choose between them, so it refuses
		// rather than putting the wrong typing on a card.
		{"Paldean Tauros", 0, "", false},
		// And the plain species still reads as the Normal form, not as a prefix.
		{"Mewtwo", 300, "Psychic", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, ok := lookup(c.name)
			if ok != c.resolves {
				t.Fatalf("resolved = %v, want %v", ok, c.resolves)
			}
			if !ok {
				return
			}
			if st.Atk != c.atk {
				t.Errorf("attack %d, want %d", st.Atk, c.atk)
			}
			if len(st.Types) == 0 || st.Types[0] != c.types {
				t.Errorf("types %v, want %s first", st.Types, c.types)
			}
		})
	}
}

// TestSpeciesLookupReadsPrimal: Primal rides the Mega tier and lives in the Mega
// table, and was indexed correctly then made unreachable by a "mega " prefix test.
func TestSpeciesLookupReadsPrimal(t *testing.T) {
	megas := map[string]megaForm{
		"primal kyogre": {Name: "Primal Kyogre", Types: []string{"Water"}, Atk: 353, Def: 268, Sta: 218},
	}
	lookup := newSpeciesLookup(nil, nil, megas)
	st, ok := lookup("Primal Kyogre")
	if !ok {
		t.Fatal("Primal Kyogre did not resolve; it is in the Mega table")
	}
	if st.Atk != 353 {
		t.Errorf("attack %d, want 353", st.Atk)
	}
	if _, ok := lookup("Primal Groudon"); ok {
		t.Error("resolved a Primal the table does not carry; a miss must never fall through")
	}
}

// TestClassifyRaidTierReadsWordNumbersAndPrimal: eventraids.go has read "Five-star"
// since it was written, because that is what LeekDuck writes as a heading, while this
// reader knew only digits and dropped the whole rotation.
func TestClassifyRaidTierReadsWordNumbersAndPrimal(t *testing.T) {
	cases := []struct {
		slug   string
		tier   string
		shadow bool
	}{
		{"deoxys-in-five-star-raid-battles-2026", "5", false},
		{"primal-kyogre-in-primal-raids-2026", "6", false},
		{"regirock-regice-registeel-in-5-star-raid-battles-august-2026", "5", false},
		{"shadow-giratina-altered-in-shadow-raids-august-2026", "5", true},
		{"mega-gyarados-in-mega-raids-august-2026", "6", false},
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			tier, shadow, ok := classifyRaidTier(c.slug, "")
			if !ok {
				t.Fatalf("did not classify %q", c.slug)
			}
			if tier != c.tier || shadow != c.shadow {
				t.Errorf("tier %q shadow %v, want %q and %v", tier, shadow, c.tier, c.shadow)
			}
		})
	}
}

// TestExtractEventBodyReportsARename: LeekDuck renames a page and the feed keeps the
// old slug, which then serves a content-free redirect stub with a 200. The scrape
// errors, the previous copy is kept, and every retry twelve hours later fetches the
// same stub, so it can never heal on its own.
func TestExtractEventBodyReportsARename(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"canonical", `<html><head><link rel="canonical" href="https://leekduck.com/events/new-slug/"></head><body></body></html>`,
			"https://leekduck.com/events/new-slug/"},
		{"meta refresh", `<html><head><meta http-equiv="refresh" content="0; url=/events/new-slug/"></head><body></body></html>`,
			"/events/new-slug/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := extractEventBody(strings.NewReader(c.html))
			var moved eventPageMoved
			if !asMoved(err, &moved) {
				t.Fatalf("error %v, want an eventPageMoved", err)
			}
			if moved.to != c.want {
				t.Errorf("target %q, want %q", moved.to, c.want)
			}
		})
	}

	// A page with real content is never treated as a rename, whatever it declares.
	body, err := extractEventBody(strings.NewReader(
		`<html><head><link rel="canonical" href="https://leekduck.com/events/other/"></head>` +
			`<body><div class="page-content"><p>Real content here.</p></div></body></html>`))
	if err != nil {
		t.Fatalf("a page with content errored: %v", err)
	}
	if !strings.Contains(body, "Real content here.") {
		t.Errorf("body %q lost its content", body)
	}
}

func asMoved(err error, target *eventPageMoved) bool {
	m, ok := err.(eventPageMoved)
	if ok {
		*target = m
	}
	return ok
}
