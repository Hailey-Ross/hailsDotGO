// Regional forms available in Pokemon GO, keyed by the base species name as
// used in gameData.shinies (PoGoAPI naming: curly apostrophe in Farfetch’d,
// "Mr. Mime" with a space). variantId is the PokeAPI pokemon id of the
// variant; its sprites resolve at the same shiny/{id}.png URL scheme as base
// dex ids, so no extra API calls are needed. shiny marks whether the shiny is
// released in Pokemon GO; only shiny rows become checklist cards. Any change
// here must be mirrored into internal/handlers/regional.go.
//
// Some tags are not geographic regions but reuse this same one variant per
// card mechanism for alternate battle forms whose shiny is released in GO
// (Therian for the Forces of Nature, Origin for Giratina/Dialga/Palkia,
// Attack/Defense/Speed for Deoxys, Sky for Shaymin). In every case the base
// dex card is the default forme (Incarnate, Altered, Normal, Land).
//
// The Unown letters (unown_a .. unown_z, unown_excl, unown_qmark) ride the same
// mechanism, but their sprites are the one exception to the numeric variantId
// rule: PokeAPI files each letter as a pokemon-form rather than a pokemon, so
// they have no variant id and their art lives at a string slug (201-b) instead.
// That is why variantId widens to number | string.
export type UnownRegion = `unown_${string}`;

// Vivillon's 20 wing patterns ride the same one variant per card mechanism as
// the Unown letters, and hit the same string slug exception: PokeAPI files each
// pattern as a pokemon-form of dex 666 (666-polar, 666-icy-snow), so their art
// lives at a slug, not a numeric variant id. The Meadow pattern is the default
// form, so its art is the plain 666.
export type VivillonRegion = `viv_${string}`;

export type Region =
  | "alolan"
  | "galarian"
  | "hisuian"
  | "paldean"
  | "therian"
  | "origin"
  | "attack"
  | "defense"
  | "speed"
  | "sky"
  | "dusk_mane"
  | "dawn_wings"
  | "crowned_sword"
  | "crowned_shield"
  | "black"
  | "white"
  | "resolute"
  | "midnight"
  | "dusk"
  | "sandy_cloak"
  | "trash_cloak"
  | "low_key"
  | "pom_pom"
  | "pau"
  | "sensu"
  | "blue_striped"
  | "white_striped"
  | "wash"
  | "heat"
  | "frost"
  | "fan"
  | "mow"
  | UnownRegion
  | VivillonRegion;

export interface RegionalForm {
  region: Region;
  variantId: number | string;
  shiny: boolean;
  // patternOnly forms are recordable on an entry and carry through evolution,
  // but get no checklist card of their own and never change the sprite. They
  // exist so Scatterbug and Spewpa can remember which Vivillon pattern they are
  // (decided at catch in GO) while still looking identical across every region.
  patternOnly?: boolean;
}

// The 28 Unown letters, in the order the game lists them. letter is what a
// trainer sees on the card and is never translated: it is the same glyph in
// every locale. spriteId is the PokeAPI sprite slug, and letter A is the odd
// one out because it IS the default form, so its art is the plain 201.
export interface UnownLetter {
  region: UnownRegion;
  letter: string;
  spriteId: number | string;
}

export const UNOWN_LETTERS: UnownLetter[] = [
  ..."abcdefghijklmnopqrstuvwxyz".split("").map((c, i) => ({
    region: `unown_${c}` as UnownRegion,
    letter: c.toUpperCase(),
    spriteId: i === 0 ? 201 : `201-${c}`,
  })),
  { region: "unown_excl",  letter: "!", spriteId: "201-exclamation" },
  { region: "unown_qmark", letter: "?", spriteId: "201-question" },
];

