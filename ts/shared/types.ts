export interface PokemonStat {
  pokemon_id: number;
  pokemon_name: string;
  base_attack: number;
  base_defense: number;
  base_stamina: number;
}

export interface FastMove {
  name: string;
  type: string;
  power: number;
  stamina_loss_scaler: number;
  duration: number;       // milliseconds
  energy_delta: number;   // positive
}

export interface ChargedMove {
  name: string;
  type: string;
  power: number;
  stamina_loss_scaler: number;
  duration: number;       // milliseconds
  energy_delta: number;   // negative
}

export interface RaidBoss {
  pokemon_name: string;
  cp: number;
  cp_max?: number;
  cp_boosted_min?: number;
  cp_boosted_max?: number;
  image_url?: string;
  types?: string[];
  can_be_shiny?: boolean;
}

export type RaidTiers = Record<string, RaidBoss[]>;

export interface ShinyPokemon {
  id: number;
  name: string;
  found_wild?: boolean;
  found_raid?: boolean;
  found_egg?: boolean;
  found_research?: boolean;
  found_evolution?: boolean;
  found_photobomb?: boolean;
}

// type_effectiveness[attackType][defenseType] = multiplier
export type TypeChart = Record<string, Record<string, number>>;

export interface CPMultiplier {
  level: number;
  multiplier: number;
}

export interface PokemonMoves {
  pokemon_id: number;
  pokemon_name: string;
  fast_moves: string[];
  charged_moves: string[];
  elite_fast_moves: string[];
  elite_charged_moves: string[];
}

export interface GameData {
  pokemon: PokemonStat[] | null;
  pokemonMoves: PokemonMoves[] | null;
  fastMoves: FastMove[] | null;
  chargedMoves: ChargedMove[] | null;
  raids: RaidTiers | null;
  shinies: Record<string, ShinyPokemon> | null;
  shadowPokemon: string[] | null;
  typeChart: TypeChart | null;
  cpMultipliers: CPMultiplier[] | null;
  pokemonTypes: Record<string, string[]> | null;
  pokemonNames: Record<string, Record<string, string>> | null;
}

export interface DPSResult {
  pokemon: string;
  fastMove: string;
  chargedMove: string;
  dps: number;
  tdo: number;
}
