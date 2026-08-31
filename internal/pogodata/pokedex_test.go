package pogodata

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bulbasaurRow1 is the literal first data row of
// pokemon_species_flavor_text.csv, kept verbatim because it is a fixture in
// itself: the field is quoted, wraps across four lines mid sentence, and carries
// a form feed after "birth.". A line based parse reads this as five broken rows
// and four parse failures, silently. encoding/csv reads it as one.
const bulbasaurRow1 = "1,1,9,\"A strange seed was\nplanted on its\nback at birth.\fThe plant sprouts\nand grows with\nthis POKéMON.\"\n"

func newTestCSV(s string) *csv.Reader {
	r := csv.NewReader(strings.NewReader(s))
	r.FieldsPerRecord = -1
	return r
}

func TestNormalizePokedexFlavor(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"line breaks", "A strange seed was\nplanted on its\nback.", "A strange seed was planted on its back."},
		{"form feed", "at birth.\fThe plant sprouts", "at birth. The plant sprouts"},
		{"carriage return", "one\r\ntwo", "one two"},
		{"collapses runs", "one     two\n\n\nthree", "one two three"},
		{"trims", "  \n padded \f ", "padded"},
		// The site's extraction runs in a browser, where \s matches a non breaking
		// space. Go's does not, so this is the case that would have rendered
		// differently on the two clients from identical upstream text.
		{"unicode spaces", "one  two", "one two"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizePokedexFlavor(c.in); got != c.want {
				t.Errorf("normalizePokedexFlavor(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestReducePokedexFlavorEmbeddedNewlines(t *testing.T) {
	got, err := reducePokedexFlavor(
		newTestCSV("species_id,version_id,language_id,flavor_text\n"+bulbasaurRow1),
		map[int]string{9: "en"},
		map[int]int{40: 0},
	)
	if err != nil {
		t.Fatalf("reducePokedexFlavor: %v", err)
	}
	want := "A strange seed was planted on its back at birth. The plant sprouts and grows with this POKéMON."
	if got["en"][1] != want {
		t.Errorf("dex 1:\n got %q\nwant %q", got["en"][1], want)
	}
}

func TestReducePokedexFlavorVersionPreference(t *testing.T) {
	// Sword (33) appears FIRST in the file and Scarlet (40) last, so a reduction
	// that kept the first match rather than the best ranked one would pick Sword.
	// Species 2 has no preferred version at all and must fall back to the LAST
	// entry, not the first, which is what ts/shared/pokedex.ts does.
	in := "species_id,version_id,language_id,flavor_text\n" +
		"1,33,9,sword text\n" +
		"1,34,9,shield text\n" +
		"1,40,9,scarlet text\n" +
		"2,1,9,red text\n" +
		"2,3,9,yellow text\n" +
		"2,17,9,black text\n"

	got, err := reducePokedexFlavor(newTestCSV(in), map[int]string{9: "en"},
		map[int]int{40: 0, 41: 1, 33: 2, 34: 3})
	if err != nil {
		t.Fatalf("reducePokedexFlavor: %v", err)
	}
	if got["en"][1] != "scarlet text" {
		t.Errorf("dex 1: got %q, want %q (Scarlet outranks Sword wherever it appears in the file)", got["en"][1], "scarlet text")
	}
	if got["en"][2] != "black text" {
		t.Errorf("dex 2: got %q, want %q (no preferred version, so the LAST entry)", got["en"][2], "black text")
	}
}

func TestReducePokedexFlavorLanguages(t *testing.T) {
	in := "species_id,version_id,language_id,flavor_text\n" +
		"1,40,9,english\n" +
		"1,40,6,german\n" +
		"1,40,3,korean\n" // not a locale this site serves, must be dropped

	got, err := reducePokedexFlavor(newTestCSV(in), map[int]string{9: "en", 6: "de"}, map[int]int{40: 0})
	if err != nil {
		t.Fatalf("reducePokedexFlavor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d languages %v, want 2", len(got), got)
	}
	if got["en"][1] != "english" || got["de"][1] != "german" {
		t.Errorf("got en=%q de=%q, want english/german", got["en"][1], got["de"][1])
	}
}

// TestReadPokedexFlagsByColumnName pins the resolution to header names. The header
// below has the same columns as upstream's in a different order with an extra one,
// which is exactly what a positional read gets silently wrong.
func TestReadPokedexFlagsByColumnName(t *testing.T) {
	in := "generation_id,id,is_mythical,something_new,is_legendary\n" +
		"1,1,0,x,0\n" + // Bulbasaur: neither
		"1,150,0,x,1\n" + // Mewtwo: legendary
		"1,151,1,x,0\n" + // Mew: mythical
		"9,1025,1,x,0\n"

	dex, flags, err := readPokedexFlags(newTestCSV(in))
	if err != nil {
		t.Fatalf("readPokedexFlags: %v", err)
	}
	if len(dex) != 4 || dex[0] != 1 || dex[3] != 1025 {
		t.Fatalf("dex list = %v, want all four sorted", dex)
	}
	// SPARSE: an ordinary species must not appear at all, so an absent key reads as
	// "neither" rather than as "unknown".
	if _, ok := flags[1]; ok {
		t.Errorf("dex 1 is neither legendary nor mythical, so it should not be in the sparse map")
	}
	if !flags[150].Legendary || flags[150].Mythical {
		t.Errorf("dex 150 = %+v, want legendary only", flags[150])
	}
	if flags[151].Legendary || !flags[151].Mythical {
		t.Errorf("dex 151 = %+v, want mythical only", flags[151])
	}
}

func TestReadPokedexFlagsMissingColumn(t *testing.T) {
	// A file that no longer carries is_legendary must fail the refresh, not report
	// every species as ordinary. Everything is legendary in the eyes of a false
	// default; the point is that the last good reduction survives.
	if _, _, err := readPokedexFlags(newTestCSV("id,is_mythical\n1,0\n")); err == nil {
		t.Fatal("readPokedexFlags accepted a file with no is_legendary column")
	}
}

func TestParseSpeciesNamesCSVGenus(t *testing.T) {
	in := "pokemon_species_id,local_language_id,name,genus\n" +
		"1,9,Bulbasaur,Seed Pokémon\n" +
		"1,6,Bisasam,Samen-Pokémon\n" +
		"1,2,Fushigidane,\n" + // roomaji: a name but no genus, must not create an entry
		"122,5,\"M. Mime\",\"Pokémon Barrière\"\n"

	names, genera, err := parseSpeciesNamesCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseSpeciesNamesCSV: %v", err)
	}
	// English is absent from the NAMES half by design (langIDToCode leaves it out,
	// since the store treats the English name as canonical) and present in the
	// genus half, which is the whole reason the two are keyed differently.
	if _, ok := names[1]["en"]; ok {
		t.Errorf("English must not appear in the names map")
	}
	if genera[9][1] != "Seed Pokémon" {
		t.Errorf("English genus = %q, want %q", genera[9][1], "Seed Pokémon")
	}
	if genera[6][1] != "Samen-Pokémon" {
		t.Errorf("German genus = %q, want %q", genera[6][1], "Samen-Pokémon")
	}
	if _, ok := genera[2]; ok {
		t.Errorf("a row with an empty genus must not create a language entry, got %v", genera[2])
	}
	if genera[5][122] != "Pokémon Barrière" {
		t.Errorf("quoted genus = %q", genera[5][122])
	}
	// The names half must be untouched by the genus half being added to it.
	if names[1]["de"] != "Bisasam" || names[122]["fr"] != "M. Mime" {
		t.Errorf("names = %v", names)
	}
}

// pokedexCSVServer stands in for the PokeAPI repo, serving the four files
// fetchPokedexSpecies reads.
func pokedexCSVServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	old := pokeAPICSVBase
	pokeAPICSVBase = srv.URL + "/"
	t.Cleanup(func() { pokeAPICSVBase = old; srv.Close() })
	return srv
}

