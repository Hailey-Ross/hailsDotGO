import { loadGameData, pokeName, pokeSprite } from "./shared/gamedata";
import { createPicker } from "./shared/picker";
import type { PickerEntry } from "./shared/picker";
import type { PokemonStat } from "./shared/types";
import type { GameData } from "./shared/types";
import { buildTabs } from "./shared/tabs";

declare const JSC: Record<string, string>;
declare const IV: Record<string, string>;
declare const IV_CTX: { loggedIn: boolean; trainerLevel: number };


interface IVCandidate {
  atk_iv: number;
  def_iv: number;
  sta_iv: number;
  level: number;
  cp: number;
  hp: number;
  iv_pct: number;
}

interface OCRExtracted {
  cp: number;
  cp_source: "text" | "arc" | "none";
  hp: number;
  raw_dust: number;
  normalised_dust: number;
  pokemon_name: string;
  name_source: "footer" | "mega" | "card" | "";
  appraisal_bars: number;
  is_hundo: boolean;
  is_lucky: boolean;
  is_shadow: boolean;
  is_purified: boolean;
  raw_cp: string;
}

interface CalcResponse {
  candidates: IVCandidate[];
  count: number;
  definitive: boolean;
  pokemon: { pokemon_name: string; form: string; pokemon_id: number };
  extracted?: OCRExtracted;
}

interface BoxEntry {
  id: number;
  pokemon_name: string;
  form: string;
  cp: number;
  level: number;
  atk_iv: number | null;
  def_iv: number | null;
  sta_iv: number | null;
  iv_candidates: IVCandidate[] | null;
  note: string;
}

function ivPokemonEntries(data: GameData): PickerEntry[] {
  const out: PickerEntry[] = [];
  for (const p of data.pokemon ?? []) {
    const form = p.form ?? "";
    const baseName = pokeName(data, p.pokemon_name);
    const label = form && form !== "Normal" ? `${baseName} (${form})` : baseName;
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

const app = document.getElementById("iv-app")!;

const CSRF_TOKEN = (() => {
  const m = document.querySelector<HTMLMetaElement>('meta[name="csrf-token"]');
  return m?.content ?? "";
})();

function jsonHeaders(): HeadersInit {
  return { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN };
}

function showToast(msg: string, ok: boolean) {
  const t = document.createElement("div");
  t.className = "toast" + (ok ? " toast-success" : " toast-error");
  t.textContent = msg;
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 3000);
}

function renderCandidates(
  candidates: IVCandidate[],
  definitive: boolean,
  onSave?: (c: IVCandidate) => void
): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "iv-results";

  const summary = document.createElement("p");
  summary.className = "iv-result-summary";
  if (candidates.length === 0) {
    summary.textContent = IV.resultNoMatch;
    wrap.appendChild(summary);
    return wrap;
  }
  summary.textContent = definitive
    ? IV.resultDefinitive
    : IV.resultCount.replace("{n}", String(candidates.length));
  wrap.appendChild(summary);

  const table = document.createElement("table");
  table.className = "iv-table";
  const cols = [IV.colAtk, IV.colDef, IV.colSta, IV.colLevel, IV.colIVPct, IV.colCP, IV.colHP];
  if (onSave) cols.push(IV.colActions);
  const thead = document.createElement("thead");
  const hr = document.createElement("tr");
  cols.forEach((label) => {
    const th = document.createElement("th");
    th.textContent = label;
    hr.appendChild(th);
  });
  thead.appendChild(hr);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  candidates.forEach((c) => {
    const tr = document.createElement("tr");
    [
      String(c.atk_iv),
      String(c.def_iv),
      String(c.sta_iv),
      String(c.level),
      c.iv_pct.toFixed(1) + "%",
      String(c.cp),
      String(c.hp),
    ].forEach((v) => {
      const td = document.createElement("td");
      td.textContent = v;
      tr.appendChild(td);
    });
    if (onSave) {
      const td = document.createElement("td");
      const btn = document.createElement("button");
      btn.className = "btn btn-sm";
      btn.textContent = IV.btnSaveBox;
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          await onSave(c);
        } catch {
          btn.disabled = false;
        }
      });
      td.appendChild(btn);
      tr.appendChild(td);
    }
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  const tw = document.createElement("div");
  tw.className = "table-wrap";
  tw.appendChild(table);
  wrap.appendChild(tw);
  return wrap;
}

