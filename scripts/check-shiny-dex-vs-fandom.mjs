// Cross checks our shiny data against Fandom's List of Shiny Pokemon.
//
//   node scripts/check-shiny-dex-vs-fandom.mjs
//   npm run check:fandom
//
// This is the answer to "how do we not fall behind again". The embedded baseline
// (internal/pogodata/fallback/shiny_baseline.json) and the regional form tables are hand curated
// from research; this re-runs that comparison against a live third source in a few seconds.
//
// Deliberately NOT part of `npm run check`. That chain must stay offline and deterministic, and a
// network fetch in it would make an unrelated change fail whenever Fandom is slow or its markup
// shifts. Run this before a release, or when a trainer reports a missing shiny.
//
// Three things about the page that will waste your afternoon if you rediscover them:
//
//   1. The normal page URL returns 403 to anything that is not a browser, and 402 through some
//      proxies. The MediaWiki API works: api.php?action=parse&prop=text.
//   2. The wikitext is NOT enough. Entries are {{PS|Name|dex|...}} and hundreds of them resolve
//      availability through a {{#if:{{Data/Shiny...}}}} template conditional, so you need the
//      RENDERED html to know what is greyed.
//   3. The page is FORM level, not species level: ~1419 entries for 1025 species. Arceus appears
//      18 times, Spinda 11, Minior 7. A greyed row does not mean the species has no shiny. The
//      rule is: a species is released if ANY of its forms is not greyed.
//
// Form identity is not in the visible name either (every Vivillon row just reads "Vivillon"). It
// is in the image filename: Vivillon_fancy_shiny.png, Tauros_combat_shiny.png.

import { readFileSync } from "node:fs";

const API = "https://pokemongo.fandom.com/api.php" +
  "?action=parse&page=List_of_Shiny_Pok%C3%A9mon&prop=text&format=json&formatversion=2";
const UA = "hailsDotGO shiny dex cross check (https://pogo.hails.app)";
const BASELINE = "internal/pogodata/fallback/shiny_baseline.json";
const TS = "ts/shared/regionalForms.ts";

const problems = [];
const notes = [];

// ---------------------------------------------------------------- fetch and parse the page
async function fandomForms() {
  const res = await fetch(API, { headers: { "User-Agent": UA, Accept: "application/json" } });
  if (!res.ok) throw new Error(`Fandom API returned HTTP ${res.status}`);
  const html = (await res.json())?.parse?.text;
  if (typeof html !== "string" || html.length < 10000) throw new Error("Fandom API returned no usable html");

  // Each entry is a <div class="pogo-list-item"> or "pogo-list-item greyed-out".
  const blocks = html.split(/(?=<div class="pogo-list-item(?: greyed-out)?")/).slice(1);
  const byDex = new Map(); // dex -> [{ key, released }]
  for (const b of blocks) {
    const dexM = /pogo-list-item-number"[^>]*>#(\d+)</.exec(b);
    if (!dexM) continue;
    const imgM = /data-image-key="([^"]+)"/.exec(b) || /\/([^/"]+\.png)/.exec(b);
    const released = !/^<div class="pogo-list-item greyed-out"/.test(b);
    const dex = Number(dexM[1]);
    const key = (imgM ? imgM[1] : "").toLowerCase().replace(/_shiny\.png$/, "");
    if (!byDex.has(dex)) byDex.set(dex, []);
    byDex.get(dex).push({ key, released });
  }
  if (byDex.size < 900) throw new Error(`only parsed ${byDex.size} species; the page markup has probably changed`);
  return byDex;
}

