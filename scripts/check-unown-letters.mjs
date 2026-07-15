// Asserts the 28 Unown letter cards actually render on the shiny checklist.
//
//   node scripts/check-unown-letters.mjs
//
// Why this exists: the Unown letters are the one place where a checklist card's sprite id is a
// STRING slug rather than a number, because PokeAPI files each letter as a pokemon-form with no id
// of its own. That makes three silent failures possible, none of which a Go test or a curl probe
// can see, because they only happen when the page renders in a browser:
//
//   1. letter A is the DEFAULT form, so its art is the plain 201.png; 201-a.png does not exist
//      upstream, and asking for it yields a broken image, not an error.
//   2. ! and ? are filed as 201-exclamation and 201-question, not 201-! / 201-?.
//   3. an Unown card reads "Unown F", not "F Unown": the generic regionalName template
//      ("{region} {name}") would put the letter first, so the letters need their own template key.
//
// So this bundles ts/shinies.ts, runs it against a stub DOM with a stub /api/data and a stub
// /api/shinies, and asserts on the grid the real page code built: one card per letter plus the
// letterless base card, the exact shiny sprite URL of every letter, the label word order, and that
// a caught Unown D ticks the D card and nothing else. It never touches the network: the sprite URLs
// are asserted as strings.
//
// It also checks every unown_* region tag fits user_shinies.region (VARCHAR(16)), because MySQL
// truncates an over-long value instead of rejecting it, which would file a catch under the wrong
// letter.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const REGION_COLUMN_MAX = 16; // user_shinies.region is VARCHAR(16)

