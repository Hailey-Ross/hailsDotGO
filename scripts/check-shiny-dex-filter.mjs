// Asserts the shiny checklist hides unreleased species by default, reveals them greyed and inert
// on request, and NEVER hides a species the trainer has already caught.
//
//   node scripts/check-shiny-dex-filter.mjs
//
// The full National Dex now ships in the payload, including species whose shiny is not out in
// Pokemon GO yet and species that are not in the game at all. Three things can break silently,
// none of which a Go test can see because they only happen when the page renders:
//
//   1. unreleased species leaking into the default view, so the Missing tab becomes a to-do list
//      of things nobody can do and the checklist can never be completed.
//   2. an unreleased card being clickable, letting somebody record a shiny that does not exist.
//   3. a species flagged unreleased that the trainer ALREADY OWNS being hidden. Flags can be
//      wrong and an admin can toggle one off; hiding somebody's own catch from their own
//      collection reads as data loss, and is the worst failure of the three.
//
// It also covers the fallback: a payload with no shinyDex at all (an older server, or any of the
// other check-* scripts) must behave exactly as the page did before the flags existed.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { build } from "esbuild";

// ------------------------------------------------------------------- the fixtures
// One species per state, plus an unreleased one the trainer has already caught.
const GAME_DATA = {
  shinies: {
    1: { id: 1, name: "Bulbasaur" },
    791: { id: 791, name: "Solgaleo" },
    // Served as released but absent from shinyDex: a new generation landing upstream before the
    // baseline is regenerated. It must still get a card rather than vanish.
    1030: { id: 1030, name: "Futuremon" },
  },
  shinyDex: {
    1:    { id: 1,    name: "Bulbasaur", in_go: true,  shiny_released: true },
    791:  { id: 791,  name: "Solgaleo",  in_go: true,  shiny_released: true },
    792:  { id: 792,  name: "Lunala",    in_go: true,  shiny_released: false },
    1007: { id: 1007, name: "Koraidon",  in_go: false, shiny_released: false },
    890:  { id: 890,  name: "Eternatus", in_go: true,  shiny_released: false },
  },
};

