// Asserts the admin Shiny Dex tab actually renders, filters, and saves.
//
//   node scripts/check-admin-shiny-dex-tab.mjs
//
// Same reasoning as check-admin-costumes-tab.mjs: admin.html's JS is split across IIFEs, so a
// helper declared in one is not in scope in another, and a trailing .catch() reports the resulting
// ReferenceError as "could not load", which is a network error message for a code bug. Go tests,
// the template parse test and curl probes are all green while the tab is dead in the browser.
//
// This one additionally guards the things that only matter at this table's size:
//   - the tab must attach a CONSTANT number of listeners, not one per row. At 1025 species,
//     per-row binding means thousands of listeners rebuilt on every keystroke.
//   - rendering must be capped, so a cleared filter does not build the whole dex.
//   - the IIFE must sit BELOW csrfFetch's declaration, which is the scope trap above.

import { readFileSync } from "node:fs";

const TEMPLATE = "templates/admin.html";
const MARKER = "// ── Shiny Dex: species and regional form availability";
const RENDER_CAP = 300;

const html = readFileSync(TEMPLATE, "utf8");

const fail = (msg, extra) => {
  console.error(`FAILED: ${msg}`);
  if (extra) console.error(extra);
  console.error("\nThe admin Shiny Dex tab is broken. Open /admin, click Shiny Dex, read the console.");
  process.exit(1);
};

// ------------------------------------------------- the helpers it is allowed to depend on
// These must be declared at the TOP LEVEL of admin.html's script block (column 0), because the tab
// runs inside its own IIFE and can only see top-level names.
for (const decl of ["function csrfFetch(", "function esc(", "const STRINGS = {"]) {
  if (!html.includes("\n" + decl)) {
    fail(`${JSON.stringify(decl)} is no longer declared at the top level of ${TEMPLATE}.`,
      "  The Shiny Dex IIFE depends on it being in the outer scope.");
  }
}

const start = html.indexOf(MARKER);
if (start < 0) fail(`could not find ${JSON.stringify(MARKER)} in ${TEMPLATE}.`, "  Update MARKER in this script.");

// The placement rule: a function DECLARATION is hoisted within its own scope, but the tab's IIFE
// runs at parse time, and csrfFetch lives in the outer scope. Being above it is the documented trap.
const csrfAt = html.indexOf("\nfunction csrfFetch(");
if (start < csrfAt) {
  fail("the Shiny Dex IIFE sits ABOVE csrfFetch in admin.html.",
    `  IIFE at char ${start}, csrfFetch at char ${csrfAt}. Move the block below it.`);
}

const end = html.indexOf("\n})();", start) + "\n})();".length;
const src = html.slice(start, end);

// ------------------------------------------------------------------ the payload
// Mirrors handlers.AdminGetShinyDex. Covers every state the row renderer branches on.
const PAYLOAD = {
  species: [
    { dex: 1,   name: "Bulbasaur", gen: 1, in_go: true,  shiny_released: true,  default_in_go: true,  default_shiny_released: true,  upstream_shiny: true,  overridden: false },
    { dex: 791, name: "Solgaleo",  gen: 7, in_go: true,  shiny_released: true,  default_in_go: true,  default_shiny_released: true,  upstream_shiny: false, overridden: false },
    { dex: 792, name: "Lunala",    gen: 7, in_go: true,  shiny_released: false, default_in_go: true,  default_shiny_released: false, upstream_shiny: false, overridden: false },
    // Overridden: carries provenance and must render a Reset button.
    { dex: 890, name: "Eternatus", gen: 8, in_go: true,  shiny_released: true,  default_in_go: true,  default_shiny_released: false, upstream_shiny: false, overridden: true, note: "GO Fest", updated_by: "hails", updated_at: "2026-07-25" },
    // Upstream disagrees with us: must render the warning pill and count as a suggestion.
    { dex: 999, name: "Gimmighoul", gen: 9, in_go: true, shiny_released: false, default_in_go: true,  default_shiny_released: false, upstream_shiny: true,  overridden: false },
    { dex: 1007, name: "Koraidon", gen: 9, in_go: false, shiny_released: false, default_in_go: false, default_shiny_released: false, upstream_shiny: false, overridden: false },
  ],
  regional: [
    { dex: 570, name: "Zorua", region: "hisuian", sprite_url: "https://example/10238.png", shiny_released: false, default_shiny_released: false, overridden: false },
    { dex: 479, name: "Rotom", region: "wash",    sprite_url: "https://example/10009.png", shiny_released: true,  default_shiny_released: true,  overridden: false },
    { dex: 201, name: "Unown", region: "unown_b", sprite_url: "https://example/201-b.png", shiny_released: true,  default_shiny_released: true,  overridden: true, note: "", updated_by: "hails", updated_at: "2026-07-25" },
  ],
};

// ------------------------------------------------------------------ the stub DOM
let listenerCount = 0;

