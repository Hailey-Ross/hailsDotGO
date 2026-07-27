// Asserts the costume field suggests costumes without a <datalist>.
//
//   node scripts/check-costume-picker.mjs
//
// Why this exists: the costume field used to be an <input list="..."> pointing at a <datalist>.
// Desktop browsers autocompleted it, so every check and every developer saw it working. Mobile
// browsers render datalist suggestions inconsistently, and on a phone the list did not appear at
// all: the entire costume catalogue was invisible unless you already knew the exact label. A
// trainer reported the Professor Willow costume as missing when it had been synced for weeks,
// because nothing on their screen could tell them we call it "Willow's Lab Coat".
//
// So this drives the real picker against a stub DOM and asserts the three things a phone needs:
// the dropdown opens on focus with no typing, every row carries its shiny sprite, and searching
// the name the GAME uses finds the label WE use.
//
// It also fails if the shiny modal ever goes back to a datalist for costumes.

import { readFileSync } from "node:fs";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const fail = (msg) => {
  console.error(`FAILED: ${msg}`);
  process.exit(1);
};

// ------------------------------------------------------------------ the stub DOM
// createPicker builds real elements and reads .hidden to decide whether the dropdown is open, so
// the stub has to model children, classes and text faithfully enough for that to mean something.
function stubDom() {
  const el = (tag) => {
    const n = {
      tag,
      className: "",
      style: {},
      children: [],
      hidden: false,
      disabled: false,
      value: "",
      src: "",
      alt: "",
      loading: "",
      decoding: "",
      type: "",
      placeholder: "",
      _text: "",
      _listeners: {},
      classList: { add() {}, remove() {}, contains: () => false },
      set textContent(v) {
        this._text = String(v);
        this.children.length = 0;
      },
      get textContent() {
        return this._text;
      },
      set innerHTML(v) {
        if (v === "") this.children.length = 0;
      },
      appendChild(c) {
        this.children.push(c);
        return c;
      },
      contains: () => false,
      setAttribute(k, v) {
        n[k] = v;
      },
      addEventListener(evt, fn) {
        (this._listeners[evt] ??= []).push(fn);
      },
      dispatch(evt, arg) {
        for (const fn of this._listeners[evt] ?? []) fn(arg ?? { preventDefault() {} });
      },
      // Depth-first walk, which is all buildDropdown's querySelector calls need.
      querySelector(sel) {
        const want = sel.split(":")[0].replace(".", "");
        const hit = (node) =>
          node.className.split(" ").includes(want) && !node.className.includes("no-match")
            ? node
            : node.children.reduce((a, c) => a ?? hit(c), null);
        return this.children.reduce((a, c) => a ?? hit(c), null);
      },
      focus() {},
    };
    return n;
  };

  globalThis.document = { createElement: el, addEventListener() {}, body: el("body") };
  globalThis.JSC = { clearSelection: "Clear", noPokemonFound: "No Pokemon found" };
  globalThis.MouseEvent = class {
    constructor(type) {
      this.type = type;
    }
  };
}

// --------------------------------------------------------------------- the bundle
// Bundle the REAL modules, so this cannot pass against a copy of the logic that has drifted from
// what ships. costumes.ts imports catalog.json and labels.json, so the entries are the real ones.
const dir = mkdtempSync(join(tmpdir(), "costume-picker-"));
let mod;
try {
  const out = join(dir, "bundle.mjs");
  await build({
    stdin: {
      contents: `
        export { createPicker } from "../ts/shared/picker";
        export { costumeEntries, costumeShinyUrl } from "../ts/shared/costumes";
      `,
      resolveDir: "scripts",
      loader: "ts",
    },
    bundle: true,
    format: "esm",
    target: "es2020",
    outfile: out,
  });
  mod = await import(`file://${out}`);
} finally {
  process.on("exit", () => rmSync(dir, { recursive: true, force: true }));
}

const { createPicker, costumeEntries, costumeShinyUrl } = mod;

// ------------------------------------------------------------------------ the run
stubDom();

const DEX = 25;
const SPECIES = "Pikachu";
const WILLOW = "Willow's Lab Coat";

const entries = costumeEntries(DEX, SPECIES);
if (!entries.length) fail("Pikachu has no costume entries at all");