function buildManualTab(data: GameData): HTMLElement {
  const panel = document.createElement("div");
  panel.className = "iv-manual-panel";

  const wrap = document.createElement("div");
  wrap.className = "iv-form";
  panel.appendChild(wrap);

  const pickerLabel = document.createElement("label");
  pickerLabel.className = "form-label";
  pickerLabel.textContent = IV.fieldPokemon;
  wrap.appendChild(pickerLabel);
  const entries = ivPokemonEntries(data);
  const picker = createPicker({
    entries,
    placeholder: JSC.searchPokemon ?? "Search Pokémon...",
    showOnFocus: true,
    onSelect: () => {},
  });
  wrap.appendChild(picker.root);

  function numField(label: string, min: number, max: number): HTMLInputElement {
    const lbl = document.createElement("label");
    lbl.className = "form-label";
    lbl.textContent = label;
    const inp = document.createElement("input");
    inp.type = "number";
    inp.className = "form-input";
    inp.min = String(min);
    inp.max = String(max);
    inp.placeholder = label;
    lbl.appendChild(inp);
    wrap.appendChild(lbl);
    return inp;
  }

  const cpInput = numField(IV.fieldCP, 10, 50000);
  const hpInput = numField(IV.fieldHP, 1, 999);

  const dustLabel = document.createElement("label");
  dustLabel.className = "form-label";
  dustLabel.textContent = IV.fieldDust;
  const dustSel = document.createElement("input");
  dustSel.type = "number";
  dustSel.className = "form-input";
  dustSel.min = "200";
  dustSel.max = "100000";
  dustSel.placeholder = "e.g. 1000";
  dustLabel.appendChild(dustSel);
  wrap.appendChild(dustLabel);

  // Pokémon Status -- normalizes the displayed dust cost to the standard bracket cost.
  // Shadow costs 1.2×, Purified costs 0.9×, Lucky costs 0.5× the base cost.
  const statusLabel = document.createElement("label");
  statusLabel.className = "form-label";
  statusLabel.textContent = IV.fieldStatus;
  wrap.appendChild(statusLabel);
  const statusRow = document.createElement("div");
  statusRow.className = "radio-row";
  let dustMultiplier = 1.0; // multiply displayed dust by this to get standard cost
  const statusOptions: { v: string; l: string; mult: number }[] = [
    { v: "normal",   l: IV.statusNormal,   mult: 1.0 },
    { v: "shadow",   l: IV.statusShadow,   mult: 10 / 12 },
    { v: "purified", l: IV.statusPurified, mult: 10 / 9 },
    { v: "lucky",    l: IV.statusLucky,    mult: 2.0 },
  ];
  statusOptions.forEach(({ v, l, mult }) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "radio-btn" + (v === "normal" ? " active" : "");
    btn.textContent = l;
    btn.dataset.value = v;
    btn.addEventListener("click", () => {
      dustMultiplier = mult;
      statusRow.querySelectorAll<HTMLButtonElement>(".radio-btn")
        .forEach((b) => b.classList.toggle("active", b.dataset.value === v));
    });
    statusRow.appendChild(btn);
  });
  wrap.appendChild(statusRow);

  const lvlLabel = document.createElement("label");
  lvlLabel.className = "form-label";
  lvlLabel.textContent = IV.fieldTrainerLvl;
  const lvlInput = document.createElement("input");
  lvlInput.type = "number";
  lvlInput.className = "form-input";
  lvlInput.min = "1";
  lvlInput.max = "50";
  lvlInput.value = IV_CTX.trainerLevel > 0 ? String(IV_CTX.trainerLevel) : "40";
  lvlLabel.appendChild(lvlInput);
  wrap.appendChild(lvlLabel);

  const topStatLabel = document.createElement("label");
  topStatLabel.className = "form-label";
  topStatLabel.textContent = IV.fieldTopStat;
  wrap.appendChild(topStatLabel);
  const topStatRow = document.createElement("div");
  topStatRow.className = "radio-row";
  let selectedTopStat = "";
  [
    { v: "atk", l: IV.topAtk },
    { v: "def", l: IV.topDef },
    { v: "sta", l: IV.topSta },
    { v: "", l: IV.topUnknown },
  ].forEach(({ v, l }) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "radio-btn" + (v === "" ? " active" : "");
    btn.textContent = l;
    btn.dataset.value = v;
    btn.addEventListener("click", () => {
      selectedTopStat = v;
      topStatRow
        .querySelectorAll<HTMLButtonElement>(".radio-btn")
        .forEach((b) => b.classList.toggle("active", b.dataset.value === v));
    });
    topStatRow.appendChild(btn);
  });
  wrap.appendChild(topStatRow);

  const starsLabel = document.createElement("label");
  starsLabel.className = "form-label";
  starsLabel.textContent = IV.fieldAppraisal;
  const starsSel = document.createElement("select");
  starsSel.className = "form-select";
  [
    { v: "-1", l: IV.starsUnknown },
    { v: "3", l: IV.stars3 },
    { v: "2", l: IV.stars2 },
    { v: "1", l: IV.stars1 },
    { v: "0", l: IV.stars0 },
  ].forEach(({ v, l }) => {
    const o = document.createElement("option");
    o.value = v;
    o.textContent = l;
    starsSel.appendChild(o);
  });
  starsSel.value = "-1";
  starsLabel.appendChild(starsSel);
  wrap.appendChild(starsLabel);

  const calcBtn = document.createElement("button");
  calcBtn.type = "button";
  calcBtn.className = "btn btn-primary";
  calcBtn.textContent = IV.btnCalculate;
  wrap.appendChild(calcBtn);

  const resultsContainer = document.createElement("div");
  resultsContainer.className = "iv-results-container";
  panel.appendChild(resultsContainer);

  calcBtn.addEventListener("click", async () => {
    const entry = picker.getSelected();
    if (!entry) return;
    const cp = parseInt(cpInput.value, 10);
    const hp = parseInt(hpInput.value, 10);
    const dust = parseInt(dustSel.value, 10);
    const trainerLevel = parseInt(lvlInput.value, 10);
    if (!cp || !hp || !dust || !trainerLevel) return;

    calcBtn.disabled = true;
    resultsContainer.innerHTML = "";
    try {
      const resp = await fetch("/api/iv/calculate", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({
          pokemon_name: (entry.data as PokemonStat).pokemon_name,
          form: (entry.data as PokemonStat).form ?? "",
          cp,
          hp,
          dust_cost: Math.round(dust * dustMultiplier),
          trainer_level: trainerLevel,
          top_stat: selectedTopStat,
          appraisal_bars: parseInt(starsSel.value, 10),
        }),
      });
      const result: CalcResponse = await resp.json();
      const poke = result.pokemon ?? { pokemon_name: entry.name, form: "", pokemon_id: 0 };

      const onSave = IV_CTX.loggedIn
        ? async (c: IVCandidate) => {
            const r2 = await fetch("/api/iv/pokemon", {
              method: "POST",
              headers: jsonHeaders(),
              body: JSON.stringify({
                pokemon_name: poke.pokemon_name,
                form: poke.form,
                cp: c.cp,
                level: c.level,
                atk_iv: c.atk_iv,
                def_iv: c.def_iv,
                sta_iv: c.sta_iv,
                iv_candidates: result.candidates,
              }),
            });
            showToast(r2.ok ? IV.saveSuccess : IV.saveFailed, r2.ok);
          }
        : undefined;

      resultsContainer.appendChild(renderCandidates(result.candidates ?? [], result.definitive, onSave));
    } catch {
      resultsContainer.textContent = IV.commonFailed;
    } finally {
      calcBtn.disabled = false;
    }
  });

  return panel;
}

