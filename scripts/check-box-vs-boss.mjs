// Asserts the raids page can score a trainer's own Pokemon against a raid boss, and that the
// two things most likely to go wrong quietly do not: a boss whose defence cannot be resolved,
// and a moveset range that reports a worst case better than its best.
//
// The defence half matters because it used to be broken for most of the board. Raid bosses are
// named with a space separated form prefix ("Shadow Slowpoke", "Alolan Sandshrew",
// "Mega Blaziken") while the Pokemon list holds base species only, so 14 of 19 bosses in a live
// rotation resolved nothing and silently fell back to a hardcoded defender stat. Ranking within
// one boss survived that, which is why nobody noticed, but every absolute figure was wrong.
//
//   npm run check:box                          fetches the live game data
//   node scripts/check-box-vs-boss.mjs --from &lt;file&gt;   reads a saved /api/data payload instead
//
// Deliberately NOT part of `npm run check`, matching check-shiny-dex-vs-fandom.mjs and
// check-events-feed.mjs. That chain has to stay offline and deterministic, and the boss list
// only exists upstream, so there is nothing meaningful to check against without the network.

import { readFileSync } from "node:fs";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
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

// Bundle the real modules rather than reimplementing the maths here, so this cannot pass while
// the shipped code is wrong.
async function loadModules() {
  const dir = mkdtempSync(join(tmpdir(), "boxvsboss-"));
  const entry = join(dir, "entry.ts");
  writeFileSync(
    entry,
    `export { calcCounters, calcMovesetRange, bossDefense, bossStats, DEFAULT_CONFIG } from ${JSON.stringify(
      join(process.cwd(), "ts/shared/counters.ts")
    )};\nexport { scoreBox as scoreBoxFn, setScoreLimitForTest } from ${JSON.stringify(
      join(process.cwd(), "ts/shared/boxcounters.ts")
    )};\n`
  );
  const out = join(dir, "out.mjs");
  await build({ entryPoints: [entry], bundle: true, outfile: out, format: "esm", platform: "neutral", logLevel: "silent" });
  const mod = await import("file://" + out.replace(/\\/g, "/"));
  rmSync(dir, { recursive: true, force: true });
  return mod;
}

const data = await loadGameData();
const { calcCounters, calcMovesetRange, bossDefense, bossStats, DEFAULT_CONFIG, scoreBoxFn, setScoreLimitForTest } = await loadModules();

const bosses = [];
for (const [tier, list] of Object.entries(data.raids ?? {})) {
  for (const b of list) bosses.push({ tier, boss: b });
}
for (const [tier, list] of Object.entries(data.maxBattles ?? {})) {
  for (const b of list) bosses.push({ tier: "max" + tier, boss: b });
}

if (!bosses.length) {
  problems.push("no raid bosses in the game data, so nothing could be checked");
}

