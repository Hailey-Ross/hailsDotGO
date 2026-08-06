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
// costumes.", a network error message for a code bug.
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

  // What the panel has already named, which is the only thing Unname can take back. Both branches:
  // one nobody has recorded (removable) and one trainers already use (must not be).
  const named = [
    {
      code: "f:NAMED_WRONGLY",
      label: "Wrong Name",
      by: "hails",
      at: "2026-08-06T04:00:00Z",
      dex: [25],
      sprite_url: spriteURL(25, "f:NAMED_WRONGLY"),
      species: ["Pikachu"],
      used: 0,
    },
    {
      code: "f:NAMED_AND_USED",
      label: "Popular Hat",
      by: "hails",
      at: "2026-08-05T04:00:00Z",
      dex: [25],
      sprite_url: spriteURL(25, "f:NAMED_AND_USED"),
      species: ["Pikachu"],
      used: 3,
    },
  ];

  return { ok: true, costumes, named };
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
  const namedCard = el("div");
  const namedList = el("div");
  namedCard.style.display = "none";
  // A real recorded element, not a stub with a no-op listener: the "Fetch new costumes" handler
  // has to be drivable, because the answer it writes used to be destroyed by the refresh that
  // followed it and nothing noticed.
  const btn = el("button");
  btn.disabled = false;
  zoomOverlay.style.display = "none";

  const byId = {
    "costume-list": list,
    "costume-status": status,
    "btn-check-costumes": btn,
    "costume-zoom-overlay": zoomOverlay,
    "costume-zoom-content": zoomContent,
    "costume-zoom-close": zoomClose,
    "costume-named-card": namedCard,
    "costume-named-list": namedList,
  };

  globalThis.document = {
    getElementById: (id) => byId[id] ?? null,
    createElement: el,
    querySelector: () => ({ content: "csrf-token" }),
    addEventListener() {}, // the Escape-to-close binding
  };
  globalThis.confirm = () => true;
  return { made, list, status, btn, zoomOverlay, zoomContent, namedCard, namedList, handlers };
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
const { made, list, status, btn, zoomOverlay, zoomContent, namedCard, namedList, handlers } = stubDom();

// What DriftCheck answers when upstream has nothing new. It is the case that used to be invisible:
// the note was written, then wiped by the refresh a few hundred ms later, so a working check looked
// like a button that did nothing.
const NOTE =
  "no new codes and no new species upstream (1268 costume shiny asset(s) among 3750 files)" +
  " · 14 costume(s) have no label yet and cannot be recorded, see the Costumes tab";
const checkResult = { ok: true, result: { key: "costumes", ok: true, note: NOTE, count: 0 }, synced: "6280ccb86615fe270fa0c9ba1772660f4bf3e360" };

const thrown = [];
const posted = [];
// The first press fails, so the script can tell whether a press that got no answer still spends
// the one cheap cached lookup the page is allowed.
let failNextPost = true;
process.on("unhandledRejection", (e) => thrown.push(e));
globalThis.fetch = async (url, opts) => {
  if (opts && opts.method === "POST") {
    posted.push(url);
    if (failNextPost) {
      failNextPost = false;
      return { ok: false, status: 502, json: async () => ({ ok: false }) };
    }
    return { ok: true, status: 200, json: async () => checkResult };
  }
  return { ok: true, status: 200, json: async () => data };
};

try {
  eval(src);
} catch (e) {
  thrown.push(e);
}
await new Promise((r) => setTimeout(r, 50));

// Scoped to the backlog list, not every <img> the tab made: the "named here" rows below carry
// sprites of their own, and counting those would turn "one sprite per costume" into a tally.
const imgsIn = (root) => (root.children ?? []).flatMap((c) => (c.tag === "img" ? [c] : imgsIn(c)));
const imgs = imgsIn(list);
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

// "Fetch new costumes" must leave its answer on screen. The button asks upstream whether the game
// has costumes we have never synced; the reply is the entire point of pressing it, and it used to
// be overwritten by the list refresh that ran straight afterwards.
const checkClick = handlers.find((h) => h.node === btn && h.evt === "click");
if (!checkClick) fail("the Fetch new costumes button has no click handler");

const press = async (what) => {
  try {
    checkClick.fn();
  } catch (e) {
    thrown.push(e);
    fail(`${what} threw: ${e}`);
  }
  await new Promise((r) => setTimeout(r, 50));
};

// Press one fails (HTTP 502). It must report that, must re-enable the button, and must NOT count
// as the answered press: burning the cached path on a press that got nothing would send every
// later press upstream for five API calls to learn what a cached listing already knew.
const realConsoleError = console.error;
console.error = () => {}; // the tab logs the deliberate 502; do not let it look like a real failure
await press("a failing press");
console.error = realConsoleError;
if (thrown.length) fail(`a failed check threw instead of reporting: ${thrown.map(String).join("; ")}`);
if (!/failed/i.test(status.textContent)) fail(`a failed check reported ${JSON.stringify(status.textContent)}`);
if (btn.disabled) fail("a failed check left the button disabled");