function buildStatusBadges(ext: OCRExtracted): HTMLElement {
  const row = document.createElement("div");
  row.className = "ocr-badges";
  const add = (label: string, cls: string) => {
    const b = document.createElement("span");
    b.className = "ocr-badge " + cls;
    b.textContent = label;
    row.appendChild(b);
  };
  if (ext.is_hundo)    add("100% IV", "badge-hundo");
  if (ext.is_lucky)    add("Lucky",   "badge-lucky");
  if (ext.is_shadow)   add("Shadow",  "badge-shadow");
  if (ext.is_purified) add("Purified","badge-purified");
  if (ext.cp_source === "arc") add("CP: arc scan", "badge-arc");
  if (ext.name_source) add("Name: " + ext.name_source, "badge-source");
  return row;
}

function buildExtractedCard(
  ext: OCRExtracted,
  onRecalc: (cp: number, hp: number, dust: number, stars: number, name: string) => void
): HTMLElement {
  const card = document.createElement("div");
  card.className = "ocr-extracted-card";

  card.appendChild(buildStatusBadges(ext));

  function field(label: string, value: string | number, type = "number"): HTMLInputElement {
    const row = document.createElement("label");
    row.className = "ocr-field-row";
    const lbl = document.createElement("span");
    lbl.className = "ocr-field-label";
    lbl.textContent = label;
    const inp = document.createElement("input");
    inp.type = type;
    inp.className = "form-input ocr-field-input";
    inp.value = String(value);
    row.appendChild(lbl);
    row.appendChild(inp);
    card.appendChild(row);
    return inp;
  }

  const nameInp  = field("Name",       ext.pokemon_name, "text");
  const cpInp    = field("CP",         ext.cp > 0 ? ext.cp : "");
  const hpInp    = field("HP",         ext.hp > 0 ? ext.hp : "");
  const dustInp  = field(
    ext.normalised_dust !== ext.raw_dust
      ? `Dust (raw: ${ext.raw_dust})`
      : "Dust",
    ext.normalised_dust > 0 ? ext.normalised_dust : ext.raw_dust > 0 ? ext.raw_dust : ""
  );
  const starsInp = field("Stars (0-3, -1=unknown)", ext.appraisal_bars);

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "btn btn-primary";
  btn.textContent = "Recalculate";
  btn.addEventListener("click", async () => {
    const cp    = parseInt((cpInp as HTMLInputElement).value, 10);
    const hp    = parseInt((hpInp as HTMLInputElement).value, 10);
    const dust  = parseInt((dustInp as HTMLInputElement).value, 10);
    const stars = parseInt((starsInp as HTMLInputElement).value, 10);
    const name  = (nameInp as HTMLInputElement).value.trim();
    if (!cp || !hp || !dust || !name) return;
    btn.disabled = true;
    try {
      await onRecalc(cp, hp, dust, isNaN(stars) ? -1 : stars, name);
    } finally {
      btn.disabled = false;
    }
  });
  card.appendChild(btn);
  return card;
}

