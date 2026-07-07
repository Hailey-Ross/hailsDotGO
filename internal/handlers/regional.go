package handlers

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
}

// regionalSpriteID returns the PokeAPI variant id for a species plus region,
// or 0 when the pair is unknown or region is empty.
func regionalSpriteID(species, region string) int {
	if region == "" {
		return 0
	}
	return regionalVariantID[species][region]
}
