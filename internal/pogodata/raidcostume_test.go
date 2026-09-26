package pogodata

import (
	"encoding/json"
	"testing"
	"time"
)

// The Horizons event, 2026-09-16 to 2026-09-22, is the case every test in this file
// is built from. Its page names "Captain's Cap Pikachu" in one star raids and
// "Charizard wearing Friede's goggles" in three star, and pokemon-go-api listed both
// under their plain species names: Pikachu with the costume art, Charizard with the
// plain art, and each carrying the real form id in a field this app used to discard.
const (
	horizonsPikachuSprite   = "https://cdn.leekduck.com/assets/img/pokemon_icons/pm25.fHORIZONS.icon.png"
	horizonsCharizardSprite = "https://cdn.leekduck.com/assets/img/pokemon_icons/pm6.fGOGGLES_2026.icon.png"
	plainCharizardSprite    = "https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm6.icon.png"
)

// horizonsWindow is the additive rotation the Raids section of the event page
// produces, with whatever bosses the caller wants on it.
func horizonsWindow(now time.Time, tier string, bosses ...WindowBoss) RaidWindow {
	return RaidWindow{
		EventID: "pokemon-horizons-the-series-celebration-event-2026",
		Name:    "Pokemon Horizons: The Series Celebration Event 2026",
		Tier:    tier, Bosses: bosses,
		RawStart: "2026-09-16T10:00:00.000", RawEnd: "2026-09-22T20:00:00.000",
		StartsUTC: now.Add(-24 * time.Hour), EndsUTC: now.Add(24 * time.Hour),
		Additive: true,
	}
}

func reconcileCostume(t *testing.T, upstream string, now time.Time, windows ...RaidWindow) (json.RawMessage, []UpcomingRaid, raidReconcileStats) {
	t.Helper()
	return reconcileRaids(raidReconcileInput{
		Upstream: json.RawMessage(upstream), Windows: windows,
		Now: now, Lookup: testLookup(t), CPMs: testCPMs(t),
	})
}

// Only the mined asset grammar carries a form token. The other three shapes on a
// real event page must produce no costume identity at all, because a wrong dex would
// join two different species and a wrong token would rename the wrong card.
// go_fest_finale_raids.html carries all four.
func TestSpriteIdentityReadsOnlyTheMinedGrammar(t *testing.T) {
	cases := []struct {
		url     string
		dex     int
		form    string
		ok      bool
		comment string
	}{
		{horizonsCharizardSprite, 6, "GOGGLES_2026", true, "the costume this whole file is about"},
		{plainCharizardSprite, 6, "", true, "plain art, a dex and nothing more"},
		{"https://x/pm150.fMEGA_X.icon.png", 150, "MEGA_X", true, "a form token that is not a costume"},
		{"https://x/pm25.fHORIZONS.s.icon.png", 25, "HORIZONS", true, "the shiny variant of the same asset"},
		{"https://x/pokemon_icon_015_51.png", 15, "", true, "legacy grammar: the trailing digits are not a token"},
		{"https://x/pokemon_icon_pm15_51_pgo_a.png", 0, "", false, "a third grammar, refused"},
		{"https://x/Mega%20Skarmory.png", 0, "", false, "a name as a filename, refused"},
		{"", 0, "", false, "no sprite at all"},
	}
	for _, c := range cases {
		dex, form, ok := spriteIdentity(c.url)
		if dex != c.dex || form != c.form || ok != c.ok {
			t.Errorf("spriteIdentity(%q) = (%d, %q, %v), want (%d, %q, %v): %s",
				c.url, dex, form, ok, c.dex, c.form, c.ok, c.comment)
		}
	}
}

