package costumes

import (
	"slices"
	"strings"
	"testing"
)

// The visor that started all this: a Charmander caught in the GO Fest 2020 visor evolves into
// a visored Charizard, so the whole Kanto starter line must resolve. The old hand-written table
// guessed the code (f:GOFEST_2026, which is really a Caterpie Poke Ball hat) and marked it
// pending, which hid art that had existed upstream since 2020.
func TestVisorResolvesAcrossTheKantoStarters(t *testing.T) {
	for _, tc := range []struct {
		dex     int
		species string
	}{
		{1, "Bulbasaur"}, {2, "Ivysaur"}, {3, "Venusaur"},
		{4, "Charmander"}, {5, "Charmeleon"}, {6, "Charizard"},
		{7, "Squirtle"}, {8, "Wartortle"}, {9, "Blastoise"},
	} {
		url, ok := SpriteURL(tc.dex, tc.species, "Visor")
		if !ok {
			t.Errorf("%s: Visor did not resolve", tc.species)
			continue
		}
		want := SpritePath + "pm" + itoa(tc.dex) + ".cSPRING_2020_NOEVOLVE.s.icon.png"
		if url != want {
			t.Errorf("%s: got %s, want %s", tc.species, url, want)
		}
	}
}

// Labels that used to point at the wrong costume. Each of these was verified by looking at the
// actual sprite, after Dittobase disagreed with our name for the code. A trainer who recorded a
// "Detective Hat" Pikachu was being shown a straw hat.
func TestMislabelledCostumesStayFixed(t *testing.T) {
	for _, tc := range []struct {
		species, label, wantCode string
	}{
		{"Pikachu", "Detective Hat", "cFEB_2019"},      // was cMAY_2019_NOEVOLVE, a straw hat
		{"Pikachu", "Straw Hat", "cMAY_2019_NOEVOLVE"}, // was cSUMMER_2018, a sun hat and sunglasses
		{"Pikachu", "Summer Hat", "cSUMMER_2018"},      // the sun hat and sunglasses, now named
		{"Pikachu", "Flower Crown", "cNOVEMBER_2018"},  // was overridden to cFEB_2019, a detective hat
		{"Pikachu", "Dawn's Hat", "fGOTOUR_2024_A_02"}, // was fGOTOUR_2024_B, which is Rei's cap
		{"Pikachu", "Rei's Cap", "fGOTOUR_2024_B"},     // the code Dawn's Hat used to squat on
	} {
		url, ok := SpriteURL(25, tc.species, tc.label)
		if !ok {
			t.Errorf("%s %q did not resolve", tc.species, tc.label)
			continue
		}
		if !strings.Contains(url, "."+tc.wantCode+".") {
			t.Errorf("%s %q -> %s, want code %s", tc.species, tc.label, url, tc.wantCode)
		}
	}
}

// The old label must keep resolving: it may already sit in user_shinies.costume as free text.
func TestAliasKeepsTheOldLabelWorking(t *testing.T) {
	old, ok := SpriteURL(6, "Charizard", "Pikachu Visor")
	if !ok {
		t.Fatal("aliased label did not resolve")
	}
	if cur, _ := SpriteURL(6, "Charizard", "Visor"); old != cur {
		t.Errorf("alias resolved to %s, canonical to %s", old, cur)
	}
}

// A label only resolves where the art actually exists. Pikachu never got the Spring 2020 visor
// (its 2026 visor is a different costume), and Snorlax never got a party hat.
func TestCostumeDoesNotLeakToIneligibleSpecies(t *testing.T) {
	if _, ok := SpriteURL(25, "Pikachu", "Visor"); ok {
		t.Error("Visor resolved for Pikachu, which has no such asset")
	}
	if _, ok := SpriteURL(143, "Snorlax", "Party Hat"); ok {
		t.Error("Party Hat resolved for Snorlax, which has no such asset")
	}
}

// Species overrides beat shared costumes, and a species gets only its own art.
func TestOverrideBeatsShared(t *testing.T) {
	url, ok := SpriteURL(25, "Pikachu", "Party Hat")
	if !ok {
		t.Fatal("Pikachu Party Hat did not resolve")
	}
	if !strings.Contains(url, "cANNIVERSARY") {
		t.Errorf("override lost to the shared code: %s", url)
	}
	// Wurmple has no override, so it takes the shared JAN_2020 party hat.
	url, ok = SpriteURL(265, "Wurmple", "Party Hat")
	if !ok {
		t.Fatal("Wurmple Party Hat did not resolve")
	}
	if !strings.Contains(url, "cJAN_2020_NOEVOLVE") {
		t.Errorf("Wurmple got the wrong party hat: %s", url)
	}
}