function makeEl(id) {
  const node = {
    id,
    value: "",
    dataset: {},
    style: {},
    _html: "",
    _listeners: {},
    set innerHTML(v) { this._html = String(v); },
    get innerHTML() { return this._html; },
    set textContent(v) { this._text = String(v); },
    get textContent() { return this._text ?? ""; },
    addEventListener(evt, fn) {
      listenerCount++;
      (this._listeners[evt] ??= []).push(fn);
    },
    fire(evt, e) { (this._listeners[evt] ?? []).forEach((f) => f(e)); },
    querySelector() { return makeEl("status"); },
    querySelectorAll() { return []; },
    closest() { return null; },
  };
  return node;
}

const byId = new Map();
const getById = (id) => {
  if (!byId.has(id)) byId.set(id, makeEl(id));
  return byId.get(id);
};

const domReady = [];
globalThis.document = {
  getElementById: getById,
  createElement: () => makeEl("created"),
  querySelector: (sel) => (sel.includes("shiny-dex") ? getById("tab-btn") : { content: "csrf-token" }),
  querySelectorAll: () => [],
  addEventListener: (evt, fn) => { if (evt === "DOMContentLoaded") domReady.push(fn); },
};
globalThis.confirm = () => true;
globalThis.window = globalThis;
globalThis.esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
globalThis.STRINGS = { error: "Something went wrong" };

const sent = [];
globalThis.csrfFetch = async (url, opts = {}) => {
  sent.push({ url, method: opts.method ?? "GET", body: opts.body ? JSON.parse(opts.body) : null });
  return { ok: true, status: 200, json: async () => ({ ok: true, overridden: true }) };
};
globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => PAYLOAD });

const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));

// ---------------------------------------------------------------------- the run
try {
  eval(src);
} catch (e) {
  thrown.push(e);
}
if (thrown.length) fail(`the tab threw while loading: ${thrown.map(String).join("; ")}`);

// The tab only loads when its nav button is clicked, exactly like Trainer Avatars.
domReady.forEach((fn) => fn());
const listenersAfterBind = listenerCount;

const api = globalThis.__shinyDexTab;
if (!api) fail("the IIFE did not expose window.__shinyDexTab, so this script cannot drive it.");

await api.load();
await new Promise((r) => setTimeout(r, 20));
if (thrown.length) fail(`the tab threw while rendering: ${thrown.map(String).join("; ")}`);

const speciesHost = getById("sd-species-table");
const regionalHost = getById("sd-regional-table");

// 1. "Needs attention" is the tab's default view, and it must narrow to the rows worth acting on
//    rather than the whole dex.
if (!html.includes('<option value="attention">Needs attention</option>')) {
  fail("the status filter no longer offers 'Needs attention'.");
}
if (html.indexOf('<option value="attention">') > html.indexOf('<option value="">All species</option>')) {
  fail("'Needs attention' is not the first status option, so it is not the default view.");
}
getById("sd-status-filter").value = "attention";
api.renderSpecies();
const attention = api.filtered();
if (attention.length !== 2) {
  fail(`"needs attention" matched ${attention.length} species, want 2 (the overridden one and the upstream disagreement).`,
    `  matched: ${attention.map((s) => s.name).join(", ")}`);
}

