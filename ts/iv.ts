import { loadGameData } from "./shared/gamedata";
import { createPicker, pokemonEntries } from "./shared/picker";
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

interface CalcResponse {
  candidates: IVCandidate[];
  count: number;
  definitive: boolean;
  pokemon: { pokemon_name: string; form: string; pokemon_id: number };
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
      btn.addEventListener("click", () => {
        btn.disabled = true;
        onSave(c);
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
  const wrap = document.createElement("div");
  wrap.className = "iv-form";

  const pickerLabel = document.createElement("label");
  pickerLabel.className = "form-label";
  pickerLabel.textContent = IV.fieldPokemon;
  wrap.appendChild(pickerLabel);
  const entries = pokemonEntries(data);
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

  const cpInput = numField(IV.fieldCP, 10, 9999);
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
  wrap.appendChild(resultsContainer);

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
          pokemon_name: entry.name,
          cp,
          hp,
          dust_cost: dust,
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

  return wrap;
}

function buildOCRTab(): HTMLElement {
  if (!IV_CTX.loggedIn) {
    const p = document.createElement("p");
    p.className = "iv-hint";
    p.textContent = IV.ocrHint;
    return p;
  }
  const wrap = document.createElement("div");
  wrap.className = "iv-ocr";

  const fileLabel = document.createElement("label");
  fileLabel.className = "btn btn-secondary iv-file-label";
  fileLabel.textContent = IV.btnOCRScan;
  const fileInput = document.createElement("input");
  fileInput.type = "file";
  fileInput.accept = "image/jpeg,image/png";
  fileInput.style.display = "none";
  fileLabel.appendChild(fileInput);
  wrap.appendChild(fileLabel);

  const status = document.createElement("p");
  status.className = "iv-status";
  wrap.appendChild(status);

  const resultsContainer = document.createElement("div");
  resultsContainer.className = "iv-results-container";
  wrap.appendChild(resultsContainer);

  fileInput.addEventListener("change", async () => {
    const file = fileInput.files?.[0];
    if (!file) return;
    status.textContent = "Scanning…";
    resultsContainer.innerHTML = "";
    fileInput.value = "";
    const fd = new FormData();
    fd.append("image", file);
    try {
      const resp = await fetch("/api/iv/ocr", {
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
      const poke = result.pokemon ?? { pokemon_name: "", form: "", pokemon_id: 0 };
      const onSave = async (c: IVCandidate) => {
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
      };
      resultsContainer.appendChild(renderCandidates(result.candidates ?? [], result.definitive, onSave));
    } catch {
      status.textContent = IV.ocrFailed;
    }
  });

  return wrap;
}

function buildBoxTab(): HTMLElement {
  if (!IV_CTX.loggedIn) {
    const p = document.createElement("p");
    p.className = "iv-hint";
    p.textContent = IV.boxHint;
    return p;
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
        const p = document.createElement("p");
        p.className = "iv-hint";
        p.textContent = IV.boxEmpty;
        list.appendChild(p);
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
    app.innerHTML = `<p class="iv-hint">${IV.commonFailed}</p>`;
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
