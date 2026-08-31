package pogodata

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// utc parses a test instant. Written out rather than using time.Date so the
// expectations below read as the same kind of string the feed itself sends.
func utc(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test timestamp %q: %v", s, err)
	}
	return v
}

// testLookup builds a species lookup over the embedded fallback data, which is
// committed and always present, so these tests need neither a network nor a
// populated cache directory.
func testLookup(t *testing.T) speciesLookup {
	t.Helper()
	read := func(name string) json.RawMessage {
		b, err := os.ReadFile("fallback/" + name)
		if err != nil {
			t.Fatalf("read fallback/%s: %v", name, err)
		}
		return json.RawMessage(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}))
	}
	return newSpeciesLookup(read("pokemon.json"), read("pokemon_types.json"), testMegas)
}

// testMegas is the trimmed Mega data the store holds after a megas refresh, using
// the real upstream numbers for Mega Gyarados. Note the stamina matches plain
// Gyarados while the attack, defense and typing all differ, which is exactly why a
// Mega cannot be derived from its base species.
var testMegas = map[string]megaForm{
	"mega gyarados": {Name: "Mega Gyarados", Types: []string{"Water", "Dark"}, Atk: 292, Def: 247, Sta: 216},
}

func testCPMs(t *testing.T) raidCPMs {
	t.Helper()
	b, err := os.ReadFile("fallback/cp_multipliers.json")
	if err != nil {
		t.Fatalf("read fallback/cp_multipliers.json: %v", err)
	}
	return raidCPMsFrom(json.RawMessage(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})))
}

func TestClassifyRaidTier(t *testing.T) {
	// Every raid rotation slug in the live feed on 2026-08-27, plus the shapes that
	// have to keep being refused.
	cases := []struct {
		eventID    string
		name       string
		wantTier   string
		wantShadow bool
		wantOK     bool
	}{
		{"lunala-in-5-star-raid-battles-august-2026", "Lunala in 5-star Raid Battles", "5", false, true},
		{"regirock-regice-registeel-in-5-star-raid-battles-august-2026", "Regirock, Regice, and Registeel in 5-star Raid Battles", "5", false, true},
		{"zacian-hero-of-many-battles-in-5-star-raid-battles-september-2026", "Zacian (Hero of Many Battles) in 5-star Raid Battles", "5", false, true},
		{"xurkitree-pheromosa-buzzwole-in-5-star-raid-battles-september-2026", "Xurkitree, Pheromosa, and Buzzwole in 5-star Raid Battles", "5", false, true},
		{"xerneas-in-5-star-raid-battles-september-2026", "Xerneas in 5-star Raid Battles", "5", false, true},
		{"mega-swampert-in-mega-raids-august-2026", "Mega Swampert in Mega Raids", "6", false, true},
		{"mega-gyarados-in-mega-raids-august-2026", "Mega Gyarados in Mega Raids", "6", false, true},
		{"mega-beedrill-in-mega-raids-september-2026", "Mega Beedrill in Mega Raids", "6", false, true},
		{"shadow-giratina-altered-in-shadow-raids-august-2026", "Shadow Giratina (Altered Forme) in Shadow Raids", "5", true, true},
		{"shadow-thundurus-incarnate-forme-in-shadow-raids-september-2026", "Shadow Thundurus (Incarnate Forme) in Shadow Raids", "5", true, true},
		{"some-elite-raid-day-2026", "Elite Raids", "5", false, true},
		// Not rotations. These describe an hour spent on a boss that is already
		// live, so classifying one would invent a window an hour wide.
		{"raidhour20260826", "Regirock, Regice, and Registeel Raid Hour", "", false, false},
		{"pokemonspotlighthour2026-08-27", "Mankey Spotlight Hour", "", false, false},
		{"season-24-twilight-trails", "Twilight Trails", "", false, false},
	}
	for _, c := range cases {
		tier, shadow, ok := classifyRaidTier(c.eventID, c.name)
		if ok != c.wantOK || tier != c.wantTier || shadow != c.wantShadow {
			t.Errorf("classifyRaidTier(%q) = (%q, %v, %v), want (%q, %v, %v)",
				c.eventID, tier, shadow, ok, c.wantTier, c.wantShadow, c.wantOK)
		}
	}
}

func TestNormalizeBossNameJoinsTheTwoFeeds(t *testing.T) {
	// The left column is how the events feed spells the boss, the right column is
	// how the raid feed spells the same boss. They have to land on one key.
	pairs := [][2]string{
		{"Giratina (Altered)", "Shadow Giratina (Altered Forme)"},
		{"Thundurus (Incarnate)", "Shadow Thundurus (Incarnate Forme)"},
		{"Zacian (Hero of Many Battles)", "Zacian (Hero of Many Battles)"},
		{"Regirock", "Regirock"},
		{"Mega Gyarados", "Mega Gyarados"},
		{"  Xerneas  ", "Xerneas"},
	}
	for _, p := range pairs {
		if got, want := normalizeBossName(p[0]), normalizeBossName(p[1]); got != want {
			t.Errorf("normalizeBossName(%q) = %q, normalizeBossName(%q) = %q, want equal", p[0], got, p[1], want)
		}
	}
	// The shadow flag must NOT come from the name, because only one of the two
	// feeds puts it there.
	if !isShadowName("Shadow Giratina (Altered Forme)") {
		t.Error("isShadowName missed a shadow boss")
	}
	if isShadowName("Giratina (Altered)") {
		t.Error("isShadowName claimed a plain boss was shadow")
	}
	if bossKey("Giratina (Altered)", true) != bossKey("Shadow Giratina (Altered Forme)", true) {
		t.Error("bossKey failed to join the shadow spellings")
	}
	if bossKey("Giratina (Altered)", true) == bossKey("Giratina (Altered)", false) {
		t.Error("bossKey collapsed the shadow and plain variants onto one key")
	}
}