func testPokedexCSVs() map[string]string {
	return map[string]string{
		"languages.csv": "id,iso639,iso3166,identifier,official,order\n" +
			"5,fr,fr,fr,1,8\n6,de,de,de,1,9\n7,es,es,es,1,10\n9,en,us,en,1,7\n11,ja,jp,ja,1,2\n",
		"versions.csv": "id,version_group_id,identifier\n" +
			"23,15,x\n33,20,sword\n40,25,scarlet\n41,25,violet\n",
		"pokemon_species.csv": "id,identifier,generation_id,is_legendary,is_mythical\n" +
			"1,bulbasaur,1,0,0\n" +
			"150,mewtwo,1,1,0\n" +
			"9999,nothingatall,9,0,0\n", // in the dex, but no genus and no flavour
		"pokemon_species_flavor_text.csv": "species_id,version_id,language_id,flavor_text\n" +
			bulbasaurRow1 +
			"1,40,6,\"Es trägt einen\nSamen auf dem\nRücken.\"\n" +
			"150,33,9,sword mewtwo\n" +
			"150,40,9,scarlet mewtwo\n",
	}
}

func testPokedexGenera() map[int]map[int]string {
	return map[int]map[int]string{
		9: {1: "Seed Pokémon", 150: "Genetic Pokémon"},
		6: {1: "Samen-Pokémon"}, // no German genus for Mewtwo: must fall back to English
	}
}

func TestFetchPokedexSpecies(t *testing.T) {
	pokedexCSVServer(t, testPokedexCSVs())
	s := &Store{client: http.DefaultClient}

	data, n, err := s.fetchPokedexSpecies(testPokedexGenera())
	if err != nil {
		t.Fatalf("fetchPokedexSpecies: %v", err)
	}
	if n != 3 {
		t.Errorf("species count = %d, want 3", n)
	}

	var p pokedexPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Dex) != 3 || p.Dex[2] != 9999 {
		t.Errorf("dex = %v, want all three sorted", p.Dex)
	}
	if !p.Flags["150"].Legendary {
		t.Errorf("dex 150 should be legendary, flags = %v", p.Flags)
	}
	if _, ok := p.Flags["1"]; ok {
		t.Errorf("flags must stay sparse, got an entry for dex 1")
	}
	if got := p.Text["en"]["150"].Flavor; got != "scarlet mewtwo" {
		t.Errorf("dex 150 English flavour = %q, want the Scarlet entry", got)
	}
	if got := p.Text["en"]["1"].Genus; got != "Seed Pokémon" {
		t.Errorf("dex 1 English genus = %q", got)
	}
	// The species with nothing to say must still be IN the dex list and absent from
	// the text maps. That is what makes it a blank entry rather than a missing one.
	if _, ok := p.Text["en"]["9999"]; ok {
		t.Errorf("a species with no text should not get a text entry")
	}
}

