package handlers

import (
	"encoding/json"
	"testing"
)

func TestCleanOCRNameKeepsWhatASpeciesNameContains(t *testing.T) {
	cases := []struct{ in, want string }{
		// The reason this function changed. A dropped digit made Porygon2 read as
		// Porygon, and it was then matched, solved and scored as a Porygon with
		// nothing anywhere reporting a problem.
		{"Porygon2", "Porygon2"},
		{"Porygon-Z", "Porygon-Z"},
		// Punctuation the stat list actually spells names with.
		{"Farfetch’d", "Farfetch’d"},
		{"Farfetch'd", "Farfetch'd"},
		{"Mr. Mime", "Mr. Mime"},
		{"Mime Jr.", "Mime Jr."},
		{"Type: Null", "Type: Null"},
		{"Nidoran♀", "Nidoran♀"},
		{"Nidoran♂", "Nidoran♂"},
		// Accents the game draws.
		{"Flabébé", "Flabébé"},
		// Non Latin scripts used to be deleted outright, leaving an empty name.
		{"ガラガラ", "ガラガラ"},
		{"ピカチュウ", "ピカチュウ"},
		// Whitespace still collapses, and the junk around a real name still goes.
		{"  Kartana  ", "Kartana"},
		{"Kartana\t\n", "Kartana"},
		{"(Kartana)", "Kartana"},
	}
	for _, c := range cases {
		if got := cleanOCRName(c.in); got != c.want {
			t.Errorf("cleanOCRName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectNameIgnoresANumberOnlyLine(t *testing.T) {
	// Keeping digits means a stat line survives the clean where it used to come
	// out empty, and no exclusion word matches a bare number. The card zone is
	// 35% to 65% of the height, so this line is inside it and is the tallest.
	// Every decoy here is INSIDE the 35% to 65% card band and TALLER than the
	// species line, which is the case that actually bites: the game draws CP
	// large, and a cropped or landscape capture slides it into the band. A test
	// that parks the decoy outside the band proves nothing.
	lines := []ocrLine{
		{Text: "185,852", X1: 300, Y1: 850, X2: 800, Y2: 1000},
		{Text: "CP1964", X1: 300, Y1: 1000, X2: 800, Y2: 1150},
		{Text: "HP140/140", X1: 300, Y1: 1150, X2: 800, Y2: 1300},
		{Text: "Kartana", X1: 400, Y1: 1320, X2: 680, Y2: 1370},
	}
	name, _ := detectName(lines, "185,852\nCP1964\nHP140/140\nKartana", 2340)
	if name != "Kartana" {
		t.Errorf("got %q, want Kartana: a stat line outbid the species", name)
	}
}

func TestDetectNameIgnoresLocalizedUILabels(t *testing.T) {
	// Widening the clean to keep non Latin text means a Japanese UI label now
	// survives it where it used to be deleted to empty, so the exclusion list has
	// to know these too. Each decoy is in the card band and taller than the name.
	for _, label := range []string{"ほしのすな", "アメ", "こうげき", "強化"} {
		lines := []ocrLine{
			{Text: label, X1: 300, Y1: 950, X2: 800, Y2: 1100},
			{Text: "カイリキー", X1: 400, Y1: 1120, X2: 680, Y2: 1170},
		}
		name, _ := detectName(lines, label+"\nカイリキー", 2340)
		if name != "カイリキー" {
			t.Errorf("decoy %q: got %q, want the species line", label, name)
		}
	}
}

func TestDetectNameAcceptsALongJapaneseName(t *testing.T) {
	// The length bound is counted in runes. At three bytes a character a byte
	// bound of 20 would reject anything past six characters.
	lines := []ocrLine{
		{Text: "CP1964", X1: 380, Y1: 120, X2: 640, Y2: 200},
		{Text: "ガラガラアローラ", X1: 360, Y1: 950, X2: 720, Y2: 1030},
	}
	name, source := detectName(lines, "CP1964\nガラガラアローラ", 2340)
	if name != "ガラガラアローラ" || source != "card" {
		t.Errorf("got name=%q source=%q, want the Japanese name from the card", name, source)
	}
}

// statList is the shape findSpecies scans, with the awkward real spellings.
func statList(t *testing.T) []pokemonStatEntry {
	t.Helper()
	var list []pokemonStatEntry
	raw := `[
		{"pokemon_name":"Porygon","form":"Normal","pokemon_id":137},
		{"pokemon_name":"Porygon2","form":"Normal","pokemon_id":233},
		{"pokemon_name":"Farfetch’d","form":"Normal","pokemon_id":83},
		{"pokemon_name":"Flabebe","form":"Normal","pokemon_id":669},
		{"pokemon_name":"Mr. Mime","form":"Normal","pokemon_id":122},
		{"pokemon_name":"Type: Null","form":"Normal","pokemon_id":772},
		{"pokemon_name":"Marowak","form":"Normal","pokemon_id":105},
		{"pokemon_name":"Marowak","form":"Alola","pokemon_id":105},
		{"pokemon_name":"Nidoran♀","form":"Normal","pokemon_id":29,"base_attack":86,"base_defense":89,"base_stamina":146},
		{"pokemon_name":"Nidoran♂","form":"Normal","pokemon_id":32,"base_attack":105,"base_defense":76,"base_stamina":130}
	]`
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("stat list: %v", err)
	}
	return list
}

func TestFindSpeciesFoldsAwkwardSpellings(t *testing.T) {
	list := statList(t)
	cases := []struct{ in, want string }{
		// Exact, which must keep working unchanged.
		{"Porygon", "Porygon"},
		{"Porygon2", "Porygon2"},
		{"marowak", "Marowak"},
		// The reader hands back the straight apostrophe for a name the list
		// spells with the curly one, or drops it entirely.
		{"Farfetch'd", "Farfetch’d"},
		{"Farfetchd", "Farfetch’d"},
		// The game draws the accents; the list does not carry them.
		{"Flabébé", "Flabebe"},
		// A period or colon the reader did not pick up.
		{"Mr Mime", "Mr. Mime"},
		{"Type Null", "Type: Null"},
	}
	for _, c := range cases {
		got := findSpecies(list, c.in)
		if got == nil {
			t.Errorf("findSpecies(%q) found nothing, want %q", c.in, c.want)
			continue
		}
		if got.PokemonName != c.want {
			t.Errorf("findSpecies(%q) = %q, want %q", c.in, got.PokemonName, c.want)
		}
	}
}

func TestFindSpeciesRefusesAnAmbiguousFold(t *testing.T) {
	// Both Nidoran fold to "nidoran" once the gender sign is dropped, and a reader
	// drops that sign routinely because it is a tiny superscript glyph. Answering
	// with either one solves the card against the wrong base stats in silence: the
	// two differ by 19 attack and 16 stamina. Declining is the correct answer, and
	// it is what Store.ResolveSpecies already does.
	list := statList(t)
	if got := findSpecies(list, "Nidoran"); got != nil {
		t.Errorf("findSpecies(Nidoran) = %q, want a refusal: the fold is ambiguous", got.PokemonName)
	}
	if got := findSpeciesForm(list, "Nidoran", "Normal"); got != nil {
		t.Errorf("findSpeciesForm(Nidoran, Normal) = %q, want a refusal", got.PokemonName)
	}
	// The exact spellings still resolve, because the exact tier never consults the fold.
	for _, exact := range []string{"Nidoran♀", "Nidoran♂"} {
		if got := findSpecies(list, exact); got == nil || got.PokemonName != exact {
			t.Errorf("findSpecies(%q) did not resolve to itself", exact)
		}
	}
}

func TestFindSpeciesExactBeatsFold(t *testing.T) {
	// A name that IS in the list exactly must never be answered by another
	// species' loose match. Kept honest by a list where the fold genuinely
	// collides: "Mr. Mime" and "Mr Mime" both fold to "mrmime", so with the tiers
	// the other way round the exact row could lose to the decoy.
	list := append(statList(t), pokemonStatEntry{PokemonName: "Mr Mime", Form: "Normal", PokemonID: 9122})
	got := findSpecies(list, "Mr. Mime")
	if got == nil || got.PokemonName != "Mr. Mime" || got.PokemonID != 122 {
		t.Errorf("findSpecies(Mr. Mime) = %v, want the exact row at dex 122", got)
	}
}

func TestFindSpeciesPrefersNormalForm(t *testing.T) {
	// The existing preference must survive the fold tier being added under it.
	list := statList(t)
	if got := findSpecies(list, "Marowak"); got == nil || got.Form != "Normal" {
		t.Errorf("Marowak resolved to form %v, want Normal", got)
	}
}

func TestFindSpeciesFormKeepsTheFormAcrossAFold(t *testing.T) {
	list := statList(t)
	got := findSpeciesForm(list, "marowak", "Alola")
	if got == nil || got.Form != "Alola" {
		t.Errorf("findSpeciesForm(marowak, Alola) = %v, want the Alola row", got)
	}
}

func TestFindSpeciesRefusesJunk(t *testing.T) {
	list := statList(t)
	for _, in := range []string{"", "   ", "185852", "not a pokemon"} {
		if got := findSpecies(list, in); got != nil {
			t.Errorf("findSpecies(%q) = %q, want nothing", in, got.PokemonName)
		}
	}
}