// Every label the picker offers must resolve. If these ever disagree, the UI advertises a
// costume that renders as a plain shiny.
func TestEveryOfferedLabelResolves(t *testing.T) {
	for species := range lab.Species {
		dex := dexOfSpecies(species)
		if dex == 0 {
			continue
		}
		for _, label := range LabelsForDex(dex, species) {
			if _, ok := SpriteURL(dex, species, label); !ok {
				t.Errorf("%s: picker offers %q but it does not resolve", species, label)
			}
		}
	}
}

// The review queue: costumes the game has that nobody has named. Each is inert (no label means
// it can never be offered or typed), so this list is the backlog the admin tab renders.
func TestUnlabelledIsTheReviewQueue(t *testing.T) {
	list := Unlabelled()
	if len(list) == 0 {
		t.Skip("everything is named")
	}

	labelled := map[string]bool{}
	for _, byLabel := range lab.Species {
		for _, code := range byLabel {
			labelled[code] = true
		}
	}
	for _, s := range lab.Shared {
		labelled[s.Code] = true
	}
	hidden := map[string]bool{}
	for _, h := range lab.Hidden {
		hidden[h] = true
	}

	for _, c := range list {
		if labelled[c.Code] {
			t.Errorf("%s has a label but is in the review queue", c.Code)
		}
		if hidden[c.Code] {
			t.Errorf("%s is hidden but is in the review queue", c.Code)
		}
		if len(c.Dex) == 0 {
			t.Errorf("%s has no eligible species", c.Code)
		}
		// The tab renders these as <img>. If the proxy would refuse one, it shows a broken image.
		file := strings.TrimPrefix(c.SpriteURL, SpritePath)
		if !AllowedFile(file) {
			t.Errorf("%s: the proxy would refuse %q, so the tab would render a broken image", c.Code, file)
		}
	}
}

// Hidden codes are excluded: they are flagged as costumes upstream but are not costumes a trainer
// would record (the Gimmighoul coins), and nagging about them every run is how a review queue
// gets ignored.
func TestHiddenStaysOutOfTheQueue(t *testing.T) {
	for _, c := range Unlabelled() {
		if c.Code == "f:COIN_A1" || c.Code == "f:COIN_A2_2026" {
			t.Errorf("%s is hidden but showed up in the review queue", c.Code)
		}
	}
}

// The admin zoom shows a costume on every species that has it, and builds those URLs with
// SpriteURLFor. If it ever disagreed with AllowedFile, the zoom would render broken images.
func TestSpriteURLForAgreesWithTheProxy(t *testing.T) {
	for code, e := range cat.Codes {
		for _, dex := range e.Dex {
			url, ok := SpriteURLFor(dex, code)
			if !ok {
				t.Errorf("%s: no sprite for dex %d, which the catalog says has the art", code, dex)
				continue
			}
			if !AllowedFile(strings.TrimPrefix(url, SpritePath)) {
				t.Errorf("%s: the proxy would refuse %s", code, url)
			}
		}
	}

	// A species the costume has no art for must yield nothing rather than a URL that 404s.
	if _, ok := SpriteURLFor(25, "c:SPRING_2020_NOEVOLVE"); ok {
		t.Error("returned a sprite for Pikachu, which never had the Spring 2020 visor")
	}
	if _, ok := SpriteURLFor(6, "c:TOTALLY_FAKE"); ok {
		t.Error("returned a sprite for a code that does not exist")
	}
}

// The proxy must only ever fetch filenames the catalog can produce; anything else is an open
// proxy onto the asset host.
func TestAllowedFileGatesTheProxy(t *testing.T) {
	if !AllowedFile("pm6.cSPRING_2020_NOEVOLVE.s.icon.png") {
		t.Error("rejected a real catalog sprite")
	}
	for _, bad := range []string{
		"pm25.cSPRING_2020_NOEVOLVE.s.icon.png", // real code, ineligible species
		"pm6.cTOTALLY_FAKE.s.icon.png",          // invented code
		"pm6.cSPRING_2020_NOEVOLVE.icon.png",    // non-shiny; we never serve it
		"../../../etc/passwd",
		"",
	} {
		if AllowedFile(bad) {
			t.Errorf("proxy would have fetched %q", bad)
		}
	}
}

// dexOfSpecies finds a dex the species actually has art for, by asking the catalog.
func dexOfSpecies(species string) int {
	for _, code := range lab.Species[species] {
		if e, ok := cat.Codes[code]; ok && len(e.Dex) > 0 {
			// Any dex the code covers will do only if it is really this species; the label test
			// below re-checks resolution, so pick the dex that resolves.
			for _, d := range e.Dex {
				if _, ok := resolve(d, species, labelFor(species, code)); ok {
					return d
				}
			}
		}
	}
	return 0
}