// The join, on the exact shape production had on 2026-09-21: upstream's card is
// called "Charizard" and drawn plain, and only its form id says otherwise. One card
// comes out, called what the page calls it, drawn how the page draws it, dated, and
// still carrying upstream's own CP.
func TestCostumeCardTakesThePageNameAndKeepsUpstreamCP(t *testing.T) {
	const upstream = `{"3":[{"pokemon_name":"Charizard","cp":1574,"cp_max":1651,"cp_boosted_min":1968,"cp_boosted_max":2064,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm6.icon.png",
	"types":["Fire","Flying"],"can_be_shiny":true,"form":"CHARIZARD_GOGGLES_2026"}]}`

	now := utc(t, "2026-09-21T13:00:00Z")
	served, _, stats := reconcileCostume(t, upstream, now,
		horizonsWindow(now, "3", WindowBoss{Name: "Charizard wearing Friede's goggles", Image: horizonsCharizardSprite, CanBeShiny: true}))

	cards := servedTier(t, served, "3")
	if len(cards) != 1 {
		t.Fatalf("tier 3 = %v, want exactly one card: the plain species and the costume are one slot", names(cards))
	}
	got := cards[0]
	if got.PokemonName != "Charizard wearing Friede's goggles" {
		t.Errorf("card name = %q, want the page's name", got.PokemonName)
	}
	if got.ImageURL != horizonsCharizardSprite {
		t.Errorf("card sprite = %q, want the costume art the page carries", got.ImageURL)
	}
	// Upstream's numbers, untouched: the costume is cosmetic and the stat line is
	// the base species', which is what upstream already computed.
	if got.CP != 1574 || got.CPMax != 1651 || got.CPBoostedMin != 1968 || got.CPBoostedMax != 2064 {
		t.Errorf("CP %d-%d boosted %d-%d, want upstream's 1574-1651 and 1968-2064",
			got.CP, got.CPMax, got.CPBoostedMin, got.CPBoostedMax)
	}
	if got.EventID != "pokemon-horizons-the-series-celebration-event-2026" || got.EndsAt != "2026-09-22T20:00:00.000" {
		t.Errorf("card window = %q %q, want the Horizons event and its end", got.EventID, got.EndsAt)
	}
	// The form id is an input and must never be served.
	if got.Form != "" {
		t.Errorf("served card carries form %q, which is not part of the contract", got.Form)
	}
	if stats.Pending != 0 {
		t.Errorf("pending = %d (%+v), want none: the costume boss has a card", stats.Pending, stats.PendingList)
	}
	if stats.Annotated != 1 {
		t.Errorf("annotated = %d, want 1", stats.Annotated)
	}
}

// The same page boss with nothing to join to. It has to become a card anyway, built
// from the species the label is decorating, or a tier that upstream has not caught
// up to sits empty while the strip promises a boss that never arrives.
func TestCostumeCardIsSynthesizedWhenUpstreamHasNotListedIt(t *testing.T) {
	now := utc(t, "2026-09-21T13:00:00Z")
	served, upcoming, stats := reconcileCostume(t, `{"1":[]}`, now,
		horizonsWindow(now, "1", WindowBoss{Name: "Captain's Cap Pikachu", Image: horizonsPikachuSprite, CanBeShiny: true}))

	cards := servedTier(t, served, "1")
	if len(cards) != 1 || cards[0].PokemonName != "Captain's Cap Pikachu" {
		t.Fatalf("tier 1 = %v, want the costumed Pikachu", names(cards))
	}
	// Pikachu's line, at the raid capture levels: the costume changes nothing about
	// the stats, which is the whole reason resolving the base species is safe.
	if cards[0].CP != 493 || cards[0].CPMax != 536 {
		t.Errorf("CP %d-%d, want Pikachu's 493-536", cards[0].CP, cards[0].CPMax)
	}
	if cards[0].Source != "events" {
		t.Errorf("source = %q, want events", cards[0].Source)
	}
	if stats.Pending != 0 {
		t.Errorf("pending = %d (%+v), want none", stats.Pending, stats.PendingList)
	}
	// And it must not ALSO be advertised as a rotation still waiting for details.
	for _, u := range upcoming {
		if u.Live {
			t.Errorf("up next still lists %v as live without a card, but it has one", u.Bosses)
		}
	}
}

// Decision: a costumed boss and the plain species are one slot. When upstream lists
// the plain card separately, it leaves the grid for as long as the costume is live.
func TestPlainCardLeavesTheGridWhileTheCostumeIsLive(t *testing.T) {
	const upstream = `{"3":[{"pokemon_name":"Charizard","cp":1574,"cp_max":1651,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm6.icon.png",
	"types":["Fire","Flying"],"form":"CHARIZARD"},
	{"pokemon_name":"Meowscarada","cp":1538,"cp_max":1614,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm908.icon.png",
	"types":["Grass","Dark"],"form":"MEOWSCARADA"}]}`

	now := utc(t, "2026-09-21T13:00:00Z")
	served, _, stats := reconcileCostume(t, upstream, now,
		horizonsWindow(now, "3",
			WindowBoss{Name: "Charizard wearing Friede's goggles", Image: horizonsCharizardSprite, CanBeShiny: true},
			WindowBoss{Name: "Meowscarada", Image: "https://cdn.leekduck.com/assets/img/pokemon_icons/pm908.icon.png", CanBeShiny: true}))

	cards := servedTier(t, served, "3")
	if hasName(cards, "Charizard") {
		t.Errorf("tier 3 = %v, still carries the plain Charizard beside the costumed one", names(cards))
	}
	if !hasName(cards, "Charizard wearing Friede's goggles") {
		t.Errorf("tier 3 = %v, lost the costumed Charizard", names(cards))
	}
	// A boss the event names WITHOUT a costume is untouched by any of this.
	if !hasName(cards, "Meowscarada") {
		t.Errorf("tier 3 = %v, an ordinary event boss was caught by the costume rule", names(cards))
	}
	if stats.CostumeReplaced != 1 {
		t.Errorf("CostumeReplaced = %d, want 1", stats.CostumeReplaced)
	}
}