// TestFetchPokedexSpeciesNoGenus pins the rule from the top of cpmultipliers.go: a
// refresh that cannot see the whole picture must fail rather than apply a partial
// one over a good one.
func TestFetchPokedexSpeciesNoGenus(t *testing.T) {
	pokedexCSVServer(t, testPokedexCSVs())
	s := &Store{client: http.DefaultClient}

	if _, _, err := s.fetchPokedexSpecies(nil); err == nil {
		t.Fatal("fetchPokedexSpecies built a payload with no genus data at all")
	}
	// Genus for German only: English is what every other locale falls back to, so
	// this is still a partial and must still fail.
	if _, _, err := s.fetchPokedexSpecies(map[int]map[int]string{6: {1: "Samen-Pokémon"}}); err == nil {
		t.Fatal("fetchPokedexSpecies built a payload with no English genus")
	}
}

func TestPokedexApplyAndResolve(t *testing.T) {
	pokedexCSVServer(t, testPokedexCSVs())
	s := &Store{client: http.DefaultClient}
	data, _, err := s.fetchPokedexSpecies(testPokedexGenera())
	if err != nil {
		t.Fatalf("fetchPokedexSpecies: %v", err)
	}
	s.applyResult("pokedex_species", data)

	if s.PokedexVersion() == "" {
		t.Error("PokedexVersion is empty after a successful apply")
	}
	if s.PokedexSize() != 3 {
		t.Errorf("PokedexSize = %d, want 3", s.PokedexSize())
	}

	en := s.PokedexSpecies("en")
	if len(en) != 3 {
		t.Fatalf("English set has %d species, want 3 (every species, blank or not)", len(en))
	}
	if got := en[9999]; got.Genus != "" || got.Flavor != "" || got.Legendary || got.Mythical {
		t.Errorf("a species with no data should resolve to a blank entry, got %+v", got)
	}
	if !en[150].Legendary {
		t.Error("dex 150 should resolve as legendary")
	}

	de := s.PokedexSpecies("de")
	if de[1].Genus != "Samen-Pokémon" {
		t.Errorf("German genus = %q, want the German one", de[1].Genus)
	}
	if !strings.HasPrefix(de[1].Flavor, "Es trägt einen Samen") {
		t.Errorf("German flavour = %q, want the German one normalized", de[1].Flavor)
	}
	// Per FIELD, not per species. Mewtwo has no German genus and no German flavour
	// in the fixture, so both fall back rather than blanking the section.
	if de[150].Genus != "Genetic Pokémon" || de[150].Flavor != "scarlet mewtwo" {
		t.Errorf("German Mewtwo = %+v, want both fields fallen back to English", de[150])
	}
	// And the fallback is per field: Bulbasaur keeps its own German genus above
	// German flavour text, with nothing English leaking in.
	if de[1].Genus == en[1].Genus {
		t.Error("German Bulbasaur genus fell back to English when it had its own")
	}

	one, ok := s.PokedexSpeciesOne(150, "de")
	if !ok || one.Genus != "Genetic Pokémon" {
		t.Errorf("PokedexSpeciesOne(150, de) = %+v, %v", one, ok)
	}
	if _, ok := s.PokedexSpeciesOne(4242, "en"); ok {
		t.Error("PokedexSpeciesOne answered ok for a species upstream does not have")
	}
}

// TestPokedexApplyKeepsLastGood is the same rule as above, one layer down: a
// payload that parses but carries no English must not replace one that does.
func TestPokedexApplyKeepsLastGood(t *testing.T) {
	pokedexCSVServer(t, testPokedexCSVs())
	s := &Store{client: http.DefaultClient}
	good, _, err := s.fetchPokedexSpecies(testPokedexGenera())
	if err != nil {
		t.Fatalf("fetchPokedexSpecies: %v", err)
	}
	s.applyResult("pokedex_species", good)
	wantVersion := s.PokedexVersion()

	for _, bad := range []string{
		`{"dex":[],"flags":{},"text":{"en":{}}}`,
		`{"dex":[1,150],"flags":{},"text":{"de":{"1":{"genus":"Samen-Pokémon"}}}}`,
		`{"not":"the payload at all"`,
	} {
		s.applyResult("pokedex_species", json.RawMessage(bad))
		if s.PokedexSize() != 3 || s.PokedexVersion() != wantVersion {
			t.Fatalf("applying %s replaced the good reduction: size=%d version=%q", bad, s.PokedexSize(), s.PokedexVersion())
		}
	}
}