// ---------------------------------------------------------------- species level
function checkSpecies(byDex) {
  const baseline = JSON.parse(readFileSync(BASELINE, "utf8"));
  // A species is released if ANY of its forms is not greyed.
  const theirs = new Map([...byDex].map(([dex, forms]) => [dex, forms.some((f) => f.released)]));

  let compared = 0;
  for (const [key, e] of Object.entries(baseline)) {
    const dex = Number(key);
    if (!theirs.has(dex)) {
      problems.push(`species: dex ${dex} (${e.name}) is in our baseline but absent from the Fandom page.`);
      continue;
    }
    compared++;
    if (theirs.get(dex) !== e.shiny_released) {
      problems.push(
        `species: dex ${dex} ${e.name}: we say shiny_released ${e.shiny_released}, Fandom says ${theirs.get(dex)}.`,
      );
    }
  }
  for (const dex of theirs.keys()) {
    if (!baseline[String(dex)]) problems.push(`species: Fandom lists dex ${dex} but our baseline has no such species.`);
  }
  const ourReleased = Object.values(baseline).filter((e) => e.shiny_released).length;
  const theirReleased = [...theirs.values()].filter(Boolean).length;
  notes.push(`species: compared ${compared}; we say ${ourReleased} released, Fandom says ${theirReleased}.`);
}

// ---------------------------------------------------------------- form level
// Our region tags do not match Fandom's image keys, so this is an explicit table. Anything not
// listed here is reported as UNMATCHED rather than skipped: a check that quietly ignores what it
// cannot understand is worse than no check, and skipping is exactly how the Paldean Tauros
// disagreement stayed hidden the first time this comparison was run by hand.
const FORM_KEYS = {
  alolan: ["alolan"], galarian: ["galarian"], hisuian: ["hisuian"], paldean: ["paldean"],
  paldean_combat: ["combat"], paldean_blaze: ["blaze"], paldean_aqua: ["aqua"],
  therian: ["therian"], origin: ["origin"], sky: ["sky"],
  attack: ["attack"], defense: ["defense"], speed: ["speed"],
  dusk_mane: ["dusk_mane", "duskmane"], dawn_wings: ["dawn_wings", "dawnwings"],
  crowned_sword: ["crowned_sword", "crowned"], crowned_shield: ["crowned_shield", "crowned"],
  black: ["black"], white: ["white"], resolute: ["resolute"],
  midnight: ["midnight"], dusk: ["dusk"],
  sandy_cloak: ["sandy"], trash_cloak: ["trash"], low_key: ["low-key", "low_key", "lowkey"],
  pom_pom: ["pom-pom", "pom_pom"], pau: ["pa%27u", "pa'u", "pau"], sensu: ["sensu"],
  blue_striped: ["blue"], white_striped: ["white-striped", "white_striped"],
  wash: ["wash"], heat: ["heat"], frost: ["frost"], fan: ["fan"], mow: ["mow"],
};

function ourForms() {
  const ts = readFileSync(TS, "utf8");
  const body = ts.slice(ts.indexOf("export const REGIONAL_FORMS"), ts.indexOf("\n};", ts.indexOf("export const REGIONAL_FORMS")));
  const out = [];
  for (const m of body.matchAll(/"([^"]+)":\s*\[([\s\S]*?)\]/g)) {
    for (const r of m[2].matchAll(/region:\s*"([^"]+)",\s*variantId:\s*(\d+),\s*shiny:\s*(true|false)/g)) {
      out.push({ species: m[1], region: r[1], shiny: r[3] === "true" });
    }
  }
  // Vivillon's patterns are generated from their own array, so parse that too.
  const viv = ts.slice(ts.indexOf("export const VIVILLON_PATTERNS"), ts.indexOf("\n];", ts.indexOf("export const VIVILLON_PATTERNS")));
  for (const m of viv.matchAll(/\{\s*region:\s*"(viv_[a-z_]+)"[^}]*\}/g)) {
    out.push({ species: "Vivillon", region: m[1], shiny: !/shiny:\s*false/.test(m[0]), viv: true });
  }
  return out;
}

