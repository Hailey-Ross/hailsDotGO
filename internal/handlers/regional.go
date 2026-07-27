package handlers

import (
	"sort"
	"strconv"
)

// This file mirrors ts/shared/regionalForms.ts. It maps a base species name
// plus region to the PokeAPI variant pokemon id so public profile sprites
// resolve at the same shiny/{id}.png URL scheme as base dex ids. Species names
// use PoGoAPI naming: curly apostrophe in Farfetch’d, "Mr. Mime" with a space.
//
// The server still does not FILTER by shiny availability (a sprite lookup for a
// not yet shiny form is harmless), but it now needs the defaults so the admin
// panel can list every form and let an admin flip one without a deploy. See
// regionalShinyUnreleased below.
//
// Keep this file and the TS one in sync. That used to be a comment and a hope;
// scripts/check-regional-form-parity.mjs now enforces it.

var validRegions = map[string]bool{
	"":         true,
	"alolan":   true,
	"galarian": true,
	"hisuian":  true,
	"paldean":  true,
	// Paldean Tauros has three breeds with distinct shiny sprites, so each is its own card.
	// Plain "paldean" stays valid: Wooper still uses it, as may older Tauros entries.
	"paldean_combat": true,
	"paldean_blaze":  true,
	"paldean_aqua":   true,
	// Not geographic regions: alternate battle formes with a released GO shiny
	// that reuse this axis to add extra collectible cards per species (Therian
	// for the Forces of Nature, Origin for Giratina/Dialga/Palkia,
	// Attack/Defense/Speed for Deoxys, Sky for Shaymin).
	"therian": true,
	"origin":  true,
	"attack":  true,
	"defense": true,
	"speed":   true,
	"sky":     true,
	// Fusion and Crowned formes (base dex card is the default forme).
	"dusk_mane":      true,
	"dawn_wings":     true,
	"crowned_sword":  true,
	"crowned_shield": true,
	// Kyurem fusions, Keldeo, and other alternate formes with distinct sprites.
	"black":         true,
	"white":         true,
	"resolute":      true,
	"midnight":      true,
	"dusk":          true,
	"sandy_cloak":   true,
	"trash_cloak":   true,
	"low_key":       true,
	"pom_pom":       true,
	"pau":           true,
	"sensu":         true,
	"blue_striped":  true,
	"white_striped": true,
	"wash":          true,
	// Rotom appliance formes without a released shiny yet (kept for sprite parity).
	"heat":  true,
	"frost": true,
	"fan":   true,
	"mow":   true,
}

