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
  shiny_found_wild?: boolean;
  shiny_found_raid?: boolean;
  shiny_found_egg?: boolean;
  shiny_found_research?: boolean;
  shiny_found_evolution?: boolean;
  shiny_found_photobomb?: boolean;
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
  typeChart: TypeChart | null;
  cpMultipliers: CPMultiplier[] | null;
}

export interface DPSResult {
  pokemon: string;
  fastMove: string;
  chargedMove: string;
  dps: number;
  tdo: number;
}
