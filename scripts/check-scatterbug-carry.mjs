// Asserts that Scatterbug and Spewpa CARRY a Vivillon pattern without showing it, and that the
// pattern rides through evolution to the matching Vivillon.
//
//   node scripts/check-scatterbug-carry.mjs
//
// In Pokemon GO the pattern is decided when you catch the Scatterbug and rides up the line, but
// Scatterbug and Spewpa look identical across every region (PokeAPI has no per-pattern art for
// them). So the pattern is carry-only: recordable on the entry and propagated on evolve, but it
// must not spawn 20 near-identical cards and must not change the sprite. Three things can break
// silently, none of which a Go test can see because they only happen when the page renders:
//
//   1. a carry-only pattern leaking into a card, giving Scatterbug/Spewpa 21 cards each.
//   2. a patterned Scatterbug failing to tick the single base card (because its region did not
//      collapse to "" for the checklist key), so the base card reads as missing.
//   3. the region failing to propagate on evolve, so a Spewpa drops to the patternless Vivillon.
//
// So this bundles ts/shinies.ts, renders it against a stub DOM, and asserts the grid the real page
// built, plus it re-exports getEvolveTargets and checks the region carries Scatterbug -> Spewpa ->
// Vivillon. It never touches the network: sprite URLs are asserted as strings.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const shinyUrl = (id) =>
  `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;

const CAUGHT_REGION = "viv_elegant"; // an arbitrary pattern, set on the caught Scatterbug

// ------------------------------------------------------------------- the fixtures
// The whole Scatterbug line, plus an ordinary species so a mistake cannot hide behind an empty grid.
const GAME_DATA = {
  shinies: {
    Rattata: { id: 19, name: "Rattata" },
    Scatterbug: { id: 664, name: "Scatterbug" },
    Spewpa: { id: 665, name: "Spewpa" },
    Vivillon: { id: 666, name: "Vivillon" },
  },
};

// One caught Scatterbug, tagged with a pattern. It must tick the single base Scatterbug card.
const USER_SHINIES = [
  {
    id: 7,
    pokemon_id: "Scatterbug",
    form: "",
    region: CAUGHT_REGION,
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
      querySelector(sel) {
        if (!found.has(sel)) found.set(sel, el("div"));
        return found.get(sel);
      },
      querySelectorAll: () => [],
      scrollIntoView() {},
    };
    return n;
  };

  const byId = new Map();
  const getById = (id) => {
    if (!byId.has(id)) byId.set(id, el("div"));
    return byId.get(id);
  };

  globalThis.document = {
    getElementById: getById,
    createElement: el,
    querySelector: () => ({ content: "test-csrf" }),
    querySelectorAll: () => [],
    addEventListener() {},
    body: el("body"),
  };
  globalThis.location = { search: "" };
  return { getById };
}

const strings = (extra) =>
  new Proxy(extra, { get: (t, k) => (k in t ? t[k] : typeof k === "string" ? k : undefined) });

globalThis.JSC = strings({
  regionalName: "{region} {name}",
  unownName: "{name} {letter}",
  formNormal: "Normal",
  formAlolan: "Alolan",
});
globalThis.SH = strings({ counts: "{unique} / {total}", heading: "Shiny checklist" });
globalThis.SITE_LANG = "en";
globalThis.COSTUME_LABELS = null;

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
const tmp = mkdtempSync(join(tmpdir(), "scatterbug-carry-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
  console.error("\nScatterbug pattern carry is broken. Open /shinies, search Scatterbug, and read the console.");
  rmSync(tmp, { recursive: true, force: true });
  process.exit(1);
};

try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  writeFileSync(
    entry,
    `import ${JSON.stringify(join(process.cwd(), "ts/shinies.ts"))};\n` +
      `export { getEvolveTargets } from ${JSON.stringify(join(process.cwd(), "ts/shared/evolutions.ts"))};\n`,
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
  await new Promise((r) => setTimeout(r, 80));

  if (thrown.length) fail(`the checklist threw: ${thrown.map(String).join("; ")}`);

  const content = getById("sc-content");
  const grid = content.children.find((c) => c.classList.contains("shiny-grid"));
  if (!grid) fail(`the shiny grid did not render (content: ${content.innerHTML || "(empty)"})`);

  const cards = grid.children.filter((c) => c.classList.contains("shiny-tag"));
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
  const all = cards.map(cardInfo);
  const bySpecies = (name) => all.filter((c) => c.alt === name);

  // 1. Scatterbug and Spewpa are a single card each (no pattern cards); Vivillon still fans out.
  for (const [name, want] of [["Scatterbug", 1], ["Spewpa", 1], ["Vivillon", 21]]) {
    const n = bySpecies(name).length;
    if (n !== want) fail(`${name} rendered ${n} card(s), want ${want}`);
  }

  // 2. The caught patterned Scatterbug ticks the single base card, and its sprite is the plain
  //    664 -- never 664-elegant (no such art) and never the Vivillon 666-elegant.
  const scatter = bySpecies("Scatterbug")[0];
  if (!scatter.caught) fail(`a caught ${CAUGHT_REGION} Scatterbug did not tick the base Scatterbug card`);
  if (scatter.src !== shinyUrl(664)) {
    fail(`the Scatterbug card points at ${scatter.src}, want the plain ${shinyUrl(664)}`);
  }
  if (scatter.label !== "Scatterbug") fail(`the Scatterbug card is labelled ${JSON.stringify(scatter.label)}, want "Scatterbug"`);
  // Spewpa is present, plain, and not caught.
  const spewpa = bySpecies("Spewpa")[0];
  if (spewpa.caught) fail("the Spewpa card is ticked, but nothing Spewpa was caught");
  if (spewpa.src !== shinyUrl(665)) fail(`the Spewpa card points at ${spewpa.src}, want the plain ${shinyUrl(665)}`);

  // 3. The pattern carries on evolve: Scatterbug -> Spewpa -> Vivillon, all keeping viv_elegant.
  const step = (species) => {
    const t = mod.getEvolveTargets(species, CAUGHT_REGION);
    if (t.length !== 1) fail(`getEvolveTargets(${species}) returned ${t.length} targets, want 1`);
    return t[0];
  };
  const toSpewpa = step("Scatterbug");
  if (toSpewpa.name !== "Spewpa" || toSpewpa.region !== CAUGHT_REGION) {
    fail(`Scatterbug evolves to ${JSON.stringify(toSpewpa)}, want Spewpa keeping ${CAUGHT_REGION}`);
  }
  const toVivillon = step("Spewpa");
  if (toVivillon.name !== "Vivillon" || toVivillon.region !== CAUGHT_REGION) {
    fail(`Spewpa evolves to ${JSON.stringify(toVivillon)}, want Vivillon keeping ${CAUGHT_REGION}`);
  }

  console.log(
    "Scatterbug carry: Scatterbug and Spewpa render a single plain card each (no pattern cards), a caught " +
      `${CAUGHT_REGION} Scatterbug ticks the base card with the plain 664 sprite, and the pattern carries on ` +
      "evolve Scatterbug -> Spewpa -> Vivillon, so a Spewpa becomes the matching Vivillon pattern.",
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