await press("clicking Fetch new costumes");
if (thrown.length) fail(`the upstream check threw: ${thrown.map(String).join("; ")}`);
if (status.textContent !== NOTE) {
  fail(
    `the drift note did not survive the refresh: status is ${JSON.stringify(status.textContent)}, ` +
      `want ${JSON.stringify(NOTE)}`,
  );
}
if (btn.disabled) fail("the Fetch new costumes button was left disabled");

// The server answers the first press from a cached listing, because one scan costs five GitHub API
// calls shared with the Check Scrapers panel. A second press has to ask for a live look, or an
// admin watching an event drop is stuck behind the cache with no way through.
if (posted[0] !== "/admin/check-costumes") fail(`the failed press asked for ${posted[0]}, want the cached path`);
if (posted[1] !== "/admin/check-costumes") {
  fail(`after a FAILED press the next one asked for ${JSON.stringify(posted[1])}, want the cached path still`);
}
await press("the third press");
if (thrown.length) fail(`the third press threw: ${thrown.map(String).join("; ")}`);
if (posted[2] !== "/admin/check-costumes?refresh=1") {
  fail(`the press after an answered one asked for ${JSON.stringify(posted[2])}, want a forced refresh`);
}

// A costume with no upstream name has nothing running the sharesWord guard against it, so the row
// and the zoom must both say so: a silent guard is indistinguishable from one that passed. And
// they must say it ONLY there, or the sentence is a lie on the rows that ARE cross-checked.
const rowText = (n) => (n.children ?? []).flatMap((c) => [c._text ?? "", rowText(c)]).join(" ");
const WARNING = /nothing upstream to cross-check/i;

const noSuggestion = data.costumes.findIndex((c) => !c.suggested);
const withSuggestion = data.costumes.findIndex((c) => c.suggested);
if (noSuggestion < 0 || withSuggestion < 0) fail("the payload does not cover both suggestion branches");

if (!WARNING.test(rowText(list.children[noSuggestion]))) {
  fail("a costume with no upstream name does not say that nothing is cross-checking its label");
}
if (WARNING.test(rowText(list.children[withSuggestion]))) {
  fail(`${data.costumes[withSuggestion].code} has an upstream name but still claims nothing cross-checks it`);
}

// The zoom is the view an admin actually names from, so the same has to hold there.
for (const [idx, want] of [[noSuggestion, true], [withSuggestion, false]]) {
  zoomContent.textContent = "";
  thumbClicks[idx].fn();
  const shown = WARNING.test(rowText(zoomContent));
  if (shown !== want) {
    fail(
      `the zoom for ${data.costumes[idx].code} ${shown ? "claims" : "does not say"} nothing cross-checks it, ` +
        `want the opposite`,
    );
  }
}

// Naming a costume drops it out of the backlog at once, so this is the ONLY route back to a name.
// Without it the Unname endpoint was unreachable from the browser, and two GO Tour costumes sat on
// the wrong variant with nothing in the panel able to correct them.
if (namedCard.style.display === "none") fail("names given here are not shown, so none can be taken back");
if (namedList.children.length !== data.named.length) {
  fail(`the named list rendered ${namedList.children.length} rows, want ${data.named.length}`);
}

const namedButtons = namedList.children.map((row) => (row.children ?? []).find((c) => c.tag === "button"));
if (namedButtons.some((b) => !b)) fail("a named costume has no button, so it cannot be taken back");

const removable = namedButtons[data.named.findIndex((n) => !n.used)];
const inUse = namedButtons[data.named.findIndex((n) => n.used)];

// A label trainers already recorded must not offer a removal: taking it back would blank the
// costume art on their saved entries. The endpoint refuses too, so a live button could only fail.
if (!inUse.disabled) fail("a costume trainers already recorded still offers to have its name removed");
if (handlers.some((h) => h.node === inUse && h.evt === "click")) {
  fail("the in-use name has a click handler, so it can be pressed anyway");
}

const removeClick = handlers.find((h) => h.node === removable && h.evt === "click");
if (!removeClick) fail("the Remove name button has no click handler");

const deletes = [];
const realFetch = globalThis.fetch;
globalThis.fetch = async (url, opts) => {
  if (opts && opts.method === "DELETE") {
    deletes.push(url);
    return { ok: true, status: 200, json: async () => ({ ok: true }) };
  }
  return realFetch(url, opts);
};
try {
  removeClick.fn();
} catch (e) {
  thrown.push(e);
  fail(`removing a name threw: ${e}`);
}
await new Promise((r) => setTimeout(r, 50));
if (thrown.length) fail(`removing a name threw: ${thrown.map(String).join("; ")}`);

const wantDelete = `/api/admin/costumes/name?code=${encodeURIComponent(data.named[0].code)}`;
if (deletes[0] !== wantDelete) {
  fail(`Remove name sent ${JSON.stringify(deletes[0])}, want a DELETE to ${JSON.stringify(wantDelete)}`);
}

console.log(
  `admin Costumes tab renders ${want} costumes with working name controls, and clicking ${multi.code} ` +
    `zooms to ${bigs.length} sprite(s), one per species. Fetching upstream leaves its answer on ` +
    `screen. A name given here can be taken back, and one trainers already use cannot. Nothing throws.`,
);
