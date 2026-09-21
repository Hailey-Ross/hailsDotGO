// Pokemon Box: a trainer's own Pokemon, with add and remove.
//
// This used to be a tab inside the IV calculator, reachable only after running a
// calculation, and the only way in was that calculator or the mobile app, which
// has not shipped. It is its own page now because the raids page scores these
// Pokemon against the boss you have open, which makes the box a thing you visit
// rather than a side effect of a calculation.

import { loadGameData, pokeSprite, pokeName } from "./shared/gamedata";
import { createPicker } from "./shared/picker";
import type { PickerEntry } from "./shared/picker";
import type { GameData } from "./shared/types";

declare const BOX_CTX: { loggedIn: boolean };
declare const BX: Record<string, string>;
declare const JSC: Record<string, string>;

// Read from the meta tag base.html renders, the same way every other page does.
// It is NOT a global here: admin.html and translate.html define a page local
// CSRF_TOKEN in their own inline script, but base.html only publishes the meta
// tag, so declaring it would compile happily and then throw at the first click.
const CSRF_TOKEN =
  (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement | null)?.content ?? "";

interface BoxEntry {
  id: number;
  pokemon_name: string;
  form: string;
  is_shadow: boolean | null;
  is_purified: boolean | null;
  cp: number;
  level: number;
  atk_iv: number | null;
  def_iv: number | null;
  sta_iv: number | null;
  note: string;
  // The form's own art, resolved server side. Absent when the form has no
  // distinct sprite, which is most of them.
  sprite_url?: string;
}

const PAGE_SIZE = 50;
// Matches maxPokemonBoxSize in internal/handlers/iv.go. Shown alongside the count
// so the ceiling is visible before you hit it rather than only when you do.
const MAX_BOX = 9000;

// A blank level on the add form means this. Level 50 is the ceiling and what a
// maxed Pokemon sits at, which is the common case for anything a trainer is
// adding in order to rank it against a raid boss.
const DEFAULT_LEVEL = 50;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

function toast(msg: string, ok: boolean) {
  const t = el("div", "toast " + (ok ? "toast-success" : "toast-error"));
  // The region has to be in the document BEFORE its text changes, or the
  // announcement is unreliable. Appending a node that already contains its
  // message frequently says nothing at all.
  t.setAttribute("role", "status");
  t.setAttribute("aria-live", "polite");
  document.body.appendChild(t);
  requestAnimationFrame(() => { t.textContent = msg; });
  setTimeout(() => t.remove(), 3200);
}

// ivPercent is null when the spread was never pinned down, which the calculator
// leaves as candidates rather than a single answer.
function ivPercent(e: BoxEntry): number | null {
  if (e.atk_iv === null || e.def_iv === null || e.sta_iv === null) return null;
  return Math.round(((e.atk_iv + e.def_iv + e.sta_iv) / 45) * 1000) / 10;
}

function statusTag(e: BoxEntry): HTMLElement | null {
  if (e.is_shadow) return el("span", "box-tag box-tag-shadow", BX.shadowTag);
  if (e.is_purified) return el("span", "box-tag box-tag-purified", BX.purifiedTag);
  return null;
}

async function main() {
  const app = document.getElementById("box-app");
  if (!app) return;

  if (!BOX_CTX.loggedIn) {
    app.innerHTML = "";
    const state = el("div", "loading-state");
    state.appendChild(el("p", undefined, BX.loggedOut));
    const link = el("a", "btn-primary", BX.logIn);
    link.setAttribute("href", "/login?next=/box");
    state.appendChild(link);
    app.appendChild(state);
    return;
  }

  // Guarded like every other page. Unguarded this threw on a failed game data
  // fetch and left the whole page on its spinner with nothing to explain it.
  let data: GameData;
  try {
    data = await loadGameData();
  } catch {
    app.innerHTML = "";
    app.appendChild(el("p", "empty-state", BX.loadFailed));
    return;
  }
  app.innerHTML = "";

  app.appendChild(buildAddPanel(data, () => loadPage(0)));

  const countEl = el("p", "iv-box-count");
  countEl.setAttribute("aria-live", "polite");
  app.appendChild(countEl);
  const list = el("div", "iv-box-list");
  app.appendChild(list);
  const pager = el("div", "box-pager");
  app.appendChild(pager);

  let offset = 0;

  async function loadPage(newOffset: number) {
    offset = newOffset;
    list.innerHTML = "";
    pager.innerHTML = "";
    try {
      const resp = await fetch(`/api/iv/pokemon?limit=${PAGE_SIZE}&offset=${offset}`);
      if (!resp.ok) throw new Error(String(resp.status));
      const payload: { pokemon: BoxEntry[]; total: number } = await resp.json();
      countEl.textContent = BX.count.replace("{n}", String(payload.total)).replace("{max}", String(MAX_BOX));

      if (!payload.pokemon.length) {
        const empty = el("div", "loading-state");
        empty.appendChild(el("p", undefined, BX.empty));
        list.appendChild(empty);
        // Still build the pager: deleting the last entry on page two used to
        // leave "50 of 9000 saved" above an empty page with no way back.
        buildPager(pager, payload.total, offset, loadPage);
        return;
      }

      list.appendChild(buildTable(data, payload.pokemon, () => loadPage(offset)));
      buildPager(pager, payload.total, offset, loadPage);
    } catch {
      list.innerHTML = "";
      list.appendChild(el("p", "empty-state", BX.loadFailed));
    }
  }

  await loadPage(0);
}