// 2. Every species renders a row with both checkboxes.
getById("sd-status-filter").value = "";
api.renderSpecies();
const rowCount = (speciesHost.innerHTML.match(/class="sd-row/g) ?? []).length;
if (rowCount !== PAYLOAD.species.length) {
  fail(`rendered ${rowCount} species rows, want ${PAYLOAD.species.length}.`);
}
for (const s of PAYLOAD.species) {
  if (!speciesHost.innerHTML.includes(`>${s.name}<`)) fail(`${s.name} is missing from the species table.`);
}
const boxes = (speciesHost.innerHTML.match(/type="checkbox"/g) ?? []).length;
if (boxes !== PAYLOAD.species.length * 2) {
  fail(`rendered ${boxes} checkboxes, want ${PAYLOAD.species.length * 2} (in_go and shiny_released per species).`);
}

// 3. Provenance and affordances on the overridden row.
if (!speciesHost.innerHTML.includes("overridden")) fail("the overridden species has no 'overridden' pill.");
if (!speciesHost.innerHTML.includes("hails on 2026-07-25: GO Fest")) {
  fail("the overridden pill does not carry who changed it, when, and why.");
}
if (!speciesHost.innerHTML.includes("sd-reset")) fail("an overridden row offers no Reset button.");
if (!speciesHost.innerHTML.includes("upstream says shiny")) {
  fail("a species upstream lists as shiny and we do not is not flagged.");
}

// 4. Filters.
const cases = [
  ["gen", "sd-gen-filter", "9", 2],
  ["status released", "sd-status-filter", "released", 3],
  ["status pending", "sd-status-filter", "pending", 2],
  ["status notingo", "sd-status-filter", "notingo", 1],
  ["status overridden", "sd-status-filter", "overridden", 1],
];
for (const [label, id, value, want] of cases) {
  getById("sd-gen-filter").value = "";
  getById("sd-status-filter").value = "";
  getById("sd-search").value = "";
  getById(id).value = value;
  const got = api.filtered().length;
  if (got !== want) fail(`filter ${label}=${value} matched ${got} species, want ${want}.`);
}
getById("sd-gen-filter").value = "";
getById("sd-status-filter").value = "";
getById("sd-search").value = "solgaleo";
if (api.filtered().length !== 1) fail("searching 'solgaleo' did not match exactly one species.");
getById("sd-search").value = "791";
if (api.filtered().length !== 1) fail("searching by dex number '791' did not match exactly one species.");
getById("sd-search").value = "";

// 5. Regional forms render, and Scatterbug/Spewpa are not in the table (they follow Vivillon).
api.renderRegional();
const regionalRows = (regionalHost.innerHTML.match(/class="sd-row/g) ?? []).length;
if (regionalRows !== PAYLOAD.regional.length) {
  fail(`rendered ${regionalRows} regional rows, want ${PAYLOAD.regional.length}.`);
}
if (/Scatterbug|Spewpa/.test(regionalHost.innerHTML)) {
  fail("Scatterbug or Spewpa appears in the regional table; they inherit Vivillon's flags and must not be editable.");
}
if ((regionalHost.innerHTML.match(/type="checkbox"/g) ?? []).length !== PAYLOAD.regional.length) {
  fail("a regional form must expose exactly one checkbox (shiny only; in_go is the species' business).");
}

// 6. Listeners are DELEGATED: re-rendering must not attach more. This is the one that stops the
//    tab quietly becoming unusable at 1025 rows.
const before = listenerCount;
api.renderSpecies();
api.renderSpecies();
api.renderRegional();
if (listenerCount !== before) {
  fail(`re-rendering attached ${listenerCount - before} new listeners; they must be delegated to the container.`,
    `  ${listenersAfterBind} were bound once at startup, which is correct.`);
}

// 7. Saving sends the right body and does not send in_go for a form.
const speciesRow = {
  dataset: { dex: "792", region: "" },
  querySelector: () => makeEl("status"),
  querySelectorAll: () => [
    { dataset: { field: "in_go" }, checked: true, type: "checkbox" },
    { dataset: { field: "shiny_released" }, checked: true, type: "checkbox" },
  ],
};
sent.length = 0;
await api.save(speciesRow);
if (sent.length !== 1) fail(`saving a species sent ${sent.length} requests, want 1.`);
if (sent[0].method !== "PUT" || !sent[0].url.endsWith("/api/admin/shiny-dex/792")) {
  fail(`species save hit ${sent[0].method} ${sent[0].url}, want PUT /api/admin/shiny-dex/792.`);
}
if (sent[0].body.shiny_released !== true || sent[0].body.in_go !== true || sent[0].body.region !== "") {
  fail(`species save body is wrong: ${JSON.stringify(sent[0].body)}`);
}

const formRow = {
  dataset: { dex: "570", region: "hisuian" },
  querySelector: () => makeEl("status"),
  querySelectorAll: () => [{ dataset: { field: "shiny_released" }, checked: true, type: "checkbox" }],
};
sent.length = 0;
await api.save(formRow);
if (sent[0].body.region !== "hisuian") fail("form save did not carry its region.");
if ("in_go" in sent[0].body) fail("form save sent in_go, which the API rejects: a form has no in_go of its own.");

// 8. The render cap holds, so clearing the filters cannot build the entire National Dex.
const many = [];
for (let i = 1; i <= 1025; i++) {
  many.push({ dex: i, name: `Mon${i}`, gen: 1, in_go: true, shiny_released: true,
    default_in_go: true, default_shiny_released: true, upstream_shiny: false, overridden: false });
}
globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => ({ species: many, regional: [] }) });
await api.load();
getById("sd-status-filter").value = "";
api.renderSpecies();
const capped = (speciesHost.innerHTML.match(/class="sd-row/g) ?? []).length;
if (capped > RENDER_CAP) fail(`rendered ${capped} rows with no filter; the cap of ${RENDER_CAP} is not holding.`);
if (!speciesHost.innerHTML.includes("of 1025")) {
  fail("the tab truncated silently: it must say how many rows it is hiding.");
}

console.log(
  `admin Shiny Dex: ${PAYLOAD.species.length} species and ${PAYLOAD.regional.length} forms render with their ` +
  "flags, provenance and Reset buttons; search, generation and status filters all narrow correctly; " +
  "Scatterbug and Spewpa stay out of the form table; saving sends the right body and omits in_go for a " +
  `form; listeners are delegated so re-rendering attaches none; and 1025 species render capped at ${RENDER_CAP} ` +
  "with the remainder declared rather than silently dropped.",
);
