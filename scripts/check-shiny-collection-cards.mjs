// Asserts the Caught/Evolved tabs of the shiny collection render as cards and edit through a modal.
//
//   node scripts/check-shiny-collection-cards.mjs
//
// Why this exists: the editor for a recorded shiny used to be a wide inline row, one per entry, each
// owning its own controls and its own save state. It is now ONE reused modal driven by whichever
// card you clicked, which quietly turns two former non-problems into real ones:
//
//   1. Editing any field used to re-send the catch date, which walked the date a day earlier on
//      every edit for anyone west of UTC. The fix only sends the date when it actually changed, and
//      that fix lives in the code this refactor rewrote. If it regresses, catches drift again.
//   2. A save that is still in flight when you close the modal and open another entry would land on
//      the wrong entry, because the status line and the record are no longer per-row.
//
// Both only fire in a browser, so they need a check that runs one.

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

// Two Pikachu (a duplicate pair, one costumed) and a regional Growlithe. Dates are deliberately
// stored as UTC midnight, which is how the server writes them.
const SHINIES = [
  { id: 42, pokemon_id: "Pikachu",   form: "",       region: "",        costume: "Party Hat", event_tag: "GO Fest 2024", method: "raid", caught_at: "2024-07-06T00:00:00Z", evolved_at: null },
  { id: 43, pokemon_id: "Pikachu",   form: "",       region: "",        costume: "",          event_tag: "",             method: "wild", caught_at: "2023-03-04T00:00:00Z", evolved_at: null },
  { id: 44, pokemon_id: "Growlithe", form: "shadow", region: "hisuian", costume: "",          event_tag: "",             method: "",     caught_at: "2022-11-12T00:00:00Z", evolved_at: "2024-01-01T00:00:00Z" },
  // Form, costume AND event: three tags, which is one more than a card will show.
  { id: 45, pokemon_id: "Pikachu",   form: "shadow", region: "",        costume: "Witch Hat", event_tag: "Halloween",    method: "wild", caught_at: "2021-10-31T00:00:00Z", evolved_at: null },
];

const MAX_PILLS = 2;

const GAME_DATA = {
  shinies: {
    Pikachu:   { id: 25, name: "Pikachu" },
    Growlithe: { id: 58, name: "Growlithe" },
    Arcanine:  { id: 59, name: "Arcanine" },
  },
  pokemon: [], fastMoves: [], chargedMoves: [], shadowPokemon: [],
  typeChart: null, cpMultipliers: [], pokemonTypes: {}, pokemonNames: {},
};

const puts = [];

