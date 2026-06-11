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
