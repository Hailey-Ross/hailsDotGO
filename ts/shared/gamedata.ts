import type { GameData } from "./types";

let cached: GameData | null = null;

export async function loadGameData(): Promise<GameData> {
  if (cached) return cached;

  const res = await fetch("/api/data");
  if (!res.ok) throw new Error(`Failed to load game data: ${res.status}`);

  cached = (await res.json()) as GameData;
  return cached;
}

export function pokemonByName(data: GameData, name: string) {
  return data.pokemon.find(
    (p) => p.pokemon_name.toLowerCase() === name.toLowerCase()
  );
}

export function fastMoveByName(data: GameData, name: string) {
  return data.fastMoves.find(
    (m) => m.name.toLowerCase() === name.toLowerCase()
  );
}

export function chargedMoveByName(data: GameData, name: string) {
  return data.chargedMoves.find(
    (m) => m.name.toLowerCase() === name.toLowerCase()
  );
}

export function pokeSprite(id: number): string {
  return `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/${id}.png`;
}

export function typeEffectiveness(
  data: GameData,
  attackType: string,
  defenseTypes: string[]
): number {
  let mult = 1;
  for (const defType of defenseTypes) {
    mult *= data.typeChart[attackType]?.[defType] ?? 1;
  }
  return mult;
}
