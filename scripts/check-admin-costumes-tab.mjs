// Asserts the admin Costumes tab actually renders.
//
//   node scripts/check-admin-costumes-tab.mjs
//
// Why this exists: the tab shipped broken and every other check passed. `go test`, the Go
// template *parse* check and curl probes against the endpoint were all green, because the bug
// only fired at render time in a browser: the tab's JS called escapeHtml(), which is declared
// inside a DIFFERENT IIFE in admin.html and is therefore not in scope.
//
// Worse, it lied about it. A .catch() on the end of a promise chain also swallows exceptions
// thrown inside earlier .then() handlers, so the ReferenceError surfaced as "Could not load
// costumes." -- a network error message for a code bug.
//
// So this pulls the tab's IIFE straight out of templates/admin.html, runs it against a stub DOM
// with a realistic payload, and fails if it throws or does not build a row and a sprite for every
// costume. It catches any admin-panel JS that references something out of scope.

import { readFileSync } from "node:fs";

const TEMPLATE = "templates/admin.html";
const MARKER = "// ── Costumes: the review queue";

// ---------------------------------------------------------------- the payload
// Mirrors what handlers.AdminCostumes returns. The unlabelled set is derived the same way
// costumes.Unlabelled() derives it: every catalog code no label points at and that is not hidden.
// (internal/costumes/costumes_test.go is what proves that logic; here it only needs to be
// realistic enough to drive the rendering.)
function payload() {
  const catalog = JSON.parse(readFileSync("internal/costumes/catalog.json", "utf8"));
  const labels = JSON.parse(readFileSync("internal/costumes/labels.json", "utf8"));

  const labelled = new Set(labels.shared.map((s) => s.code));
  for (const byLabel of Object.values(labels.species)) {
    for (const code of Object.values(byLabel)) labelled.add(code);
  }
  const hidden = new Set(labels.hidden ?? []);

  const spriteURL = (dex, code) => `/api/costume-sprite/pm${dex}.${code[0]}${code.slice(2)}.s.icon.png`;

  const costumes = Object.entries(catalog.codes)
    .filter(([code, e]) => !labelled.has(code) && !hidden.has(code) && e.dex.length)
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([code, e]) => ({
      code,
      pretty: e.pretty,
      suggested: e.suggested ?? "",
      dex: e.dex,
      sprite_url: spriteURL(e.dex[0], code),
      species: e.dex.map((d) => `Species${d}`),
      // One per eligible species: a costume is named from the whole set, not from whichever
      // species happens to be first.
      sprites: e.dex.map((d) => ({ dex: d, species: `Species${d}`, url: spriteURL(d, code) })),
    }));

  // Force both branches of the name rendering, so a costume with no upstream name is covered too.
  if (costumes.length && !costumes.some((c) => !c.suggested)) costumes[0].suggested = "";
  if (costumes.length && !costumes.some((c) => c.suggested)) costumes[0].suggested = "Test Hat";

  return { ok: true, costumes };
}

// ------------------------------------------------------------------ the stub DOM
// Records what was built so we can assert the tab produced a row and an <img> per costume.
function stubDom() {
  const made = [];
  const handlers = [];
  const el = (tag) => {
    const n = {
      tag,
      style: { cssText: "", minWidth: "" },
      children: [],
      _text: "",
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
      // The name / hide controls are real elements with real listeners; without these the tab
      // throws while building a row, which is exactly the failure this script exists to catch.
      addEventListener(evt, fn) {
        handlers.push({ node: this, evt, fn });
      },
    };
    made.push(n);
    return n;
  };
  const list = el("div");
  const status = el("span");
  const zoomOverlay = el("div");
  const zoomContent = el("div");
  const zoomClose = el("button");
  const btn = { disabled: false, addEventListener() {} };
  zoomOverlay.style.display = "none";

  const byId = {
    "costume-list": list,
    "costume-status": status,
    "btn-check-costumes": btn,
    "costume-zoom-overlay": zoomOverlay,
    "costume-zoom-content": zoomContent,
    "costume-zoom-close": zoomClose,
  };

  globalThis.document = {
    getElementById: (id) => byId[id] ?? null,
    createElement: el,
    querySelector: () => ({ content: "csrf-token" }),
    addEventListener() {}, // the Escape-to-close binding
  };
  globalThis.confirm = () => true;
  return { made, list, status, zoomOverlay, zoomContent, handlers };
}

