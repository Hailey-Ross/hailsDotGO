// Asserts the 20 Vivillon pattern cards actually render on the shiny checklist.
//
//   node scripts/check-vivillon-patterns.mjs
//
// Why this exists: like the Unown letters, the Vivillon patterns are the rare case where a
// checklist card's sprite id is a STRING slug rather than a number, because PokeAPI files each
// pattern as a pokemon-form of dex 666 with no id of its own. That makes silent failures possible
// that neither a Go test nor a curl probe can see, because they only happen when the page renders:
//
//   1. Meadow is the DEFAULT form, so its art is the plain 666.png; 666-meadow.png does not exist
//      upstream, and asking for it yields a broken image, not an error.
//   2. the hyphenated patterns are filed as 666-icy-snow / 666-high-plains / 666-poke-ball, not
//      666-icysnow / 666-highplains / 666-pokeball.
//   3. a Vivillon card reads "Meadow Vivillon" (pattern first, ordinary regional word order).
//
// So this bundles ts/shinies.ts, runs it against a stub DOM with a stub /api/data and a stub
// /api/shinies, and asserts on the grid the real page code built: one card per pattern plus the
// patternless base card, the exact shiny sprite URL of every pattern, the label word order, and
// that a caught Vivillon Elegant ticks the Elegant card and nothing else. It never touches the
// network: the sprite URLs are asserted as strings.
//
// It also checks every viv_* region tag fits user_shinies.region (VARCHAR(16)), because MySQL
// truncates an over-long value instead of rejecting it, which would file a catch under the wrong
// pattern.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const REGION_COLUMN_MAX = 16; // user_shinies.region is VARCHAR(16)

