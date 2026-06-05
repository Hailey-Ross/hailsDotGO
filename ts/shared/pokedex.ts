export interface SpeciesData {
  flavor: string;
  genus: string;
  isLegendary: boolean;
  isMythical: boolean;
  varieties: string[];  // non-default variety names, e.g. ["kyogre-primal"]
}

const speciesCache    = new Map<string | number, SpeciesData>();
const cryCache        = new Map<number, string>();
const formSpriteCache = new Map<string, { normal: string; shiny: string }>();

const PREFERRED = ["scarlet", "violet", "sword", "shield", "sun", "moon", "x", "y", "alpha-sapphire", "omega-ruby"];

function normalizeName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[''\.]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

export async function fetchSpeciesData(nameOrId: string | number): Promise<SpeciesData> {
  const key = typeof nameOrId === "number" ? nameOrId : normalizeName(String(nameOrId));
  if (speciesCache.has(key)) return speciesCache.get(key)!;

  const empty: SpeciesData = { flavor: "", genus: "", isLegendary: false, isMythical: false, varieties: [] };

  try {
    const res = await fetch(`https://pokeapi.co/api/v2/pokemon-species/${key}/`);
    if (!res.ok) { speciesCache.set(key, empty); return empty; }
    const json = await res.json();

    // Flavor text
    const entries: Array<{ flavor_text: string; language: { name: string }; version: { name: string } }> =
      json.flavor_text_entries ?? [];
    const en = entries.filter(e => e.language.name === "en");
    let flavor = "";
    for (const v of PREFERRED) {
      const found = en.find(e => e.version.name === v);
      if (found) { flavor = found.flavor_text; break; }
    }
    if (!flavor && en.length > 0) flavor = en[en.length - 1].flavor_text;
    flavor = flavor.replace(/[\f\n\r]/g, " ").replace(/\s+/g, " ").trim();

    // Genus
    const genera: Array<{ genus: string; language: { name: string } }> = json.genera ?? [];
    const genusEntry = genera.find(g => g.language.name === "en");
    const genus = genusEntry?.genus ?? "";

    // Non-default varieties (primal, mega, etc.)
    const vars: Array<{ is_default: boolean; pokemon: { name: string } }> = json.varieties ?? [];
    const varieties = vars.filter(v => !v.is_default).map(v => v.pokemon.name);

    const data: SpeciesData = {
      flavor,
      genus,
      isLegendary: json.is_legendary ?? false,
      isMythical:  json.is_mythical  ?? false,
      varieties,
    };
    speciesCache.set(key, data);
    return data;
  } catch {
    speciesCache.set(key, empty);
    return empty;
  }
}

// Backwards-compatible wrapper
export async function fetchFlavorText(nameOrId: string | number): Promise<string> {
  return (await fetchSpeciesData(nameOrId)).flavor;
}

export async function fetchCryUrl(id: number): Promise<string> {
  if (cryCache.has(id)) return cryCache.get(id)!;
  try {
    const res = await fetch(`https://pokeapi.co/api/v2/pokemon/${id}/`);
    if (!res.ok) { cryCache.set(id, ""); return ""; }
    const json = await res.json();
    const url: string = json.cries?.latest ?? "";
    cryCache.set(id, url);
    return url;
  } catch {
    cryCache.set(id, "");
    return "";
  }
}

export async function fetchFormSprites(name: string): Promise<{ normal: string; shiny: string }> {
  if (formSpriteCache.has(name)) return formSpriteCache.get(name)!;
  const empty = { normal: "", shiny: "" };
  try {
    const res = await fetch(`https://pokeapi.co/api/v2/pokemon/${name}/`);
    if (!res.ok) { formSpriteCache.set(name, empty); return empty; }
    const json = await res.json();
    const result = {
      normal: json.sprites?.front_default ?? "",
      shiny:  json.sprites?.front_shiny  ?? "",
    };
    formSpriteCache.set(name, result);
    return result;
  } catch {
    formSpriteCache.set(name, empty);
    return empty;
  }
}