// Vivillon's 20 wing patterns. label is what a trainer sees on the card and is
// self labelling in English (like the Unown letters, no locale keys): Meadow,
// Sun, Ocean read the same everywhere. spriteId is the PokeAPI sprite slug, and
// Meadow is the odd one out because it IS the default form, so its art is the
// plain 666. geo is the real world region the pattern is collected from, shown
// as a card tooltip. Fancy and Poke Ball are event exclusive, so they have no
// region. Every region tag must fit user_shinies.region (VARCHAR(16)).
export interface VivillonPattern {
  region: VivillonRegion;
  label: string;
  spriteId: number | string;
  geo: string;
}

export const VIVILLON_PATTERNS: VivillonPattern[] = [
  { region: "viv_meadow",      label: "Meadow",      spriteId: 666,               geo: "Western Europe" },
  { region: "viv_polar",       label: "Polar",       spriteId: "666-polar",       geo: "Northern latitudes (northern N. America, N. Europe, Russia)" },
  { region: "viv_tundra",      label: "Tundra",      spriteId: "666-tundra",      geo: "Northeastern Europe, Russia" },
  { region: "viv_continental", label: "Continental", spriteId: "666-continental", geo: "Central and Eastern Europe, Central Asia, India" },
  { region: "viv_garden",      label: "Garden",      spriteId: "666-garden",      geo: "UK and Ireland" },
  { region: "viv_elegant",     label: "Elegant",     spriteId: "666-elegant",     geo: "Japan" },
  { region: "viv_icy_snow",    label: "Icy Snow",    spriteId: "666-icy-snow",    geo: "Arctic (Iceland, northern Scandinavia, Alaska, northern Canada)" },
  { region: "viv_modern",      label: "Modern",      spriteId: "666-modern",      geo: "Eastern North America" },
  { region: "viv_marine",      label: "Marine",      spriteId: "666-marine",      geo: "Southern and Mediterranean Europe" },
  { region: "viv_archipelago", label: "Archipelago", spriteId: "666-archipelago", geo: "Caribbean and Southeast Asian islands" },
  { region: "viv_high_plains", label: "High Plains", spriteId: "666-high-plains", geo: "Western and central North America" },
  { region: "viv_sandstorm",   label: "Sandstorm",   spriteId: "666-sandstorm",   geo: "Middle East, North Africa" },
  { region: "viv_river",       label: "River",       spriteId: "666-river",       geo: "Australia" },
  { region: "viv_monsoon",     label: "Monsoon",     spriteId: "666-monsoon",     geo: "South and Southeast Asia" },
  { region: "viv_savanna",     label: "Savanna",     spriteId: "666-savanna",     geo: "Sub Saharan Africa" },
  { region: "viv_sun",         label: "Sun",         spriteId: "666-sun",         geo: "Central America, Mexico" },
  { region: "viv_ocean",       label: "Ocean",       spriteId: "666-ocean",       geo: "Pacific islands, Oceania" },
  { region: "viv_jungle",      label: "Jungle",      spriteId: "666-jungle",      geo: "Equatorial rainforests (Amazon, Central Africa)" },
  { region: "viv_fancy",       label: "Fancy",       spriteId: "666-fancy",       geo: "Event exclusive" },
  { region: "viv_poke_ball",   label: "Poke Ball",   spriteId: "666-poke-ball",   geo: "Event exclusive" },
];

export const REGION_ORDER: Region[] = [
  "alolan", "galarian", "hisuian", "paldean",
  "therian", "origin", "attack", "defense", "speed", "sky",
  "dusk_mane", "dawn_wings", "crowned_sword", "crowned_shield",
  "black", "white", "resolute", "midnight", "dusk",
  "sandy_cloak", "trash_cloak", "low_key",
  "pom_pom", "pau", "sensu", "blue_striped", "white_striped",
  "wash", "heat", "frost", "fan", "mow",
  ...UNOWN_LETTERS.map((u) => u.region),
  ...VIVILLON_PATTERNS.map((p) => p.region),
];

