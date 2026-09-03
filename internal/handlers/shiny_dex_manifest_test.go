package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"pogo.hails.cc/internal/pogodata"
)

// The join key is the whole contract between this manifest and a client's own
// collection. It mirrors buildCaughtCardKeys in ts/shinies.ts, which builds
// `${pokemon_id}:${cardRegion(...)}` where pokemon_id is the species NAME, because
// that is what user_shinies.pokemon_id holds.
//
// Getting it wrong does not fail loudly: every card simply renders unticked, which
// looks like the trainer owns nothing rather than like a bug. Hence a test that
// spells the format out rather than deriving it.
func TestShinyDexCardKeyFormat(t *testing.T) {
	species := pogodata.BaselineSpecies{ID: 201, Name: "Unown", InGo: true, ShinyReleased: true}

	base := shinyDexCard{Key: species.Name + ":"}
	if base.Key != "Unown:" {
		t.Errorf("base card key = %q, want %q", base.Key, "Unown:")
	}

	form := formCard(species, RegionalFormRow{Species: "Unown", Region: "unown_b", Slug: "201-b"}, nil, nil)
	if form.Key != "Unown:unown_b" {
		t.Errorf("form card key = %q, want %q", form.Key, "Unown:unown_b")
	}
	if form.Label != "B" {
		t.Errorf("Unown b label = %q, want B", form.Label)
	}
	if !strings.HasSuffix(form.SpriteURL, "/shiny/201-b.png") {
		t.Errorf("sprite URL = %q, want the shiny 201-b art", form.SpriteURL)
	}
}

// A form's shiny cannot exist before its species'. This is rule 1 in formCard and
// the one that would otherwise let an unreleased species show released forms.
func TestFormShinyRequiresTheSpecies(t *testing.T) {
	locked := pogodata.BaselineSpecies{ID: 570, Name: "Zorua", InGo: true, ShinyReleased: false, ReleaseDate: "2027-01-01"}
	form := formCard(locked, RegionalFormRow{Species: "Zorua", Region: "hisuian", Slug: "10238"}, nil, nil)

	if form.Released {
		t.Error("a form of a species with no released shiny is marked released")
	}
	// Rule 3: it shows the SPECIES' date, because the form cannot arrive first.
	if form.ReleaseDate != "2027-01-01" {
		t.Errorf("release date = %q, want the species' date while the species is locked", form.ReleaseDate)
	}
}

// The compiled-in default is the answer when no admin has an opinion, and an
// override beats it in BOTH directions. The overlay is sparse, so the absent case
// must not be read as false: that would mark every ordinary form unreleased.
func TestFormShinyOverridePrecedence(t *testing.T) {
	open := pogodata.BaselineSpecies{ID: 570, Name: "Zorua", InGo: true, ShinyReleased: true}
	row := RegionalFormRow{Species: "Zorua", Region: "hisuian", Slug: "10238"}

	// Hisuian Zorua is one of the eight pairs the default table lists as unreleased.
	if got := formCard(open, row, nil, nil); got.Released {
		t.Error("Hisuian Zorua is released with no override, but the default table says otherwise")
	}

	on := map[string]map[string]bool{"Zorua": {"hisuian": true}}
	if got := formCard(open, row, on, nil); !got.Released {
		t.Error("an admin override turning a form on was ignored")
	}

	// And the other direction, on a form whose default is released.
	alolan := pogodata.BaselineSpecies{ID: 19, Name: "Rattata", InGo: true, ShinyReleased: true}
	rat := RegionalFormRow{Species: "Rattata", Region: "alolan", Slug: "10091"}
	if got := formCard(alolan, rat, nil, nil); !got.Released {
		t.Error("Alolan Rattata should be released by default")
	}
	off := map[string]map[string]bool{"Rattata": {"alolan": false}}
	if got := formCard(alolan, rat, off, nil); got.Released {
		t.Error("an admin override turning a form off was ignored")
	}
}

// Once the species is out, the form carries its OWN announced date rather than the
// species' history.
func TestFormCarriesItsOwnDateOnceTheSpeciesIsOut(t *testing.T) {
	open := pogodata.BaselineSpecies{ID: 570, Name: "Zorua", InGo: true, ShinyReleased: true}
	dates := map[string]map[string]string{"Zorua": {"hisuian": "2026-12-25"}}

	got := formCard(open, RegionalFormRow{Species: "Zorua", Region: "hisuian", Slug: "10238"}, nil, dates)
	if got.ReleaseDate != "2026-12-25" {
		t.Errorf("release date = %q, want the form's own announced day", got.ReleaseDate)
	}
}

