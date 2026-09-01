package pogodata

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func readRaidFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return d
}

// rosterIndex maps boss name to "tier|day" so an expectation reads as a table.
func rosterIndex(roster []eventPageBoss) map[string]string {
	out := make(map[string]string, len(roster))
	for _, r := range roster {
		key := r.tier
		if r.shadow {
			key += ":shadow"
		}
		d := "whole-event"
		if !r.day.IsZero() {
			d = r.day.Format("2006-01-02")
		}
		out[r.boss.Name] = key + "|" + d
	}
	return out
}

// TestParseEventPageRaidsMegaAscension is the bug this whole file exists for. On
// 2026-08-31 the Mega Ascension event was running, its page named a different Mega
// line up for every day of the week, and none of them were on the raids page:
// the events feed models the event as eventType "event" with no raid data at all,
// and pokemon-go-api's Mega tier still held Mega Gyarados alone.
func TestParseEventPageRaidsMegaAscension(t *testing.T) {
	roster := parseEventPageRaids(
		readRaidFixture(t, "mega_ascension_raids.html"),
		day(t, "2026-08-31"), day(t, "2026-09-04"))

	want := map[string]string{
		"Mega Victreebel": "6|2026-08-31",
		"Mega Dragonite":  "6|2026-08-31",
		"Mega Malamar":    "6|2026-08-31",
		"Mega Falinks":    "6|2026-09-01",
		"Mega Skarmory":   "6|2026-09-02",
		"Mega Starmie":    "6|2026-09-03",
		"Mega Raichu X":   "6|2026-09-04",
		"Mega Raichu Y":   "6|2026-09-04",
		// Under the h2 "Throughout Mega Ascension", which resets the day scope the
		// h3 date headings above it had established. Getting this wrong would pin
		// the Lati twins to Friday, the last dated heading before them.
		"Mega Latias": "6|whole-event",
		"Mega Latios": "6|whole-event",
	}
	got := rosterIndex(roster)
	if len(got) != len(want) {
		t.Fatalf("read %d bosses, want %d: %v", len(got), len(want), got)
	}
	for name, scope := range want {
		if got[name] != scope {
			t.Errorf("%s: scope %q, want %q", name, got[name], scope)
		}
	}
	for _, r := range roster {
		if !r.boss.CanBeShiny {
			t.Errorf("%s: canBeShiny false, but every boss on this page carries a shiny icon", r.boss.Name)
		}
		if r.boss.Image == "" {
			t.Errorf("%s: no image", r.boss.Name)
		}
	}
}

// TestParseEventPageRaidsGoFestHabitats covers the harder page shape: an h2 sets
// the day and the h3 habitat names under it carry no date of their own, so they
// have to inherit it.
//
// It also asserts the FULL roster survives. Every one of these Megas is genuinely
// live on its day, rotating by habitat, so truncating the list would be the same
// defect this reader exists to remove.
func TestParseEventPageRaidsGoFestHabitats(t *testing.T) {
	roster := parseEventPageRaids(
		readRaidFixture(t, "go_fest_finale_raids.html"),
		day(t, "2026-09-05"), day(t, "2026-09-06"))

	if len(roster) != 34 {
		t.Fatalf("read %d bosses, want the whole 34 boss roster", len(roster))
	}
	got := rosterIndex(roster)

	spot := map[string]string{
		// Under the h3 "Five-star Raids", which names a tier but no day.
		"Armored Mewtwo": "5|whole-event",
		// Under bare weekday h3 headings.
		"Mega Mewtwo X": "6|2026-09-05",
		"Mega Mewtwo Y": "6|2026-09-06",
		// Habitat h3 headings inheriting the day from the h2 above them.
		"Mega Beedrill":  "6|2026-09-05", // Verdant Overgrowth, Saturday
		"Mega Manectric": "6|2026-09-05", // Circuit Plaza, Saturday
		"Mega Steelix":   "6|2026-09-06", // Iron Frostworks, Sunday
		"Mega Audino":    "6|2026-09-06", // Prism Promenade, Sunday
	}
	for name, scope := range spot {
		if got[name] != scope {
			t.Errorf("%s: scope %q, want %q", name, got[name], scope)
		}
	}

	sat, sun := 0, 0
	for _, r := range roster {
		if r.day.IsZero() {
			continue
		}
		switch r.day.Format("2006-01-02") {
		case "2026-09-05":
			sat++
		case "2026-09-06":
			sun++
		}
	}
	if sat != 16 || sun != 17 {
		t.Errorf("day split: Saturday %d, Sunday %d, want 16 and 17", sat, sun)
	}
}

