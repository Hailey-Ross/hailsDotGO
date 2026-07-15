// Asserts the shiny detail view on trainer pages actually works.
//
//   node scripts/check-trainer-shiny-modal.mjs
//
// Why this exists: the admin Costumes tab shipped broken while `go test`, the Go template parse
// check and curl probes against its endpoint were all green, because the bug only fired at render
// time in a browser. Anything that only runs in the browser needs a check that runs it.
//
// So this bundles ts/trainer.ts, runs it against a stub DOM with a realistic API response, clicks a
// shiny, and asserts the detail modal opens with the sprite and a row for every field the trainer
// actually filled in -- and no row for the ones they left blank.

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

// One with everything, one with nothing but a method, one regional shadow, one evolved.
const ITEMS = [
  {
    id: 42,
    pokemon_id: "Pikachu",
    form: "",
    region: "",
    costume: "Party Hat",
    event_tag: "GO Fest 2024",
    method: "raid",
    sprite_url: "https://example.test/sprites/pokemon/shiny/25.png",
    caught_at: "2024-07-06T00:00:00Z",
    evolved_at: null,
  },
  {
    id: 43,
    pokemon_id: "Rattata",
    form: "shadow",
    region: "alolan",
    costume: "",
    event_tag: "",
    method: "",
    sprite_url: "https://example.test/sprites/pokemon/shiny/19.png",
    caught_at: "2023-01-02T00:00:00Z",
    evolved_at: "2024-01-01T00:00:00Z",
  },
];

function stubDom() {
  const made = [];
  const el = (tag) => {
    const classes = new Set();
    const n = {
      tag,
      style: { cssText: "", cursor: "" },
      children: [],
      _text: "",
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
      set innerHTML(_) {
        this.children.length = 0;
      },
      set textContent(v) {
        this._text = String(v);
        this.children.length = 0;
      },
      get textContent() {
        return this._text;
      },
      appendChild(c) {
        this.children.push(c);
        return c;
      },
      remove() {},
      addEventListener(evt, fn) {
        (this._on ??= {})[evt] ??= [];
        this._on[evt].push(fn);
      },
      click() {
        (this._on?.click ?? []).forEach((f) => f({ target: this }));
      },
      querySelectorAll: () => [],
      querySelector: () => null,
      scrollIntoView() {},
    };
    made.push(n);
    return n;
  };

  const container = el("div");
  const body = el("body");

  globalThis.document = {
    getElementById: (id) => (id === "trainer-shiny-container" ? container : null),
    createElement: el,
    querySelectorAll: () => [],
    querySelector: () => null,
    addEventListener() {},
    body,
  };
  return { made, container, body };
}

// Everything the trainer page injects.
globalThis.TRAINER_CTX = new Proxy(
  {
    username: "tester",
    isOwnProfile: true,
    regionalName: "{region} {name}",
    unownName: "{name} {letter}",
    formShadow: "Shadow",
    formPurified: "Purified",
    formAlolan: "Alolan",
    costume: "Costume",
    eventTag: "Event",
    method: "Method",
    caught: "Caught",
    evolved: "Evolved",
    shinyEdit: "Edit in your collection",
    shinyDetails: "View details",
    collectionEmpty: "empty",
    collectionLoading: "loading",
    shinyShowAll: "Show all {n}",
  },
  // Any string the page asks for that we forgot to stub still yields a string, so this harness tests
  // the modal rather than the completeness of my fixture.
  { get: (t, k) => (k in t ? t[k] : typeof k === "string" ? k : undefined) },
);
globalThis.COSTUME_LABELS = null; // fall back to the compiled-in labels

const { made, container, body } = stubDom();
globalThis.fetch = async () => ({ ok: true, json: async () => ITEMS });

const tmp = mkdtempSync(join(tmpdir(), "trainer-modal-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

try {
  const out = join(tmp, "b.js");
  await build({
    entryPoints: ["ts/trainer.ts"],
    bundle: true,
    outfile: out,
    platform: "node",
    format: "esm",
    logLevel: "silent",
  });
  await import("file://" + out.replace(/\\/g, "/"));
  await new Promise((r) => setTimeout(r, 60)); // let the fetch promise settle

  const fail = (msg) => {
    console.error(`FAILED: ${msg}`);
    if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
    console.error("\nThe shiny detail view does not work. Open a trainer page and read the console.");
    process.exit(1);
  };

  if (thrown.length) fail(`the collection threw: ${thrown.map(String).join("; ")}`);

  const grid = container.children.find((c) => c.classList.contains("trainer-shiny-grid"));
  if (!grid) fail("the shiny grid did not render");
  if (grid.children.length !== ITEMS.length) {
    fail(`rendered ${grid.children.length} shinies, want ${ITEMS.length}`);
  }

  // Click the fully-populated one.
  grid.children[0].click();

  const modal = body.children.find((c) => c.classList.contains("sc-modal"));
  if (!modal) fail("clicking a shiny did not create the detail modal");
  if (!modal.classList.contains("open")) fail("the detail modal did not open");

  const inner = modal.children.find((c) => c.classList.contains("sc-modal-inner"));
  if (!inner) fail("the modal has no body");

  const flat = (n) => [n, ...n.children.flatMap(flat)];
  const nodes = flat(inner);
  const img = nodes.find((n) => n.tag === "img");
  if (!img) fail("the modal shows no sprite");

  // Pikachu: costume + event + method + caught. Not evolved, not shadow.
  const text = nodes.map((n) => n.textContent).filter(Boolean).join(" | ");
  for (const want of ["Party Hat", "GO Fest 2024", "Raid", "Pikachu"]) {
    if (!text.includes(want)) fail(`the modal does not show ${JSON.stringify(want)} (got: ${text})`);
  }
  if (text.includes("Evolved")) fail("the modal claims an un-evolved shiny was evolved");
  if (!nodes.some((n) => n.tag === "a" && n.textContent === "Edit in your collection")) {
    fail("your own entry has no Edit link");
  }

  // The second one: the trainer filled in almost nothing, so the modal must not invent rows.
  grid.children[1].click();
  const text2 = flat(modal.children[0]).map((n) => n.textContent).filter(Boolean).join(" | ");
  if (!text2.includes("Shadow")) fail("the modal does not show that the shiny is shadow");
  if (!text2.includes("Alolan")) fail("the modal does not show the regional form");
  if (!text2.includes("Evolved")) fail("the modal does not show that the shiny was evolved");
  if (text2.includes("Costume") || text2.includes("Event")) {
    fail("the modal shows empty Costume / Event rows the trainer never filled in");
  }

  console.log(
    `trainer shiny detail: ${ITEMS.length} shinies render, clicking one opens the modal with its ` +
      `sprite and only the fields the trainer actually set, and your own entry offers an Edit link.`,
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