function stubDom() {
  const byId = new Map();

  const el = (tag) => {
    const classes = new Set();
    const attrs = {};
    const n = {
      tag,
      style: { cssText: "", display: "", cursor: "" },
      dataset: {},
      children: [],
      parent: null,
      _text: "",
      value: "",
      max: "",
      disabled: false,
      selected: false,
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
      // shinies.ts builds both modals by assigning an HTML string and then reaching into them with
      // querySelector/getElementById, so the harness has to materialise the tags it declares.
      // Flat, attribute-only parsing is enough: nothing here depends on nesting.
      set innerHTML(html) {
        this.children.length = 0;
        for (const m of String(html).matchAll(/<(\w+)([^>]*)>/g)) {
          const [, tag, rest] = m;
          if (tag === "br" || tag === "hr") continue;
          const child = el(tag);
          const cls = /class="([^"]*)"/.exec(rest);
          const id = /id="([^"]*)"/.exec(rest);
          if (cls) child.className = cls[1];
          if (id) { child.id = id[1]; byId.set(id[1], child); }
          this.appendChild(child);
        }
      },
      get innerHTML() { return ""; },
      set textContent(v) { this._text = String(v); this.children.length = 0; },
      get textContent() { return this._text; },
      setAttribute(k, v) { attrs[k] = String(v); },
      getAttribute(k) { return attrs[k] ?? null; },
      appendChild(c) { c.parent = n; this.children.push(c); return c; },
      insertBefore(c) { c.parent = n; this.children.unshift(c); return c; },
      append(...cs) { cs.forEach((c) => this.appendChild(c)); },
      remove() {
        if (!this.parent) return;
        const i = this.parent.children.indexOf(this);
        if (i >= 0) this.parent.children.splice(i, 1);
      },
      addEventListener(evt, fn) { (this._on ??= {})[evt] ??= []; this._on[evt].push(fn); },
      fire(evt, ev) { (this._on?.[evt] ?? []).forEach((f) => f(ev ?? { target: this })); },
      click() { this.fire("click"); },
      focus() {},
      scrollIntoView() {},
      querySelector(sel) { return descendants(this).find((d) => matches(d, sel)) ?? null; },
      querySelectorAll(sel) { return descendants(this).filter((d) => matches(d, sel)); },
    };
    return n;
  };

  const descendants = (n) => n.children.flatMap((c) => [c, ...descendants(c)]);
  const matches = (n, sel) => {
    const m = /^\.([\w-]+)$/.exec(sel);
    if (m) return n.classList.contains(m[1]);
    const d = /^\.([\w-]+)\[data-tab="([\w-]+)"\]$/.exec(sel);
    if (d) return n.classList.contains(d[1]) && n.dataset.tab === d[2];
    return false;
  };

  const body = el("body");
  // shinies.ts writes an innerHTML template then looks its parts up by id, so the harness has to
  // hand back a live node for any id the page asks for.
  const get = (id) => {
    if (!byId.has(id)) byId.set(id, el("div"));
    return byId.get(id);
  };

  globalThis.document = {
    getElementById: get,
    createElement: el,
    querySelector: (sel) =>
      sel === 'meta[name="csrf-token"]' ? { content: "test-token" } : body.querySelector(sel),
    querySelectorAll: (sel) => body.querySelectorAll(sel),
    addEventListener() {},
    body,
  };
  globalThis.location = { search: "" };
  return { body, get, el };
}

const strings = new Proxy({}, { get: (_, k) => (typeof k === "string" ? k : undefined) });
globalThis.JSC = strings;
globalThis.SH = strings;
globalThis.SITE_LANG = "en-GB";
globalThis.COSTUME_LABELS = null;

const { body, get } = stubDom();

globalThis.fetch = async (url, opts) => {
  if (url === "/api/app/data") return { ok: true, status: 200, json: async () => GAME_DATA };
  if (url === "/api/shinies") return { ok: true, status: 200, json: async () => SHINIES };
  if (url === "/api/events") return { ok: true, status: 200, json: async () => [] };
  if (String(url).startsWith("/api/shinies/") && opts?.method === "PUT") {
    puts.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ ok: true }) };
  }
  return { ok: true, status: 200, json: async () => ({}) };
};

const tmp = mkdtempSync(join(tmpdir(), "shiny-cards-"));
const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

const settle = () => new Promise((r) => setTimeout(r, 40));

