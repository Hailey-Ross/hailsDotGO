import type { FastMove, ChargedMove, PokemonStat } from "./types";

// Pokemon GO damage formula (PvE)
export function calcDamage(
  movePower: number,
  attackerAtk: number,
  defenderDef: number,
  effectiveness: number
): number {
  return Math.floor(0.5 * movePower * (attackerAtk / defenderDef) * effectiveness) + 1;
}

// Effective DPS of a fast move
export function fastDPS(move: FastMove, attackerAtk: number, defenderDef: number, effectiveness: number): number {
  const dmg = calcDamage(move.power, attackerAtk, defenderDef, effectiveness);
  return dmg / (move.duration / 1000);
}

// Energy per second of a fast move
export function fastEPS(move: FastMove): number {
  return move.energy_delta / (move.duration / 1000);
}

// True DPS accounting for both fast and charged moves.
// fastEff and chargedEff are type-effectiveness multipliers for each move's type.
export function trueDPS(
  fast: FastMove,
  charged: ChargedMove,
  attackerAtk: number,
  defenderDef: number,
  fastEff = 1,
  chargedEff = 1
): number {
  const fastDmg = calcDamage(fast.power, attackerAtk, defenderDef, fastEff);
  const chargedDmg = calcDamage(charged.power, attackerAtk, defenderDef, chargedEff);
  const energyCost = Math.abs(charged.energy_delta);
  const fastCount = Math.ceil(energyCost / fast.energy_delta);
  const cycleDmg = fastCount * fastDmg + chargedDmg;
  const cycleMs = fastCount * fast.duration + charged.duration;
  return cycleDmg / (cycleMs / 1000);
}

// Total Damage Output: how much damage before fainting (simplified)
export function estimateTDO(
  pokemon: PokemonStat,
  fast: FastMove,
  charged: ChargedMove,
  attackerAtk: number,
  defenderDef: number,
  fastEff = 1,
  chargedEff = 1,
  cpMultiplier: number
): number {
  const hp = Math.floor((pokemon.base_stamina + 15) * cpMultiplier);
  const dps = trueDPS(fast, charged, attackerAtk, defenderDef, fastEff, chargedEff);
  const survivalTime = hp / 15;
  return dps * survivalTime;
}
