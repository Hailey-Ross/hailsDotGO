package handlers

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadPokeList(t *testing.T) []pokemonStatEntry {
	t.Helper()
	raw, err := os.ReadFile("../pogodata/fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read pokemon fallback: %v", err)
	}
	var list []pokemonStatEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("parse pokemon fallback: %v", err)
	}
	return list
}

func loadEvolutions(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../pogodata/fallback/pokemon_evolutions.json")
	if err != nil {
		t.Fatalf("read evolutions fallback: %v", err)
	}
	return raw
}

func knownFromList(list []pokemonStatEntry) func(string) bool {
	set := make(map[string]bool, len(list))
	for i := range list {
		set[strings.ToLower(list[i].PokemonName)] = true
	}
	return func(n string) bool { return set[strings.ToLower(n)] }
}

func TestDetectCandyBase(t *testing.T) {
	known := knownFromList(loadPokeList(t))
	// OCR text shape from the real "John Cena" Machamp scan.
	machampText := "John Cena\n140 / 140 HP\nHundo\n159.37kg\nFIGHTING\n1.54m\n195,002\nSTARDUST\n3\nMACHOP CANDY\n26\nMACHOP CANDY XL\nPOWER UP\n3,000"
	if got := detectCandyBase(machampText, known); !strings.EqualFold(got, "Machop") {
		t.Errorf("machamp scan: got %q, want Machop", got)
	}
	// "Queen Hundo" Altaria: candy line plus mega energy line.
	altariaText := "Queen Hundo\n146 / 146 HP\n185,852\nSTARDUST\n14\nSWABLU CANDY\n41\nSWABLU CANDY XL\n3,700\nALTARIA MEGA ENERGY\nPOWER UP\n5,400"
	if got := detectCandyBase(altariaText, known); !strings.EqualFold(got, "Swablu") {
		t.Errorf("altaria scan: got %q, want Swablu", got)
	}
	// Count glued onto the species token by OCR still resolves.
	if got := detectCandyBase("13 KARTANA CANDY XL", known); !strings.EqualFold(got, "Kartana") {
		t.Errorf("glued count: got %q, want Kartana", got)
	}
	// Non-species candy text must not match.
	if got := detectCandyBase("RARE CANDY", known); got != "" {
		t.Errorf("rare candy: got %q, want empty", got)
	}
}

func TestFamilySpecies(t *testing.T) {
	evo := loadEvolutions(t)
	got := familySpecies("Machop", evo)
	want := []string{"Machop", "Machoke", "Machamp"}
	if len(got) != len(want) {
		t.Fatalf("machop family = %v, want %v", got, want)
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			t.Fatalf("machop family = %v, want %v", got, want)
		}
	}
	swablu := familySpecies("Swablu", evo)
	if len(swablu) != 2 || !strings.EqualFold(swablu[1], "Altaria") {
		t.Errorf("swablu family = %v, want [Swablu Altaria]", swablu)
	}
	// Branching families walk every branch.
	eevee := familySpecies("Eevee", evo)
	if len(eevee) < 9 {
		t.Errorf("eevee family only %d members: %v", len(eevee), eevee)
	}
	// Unknown data degrades to just the base.
	solo := familySpecies("Machop", json.RawMessage("not json"))
	if len(solo) != 1 || solo[0] != "Machop" {
		t.Errorf("fallback family = %v, want [Machop]", solo)
	}
}

// End-to-end nickname flow: candy says the Machop family, and only Machamp's
// stats fit the scanned CP/HP/dust, so the species resolves without any name.
func TestCandyFamilyDisambiguation(t *testing.T) {
	cpms := loadCPMs(t)
	pokeList := loadPokeList(t)
	evo := loadEvolutions(t)

	base := detectCandyBase("3\nMACHOP CANDY\nPOWER UP\n3,000", knownFromList(pokeList))
	if base == "" {
		t.Fatal("candy base not detected")
	}
	req := ivRequest{CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46}
	var fits []string
	for _, name := range familySpecies(base, evo) {
		sp := findSpecies(pokeList, name)
		if sp == nil {
			t.Fatalf("species %q missing from stat list", name)
		}
		if cands, _, _ := runOCRSearch(req, dustCandidates(3000, nil, nil, nil), arcReading{}, *sp, cpms); len(cands) > 0 {
			fits = append(fits, sp.PokemonName)
		}
	}
	if len(fits) != 1 || !strings.EqualFold(fits[0], "Machamp") {
		t.Errorf("family fits = %v, want exactly [Machamp]", fits)
	}
}