// Scatterbug and Spewpa record a pattern they never show. A client that does not
// fold those onto the base card builds a key matching no card, and the trainer's
// catch stops counting with nothing to indicate why.
func TestPatternCarriersCoverEveryVivillonPattern(t *testing.T) {
	carriers := patternCarriers()
	for _, species := range []string{"Scatterbug", "Spewpa"} {
		regions, ok := carriers[species]
		if !ok {
			t.Fatalf("%s is missing from pattern_carriers", species)
		}
		if len(regions) != len(vivillonVariants) {
			t.Errorf("%s carries %d patterns, want all %d", species, len(regions), len(vivillonVariants))
		}
		have := make(map[string]bool, len(regions))
		for _, r := range regions {
			have[r] = true
		}
		for _, v := range vivillonVariants {
			if !have[v.Region] {
				t.Errorf("%s is missing pattern %s", species, v.Region)
			}
		}
	}
	// Vivillon itself OWNS its patterns; folding them away would collapse 20 cards
	// into one.
	if _, wrong := carriers["Vivillon"]; wrong {
		t.Error("Vivillon is listed as a pattern carrier, which would erase its own 20 cards")
	}
}

// The version is a content hash rather than a timestamp. The cache expires once a
// minute, so a clock-based version would change up to sixty times an hour and make
// every client re-download an identical payload.
func TestManifestVersionIsContentAddressed(t *testing.T) {
	a := []shinyDexCard{{Key: "Bulbasaur:", Dex: 1, Released: true}}
	b := []shinyDexCard{{Key: "Bulbasaur:", Dex: 1, Released: true}}
	c := []shinyDexCard{{Key: "Bulbasaur:", Dex: 1, Released: false}}

	if manifestVersion(a) != manifestVersion(b) {
		t.Error("identical card lists produced different versions, so every client refetches on every rebuild")
	}
	if manifestVersion(a) == manifestVersion(c) {
		t.Error("a changed release flag did not change the version, so no client would ever see it")
	}
}

// Absent fields must be absent, not empty. The app codes against presence, and an
// empty string in a label or a region reads as a real value.
func TestBaseCardOmitsFormOnlyFields(t *testing.T) {
	card := shinyDexCard{Key: "Pikachu:", Dex: 25, Species: "Pikachu", SpriteSlug: "25", InGo: true, Released: true}
	body, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"region", "label", "release_date"} {
		if strings.Contains(string(body), `"`+field+`"`) {
			t.Errorf("base card carries an empty %s: %s", field, body)
		}
	}
}

// ── The whole manifest, against the real embedded baseline ───────────────────

// buildShinyDexManifest needs a store whose dex table has been resolved.
// SetShinyOverrides is the real entry point for that: it is what
// reloadShinyOverrides calls at startup, and passing no overrides exercises the
// shipped defaults, which is exactly what should be under test here.
func manifestForTest(t *testing.T) shinyDexManifest {
	t.Helper()
	store := pogodata.New()
	store.SetShinyOverrides(nil)
	h := &Handlers{store: store}

	m := h.buildShinyDexManifest()
	if len(m.Cards) == 0 {
		t.Skip("no shiny baseline embedded in this build, so there is no manifest to check")
	}
	return m
}

// The counts are the whole point of this endpoint: the native screen rendered one
// card per species and was missing every form. These numbers are what "at parity
// with the website" means, and npm run check pins the same two families on the
// TypeScript side.
func TestManifestExpandsEveryFormFamily(t *testing.T) {
	m := manifestForTest(t)

	byRegion := map[string]int{}
	unown, vivillon := 0, 0
	for _, c := range m.Cards {
		byRegion[c.Region]++
		switch {
		case strings.HasPrefix(c.Region, "unown_"):
			unown++
		case strings.HasPrefix(c.Region, "viv_"):
			vivillon++
		}
	}

	if unown != 28 {
		t.Errorf("Unown letter cards = %d, want 28", unown)
	}
	if vivillon != 20 {
		t.Errorf("Vivillon pattern cards = %d, want 20", vivillon)
	}
	// Base cards are the species count; everything else is a form.
	base := byRegion[""]
	forms := len(m.Cards) - base
	if base < 1000 {
		t.Errorf("base species cards = %d, want the full National Dex", base)
	}
	if forms < 100 {
		t.Errorf("form cards = %d; the regional and alternate formes are missing", forms)
	}
	t.Logf("%d cards: %d species, %d forms (%d Unown, %d Vivillon)", len(m.Cards), base, forms, unown, vivillon)
}

