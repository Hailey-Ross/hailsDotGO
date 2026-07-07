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
  | "sky";

export interface RegionalForm {
  region: Region;
  variantId: number;
  shiny: boolean;
}

export const REGION_ORDER: Region[] = [
  "alolan", "galarian", "hisuian", "paldean",
  "therian", "origin", "attack", "defense", "speed", "sky",
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
};

// Regional forms whose shiny is released, i.e. the ones that get checklist
// cards and edit row options.
export function shinyRegionalForms(species: string): RegionalForm[] {
  return (REGIONAL_FORMS[species] ?? []).filter((f) => f.shiny);
}

export function regionalVariantId(species: string, region: string): number {
  return REGIONAL_FORMS[species]?.find((f) => f.region === region)?.variantId ?? 0;
}
