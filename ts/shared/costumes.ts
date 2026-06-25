const POGO_ASSETS = "https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/";

// Maps pokemon name → costume label → {p: url prefix, code: Niantic internal code}
const COSTUME_SPRITES: Record<string, Record<string, {p: "c"|"f", code: string}>> = {
  "Pikachu": {
    "Party Hat":              { p: "c", code: "ANNIVERSARY" },
    "Witch Hat":              { p: "c", code: "HALLOWEEN_2017" },
    "Santa Hat":              { p: "c", code: "HOLIDAY_2016" },
    "Holiday Outfit":         { p: "c", code: "WINTER_2018" },
    "Straw Hat":              { p: "c", code: "SUMMER_2018" },
    "Detective Hat":          { p: "c", code: "MAY_2019_NOEVOLVE" },
    "Flower Crown":           { p: "c", code: "FEB_2019" },
    "Cake Hat":               { p: "c", code: "ANNIVERSARY_2022_NOEVOLVE" },
    "Safari Hat":             { p: "c", code: "SAFARI_2020_NOEVOLVE" },
    "Top Hat":                { p: "c", code: "JAN_2023_NOEVOLVE" },
    "Rock Star":              { p: "f", code: "ROCK_STAR" },
    "Pop Star":               { p: "f", code: "POP_STAR" },
    "Kariyushi Shirt":        { p: "f", code: "KARIYUSHI" },
    "Blue Shirt":             { p: "f", code: "JEJU" },
    "Lucas's Hat":            { p: "f", code: "GOTOUR_2024_A" },
    "Dawn's Hat":             { p: "f", code: "GOTOUR_2024_B" },
    "Akari's Kerchief":       { p: "f", code: "GOTOUR_2024_B_02" },
    "Hilbert's Hat":          { p: "f", code: "GOTOUR_2025_A" },
    "Hilda's Hat":            { p: "f", code: "GOTOUR_2025_A_02" },
    "Nate's Visor":           { p: "f", code: "GOTOUR_2025_B" },
    "Rosa's Visor":           { p: "f", code: "GOTOUR_2025_B_02" },
  },
  "Pichu":     { "Santa Hat":               { p: "c", code: "HOLIDAY_2016" } },
  "Raichu":    { "Santa Hat":               { p: "c", code: "HOLIDAY_2016" } },
  "Espeon":    { "Day Scarf":               { p: "f", code: "GOFEST_2024_SSCARF" } },
  "Umbreon":   { "Night Scarf":             { p: "f", code: "GOFEST_2024_MSCARF" } },
  "Squirtle":  { "Sunglasses":              { p: "f", code: "FALL_2019" } },
  "Wartortle": { "Sunglasses":              { p: "c", code: "SUMMER_2018" } },
  "Blastoise": { "Sunglasses":              { p: "c", code: "SUMMER_2018" } },
  "Snorlax":   { "Nightcap":               { p: "c", code: "NIGHTCAP" } },
  "Slowpoke":  { "New Year Costume":        { p: "c", code: "PI_NOEVOLVE" } },
  "Jigglypuff":{ "Ribbon":                  { p: "c", code: "JAN_2024" } },
  "Wigglytuff":{ "Ribbon":                  { p: "c", code: "JAN_2024" } },
  "Noibat":    { "Headband":               { p: "c", code: "HALLOWEEN_2025" } },
  "Noivern":   { "Headband":               { p: "c", code: "HALLOWEEN_2025" } },
  "Cubchoo":   { "Holiday Outfit":          { p: "f", code: "WINTER_2020" } },
  "Beartic":   { "Holiday Outfit":          { p: "f", code: "WINTER_2020" } },
  "Delibird":  {
    "Holiday Ribbon":         { p: "f", code: "WINTER_2020" },
    "Holiday Outfit":         { p: "f", code: "WINTER_2020" },
  },
  "Spheal":    { "Festive Outfit":          { p: "c", code: "HOLIDAY_2021_NOEVOLVE" } },
  "Psyduck":   { "Holiday Attire":          { p: "c", code: "HOLIDAY_2023" } },
  "Gengar":    { "Halloween Costume":       { p: "f", code: "COSTUME_2020" } },
  "Lapras":    { "Drip Scarf":              { p: "f", code: "COSTUME_2020" } },
  "Dragonite": { "Bowtie & Sunglasses":     { p: "c", code: "FALL_2023" } },
  "Minccino":  { "Fashion Outfit":          { p: "c", code: "FASHION_2025" } },
  "Cinccino":  { "Fashion Outfit":          { p: "c", code: "FASHION_2025" } },
  "Shinx":     { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Luxio":     { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Luxray":    { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Kirlia":    { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Gardevoir": { "Fashion Outfit":          { p: "c", code: "GOFEST_2021_NOEVOLVE" } },
  "Croagunk":  { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Toxicroak": { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Diglett":   { "Fashion Outfit":          { p: "c", code: "FALL_2022" } },
  "Butterfree":{ "Fashion Outfit":          { p: "c", code: "FASHION_2021_NOEVOLVE" } },
  "Aerodactyl":{ "Satchel":                 { p: "f", code: "SUMMER_2023" } },
  "Cubone":    { "Cempasúchil Crown":       { p: "c", code: "FALL_2023" } },
  "Ponyta":    { "Candela Costume":         { p: "c", code: "SPRING_2023_VALOR" } },
  "Vulpix":    { "Spooky Festival Costume": { p: "c", code: "FALL_2022" } },
};

export const TINY_POKEMON = new Set<number>([
  // Baby Pokemon
  172, 173, 174, 175, 236, 238, 239, 240,
  298, 360, 406, 433, 438, 439, 440, 446, 447, 458,
  // Small mythicals and user-called-out tiny Pokemon
  151, 251, 311, 312, 385, 490, 494,
  // Other notoriously small sprites
  595,       // Joltik
  669, 670,  // Flabebe, Floette
  742, 743,  // Cutiefly, Ribombee
]);

export function costumeShinyUrl(dexId: number, pokemonName: string, costumeLabel: string): string | null {
  const entry = COSTUME_SPRITES[pokemonName]?.[costumeLabel];
  if (!entry) return null;
  return `${POGO_ASSETS}pm${dexId}.${entry.p}${entry.code}.s.icon.png`;
}