// TestParseEventPageRaidsReadsATierOneSection: a tier 1 Raids section is read like
// any other now that governedTiers holds 1 and 3, so a page that dates a 1 star
// rotation reaches the grid with the window it states rather than as an undated
// upstream card.
//
// The markup here is the LEGO event's, minus the two sentences that say where those
// raids happen. Those sentences are the subject of the test below and they are the
// reason the real page contributes nothing.
func TestParseEventPageRaidsReadsATierOneSection(t *testing.T) {
	page := strings.Replace(readRaidFixture(t, "lego_raids.html"),
		"<p>The following Pokémon will appear more frequently in raids at participating LEGO Store locations. "+
			"You may encounter one with a Special Background!</p>"+
			"<p>These raids are local only—Remote Raid Passes cannot be used. "+
			"Trainers can participate in one raid per day per store.</p>",
		"<p>The following Pokémon will appear more frequently in raids.</p>", 1)
	if strings.Contains(page, "participating") {
		t.Fatal("the fixture's location sentences did not match; this test is no longer testing what it says")
	}
	roster := parseEventPageRaids(page, day(t, "2026-08-03"), day(t, "2026-09-30"))
	if len(roster) != 1 {
		t.Fatalf("read %d bosses from the 1-star raid section, want 1: %v", len(roster), rosterIndex(roster))
	}
	if roster[0].tier != "1" || roster[0].shadow {
		t.Errorf("classified as tier %q shadow=%v, want tier 1", roster[0].tier, roster[0].shadow)
	}
	if roster[0].boss.Name != "Pikachu" {
		t.Errorf("read %q, want Pikachu", roster[0].boss.Name)
	}
}

// TestParseEventPageRaidsSkipsALocationLimitedRoster is the LEGO page as it actually
// is. Its raids happen in LEGO stores and bar Remote Raid Passes, so a worldwide
// grid whose promise is "these are the raids you can do" must not carry that
// Pikachu, and it did for a day after tier 1 became governed.
func TestParseEventPageRaidsSkipsALocationLimitedRoster(t *testing.T) {
	roster := parseEventPageRaids(
		readRaidFixture(t, "lego_raids.html"),
		day(t, "2026-08-03"), day(t, "2026-09-30"))
	if len(roster) != 0 {
		t.Errorf("read %d bosses off a store only roster, want none: %v", len(roster), rosterIndex(roster))
	}
}

// TestEventPageLocationLimitedPhrases pins each phrase on its own, and pins the
// near misses that must NOT trip it. The rule declines to add a boss, so a false
// positive costs a whole event's roster.
func TestEventPageLocationLimitedPhrases(t *testing.T) {
	cases := []struct {
		prose   string
		limited bool
	}{
		{"These raids are local only. Remote Raid Passes cannot be used.", true},
		{"Remote Raid Pass cannot be used for these raids.", true},
		{"These raids are local only, so bring a friend.", true},
		{"Pikachu will appear in raids at participating LEGO Store locations.", true},
		{"Raids will be held at participating retail locations.", true},
		// Near misses. Every one of these is ordinary event prose.
		{"Remote Raid Passes can be used as normal during this event.", false},
		{"Pikachu will appear in raids around the world.", false},
		{"Trainers can participate in one raid per day.", false},
		{"You may encounter one with a Special Background!", false},
		// A sentence boundary stops the participating clause reaching across into
		// an unrelated one, which is what the [^.] bound is for.
		{"Tickets are available at participating stores. Raids run in all locations.", false},
	}
	for _, c := range cases {
		page := `<h2 id="raids" class="event-section-header raids">Raids</h2><p>` + c.prose + `</p>` +
			`<h3>Appearing in 1-Star Raids</h3><ul class="pkmn-list-flex">` +
			`<li class="pkmn-list-item"><div class="pkmn-name">Pikachu</div></li></ul>`
		roster := parseEventPageRaids(page, day(t, "2026-08-03"), day(t, "2026-09-30"))
		got := len(roster) == 0
		if got != c.limited {
			t.Errorf("%q: read %d bosses, limited=%v want limited=%v", c.prose, len(roster), got, c.limited)
		}
	}
}