const shinyUrl = (id) =>
  `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;

// The spec, written out here independently of ts/shared/regionalForms.ts, so this check cannot
// agree with the code by simply reading the code's own answer back. Meadow is the default form
// (plain 666); the rest are 666-<slug>.
const PATTERNS = [
  { label: "Meadow",      region: "viv_meadow",      slug: "666" },
  { label: "Polar",       region: "viv_polar",       slug: "666-polar" },
  { label: "Tundra",      region: "viv_tundra",      slug: "666-tundra" },
  { label: "Continental", region: "viv_continental", slug: "666-continental" },
  { label: "Garden",      region: "viv_garden",      slug: "666-garden" },
  { label: "Elegant",     region: "viv_elegant",     slug: "666-elegant" },
  { label: "Icy Snow",    region: "viv_icy_snow",    slug: "666-icy-snow" },
  { label: "Modern",      region: "viv_modern",      slug: "666-modern" },
  { label: "Marine",      region: "viv_marine",      slug: "666-marine" },
  { label: "Archipelago", region: "viv_archipelago", slug: "666-archipelago" },
  { label: "High Plains", region: "viv_high_plains", slug: "666-high-plains" },
  { label: "Sandstorm",   region: "viv_sandstorm",   slug: "666-sandstorm" },
  { label: "River",       region: "viv_river",       slug: "666-river" },
  { label: "Monsoon",     region: "viv_monsoon",     slug: "666-monsoon" },
  { label: "Savanna",     region: "viv_savanna",     slug: "666-savanna" },
  { label: "Sun",         region: "viv_sun",         slug: "666-sun" },
  { label: "Ocean",       region: "viv_ocean",       slug: "666-ocean" },
  { label: "Jungle",      region: "viv_jungle",      slug: "666-jungle" },
  // Event exclusive: the art exists but the shiny has never been obtainable, so these two get no
  // card by default and only appear once a trainer ticks "show unreleased".
  { label: "Fancy",       region: "viv_fancy",       slug: "666-fancy",     unreleased: true },
  { label: "Poke Ball",   region: "viv_poke_ball",   slug: "666-poke-ball", unreleased: true },
];

const RELEASED = PATTERNS.filter((p) => !p.unreleased);
const UNRELEASED = PATTERNS.filter((p) => p.unreleased);

// ------------------------------------------------------------------- the fixtures
// Enough of /api/data to drive the grid: Vivillon, plus a species with an ordinary regional form so
// a mistake in the pattern path cannot hide behind an empty grid.
const GAME_DATA = {
  shinies: {
    Rattata: { id: 19, name: "Rattata" },
    Vivillon: { id: 666, name: "Vivillon" },
  },
};

// One caught Vivillon, Elegant pattern only. Nothing else in the collection.
const CAUGHT = PATTERNS.find((p) => p.label === "Elegant");
const USER_SHINIES = [
  {
    id: 7,
    pokemon_id: "Vivillon",
    form: "",
    region: CAUGHT.region,
    costume: "",
    event_tag: "",
    method: "wild",
    caught_at: "2026-07-01T00:00:00Z",
    evolved_at: null,
  },
];

// ------------------------------------------------------------------- the stub DOM
function stubDom() {
  const el = (tag) => {
    const classes = new Set();
    const found = new Map();
    const n = {
      tag,
      style: { cssText: "", display: "" },
      dataset: {},
      children: [],
      value: "",
      _text: "",
      _html: "",
      classList: {
        add: (...c) => c.forEach((x) => x && classes.add(x)),
        remove: (...c) => c.forEach((x) => classes.delete(x)),
        contains: (c) => classes.has(c),
      },
      get className() {
        return [...classes].join(" ");
      },
      set className(v) {
        classes.clear();
        String(v).split(/\s+/).filter(Boolean).forEach((c) => classes.add(c));
      },
      set innerHTML(v) {
        this._html = String(v);
        this.children.length = 0;
      },
      get innerHTML() {
        return this._html;
      },
      set textContent(v) {
        this._text = String(v);
        this.children.length = 0;
      },
      get textContent() {
        return this._text;
      },
      get childElementCount() {
        return this.children.length;
      },
      appendChild(c) {
        this.children.push(c);
        return c;
      },
      setAttribute() {},
      remove() {},
      addEventListener(evt, fn) {
        (this._on ??= {})[evt] ??= [];
        this._on[evt].push(fn);
      },
      click() {
        (this._on?.click ?? []).forEach((f) => f({ target: this }));
      },
      // The page reaches into the markup it just set as innerHTML (the modal's close button, its
      // name row). Hand back a stable stub per selector so those bindings do not throw.
      querySelector(sel) {
        if (!found.has(sel)) found.set(sel, el("div"));
        return found.get(sel);
      },
      querySelectorAll: () => [],
      scrollIntoView() {},
    };
    return n;
  };

  // Ids are auto-vivified: the page writes its own markup as an innerHTML string and then looks the
  // pieces up by id, so a stub that only knows #shinies-app up front would break on the rest.
  const byId = new Map();
  const getById = (id) => {
    if (!byId.has(id)) byId.set(id, el("div"));
    return byId.get(id);
  };

  globalThis.document = {
    getElementById: getById,
    createElement: el,
    // The only document-level query the page makes is for the CSRF meta tag.
    querySelector: () => ({ content: "test-csrf" }),
    querySelectorAll: () => [],
    addEventListener() {},
    body: el("body"),
  };
  globalThis.location = { search: "" };
  return { getById };
}

// Everything the shinies template injects. Unknown keys yield their own name, so this harness tests
// the Vivillon cards rather than the completeness of my locale fixture.
const strings = (extra) =>
  new Proxy(extra, { get: (t, k) => (k in t ? t[k] : typeof k === "string" ? k : undefined) });

globalThis.JSC = strings({
  regionalName: "{region} {name}", // the order for a pattern: "Meadow Vivillon"
  unownName: "{name} {letter}",
  formNormal: "Normal",
  formAlolan: "Alolan",
});
globalThis.SH = strings({
  counts: "{unique} / {total}",
  heading: "Shiny checklist",
});
globalThis.SITE_LANG = "en";
globalThis.COSTUME_LABELS = null; // fall back to the compiled-in labels

const { getById } = stubDom();
globalThis.fetch = async (url) => {
  const path = String(url);
  if (path.startsWith("/api/shinies")) return { ok: true, status: 200, json: async () => USER_SHINIES };
  if (path === "/api/app/data" || path === "/api/data") {
    return { ok: true, status: 200, json: async () => GAME_DATA };
  }
  return { ok: false, status: 404, json: async () => ({}) };
};

// ---------------------------------------------------------------------- the run
const tmp = mkdtempSync(join(tmpdir(), "vivillon-patterns-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
  console.error("\nThe Vivillon patterns do not render. Open /shinies, search Vivillon, and read the console.");
  rmSync(tmp, { recursive: true, force: true });
  process.exit(1);
};

try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  // Import the page for its side effects (it renders on import), and re-export the pattern table so
  // the region-tag length check runs against the same constant the page ships.
  writeFileSync(
    entry,
    `import ${JSON.stringify(join(process.cwd(), "ts/shinies.ts"))};\n` +
      `export { VIVILLON_PATTERNS } from ${JSON.stringify(join(process.cwd(), "ts/shared/regionalForms.ts"))};\n`,
  );
  await build({
    entryPoints: [entry],
    bundle: true,
    outfile: out,
    platform: "node",
    format: "esm",
    logLevel: "silent",
  });

  const mod = await import("file://" + out.replace(/\\/g, "/"));
  await new Promise((r) => setTimeout(r, 80)); // let the two fetch promises settle

  if (thrown.length) fail(`the checklist threw: ${thrown.map(String).join("; ")}`);

  const content = getById("sc-content");
  const grid = content.children.find((c) => c.classList.contains("shiny-grid"));
  if (!grid) fail(`the shiny grid did not render (content: ${content.innerHTML || "(empty)"})`);

  const cards = grid.children.filter((c) => c.classList.contains("shiny-tag"));
  if (!cards.length) fail("the grid rendered no cards");

  const cardInfo = (card) => {
    const img = card.children.find((c) => c.tag === "img");
    const label = card.children.find((c) => c.classList.contains("shiny-label"));
    return {
      src: img?.src ?? "",
      alt: img?.alt ?? "",
      label: label?.textContent ?? "",
      caught: card.classList.contains("sc-caught"),
    };
  };
  const vivillon = cards.map(cardInfo).filter((c) => c.alt === "Vivillon");

  // 1. Exactly 19 cards by default: the patternless base card plus the 18 patterns with a
  //    released shiny, no duplicates. Fancy and Poke Ball are event exclusive and must NOT be here.
  const WANT_CARDS = 1 + RELEASED.length;
  if (vivillon.length !== WANT_CARDS) {
    fail(`Vivillon rendered ${vivillon.length} cards, want ${WANT_CARDS} (base + ${RELEASED.length} released patterns)`);
  }
  for (const p of UNRELEASED) {
    if (vivillon.find((c) => c.label === `${p.label} Vivillon`)) {
      fail(`${p.label} has no released shiny but got a card in the default view`);
    }
  }
  const labels = vivillon.map((c) => c.label);
  const dupes = labels.filter((l, i) => labels.indexOf(l) !== i);
  if (dupes.length) fail(`duplicate Vivillon cards: ${[...new Set(dupes)].join(", ")}`);

  const base = vivillon.find((c) => c.label === "Vivillon");
  if (!base) fail("the patternless base Vivillon card is missing");
  if (base.src !== shinyUrl(666)) fail(`the base Vivillon card points at ${base.src}, want ${shinyUrl(666)}`);

  // 2. Every pattern card exists, and its shiny sprite URL is exactly right. The traps: Meadow must
  //    be plain 666 (666-meadow does not exist upstream), and the hyphenated slugs must be spelled
  //    out (666-icy-snow, 666-high-plains, 666-poke-ball).
  // 3. The label reads "Meadow Vivillon" (pattern first).
  for (const p of RELEASED) {
    const want = `${p.label} Vivillon`;
    const card = vivillon.find((c) => c.label === want);
    if (!card) fail(`no card labelled ${JSON.stringify(want)} (labels: ${labels.join(", ")})`);
    const wantSrc = shinyUrl(p.slug);
    if (card.src !== wantSrc) {
      fail(`${want} points at ${JSON.stringify(card.src)}, want ${JSON.stringify(wantSrc)}`);
    }
  }

  // 4. Ticking is per pattern: a caught viv_elegant ticks the Elegant card and nothing else.
  const ticked = vivillon.filter((c) => c.caught).map((c) => c.label);
  const wantTicked = `${CAUGHT.label} Vivillon`;
  if (ticked.length !== 1 || ticked[0] !== wantTicked) {
    fail(
      `a caught ${CAUGHT.region} ticked ${ticked.length} card(s) ` +
        `(${ticked.join(", ") || "none"}), want only ${JSON.stringify(wantTicked)}`,
    );
  }

  // 5. Every region tag fits the column. MySQL truncates rather than errors, so an over-long tag
  //    would file the catch under a different pattern, silently.
  const table = mod.VIVILLON_PATTERNS;
  if (table.length !== PATTERNS.length) {
    fail(`VIVILLON_PATTERNS has ${table.length} entries, want ${PATTERNS.length}`);
  }
  for (const p of table) {
    const want = PATTERNS.find((s) => s.region === p.region);
    if (!want) fail(`unexpected pattern region tag ${JSON.stringify(p.region)}`);
    if (want.label !== p.label) {
      fail(`pattern ${JSON.stringify(p.region)} has label ${JSON.stringify(p.label)}, want ${JSON.stringify(want.label)}`);
    }
    if (p.region.length > REGION_COLUMN_MAX) {
      fail(
        `region tag ${JSON.stringify(p.region)} is ${p.region.length} chars, over the ` +
          `${REGION_COLUMN_MAX}-char user_shinies.region column: MySQL would truncate it`,
      );
    }
  }

  console.log(
    `Vivillon patterns: the checklist renders ${vivillon.length} Vivillon cards (base + ${RELEASED.length} released ` +
    `patterns, with ${UNRELEASED.map((p) => p.label).join(" and ")} correctly absent), ` +
      `each with the right shiny sprite (Meadow is 666, hyphenated slugs like 666-icy-snow are spelled right), ` +
      `each labelled pattern first ("Meadow Vivillon"), a caught viv_elegant ticks only the Elegant card, and ` +
      `every region tag fits the ${REGION_COLUMN_MAX}-char column.`,
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