// Forms Fandom's page simply does not carry. These are gaps in THEIR data, not ours, so they are
// reported as notes rather than failures. Each one needs a reason, and the check still shouts if a
// gap disappears, so the list cannot rot into a way of silencing real drift.
const KNOWN_FANDOM_GAPS = {
  "Vivillon|viv_archipelago":
    "Fandom's shiny list carries only 19 of the 20 Vivillon patterns and omits Archipelago " +
    "(Caribbean and Southeast Asian islands). The pattern is real and our data is correct.",
};

function checkForms(byDex) {
  const baseline = JSON.parse(readFileSync(BASELINE, "utf8"));
  const dexOf = new Map(Object.values(baseline).map((e) => [e.name, e.id]));

  let agree = 0, unmatched = 0;
  const gapsSeen = new Set();
  for (const f of ourForms()) {
    const gapKey = `${f.species}|${f.region}`;
    const dex = dexOf.get(f.species);
    const forms = dex ? byDex.get(dex) : null;
    if (!forms) {
      problems.push(`form: ${f.species}|${f.region}: no Fandom entry for dex ${dex ?? "?"}.`);
      continue;
    }
    // Vivillon patterns key off the pattern name inside the filename.
    const keys = f.viv ? [f.region.replace(/^viv_/, "").replace(/_/g, "")] : FORM_KEYS[f.region];
    if (!keys) {
      unmatched++;
      problems.push(`form: ${f.species}|${f.region}: no entry in FORM_KEYS, so it was never checked. Add one.`);
      continue;
    }
    const hits = forms.filter((x) => keys.some((k) => x.key.replace(/[_-]/g, "").includes(k.replace(/[_-]/g, ""))));
    if (!hits.length) {
      if (KNOWN_FANDOM_GAPS[gapKey]) {
        gapsSeen.add(gapKey);
        notes.push(`forms: ${gapKey} not on the page. ${KNOWN_FANDOM_GAPS[gapKey]}`);
        continue;
      }
      unmatched++;
      problems.push(
        `form: ${gapKey}: UNMATCHED against Fandom (page has: ${forms.map((x) => x.key).join(", ")}).`,
      );
      continue;
    }
    const theirs = hits.some((x) => x.released);
    if (theirs === f.shiny) agree++;
    else {
      problems.push(
        `form: ${f.species}|${f.region}: we say shiny ${f.shiny}, Fandom says ${theirs} ` +
        `(matched: ${hits.map((x) => x.key).join(", ")}).`,
      );
    }
  }
  // If Fandom fills a gap in, say so: a stale allowance is how a real disagreement gets hidden.
  for (const key of Object.keys(KNOWN_FANDOM_GAPS)) {
    if (!gapsSeen.has(key)) {
      problems.push(`form: ${key} is listed in KNOWN_FANDOM_GAPS but Fandom now carries it. Remove the entry and re-check.`);
    }
  }
  notes.push(`forms: ${agree} agree, ${unmatched} unmatched, ${gapsSeen.size} known Fandom gap(s).`);
}

// ---------------------------------------------------------------------- run
try {
  const byDex = await fandomForms();
  checkSpecies(byDex);
  checkForms(byDex);
} catch (e) {
  console.error(`FAILED: could not compare against Fandom: ${e.message}`);
  console.error("\nThis check needs the network. If Fandom is down, skip it; `npm run check` does not depend on it.");
  process.exit(1);
}

for (const n of notes) console.log("  " + n);

if (problems.length) {
  console.error(`\nFAILED: ${problems.length} disagreement(s) with Fandom's List of Shiny Pokemon.\n`);
  for (const p of problems) console.error("  - " + p);
  console.error(
    "\nFandom is a third source, not gospel: check Serebii and a dated announcement before changing anything.\n" +
    "A species level change means regenerating the baseline; a form level change means editing the shiny flag\n" +
    "in ts/shared/regionalForms.ts AND the matching entry in internal/handlers/regional.go (the parity script\n" +
    "will fail if you only do one). Anything urgent can be toggled in the admin Shiny Dex tab instead.",
  );
  process.exit(1);
}

console.log("\nshiny dex vs Fandom: no drift. Species availability and every regional form agree.");
