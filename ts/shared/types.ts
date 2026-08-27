export interface PokemonStat {
  pokemon_id: number;
  pokemon_name: string;
  form?: string;
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
  // Stamped on by the server's raid schedule, and absent on any boss no rotation
  // describes, which is every tier 1 and tier 3 entry.
  event_id?: string;
  // The feed's own floating wall clock strings. Parse them as local (see
  // shared/time.ts): the server has already decided the boss belongs on the page,
  // and these say what the rotation means on the viewer's own clock.
  starts_at?: string;
  ends_at?: string;
  // "events" on a card the server built because the raid feed had not listed the
  // boss yet.
  source?: string;
}

export type RaidTiers = Record<string, RaidBoss[]>;

// One entry of the "up next" strip: the soonest rotation for a tier, or one that is
// already live but has no card on the grid yet (a Mega the raid feed has not caught
// up to, which cannot be built locally because no Mega typing data ships with the app).
export interface UpcomingRaid {
  event_id: string;
  name: string;
  tier: string;
  shadow?: boolean;
  bosses: { name: string; image: string; canBeShiny?: boolean }[];
  starts_at: string;
  ends_at: string;
  live?: boolean;
}

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

// One row of the full National Dex, with availability. shinies above stays the
// released-only view every client already reads; this is the superset that also
// carries the species we know about but cannot catch a shiny of yet.
export interface ShinyDexEntry {
  id: number;
  name: string;
  in_go: boolean;
  shiny_released: boolean;
  // The announced release day, "YYYY-MM-DD". Present only while the shiny is
  // still locked: once the day arrives the server flips shiny_released and drops
  // the date, so a catchable card never carries one.
  shiny_release_date?: string;
}

// One Mega's stat line and typing, keyed in GameData.megas by lowercased display
// name ("mega gyarados").
//
// Megas are absent from `pokemon` entirely, and cannot be derived from the base
// species: a Mega keeps the base stamina but its attack, defense and typing all
// change. Mega Gyarados is 292/247/216 and Water plus Dark where Gyarados is
// 237/186/216 and Water plus Flying.
export interface MegaForm {
  name: string;
  types: string[];
  atk: number;
  def: number;
  sta: number;
  image?: string;
}

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
  upcomingRaids?: UpcomingRaid[] | null;
  megas?: Record<string, MegaForm> | null;
  maxBattles: RaidTiers | null;
  shinies: Record<string, ShinyPokemon> | null;
  // Both are omitted by the server when no baseline is embedded, and every
  // consumer must degrade to shinies alone rather than render an empty page.
  shinyDex?: Record<string, ShinyDexEntry> | null;
  regionalShinyOverrides?: Record<string, Record<string, boolean>> | null;
  // Announced release days for regional and alternate forms, species name ->
  // region tag -> "YYYY-MM-DD". A separate blob so the map above keeps the plain
  // boolean shape every consumer already reads.
  regionalShinyDates?: Record<string, Record<string, string>> | null;
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
