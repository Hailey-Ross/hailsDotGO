// Asserts a COSTUME raid boss still resolves to a real defender stat.
//
//   node scripts/check-costume-boss-counters.mjs
//   node scripts/check-costume-boss-counters.mjs --from <file>   reads a saved /api/data payload
//
// Why this exists: the server names a costumed boss the way the event page does, so
// /api/raids carries "Charizard wearing Friede's goggles" and "Captain's Cap Pikachu"
// rather than the plain species. The Pokemon list holds base species only, so a label
// like that matches nothing, bossStats returns undefined, and bossDefense falls back to
// the hardcoded 200. Ranking within one boss survives that, which is exactly why nobody
// notices, but every absolute DPS and TDO figure on the panel is then wrong.
//
// check-box-vs-boss.mjs covers the same failure for the SPACE SEPARATED prefixes
// ("Shadow Slowpoke", "Alolan Sandshrew"). This one covers the labels no prefix list can
// strip, which is the shape a costume always takes.
//
// Deliberately NOT part of `npm run check`, matching check-box-vs-boss.mjs: that chain
// stays offline and deterministic, and costume bosses only exist upstream.

import { readFileSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const LIVE = "https://pogo.hails.app/api/data";
const problems = [];
const notes = [];

async function loadGameData() {
  const i = process.argv.indexOf("--from");
  if (i !== -1 && process.argv[i + 1]) {
    notes.push(`read ${process.argv[i + 1]}`);
    return JSON.parse(readFileSync(process.argv[i + 1], "utf8"));
  }
  const res = await fetch(LIVE);
  if (!res.ok) throw new Error(`game data fetch failed: HTTP ${res.status}`);
  notes.push("fetched live game data");
  return await res.json();
}

// Bundle the real module rather than reimplementing any of it, so this cannot pass while
// the shipped code is wrong.
async function loadCounters() {
  const dir = mkdtempSync(join(tmpdir(), "costumecounters-"));
  const entry = join(dir, "entry.ts");
  writeFileSync(
    entry,
    `export { bossStats, bossDefense, speciesInsideLabel } from ${JSON.stringify(
      join(process.cwd(), "ts/shared/counters.ts")
    )};\n`
  );
  const out = join(dir, "out.mjs");
  await build({ entryPoints: [entry], bundle: true, outfile: out, format: "esm", platform: "neutral", logLevel: "silent" });
  const mod = await import("file://" + out.replace(/\\/g, "/"));
  rmSync(dir, { recursive: true, force: true });
  return mod;
}

const FLAT_FALLBACK = 200;

function check(mod, data, label, wantSpecies) {
  const boss = { pokemon_name: label, types: ["Fire"] };
  const species = mod.speciesInsideLabel(data, label);
  if (species !== wantSpecies) {
    problems.push(`speciesInsideLabel(${JSON.stringify(label)}) = ${JSON.stringify(species)}, want ${JSON.stringify(wantSpecies)}`);
    return;
  }
  if (wantSpecies === null) return; // nothing further to resolve, by design
  const stats = mod.bossStats(data, boss);
  if (!stats) {
    problems.push(`bossStats(${JSON.stringify(label)}) resolved nothing, so every damage figure falls back to ${FLAT_FALLBACK}`);
    return;
  }
  if (stats.pokemon_name.toLowerCase() !== wantSpecies) {
    problems.push(`bossStats(${JSON.stringify(label)}) resolved ${stats.pokemon_name}, want ${wantSpecies}`);
  }
  const def = mod.bossDefense(data, boss);
  if (def === FLAT_FALLBACK) {
    problems.push(`bossDefense(${JSON.stringify(label)}) is the flat fallback, not a real stat line`);
  }
  notes.push(`${label} -> ${stats.pokemon_name} (${stats.form ?? "-"}), defence ${def.toFixed(1)}`);
}

const data = await loadGameData();
const mod = await loadCounters();

// The two shapes an event page really uses, prefix and suffix.
check(mod, data, "Captain's Cap Pikachu", "pikachu");
check(mod, data, "Charizard wearing Friede's goggles", "charizard");
check(mod, data, "Modern Jacket Machamp", "machamp");
// A bare species is NOT a decorated label: it must keep resolving the ordinary way, and
// the scan must not answer it with itself.
check(mod, data, "Charizard", null);
// Whatever is actually live right now, so this fails when a real costume stops resolving.
for (const [tier, list] of Object.entries(data.raids ?? {})) {
  for (const boss of list) {
    const def = mod.bossDefense(data, boss);
    if (def === FLAT_FALLBACK) {
      problems.push(`live tier ${tier} boss ${JSON.stringify(boss.pokemon_name)} falls back to the flat defence`);
    }
  }
}

for (const n of notes) console.log(`  ${n}`);
if (problems.length) {
  for (const p of problems) console.error(`FAILED: ${p}`);
  process.exit(1);
}
console.log("OK: costume bosses resolve to a real stat line");
