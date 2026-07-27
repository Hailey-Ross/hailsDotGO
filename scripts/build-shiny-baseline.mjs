// Builds internal/pogodata/fallback/shiny_baseline.json, the embedded National Dex truth table.
//
//   node scripts/build-shiny-baseline.mjs            # regenerate the baseline
//   node scripts/build-shiny-baseline.mjs --check     # verify the committed file still matches
//
// The baseline says, for all 1025 species, whether it is in Pokemon GO and whether its shiny has
// been released. It replaces "is this dex a key in the PoGoAPI shinies map" as the availability
// signal, which is what made every shiny release a deploy.
//
// Flags come from research fragments under .claude/tmp/, split by dex range. NAMES DO NOT: they are
// derived mechanically from the PoGoAPI stats feed the app already ships. That matters because
// user_shinies.pokemon_id stores the species NAME, so a hand-typed "Farfetch'd" with a straight
// apostrophe would orphan real collection rows. Nobody types a name.
//
// .claude/ is not committed, so a fresh clone cannot regenerate; --check degrades to validating the
// committed baseline on its own terms. The load-bearing guard is TestShinyBaselineIntegrity in
// internal/pogodata/shinydex_test.go, which runs on every `go test`.

import { readFileSync, writeFileSync, existsSync } from "node:fs";

const FRAGMENTS = [
  ".claude/tmp/shiny_dex_A.json",
  ".claude/tmp/shiny_dex_B.json",
  ".claude/tmp/shiny_dex_C.json",
];
const STATS = "internal/pogodata/fallback/pokemon.json";
const OUT = "internal/pogodata/fallback/shiny_baseline.json";
const DEX_MAX = 1025;

// National Dex species the PoGoAPI stats feed has no row for, so their name has to come from
// somewhere. The feed only carries species with GO stats, and the baseline covers the whole
// National Dex including species the game has never shipped. Every entry here is a deliberate,
// documented exception, not a fallback for a typo.
//
// Basculegion is the only one today. It is absent from the stats feed AND flagged in_go false: the
// research found it has never been released (White-Striped Basculin is in GO but cannot evolve),
// so the two facts agree rather than conflict.
const STATS_GAPS = { 902: "Basculegion" };

const CHECK = process.argv.includes("--check");
const problems = [];

// ------------------------------------------------------------------ names, from the stats feed
function speciesNames() {
  const stats = JSON.parse(readFileSync(STATS, "utf8"));
  const names = new Map();
  for (const row of stats) {
    // One species has many form rows; the first is the base form and carries the plain name.
    if (!names.has(row.pokemon_id)) names.set(row.pokemon_id, row.pokemon_name);
  }
  for (const [dex, name] of Object.entries(STATS_GAPS)) {
    if (names.has(Number(dex))) {
      problems.push(`dex ${dex} is now in ${STATS}; drop it from STATS_GAPS in this script.`);
    }
    names.set(Number(dex), name);
  }
  return names;
}

// ------------------------------------------------------------------ flags, from the fragments
function fragmentFlags() {
  const flags = new Map();
  const seenIn = new Map();
  for (const path of FRAGMENTS) {
    if (!existsSync(path)) return null;
    const raw = readFileSync(path);
    if (raw[0] === 0xef && raw[1] === 0xbb && raw[2] === 0xbf) {
      problems.push(`${path} has a UTF-8 BOM; rewrite it without one.`);
    }
    const data = JSON.parse(raw.toString("utf8"));
    for (const [key, row] of Object.entries(data)) {
      const dex = Number(key);
      if (!Number.isInteger(dex) || dex < 1 || dex > DEX_MAX) {
        problems.push(`${path}: ${JSON.stringify(key)} is not a National Dex number.`);
        continue;
      }
      if (flags.has(dex)) {
        problems.push(`dex ${dex} appears in both ${seenIn.get(dex)} and ${path}; the ranges overlap.`);
        continue;
      }
      if (typeof row.in_go !== "boolean" || typeof row.shiny_released !== "boolean") {
        problems.push(`${path}: dex ${dex} is missing a boolean in_go or shiny_released.`);
        continue;
      }
      // The invariant the research agents were told to hold. Enforced rather than trusted.
      if (row.shiny_released && !row.in_go) {
        problems.push(`${path}: dex ${dex} is shiny_released but not in_go.`);
        continue;
      }
      flags.set(dex, row);
      seenIn.set(dex, path);
    }
  }
  return flags;
}