func TestParseEventPageRaidsHandlesPagesWithNothingToRead(t *testing.T) {
	start, end := day(t, "2026-08-31"), day(t, "2026-09-04")
	cases := []struct {
		name string
		html string
	}{
		{"empty", ""},
		{"whitespace", "   \n  "},
		{"no raids section", `<h2 id="spawns" class="event-section-header spawns">Spawns</h2><ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-name">Pikachu</div></li></ul>`},
		{"raids section with no list", `<h2 id="raids" class="event-section-header raids">Raids</h2><p>Details to come.</p>`},
		{"heading shape we do not recognise", `<h2 id="raids" class="event-section-header raids">Raids</h2><h3>Somewhere Else Entirely</h3><ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-name">Registeel</div></li></ul>`},
		{"boss with no name", `<h2 id="raids" class="event-section-header raids">Raids</h2><h3>Appearing in 5-Star Raids</h3><ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-name"></div></li></ul>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseEventPageRaids(tc.html, start, end); len(got) != 0 {
				t.Fatalf("read %d bosses, want none: %v", len(got), rosterIndex(got))
			}
		})
	}
}

// TestParseEventPageRaidsStopsAtTheNextSection makes sure the reader cannot walk
// out of the Raids section and start collecting the Shiny gallery, which lists
// every Mega on the page a second time.
func TestParseEventPageRaidsStopsAtTheNextSection(t *testing.T) {
	html := `<h2 id="raids" class="event-section-header raids">Raids</h2>` +
		`<h3>Monday, August 31</h3><ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-name">Mega Malamar</div></li></ul>` +
		`<h2 id="shiny" class="event-section-header shiny">Shiny</h2>` +
		`<ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-name">Mega Latios</div></li></ul>`
	roster := parseEventPageRaids(html, day(t, "2026-08-31"), day(t, "2026-09-04"))
	if len(roster) != 1 || roster[0].boss.Name != "Mega Malamar" {
		t.Fatalf("got %v, want Mega Malamar alone", rosterIndex(roster))
	}
}

func TestResolveHeadingDay(t *testing.T) {
	start, end := day(t, "2026-08-31"), day(t, "2026-09-04")
	weekend, weekendEnd := day(t, "2026-09-05"), day(t, "2026-09-06")
	cases := []struct {
		name       string
		heading    string
		start, end time.Time
		want       string // "" means it must not resolve
	}{
		{"full date with weekday", "Monday, August 31", start, end, "2026-08-31"},
		{"full date crossing the month", "Friday, September 4", start, end, "2026-09-04"},
		{"bare weekday, unique in span", "Saturday", weekend, weekendEnd, "2026-09-05"},
		{"bare weekday with trailing words", "Sunday Habitat Mega Raids", weekend, weekendEnd, "2026-09-06"},
		// The page writes no year, so a weekday that contradicts the date is the
		// clearest sign the heading was misread. Fail closed rather than pin a
		// roster to the wrong day.
		{"weekday contradicts the date", "Tuesday, August 31", start, end, ""},
		{"date outside the event span", "Monday, August 24", start, end, ""},
		{"date that does not exist", "Sunday, February 30", day(t, "2026-02-01"), day(t, "2026-03-01"), ""},
		// Nothing in the span is a Saturday, so there is no day to pick.
		{"weekday not in span", "Saturday", start, end, ""},
		{"no date at all", "Throughout Mega Ascension", start, end, ""},
		{"habitat name", "Verdant Overgrowth", weekend, weekendEnd, ""},
		{"tier heading", "Five-star Raids", weekend, weekendEnd, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveHeadingDay(tc.heading, tc.start, tc.end)
			if tc.want == "" {
				if ok {
					t.Fatalf("resolved to %s, want no day", got.Format("2006-01-02"))
				}
				return
			}
			if !ok {
				t.Fatalf("did not resolve, want %s", tc.want)
			}
			if got.Format("2006-01-02") != tc.want {
				t.Fatalf("resolved to %s, want %s", got.Format("2006-01-02"), tc.want)
			}
		})
	}
}

// TestResolveHeadingDayNeedsAShortSpan documents why a long event contributes no
// day scoping: a bare weekday is only unambiguous while the event covers it once.
func TestResolveHeadingDayNeedsAShortSpan(t *testing.T) {
	if _, ok := resolveHeadingDay("Saturday", day(t, "2026-08-01"), day(t, "2026-09-30")); ok {
		t.Fatal("a bare weekday resolved inside a two month span, which cannot be unambiguous")
	}
}

func TestHeadingTier(t *testing.T) {
	cases := []struct {
		heading    string
		tier       string
		shadow, ok bool
	}{
		{"Appearing in 1-Star Raids", "1", false, true},
		{"Appearing in 5-Star Shadow Raids", "5", true, true},
		{"Five-star Raids", "5", false, true},
		{"Saturday Habitat Mega Raids", "6", false, true},
		{"Appearing in Shadow Raids", "5", true, true},
		{"Elite Raids", "5", false, true},
		{"Monday, August 31", "", false, false},
		{"Throughout Mega Ascension", "", false, false},
		{"Verdant Overgrowth", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.heading, func(t *testing.T) {
			tier, shadow, ok := headingTier(tc.heading)
			if ok != tc.ok || tier != tc.tier || shadow != tc.shadow {
				t.Fatalf("got (%q, shadow=%v, ok=%v), want (%q, shadow=%v, ok=%v)",
					tier, shadow, ok, tc.tier, tc.shadow, tc.ok)
			}
		})
	}
}

// TestEventPageRaidWindowsShape checks what the reader hands the reconciler: one
// window per tier and day, marked additive, carrying floating wall clock strings
// the browser can render in the viewer's own zone.
func TestEventPageRaidWindowsShape(t *testing.T) {
	e := raidFeedEntry{
		EventID:   "mega-ascension",
		Name:      "Mega Ascension",
		EventType: "event",
		Start:     "2026-08-31T10:00:00.000",
		End:       "2026-09-04T23:59:00.000",
	}
	windows := eventPageRaidWindows(e, readRaidFixture(t, "mega_ascension_raids.html"))
	// Five dated days plus the "throughout" pair.
	if len(windows) != 6 {
		t.Fatalf("built %d windows, want 6", len(windows))
	}
	byStart := map[string]RaidWindow{}
	for _, w := range windows {
		if !w.Additive {
			t.Errorf("%s (%s): not marked additive; an event page must never take a tier over", w.EventID, w.RawStart)
		}
		if w.EventID != "mega-ascension" {
			t.Errorf("event id %q, want the bare id so the client can open the modal", w.EventID)
		}
		if w.Tier != "6" || w.Shadow {
			t.Errorf("%s: tier %q shadow %v, want 6 and not shadow", w.RawStart, w.Tier, w.Shadow)
		}
		byStart[w.RawStart] = w
	}

	monday, ok := byStart["2026-08-31T00:01:00.000"]
	if !ok {
		t.Fatalf("no Monday window, got starts %v", byStart)
	}
	if monday.RawEnd != "2026-08-31T23:59:00.000" {
		t.Errorf("Monday ends %q, want the end of the local day", monday.RawEnd)
	}
	if len(monday.Bosses) != 3 {
		t.Errorf("Monday has %d bosses, want 3", len(monday.Bosses))
	}

	// The whole event window keeps the feed's own strings rather than inventing a
	// day, so its pill matches the event modal exactly.
	throughout, ok := byStart["2026-08-31T10:00:00.000"]
	if !ok {
		t.Fatalf("no whole event window, got starts %v", byStart)
	}
	if throughout.RawEnd != e.End {
		t.Errorf("whole event window ends %q, want the feed's %q", throughout.RawEnd, e.End)
	}
	if len(throughout.Bosses) != 2 {
		t.Errorf("whole event window has %d bosses, want Mega Latias and Mega Latios", len(throughout.Bosses))
	}

	// A day window has to be live for the whole of that day everywhere on Earth,
	// which is the 26 hours between the first zone reaching 00:01 and the last
	// reaching 23:59.
	if got := monday.EndsUTC.Sub(monday.StartsUTC); got < 47*time.Hour || got > 50*time.Hour {
		t.Errorf("Monday spans %s, want roughly 48 hours of union window", got)
	}
}

func TestEventPageRaidWindowsRefusesAnUnparseableSpan(t *testing.T) {
	e := raidFeedEntry{EventID: "mega-ascension", EventType: "event", Start: "not a time", End: "also not"}
	if got := eventPageRaidWindows(e, readRaidFixture(t, "mega_ascension_raids.html")); len(got) != 0 {
		t.Fatalf("built %d windows from an unparseable span, want none", len(got))
	}
}

// megaOnlyLookup answers for the two Megas these reconcile tests use and nothing
// else, so a card being built proves the window reached the synthesizer.
func megaOnlyLookup(name string) (speciesStats, bool) {
	switch normalizeBossName(name) {
	case "mega victreebel":
		return speciesStats{Types: []string{"Grass", "Poison"}, Atk: 207, Def: 135, Sta: 190}, true
	case "mega gyarados":
		return speciesStats{Types: []string{"Water", "Dark"}, Atk: 292, Def: 247, Sta: 216}, true
	}
	return speciesStats{}, false
}

func seasonalMegaWindow(now time.Time) RaidWindow {
	return RaidWindow{
		EventID: "mega-gyarados-in-mega-raids-august-2026", Name: "Mega Gyarados in Mega Raids",
		Tier: "6", Bosses: []WindowBoss{{Name: "Mega Gyarados"}},
		RawStart: "2026-08-26T06:00:00.000", RawEnd: "2026-09-08T22:00:00.000",
		StartsUTC: now.Add(-72 * time.Hour), EndsUTC: now.Add(72 * time.Hour),
	}
}

func eventPageMegaWindow(now time.Time) RaidWindow {
	return RaidWindow{
		EventID: "mega-ascension", Name: "Mega Ascension",
		Tier: "6", Bosses: []WindowBoss{{Name: "Mega Victreebel", CanBeShiny: true}},
		RawStart: "2026-08-31T00:01:00.000", RawEnd: "2026-08-31T23:59:00.000",
		StartsUTC: now.Add(-6 * time.Hour), EndsUTC: now.Add(6 * time.Hour),
		Additive: true,
	}
}

// servedTier pulls one tier's cards out of a reconciled blob. hasName, declared
// alongside the other reconcile tests, does the membership check.
func servedTier(t *testing.T, blob json.RawMessage, tier string) []raidBoss {
	t.Helper()
	var tiers map[string][]raidBoss
	if err := json.Unmarshal(blob, &tiers); err != nil {
		t.Fatalf("served blob parse: %v", err)
	}
	return tiers[tier]
}

func servedNames(t *testing.T, blob json.RawMessage, tier string) []string {
	t.Helper()
	cards := servedTier(t, blob, tier)
	out := make([]string, 0, len(cards))
	for _, b := range cards {
		out = append(out, b.PokemonName)
	}
	return out
}

// TestAdditiveWindowDoesNotDropUpstreamBosses is the second half of the Mega
// Ascension defect, and the half that would have survived a fix to the reader
// alone.
//
// The seasonal Mega rotation is live, so it makes tier 6 authoritative, so any
// tier 6 boss it does not name is dropped as expired. That is correct for a boss
// nothing describes and catastrophic for one an event page describes: on the day
// upstream finally listed the Mega Ascension line up, the reconciler would have
// deleted it again.
func TestAdditiveWindowDoesNotDropUpstreamBosses(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Gyarados"},{"pokemon_name":"Mega Victreebel"}]}`)

	// Without the event page window, the seasonal rotation is the only authority
	// and Mega Victreebel goes.
	_, _, before := reconcileRaids(upstream, []RaidWindow{seasonalMegaWindow(now)}, nil, now, megaOnlyLookup, defaultRaidCPMs)
	if before.Dropped != 1 {
		t.Fatalf("Dropped %d without the event page window, want 1: the drop rule itself must keep working", before.Dropped)
	}

	served, _, after := reconcileRaids(upstream,
		[]RaidWindow{seasonalMegaWindow(now), eventPageMegaWindow(now)}, nil, now, megaOnlyLookup, defaultRaidCPMs)
	if after.Dropped != 0 {
		t.Errorf("Dropped %d with the event page window, want 0", after.Dropped)
	}
	cards := servedTier(t, served, "6")
	for _, want := range []string{"Mega Gyarados", "Mega Victreebel"} {
		if !hasName(cards, want) {
			t.Errorf("%s missing from the served tier 6: %v", want, servedNames(t, served, "6"))
		}
	}
	if after.FromEventPages != 1 || after.EventWindows != 1 {
		t.Errorf("stats: %d event windows supplying %d bosses, want 1 and 1", after.EventWindows, after.FromEventPages)
	}
}

// TestAdditiveWindowNeverTakesOverATier is the rule stated the other way round. An
// event page says what IS running and never what is not, so a tier described only
// by an event page leaves upstream's own list completely alone.
func TestAdditiveWindowNeverTakesOverATier(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Gyarados"},{"pokemon_name":"Mega Aggron"}]}`)
	served, _, stats := reconcileRaids(upstream, []RaidWindow{eventPageMegaWindow(now)}, nil, now, megaOnlyLookup, defaultRaidCPMs)
	if stats.Dropped != 0 {
		t.Errorf("Dropped %d, want 0: an event page must not be able to remove a boss", stats.Dropped)
	}
	cards := servedTier(t, served, "6")
	for _, want := range []string{"Mega Gyarados", "Mega Aggron", "Mega Victreebel"} {
		if !hasName(cards, want) {
			t.Errorf("%s missing from the served tier 6: %v", want, servedNames(t, served, "6"))
		}
	}
}

// TestAdditiveWindowSynthesizesAMissingBoss is the case that was actually live on
// 2026-08-31: upstream had never heard of the boss, and the only description of it
// anywhere was the event page.
func TestAdditiveWindowSynthesizesAMissingBoss(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Gyarados"}]}`)
	served, _, stats := reconcileRaids(upstream,
		[]RaidWindow{seasonalMegaWindow(now), eventPageMegaWindow(now)}, nil, now, megaOnlyLookup, defaultRaidCPMs)
	if stats.Synthesized != 1 || stats.FromEventPages != 1 {
		t.Fatalf("stats: %d synthesized, %d from event pages, want 1 and 1", stats.Synthesized, stats.FromEventPages)
	}
	cards := servedTier(t, served, "6")
	var card *raidBoss
	for i := range cards {
		if cards[i].PokemonName == "Mega Victreebel" {
			card = &cards[i]
		}
	}
	if card == nil {
		t.Fatalf("Mega Victreebel not on the grid: %v", servedNames(t, served, "6"))
	}
	if card.EventID != "mega-ascension" {
		t.Errorf("event id %q, want mega-ascension so the card links to the right modal", card.EventID)
	}
	if card.Source != "events" {
		t.Errorf("source %q, want events", card.Source)
	}
	if card.StartsAt != "2026-08-31T00:01:00.000" || card.EndsAt != "2026-08-31T23:59:00.000" {
		t.Errorf("window %q to %q, want the local day the page scoped it to", card.StartsAt, card.EndsAt)
	}
	if len(card.Types) != 2 || card.CP == 0 || card.CPMax == 0 {
		t.Errorf("card is not fully built: types %v, cp %d to %d", card.Types, card.CP, card.CPMax)
	}
	if !card.CanBeShiny {
		t.Error("shiny flag lost between the page and the card")
	}
}

// TestSeasonalWindowWinsTheLabelOnASharedBoss pins the tie break. A boss named by
// both a seasonal rotation and an event page is not gone when the event ends, so
// the pill should say the longer of the two.
func TestSeasonalWindowWinsTheLabelOnASharedBoss(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	shared := eventPageMegaWindow(now)
	shared.Bosses = []WindowBoss{{Name: "Mega Gyarados"}}
	upstream := json.RawMessage(`{"6":[{"pokemon_name":"Mega Gyarados"}]}`)
	served, _, _ := reconcileRaids(upstream, []RaidWindow{seasonalMegaWindow(now), shared}, nil, now, megaOnlyLookup, defaultRaidCPMs)
	cards := servedTier(t, served, "6")
	if len(cards) != 1 {
		t.Fatalf("tier 6 has %d cards, want 1", len(cards))
	}
	if got := cards[0].EventID; got != "mega-gyarados-in-mega-raids-august-2026" {
		t.Errorf("card labelled %q, want the seasonal rotation that outlasts the event", got)
	}
}

// TestEventPageWindowsLockedSkipsRaidBattlesEvents keeps the reader off pages the
// feed already describes properly, and proves the memo is keyed on something that
// actually changes.
func TestEventPageWindowsLockedSkipsRaidBattlesEvents(t *testing.T) {
	page := readRaidFixture(t, "mega_ascension_raids.html")
	s := &Store{
		events: json.RawMessage(`[
			{"eventID":"mega-ascension","name":"Mega Ascension","eventType":"event",
			 "start":"2026-08-31T10:00:00.000","end":"2026-09-04T23:59:00.000"},
			{"eventID":"already-in-the-feed","name":"Megas in Mega Raids","eventType":"raid-battles",
			 "start":"2026-08-31T06:00:00.000","end":"2026-09-08T22:00:00.000"}
		]`),
		eventDetails: map[string]eventDetail{
			"mega-ascension":      {HTML: page, FetchedAt: time.Now()},
			"already-in-the-feed": {HTML: page, FetchedAt: time.Now()},
		},
	}
	windows, _ := s.eventPageRaidsLocked()
	if len(windows) == 0 {
		t.Fatal("no windows built at all")
	}
	for _, w := range windows {
		if w.EventID != "mega-ascension" {
			t.Fatalf("read a raid-battles event's page (%s); the feed already carries its roster with real timing", w.EventID)
		}
	}
	if len(s.eventRaidCache) != 1 {
		t.Errorf("memoized %d pages, want only the one that was read", len(s.eventRaidCache))
	}

	// A page that leaves the feed leaves the memo with it, or the map grows for
	// the life of the process.
	s.events = json.RawMessage(`[{"eventID":"something-else","eventType":"event","start":"2026-08-31T10:00:00.000","end":"2026-09-04T23:59:00.000"}]`)
	if got, _ := s.eventPageRaidsLocked(); len(got) != 0 {
		t.Errorf("built %d windows for a feed naming no scraped event", len(got))
	}
	if len(s.eventRaidCache) != 0 {
		t.Errorf("memo still holds %d entries after the feed dropped them", len(s.eventRaidCache))
	}
}

// TestEventPageWindowsLockedIsQuietWithNothingToRead covers the cold start: no
// scraped pages on disk means the grid is exactly what it was before this reader
// existed, rather than empty.
func TestEventPageWindowsLockedIsQuietWithNothingToRead(t *testing.T) {
	cases := []*Store{
		{},
		{events: json.RawMessage(`[{"eventID":"x","eventType":"event"}]`)},
		{events: json.RawMessage(`not json`), eventDetails: map[string]eventDetail{"x": {HTML: "<p>hi</p>"}}},
		{events: json.RawMessage(`[{"eventID":"x","eventType":"event","start":"2026-08-31T10:00:00.000","end":"2026-09-04T23:59:00.000"}]`),
			eventDetails: map[string]eventDetail{"x": {HTML: "   "}}},
	}
	for i, s := range cases {
		if got, _ := s.eventPageRaidsLocked(); len(got) != 0 {
			t.Errorf("case %d: built %d windows, want none", i, len(got))
		}
	}
}

// TestFeedWindowWinsAnExactTie covers two rotations describing the same boss over
// the same span, which is Armored Mewtwo on the GO Fest weekend: the event page
// lists it and so does its own raid-battles entry. Nothing is lost either way, but
// the card should carry the rotation's id rather than the event it sits inside.
func TestFeedWindowWinsAnExactTie(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	span := func(w RaidWindow) RaidWindow {
		w.Tier = "5"
		w.Bosses = []WindowBoss{{Name: "Mega Victreebel"}}
		w.StartsUTC, w.EndsUTC = now.Add(-24*time.Hour), now.Add(6*time.Hour)
		return w
	}
	feed := span(RaidWindow{EventID: "armored-mewtwo-in-5-star-raid-battles-september-2026"})
	page := span(RaidWindow{EventID: "pokemon-go-fest-2026-mega-finale", Additive: true})
	upstream := json.RawMessage(`{"5":[{"pokemon_name":"Mega Victreebel"}]}`)

	// Both orderings, because the answer must not depend on which source the
	// rebuild happened to append first.
	for _, windows := range [][]RaidWindow{{feed, page}, {page, feed}} {
		served, _, _ := reconcileRaids(upstream, windows, nil, now, megaOnlyLookup, defaultRaidCPMs)
		cards := servedTier(t, served, "5")
		if len(cards) != 1 {
			t.Fatalf("tier 5 has %d cards, want 1", len(cards))
		}
		if got := cards[0].EventID; got != feed.EventID {
			t.Errorf("card labelled %q, want the feed's %q", got, feed.EventID)
		}
	}
}