var regionalVariantID = map[string]map[string]int{
	// Alolan
	"Rattata":    {"alolan": 10091},
	"Raticate":   {"alolan": 10092},
	"Raichu":     {"alolan": 10100},
	"Sandshrew":  {"alolan": 10101},
	"Sandslash":  {"alolan": 10102},
	"Vulpix":     {"alolan": 10103},
	"Ninetales":  {"alolan": 10104},
	"Diglett":    {"alolan": 10105},
	"Dugtrio":    {"alolan": 10106},
	"Meowth":     {"alolan": 10107, "galarian": 10161},
	"Persian":    {"alolan": 10108},
	"Geodude":    {"alolan": 10109},
	"Graveler":   {"alolan": 10110},
	"Golem":      {"alolan": 10111},
	"Grimer":     {"alolan": 10112},
	"Muk":        {"alolan": 10113},
	"Exeggutor":  {"alolan": 10114},
	"Marowak":    {"alolan": 10115},
	// Galarian (Meowth listed above)
	"Ponyta":     {"galarian": 10162},
	"Rapidash":   {"galarian": 10163},
	"Slowpoke":   {"galarian": 10164},
	"Slowbro":    {"galarian": 10165},
	"Farfetch’d": {"galarian": 10166},
	"Weezing":    {"galarian": 10167},
	"Mr. Mime":   {"galarian": 10168},
	"Articuno":   {"galarian": 10169},
	"Zapdos":     {"galarian": 10170},
	"Moltres":    {"galarian": 10171},
	"Slowking":   {"galarian": 10172},
	"Corsola":    {"galarian": 10173},
	"Zigzagoon":  {"galarian": 10174},
	"Linoone":    {"galarian": 10175},
	"Darumaka":   {"galarian": 10176},
	"Darmanitan": {"galarian": 10177},
	"Yamask":     {"galarian": 10179},
	"Stunfisk":   {"galarian": 10180},
	// Hisuian
	"Growlithe":  {"hisuian": 10229},
	"Arcanine":   {"hisuian": 10230},
	"Voltorb":    {"hisuian": 10231},
	"Electrode":  {"hisuian": 10232},
	"Typhlosion": {"hisuian": 10233},
	"Qwilfish":   {"hisuian": 10234},
	"Sneasel":    {"hisuian": 10235},
	"Samurott":   {"hisuian": 10236},
	"Lilligant":  {"hisuian": 10237},
	"Zorua":      {"hisuian": 10238},
	"Zoroark":    {"hisuian": 10239},
	"Braviary":   {"hisuian": 10240},
	"Avalugg":    {"hisuian": 10243},
	"Decidueye":  {"hisuian": 10244},
	// Paldean
	"Wooper": {"paldean": 10253},
	"Tauros": {"paldean_combat": 10250, "paldean_blaze": 10251, "paldean_aqua": 10252},
	// Therian (Forces of Nature); base dex sprite is the Incarnate form.
	// Enamorus is omitted: its shiny is not released in Pokemon GO.
	"Tornadus":  {"therian": 10019},
	"Thundurus": {"therian": 10020},
	"Landorus":  {"therian": 10021},
	// Alternate battle formes; base dex sprite is the default forme
	// (Normal Deoxys, Altered Giratina/Dialga/Palkia, Land Shaymin).
	"Deoxys":   {"attack": 10001, "defense": 10002, "speed": 10003},
	"Giratina": {"origin": 10007},
	"Dialga":   {"origin": 10245},
	"Palkia":   {"origin": 10246},
	"Shaymin":  {"sky": 10006},
	// Fusion formes (base dex sprite is the default forme).
	"Necrozma": {"dusk_mane": 10155, "dawn_wings": 10156},
	// Crowned formes (base dex sprite is the Hero of Many Battles forme).
	"Zacian":    {"crowned_sword": 10188},
	"Zamazenta": {"crowned_shield": 10189},
	// Kyurem fusions, Keldeo, Lycanroc, Wormadam, Toxtricity, Oricorio,
	// Basculin, and Rotom appliance formes (base dex sprite is the default forme).
	"Kyurem":     {"black": 10022, "white": 10023},
	"Keldeo":     {"resolute": 10024},
	"Lycanroc":   {"midnight": 10126, "dusk": 10152},
	"Wormadam":   {"sandy_cloak": 10004, "trash_cloak": 10005},
	"Toxtricity": {"low_key": 10184},
	"Oricorio":   {"pom_pom": 10123, "pau": 10124, "sensu": 10125},
	"Basculin":   {"blue_striped": 10016, "white_striped": 10247},
	"Rotom":      {"wash": 10009, "heat": 10008, "frost": 10010, "fan": 10011, "mow": 10012},
}

// regionalShinyUnreleased lists the species/region pairs whose shiny is NOT yet released in
// Pokemon GO. Every other pair in regionalVariantID, plus every Unown letter and every Vivillon
// pattern, defaults to released.
//
// Storing only the exceptions is deliberate: the alternative is restating 133 booleans that are
// almost all true, which is three lines of noise per genuine fact and drifts the moment someone
// adds a form. The parity check script asserts this map matches the shiny:false rows in
// ts/shared/regionalForms.ts exactly.
//
// This is only the DEFAULT. An admin override in shiny_dex_overrides beats it, which is the whole
// point: when Hisuian Zorua's shiny finally ships, that is a checkbox, not a deploy.
var regionalShinyUnreleased = map[string]map[string]bool{
	"Zorua":   {"hisuian": true},
	"Zoroark": {"hisuian": true},
	// Only Wash has a released shiny as of 2026-07.
	"Rotom": {"heat": true, "frost": true, "fan": true, "mow": true},
	// Event exclusive patterns whose art exists but whose shiny has never been obtainable.
	"Vivillon": {"viv_fancy": true, "viv_poke_ball": true},
}

// regionalShinyDefault reports the compiled-in shiny availability for a species/region pair.
func regionalShinyDefault(species, region string) bool {
	return !regionalShinyUnreleased[species][region]
}

// RegionalFormRow is one row of the admin regional form editor.
type RegionalFormRow struct {
	Species string
	Region  string
	Slug    string // PokeAPI sprite slug, e.g. "10238" or "201-b"
}