export const REGIONAL_FORMS: Record<string, RegionalForm[]> = {
  // Alolan
  "Rattata":    [{ region: "alolan", variantId: 10091, shiny: true }],
  "Raticate":   [{ region: "alolan", variantId: 10092, shiny: true }],
  "Raichu":     [{ region: "alolan", variantId: 10100, shiny: true }],
  "Sandshrew":  [{ region: "alolan", variantId: 10101, shiny: true }],
  "Sandslash":  [{ region: "alolan", variantId: 10102, shiny: true }],
  "Vulpix":     [{ region: "alolan", variantId: 10103, shiny: true }],
  "Ninetales":  [{ region: "alolan", variantId: 10104, shiny: true }],
  "Diglett":    [{ region: "alolan", variantId: 10105, shiny: true }],
  "Dugtrio":    [{ region: "alolan", variantId: 10106, shiny: true }],
  "Meowth": [
    { region: "alolan",   variantId: 10107, shiny: true },
    { region: "galarian", variantId: 10161, shiny: true },
  ],
  "Persian":    [{ region: "alolan", variantId: 10108, shiny: true }],
  "Geodude":    [{ region: "alolan", variantId: 10109, shiny: true }],
  "Graveler":   [{ region: "alolan", variantId: 10110, shiny: true }],
  "Golem":      [{ region: "alolan", variantId: 10111, shiny: true }],
  "Grimer":     [{ region: "alolan", variantId: 10112, shiny: true }],
  "Muk":        [{ region: "alolan", variantId: 10113, shiny: true }],
  "Exeggutor":  [{ region: "alolan", variantId: 10114, shiny: true }],
  "Marowak":    [{ region: "alolan", variantId: 10115, shiny: true }],
  // Galarian (Meowth listed above)
  "Ponyta":     [{ region: "galarian", variantId: 10162, shiny: true }],
  "Rapidash":   [{ region: "galarian", variantId: 10163, shiny: true }],
  "Slowpoke":   [{ region: "galarian", variantId: 10164, shiny: true }],
  "Slowbro":    [{ region: "galarian", variantId: 10165, shiny: true }],
  "Farfetch’d": [{ region: "galarian", variantId: 10166, shiny: true }],
  "Weezing":    [{ region: "galarian", variantId: 10167, shiny: true }],
  "Mr. Mime":   [{ region: "galarian", variantId: 10168, shiny: true }],
  "Articuno":   [{ region: "galarian", variantId: 10169, shiny: true }],
  "Zapdos":     [{ region: "galarian", variantId: 10170, shiny: true }],
  "Moltres":    [{ region: "galarian", variantId: 10171, shiny: true }],
  "Slowking":   [{ region: "galarian", variantId: 10172, shiny: true }],
  "Corsola":    [{ region: "galarian", variantId: 10173, shiny: true }],
  "Zigzagoon":  [{ region: "galarian", variantId: 10174, shiny: true }],
  "Linoone":    [{ region: "galarian", variantId: 10175, shiny: true }],
  "Darumaka":   [{ region: "galarian", variantId: 10176, shiny: true }],
  // Standard mode variant; Zen Mode is not obtainable in GO.
  "Darmanitan": [{ region: "galarian", variantId: 10177, shiny: true }],
  "Yamask":     [{ region: "galarian", variantId: 10179, shiny: true }],
  "Stunfisk":   [{ region: "galarian", variantId: 10180, shiny: true }],
  // Hisuian
  "Growlithe":  [{ region: "hisuian", variantId: 10229, shiny: true }],
  "Arcanine":   [{ region: "hisuian", variantId: 10230, shiny: true }],
  "Voltorb":    [{ region: "hisuian", variantId: 10231, shiny: true }],
  "Electrode":  [{ region: "hisuian", variantId: 10232, shiny: true }],
  "Typhlosion": [{ region: "hisuian", variantId: 10233, shiny: true }],
  "Qwilfish":   [{ region: "hisuian", variantId: 10234, shiny: true }],
  "Sneasel":    [{ region: "hisuian", variantId: 10235, shiny: true }],
  "Samurott":   [{ region: "hisuian", variantId: 10236, shiny: true }],
  "Lilligant":  [{ region: "hisuian", variantId: 10237, shiny: true }],
  // In GO but shiny not released as of 2026-07-05.
  "Zorua":      [{ region: "hisuian", variantId: 10238, shiny: false }],
  "Zoroark":    [{ region: "hisuian", variantId: 10239, shiny: false }],
  "Braviary":   [{ region: "hisuian", variantId: 10240, shiny: true }],
  "Avalugg":    [{ region: "hisuian", variantId: 10243, shiny: true }],
  "Decidueye":  [{ region: "hisuian", variantId: 10244, shiny: true }],
  // Paldean
  "Wooper":     [{ region: "paldean", variantId: 10253, shiny: true }],
  // Combat Breed variant id; shiny status disputed between sources
  // (Serebii yes, LeekDuck no), kept false until confirmed. Enabling later
  // may need per breed values.
  "Tauros":     [{ region: "paldean", variantId: 10250, shiny: false }],
  // Therian (Forces of Nature). Base card is the Incarnate form; the therian
  // row adds the second collectible card. Enamorus is intentionally omitted:
  // its shiny is not released in Pokemon GO.
  "Tornadus":   [{ region: "therian", variantId: 10019, shiny: true }],
  "Thundurus":  [{ region: "therian", variantId: 10020, shiny: true }],
  "Landorus":   [{ region: "therian", variantId: 10021, shiny: true }],
  // Alternate battle formes with a released shiny in GO. Base card is the
  // default forme (Normal Deoxys, Altered Giratina/Dialga/Palkia, Land Shaymin).
  "Deoxys": [
    { region: "attack",  variantId: 10001, shiny: true },
    { region: "defense", variantId: 10002, shiny: true },
    { region: "speed",   variantId: 10003, shiny: true },
  ],
  "Giratina":   [{ region: "origin", variantId: 10007, shiny: true }],
  "Dialga":     [{ region: "origin", variantId: 10245, shiny: true }],
  "Palkia":     [{ region: "origin", variantId: 10246, shiny: true }],
  "Shaymin":    [{ region: "sky",    variantId: 10006, shiny: true }],
  // Fusion forms (base dex card is the default forme). Shiny of the fusion is
  // obtained by fusing a shiny base, released in Pokemon GO as of 2026-07.
  "Necrozma": [
    { region: "dusk_mane",  variantId: 10155, shiny: true },
    { region: "dawn_wings", variantId: 10156, shiny: true },
  ],
  // Crowned formes (base dex card is the Hero of Many Battles forme).
  "Zacian":    [{ region: "crowned_sword",  variantId: 10188, shiny: true }],
  "Zamazenta": [{ region: "crowned_shield", variantId: 10189, shiny: true }],
  // Kyurem fusions (base dex card is plain Kyurem). Shiny released GO Tour Unova.
  "Kyurem": [
    { region: "black", variantId: 10022, shiny: true },
    { region: "white", variantId: 10023, shiny: true },
  ],
  // Keldeo Resolute (base dex card is Ordinary). Shiny released Final Justice.
  "Keldeo":     [{ region: "resolute", variantId: 10024, shiny: true }],
  // Lycanroc (base dex card is Midday).
  "Lycanroc": [
    { region: "midnight", variantId: 10126, shiny: true },
    { region: "dusk",     variantId: 10152, shiny: true },
  ],
  // Wormadam cloaks (base dex card is Plant Cloak).
  "Wormadam": [
    { region: "sandy_cloak", variantId: 10004, shiny: true },
    { region: "trash_cloak", variantId: 10005, shiny: true },
  ],
  // Toxtricity (base dex card is Amped).
  "Toxtricity": [{ region: "low_key", variantId: 10184, shiny: true }],
  // Oricorio styles (base dex card is Baile).
  "Oricorio": [
    { region: "pom_pom", variantId: 10123, shiny: true },
    { region: "pau",     variantId: 10124, shiny: true },
    { region: "sensu",   variantId: 10125, shiny: true },
  ],
  // Basculin (base dex card is Red-Striped).
  "Basculin": [
    { region: "blue_striped",  variantId: 10016, shiny: true },
    { region: "white_striped", variantId: 10247, shiny: true },
  ],
  // Rotom appliance formes (base dex card is Rotom). Only Wash has a released
  // shiny as of 2026-07; the others are in GO with shiny not yet released.
  "Rotom": [
    { region: "wash",  variantId: 10009, shiny: true },
    { region: "heat",  variantId: 10008, shiny: false },
    { region: "frost", variantId: 10010, shiny: false },
    { region: "fan",   variantId: 10011, shiny: false },
    { region: "mow",   variantId: 10012, shiny: false },
  ],
  // Unown letters. Every letter's shiny is released, so all 28 get a card. The
  // base dex card is kept as well, for a catch whose letter was never noted.
  "Unown": UNOWN_LETTERS.map((u) => ({ region: u.region, variantId: u.spriteId, shiny: true })),
  // Vivillon patterns. Every pattern's shiny is released, so all 20 get a card.
  // The base dex card is kept as well, for a catch whose pattern was never noted.
  // Only Vivillon carries patterns: Scatterbug and Spewpa look identical across
  // every region (PokeAPI files their pattern forms with no sprite of their own).
  "Vivillon": VIVILLON_PATTERNS.map((p) => ({ region: p.region, variantId: p.spriteId, shiny: true })),
  // Scatterbug and Spewpa carry the pattern (chosen at catch, and it rides up the
  // line to Vivillon) but never show it: patternOnly means the pattern is
  // recordable and evolves through, yet spawns no card and leaves the sprite the
  // plain 664/665. variantId 0 forces every sprite lookup back to the base dex.
  "Scatterbug": VIVILLON_PATTERNS.map((p) => ({ region: p.region, variantId: 0, shiny: true, patternOnly: true })),
  "Spewpa":     VIVILLON_PATTERNS.map((p) => ({ region: p.region, variantId: 0, shiny: true, patternOnly: true })),
};