function buildOCRTab(): HTMLElement {
  if (!IV_CTX.loggedIn) {
    const container = document.createElement("div");
    container.className = "loading-state";
    const p = document.createElement("p");
    p.textContent = IV.ocrHint;
    container.appendChild(p);
    return container;
  }
  const wrap = document.createElement("div");
  wrap.className = "iv-ocr";

  const controlsCard = document.createElement("div");
  controlsCard.className = "iv-form";
  wrap.appendChild(controlsCard);

  const fileLabel = document.createElement("label");
  fileLabel.className = "btn btn-secondary";
  fileLabel.textContent = IV.btnOCRScan;
  const fileInput = document.createElement("input");
  fileInput.type = "file";
  fileInput.accept = "image/jpeg,image/png";
  fileInput.style.display = "none";
  fileLabel.appendChild(fileInput);
  controlsCard.appendChild(fileLabel);

  const status = document.createElement("p");
  status.className = "iv-status";
  controlsCard.appendChild(status);

  const extractedContainer = document.createElement("div");
  extractedContainer.className = "ocr-extracted-container";
  controlsCard.appendChild(extractedContainer);

  const resultsContainer = document.createElement("div");
  resultsContainer.className = "iv-results-container";
  wrap.appendChild(resultsContainer);

  let lastPoke = { pokemon_name: "", form: "", pokemon_id: 0 };
  let lastCandidates: IVCandidate[] = [];

  async function runRecalc(cp: number, hp: number, dust: number, stars: number, name: string) {
    resultsContainer.innerHTML = "";
    try {
      const body: Record<string, unknown> = {
        pokemon_name: name.toLowerCase(),
        cp,
        hp,
        dust_cost: dust,
        trainer_level: IV_CTX.trainerLevel || 40,
      };
      if (stars >= 0) body.appraisal_bars = stars;
      const resp = await fetch("/api/iv/calculate", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify(body),
      });
      const result: CalcResponse = await resp.json();
      lastPoke = result.pokemon ?? lastPoke;
      lastCandidates = result.candidates ?? [];
      resultsContainer.appendChild(
        renderCandidates(lastCandidates, result.definitive, makeSaveHandler())
      );
    } catch {
      status.textContent = IV.ocrFailed;
    }
  }

  function makeSaveHandler() {
    return async (c: IVCandidate) => {
      const r2 = await fetch("/api/iv/pokemon", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({
          pokemon_name: lastPoke.pokemon_name,
          form: lastPoke.form,
          cp: c.cp,
          level: c.level,
          atk_iv: c.atk_iv,
          def_iv: c.def_iv,
          sta_iv: c.sta_iv,
          iv_candidates: lastCandidates,
        }),
      });
      showToast(r2.ok ? IV.saveSuccess : IV.saveFailed, r2.ok);
    };
  }

  fileInput.addEventListener("change", async () => {
    const file = fileInput.files?.[0];
    if (!file) return;
    status.textContent = "Scanning…";
    extractedContainer.innerHTML = "";
    resultsContainer.innerHTML = "";
    fileInput.value = "";
    const fd = new FormData();
    fd.append("image", file);
    try {
      const tl = IV_CTX.trainerLevel > 0 ? IV_CTX.trainerLevel : 40;
      const resp = await fetch(`/api/iv/ocr?trainer_level=${tl}`, {
        method: "POST",
        headers: { "X-CSRF-Token": CSRF_TOKEN },
        body: fd,
      });
      if (!resp.ok) {
        status.textContent = IV.ocrFailed;
        return;
      }
      const result: CalcResponse = await resp.json();
      status.textContent = "";
      lastPoke = result.pokemon ?? { pokemon_name: "", form: "", pokemon_id: 0 };
      lastCandidates = result.candidates ?? [];

      if (result.extracted) {
        extractedContainer.appendChild(
          buildExtractedCard(result.extracted, (cp, hp, dust, stars, name) =>
            runRecalc(cp, hp, dust, stars, name)
          )
        );
      }

      resultsContainer.appendChild(
        renderCandidates(lastCandidates, result.definitive, makeSaveHandler())
      );
    } catch {
      status.textContent = IV.ocrFailed;
    }
  });

  return wrap;
}