function buildTable(data: GameData, entries: BoxEntry[], reload: () => void): HTMLElement {
  const wrap = el("div", "table-wrap");
  const table = el("table", "iv-table");

  const thead = el("thead");
  const hr = el("tr");
  for (const h of [BX.colPokemon, BX.colLevel, BX.colIv, BX.colCp, BX.colActions]) {
    hr.appendChild(el("th", undefined, h));
  }
  thead.appendChild(hr);
  table.appendChild(thead);

  const tbody = el("tbody");
  for (const e of entries) {
    const tr = el("tr");

    const nameTd = el("td");
    nameTd.style.whiteSpace = "nowrap";
    const stat = (data.pokemon ?? []).find(
      (p) => p.pokemon_name.toLowerCase() === e.pokemon_name.toLowerCase()
    );
    if (stat) {
      const img = el("img");
      // Nothing in the bundle separates two forms of a species for sprite
      // purposes: both Zacian rows are pokemon_id 888, so the dex number alone
      // drew Hero of Many Battles beside the text "(Crowned_sword)". The server
      // resolves the form's own art and sends it; the dex sprite is the right
      // answer only when it does not.
      img.src = e.sprite_url || pokeSprite(stat.pokemon_id);
      img.alt = e.pokemon_name;
      img.className = "poke-sprite";
      img.loading = "lazy";
      img.decoding = "async";
      nameTd.appendChild(img);
    }
    nameTd.append(` ${pokeName(data, e.pokemon_name)}`);
    if (e.form && e.form !== "Normal") nameTd.append(` (${e.form})`);
    const tag = statusTag(e);
    if (tag) nameTd.appendChild(tag);
    tr.appendChild(nameTd);

    tr.appendChild(el("td", undefined, String(e.level)));

    const pct = ivPercent(e);
    tr.appendChild(el("td", undefined, pct === null ? "?" : pct.toFixed(1) + "%"));

    tr.appendChild(el("td", undefined, String(e.cp)));

    const actTd = el("td");
    const del = el("button", "btn btn-sm btn-danger", BX.btnDelete);
    // A column of buttons all reading "Remove" tells a screen reader user nothing
    // about which Pokemon each one destroys.
    del.setAttribute("aria-label", BX.btnDeleteNamed.replace("{name}", pokeName(data, e.pokemon_name)));
    del.type = "button";
    del.addEventListener("click", async () => {
      del.disabled = true;
      const r = await fetch(`/api/iv/pokemon/${e.id}`, {
        method: "DELETE",
        headers: { "X-CSRF-Token": CSRF_TOKEN },
      });
      if (r.ok) {
        toast(BX.deleteOk, true);
        reload();
      } else {
        toast(BX.deleteFail, false);
        del.disabled = false;
      }
    });
    actTd.appendChild(del);
    tr.appendChild(actTd);

    tbody.appendChild(tr);
  }

  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}

function buildPager(pager: HTMLElement, total: number, offset: number, go: (o: number) => void) {
  if (total <= PAGE_SIZE) return;
  const prev = el("button", "btn btn-sm", BX.prev);
  prev.type = "button";
  prev.disabled = offset === 0;
  prev.addEventListener("click", () => go(Math.max(0, offset - PAGE_SIZE)));

  const next = el("button", "btn btn-sm", BX.next);
  next.type = "button";
  next.disabled = offset + PAGE_SIZE >= total;
  next.addEventListener("click", () => go(offset + PAGE_SIZE));

  pager.append(prev, next);
}