func TestManifestCardsAreUniqueSortedAndRenderable(t *testing.T) {
	m := manifestForTest(t)

	seen := make(map[string]bool, len(m.Cards))
	lastDex, lastRank := 0, -1
	for i, c := range m.Cards {
		if seen[c.Key] {
			t.Fatalf("duplicate card key %q at index %d; a client joining on it would tick two cards", c.Key, i)
		}
		seen[c.Key] = true

		if c.SpriteURL == "" || c.SpriteSlug == "" {
			t.Errorf("card %q has no sprite, so it renders blank", c.Key)
		}
		if c.Species == "" || c.Dex == 0 {
			t.Errorf("card %q is missing its species or dex", c.Key)
		}
		if c.Key != c.Species+":"+c.Region {
			t.Errorf("card %q does not match its own species and region (%s / %s)", c.Key, c.Species, c.Region)
		}

		// Sorted by dex, then by REGION_ORDER within a species. A form sorting
		// before its own base card is the failure this catches.
		rank := regionRank(c.Region)
		if c.Dex < lastDex {
			t.Fatalf("card %q (dex %d) sorts after dex %d", c.Key, c.Dex, lastDex)
		}
		if c.Dex == lastDex && rank < lastRank {
			t.Fatalf("card %q sorts out of REGION_ORDER within its species", c.Key)
		}
		lastDex, lastRank = c.Dex, rank
	}
}

// A released shiny implies the species is in the game, and the checklist would be
// nonsense otherwise: a card you can catch for a species that does not exist.
func TestManifestReleasedImpliesInGo(t *testing.T) {
	m := manifestForTest(t)
	for _, c := range m.Cards {
		if c.Released && !c.InGo {
			t.Errorf("card %q is released but not in GO", c.Key)
		}
	}
}

// A card that is already catchable must not carry a date. The date exists to say
// "not yet"; leaving it on a released card would have the app render "coming" over
// something a trainer can go and catch right now.
func TestManifestReleasedCardsCarryNoDate(t *testing.T) {
	m := manifestForTest(t)
	for _, c := range m.Cards {
		if c.Released && c.ReleaseDate != "" {
			t.Errorf("card %q is released but still carries the date %q", c.Key, c.ReleaseDate)
		}
	}
}

// The manifest is the ONE payload whose sprite URL must be absolute.
//
// Everywhere else the server can send a site relative path, because the app runs sprite
// URLs through ApiClient.absoluteUrl before drawing them. The shiny dex does not: both
// ShinyDexScreen and ShinyDexDetailSheet pass card.spriteUrl straight to the image loader.
// A relative path there is a thousand blank cards on every build of the app already in a
// trainer's hands, and no server change can rescue it afterwards.
//
// So this asserts the shape the shipped app depends on, not merely the shape we prefer.
func TestManifestSpriteURLsAreAbsoluteAndProxied(t *testing.T) {
	m := manifestForTest(t)
	if len(m.Cards) == 0 {
		t.Fatal("no cards to check")
	}
	for _, c := range m.Cards {
		if !strings.HasPrefix(c.SpriteURL, "http://") && !strings.HasPrefix(c.SpriteURL, "https://") {
			t.Fatalf("card %q sprite_url is not absolute (%q); the app renders this one without absoluteUrl", c.Key, c.SpriteURL)
		}
		if strings.Contains(c.SpriteURL, "githubusercontent.com") {
			t.Fatalf("card %q still hotlinks its sprite: %s", c.Key, c.SpriteURL)
		}
		if !strings.Contains(c.SpriteURL, PokemonSpritePath+"shiny/") {
			t.Fatalf("card %q sprite_url does not go through the shiny sprite proxy: %s", c.Key, c.SpriteURL)
		}
		// It must also be a name the proxy will actually serve.
		file := c.SpriteURL[strings.LastIndex(c.SpriteURL, "/")+1:]
		if !pokemonSpriteAllowed(file) {
			t.Fatalf("card %q sprite_url %q ends in a name the proxy allowlist rejects", c.Key, c.SpriteURL)
		}
	}
}
