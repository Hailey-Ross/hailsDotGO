import type { GameData, PokemonStat } from "./types";

declare var SITE_LANG: string;

let cached: GameData | null = null;

export async function loadGameData(): Promise<GameData> {
  if (cached) return cached;

  // Try session endpoint (no rate limit); fall back to public if not logged in.
  let res = await fetch("/api/app/data");
  if (res.status === 401) {
    res = await fetch("/api/data");
  }
  if (!res.ok) throw new Error(`Failed to load game data: ${res.status}`);

  cached = (await res.json()) as GameData;
  return cached;
}

export function pokemonByName(data: GameData, name: string) {
  return (data.pokemon ?? []).find(
    (p) => p.pokemon_name.toLowerCase() === name.toLowerCase()
  );
}

export function fastMoveByName(data: GameData, name: string) {
  return (data.fastMoves ?? []).find(
    (m) => m.name.toLowerCase() === name.toLowerCase()
  );
}

export function chargedMoveByName(data: GameData, name: string) {
  return (data.chargedMoves ?? []).find(
    (m) => m.name.toLowerCase() === name.toLowerCase()
  );
}

export function pokeSprite(id: number): string {
  return `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/${id}.png`;
}

export function cpForLevel(
  poke: PokemonStat,
  atkIV: number,
  defIV: number,
  staIV: number,
  cpm: number
): number {
  const atk = poke.base_attack + atkIV;
  const def = poke.base_defense + defIV;
  const sta = poke.base_stamina + staIV;
  return Math.max(10, Math.floor(atk * Math.sqrt(def) * Math.sqrt(sta) * cpm * cpm / 10));
}

export function cpmFromCP(
  data: GameData,
  poke: PokemonStat,
  atkIV: number,
  defIV: number,
  staIV: number,
  targetCP: number
): number {
  const mults = (data.cpMultipliers ?? []).slice().sort((a, b) => a.level - b.level);
  let bestCPM = mults[0]?.multiplier ?? 0.094;
  for (const { multiplier } of mults) {
    const cp = cpForLevel(poke, atkIV, defIV, staIV, multiplier);
    if (cp <= targetCP) {
      bestCPM = multiplier;
    } else {
      break;
    }
  }
  return bestCPM;
}

export function pokeName(data: GameData, englishName: string): string {
  const lang = typeof SITE_LANG !== "undefined" ? SITE_LANG : "en";
  if (lang === "en") return englishName.replace(/_/g, " ");
  return data.pokemonNames?.[englishName]?.[lang] ?? englishName.replace(/_/g, " ");
}

export function typeEffectiveness(
  data: GameData,
  attackType: string,
  defenseTypes: string[]
): number {
  let mult = 1;
  for (const defType of defenseTypes) {
    mult *= data.typeChart?.[attackType]?.[defType] ?? 1;
  }
  return mult;
}