// Upstream says a costume is live and no event page names it, which is what a broken
// Raids section reader looks like from here. The card is served exactly as it
// arrived, and the admin screen gets a number rather than a silently plain grid.
func TestCostumeUnnamedCountsUpstreamDisagreeingWithItself(t *testing.T) {
	const upstream = `{"3":[{"pokemon_name":"Charizard","cp":1574,"cp_max":1651,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm6.icon.png",
	"types":["Fire","Flying"],"form":"CHARIZARD_GOGGLES_2026"}]}`

	now := utc(t, "2026-09-21T13:00:00Z")
	// A live rotation somewhere else in the schedule, so the reconciler runs at all.
	served, _, stats := reconcileCostume(t, upstream, now, horizonsWindow(now, "1",
		WindowBoss{Name: "Captain's Cap Pikachu", Image: horizonsPikachuSprite, CanBeShiny: true}))

	cards := servedTier(t, served, "3")
	if len(cards) != 1 || cards[0].PokemonName != "Charizard" || cards[0].ImageURL != plainCharizardSprite {
		t.Fatalf("tier 3 = %v, want upstream's card exactly as it arrived", names(cards))
	}
	if stats.CostumeUnnamed != 1 {
		t.Errorf("CostumeUnnamed = %d, want 1", stats.CostumeUnnamed)
	}
	// And a plain card, with no costume in its form id, must never be counted.
	_, _, plain := reconcileCostume(t, `{"3":[{"pokemon_name":"Meowscarada","cp":1538,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm908.icon.png",
	"types":["Grass","Dark"],"form":"MEOWSCARADA"}]}`, now, horizonsWindow(now, "1",
		WindowBoss{Name: "Captain's Cap Pikachu", Image: horizonsPikachuSprite}))
	if plain.CostumeUnnamed != 0 {
		t.Errorf("CostumeUnnamed = %d for an undecorated form id, want 0", plain.CostumeUnnamed)
	}
}

// Upstream's form id is an input, not part of what this app serves, and the tier
// loop is not enough to guarantee that: a tier the schedule does not govern is
// passed straight through, card for card, and never visits the loop at all. Only the
// sweep before the marshal covers it.
func TestUpstreamFormNeverReachesAnyServedTier(t *testing.T) {
	const upstream = `{"3":[{"pokemon_name":"Charizard","cp":1574,
	"image_url":"https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/pm6.icon.png",
	"types":["Fire","Flying"],"form":"CHARIZARD_GOGGLES_2026"}],
	"2":[{"pokemon_name":"Wobbuffet","cp":900,"types":["Psychic"],"form":"WOBBUFFET_COSTUME_2026"}]}`

	now := utc(t, "2026-09-21T13:00:00Z")
	served, _, _ := reconcileCostume(t, upstream, now, horizonsWindow(now, "1",
		WindowBoss{Name: "Captain's Cap Pikachu", Image: horizonsPikachuSprite}))

	var tiers map[string][]raidBoss
	if err := json.Unmarshal(served, &tiers); err != nil {
		t.Fatalf("served blob parse: %v", err)
	}
	for tier, cards := range tiers {
		for _, c := range cards {
			if c.Form != "" {
				t.Errorf("tier %s card %q was served carrying form %q", tier, c.PokemonName, c.Form)
			}
		}
	}
	// And the ungoverned tier is still passed through, rather than quietly dropped by
	// the pass that strips the field.
	if len(tiers["2"]) != 1 {
		t.Errorf("tier 2 = %v, want the card upstream sent, untouched apart from the form", names(tiers["2"]))
	}
}

// baseSpeciesForBoss is the fallback that makes a costume label resolvable at all.
// The residue test is the load bearing half: a name that is only a species must keep
// failing, or every unknown boss quietly answers itself.
func TestBaseSpeciesForBossNeedsSomethingLeftOver(t *testing.T) {
	cases := []struct {
		boss WindowBoss
		want string
		ok   bool
	}{
		{WindowBoss{Name: "Charizard wearing Friede's goggles", Image: horizonsCharizardSprite}, "Charizard", true},
		{WindowBoss{Name: "Captain's Cap Pikachu"}, "Pikachu", true},
		{WindowBoss{Name: "Modern Jacket Machamp"}, "Machamp", true},
		{WindowBoss{Name: "Charizard"}, "", false},
		{WindowBoss{Name: "Notapokemon"}, "", false},
		// The sprite wins over the name, because it is the game's own answer and
		// does not depend on what language the page was written in.
		{WindowBoss{Name: "Ein Pikachu mit Kappe", Image: horizonsPikachuSprite}, "Pikachu", true},
	}
	for _, c := range cases {
		got, ok := baseSpeciesForBoss(c.boss)
		if got != c.want || ok != c.ok {
			t.Errorf("baseSpeciesForBoss(%q) = (%q, %v), want (%q, %v)", c.boss.Name, got, ok, c.want, c.ok)
		}
	}
}
