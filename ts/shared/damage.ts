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
  // A fast move that generates no energy can never fill a charged move, so the cycle
  // this function measures does not exist. Transform (Ditto) is the only one in the
  // data. Returning 0 is not cosmetic: the division would otherwise yield Infinity,
  // then NaN, and a NaN dps makes the ranking comparator in counters.ts inconsistent,
  // which reorders the WHOLE counters list rather than just this entry.
  if (fast.energy_delta <= 0 || fast.duration <= 0 || charged.duration <= 0) return 0;

  const fastDmg = calcDamage(fast.power, attackerAtk, defenderDef, fastEff);
  const chargedDmg = calcDamage(charged.power, attackerAtk, defenderDef, chargedEff);
  const energyCost = Math.abs(charged.energy_delta);
  const fastCount = Math.ceil(energyCost / fast.energy_delta);
  const cycleDmg = fastCount * fastDmg + chargedDmg;
  const cycleMs = fastCount * fast.duration + charged.duration;
  return cycleDmg / (cycleMs / 1000);
}

// Total Damage Output: how much damage before fainting (simplified).
// defMult: 5/6 for Shadow (takes 20% more damage per hit), 1 otherwise.
export function estimateTDO(
  pokemon: PokemonStat,
  fast: FastMove,
  charged: ChargedMove,
  attackerAtk: number,
  defenderDef: number,
  fastEff = 1,
  chargedEff = 1,
  cpMultiplier: number,
  staIV = 15,
  defMult = 1
): number {
  const hp = Math.floor((pokemon.base_stamina + staIV) * cpMultiplier);
  const dps = trueDPS(fast, charged, attackerAtk, defenderDef, fastEff, chargedEff);
  const survivalTime = (hp * defMult) / 15;
  return dps * survivalTime;
}
