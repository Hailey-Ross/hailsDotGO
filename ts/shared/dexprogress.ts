// Shiny dex progress: how many released checklist cards (species plus the regional forms that get a
// card) the trainer has at least one entry for. One function, used by both the Dex page ring and the
// Home page ring, so the two can never disagree.
import { cardRegionalForms, setRegionalShinyOverrides, setRegionalShinyDates, isRegionalShiny, isPatternOnlyRegion } from './regionalForms';
import type { GameData, ShinyDexEntry } from './types';

export interface DexEntryLike { pokemon_id: string; region: string; }

// A carry-only pattern (a Scatterbug or Spewpa Vivillon pattern) counts against the single base card.
function cardRegion(species: string, region: string): string {
  return isPatternOnlyRegion(species, region) ? '' : region;
}

/** The released checklist cards as "Name:region" keys ('' = the original form). */
export function releasedCardKeys(gameData: GameData): string[] {
  setRegionalShinyOverrides(gameData.regionalShinyOverrides);
  setRegionalShinyDates(gameData.regionalShinyDates);
  const shinies = gameData.shinies || {};
  // Same sources as the Dex page: the full dex table with availability flags, else the released
  // view; plus anything served as released that the table has never heard of.
  const entries: ShinyDexEntry[] = gameData.shinyDex
    ? Object.values(gameData.shinyDex)
    : Object.values(shinies).map((s) => ({ id: s.id, name: s.name, in_go: true, shiny_released: true }));
  if (gameData.shinyDex) {
    const known = new Set(entries.map((e) => e.id));
    for (const s of Object.values(shinies)) if (!known.has(s.id)) entries.push({ id: s.id, name: s.name, in_go: true, shiny_released: true });
  }
  const keys: string[] = [];
  for (const e of entries) {
    const released = e.shiny_released ?? true;
    if (released) keys.push(e.name + ':');
    for (const f of cardRegionalForms(e.name)) if (released && isRegionalShiny(e.name, f)) keys.push(e.name + ':' + f.region);
  }
  return keys;
}

/** have = released cards with at least one entry; total = released cards. */
export function dexProgress(gameData: GameData, mine: DexEntryLike[]): { have: number; total: number } {
  const cards = releasedCardKeys(gameData);
  const owned = new Set(mine.map((s) => s.pokemon_id + ':' + cardRegion(s.pokemon_id, s.region)));
  return { have: cards.filter((k) => owned.has(k)).length, total: cards.length };
}
