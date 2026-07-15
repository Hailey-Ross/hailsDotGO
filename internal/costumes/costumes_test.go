package costumes

import (
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