try {
  const out = join(tmp, "b.js");
  await build({
    entryPoints: ["ts/shinies.ts"],
    bundle: true,
    outfile: out,
    platform: "node",
    format: "esm",
    logLevel: "silent",
  });
  await import("file://" + out.replace(/\\/g, "/"));
  await settle();

  const fail = (msg) => {
    console.error(`FAILED: ${msg}`);
    if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
    console.error("\nThe shiny collection editor does not work. Open /shinies and read the console.");
    process.exit(1);
  };
  if (thrown.length) fail(`the collection threw: ${thrown.map(String).join("; ")}`);

  // Switch to the Caught tab the way the tab bar does.
  const tabs = get("sc-tabs");
  const caughtBtn = { classList: { contains: (c) => c === "tab-btn", add() {}, remove() {} }, dataset: { tab: "caught" } };
  tabs.fire("click", { target: { closest: (sel) => (sel === ".tab-btn" ? caughtBtn : null) } });
  await settle();

  const content = get("sc-content");
  const grid = content.children.find((c) => c.classList.contains("shiny-grid"));
  if (!grid) fail("the Caught tab did not render a .shiny-grid, so it is still not uniform with All Shinies");
  if (grid.children.length !== SHINIES.length) {
    fail(`rendered ${grid.children.length} entry cards, want ${SHINIES.length} (duplicates must each get a card)`);
  }
  for (const card of grid.children) {
    if (!card.classList.contains("shiny-tag")) fail("an entry card does not use the shared .shiny-tag class");
  }
  if (grid.children.filter((c) => c.classList.contains("sc-row-evolved")).length !== 1) {
    fail("exactly one fixture entry is evolved, but that is not what the grid marked");
  }

  const flat = (n) => [n, ...n.children.flatMap(flat)];

  // A card has to look composed whether it is data-rich or bare, so the tag row is capped and
  // is absent entirely rather than empty. Entry 45 has form + costume + event, which is three.
  const pillsOf = (card) =>
    flat(card).filter((n) => n.classList.contains("sc-pill"));
  const tagRowOf = (card) => flat(card).find((n) => n.classList.contains("sc-card-tags"));

  const rich = grid.children[3];
  const richPills = pillsOf(rich);
  if (richPills.length !== MAX_PILLS + 1) {
    fail(`an entry with 3 tags rendered ${richPills.length} pills, want ${MAX_PILLS} plus a +N counter`);
  }
  const more = richPills[richPills.length - 1];
  if (more.textContent !== "+1") fail(`the overflow counter reads ${JSON.stringify(more.textContent)}, want "+1"`);
  if (!more.title) fail("the +N counter does not name the tags it is hiding, so they are unreachable");
  for (const p of richPills.slice(0, MAX_PILLS)) {
    if (!p.title) fail("a pill ellipsises but carries no title, so a long value cannot be read in full");
  }

  const bare = grid.children[1]; // no form, no costume, no event
  if (pillsOf(bare).length !== 0) fail("an entry with no tags rendered pills");
  const bareRow = tagRowOf(bare);
  if (bareRow && bareRow.style.display !== "none") {
    fail("an entry with no tags still reserves its tag row, which leaves the card looking unfinished");
  }

  // Every entry has a date, so that line is what keeps a bare card from reading as truncated.
  const metaOf = (card) => flat(card).find((n) => n.classList.contains("sc-card-meta"));
  if (!metaOf(bare) || !metaOf(bare).textContent) fail("the bare entry shows no caught date");
  // The method moved to a corner badge, and an icon alone is a guess without its label.
  const badgeOf = (card) => flat(card).find((n) => n.classList.contains("sc-method-badge"));
  if (!badgeOf(bare) || !badgeOf(bare).textContent) fail("the bare entry shows no method badge");
  if (!badgeOf(bare).title) fail("the method badge has no tooltip, so the icon is the only clue to its meaning");
  // Entry 44 has no method at all, so its badge must be hidden rather than blank.
  const noMethod = badgeOf(grid.children[2]);
  if (noMethod && noMethod.style.display !== "none") fail("an entry with no method still shows a method badge");
  // The sprite sits on the pedestal, so the stage wrapper has to exist.
  if (!flat(grid.children[0]).some((n) => n.classList.contains("sc-card-stage"))) {
    fail("the sprite is not inside .sc-card-stage, so it has no pedestal");
  }

  // Clicking a card opens the compact editor.
  grid.children[0].click();
  await settle();
  const editModal = body.children.find((c) => c.classList.contains("sc-edit-modal"));
  if (!editModal) fail("clicking an entry card did not create the edit modal");
  if (!editModal.classList.contains("open")) fail("clicking an entry card did not open the edit modal");

  const fields = get("sc-edit-fields");
  const controls = flat(fields).filter((n) => n.tag === "select" || n.tag === "input");
  const dateInput = controls.find((n) => n.type === "date");
  const methodSel = controls.filter((n) => n.tag === "select").pop();
  if (!dateInput) fail("the edit modal has no caught-date input");
  if (!methodSel) fail("the edit modal has no method select");

  // Entry 42 was caught 2024-07-06 UTC. Read back in UTC, it must still say the 6th.
  if (dateInput.value !== "2024-07-06") {
    fail(`the date input shows ${JSON.stringify(dateInput.value)}, want "2024-07-06" (UTC read-back is broken)`);
  }

  // THE REGRESSION TEST: change only the method. The catch date must NOT be re-sent.
  puts.length = 0;
  methodSel.value = "egg";
  methodSel.fire("change");
  await settle();
  if (puts.length !== 1) fail(`changing the method sent ${puts.length} updates, want 1`);
  if (puts[0].body.caught_at !== "") {
    fail(
      `editing the method re-sent caught_at as ${JSON.stringify(puts[0].body.caught_at)}. It must be "" ` +
        `or the catch date walks a day earlier on every unrelated edit.`,
    );
  }
  if (puts[0].body.method !== "egg") fail("the method edit did not actually send the new method");

  // Changing the date itself still persists it.
  puts.length = 0;
  dateInput.value = "2024-07-04";
  dateInput.fire("change");
  await settle();
  if (puts[0]?.body.caught_at !== "2024-07-04") {
    fail(`editing the date sent caught_at ${JSON.stringify(puts[0]?.body.caught_at)}, want "2024-07-04"`);
  }

  // A second entry must show its OWN date, not the one left behind by the first.
  grid.children[1].click();
  await settle();
  const date2 = flat(get("sc-edit-fields")).find((n) => n.type === "date");
  if (date2.value !== "2023-03-04") {
    fail(`opening a second entry shows date ${JSON.stringify(date2.value)}, want "2023-03-04" (state leaked between entries)`);
  }

  // One modal serves every entry, so anything it displays has to belong to the entry currently
  // open. Save here, then reopen on another entry: the "Saved" confirmation must not still be
  // sitting there, which is what happens the moment the status line is hoisted somewhere that
  // outlives a single open.
  const method2 = flat(get("sc-edit-fields")).filter((n) => n.tag === "select").pop();
  method2.value = "trade";
  method2.fire("change");
  await settle();
  const statusAfterSave = flat(get("sc-edit-actions")).find((n) => n.classList.contains("sc-save-status"));
  if (!statusAfterSave || !statusAfterSave.textContent) {
    fail("saving from the edit modal showed no confirmation, so the status assertion below proves nothing");
  }
  grid.children[2].click();
  await settle();
  const statusOnReopen = flat(get("sc-edit-actions")).find((n) => n.classList.contains("sc-save-status"));
  if (statusOnReopen && statusOnReopen.textContent) {
    fail(
      `opening another entry still shows ${JSON.stringify(statusOnReopen.textContent)} from the previous ` +
        `entry's save. The status line must be rebuilt per open, not shared across the modal.`,
    );
  }
  const date3 = flat(get("sc-edit-fields")).find((n) => n.type === "date");
  if (date3.value !== "2022-11-12") {
    fail(`the third entry shows date ${JSON.stringify(date3.value)}, want "2022-11-12"`);
  }

  console.log(
    `shiny collection: the Caught tab renders ${SHINIES.length} entry cards in the shared .shiny-grid ` +
      `(duplicates included), each on a pedestal with a labelled method badge and a caught date; a ` +
      `heavily tagged catch caps at ${MAX_PILLS} pills plus a +N that names the rest, an untagged one ` +
      `drops its tag row entirely rather than leaving a gap; clicking a card opens the compact editor ` +
      `with its own caught date, editing the method sends caught_at:"" so the date cannot drift, ` +
      `editing the date persists it, and the editor carries no state over from the entry before.`,
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