// ------------------------------------------------------------------ build
function build() {
  const names = speciesNames();
  const flags = fragmentFlags();
  if (flags === null) return null;

  const out = {};
  for (let dex = 1; dex <= DEX_MAX; dex++) {
    const row = flags.get(dex);
    if (!row) {
      problems.push(`no fragment covers dex ${dex}.`);
      continue;
    }
    const name = names.get(dex);
    if (!name) {
      problems.push(`dex ${dex} has no name in ${STATS} and is not a documented exception.`);
      continue;
    }
    out[String(dex)] = { id: dex, name, in_go: row.in_go, shiny_released: row.shiny_released };
  }

  // The released set can never shrink below what the app already shows, or a regeneration would
  // silently remove a species somebody is currently collecting.
  for (const file of ["internal/pogodata/fallback/shinies.json", "internal/pogodata/fallback/shinies_supplement.json"]) {
    const listed = JSON.parse(readFileSync(file, "utf8"));
    for (const key of Object.keys(listed)) {
      const entry = out[key];
      if (!entry) {
        problems.push(`${file} lists dex ${key} but the baseline has no such species.`);
      } else if (!entry.shiny_released) {
        problems.push(`${file} lists dex ${key} (${entry.name}) as shiny, but the fragments say shiny_released false.`);
      }
    }
  }
  return out;
}

// Keys ascending numerically so the diff of a regeneration is readable, one species per line.
function serialise(table) {
  const lines = Object.keys(table)
    .map(Number)
    .sort((a, b) => a - b)
    .map((dex) => {
      const e = table[String(dex)];
      return `  ${JSON.stringify(String(dex))}: ${JSON.stringify(e)}`;
    });
  return "{\n" + lines.join(",\n") + "\n}\n";
}

const table = build();

if (table === null) {
  if (!CHECK) {
    console.error(`FAILED: the research fragments are missing.\n  Expected: ${FRAGMENTS.join(", ")}`);
    process.exit(1);
  }
  // A fresh clone has no .claude/. Validate what is committed instead of failing for being tidy.
  if (!existsSync(OUT)) {
    console.error(`FAILED: ${OUT} does not exist and the fragments to rebuild it are not here either.`);
    process.exit(1);
  }
  const raw = readFileSync(OUT);
  if (raw[0] === 0xef) { console.error(`FAILED: ${OUT} has a UTF-8 BOM.`); process.exit(1); }
  const committed = JSON.parse(raw.toString("utf8"));
  const n = Object.keys(committed).length;
  if (n !== DEX_MAX) { console.error(`FAILED: ${OUT} has ${n} species, want ${DEX_MAX}.`); process.exit(1); }
  console.log(`shiny baseline: ${n} species committed and well formed (research fragments absent, so no rebuild diff).`);
  process.exit(0);
}

if (problems.length) {
  console.error("FAILED: the shiny baseline cannot be built.\n");
  for (const p of problems.slice(0, 40)) console.error("  - " + p);
  if (problems.length > 40) console.error(`  ... and ${problems.length - 40} more`);
  process.exit(1);
}

const text = serialise(table);

if (CHECK) {
  if (!existsSync(OUT)) { console.error(`FAILED: ${OUT} does not exist. Run without --check.`); process.exit(1); }
  if (readFileSync(OUT, "utf8") !== text) {
    console.error(`FAILED: ${OUT} does not match a rebuild from the research fragments.`);
    console.error("  Someone hand edited the baseline, or a fragment changed. Re-run without --check.");
    process.exit(1);
  }
  console.log(`shiny baseline: ${Object.keys(table).length} species, matches a rebuild from the fragments.`);
} else {
  writeFileSync(OUT, text, { encoding: "utf8" });
  const released = Object.values(table).filter((e) => e.shiny_released).length;
  const inGo = Object.values(table).filter((e) => e.in_go).length;
  console.log(
    `shiny baseline: wrote ${Object.keys(table).length} species to ${OUT} ` +
    `(${inGo} in Pokemon GO, ${released} with a shiny released, ${inGo - released} awaiting one, ` +
    `${DEX_MAX - inGo} not in the game).`,
  );
}
