package pogodata

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePokemonNamesCSV(t *testing.T) {
	csvData := "pokemon_species_id,local_language_id,name,genus\n" +
		"1,5,Bulbizarre,Pokémon Graine\n" +
		"1,6,Bisasam,Samen-Pokémon\n" +
		"1,7,Bulbasaur,Pokémon Semilla\n" +
		"1,9,Bulbasaur,Seed Pokémon\n" + // English (id 9) is unsupported, must be skipped
		"1,11,フシギダネ,たねポケモン\n" +
		"122,5,\"M. Mime\",\"Pokémon Barrière\"\n" + // quoted fields
		"bogus,5,Broken,Row\n" + // malformed species id, must be skipped
		"2,notanumber,Broken,Row\n" // malformed language id, must be skipped

	got, err := parsePokemonNamesCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("parsePokemonNamesCSV: %v", err)
	}
	want := map[int]map[string]string{
		1:   {"fr": "Bulbizarre", "de": "Bisasam", "es": "Bulbasaur", "ja": "フシギダネ"},
		122: {"fr": "M. Mime"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d species, want %d (got: %v)", len(got), len(want), got)
	}
	for id, wantLangs := range want {
		gotLangs, ok := got[id]
		if !ok {
			t.Fatalf("species %d missing from result", id)
		}
		if len(gotLangs) != len(wantLangs) {
			t.Fatalf("species %d: got %d langs %v, want %d %v", id, len(gotLangs), gotLangs, len(wantLangs), wantLangs)
		}
		for code, name := range wantLangs {
			if gotLangs[code] != name {
				t.Errorf("species %d lang %s: got %q, want %q", id, code, gotLangs[code], name)
			}
		}
	}
}

func TestApplyResultPokemonNamesPrimaryShape(t *testing.T) {
	s := &Store{}
	data := json.RawMessage(`{"1":{"fr":"Bulbizarre","de":"Bisasam","es":"Bulbasaur","ja":"フシギダネ"},"25":{"fr":"Pikachu","ja":"ピカチュウ"}}`)
	s.mu.Lock()
	s.applyResult("pokemon_names", data)
	s.mu.Unlock()
	if got := s.pokemonNamesById[1]["de"]; got != "Bisasam" {
		t.Errorf("id 1 de: got %q, want %q", got, "Bisasam")
	}
	if got := s.pokemonNamesById[25]["ja"]; got != "ピカチュウ" {
		t.Errorf("id 25 ja: got %q, want %q", got, "ピカチュウ")
	}
	if got := len(s.pokemonNamesById); got != 2 {
		t.Errorf("got %d ids, want 2", got)
	}
}

func TestApplyResultPokemonNamesLegacyFormatC(t *testing.T) {
	s := &Store{}
	data := json.RawMessage(`{"1":{"5":"Bulbizarre","6":"Bisasam","9":"Bulbasaur"}}`)
	s.mu.Lock()
	s.applyResult("pokemon_names", data)
	s.mu.Unlock()
	if got := s.pokemonNamesById[1]["fr"]; got != "Bulbizarre" {
		t.Errorf("legacy C id 1 fr: got %q, want %q", got, "Bulbizarre")
	}
	if _, ok := s.pokemonNamesById[1]["en"]; ok {
		t.Errorf("legacy C: English (lang 9) should not be mapped")
	}
}

func TestApplyResultShiniesMergesSupplement(t *testing.T) {
	s := &Store{}
	upstream := json.RawMessage(`{"1":{"found_egg":true,"found_evolution":false,"found_photobomb":true,"found_raid":true,"found_research":true,"found_wild":true,"id":1,"name":"Bulbasaur"}}`)
	s.mu.Lock()
	s.applyResult("shinies", upstream)
	s.mu.Unlock()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(s.shinies, &m); err != nil {
		t.Fatalf("merged shinies unparseable: %v", err)
	}
	if _, ok := m["1"]; !ok {
		t.Error("upstream entry dex 1 lost after merge")
	}
	// Every embedded supplement entry must be injected (upstream PogoAPI is
	// missing all of them, e.g. the Scorbunny and Sobble lines at dex 813 to 818).
	if len(shinySupplement) == 0 {
		t.Fatal("embedded supplement is empty or failed to load")
	}
	for dex := range shinySupplement {
		if _, ok := m[dex]; !ok {
			t.Errorf("supplement entry dex %s missing after merge", dex)
		}
	}
	var entry struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(m["813"], &entry); err != nil || entry.ID != 813 || entry.Name != "Scorbunny" {
		t.Errorf("dex 813: got id=%d name=%q err=%v, want id=813 name=Scorbunny", entry.ID, entry.Name, err)
	}
}

func TestMergeShinySupplementNullUpstream(t *testing.T) {
	// A literal JSON null body passes fetch validation and unmarshals to a nil
	// map without error; the merge must not panic and must still supply the
	// supplement entries.
	merged := mergeShinySupplement(json.RawMessage(`null`))
	var m map[string]json.RawMessage
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("merged output unparseable: %v", err)
	}
	if _, ok := m["813"]; !ok {
		t.Error("supplement entry dex 813 missing after merging null upstream")
	}
}

func TestApplyResultPokemonNamesEmptyKeepsExisting(t *testing.T) {
	s := &Store{pokemonNamesById: map[int]map[string]string{1: {"fr": "Bulbizarre"}}}
	// Broken modern PoGoAPI shape with no language data must not wipe good data.
	data := json.RawMessage(`{"1":{"id":1,"name":"Bulbasaur"}}`)
	s.mu.Lock()
	s.applyResult("pokemon_names", data)
	s.mu.Unlock()
	if got := s.pokemonNamesById[1]["fr"]; got != "Bulbizarre" {
		t.Errorf("existing data lost: got %q, want %q", got, "Bulbizarre")
	}
}