func labelFor(species, code string) string {
	for label, c := range lab.Species[species] {
		if c == code {
			return label
		}
	}
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// A species must never be offered the same ART under two labels, whichever tier the labels came
// from. Otherwise the picker shows one sprite twice under two names and whichever a trainer picks
// is a coin toss, and on the mobile API, which serves LabelsForDex's output precomputed, it arrives
// as two identical rows.
//
// Two sources can cause it. A shared costume colliding with a species' own override is what the
// usedCodes set in LabelsForDex guards against. Two OVERRIDE labels on one code is a data mistake
// instead, and the file says so itself: labels.json is append-only because a label is user data, so
// a costume being renamed keeps its old spelling in "aliases", not as a second override. Delibird
// carried "Holiday Ribbon" and "Holiday Outfit" on f:WINTER_2020 until the ribbon was retired into
// the alias map, which is why this checks both tiers rather than only the one with a guard.
//
// Every dex a species' codes cover is checked, rather than one guessed dex: costume codes are
// shared across species (f:WINTER_2020 covers Pikachu, Delibird, Cubchoo and Beartic), so there is
// no single right answer to "which dex is this species" inside this package.
func TestNoSpeciesIsOfferedTheSameArtTwice(t *testing.T) {
	for species, byLabel := range lab.Species {
		dexes := map[int]bool{}
		for _, code := range byLabel {
			if e, ok := cat.Codes[code]; ok {
				for _, d := range e.Dex {
					dexes[d] = true
				}
			}
		}
		for dex := range dexes {
			seen := map[string]string{} // sprite url -> the label that claimed it first
			for _, label := range LabelsForDex(dex, species) {
				url, ok := SpriteURL(dex, species, label)
				if !ok {
					continue
				}
				if first, clash := seen[url]; clash {
					t.Errorf("%s (dex %d): %q and %q are the same art (%s); retire one into aliases",
						species, dex, first, label, url)
					continue
				}
				seen[url] = label
			}
		}
	}
}

// A retired label must keep resolving: user_shinies.costume is free text, so a trainer who recorded
// a Delibird before the rename has "Holiday Ribbon" saved and their costume art has to survive it.
// The whole family carries "Holiday Outfit" for this code, which is why the ribbon was the spelling
// that moved.
func TestRetiredDelibirdLabelStillResolves(t *testing.T) {
	ribbon, ok := SpriteURL(225, "Delibird", "Holiday Ribbon")
	if !ok {
		t.Fatal("a Delibird saved as \"Holiday Ribbon\" lost its costume art")
	}
	outfit, ok := SpriteURL(225, "Delibird", "Holiday Outfit")
	if !ok {
		t.Fatal("Delibird Holiday Outfit did not resolve")
	}
	if ribbon != outfit {
		t.Errorf("the alias resolves elsewhere: %s vs %s", ribbon, outfit)
	}
	// The family it was aligned with must be unaffected: the alias map is global, so a rewrite in
	// the wrong direction would have stranded these three.
	for _, s := range []struct {
		dex     int
		species string
	}{{25, "Pikachu"}, {613, "Cubchoo"}, {614, "Beartic"}} {
		if _, ok := SpriteURL(s.dex, s.species, "Holiday Outfit"); !ok {
			t.Errorf("%s lost Holiday Outfit", s.species)
		}
	}
	// And the picker offers it once, not twice.
	if got := LabelsForDex(225, "Delibird"); len(got) != 1 || got[0] != "Holiday Outfit" {
		t.Errorf("Delibird picker offers %v, want exactly [Holiday Outfit]", got)
	}
}

// AliasesFor is the reverse of the aliases map, and the mobile picker MATCHES on what it returns
// while SHOWING the canonical label. Four spellings point at the lab coat, two of them with a curly
// apostrophe, and every one of them has to come back: a trainer searching the name the game uses
// finding nothing is the bug the alias map was added to prevent.
func TestAliasesForReversesTheAliasMap(t *testing.T) {
	for alias, canonical := range lab.Aliases {
		got := AliasesFor(canonical)
		if !slices.Contains(got, alias) {
			t.Errorf("AliasesFor(%q) = %v, missing the alias %q that resolves to it", canonical, got, alias)
		}
	}
	if got := AliasesFor("Willow's Lab Coat"); len(got) != 4 {
		t.Errorf("Willow's Lab Coat: want 4 spellings, got %d (%v)", len(got), got)
	}
	// A canonical label with no aliases, and the empty string, must not invent one.
	if got := AliasesFor("Party Hat"); len(got) != 0 {
		t.Errorf("Party Hat has no aliases, got %v", got)
	}
	if got := AliasesFor(""); got != nil {
		t.Errorf(`AliasesFor("") = %v, want nil`, got)
	}
}