// regionalFormRows enumerates every regional form that gets its own checklist card, in a stable
// order. Scatterbug and Spewpa are absent by construction: their patterns are carry-only and live
// solely on the TS side, and they inherit Vivillon's flags rather than carrying their own.
func regionalFormRows() []RegionalFormRow {
	out := make([]RegionalFormRow, 0, len(regionalVariantID)+len(unownSpriteSlug)+len(vivillonSpriteSlug))
	for species, regions := range regionalVariantID {
		for region, id := range regions {
			out = append(out, RegionalFormRow{Species: species, Region: region, Slug: strconv.Itoa(id)})
		}
	}
	for region, slug := range unownSpriteSlug {
		out = append(out, RegionalFormRow{Species: "Unown", Region: region, Slug: slug})
	}
	for region, slug := range vivillonSpriteSlug {
		out = append(out, RegionalFormRow{Species: "Vivillon", Region: region, Slug: slug})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Species != out[j].Species {
			return out[i].Species < out[j].Species
		}
		return out[i].Region < out[j].Region
	})
	return out
}

// regionalFormExists reports whether a species/region pair is a real form, so the admin API can
// 404 a write rather than storing an override row nothing will ever read.
func regionalFormExists(species, region string) bool {
	if region == "" {
		return false
	}
	return regionalSpriteSlug(species, region) != ""
}

// regionalSpriteID returns the PokeAPI variant id for a species plus region,
// or 0 when the pair is unknown or region is empty.
func regionalSpriteID(species, region string) int {
	if region == "" {
		return 0
	}
	return regionalVariantID[species][region]
}

// unownSpriteSlug is the one variant family that cannot use a numeric variant id.
//
// PokeAPI files the Unown letters as pokemon-FORM records rather than pokemon records, so
// unlike Alolan Rattata (10091) they have no id of their own: their art is filed under a
// string slug instead. Letter A is the default form, so it is the plain 201.
//
// The region tags mirror UNOWN_LETTERS in ts/shared/regionalForms.ts. Keep them under 16
// characters: user_shinies.region is VARCHAR(16), which is why ! and ? are spelled excl and
// qmark rather than exclamation and question.
var unownSpriteSlug = func() map[string]string {
	m := map[string]string{
		"unown_excl":  "201-exclamation",
		"unown_qmark": "201-question",
	}
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		slug := "201-" + string(c)
		if c == 'a' {
			slug = "201"
		}
		m["unown_"+string(c)] = slug
	}
	return m
}()

// vivillonSpriteSlug maps each viv_* pattern tag to its PokeAPI sprite slug.
//
// Like the Unown letters, Vivillon's 20 patterns are filed as pokemon-FORM records of dex 666,
// so their art lives at a string slug (666-polar) rather than a numeric variant id. The Meadow
// pattern is the default form, so it is the plain 666. Only Vivillon carries patterns; Scatterbug
// and Spewpa look identical across every region.
//
// The tags mirror VIVILLON_PATTERNS in ts/shared/regionalForms.ts. Keep them under 16 characters:
// user_shinies.region is VARCHAR(16).
var vivillonSpriteSlug = map[string]string{
	"viv_meadow":      "666",
	"viv_polar":       "666-polar",
	"viv_tundra":      "666-tundra",
	"viv_continental": "666-continental",
	"viv_garden":      "666-garden",
	"viv_elegant":     "666-elegant",
	"viv_icy_snow":    "666-icy-snow",
	"viv_modern":      "666-modern",
	"viv_marine":      "666-marine",
	"viv_archipelago": "666-archipelago",
	"viv_high_plains": "666-high-plains",
	"viv_sandstorm":   "666-sandstorm",
	"viv_river":       "666-river",
	"viv_monsoon":     "666-monsoon",
	"viv_savanna":     "666-savanna",
	"viv_sun":         "666-sun",
	"viv_ocean":       "666-ocean",
	"viv_jungle":      "666-jungle",
	"viv_fancy":       "666-fancy",
	"viv_poke_ball":   "666-poke-ball",
}

func init() {
	for region := range unownSpriteSlug {
		validRegions[region] = true
	}
	for region := range vivillonSpriteSlug {
		validRegions[region] = true
	}
}

// regionalSpriteSlug returns the PokeAPI sprite slug for a species plus region, or "" when
// the pair is unknown or region is empty. Everything except the Unown letters resolves to a
// plain numeric id.
func regionalSpriteSlug(species, region string) string {
	if region == "" {
		return ""
	}
	if species == "Unown" {
		return unownSpriteSlug[region]
	}
	if species == "Vivillon" {
		return vivillonSpriteSlug[region]
	}
	if id := regionalVariantID[species][region]; id != 0 {
		return strconv.Itoa(id)
	}
	return ""
}