func TestRaidWindowSpanCoversEveryZone(t *testing.T) {
	// A floating rotation: 06:00 on the 26th through 22:00 on the 8th, which is a
	// different instant in every zone on Earth.
	start, end, ok := raidWindowSpan("2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000")
	if !ok {
		t.Fatal("floating window refused")
	}
	// Starts when 06:00 first arrives anywhere, in UTC+14.
	if want := utc(t, "2026-08-25T16:00:00Z"); !start.Equal(want) {
		t.Errorf("start = %s, want %s (06:00 in UTC+14)", start.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	// Ends when 22:00 finally arrives in the last zone, UTC-12.
	if want := utc(t, "2026-09-09T10:00:00Z"); !end.Equal(want) {
		t.Errorf("end = %s, want %s (22:00 in UTC-12)", end.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	w := RaidWindow{StartsUTC: start, EndsUTC: end}
	for _, c := range []struct {
		when string
		want bool
	}{
		{"2026-08-25T15:59:59Z", false}, // not yet, even in Kiritimati
		{"2026-08-25T16:00:00Z", true},  // the instant it opens for the first trainer
		{"2026-09-09T09:59:59Z", true},  // still live for the last one
		{"2026-09-09T10:00:00Z", false}, // over everywhere
	} {
		if got := w.Active(utc(t, c.when)); got != c.want {
			t.Errorf("Active(%s) = %v, want %v", c.when, got, c.want)
		}
	}

	// A Z timestamp is a real instant, so both readings collapse onto it and the
	// window is not stretched by 26 hours.
	zStart, zEnd, ok := raidWindowSpan("2026-08-26T06:00:00.000Z", "2026-08-26T22:00:00.000Z")
	if !ok {
		t.Fatal("absolute window refused")
	}
	if !zStart.Equal(utc(t, "2026-08-26T06:00:00Z")) || !zEnd.Equal(utc(t, "2026-08-26T22:00:00Z")) {
		t.Errorf("absolute window was shifted: %s to %s", zStart.Format(time.RFC3339), zEnd.Format(time.RFC3339))
	}

	if _, _, ok := raidWindowSpan("not a time", "2026-09-08T22:00:00.000"); ok {
		t.Error("accepted an unparseable start")
	}
	if _, _, ok := raidWindowSpan("2026-09-08T22:00:00.000", "2026-08-26T06:00:00.000"); ok {
		t.Error("accepted a window that ends before it starts")
	}
}

// changeoverEvents is the real 2026-08-25 to 08-26 rotation swap, taken from the
// live events cache.
const changeoverEvents = `[
{"eventID":"lunala-in-5-star-raid-battles-august-2026","name":"Lunala in 5-star Raid Battles","eventType":"raid-battles","start":"2026-08-19T06:00:00.000","end":"2026-08-25T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Lunala","image":"https://cdn.leekduck.com/l.png","canBeShiny":true}]}}},
{"eventID":"mega-swampert-in-mega-raids-august-2026","name":"Mega Swampert in Mega Raids","eventType":"raid-battles","start":"2026-08-19T06:00:00.000","end":"2026-08-25T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Mega Swampert","image":"https://cdn.leekduck.com/s.png","canBeShiny":true}]}}},
{"eventID":"shadow-giratina-altered-in-shadow-raids-august-2026","name":"Shadow Giratina (Altered Forme) in Shadow Raids","eventType":"raid-battles","start":"2026-08-05T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Giratina (Altered)","image":"https://cdn.leekduck.com/g.png","canBeShiny":true}]}}},
{"eventID":"mega-gyarados-in-mega-raids-august-2026","name":"Mega Gyarados in Mega Raids","eventType":"raid-battles","start":"2026-08-26T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Mega Gyarados","image":"https://cdn.leekduck.com/mg.png","canBeShiny":true}]}}},
{"eventID":"regirock-regice-registeel-in-5-star-raid-battles-august-2026","name":"Regirock, Regice, and Registeel in 5-star Raid Battles","eventType":"raid-battles","start":"2026-08-26T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Regirock","image":"https://cdn.leekduck.com/r1.png","canBeShiny":true},{"name":"Regice","image":"https://cdn.leekduck.com/r2.png","canBeShiny":true},{"name":"Registeel","image":"https://cdn.leekduck.com/r3.png","canBeShiny":true}]}}},
{"eventID":"raidhour20260826","name":"Regirock, Regice, and Registeel Raid Hour","eventType":"raid-hour","start":"2026-08-26T18:00:00.000","end":"2026-08-26T19:00:00.000","extraData":{"generic":{"hasSpawns":false}}},
{"eventID":"pokemonspotlighthour2026-08-27","name":"Mankey Spotlight Hour","eventType":"pokemon-spotlight-hour","start":"2026-08-27T18:00:00.000","end":"2026-08-27T19:00:00.000","extraData":{"spotlight":{"name":"Mankey"}}}
]`

// staleUpstream is what pokemon-go-api was still serving on 2026-08-27, two days
// after Lunala and Mega Swampert ended.
const staleUpstream = `{
"1":[{"pokemon_name":"Pikachu","cp":493,"cp_max":536,"cp_boosted_min":616,"cp_boosted_max":670,"types":["Electric"],"can_be_shiny":true}],
"3":[{"pokemon_name":"Shadow Snorlax","cp":1000,"types":["Normal"]}],
"5":[{"pokemon_name":"Lunala","cp":2219,"cp_max":2310,"cp_boosted_min":2774,"cp_boosted_max":2887,"types":["Psychic","Ghost"]},
     {"pokemon_name":"Shadow Giratina (Altered Forme)","cp":1848,"cp_max":1931,"types":["Ghost","Dragon"]}],
"6":[{"pokemon_name":"Mega Swampert","cp":1622,"cp_max":1699,"types":["Water","Ground"]}]
}`

func decodeTiers(t *testing.T, blob json.RawMessage) map[string][]raidBoss {
	t.Helper()
	var m map[string][]raidBoss
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("decode served raids: %v", err)
	}
	return m
}

func names(bosses []raidBoss) []string {
	out := make([]string, 0, len(bosses))
	for _, b := range bosses {
		out = append(out, b.PokemonName)
	}
	return out
}

func hasName(bosses []raidBoss, want string) bool {
	for _, b := range bosses {
		if b.PokemonName == want {
			return true
		}
	}
	return false
}

func TestReconcileDropsExpiredAndAddsStarted(t *testing.T) {
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	if len(windows) != 5 {
		t.Fatalf("got %d rotations, want 5 (the raid hour and spotlight hour must not count)", len(windows))
	}
	now := utc(t, "2026-08-27T12:00:00Z")
	served, upcoming, stats := reconcileRaids(json.RawMessage(staleUpstream), windows, now, testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	// The bug: both of these ended on the 25th and upstream still lists them.
	if hasName(tiers["5"], "Lunala") {
		t.Errorf("Lunala survived its window: tier 5 = %v", names(tiers["5"]))
	}
	if hasName(tiers["6"], "Mega Swampert") {
		t.Errorf("Mega Swampert survived its window: tier 6 = %v", names(tiers["6"]))
	}
	// Still running until 2026-09-08, and upstream does list it.
	if !hasName(tiers["5"], "Shadow Giratina (Altered Forme)") {
		t.Errorf("Shadow Giratina was dropped while still live: tier 5 = %v", names(tiers["5"]))
	}
	// Live since the 26th, and upstream has no idea.
	for _, want := range []string{"Regirock", "Regice", "Registeel"} {
		if !hasName(tiers["5"], want) {
			t.Errorf("%s is live but was not added: tier 5 = %v", want, names(tiers["5"]))
		}
	}
	// Mega Gyarados started on the 26th and upstream still has not listed it, so it
	// is built from the Mega data instead of leaving the tier empty.
	if !hasName(tiers["6"], "Mega Gyarados") {
		t.Errorf("Mega Gyarados is live but was not added: tier 6 = %v", names(tiers["6"]))
	}
	for _, b := range tiers["6"] {
		if b.PokemonName != "Mega Gyarados" {
			continue
		}
		// Its OWN typing, not Gyarados's Water and Flying: the counter table ranks
		// against these, so borrowing the base species would rank the wrong things.
		if len(b.Types) != 2 || b.Types[0] != "Water" || b.Types[1] != "Dark" {
			t.Errorf("Mega Gyarados types = %v, want [Water Dark]", b.Types)
		}
	}
	// Nothing is left waiting, so the up next strip carries no live placeholder.
	for _, u := range upcoming {
		if u.Live {
			t.Errorf("up next still holds a live placeholder: %+v", u)
		}
	}

	// Tiers nothing schedules are upstream's business alone.
	if got := names(tiers["1"]); len(got) != 1 || got[0] != "Pikachu" {
		t.Errorf("tier 1 = %v, want it untouched", got)
	}
	if got := names(tiers["3"]); len(got) != 1 || got[0] != "Shadow Snorlax" {
		t.Errorf("tier 3 = %v, want it untouched", got)
	}

	if stats.Dropped != 2 || stats.Synthesized != 4 || stats.Pending != 0 {
		t.Errorf("stats = %+v, want 2 dropped, 4 synthesized, 0 pending", stats)
	}
}

func TestReconcileAnnotatesWithTheFeedsOwnStrings(t *testing.T) {
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	now := utc(t, "2026-08-27T12:00:00Z")
	served, _, _ := reconcileRaids(json.RawMessage(staleUpstream), windows, now, testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	for _, b := range tiers["5"] {
		if b.PokemonName != "Shadow Giratina (Altered Forme)" {
			continue
		}
		if b.EventID != "shadow-giratina-altered-in-shadow-raids-august-2026" {
			t.Errorf("event_id = %q", b.EventID)
		}
		// Floating, verbatim, no zone bolted on: the browser reads these as local.
		if b.StartsAt != "2026-08-05T06:00:00.000" || b.EndsAt != "2026-09-08T22:00:00.000" {
			t.Errorf("window strings = %q to %q, want the feed's own", b.StartsAt, b.EndsAt)
		}
		if b.Source != "" {
			t.Errorf("source = %q, want empty on a boss upstream supplied", b.Source)
		}
	}
	// Tier 1 is ungoverned, so it must carry no schedule at all.
	for _, b := range tiers["1"] {
		if b.EventID != "" || b.StartsAt != "" || b.EndsAt != "" {
			t.Errorf("tier 1 boss %q was annotated: %+v", b.PokemonName, b)
		}
	}
}

func TestReconcileKeepsBothSidesOfTheChangeover(t *testing.T) {
	// The 26 hour overlap is the point. Between the moment the new rotation opens
	// in UTC+14 and the moment the old one shuts in UTC-12, both are genuinely
	// live for somebody and both have to be listed.
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	now := utc(t, "2026-08-26T00:00:00Z") // Regi open since 08-25T16:00Z, Lunala shuts at 08-26T10:00Z
	served, _, _ := reconcileRaids(json.RawMessage(staleUpstream), windows, now, testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if !hasName(tiers["5"], "Lunala") {
		t.Errorf("Lunala was dropped while still live in the western zones: %v", names(tiers["5"]))
	}
	if !hasName(tiers["5"], "Regirock") {
		t.Errorf("Regirock was withheld though it had already started in the eastern zones: %v", names(tiers["5"]))
	}
}

func TestReconcileFailsOpen(t *testing.T) {
	// Whatever is wrong with the events feed, the answer is never an empty raids
	// page. Serving today's behaviour is the floor.
	for _, c := range []struct {
		name   string
		events string
	}{
		{"empty", ``},
		{"empty array", `[]`},
		{"not json", `{"nope"`},
		{"an object, not the feed", `{"events":[]}`},
		{"records with no usable field", `[{},{},{}]`},
		{"rotations with unreadable windows", `[{"eventID":"x-in-5-star-raid-battles","eventType":"raid-battles","start":"whenever","end":"later","extraData":{"raidbattles":{"bosses":[{"name":"Lunala"}]}}}]`},
	} {
		windows := parseRaidWindows(json.RawMessage(c.events))
		served, upcoming, stats := reconcileRaids(json.RawMessage(staleUpstream), windows, utc(t, "2026-08-27T12:00:00Z"), testLookup(t), testCPMs(t))
		if !bytes.Equal(served, []byte(staleUpstream)) {
			t.Errorf("%s: served blob was rewritten, want the upstream bytes verbatim", c.name)
		}
		if len(upcoming) != 0 || stats.Dropped != 0 || stats.Synthesized != 0 {
			t.Errorf("%s: acted on an unusable feed: %+v", c.name, stats)
		}
	}

	// An unreadable upstream blob is the other direction of the same rule.
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	broken := json.RawMessage(`{"5": "not a list"}`)
	served, _, _ := reconcileRaids(broken, windows, utc(t, "2026-08-27T12:00:00Z"), testLookup(t), testCPMs(t))
	if !bytes.Equal(served, broken) {
		t.Error("a broken upstream blob was not passed through untouched")
	}
}

func TestReconcileLeavesAGroupWithNothingScheduledAlone(t *testing.T) {
	// With no rotation running for a group, the schedule has nothing to say about it
	// and upstream is the only source. This is what keeps an Elite Raid, or a gap in
	// the feed, from silently emptying a tier.
	upstream := json.RawMessage(`{"5":[{"pokemon_name":"Regigigas","cp":2000,"types":["Normal"]}]}`)
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	// Long after every rotation in the fixture has finished.
	served, _, stats := reconcileRaids(upstream, windows, utc(t, "2027-01-01T00:00:00Z"), testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)
	if !hasName(tiers["5"], "Regigigas") {
		t.Errorf("a boss was dropped with nothing scheduled to drop it: %v", names(tiers["5"]))
	}
	if stats.Dropped != 0 {
		t.Errorf("dropped %d, want 0", stats.Dropped)
	}
}

func TestReconcileDropsAStaleBossWhoseRotationLeftTheFeed(t *testing.T) {
	// The regression that put Lunala and Mega Swampert back on the live page on
	// 2026-08-27.
	//
	// The drop rule used to require finding an expired window that named the boss.
	// The events feed prunes a rotation a day or so after it ends, so once that
	// happened the boss was described by nothing, was read as "upstream knows best",
	// and came straight back, with upstream still serving it a week later. What
	// decides now is whether the schedule has an ACTIVE rotation for that group.
	//
	// Note this fixture contains no Lunala or Mega Swampert event at all, which is
	// exactly the state the feed reaches.
	events := json.RawMessage(`[
	{"eventID":"shadow-giratina-altered-in-shadow-raids-august-2026","name":"Shadow Giratina (Altered Forme) in Shadow Raids","eventType":"raid-battles","start":"2026-08-05T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Giratina (Altered)"}]}}},
	{"eventID":"mega-gyarados-in-mega-raids-august-2026","name":"Mega Gyarados in Mega Raids","eventType":"raid-battles","start":"2026-08-26T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Mega Gyarados"}]}}},
	{"eventID":"regirock-regice-registeel-in-5-star-raid-battles-august-2026","name":"Regirock, Regice, and Registeel in 5-star Raid Battles","eventType":"raid-battles","start":"2026-08-26T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Regirock"},{"name":"Regice"},{"name":"Registeel"}]}}}
	]`)
	windows := parseRaidWindows(events)
	served, _, stats := reconcileRaids(json.RawMessage(staleUpstream), windows,
		utc(t, "2026-08-31T12:00:00Z"), testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if hasName(tiers["5"], "Lunala") {
		t.Errorf("Lunala came back once its rotation left the feed: %v", names(tiers["5"]))
	}
	if hasName(tiers["6"], "Mega Swampert") {
		t.Errorf("Mega Swampert came back once its rotation left the feed: %v", names(tiers["6"]))
	}
	// The shadow half of tier 5 is scheduled separately and is still running.
	if !hasName(tiers["5"], "Shadow Giratina (Altered Forme)") {
		t.Errorf("the live shadow boss was dropped with it: %v", names(tiers["5"]))
	}
	if !hasName(tiers["6"], "Mega Gyarados") || !hasName(tiers["5"], "Registeel") {
		t.Errorf("the live rotations were not built: 5=%v 6=%v", names(tiers["5"]), names(tiers["6"]))
	}
	if stats.Dropped != 2 {
		t.Errorf("dropped %d, want 2", stats.Dropped)
	}
	// Tiers the schedule never governs are untouched by any of this.
	if len(tiers["1"]) != 1 || len(tiers["3"]) != 1 {
		t.Errorf("an ungoverned tier changed: 1=%v 3=%v", names(tiers["1"]), names(tiers["3"]))
	}
}

func TestReconcileGovernsShadowAndNormalSeparately(t *testing.T) {
	// Tier 5 shadow and tier 5 normal are scheduled independently. A live shadow
	// rotation must not make the schedule the authority on the normal half, or a
	// legitimate 5 star boss the feed happens not to describe would be wiped.
	events := json.RawMessage(`[
	{"eventID":"shadow-giratina-altered-in-shadow-raids-august-2026","name":"Shadow Giratina (Altered Forme) in Shadow Raids","eventType":"raid-battles","start":"2026-08-05T06:00:00.000","end":"2026-09-08T22:00:00.000","extraData":{"raidbattles":{"bosses":[{"name":"Giratina (Altered)"}]}}}
	]`)
	windows := parseRaidWindows(events)
	served, _, stats := reconcileRaids(json.RawMessage(staleUpstream), windows,
		utc(t, "2026-08-31T12:00:00Z"), testLookup(t), testCPMs(t))
	tiers := decodeTiers(t, served)

	if !hasName(tiers["5"], "Lunala") {
		t.Errorf("the normal half of tier 5 was wiped by a shadow rotation: %v", names(tiers["5"]))
	}
	if !hasName(tiers["6"], "Mega Swampert") {
		t.Errorf("tier 6 was wiped with nothing scheduled for it: %v", names(tiers["6"]))
	}
	if stats.Dropped != 0 {
		t.Errorf("dropped %d, want 0", stats.Dropped)
	}
}

func TestSynthesizedCPMatchesUpstream(t *testing.T) {
	// The whole case for building a card locally rests on the numbers coming out
	// identical to the ones upstream would have sent. These are the live values
	// from cache/raids.json on 2026-08-27.
	lookup, cpms := testLookup(t), testCPMs(t)
	cases := []struct {
		name                       string
		cp, cpMax, boostMin, boost int
		types                      []string
	}{
		{"Lunala", 2219, 2310, 2774, 2887, []string{"Psychic", "Ghost"}},
		{"Pikachu", 493, 536, 616, 670, []string{"Electric"}},
		{"Impidimp", 421, 461, 526, 576, []string{"Dark", "Fairy"}},
	}
	for _, c := range cases {
		rb, ok := synthesizeBoss(WindowBoss{Name: c.name}, RaidWindow{Tier: "5"}, lookup, cpms)
		if !ok {
			t.Errorf("%s: could not be synthesized", c.name)
			continue
		}
		if rb.CP != c.cp || rb.CPMax != c.cpMax || rb.CPBoostedMin != c.boostMin || rb.CPBoostedMax != c.boost {
			t.Errorf("%s: CP %d-%d boosted %d-%d, want %d-%d and %d-%d",
				c.name, rb.CP, rb.CPMax, rb.CPBoostedMin, rb.CPBoostedMax, c.cp, c.cpMax, c.boostMin, c.boost)
		}
		if len(rb.Types) != len(c.types) {
			t.Errorf("%s: types %v, want %v", c.name, rb.Types, c.types)
			continue
		}
		for i := range c.types {
			if rb.Types[i] != c.types[i] {
				t.Errorf("%s: types %v, want %v", c.name, rb.Types, c.types)
				break
			}
		}
	}
}

func TestSynthesizeResolvesFormsAcrossTheFeeds(t *testing.T) {
	lookup, cpms := testLookup(t), testCPMs(t)

	// The events feed labels forms its own way. "Hero of Many Battles" is form
	// "Hero" in the dataset, and picking the wrong one would hand back Crowned
	// Sword's stats.
	zacian, ok := synthesizeBoss(WindowBoss{Name: "Zacian (Hero of Many Battles)"}, RaidWindow{Tier: "5"}, lookup, cpms)
	if !ok {
		t.Fatal("Zacian (Hero of Many Battles) did not resolve")
	}
	crowned, _ := synthesizeBoss(WindowBoss{Name: "Zacian (Crowned Sword)"}, RaidWindow{Tier: "5"}, lookup, cpms)
	if zacian.CP == crowned.CP {
		t.Error("Hero and Crowned Sword resolved to the same stat line")
	}

	// A shadow rotation names the plain species; the served card has to carry the
	// prefix, because currentBossTiers and the raid finder boss picker match the
	// display name exactly.
	giratina, ok := synthesizeBoss(
		WindowBoss{Name: "Giratina (Altered)"},
		RaidWindow{Tier: "5", Shadow: true},
		lookup, cpms)
	if !ok {
		t.Fatal("Giratina (Altered) did not resolve")
	}
	if giratina.PokemonName != "Shadow Giratina (Altered)" {
		t.Errorf("shadow name = %q, want the Shadow prefix", giratina.PokemonName)
	}
	origin, _ := synthesizeBoss(WindowBoss{Name: "Giratina (Origin)"}, RaidWindow{Tier: "5"}, lookup, cpms)
	if giratina.CP == origin.CP {
		t.Error("Altered and Origin resolved to the same stat line")
	}

	// A Mega comes from its own dataset. These are the published figures for Mega
	// Gyarados, and matching them is the whole argument for building a card locally.
	mega, ok := synthesizeBoss(WindowBoss{Name: "Mega Gyarados"}, RaidWindow{Tier: "6"}, lookup, cpms)
	if !ok {
		t.Fatal("Mega Gyarados did not resolve")
	}
	if mega.CP != 2597 || mega.CPMax != 2695 || mega.CPBoostedMin != 3247 || mega.CPBoostedMax != 3369 {
		t.Errorf("Mega Gyarados CP %d-%d boosted %d-%d, want 2597-2695 and 3247-3369",
			mega.CP, mega.CPMax, mega.CPBoostedMin, mega.CPBoostedMax)
	}
	// Water and Dark, never the base species' Water and Flying.
	if len(mega.Types) != 2 || mega.Types[0] != "Water" || mega.Types[1] != "Dark" {
		t.Errorf("Mega Gyarados types = %v, want [Water Dark]", mega.Types)
	}

	// A Mega with no data must produce NO card. Falling through to the base species
	// would be worse than refusing, because the typing would be wrong.
	if _, ok := synthesizeBoss(WindowBoss{Name: "Mega Rayquaza"}, RaidWindow{Tier: "6"}, lookup, cpms); ok {
		t.Error("a Mega with no data borrowed something to render anyway")
	}
	if _, ok := synthesizeBoss(WindowBoss{Name: "Notapokemon"}, RaidWindow{Tier: "5"}, lookup, cpms); ok {
		t.Error("an unknown species was synthesized")
	}
}

func TestNextRaidBoundary(t *testing.T) {
	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	// From this moment the next thing that can change the answer is Lunala and
	// Mega Swampert shutting at 22:00 in UTC-12 on the 25th, which is 10:00Z on
	// the 26th.
	now := utc(t, "2026-08-26T00:00:00Z")
	if got, want := nextRaidBoundary(windows, now), utc(t, "2026-08-26T10:00:00Z"); !got.Equal(want) {
		t.Errorf("nextRaidBoundary = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	// Past every window, there is nothing left to wait for.
	if got := nextRaidBoundary(windows, utc(t, "2027-01-01T00:00:00Z")); !got.IsZero() {
		t.Errorf("nextRaidBoundary = %s, want the zero time", got.Format(time.RFC3339))
	}
}

func TestRebuildRaidsLockedIsTheOnlyWriterOfServedRaids(t *testing.T) {
	// The store wiring, end to end, on a bare Store: upstream in, schedule in,
	// reconciled blob out, with the pristine copy left alone.
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
	s.applyResult("raids", json.RawMessage(staleUpstream))
	s.applyResult("events", json.RawMessage(changeoverEvents))
	s.applyResult("pokemon", json.RawMessage(pk))
	s.applyResult("pokemon_types", json.RawMessage(ty))
	s.rebuildRaidsLocked()
	s.mu.Unlock()

	if !bytes.Equal(s.raidsUpstream, []byte(staleUpstream)) {
		t.Error("raidsUpstream was modified; it has to stay byte faithful for the drift check")
	}
	tiers := decodeTiers(t, s.Raids())
	if hasName(tiers["5"], "Lunala") {
		t.Errorf("served blob still lists Lunala: %v", names(tiers["5"]))
	}
	if !hasName(tiers["5"], "Regirock") {
		t.Errorf("served blob is missing Regirock: %v", names(tiers["5"]))
	}
	if len(s.RaidsUpcoming()) == 0 {
		t.Error("no up next list was built")
	}
	if s.raidsBuiltFor.IsZero() {
		t.Error("no next boundary was recorded, so the ticker would rebuild every time")
	}
}

func TestAllDataCarriesTheRaidSchedule(t *testing.T) {
	// The raids page reads /api/app/data, not /api/raids, so the reconciled blob
	// and the up next list have to arrive through AllData or the page never sees
	// either of them.
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
	s.applyResult("raids", json.RawMessage(staleUpstream))
	s.applyResult("events", json.RawMessage(changeoverEvents))
	s.applyResult("pokemon", json.RawMessage(pk))
	s.applyResult("pokemon_types", json.RawMessage(ty))
	s.rebuildRaidsLocked()
	s.mu.Unlock()

	var blob struct {
		Raids         map[string][]raidBoss `json:"raids"`
		UpcomingRaids []UpcomingRaid        `json:"upcomingRaids"`
	}
	if err := json.Unmarshal(s.AllData(), &blob); err != nil {
		t.Fatalf("decode AllData: %v", err)
	}
	if hasName(blob.Raids["5"], "Lunala") {
		t.Errorf("AllData still serves Lunala: %v", names(blob.Raids["5"]))
	}
	if !hasName(blob.Raids["5"], "Registeel") {
		t.Errorf("AllData is missing Registeel: %v", names(blob.Raids["5"]))
	}
	if len(blob.UpcomingRaids) == 0 {
		t.Fatal("AllData carried no up next list")
	}

	// The client parses these as local time, so anything other than the feed's own
	// floating string would shift every countdown by the viewer's offset.
	var annotated int
	for _, b := range blob.Raids["5"] {
		if b.EndsAt == "" {
			continue
		}
		annotated++
		if _, _, ok := ParseFeedTime(b.EndsAt, time.UTC); !ok {
			t.Errorf("%s: ends_at %q is not a feed timestamp", b.PokemonName, b.EndsAt)
		}
		if b.EndsAt != "2026-09-08T22:00:00.000" {
			t.Errorf("%s: ends_at = %q, want the feed's own floating string", b.PokemonName, b.EndsAt)
		}
	}
	if annotated != 4 {
		t.Errorf("%d of tier 5 carried a window, want all 4", annotated)
	}
}

func TestReconcileFallsBackToUpNextWithoutMegaData(t *testing.T) {
	// A fresh install, or a megas fetch that failed, must still behave: the expired
	// Mega goes, the incoming one is named in the up next strip rather than becoming
	// a card with no typing, and the tier is simply empty for a while.
	pk, err := os.ReadFile("fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	ty, err := os.ReadFile("fallback/pokemon_types.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	lookup := newSpeciesLookup(json.RawMessage(pk), json.RawMessage(ty), nil)

	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	served, upcoming, stats := reconcileRaids(json.RawMessage(staleUpstream), windows,
		utc(t, "2026-08-27T12:00:00Z"), lookup, testCPMs(t))
	tiers := decodeTiers(t, served)

	if hasName(tiers["6"], "Mega Swampert") {
		t.Errorf("the expired Mega survived: %v", names(tiers["6"]))
	}
	if len(tiers["6"]) != 0 {
		t.Errorf("tier 6 = %v, want empty with no Mega data to build from", names(tiers["6"]))
	}
	var live *UpcomingRaid
	for i := range upcoming {
		if upcoming[i].Tier == "6" && upcoming[i].Live {
			live = &upcoming[i]
		}
	}
	if live == nil {
		t.Fatalf("Mega Gyarados is live but absent from up next: %+v", upcoming)
	}
	if live.Bosses[0].Name != "Mega Gyarados" {
		t.Errorf("up next names %q, want Mega Gyarados", live.Bosses[0].Name)
	}
	// The 5 star side is unaffected: it never needed the Mega data.
	if !hasName(tiers["5"], "Regirock") || stats.Synthesized != 3 || stats.Pending != 1 {
		t.Errorf("stats = %+v, want the 5 star trio still built and 1 pending", stats)
	}
}

func TestPgapiTypeName(t *testing.T) {
	for raw, want := range map[string]string{
		"POKEMON_TYPE_WATER":  "Water",
		"POKEMON_TYPE_DARK":   "Dark",
		"POKEMON_TYPE_FLYING": "Flying",
		"POKEMON_TYPE_ICE":    "Ice",
		"":                    "",
	} {
		if got := pgapiTypeName(raw); got != want {
			t.Errorf("pgapiTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseMegaForms(t *testing.T) {
	// Shaped like the real pokemon-go-api Mega slice, including the repeat it
	// actually contains: a species is listed once per form, so the same Mega turns
	// up more than once.
	raw := json.RawMessage(`[
	{"megaEvolutions":{"GYARADOS_MEGA":{
		"names":{"English":"Mega Gyarados"},
		"stats":{"stamina":216,"attack":292,"defense":247},
		"primaryType":{"type":"POKEMON_TYPE_WATER"},
		"secondaryType":{"type":"POKEMON_TYPE_DARK"},
		"assets":{"image":"https://example.invalid/pm130.fMEGA.icon.png"}}}},
	{"megaEvolutions":{
		"CHARIZARD_MEGA_X":{"names":{"English":"Mega Charizard X"},
			"stats":{"stamina":186,"attack":273,"defense":213},
			"primaryType":{"type":"POKEMON_TYPE_FIRE"},"secondaryType":{"type":"POKEMON_TYPE_DRAGON"}},
		"CHARIZARD_MEGA_Y":{"names":{"English":"Mega Charizard Y"},
			"stats":{"stamina":186,"attack":319,"defense":212},
			"primaryType":{"type":"POKEMON_TYPE_FIRE"},"secondaryType":{"type":"POKEMON_TYPE_FLYING"}}}},
	{"megaEvolutions":{"CHARIZARD_MEGA_X":{"names":{"English":"Mega Charizard X"},
		"stats":{"stamina":186,"attack":273,"defense":213},
		"primaryType":{"type":"POKEMON_TYPE_FIRE"},"secondaryType":{"type":"POKEMON_TYPE_DRAGON"}}}},
	{"megaEvolutions":{"BROKEN":{"names":{"English":"Mega Nothing"},"stats":{"stamina":1,"attack":0,"defense":1}}}},
	{"dexNr":25}
	]`)
	got := parseMegaForms(raw)

	if len(got) != 3 {
		t.Fatalf("parsed %d forms (%v), want 3", len(got), got)
	}
	g := got["mega gyarados"]
	if g.Atk != 292 || g.Def != 247 || g.Sta != 216 {
		t.Errorf("Mega Gyarados stats = %d/%d/%d, want 292/247/216", g.Atk, g.Def, g.Sta)
	}
	if len(g.Types) != 2 || g.Types[0] != "Water" || g.Types[1] != "Dark" {
		t.Errorf("Mega Gyarados types = %v, want [Water Dark]", g.Types)
	}
	if g.Image == "" {
		t.Error("Mega Gyarados lost its image")
	}
	// The two Charizards must stay apart: same species, different stats and typing.
	x, y := got["mega charizard x"], got["mega charizard y"]
	if x.Atk != 273 || y.Atk != 319 {
		t.Errorf("Charizard X/Y attack = %d/%d, want 273/319", x.Atk, y.Atk)
	}
	if len(y.Types) != 2 || y.Types[1] != "Flying" {
		t.Errorf("Mega Charizard Y types = %v, want [Fire Flying]", y.Types)
	}
	// A record with no usable stat line is skipped rather than stored as zeroes.
	if _, ok := got["mega nothing"]; ok {
		t.Error("a Mega with no attack stat was kept")
	}

	if parseMegaForms(json.RawMessage(`not json`)) != nil {
		t.Error("junk should parse to nil, so the previous data is kept")
	}
}

// ── Pending rotations ────────────────────────────────────────────────────────

// Pending used to be a count, and a count only ever reached a log line: the set
// was discarded on every rebuild, so nothing could act on it and the only fix was
// the next scheduled refresh, hours away, with the tier a boss short the whole
// time. This is the same fixture as TestReconcileFallsBackToUpNextWithoutMegaData,
// asking what the pending entry actually says.
func TestPendingListNamesWhatItIsWaitingOn(t *testing.T) {
	pk, err := os.ReadFile("fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	ty, err := os.ReadFile("fallback/pokemon_types.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	// No Mega table, so the live Mega rotation cannot be built.
	lookup := newSpeciesLookup(json.RawMessage(pk), json.RawMessage(ty), nil)

	windows := parseRaidWindows(json.RawMessage(changeoverEvents))
	now := utc(t, "2026-08-27T12:00:00Z")
	_, _, stats := reconcileRaids(json.RawMessage(staleUpstream), windows, now, lookup, testCPMs(t))

	if len(stats.PendingList) != stats.Pending {
		t.Fatalf("PendingList has %d entries but Pending counts %d", len(stats.PendingList), stats.Pending)
	}
	if len(stats.PendingList) != 1 {
		t.Fatalf("want exactly one pending rotation, got %+v", stats.PendingList)
	}

	p := stats.PendingList[0]
	if p.Species != "Mega Gyarados" {
		t.Errorf("pending species = %q, want Mega Gyarados", p.Species)
	}
	if p.Tier != "6" {
		t.Errorf("pending tier = %q, want 6", p.Tier)
	}
	if p.Reason != "mega stats not loaded" {
		t.Errorf("pending reason = %q; an admin needs to know this is our data gap, not upstream being behind", p.Reason)
	}
	if p.EndsUTC.IsZero() || !p.EndsUTC.After(now) {
		t.Errorf("pending window ends at %v, which is not in the future; it would be dropped as expired at once", p.EndsUTC)
	}
	if p.Expired(now) {
		t.Error("a live rotation reports itself expired")
	}
	if !p.Expired(p.EndsUTC) {
		t.Error("a rotation is not expired at its own end instant")
	}
}

// A rotation that ended while still pending is history, not work. Keeping it would
// hold the retry loop open forever on something that can never succeed, and the
// backoff would never reset.
func TestSetRaidsPendingDropsExpiredAndPersists(t *testing.T) {
	s := New()
	s.cacheDir = t.TempDir()

	// Anchored to the wall clock, not a fixed date: RaidsPending filters against
	// time.Now() on purpose, because a rotation expires while the process runs and
	// not only when something rebuilds. A fixed date here would be expired by the
	// time anyone ran the test.
	now := time.Now().UTC()
	live := RaidPending{Species: "Mega Gyarados", Tier: "6", Reason: "mega stats not loaded",
		StartsUTC: now.Add(-time.Hour), EndsUTC: now.Add(time.Hour)}
	over := RaidPending{Species: "Mega Swampert", Tier: "6", Reason: "mega stats not loaded",
		StartsUTC: now.Add(-48 * time.Hour), EndsUTC: now.Add(-24 * time.Hour)}

	s.mu.Lock()
	s.setRaidsPendingLocked([]RaidPending{live, over}, now)
	s.mu.Unlock()
	s.persistRaidsPending()

	got := s.RaidsPending()
	if len(got) != 1 || got[0].Species != "Mega Gyarados" {
		t.Fatalf("pending = %+v, want only the live rotation", got)
	}

	// Persisted, so a restart does not forget what it was waiting for.
	raw, err := os.ReadFile(filepath.Join(s.cacheDir, raidsPendingFile))
	if err != nil {
		t.Fatalf("pending set was not written to disk: %v", err)
	}
	var round []RaidPending
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("pending cache is not readable JSON: %v", err)
	}
	if len(round) != 1 || round[0].Species != "Mega Gyarados" {
		t.Errorf("round tripped %+v, want the live rotation", round)
	}

	// And an emptied set is written too, or the next boot reads back a stale one.
	s.mu.Lock()
	s.setRaidsPendingLocked(nil, now)
	s.mu.Unlock()
	s.persistRaidsPending()
	if got := s.RaidsPending(); len(got) != 0 {
		t.Errorf("pending = %+v after clearing, want empty", got)
	}
	s2 := New()
	s2.cacheDir = s.cacheDir
	s2.loadRaidsPending()
	if got := s2.RaidsPending(); len(got) != 0 {
		t.Errorf("a restart read back %+v from an emptied cache", got)
	}
}

func TestLoadRaidsPendingRestoresAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	s := New()
	s.cacheDir = dir
	s.mu.Lock()
	s.setRaidsPendingLocked([]RaidPending{{
		Species: "Mega Latios", Tier: "6", Reason: "mega stats not loaded",
		StartsUTC: now.Add(-time.Hour), EndsUTC: now.Add(6 * time.Hour),
	}}, now)
	s.mu.Unlock()
	s.persistRaidsPending()

	restarted := New()
	restarted.cacheDir = dir
	restarted.loadRaidsPending()

	got := restarted.RaidsPending()
	if len(got) != 1 || got[0].Species != "Mega Latios" {
		t.Fatalf("after a restart pending = %+v, want the Mega Latios rotation", got)
	}
	if got[0].Reason == "" {
		t.Error("the reason did not survive the round trip, so the retry cannot tell what to refetch")
	}
}

func TestPendingSummary(t *testing.T) {
	if got := pendingSummary(nil); got != "" {
		t.Errorf("empty set summarised as %q", got)
	}

	one := []RaidPending{{Species: "Mega Gyarados", Reason: "mega stats not loaded"}}
	if got := pendingSummary(one); got != "Mega Gyarados: mega stats not loaded" {
		t.Errorf("summary = %q", got)
	}

	// Capped, because this lands on one line in the admin panel and a changeover
	// can leave a whole tier pending at once. The count in the sentence before it
	// carries the total.
	many := make([]RaidPending, 6)
	for i := range many {
		many[i] = RaidPending{Species: "Boss", Reason: "species unknown"}
	}
	got := pendingSummary(many)
	if !strings.Contains(got, "and 3 more") {
		t.Errorf("summary = %q, want the tail collapsed", got)
	}
	if strings.Count(got, "Boss:") != 3 {
		t.Errorf("summary named %d bosses, want 3 then a count", strings.Count(got, "Boss:"))
	}
}

func TestIsMegaName(t *testing.T) {
	for name, want := range map[string]bool{
		"Mega Gyarados":  true,
		"mega latios":    true,
		"Primal Groudon": true,
		"Gyarados":       false,
		"Shadow Mewtwo":  false,
		"Megalith":       false, // prefix match must not fire on a name that merely starts with the letters
		"":               false,
	} {
		if got := isMegaName(name); got != want {
			t.Errorf("isMegaName(%q) = %v, want %v", name, got, want)
		}
	}
}

// ── Archive export ───────────────────────────────────────────────────────────

// RaidArchiveRows is what the warehouse writes from. It must read the SERVED list,
// not upstream: what is worth recording is what trainers actually saw, and after
// reconciliation those are not the same list.
func TestRaidArchiveRowsReadTheServedList(t *testing.T) {
	pk, err := os.ReadFile("fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	ty, err := os.ReadFile("fallback/pokemon_types.json")
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}

	s := New()
	s.cacheDir = t.TempDir()
	s.mu.Lock()
	s.applyResult("pokemon", json.RawMessage(pk))
	s.applyResult("pokemon_types", json.RawMessage(ty))
	s.applyResult("events", json.RawMessage(changeoverEvents))
	s.applyResult("raids", json.RawMessage(staleUpstream))
	s.rebuildRaidsLocked()
	s.mu.Unlock()

	rows := s.RaidArchiveRows()
	if len(rows) == 0 {
		t.Fatal("no archive rows from a served list that has bosses in it")
	}

	byName := map[string]RaidArchiveRow{}
	for _, r := range rows {
		byName[r.Species] = r
	}

	// The expired boss was dropped from the served list, so it must not be
	// archived: recording it would claim it ran when it did not.
	if _, present := byName["Mega Swampert"]; present {
		t.Error("an expired boss reached the archive; it is not in the served list")
	}

	// A live 5 star boss is there, with a resolved window it can be keyed on.
	regirock, ok := byName["Regirock"]
	if !ok {
		t.Fatalf("Regirock is served but absent from the archive rows: %v", names5(rows))
	}
	if !regirock.HasWindow() {
		t.Error("a governed rotation has no resolved window, so it cannot be recorded as an appearance")
	}
	if regirock.WindowEnd.Before(regirock.WindowStart) {
		t.Error("window ends before it starts")
	}
	if regirock.Tier != "5" {
		t.Errorf("Regirock tier = %q, want 5", regirock.Tier)
	}
	if len(regirock.Types) == 0 {
		t.Error("no typing, so the dimension row would be written blank on first sight")
	}
	if regirock.Source == "" {
		t.Error("no source, so a later reader cannot tell a published boss from an inferred one")
	}

	// Stable order, so a diff of two runs reads as a diff and not a reshuffle.
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.Tier > b.Tier || (a.Tier == b.Tier && a.Species > b.Species) {
			t.Fatalf("archive rows are not sorted: %q/%q then %q/%q", a.Tier, a.Species, b.Tier, b.Species)
		}
	}
}

// A boss no rotation describes still identifies something worth remembering, but
// there is no window to key an appearance on. Inventing one would put a row in the
// fact table recording something that did not happen.
func TestArchiveRowWithoutAWindowIsNotAnAppearance(t *testing.T) {
	r := RaidArchiveRow{Species: "Mewtwo", Tier: "1"}
	if r.HasWindow() {
		t.Error("a row with no window claims it can be recorded as an appearance")
	}
	r.WindowStart = time.Now()
	if r.HasWindow() {
		t.Error("a half-resolved window counts as a window")
	}
	r.WindowEnd = r.WindowStart.Add(time.Hour)
	if !r.HasWindow() {
		t.Error("a fully resolved window is not recognised")
	}
}

// names5 lists what came back, for a failure message worth reading.
func names5(rows []RaidArchiveRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Tier+":"+r.Species)
	}
	return out
}

// A rebuild is a pure in-memory operation. Several tests drive rebuildRaidsLocked
// directly on a store with no cache directory, and this used to scatter a
// raids_pending.json through the package directory when persistence lived inside
// setRaidsPendingLocked.
func TestRebuildDoesNotWriteThePendingCache(t *testing.T) {
	s := New()
	s.cacheDir = "" // no cache directory at all, the state a test-built store is in

	s.mu.Lock()
	s.setRaidsPendingLocked([]RaidPending{{
		Species: "Mega Latios", EndsUTC: time.Now().Add(time.Hour),
	}}, time.Now())
	s.mu.Unlock()

	// Held in memory...
	if got := s.RaidsPending(); len(got) != 1 {
		t.Fatalf("pending = %+v, want the entry held in memory", got)
	}
	// ...and persisting is a no-op rather than a write to the working directory.
	s.persistRaidsPending()
	if _, err := os.Stat(raidsPendingFile); err == nil {
		os.Remove(raidsPendingFile)
		t.Errorf("a store with no cache directory wrote %s into the working directory", raidsPendingFile)
	}
}

// Every path that rebuilds the served raid list must tell whoever is listening,
// or the raid history silently stops recording.
//
// This started as one missed call in CheckScrapers and turned out to be three:
// refreshMegas (a pending Mega becomes a real card), refreshEvents (the schedule
// IS who is live, and it refreshes every 30 minutes) and CheckScrapers itself.
// Each was a rebuild whose result nothing downstream ever heard about, and the
// symptom was not an error: it was a history with gaps in it, noticed weeks later
// if at all.
//
// A source-level assertion because the alternative is a fake store and a fake
// clock to observe a callback, which would test the harness more than the code.
// internal/server guards its route table the same way.
func TestEveryRaidRebuildNotifies(t *testing.T) {
	src := readPogodataSource(t, "pogodata.go") + "\n" + readPogodataSource(t, "raidschedule.go")

	// The two boot paths hold s.mu across their whole body and cannot unlock to
	// notify. They do not need to: handlers.StartRaidHistory archives once at
	// startup precisely because a boot that serves the disk cache unchanged has
	// no rebuild to hook. Anything else added to this list needs the same kind of
	// reason written down beside it.
	exempt := map[string]string{
		"loadFromCache": "boot; holds the lock for its whole body, and StartRaidHistory archives at startup",
		"loadFallback":  "boot; same as loadFromCache",
	}

	funcRe := regexp.MustCompile(`^func (?:\(s \*Store\) )?(\w+)\(`)
	var current string
	bodies := map[string][]string{}
	order := []string{}
	for _, line := range strings.Split(src, "\n") {
		if m := funcRe.FindStringSubmatch(line); m != nil {
			current = m[1]
			if _, seen := bodies[current]; !seen {
				order = append(order, current)
			}
		}
		if current != "" {
			bodies[current] = append(bodies[current], line)
		}
	}

	checked := 0
	for _, name := range order {
		body := strings.Join(bodies[name], "\n")
		if !strings.Contains(body, "s.rebuildRaidsLocked()") {
			continue
		}
		// The definition of the helper itself, and the ones that only mention it
		// in prose, are not call sites.
		if name == "rebuildRaidsLocked" {
			continue
		}
		checked++
		if why, ok := exempt[name]; ok {
			if strings.Contains(body, "s.notifyRaidsApplied()") {
				t.Errorf("%s is on the exempt list (%s) but now notifies; remove it from the list", name, why)
			}
			continue
		}
		if !strings.Contains(body, "s.notifyRaidsApplied()") {
			t.Errorf("%s rebuilds the served raid list without calling notifyRaidsApplied, so the raid history will not see it", name)
		}
	}

	// A guard that checks nothing is worse than no guard: it passes forever while
	// the thing it was written for quietly stops existing.
	if checked < 4 {
		t.Fatalf("only found %d rebuild sites; this test has stopped matching the code it guards", checked)
	}
	t.Logf("checked %d raid rebuild sites", checked)
}

// readPogodataSource returns one file from this package as text.
func readPogodataSource(t *testing.T, name string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test file")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(self), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
