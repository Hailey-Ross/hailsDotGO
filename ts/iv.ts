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
  cp_source: "text" | "arc-level" | "none";
  hp: number;
  raw_dust: number;
  normalised_dust: number;
  pokemon_name: string;
  name_source: "footer" | "mega" | "card" | "fulltext" | "candy" | "";
  appraisal_bars: number;
  is_hundo: boolean;
  is_lucky: boolean;
  is_shadow: boolean;
  is_purified: boolean;
  raw_cp: string;
  arc_level: number;
}

interface CalcResponse {
  candidates: IVCandidate[];
  count: number;
  definitive: boolean;
  pokemon: { pokemon_name: string; form: string; pokemon_id: number };
  extracted?: OCRExtracted;
  best_buddy_assumed?: boolean;
  arc_rescue?: boolean;
  iv_summary?: { min_pct: number; max_pct: number };
  truncated_from?: number;
  species_candidates?: string[];
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

// buildResultNotes renders the scan-quality hints the server attaches to a
// result set (Best Buddy assumption, arc rescue, truncation, IV range).
function buildResultNotes(result: CalcResponse): HTMLElement | null {
  const notes: string[] = [];
  if (result.best_buddy_assumed) notes.push(IV.noteBestBuddy);
  if (result.arc_rescue) notes.push(IV.noteArcRescue);
  if (result.truncated_from) {
    notes.push(
      IV.noteTruncated
        .replace("{shown}", String(result.candidates?.length ?? 0))
        .replace("{n}", String(result.truncated_from))
    );
  }
  if (result.iv_summary && !result.definitive && (result.count ?? 0) > 1) {
    notes.push(
      IV.noteIVRange
        .replace("{min}", result.iv_summary.min_pct.toFixed(1))
        .replace("{max}", result.iv_summary.max_pct.toFixed(1))
    );
  }
  if (notes.length === 0) return null;
  const wrap = document.createElement("div");
  wrap.className = "iv-result-notes";
  notes.forEach((n) => {
    const p = document.createElement("p");
    p.className = "iv-result-note";
    p.textContent = n;
    wrap.appendChild(p);
  });
  return wrap;
}

function renderCandidates(
  candidates: IVCandidate[],
  definitive: boolean,
  onSave?: (c: IVCandidate) => void,
  totalCount?: number
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
    : IV.resultCount.replace("{n}", String(totalCount ?? candidates.length));
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

  // Pokémon Status -- sent as explicit flags; the server maps the displayed
  // dust cost to the right level bracket (lucky 0.5x, shadow 1.2x,
  // purified 0.9x of the base tier). The user enters dust EXACTLY as shown.
  const statusLabel = document.createElement("label");
  statusLabel.className = "form-label";
  statusLabel.textContent = IV.fieldStatus;
  wrap.appendChild(statusLabel);
  const statusRow = document.createElement("div");
  statusRow.className = "radio-row";
  let selectedStatus = "normal";
  const statusOptions: { v: string; l: string }[] = [
    { v: "normal",   l: IV.statusNormal },
    { v: "shadow",   l: IV.statusShadow },
    { v: "purified", l: IV.statusPurified },
    { v: "lucky",    l: IV.statusLucky },
  ];
  statusOptions.forEach(({ v, l }) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "radio-btn" + (v === "normal" ? " active" : "");
    btn.textContent = l;
    btn.dataset.value = v;
    btn.addEventListener("click", () => {
      selectedStatus = v;
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
          dust_cost: dust,
          trainer_level: trainerLevel,
          top_stat: selectedTopStat,
          appraisal_bars: parseInt(starsSel.value, 10),
          is_lucky: selectedStatus === "lucky",
          is_shadow: selectedStatus === "shadow",
          is_purified: selectedStatus === "purified",
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
                // The status was already chosen to read the dust cost. Carry it
                // into the box too: the raids page scores a shadow at +20% attack,
                // which is often what decides whether it is worth bringing.
                is_shadow: selectedStatus === "shadow",
                is_purified: selectedStatus === "purified",
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

      const notes = buildResultNotes(result);
      if (notes) resultsContainer.appendChild(notes);
      resultsContainer.appendChild(
        renderCandidates(result.candidates ?? [], result.definitive, onSave, result.count)
      );
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
  if (ext.is_lucky)    add(IV.statusLucky,    "badge-lucky");
  if (ext.is_shadow)   add(IV.statusShadow,   "badge-shadow");
  if (ext.is_purified) add(IV.statusPurified, "badge-purified");
  if (ext.cp_source === "arc-level") add(IV.badgeArcScan, "badge-arc");
  if (ext.arc_level > 0) add(IV.badgeArcLevel.replace("{lvl}", String(ext.arc_level)), "badge-arc");
  if (ext.name_source) add("Name: " + ext.name_source, "badge-source");
  return row;
}

function buildExtractedCard(
  ext: OCRExtracted,
  onRecalc: (cp: number, hp: number, dust: number, stars: number, name: string) => void,
  speciesCandidates?: string[]
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

  // Candy-line disambiguation left several plausible species: offer a picker
  // that fills the name field.
  if (speciesCandidates && speciesCandidates.length > 1) {
    const row = document.createElement("label");
    row.className = "ocr-field-row";
    const lbl = document.createElement("span");
    lbl.className = "ocr-field-label";
    lbl.textContent = IV.fieldSpeciesPick;
    const sel = document.createElement("select");
    sel.className = "form-select ocr-field-input";
    speciesCandidates.forEach((name) => {
      const o = document.createElement("option");
      o.value = name;
      o.textContent = name;
      sel.appendChild(o);
    });
    sel.value = ext.pokemon_name;
    sel.addEventListener("change", () => {
      nameInp.value = sel.value;
    });
    row.appendChild(lbl);
    row.appendChild(sel);
    card.appendChild(row);
  }

  const cpInp    = field("CP",         ext.cp > 0 ? ext.cp : "");
  const hpInp    = field("HP",         ext.hp > 0 ? ext.hp : "");
  // Dust is entered exactly as shown on screen; the server resolves the
  // lucky/shadow/purified discount to the base tier.
  const dustInp  = field(
    ext.normalised_dust > 0 && ext.normalised_dust !== ext.raw_dust
      ? `Dust (base: ${ext.normalised_dust})`
      : "Dust",
    ext.raw_dust > 0 ? ext.raw_dust : ext.normalised_dust > 0 ? ext.normalised_dust : ""
  );
  const starsInp = field("Stars (0-3, -1=unknown)", ext.appraisal_bars);

  // CP unreadable and ambiguous: tell the user how to pin the exact spread.
  if (ext.cp_source === "arc-level" && ext.cp <= 0) {
    const hint = document.createElement("p");
    hint.className = "ocr-cp-hint";
    hint.textContent = IV.noteEnterCP;
    card.appendChild(hint);
  }

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
  let lastExt: OCRExtracted | null = null;

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
      // Positive status flags from the scan carry over; unknown flags stay
      // absent so the server keeps all dust interpretations in play.
      if (lastExt?.is_lucky) body.is_lucky = true;
      if (lastExt?.is_shadow) body.is_shadow = true;
      if (lastExt?.is_purified) body.is_purified = true;
      const resp = await fetch("/api/iv/calculate", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify(body),
      });
      const result: CalcResponse = await resp.json();
      lastPoke = result.pokemon ?? lastPoke;
      lastCandidates = result.candidates ?? [];
      const notes = buildResultNotes(result);
      if (notes) resultsContainer.appendChild(notes);
      resultsContainer.appendChild(
        renderCandidates(lastCandidates, result.definitive, makeSaveHandler(), result.count)
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
          // From the scan rather than a radio button on this tab, so it is only
          // asserted when the screenshot actually showed it.
          is_shadow: lastExt?.is_shadow ?? null,
          is_purified: lastExt?.is_purified ?? null,
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
      lastExt = result.extracted ?? null;

      if (result.extracted) {
        extractedContainer.appendChild(
          buildExtractedCard(
            result.extracted,
            (cp, hp, dust, stars, name) => runRecalc(cp, hp, dust, stars, name),
            result.species_candidates
          )
        );
      }

      const notes = buildResultNotes(result);
      if (notes) resultsContainer.appendChild(notes);
      resultsContainer.appendChild(
        renderCandidates(lastCandidates, result.definitive, makeSaveHandler(), result.count)
      );
    } catch {
      status.textContent = IV.ocrFailed;
    }
  });

  return wrap;
}

// The box moved to its own page at /box, in the sidebar. It is reachable
// without running a calculation first, and it is what the raids page reads
// when it scores your Pokemon against the boss you have open.


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

  ];

  app.innerHTML = "";
  app.appendChild(buildTabs(tabs, "manual"));
}

main();