function buildBoxTab(): HTMLElement {
  if (!IV_CTX.loggedIn) {
    const container = document.createElement("div");
    container.className = "loading-state";
    const p = document.createElement("p");
    p.textContent = IV.boxHint;
    container.appendChild(p);
    return container;
  }
  const wrap = document.createElement("div");
  wrap.className = "iv-box";

  const countEl = document.createElement("p");
  countEl.className = "iv-box-count";
  wrap.appendChild(countEl);

  const list = document.createElement("div");
  list.className = "iv-box-list";
  wrap.appendChild(list);

  let offset = 0;
  const limit = 50;

  async function loadPage() {
    list.innerHTML = "";
    try {
      const resp = await fetch(`/api/iv/pokemon?limit=${limit}&offset=${offset}`);
      const data: { pokemon: BoxEntry[]; total: number } = await resp.json();
      countEl.textContent = IV.boxCount.replace("{n}", String(data.total));

      if (data.pokemon.length === 0) {
        const empty = document.createElement("div");
        empty.className = "loading-state";
        const p = document.createElement("p");
        p.textContent = IV.boxEmpty;
        empty.appendChild(p);
        list.appendChild(empty);
        return;
      }

      const table = document.createElement("table");
      table.className = "iv-table";
      const thead = document.createElement("thead");
      const hr = document.createElement("tr");
      [IV.fieldPokemon, IV.colLevel, IV.colIVPct, IV.colCP, IV.colActions].forEach((h) => {
        const th = document.createElement("th");
        th.textContent = h;
        hr.appendChild(th);
      });
      thead.appendChild(hr);
      table.appendChild(thead);

      const tbody = document.createElement("tbody");
      for (const e of data.pokemon) {
        const tr = document.createElement("tr");

        const nameTd = document.createElement("td");
        nameTd.textContent = e.pokemon_name + (e.form && e.form !== "Normal" ? ` (${e.form})` : "");
        tr.appendChild(nameTd);

        const lvlTd = document.createElement("td");
        lvlTd.textContent = String(e.level);
        tr.appendChild(lvlTd);

        const ivTd = document.createElement("td");
        if (e.atk_iv !== null && e.def_iv !== null && e.sta_iv !== null) {
          const pct = Math.round((e.atk_iv + e.def_iv + e.sta_iv) / 45 * 1000) / 10;
          ivTd.textContent = pct.toFixed(1) + "%";
        } else {
          ivTd.textContent = "?";
        }
        tr.appendChild(ivTd);

        const cpTd = document.createElement("td");
        cpTd.textContent = String(e.cp);
        tr.appendChild(cpTd);

        const actTd = document.createElement("td");
        const delBtn = document.createElement("button");
        delBtn.type = "button";
        delBtn.className = "btn btn-sm btn-danger";
        delBtn.textContent = IV.btnDelete;
        delBtn.addEventListener("click", async () => {
          delBtn.disabled = true;
          const r = await fetch(`/api/iv/pokemon/${e.id}`, {
            method: "DELETE",
            headers: { "X-CSRF-Token": CSRF_TOKEN },
          });
          if (r.ok) {
            showToast(IV.deleteSuccess, true);
            await loadPage();
          } else {
            showToast(IV.deleteFailed, false);
            delBtn.disabled = false;
          }
        });
        actTd.appendChild(delBtn);
        tr.appendChild(actTd);

        tbody.appendChild(tr);
      }
      table.appendChild(tbody);
      const btw = document.createElement("div");
      btw.className = "table-wrap";
      btw.appendChild(table);
      list.appendChild(btw);

      if (data.total > limit) {
        const pag = document.createElement("div");
        pag.className = "pagination";
        if (offset > 0) {
          const prev = document.createElement("button");
          prev.type = "button";
          prev.className = "btn btn-sm";
          prev.textContent = "←";
          prev.addEventListener("click", () => { offset -= limit; loadPage(); });
          pag.appendChild(prev);
        }
        const pg = document.createElement("span");
        pg.style.margin = "0 0.5rem";
        pg.textContent = `${Math.floor(offset / limit) + 1} / ${Math.ceil(data.total / limit)}`;
        pag.appendChild(pg);
        if (offset + limit < data.total) {
          const next = document.createElement("button");
          next.type = "button";
          next.className = "btn btn-sm";
          next.textContent = "→";
          next.addEventListener("click", () => { offset += limit; loadPage(); });
          pag.appendChild(next);
        }
        list.appendChild(pag);
      }
    } catch {
      const p = document.createElement("p");
      p.textContent = IV.commonFailed;
      list.appendChild(p);
    }
  }

  loadPage();
  return wrap;
}

async function main() {
  let data: GameData;
  try {
    data = await loadGameData();
  } catch {
    app.innerHTML = `<div class="error-state"><p>${IV.commonFailed}</p></div>`;
    return;
  }

  const tabs = [
    { id: "manual", label: IV.tabManual, render: () => buildManualTab(data) },
    ...(IV_CTX.loggedIn ? [{ id: "ocr", label: IV.tabOCR, render: buildOCRTab }] : []),
    ...(IV_CTX.loggedIn ? [{ id: "box", label: IV.tabBox, render: buildBoxTab }] : []),
  ];

  app.innerHTML = "";
  app.appendChild(buildTabs(tabs, "manual"));
}

main();