const willow = entries.find((e) => e.label === WILLOW);
if (!willow) {
  fail(`the picker does not offer ${JSON.stringify(WILLOW)} for Pikachu (offers ${entries.length} costumes)`);
}
if (!willow.sprite || !willow.sprite.startsWith("/api/costume-sprite/")) {
  fail(`${WILLOW} has no proxied sprite: ${JSON.stringify(willow.sprite)}`);
}
if (!willow.sprite.includes(".fANNIVERSARY_2026.")) {
  fail(`${WILLOW} points at ${willow.sprite}, want the f:ANNIVERSARY_2026 art`);
}
const spriteless = entries.filter((e) => !e.sprite);
if (spriteless.length) {
  fail(`${spriteless.length} costume(s) would render a blank row: ${spriteless.map((e) => e.label).join(", ")}`);
}

// The alias is what makes the name the trainer READ find the label we SHOW.
if (!willow.aliases?.some((a) => a.toLowerCase().includes("professor willow"))) {
  fail(`${WILLOW} carries no "Professor Willow" alias, so searching the official name finds nothing`);
}

const picker = createPicker({
  entries,
  placeholder: "costume",
  noMatchText: "No costumes found",
  showOnFocus: true,
  preview: false,
  maxResults: 100,
  onSelect: () => {},
});
const dropdown = picker.root.children.find((c) => c.className === "picker-dropdown");
if (!dropdown) fail("the picker built no dropdown");

// The "nothing matched" placeholder is a picker-option too, so keep the two apart: one is a
// costume you can choose, the other is the message saying there are none.
const allRows = () => dropdown.children.filter((c) => c.className.split(" ").includes("picker-option"));
const rows = () => allRows().filter((c) => !c.className.split(" ").includes("no-match"));
const labelsShown = () =>
  rows().map((r) => (r.children.find((c) => c.className === "picker-option-name") ?? {}).textContent);

// 1. On a phone there is no hover and often no keyboard yet: focusing must show the list.
if (picker.isDropdownOpen()) fail("the dropdown is open before the field is touched");
picker.input.dispatch("focus");
if (!picker.isDropdownOpen()) {
  fail("focusing the empty costume field showed nothing, which is exactly the datalist bug on mobile");
}
if (!labelsShown().includes(WILLOW)) {
  fail(`focusing listed ${rows().length} costume(s) but not ${JSON.stringify(WILLOW)}`);
}
if (rows().some((r) => !r.children.some((c) => c.tag === "img"))) {
  fail("a dropdown row has no sprite, so the costume cannot be recognised by sight");
}

// 2. Searching the official name has to find our label.
picker.input.value = "Professor Willow";
picker.input.dispatch("input");
if (!labelsShown().includes(WILLOW)) {
  fail(`typing "Professor Willow" listed ${JSON.stringify(labelsShown())}, want ${JSON.stringify(WILLOW)}`);
}

// 3. A substring of our own label still works, and picking a row fills the field.
picker.input.value = "lab coat";
picker.input.dispatch("input");
const row = rows().find((r) => r.children.some((c) => c.textContent === WILLOW));
if (!row) fail('typing "lab coat" did not find the costume');
row.dispatch("mousedown");
if (picker.input.value !== WILLOW) {
  fail(`choosing the row set the field to ${JSON.stringify(picker.input.value)}, want ${JSON.stringify(WILLOW)}`);
}
if (picker.isDropdownOpen()) fail("the dropdown stayed open after a costume was chosen");

// 4. Free text must survive: costume is a free-text column and trainers record costumes we have
//    not labelled yet. A picker that swallowed unknown text would lose data.
picker.input.value = "Some Costume We Have Not Named";
picker.input.dispatch("input");
if (picker.input.value !== "Some Costume We Have Not Named") fail("the picker overwrote free text");
if (rows().length) {
  fail(`an unknown costume matched ${JSON.stringify(labelsShown())} instead of reporting no match`);
}
if (allRows()[0]?.textContent !== "No costumes found") {
  fail(`the no-match row says ${JSON.stringify(allRows()[0]?.textContent)}, want the costume-specific copy`);
}

// 5. The regression itself: no costume field may go back to a <datalist>.
const shinies = readFileSync("ts/shinies.ts", "utf8");
if (/sc-costume-list/.test(shinies)) {
  fail("ts/shinies.ts still references the costume datalist, which does not render on mobile");
}

const resolved = costumeShinyUrl(DEX, SPECIES, "Professor Willow's Assistant");
if (resolved !== willow.sprite) {
  fail(`the typed official name resolved to ${JSON.stringify(resolved)}, want ${JSON.stringify(willow.sprite)}`);
}

console.log(
  `costume picker: focusing the field lists all ${entries.length} Pikachu costumes with sprites and no ` +
    `typing, "Professor Willow" finds "${WILLOW}" through its alias, choosing a row fills the field, ` +
    `unknown text is kept as free text, and no <datalist> is left in the shiny page.`,
);