// 1. Every boss must resolve a real defender stat. The old fallback was 200 flat; a boss whose
//    computed defence equals that exactly is the signature of the bug coming back.
// This calls the shipped bossDefense rather than reimplementing it, so the check
// cannot pass while the real lookup is broken. 200 is the literal the function
// falls back to when nothing resolves, and is the signature of the old bug.
// Asserting only "not 200" is not enough, and was actively misleading: 60 species
// carry several stat lines under one name, so a boss could resolve to a real
// number belonging to the WRONG forme and sail through. Deoxys (Defense Forme)
// picked up Deoxys Attack, defence 46 against a true 330, and the check called it
// fixed. So the FORM is what gets asserted here.
const FALLBACK = 200;
const FORM_EXPECTED = [
  [/^Alolan\s+/i, "alola"], [/^Galarian\s+/i, "galarian"],
  [/^Hisuian\s+/i, "hisuian"], [/^Paldean\s+/i, "paldea"],
  [/^Shadow\s+/i, "normal"],
];
let resolved = 0;
for (const { boss } of bosses) {
  const stats = bossStats(data, boss);
  const def = bossDefense(data, boss);
  if (!stats) {
    problems.push(`${boss.pokemon_name}: resolves to nothing, so it falls back to the flat ${FALLBACK}`);
    continue;
  }
  if (!(def > 0)) {
    problems.push(`${boss.pokemon_name}: defence is ${def}`);
    continue;
  }
  const form = (stats.form ?? "").toLowerCase();
  const paren = boss.pokemon_name.match(/\(([^)]*)\)\s*$/);
  let want = null;
  if (paren) want = paren[1].replace(/\s*forme?\s*$/i, "").trim().toLowerCase();
  else for (const [re, f] of FORM_EXPECTED) if (re.test(boss.pokemon_name)) { want = f; break; }
  // An undecorated name means the ORDINARY form. Leaving want null here is what
  // let plain Mewtwo resolve to Armored and still report a pass.
  if (want === null && !/^(Mega|Primal)s+/i.test(boss.pokemon_name)) want = "normal";

  // Mega and Primal have no entry in this dataset at all, so they legitimately
  // fall back to the ordinary form. Reported, not failed.
  if (/^(Mega|Primal)\s+/i.test(boss.pokemon_name)) {
    notes.push(`${boss.pokemon_name}: no Mega stats upstream, using the base form's defence`);
    resolved++;
    continue;
  }
  if (want && form !== want) {
    problems.push(`${boss.pokemon_name}: resolved to the "${stats.form}" forme (def ${stats.base_defense}), expected "${want}"`);
    continue;
  }
  resolved++;
}
notes.push(`${resolved} of ${bosses.length} bosses resolve the correct forme`);

// 2. Score a stand in box against every boss. Species chosen to cover a normal attacker, a
//    shadow, and one whose move pool spans several types.
const box = [
  { pokemon_name: "Machamp", level: 40, atk: 15, def: 15, sta: 15, form: "normal" },
  { pokemon_name: "Machamp", level: 40, atk: 15, def: 15, sta: 15, form: "shadow" },
  { pokemon_name: "Tyranitar", level: 35, atk: 10, def: 12, sta: 14, form: "normal" },
  { pokemon_name: "Gardevoir", level: 25.5, atk: 0, def: 0, sta: 0, form: "normal" },
];

function cpmForLevel(level) {
  return (data.cpMultipliers ?? []).find((c) => c.level === level)?.multiplier ?? null;
}

let scored = 0;
let shadowBeatsNormal = false;
for (const { boss } of bosses) {
  for (const mon of box) {
    const cpm = cpmForLevel(mon.level);
    if (cpm === null) {
      problems.push(`no CP multiplier for level ${mon.level}, so a real box entry at that level would be dropped`);
      continue;
    }
    const config = { ...DEFAULT_CONFIG, cpm, atkIV: mon.atk, defIV: mon.def, staIV: mon.sta, form: mon.form };
    const range = calcMovesetRange(data, mon.pokemon_name, boss, config);
    if (!range) {
      problems.push(`${mon.pokemon_name} (${mon.form}) could not be scored against ${boss.pokemon_name}`);
      continue;
    }
    scored++;
    if (range.worst.dps > range.best.dps) {
      problems.push(`${mon.pokemon_name} vs ${boss.pokemon_name}: worst moveset ${range.worst.dps.toFixed(2)} beats best ${range.best.dps.toFixed(2)}`);
    }
    if (!(range.best.dps > 0) || !(range.best.tdo > 0)) {
      problems.push(`${mon.pokemon_name} vs ${boss.pokemon_name}: non positive DPS or TDO`);
    }
  }
}
notes.push(`${scored} box entries scored across every boss`);

// 3. Shadow must beat its own normal form, which is the whole reason the box records the flag.
const firstBoss = bosses[0]?.boss;
if (firstBoss) {
  const cpm = cpmForLevel(40);
  const base = { ...DEFAULT_CONFIG, cpm, atkIV: 15, defIV: 15, staIV: 15 };
  const normal = calcMovesetRange(data, "Machamp", firstBoss, { ...base, form: "normal" });
  const shadow = calcMovesetRange(data, "Machamp", firstBoss, { ...base, form: "shadow" });
  if (normal && shadow) {
    shadowBeatsNormal = shadow.best.dps > normal.best.dps;
    if (!shadowBeatsNormal) {
      problems.push("a shadow Pokemon did not out damage its own normal form, so the status is not reaching the calculation");
    }
  }
}