// The trainer owns an Eternatus, whose shiny our flags say is not released.
const USER_SHINIES = [
  {
    id: 11, pokemon_id: "Eternatus", form: "", region: "", costume: "", event_tag: "",
    method: "raid", caught_at: "2026-07-01T00:00:00Z", evolved_at: null,
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
      checked: false,
      placeholder: "",
      _text: "",
      _html: "",
      _attrs: {},
      classList: {
        add: (...c) => c.forEach((x) => x && classes.add(x)),
        remove: (...c) => c.forEach((x) => classes.delete(x)),
        contains: (c) => classes.has(c),
      },
      get className() { return [...classes].join(" "); },
      set className(v) {
        classes.clear();
        String(v).split(/\s+/).filter(Boolean).forEach((c) => classes.add(c));
      },
      set innerHTML(v) { this._html = String(v); this.children.length = 0; },
      get innerHTML() { return this._html; },
      set textContent(v) { this._text = String(v); this.children.length = 0; },
      get textContent() { return this._text; },
      get childElementCount() { return this.children.length; },
      appendChild(c) { this.children.push(c); return c; },
      setAttribute(k, v) { this._attrs[k] = String(v); },
      getAttribute(k) { return this._attrs[k]; },
      remove() {},
      addEventListener(evt, fn) { (this._on ??= {})[evt] ??= []; this._on[evt].push(fn); },
      // Records whether anything is listening, which is how we tell an inert card from a live one.
      hasListener(evt) { return !!(this._on?.[evt]?.length); },
      click() { (this._on?.click ?? []).forEach((f) => f({ target: this })); },
      fire(evt) { (this._on?.[evt] ?? []).forEach((f) => f({ target: this })); },
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
  // No localStorage on purpose: the page must tolerate private mode, where reading it throws.
  return { getById };
}

const strings = (extra) =>
  new Proxy(extra, { get: (t, k) => (k in t ? t[k] : typeof k === "string" ? k : undefined) });

globalThis.JSC = strings({
  regionalName: "{region} {name}",
  unownName: "{name} {letter}",
  formNormal: "Normal",
  searchNPokemon: "Search {n} Pokemon",
});
globalThis.SH = strings({
  counts: "{unique} / {total}",
  heading: "Shiny checklist",
  showUnreleased: "Show unreleased",
  badgeSoon: "soon",
  badgeNotInGo: "n/a",
  tipShinyUnreleased: "Shiny not released in Pokemon GO yet",
  tipNotInGo: "Not available in Pokemon GO",
});
globalThis.SITE_LANG = "en";
globalThis.COSTUME_LABELS = null;

const { getById } = stubDom();

// The fallback case needs a completely fresh page render, so it runs as a child process rather
// than trying to re-import the bundle: init() runs once per module instance, and re-importing to
// get a second render is exactly the kind of cleverness that makes a test lie.
const FALLBACK_RUN = process.argv.includes("--fallback");

// With --fallback the payload carries only `shinies`, as an older server (or any other check-*
// script) returns. Everything in it must be treated as released.
let gameData = FALLBACK_RUN ? { shinies: GAME_DATA.shinies } : GAME_DATA;
globalThis.fetch = async (url) => {
  const path = String(url);
  if (path.startsWith("/api/shinies")) return { ok: true, status: 200, json: async () => USER_SHINIES };
  if (path === "/api/app/data" || path === "/api/data") {
    return { ok: true, status: 200, json: async () => gameData };
  }
  return { ok: false, status: 404, json: async () => ({}) };
};

// ---------------------------------------------------------------------- the run
const tmp = mkdtempSync(join(tmpdir(), "shiny-dex-filter-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
  console.error("\nThe unreleased filter is broken. Open /shinies and read the console.");
  rmSync(tmp, { recursive: true, force: true });
  process.exit(1);
};

try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  writeFileSync(entry, `import ${JSON.stringify(join(process.cwd(), "ts/shinies.ts"))};\n`);
  await build({
    entryPoints: [entry], bundle: true, outfile: out,
    platform: "node", format: "esm", logLevel: "silent",
  });
  await import("file://" + out.replace(/\\/g, "/"));
  await new Promise((r) => setTimeout(r, 80));

  if (thrown.length) fail(`the checklist threw: ${thrown.map(String).join("; ")}`);

  const content = getById("sc-content");
  const cardsNow = () => {
    const grid = content.children.find((c) => c.classList.contains("shiny-grid"));
    if (!grid) fail(`the shiny grid did not render (content: ${content.innerHTML || "(empty)"})`);
    return grid.children
      .filter((c) => c.classList.contains("shiny-tag"))
      .map((card) => {
        const img = card.children.find((c) => c.tag === "img");
        const label = card.children.find((c) => c.classList.contains("shiny-label"));
        return {
          name: img?.alt ?? "",
          label: label?.textContent ?? "",
          caught: card.classList.contains("sc-caught"),
          locked: card.classList.contains("sc-card-unreleased"),
          clickable: card.hasListener("click"),
          title: card.getAttribute("title") ?? "",
          badge: card.children.find((c) => c.classList.contains("sc-soon-badge"))?.textContent ?? "",
        };
      });
  };

  if (FALLBACK_RUN) {
    // A payload with no shinyDex must render exactly as the page did before the flags existed.
    const names = cardsNow().map((c) => c.name).sort();
    if (names.join(",") !== "Bulbasaur,Futuremon,Solgaleo") {
      fail(`without shinyDex the page shows [${names.join(", ")}], want every species in shinies treated as released.`);
    }
    if (cardsNow().some((c) => c.locked)) {
      fail("without shinyDex a card rendered as unreleased; the fallback must treat everything as released.");
    }
    if (cardsNow().some((c) => !c.clickable)) {
      fail("without shinyDex a card is not clickable; the fallback must leave every species recordable.");
    }
    console.log(
      "shiny dex fallback: a payload carrying only `shinies` (an older server, or any other check script) " +
      "renders every species as released and clickable, so the page degrades to its pre-flags behaviour " +
      "instead of emptying out.",
    );
    rmSync(tmp, { recursive: true, force: true });
    process.exit(0);
  }

  // 1. Default view: released species only, PLUS the caught unreleased one.
  let cards = cardsNow();
  let names = cards.map((c) => c.name).sort();
  if (names.join(",") !== "Bulbasaur,Eternatus,Futuremon,Solgaleo") {
    fail(`the default All tab shows [${names.join(", ")}], want Bulbasaur, Eternatus, Futuremon and Solgaleo only ` +
      "(released species plus the one already caught).");
  }
  if (cards.find((c) => c.name === "Lunala") || cards.find((c) => c.name === "Koraidon")) {
    fail("an unreleased species is visible by default.");
  }

  // 2. The caught unreleased species is NOT greyed and IS clickable. This is the data-loss guard.
  const eternatus = cards.find((c) => c.name === "Eternatus");
  if (eternatus.locked) fail("a species the trainer has already caught renders as unreleased/greyed.");
  if (!eternatus.caught) fail("the caught Eternatus card is not ticked.");
  if (!eternatus.clickable) fail("a caught card is not clickable, so the trainer cannot record another.");

  // 3. The search placeholder counts what is visible, so it matches the grid.
  const search = getById("sc-search");
  if (search.placeholder !== `Search ${cards.length} Pokemon`) {
    fail(`the placeholder says ${JSON.stringify(search.placeholder)} but ${cards.length} cards are shown.`);
  }

  // 4. Ticking the toggle reveals the rest, greyed and inert, with honest badges.
  const toggle = getById("sc-show-unreleased");
  if (!toggle.hasListener("change")) fail("the show-unreleased checkbox has no change handler.");
  toggle.checked = true;
  toggle.fire("change");

  cards = cardsNow();
  names = cards.map((c) => c.name).sort();
  if (names.join(",") !== "Bulbasaur,Eternatus,Futuremon,Koraidon,Lunala,Solgaleo") {
    fail(`with the toggle on the All tab shows [${names.join(", ")}], want all five species.`);
  }
  for (const [name, wantBadge, wantTip] of [
    ["Lunala", "soon", "Shiny not released in Pokemon GO yet"],
    ["Koraidon", "n/a", "Not available in Pokemon GO"],
  ]) {
    const card = cards.find((c) => c.name === name);
    if (!card.locked) fail(`${name} is unreleased but does not render greyed.`);
    if (card.clickable) fail(`${name} is unreleased but is still clickable, so it can be recorded.`);
    if (card.badge !== wantBadge) fail(`${name} shows badge ${JSON.stringify(card.badge)}, want ${JSON.stringify(wantBadge)}.`);
    if (card.title !== wantTip) fail(`${name} shows tooltip ${JSON.stringify(card.title)}, want ${JSON.stringify(wantTip)}.`);
  }
  // Released species must not have been dragged into the locked state.
  if (cards.find((c) => c.name === "Solgaleo").locked) fail("Solgaleo is greyed; its shiny is released.");

  // 5. Unticking hides them again.
  toggle.checked = false;
  toggle.fire("change");
  if (cardsNow().length !== 4) fail("unticking the toggle did not hide the unreleased species again.");

  // 6. The Missing tab excludes unreleased species: it is a to-do list, not a wish list.
  const tabs = getById("sc-tabs");
  const missingBtn = { dataset: { tab: "missing" }, closest: () => missingBtn, classList: { add() {}, remove() {} } };
  tabs.fire ? null : null;
  (tabs._on?.click ?? []).forEach((f) => f({ target: { closest: () => missingBtn } }));
  const missing = cardsNow().map((c) => c.name).sort();
  if (missing.join(",") !== "Bulbasaur,Futuremon,Solgaleo") {
    fail(`the Missing tab shows [${missing.join(", ")}], want the two uncaught RELEASED species only.`);
  }

  console.log(
    "shiny dex filter: the All tab shows released species plus anything already caught (an unreleased " +
    "Eternatus the trainer owns stays visible, ticked and clickable); ticking Show unreleased reveals " +
    "Lunala greyed with a 'soon' badge and Koraidon greyed as not in the game, both inert with no click " +
    "handler; unticking hides them again; the Missing tab lists only uncaught released species; and the " +
    "search placeholder count matches the grid.",
  );

  // 7. The fallback, in a fresh process so init() runs again from scratch.
  const { status, stdout, stderr } = spawnSync(
    process.execPath, [import.meta.filename, "--fallback"], { encoding: "utf8" },
  );
  process.stdout.write(stdout);
  if (status !== 0) {
    process.stderr.write(stderr);
    fail("the no-shinyDex fallback run failed (see above).");
  }
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