const shinyUrl = (id) =>
  `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;

// The spec, written out here independently of ts/shared/regionalForms.ts, so this check cannot
// agree with the code by simply reading the code's own answer back.
const LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("");
const WANT_SPRITE = new Map([
  // Letter A is the default form: plain 201, NOT 201-a.
  ...LETTERS.map((c, i) => [c, shinyUrl(i === 0 ? 201 : `201-${c.toLowerCase()}`)]),
  ["!", shinyUrl("201-exclamation")],
  ["?", shinyUrl("201-question")],
]);
const WANT_REGION = new Map([
  ...LETTERS.map((c) => [c, `unown_${c.toLowerCase()}`]),
  ["!", "unown_excl"],
  ["?", "unown_qmark"],
]);

// ------------------------------------------------------------------- the fixtures
// Enough of /api/data to drive the grid: Unown, plus a species with an ordinary regional form so a
// mistake in the letter path cannot hide behind an empty grid.
const GAME_DATA = {
  shinies: {
    Rattata: { id: 19, name: "Rattata" },
    Pikachu: { id: 25, name: "Pikachu" },
    Unown: { id: 201, name: "Unown" },
  },
};

// One caught Unown, letter D only. Nothing else in the collection.
const CAUGHT_LETTER = "D";
const USER_SHINIES = [
  {
    id: 7,
    pokemon_id: "Unown",
    form: "",
    region: WANT_REGION.get(CAUGHT_LETTER),
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
// the Unown cards rather than the completeness of my locale fixture.
const strings = (extra) =>
  new Proxy(extra, { get: (t, k) => (k in t ? t[k] : typeof k === "string" ? k : undefined) });

globalThis.JSC = strings({
  regionalName: "{region} {name}", // the wrong order for a letter: "A Unown"
  unownName: "{name} {letter}", // the right one: "Unown A"
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
const tmp = mkdtempSync(join(tmpdir(), "unown-letters-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
  console.error("\nThe Unown letters do not render. Open /shinies, search Unown, and read the console.");
  rmSync(tmp, { recursive: true, force: true });
  process.exit(1);
};

try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  // Import the page for its side effects (it renders on import), and re-export the letter table so
  // the region-tag length check runs against the same constant the page ships.
  writeFileSync(
    entry,
    `import ${JSON.stringify(join(process.cwd(), "ts/shinies.ts"))};\n` +
      `export { UNOWN_LETTERS } from ${JSON.stringify(join(process.cwd(), "ts/shared/regionalForms.ts"))};\n`,
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
  const unown = cards.map(cardInfo).filter((c) => c.alt === "Unown");

  // 1. Exactly 29 cards: the letterless base card plus all 28 letters, no duplicates.
  const WANT_CARDS = 1 + WANT_SPRITE.size;
  if (unown.length !== WANT_CARDS) {
    fail(`Unown rendered ${unown.length} cards, want ${WANT_CARDS} (base + ${WANT_SPRITE.size} letters)`);
  }
  const labels = unown.map((c) => c.label);
  const dupes = labels.filter((l, i) => labels.indexOf(l) !== i);
  if (dupes.length) fail(`duplicate Unown cards: ${[...new Set(dupes)].join(", ")}`);

  const base = unown.find((c) => c.label === "Unown");
  if (!base) fail("the letterless base Unown card is missing");
  if (base.src !== shinyUrl(201)) fail(`the base Unown card points at ${base.src}, want ${shinyUrl(201)}`);

  // 2. Every letter card exists, and its shiny sprite URL is exactly right. The traps: A must be
  //    plain 201 (201-a does not exist upstream), and ! / ? must be spelled out.
  // 3. The label reads "Unown A" (name first), never "A Unown".
  const byLetter = new Map();
  for (const [letter] of WANT_SPRITE) {
    const want = `Unown ${letter}`;
    const card = unown.find((c) => c.label === want);
    if (!card) fail(`no card labelled ${JSON.stringify(want)} (labels: ${labels.join(", ")})`);
    byLetter.set(letter, card);
    const wantSrc = WANT_SPRITE.get(letter);
    if (card.src !== wantSrc) {
      fail(`Unown ${letter} points at ${JSON.stringify(card.src)}, want ${JSON.stringify(wantSrc)}`);
    }
  }
  const backwards = labels.filter((l) => /^\S+ Unown$/.test(l));
  if (backwards.length) {
    fail(`these cards read letter first, want name first: ${backwards.join(", ")}`);
  }

  // 4. Ticking is per letter: a caught unown_d ticks the D card and nothing else.
  const ticked = unown.filter((c) => c.caught).map((c) => c.label);
  if (ticked.length !== 1 || ticked[0] !== `Unown ${CAUGHT_LETTER}`) {
    fail(
      `a caught ${WANT_REGION.get(CAUGHT_LETTER)} ticked ${ticked.length} card(s) ` +
        `(${ticked.join(", ") || "none"}), want only "Unown ${CAUGHT_LETTER}"`,
    );
  }

  // 5. Every region tag fits the column. MySQL truncates rather than errors, so an over-long tag
  //    would file the catch under a different letter, silently.
  const letterTable = mod.UNOWN_LETTERS;
  if (letterTable.length !== WANT_SPRITE.size) {
    fail(`UNOWN_LETTERS has ${letterTable.length} entries, want ${WANT_SPRITE.size}`);
  }
  for (const u of letterTable) {
    const wantRegion = WANT_REGION.get(u.letter);
    if (u.region !== wantRegion) {
      fail(`letter ${u.letter} has region tag ${JSON.stringify(u.region)}, want ${JSON.stringify(wantRegion)}`);
    }
    if (u.region.length > REGION_COLUMN_MAX) {
      fail(
        `region tag ${JSON.stringify(u.region)} is ${u.region.length} chars, over the ` +
          `${REGION_COLUMN_MAX}-char user_shinies.region column: MySQL would truncate it`,
      );
    }
  }

  console.log(
    `Unown letters: the checklist renders ${unown.length} Unown cards (base + ${WANT_SPRITE.size} letters), ` +
      `each with the right shiny sprite (A is 201, ! is 201-exclamation, ? is 201-question), each labelled ` +
      `name first ("Unown ${CAUGHT_LETTER}"), a caught unown_d ticks only the D card, and every region tag ` +
      `fits the ${REGION_COLUMN_MAX}-char column.`,
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
