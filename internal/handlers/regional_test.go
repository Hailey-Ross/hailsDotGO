package handlers

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRegionalSpriteID(t *testing.T) {
	cases := []struct {
		species, region string
		want            int
	}{
		{"Growlithe", "hisuian", 10229},
		{"Meowth", "galarian", 10161},
		{"Meowth", "alolan", 10107},
		{"Farfetch’d", "galarian", 10166},
		{"Mr. Mime", "galarian", 10168},
		{"Wooper", "paldean", 10253},
		{"Tornadus", "therian", 10019},
		{"Thundurus", "therian", 10020},
		{"Landorus", "therian", 10021},
		{"Deoxys", "attack", 10001},
		{"Deoxys", "defense", 10002},
		{"Deoxys", "speed", 10003},
		{"Giratina", "origin", 10007},
		{"Dialga", "origin", 10245},
		{"Palkia", "origin", 10246},
		{"Shaymin", "sky", 10006},
		{"Tornadus", "", 0},
		{"Meowth", "", 0},
		{"Meowth", "hisuian", 0},
		{"Pikachu", "alolan", 0},
		{"Nonexistent", "galarian", 0},
	}
	for _, c := range cases {
		if got := regionalSpriteID(c.species, c.region); got != c.want {
			t.Errorf("regionalSpriteID(%q, %q) = %d, want %d", c.species, c.region, got, c.want)
		}
	}
}

func TestValidRegions(t *testing.T) {
	for _, ok := range []string{"", "alolan", "galarian", "hisuian", "paldean", "therian", "origin", "attack", "defense", "speed", "sky"} {
		if !validRegions[ok] {
			t.Errorf("region %q should be valid", ok)
		}
	}
	for _, bad := range []string{"kanto", "Alolan", "shadow", "hisui", "galar", "incarnate", "altered", "normal", "land"} {
		if validRegions[bad] {
			t.Errorf("region %q should be invalid", bad)
		}
	}
}

// Every species key in the regional map must exist by exact name in the
// embedded shiny fallback data, so typos (especially apostrophe variants)
// cannot silently break sprite resolution.
func TestRegionalSpeciesNamesMatchShinyData(t *testing.T) {
	data, err := os.ReadFile("../pogodata/fallback/shinies.json")
	if err != nil {
		t.Fatalf("read fallback shinies: %v", err)
	}
	var entries map[string]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse fallback shinies: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name] = true
	}
	for species := range regionalVariantID {
		if !names[species] {
			t.Errorf("regional species %q not found in fallback shiny data (name mismatch?)", species)
		}
	}
}