// ---------------------------------------------------------------------- the run
const html = readFileSync(TEMPLATE, "utf8");
const start = html.indexOf(MARKER);
if (start < 0) {
  console.error(`FAILED: could not find ${JSON.stringify(MARKER)} in ${TEMPLATE}.`);
  console.error("The costumes tab was renamed or removed; update MARKER in this script.");
  process.exit(1);
}
const end = html.indexOf("})();", start) + "})();".length;
const src = html.slice(start, end);

const data = payload();
const { made, list, status, zoomOverlay, zoomContent, handlers } = stubDom();

const thrown = [];
process.on("unhandledRejection", (e) => thrown.push(e));
globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => data });

try {
  eval(src);
} catch (e) {
  thrown.push(e);
}
await new Promise((r) => setTimeout(r, 50));

const imgs = made.filter((n) => n.tag === "img");
const want = data.costumes.length;

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  console.error(`  rows=${list.children.length} imgs=${imgs.length} want=${want}`);
  if (thrown.length) console.error(`  threw: ${thrown.map(String).join("; ")}`);
  console.error(`\nThe Costumes tab does not render. Open /admin in a browser and read the console.`);
  process.exit(1);
};

if (thrown.length) fail(`the tab's JS threw: ${thrown.map(String).join("; ")}`);
if (!want) {
  console.log("every costume is named; nothing for the tab to render");
  process.exit(0);
}
if (list.children.length !== want) fail("the tab did not render a row per costume");
if (imgs.length !== want) fail("the tab did not render a sprite per costume");
if (!/^\/api\/costume-sprite\//.test(imgs[0].src ?? "")) fail("sprites are not served through our proxy");
if (!status.textContent) fail("the tab did not report how many costumes need naming");

// Naming is the point of the tab now, so a row without working controls is a broken tab.
const inputs = made.filter((n) => n.tag === "input");
const buttons = made.filter((n) => n.tag === "button");
if (inputs.length !== want) fail("not every costume has a name box");
if (buttons.length < want * 2) fail("not every costume has both a Name and a Not-a-costume button");
if (handlers.filter((h) => h.evt === "click").length < want * 2) {
  fail("the name / hide buttons have no click handlers");
}

// Clicking a thumbnail must open the costume at full size on EVERY species that has it. A 56px
// thumbnail is not enough to name a costume from, and the name is permanent once trainers use it.
const thumbClicks = handlers.filter((h) => h.evt === "click" && h.node.tag === "img");
if (thumbClicks.length !== want) fail("the sprite thumbnails are not clickable");

// Pick the costume with the most species, so a single-species one cannot make this pass by luck.
const multi = data.costumes.reduce((a, b) => (b.sprites.length > a.sprites.length ? b : a));
const idx = data.costumes.indexOf(multi);
try {
  thumbClicks[idx].fn();
} catch (e) {
  thrown.push(e);
  fail(`clicking a thumbnail threw: ${e}`);
}

const zoomImgs = zoomContent.children.flatMap((c) => c.children ?? []).flatMap((c) => c.children ?? []);
const bigs = zoomImgs.filter((n) => n.tag === "img");
if (zoomOverlay.style.display !== "flex") fail("clicking a thumbnail did not open the zoom");
if (bigs.length !== multi.sprites.length) {
  fail(`the zoom shows ${bigs.length} sprite(s) for ${multi.code}, want ${multi.sprites.length} (one per species)`);
}
if (!bigs.every((n) => /^\/api\/costume-sprite\//.test(n.src ?? ""))) {
  fail("the zoom's sprites are not served through our proxy");
}

console.log(
  `admin Costumes tab renders ${want} costumes with working name controls, and clicking ${multi.code} ` +
    `zooms to ${bigs.length} sprite(s), one per species. Nothing throws.`,
);
