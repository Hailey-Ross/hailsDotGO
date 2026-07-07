const POGO_ASSETS = "https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/";

// Maps pokemon name → costume label → {p: url prefix, code: Niantic internal code}.
// Species-specific overrides: a curated label, or a code whose art differs per species.
// These take precedence over SHARED_COSTUMES below.
export const COSTUME_SPRITES: Record<string, Record<string, {p: "c"|"f", code: string}>> = {
  "Pikachu": {
    "Party Hat":              { p: "c", code: "ANNIVERSARY" },
    "Witch Hat":              { p: "c", code: "HALLOWEEN_2017" },
    "Santa Hat":              { p: "c", code: "HOLIDAY_2016" },
    "Holiday Outfit":         { p: "c", code: "WINTER_2018" },
    "Straw Hat":              { p: "c", code: "SUMMER_2018" },
    "Detective Hat":          { p: "c", code: "MAY_2019_NOEVOLVE" },
    "Detective Hat 2023":     { p: "c", code: "PI" },
    "Flower Crown":           { p: "c", code: "FEB_2019" },
    "Flower Hat":             { p: "c", code: "APRIL_2020_NOEVOLVE" },
    "Cake Hat":               { p: "c", code: "ANNIVERSARY_2022_NOEVOLVE" },
    "Safari Hat":             { p: "c", code: "SAFARI_2020_NOEVOLVE" },
    "Top Hat":                { p: "c", code: "JAN_2023_NOEVOLVE" },
    "Red Party Hat":          { p: "c", code: "JAN_2020_NOEVOLVE" },
    "Rayquaza Hat":           { p: "c", code: "HOENN_2020_NOEVOLVE" },
    "Charizard Hat":          { p: "c", code: "KANTO_2020_NOEVOLVE" },
    "Lucario Hat":            { p: "c", code: "SINNOH_2020_NOEVOLVE" },
    "Umbreon Hat":            { p: "c", code: "JOHTO_2020_NOEVOLVE" },
    "Meloetta Hat":           { p: "c", code: "GOFEST_2021_NOEVOLVE" },
    "Cherry Blossom":         { p: "c", code: "SPRING_2023" },
    "Original Cap":           { p: "c", code: "ONE_YEAR_ANNIVERSARY" },
    "World Cap":              { p: "c", code: "COSTUME_1" },
    "New Year's Hat":         { p: "c", code: "COSTUME_2" },
    "Rock Star":              { p: "f", code: "ROCK_STAR" },
    "Pop Star":               { p: "f", code: "POP_STAR" },
    "Libre":                  { p: "f", code: "VS_2019" },
    "Ph.D.":                  { p: "f", code: "DOCTOR" },
    "Clone Pikachu":          { p: "f", code: "COPY_2019" },
    "Mimikyu Costume":        { p: "f", code: "FALL_2019" },
    "Flying Pikachu":         { p: "f", code: "COSTUME_2020" },
    "Cap's Hat":              { p: "f", code: "HORIZONS" },
    "Kariyushi Shirt":        { p: "f", code: "KARIYUSHI" },
    "Blue Shirt":             { p: "f", code: "JEJU" },
    "Green Shirt":            { p: "f", code: "TSHIRT_01" },
    "Purple Shirt":           { p: "f", code: "TSHIRT_02" },
    "Batik Shirt":            { p: "f", code: "TSHIRT_03" },
    "Kurta":                  { p: "f", code: "KURTA" },
    "Saree":                  { p: "f", code: "DIWALI_2024" },
    "Lucas's Hat":            { p: "f", code: "GOTOUR_2024_A" },
    "Dawn's Hat":             { p: "f", code: "GOTOUR_2024_B" },
    "Akari's Kerchief":       { p: "f", code: "GOTOUR_2024_B_02" },
    "Hilbert's Hat":          { p: "f", code: "GOTOUR_2025_A" },
    "Hilda's Hat":            { p: "f", code: "GOTOUR_2025_A_02" },
    "Nate's Visor":           { p: "f", code: "GOTOUR_2025_B" },
    "Rosa's Visor":           { p: "f", code: "GOTOUR_2025_B_02" },
    "Sun Crown":              { p: "f", code: "GOFEST_2024_STIARA" },
    "Moon Crown":             { p: "f", code: "GOFEST_2024_MTIARA" },
    "Malachite Crown":        { p: "f", code: "SUMMER_2023_A" },
    "Aquamarine Crown":       { p: "f", code: "SUMMER_2023_B" },
    "Quartz Crown":           { p: "f", code: "SUMMER_2023_C" },
    "Pyrite Crown":           { p: "f", code: "SUMMER_2023_D" },
    "Amethyst Crown":         { p: "f", code: "SUMMER_2023_E" },
  },
  "Pichu":     { "Santa Hat":               { p: "c", code: "HOLIDAY_2016" } },
  "Raichu":    {
    "Santa Hat":              { p: "c", code: "HOLIDAY_2016" },
    "Original Cap":           { p: "c", code: "ONE_YEAR_ANNIVERSARY" },
  },
  "Eevee":     {
    "Sun Crown":              { p: "f", code: "GOFEST_2024_STIARA" },
    "Moon Crown":             { p: "f", code: "GOFEST_2024_MTIARA" },
  },
  "Espeon":    { "Day Scarf":               { p: "f", code: "GOFEST_2024_SSCARF" } },
  "Umbreon":   { "Night Scarf":             { p: "f", code: "GOFEST_2024_MSCARF" } },
  "Squirtle":  { "Sunglasses":              { p: "c", code: "SUMMER_2018" } },
  "Wartortle": { "Sunglasses":              { p: "c", code: "SUMMER_2018" } },
  "Blastoise": { "Sunglasses":              { p: "c", code: "SUMMER_2018" } },
  "Snorlax":   {
    "Nightcap":               { p: "c", code: "NIGHTCAP" },
    "Studded Jacket":         { p: "f", code: "WILDAREA_2024" },
  },
  "Slowpoke":  { "New Year Costume":        { p: "f", code: "2020" } },
  "Slowbro":   { "New Year Costume":        { p: "f", code: "2021" } },
  "Slowking":  { "New Year Costume":        { p: "f", code: "2022" } },
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
  "Kirlia":    { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Gardevoir": { "Meloetta Hat":            { p: "c", code: "GOFEST_2021_NOEVOLVE" } },
  "Croagunk":  { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Toxicroak": { "Fashion Outfit":          { p: "c", code: "FALL_2020_NOEVOLVE" } },
  "Diglett":   { "Fashion Outfit":          { p: "c", code: "FALL_2022" } },
  "Butterfree":{ "Fashion Outfit":          { p: "c", code: "FASHION_2021_NOEVOLVE" } },
  "Aerodactyl":{ "Satchel":                 { p: "f", code: "SUMMER_2023" } },
  "Cubone":    { "Cempasúchil Crown":       { p: "c", code: "FALL_2023" } },
  "Ponyta":    { "Candela Costume":         { p: "c", code: "SPRING_2023_VALOR" } },
  "Vulpix":    { "Spooky Festival Costume": { p: "c", code: "FALL_2022" } },
  "Wurmple":   { "Party Hat":               { p: "c", code: "JAN_2020_NOEVOLVE" } },
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

// Costumes shared by a single Niantic code across many species (event costumes),
// each rendering the same accessory theme. Eligibility (dex[]) is the exact set of
// species that have that shiny costume asset in pokemon-go-api/assets. Regenerate the
// dex lists from the asset tree with scripts/gen-costumes.mjs when new events release.
// KEEP IN SYNC: scripts/gen-costumes.mjs reads COSTUME_SPRITES + SHARED_COSTUMES from
// this file to emit internal/handlers/costumes_data.go (used for public profiles).
export const SHARED_COSTUMES: { label: string; p: "c"|"f"; code: string; dex: number[] }[] = [
  { label: "Party Hat",      p: "c", code: "JAN_2020_NOEVOLVE",    dex: [1,2,3,4,5,6,7,8,9,20,25,33,94,133,202,265] },
  { label: "Flower Crown",   p: "c", code: "NOVEMBER_2018",        dex: [25,26,113,133,134,135,136,172,196,197,242,440,470,471,700] },
  { label: "Cherry Blossom", p: "c", code: "SPRING_2023",          dex: [25,26,133,134,135,136,172,196,197,470,471,700] },
  { label: "Holiday Wreath", p: "c", code: "HOLIDAY_2022",         dex: [133,134,135,136,196,197,470,471,700] },
  { label: "Sunglasses",     p: "c", code: "SUMMER_2018",          dex: [7,8,9,25,26,172] },
  { label: "Flower Hat",     p: "c", code: "APRIL_2020_NOEVOLVE",  dex: [25,175,176,427,428,468] },
  { label: "Fashion Outfit", p: "c", code: "FALL_2020_NOEVOLVE",   dex: [238,281,403,453,454] },
  { label: "Witch Hat",      p: "c", code: "HALLOWEEN_2025",       dex: [216,217,714,715,901] },
  { label: "Holiday Attire", p: "c", code: "HOLIDAY_2023",         dex: [25,26,54,55] },
  { label: "Winter Hat",     p: "c", code: "WINTER_2024",          dex: [702,831,832] },
  { label: "Meloetta Hat",   p: "c", code: "GOFEST_2021_NOEVOLVE", dex: [25,282,330] },
];

function costumeUrl(dexId: number, p: "c"|"f", code: string): string {
  return `${POGO_ASSETS}pm${dexId}.${p}${code}.s.icon.png`;
}

export function costumeShinyUrl(dexId: number, pokemonName: string, costumeLabel: string): string | null {
  const override = COSTUME_SPRITES[pokemonName]?.[costumeLabel];
  if (override) return costumeUrl(dexId, override.p, override.code);
  const shared = SHARED_COSTUMES.find((s) => s.label === costumeLabel && s.dex.includes(dexId));
  if (shared) return costumeUrl(dexId, shared.p, shared.code);
  return null;
}

// Costume labels to offer for a given species: its curated overrides, plus any shared
// costume the species is eligible for (skipping a shared code already covered by an
// override so the same sprite is not offered under two labels).
export function costumeLabelsForDex(dexId: number, pokemonName: string): string[] {
  const override = COSTUME_SPRITES[pokemonName] ?? {};
  const labels = Object.keys(override);
  const usedCodes = new Set(Object.values(override).map((e) => e.code));
  for (const s of SHARED_COSTUMES) {
    if (s.dex.includes(dexId) && !usedCodes.has(s.code) && !labels.includes(s.label)) {
      labels.push(s.label);
    }
  }
  return labels;
}