// Regional forms that get their own checklist card: shiny released, and not
// carry-only. Scatterbug and Spewpa patterns are excluded here so they stay a
// single card each.
export function shinyRegionalForms(species: string): RegionalForm[] {
  return (REGIONAL_FORMS[species] ?? []).filter((f) => f.shiny && !f.patternOnly);
}

// Regional forms that can be recorded on an entry and carried through evolution:
// the card forms plus the carry-only ones. Drives the collection row's region
// select and the region propagation in getEvolveTargets.
export function recordableRegions(species: string): RegionalForm[] {
  return (REGIONAL_FORMS[species] ?? []).filter((f) => f.shiny);
}

// True when a species/region pair is carry-only (a Scatterbug/Spewpa pattern),
// so the checklist can collapse it to the base card while the entry keeps it.
export function isPatternOnlyRegion(species: string, region: string): boolean {
  return !!REGIONAL_FORMS[species]?.find((f) => f.region === region)?.patternOnly;
}

export function regionalVariantId(species: string, region: string): number | string {
  return REGIONAL_FORMS[species]?.find((f) => f.region === region)?.variantId ?? 0;
}

// The letter an unown_* region tag stands for, or "" for anything else.
export function unownLetter(region: string): string {
  return UNOWN_LETTERS.find((u) => u.region === region)?.letter ?? "";
}

// The real world region a viv_* pattern tag is collected from, or "" for
// anything else. Shown as a card tooltip on the shiny checklist.
export function vivillonGeo(region: string): string {
  return VIVILLON_PATTERNS.find((p) => p.region === region)?.geo ?? "";
}
