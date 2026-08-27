// Asserts that a fast move which generates no energy cannot produce a NaN DPS, and that the
// counters ranking stays finite and ordered when such a Pokemon is in the list.
//
// Transform is the only fast move in the game data with energy_delta 0, and Ditto is the only
// species that knows it. A fast move that never charges has no attack cycle, so the maths in
// trueDPS divided by zero, reached Infinity / Infinity, and returned NaN.
//
// That single NaN was not confined to Ditto's own row. NaN compares false against everything,
// so the ranking comparator in counters.ts became inconsistent, and an inconsistent comparator
// leaves the sort free to misorder whole runs around it. On the live data for Shadow Slowpoke
// the top of the counters table read Regieleki, Gengar, Arcanine, Mewtwo, while the true order
// is Regieleki, Zacian, Deoxys, Xurkitree: Zacian, Deoxys, Xurkitree, Darkrai and Kartana were
// missing from the list altogether. Every boss was affected, because every boss is ranked
// against every species and Ditto is always in the set.
//
// Offline and deterministic, so it belongs in `npm run check`. The synthetic data below is
// enough to catch a regression: the zero energy species is small enough a set to always reach
// the returned list, so a NaN would surface immediately.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { build } from "esbuild";

const problems = [];
const notes = [];

// Bundle the real modules rather than reimplementing the maths here, so this cannot pass while
// the shipped code is wrong.
async function loadModules() {
  const dir = mkdtempSync(join(tmpdir(), "zeroenergy-"));
  const entry = join(dir, "entry.ts");
  writeFileSync(
    entry,
    `export { trueDPS, estimateTDO } from ${JSON.stringify(join(process.cwd(), "ts/shared/damage.ts"))};\n` +
      `export { calcCounters } from ${JSON.stringify(join(process.cwd(), "ts/shared/counters.ts"))};\n`
  );
  const out = join(dir, "out.mjs");
  await build({ entryPoints: [entry], bundle: true, outfile: out, format: "esm", platform: "neutral", logLevel: "silent" });
  const mod = await import(pathToFileURL(out).href);
  rmSync(dir, { recursive: true, force: true });
  return mod;
}

const { trueDPS, estimateTDO, calcCounters } = await loadModules();

// Transform and Struggle as the game data carries them: the real pairing Ditto is ranked on.
const transform = { move_id: 242, name: "Transform", type: "Normal", power: 0, duration: 2000, energy_delta: 0 };
const struggle = { move_id: 133, name: "Struggle", type: "Normal", power: 35, duration: 2200, energy_delta: -33 };
const snarl = { move_id: 260, name: "Snarl", type: "Dark", power: 11, duration: 1000, energy_delta: 13 };
const wildCharge = { move_id: 102, name: "Wild Charge", type: "Electric", power: 90, duration: 2500, energy_delta: -50 };

// 1. The root invariant: no energy in means a finite zero out, never NaN and never Infinity.
const zeroDps = trueDPS(transform, struggle, 200, 90);
if (!Number.isFinite(zeroDps)) {
  problems.push(`trueDPS with a zero energy fast move returned ${zeroDps}, which is not a finite number`);
} else if (zeroDps !== 0) {
  problems.push(`trueDPS with a zero energy fast move returned ${zeroDps}, expected 0`);
} else {
  notes.push("trueDPS returns a finite 0 for a fast move that generates no energy");
}

// 2. TDO multiplies DPS, so it has to inherit the zero rather than carry a NaN forward.
const dummy = { pokemon_id: 132, pokemon_name: "Ditto", form: "Normal", base_attack: 91, base_defense: 91, base_stamina: 134 };
const zeroTdo = estimateTDO(dummy, transform, struggle, 200, 90, 1, 1, 0.7903);
if (!Number.isFinite(zeroTdo)) {
  problems.push(`estimateTDO with a zero energy fast move returned ${zeroTdo}, which is not a finite number`);
} else {
  notes.push("estimateTDO inherits that zero instead of propagating a NaN");
}

// 3. An ordinary pairing must be untouched by the guard.
const realDps = trueDPS(snarl, wildCharge, 274.23, 89.3039);
if (!(realDps > 0)) {
  problems.push(`trueDPS on an ordinary pairing returned ${realDps}, so the guard is catching more than it should`);
} else {
  notes.push(`an ordinary pairing still scores (${realDps.toFixed(1)} DPS)`);
}

// 4. The ranking itself: every entry finite, and the order actually descending.
const data = {
  pokemon: [
    { pokemon_id: 132, pokemon_name: "Ditto", form: "Normal", base_attack: 91, base_defense: 91, base_stamina: 134 },
    { pokemon_id: 888, pokemon_name: "Zacian", form: "Normal", base_attack: 332, base_defense: 183, base_stamina: 190 },
    { pokemon_id: 94, pokemon_name: "Gengar", form: "Normal", base_attack: 261, base_defense: 149, base_stamina: 155 },
  ],
  pokemonMoves: [
    { pokemon_id: 132, pokemon_name: "Ditto", form: "Normal", fast_moves: ["Transform"], elite_fast_moves: [], charged_moves: ["Struggle"], elite_charged_moves: [] },
    { pokemon_id: 888, pokemon_name: "Zacian", form: "Normal", fast_moves: ["Snarl"], elite_fast_moves: [], charged_moves: ["Wild Charge"], elite_charged_moves: [] },
    { pokemon_id: 94, pokemon_name: "Gengar", form: "Normal", fast_moves: ["Snarl"], elite_fast_moves: [], charged_moves: ["Wild Charge"], elite_charged_moves: [] },
  ],
  fastMoves: [transform, snarl],
  chargedMoves: [struggle, wildCharge],
  cpMultipliers: [{ level: 40, multiplier: 0.7903 }],
  typeChart: [],
  pokemonTypes: [],
};

// No types on the boss keeps effectiveness at 1 for every move, so this stays a pure maths
// check with no type chart to keep in step.
const ranked = calcCounters(data, { pokemon_name: "TestBoss", cp: 1000 });

const nan = ranked.filter((r) => !Number.isFinite(r.dps));
if (nan.length) {
  problems.push(`counters ranking produced a non finite DPS for: ${nan.map((r) => `${r.name} (${r.dps})`).join(", ")}`);
}
if (!ranked.some((r) => r.name === "Ditto")) {
  problems.push("the zero energy species fell out of the ranking entirely, so this check is no longer testing what it claims");
}
for (let i = 1; i < ranked.length; i++) {
  if (ranked[i - 1].dps < ranked[i].dps) {
    problems.push(`counters ranking is out of order at position ${i}: ${ranked[i - 1].name} ${ranked[i - 1].dps} before ${ranked[i].name} ${ranked[i].dps}`);
  }
}
if (ranked[0]?.name !== "Zacian") {
  problems.push(`strongest counter should be Zacian, got ${ranked[0]?.name ?? "nothing"}`);
}
if (!problems.length) {
  notes.push(`ranking stays finite and ordered with a zero energy species in it (${ranked.map((r) => r.name).join(", ")})`);
}

for (const n of notes) console.log("  " + n);

if (problems.length) {
  console.error(`\nzero energy DPS check FAILED with ${problems.length} problem(s):`);
  for (const p of problems) console.error("  " + p);
  process.exit(1);
}

console.log("zero energy DPS: a fast move that never charges scores 0 instead of NaN, and the counters ranking stays finite and ordered.");
