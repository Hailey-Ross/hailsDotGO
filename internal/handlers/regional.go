package handlers

import "strconv"

// This file mirrors ts/shared/regionalForms.ts. It maps a base species name
// plus region to the PokeAPI variant pokemon id so public profile sprites
// resolve at the same shiny/{id}.png URL scheme as base dex ids. Keep the two
// files in sync; shiny availability flags live only on the TS side because the
// server never filters by them (sprite lookup for a not yet shiny form is
// harmless). Species names use PoGoAPI naming: curly apostrophe in Farfetch’d,
// "Mr. Mime" with a space.

var validRegions = map[string]bool{
	"":         true,
	"alolan":   true,
	"galarian": true,
	"hisuian":  true,
	"paldean":  true,
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
	"Tauros": {"paldean": 10250},
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