// One entry per FORM, not per species.
//
// The shared pokemonEntries helper dedupes by name and keeps whichever record
// sorts first, which for the 60 multi form species is usually not the ordinary
// one. Using it here meant picking "Marowak" silently stored form "Alola", so a
// trainer could not add a Kantonian Marowak at all and the box then displayed
// back a form they never chose. The IV calculator already enumerates forms for
// exactly this reason.
function formAwareEntries(data: GameData): PickerEntry[] {
  const out: PickerEntry[] = [];
  for (const p of data.pokemon ?? []) {
    const form = p.form ?? "";
    const base = pokeName(data, p.pokemon_name);
    const label = form && form !== "Normal" ? `${base} (${form.replace(/_/g, " ")})` : base;
    out.push({
      key: form ? `${p.pokemon_name}/${form}` : p.pokemon_name,
      name: p.pokemon_name,
      label,
      sprite: pokeSprite(p.pokemon_id),
      types: data.pokemonTypes?.[p.pokemon_name] ?? [],
      group: form && form !== "Normal" ? 2 : 1,
      data: p,
    });
  }
  return out;
}

// The add form. The calculator is still the right route for a fresh catch whose
// IVs are unknown; this is for a Pokemon whose appraisal you already know.
function buildAddPanel(data: GameData, onAdded: () => void): HTMLElement {
  const panel = el("details", "box-add-panel");
  const summary = el("summary", "box-add-summary", BX.addTitle);
  panel.appendChild(summary);

  const body = el("div", "box-add-body");
  body.appendChild(el("p", "subscribe-desc", BX.addHint));

  const status2 = el("span", "um-form-status");
  status2.setAttribute("role", "status");
  status2.setAttribute("aria-live", "polite");

  const pickerWrap = el("div", "box-field");
  const pickerLabel = el("label", "box-label", BX.fieldPokemon);
  pickerWrap.appendChild(pickerLabel);
  const picker = createPicker({
    entries: formAwareEntries(data),
    placeholder: JSC.searchPokemon,
    onSelect: () => {
      status2.textContent = "";
    },
  });
  pickerWrap.appendChild(picker.root);
  // createPicker takes no label option, so the name is attached to the input it
  // built. Without this the primary control on the form announces as nothing but
  // its placeholder.
  picker.input.id = "box-field-pokemon";
  pickerLabel.htmlFor = "box-field-pokemon";
  picker.input.setAttribute("aria-label", BX.fieldPokemon);
  body.appendChild(pickerWrap);

  const statusWrap = el("div", "box-field");
  statusWrap.appendChild(el("label", "box-label", BX.fieldStatus));
  const statusRow = el("div", "radio-row");
  // These look like radio buttons, so they have to announce like them. Without
  // the roles a screen reader hears three identical buttons and cannot tell
  // which status is selected.
  statusRow.setAttribute("role", "radiogroup");
  statusRow.setAttribute("aria-label", BX.fieldStatus);
  let status = "normal";
  for (const [v, label] of [
    ["normal", BX.statusNormal],
    ["shadow", BX.statusShadow],
    ["purified", BX.statusPurified],
  ] as [string, string][]) {
    const btn = el("button", "radio-btn" + (v === "normal" ? " active" : ""), label);
    btn.type = "button";
    btn.setAttribute("role", "radio");
    btn.setAttribute("aria-checked", v === "normal" ? "true" : "false");
    btn.addEventListener("click", () => {
      status = v;
      statusRow.querySelectorAll(".radio-btn").forEach((b) => {
        b.classList.remove("active");
        b.setAttribute("aria-checked", "false");
      });
      btn.classList.add("active");
      btn.setAttribute("aria-checked", "true");
    });
    statusRow.appendChild(btn);
  }
  statusWrap.appendChild(statusRow);
  body.appendChild(statusWrap);

  let fieldSeq = 0;
  function numField(label: string, min: number, max: number, step: string, value: string, placeholder?: string) {
    const wrap = el("div", "box-field box-field-narrow");
    const id = `box-field-${++fieldSeq}`;
    // for/id, so a screen reader announces the field and tapping the label
    // focuses it. Five small number inputs on a phone need the larger target.
    const lbl = el("label", "box-label", label);
    lbl.htmlFor = id;
    wrap.appendChild(lbl);
    const input = el("input", "auth-input");
    input.id = id;
    input.type = "number";
    input.min = String(min);
    input.max = String(max);
    input.step = step;
    input.value = value;
    if (placeholder) input.placeholder = placeholder;
    wrap.appendChild(input);
    return { wrap, input };
  }

  const cp = numField(BX.fieldCp, 10, 9999, "1", "");
  // Left blank it means 50. Shown as a placeholder rather than prefilled, so the
  // field still reads as "not filled in" and the assumption is visible before
  // the trainer commits to it.
  const level = numField(BX.fieldLevel, 1, 50, "0.5", "", BX.levelPlaceholder.replace("{n}", String(DEFAULT_LEVEL)));
  const atk = numField(BX.fieldAtkIv, 0, 15, "1", "15");
  const def = numField(BX.fieldDefIv, 0, 15, "1", "15");
  const sta = numField(BX.fieldStaIv, 0, 15, "1", "15");

  const grid = el("div", "box-field-grid");
  grid.append(cp.wrap, level.wrap, atk.wrap, def.wrap, sta.wrap);
  body.appendChild(grid);
  // Said out loud rather than left to the placeholder alone, which vanishes the
  // moment the field is touched and is not announced by every screen reader.
  body.appendChild(el("p", "subscribe-hint", BX.levelHint.replace("{n}", String(DEFAULT_LEVEL))));

  const noteWrap = el("div", "box-field");
  const noteLabel = el("label", "box-label", BX.fieldNote);
  noteLabel.htmlFor = "box-field-note";
  noteWrap.appendChild(noteLabel);
  const note = el("input", "auth-input");
  note.id = "box-field-note";
  note.type = "text";
  note.maxLength = 160;
  noteWrap.appendChild(note);
  body.appendChild(noteWrap);

  const addBtn = el("button", "btn-primary", BX.btnAdd);
  addBtn.type = "button";
  addBtn.addEventListener("click", async () => {
    // Checked here rather than letting the server answer. CP and level are the
    // two fields that start empty while the three IVs are prefilled, so the form
    // looks complete when it is not, and the server's reply for that is the word
    // "invalid parameters", which names neither field.
    const fail = (msg: string) => {
      status2.textContent = msg;
      status2.style.color = "#fc8181";
    };
    const selected = picker.getSelected();
    if (!selected) return fail(BX.needPokemon);
    const cpVal = Number(cp.input.value);
    if (!cp.input.value || !Number.isFinite(cpVal) || cpVal < 10 || cpVal > 9999) return fail(BX.needCp);
    // An empty level means 50. Most Pokemon worth checking against a raid boss
    // are maxed, and a trainer who does not know the exact level should not be
    // stopped by the field.
    const lvlVal = level.input.value ? Number(level.input.value) : DEFAULT_LEVEL;
    if (!Number.isFinite(lvlVal) || lvlVal < 1 || lvlVal > 50) return fail(BX.needLevel);
    // Same rule the server applies. Without it a level like 33.3 was answered
    // with a generic rejection that named three fields, none of them this one.
    if (lvlVal * 2 !== Math.trunc(lvlVal * 2)) return fail(BX.needLevel);
    for (const f of [atk, def, sta]) {
      const v = Number(f.input.value);
      if (!Number.isFinite(v) || v < 0 || v > 15) return fail(BX.needIvs);
    }

    addBtn.disabled = true;
    status2.textContent = "";
    const payload = {
      pokemon_name: selected.name,
      form: (selected.data as { form?: string } | undefined)?.form || "Normal",
      is_shadow: status === "shadow",
      is_purified: status === "purified",
      cp: Number(cp.input.value),
      level: lvlVal,
      atk_iv: Number(atk.input.value),
      def_iv: Number(def.input.value),
      sta_iv: Number(sta.input.value),
      note: note.value,
    };
    const r = await fetch("/api/iv/pokemon", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
      body: JSON.stringify(payload),
    });
    addBtn.disabled = false;
    if (r.ok) {
      toast(BX.addOk, true);
      picker.clear();
      cp.input.value = "";
      level.input.value = "";
      note.value = "";
      onAdded();
      return;
    }
    // Map the status onto a translated string rather than printing the server's
    // own English back at the trainer. The full box case in particular needs to
    // say what to do about it, not just that something went wrong.
    if (r.status === 409) return fail(BX.boxFull);
    if (r.status === 400) return fail(BX.addRejected);
    fail(BX.addFail);
  });

  const actions = el("div", "box-add-actions");
  actions.append(addBtn, status2);
  body.appendChild(actions);

  const help = el("p", "subscribe-hint");
  const helpLink = el("a", undefined, BX.dontKnowIvs);
  helpLink.setAttribute("href", "/iv");
  help.appendChild(helpLink);
  body.appendChild(help);

  panel.appendChild(body);
  return panel;
}

main();
