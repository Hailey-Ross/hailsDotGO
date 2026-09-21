package handlers

import (
	"encoding/json"
	"os"
	"strings"
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
		{"Necrozma", "dusk_mane", 10155},
		{"Necrozma", "dawn_wings", 10156},
		{"Zacian", "crowned_sword", 10188},
		{"Zamazenta", "crowned_shield", 10189},
		{"Kyurem", "black", 10022},
		{"Kyurem", "white", 10023},
		{"Keldeo", "resolute", 10024},
		{"Lycanroc", "midnight", 10126},
		{"Lycanroc", "dusk", 10152},
		{"Wormadam", "sandy_cloak", 10004},
		{"Wormadam", "trash_cloak", 10005},
		{"Toxtricity", "low_key", 10184},
		{"Oricorio", "pau", 10124},
		{"Basculin", "white_striped", 10247},
		{"Rotom", "wash", 10009},
		{"Rotom", "heat", 10008},
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
	for _, ok := range []string{"", "alolan", "galarian", "hisuian", "paldean", "therian", "origin", "attack", "defense", "speed", "sky", "dusk_mane", "dawn_wings", "crowned_sword", "crowned_shield", "black", "white", "resolute", "midnight", "dusk", "sandy_cloak", "trash_cloak", "low_key", "pom_pom", "pau", "sensu", "blue_striped", "white_striped", "wash", "heat", "frost", "fan", "mow"} {
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

// The bundle and the region tables are two vocabularies for one idea, and a box row is
// stored in the first while every sprite table here is keyed by the second. The pairs that
// differ by more than case are the whole reason the fold exists, so they are pinned.
func TestRegionTagForBundleForm(t *testing.T) {
	cases := []struct {
		species, form, want string
	}{
		// Case alone, which is most of the bundle.
		{"Zacian", "Crowned_sword", "crowned_sword"},
		{"Necrozma", "Dusk_mane", "dusk_mane"},
		{"Basculin", "Blue_striped", "blue_striped"},
		{"Deoxys", "Attack", "attack"},
		{"Slowpoke", "Galarian", "galarian"},

		// Place against adjective.
		{"Rattata", "Alola", "alolan"},
		{"Wooper", "Paldea", "paldean"},
		{"Tauros", "Paldea_aqua", "paldean_aqua"},
		{"Tauros", "Paldea_blaze", "paldean_blaze"},
		{"Tauros", "Paldea_combat", "paldean_combat"},

		// Two spellings of one form. Both of these have art of their own, so folding
		// them wrong shows the base species instead.
		{"Oricorio", "Pompom", "pom_pom"},
		{"Darmanitan", "Galarian_standard", "galarian"},

		// Base forms are not regions. "" is the answer, not "normal".
		{"Charizard", "Normal", ""},
		{"Zacian", "Hero", ""},
		{"Palafin", "Hero", ""},
		{"Giratina", "Altered", ""},
		{"Darmanitan", "Standard", ""},
		{"Pikachu", "", ""},
		{"Pikachu", "   ", ""},

		// Prefixed families, folded by species.
		{"Unown", "A", "unown_a"},
		{"Unown", "Z", "unown_z"},
		{"Unown", "Exclamation_point", "unown_excl"},
		{"Unown", "Question_mark", "unown_qmark"},
		{"Vivillon", "Meadow", "viv_meadow"},
		{"Vivillon", "High_plains", "viv_high_plains"},
		{"Vivillon", "Pokeball", "viv_poke_ball"},
	}
	for _, c := range cases {
		if got := regionTagForBundleForm(c.species, c.form); got != c.want {
			t.Errorf("regionTagForBundleForm(%q, %q) = %q, want %q", c.species, c.form, got, c.want)
		}
	}
}

// The end to end answer a box row gets, which is what a client actually draws.
//
// Zacian is the case this exists for: both of its bundle rows are dex 888, so without a
// form resolved sprite a stored Crowned Sword renders as Hero of Many Battles.
func TestBoxSpriteURL(t *testing.T) {
	// Our own proxy, not PokeAPI's host. The box has no shiny concept, so these are all
	// the normal directory.
	const base = PokemonSpritePath
	cases := []struct {
		species, form, want string
	}{
		{"Zacian", "Crowned_sword", base + "10188.png"},
		{"Zamazenta", "Crowned_shield", base + "10189.png"},
		{"Rattata", "Alola", base + "10091.png"},
		{"Tauros", "Paldea_aqua", base + "10252.png"},
		{"Oricorio", "Pompom", base + "10123.png"},
		{"Darmanitan", "Galarian_standard", base + "10177.png"},
		{"Unown", "Z", base + "201-z.png"},
		{"Unown", "A", base + "201.png"}, // letter A IS the default form
		{"Vivillon", "Pokeball", base + "666-poke-ball.png"},

		// No art of its own, so the client falls back to the species dex sprite. This
		// is the majority answer and is not a miss.
		{"Zacian", "Hero", ""},
		{"Charizard", "Normal", ""},
		{"Darmanitan", "Galarian_zen", ""},
		{"Necrozma", "Ultra", ""},
		{"Slowpoke", "2020", ""},
		{"Pikachu", "Kariyushi", ""}, // a costume, and costumes have no variant art here
	}
	for _, c := range cases {
		if got := boxSpriteURL(c.species, c.form); got != c.want {
			t.Errorf("boxSpriteURL(%q, %q) = %q, want %q", c.species, c.form, got, c.want)
		}
	}

	// Never the shiny directory: the box has no shiny concept.
	if got := boxSpriteURL("Zacian", "Crowned_sword"); strings.Contains(got, "/shiny/") {
		t.Errorf("boxSpriteURL returned a shiny sprite: %s", got)
	}
	// And never somebody else's host.
	if got := boxSpriteURL("Zacian", "Crowned_sword"); strings.Contains(got, "githubusercontent.com") {
		t.Errorf("boxSpriteURL still hotlinks: %s", got)
	}
}