// 4. Every level a box entry can hold must produce a result. The upstream
//    multiplier table stops at 45, and taking that at face value silently
//    dropped every XL Pokemon from 45.5 to 50 out of the ranking, which is
//    precisely the set a trainer would be checking.
if (firstBoss) {
  const levels = [];
  for (let l = 1; l <= 50; l += 0.5) levels.push(l);
  const species = ["Machamp", "Tyranitar", "Lucario", "Conkeldurr", "Blaziken", "Hariyama", "Breloom", "Terrakion"];
  const box = levels.map((level, i) => ({
    id: i, pokemon_name: species[i % species.length], form: "Normal",
    is_shadow: false, is_purified: false, cp: 2000, level,
    atk_iv: 15, def_iv: 15, sta_iv: 15,
  }));
  // One level at a time, so the per species dedupe cannot mask a dropped level.
  const dropped = [];
  for (const entry of box) {
    // Vary the boss key per level: scoreCache is keyed on boss name plus box
    // length, so 99 single entry calls all collided on one memo entry and every
    // level after the first returned the first one's answer.
    const keyedBoss = { ...firstBoss, pokemon_name: firstBoss.pokemon_name + "#lvl" + entry.level };
    if (!scoreBoxFn(data, keyedBoss, [entry]).length) dropped.push(entry.level);
  }
  if (dropped.length) {
    problems.push(`levels with no CP multiplier, so a saved Pokemon at them is silently dropped: ${dropped.join(", ")}`);
  } else {
    notes.push(`all ${levels.length} possible levels from 1 to 50 produce a result`);
  }
}

// 5. The shortlist must not change the answer. Scoring only the best N entries is
//    an optimisation, so its top five has to match the top five you get by
//    scoring the whole box. An earlier cut silently lost the true best pick on
//    most bosses, which is the sort of thing that never shows up as an error.
{
  const seen = new Set();
  const box = [];
  for (const p of data.pokemon ?? []) {
    const key = p.pokemon_name.toLowerCase() + "|" + (p.form ?? "");
    if (seen.has(key)) continue;
    seen.add(key);
    box.push({
      id: box.length, pokemon_name: p.pokemon_name, form: p.form ?? "Normal",
      is_shadow: false, is_purified: false, cp: 2000, level: 40,
      atk_iv: 15, def_iv: 15, sta_iv: 15,
    });
  }
  let corrupted = 0;
  for (const { boss } of bosses) {
    setScoreLimitForTest(1e9);
    const truth = scoreBoxFn(data, boss, box).slice(0, 5).map((s) => s.entry.pokemon_name + "|" + s.entry.form);
    setScoreLimitForTest(null); // back to the shipped value
    const got = scoreBoxFn(data, boss, box).slice(0, 5).map((s) => s.entry.pokemon_name + "|" + s.entry.form);
    const missing = truth.filter((t) => !got.includes(t));
    if (missing.length) {
      corrupted++;
      problems.push(`${boss.pokemon_name}: the shortlist loses ${missing.length} of the true top 5 (${missing.join(", ")})`);
    }
  }
  if (!corrupted) notes.push(`shortlist top 5 matches full scoring on all ${bosses.length} bosses, over a ${box.length} entry box`);
}

// 6. The top counters list must still be produced for every boss.
for (const { boss } of bosses) {
  const counters = calcCounters(data, boss);
  if (!counters.length) problems.push(`${boss.pokemon_name}: no counters produced`);
}

for (const n of notes) console.log("  " + n);

if (problems.length) {
  console.error(`\nbox vs boss check FAILED with ${problems.length} problem(s):`);
  for (const p of problems.slice(0, 25)) console.error("  " + p);
  if (problems.length > 25) console.error(`  ...and ${problems.length - 25} more`);
  process.exit(1);
}

console.log("box vs boss: every boss resolves a real defence, every stand in Pokemon scores, no moveset range is inverted, and shadow status reaches the maths.");
