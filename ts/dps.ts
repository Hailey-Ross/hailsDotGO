import { loadGameData, typeEffectiveness, pokemonByName, pokeSprite, cpForLevel, cpmFromCP, pokeName } from "./shared/gamedata";
import { trueDPS, estimateTDO, fastDPS, fastEPS } from "./shared/damage";
import { buildTabs } from "./shared/tabs";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import { fetchSpeciesData } from "./shared/pokedex";
import { createPicker, pokemonEntries } from "./shared/picker";
import type { PickerEntry } from "./shared/picker";
import type { GameData, PokemonStat, FastMove, ChargedMove } from "./shared/types";

declare const JSC: Record<string, string>;
declare const DP: Record<string, string>;
declare const DPS_CTX: { loggedIn: boolean };

const LS_CALC_TARGET = "dps_calc_target";
const LS_CMP_TARGET  = "dps_cmp_target";

const app = document.getElementById("dps-app")!;

function buildMoveSelect(
  options: { value: string; label: string; type?: string }[],
  placeholder: string
): HTMLSelectElement {
  const sel = document.createElement("select");
  sel.className = "move-select";
  const blank = document.createElement("option");
  blank.value = ""; blank.textContent = placeholder;
  blank.disabled = true; blank.selected = true;
  sel.appendChild(blank);
  for (const opt of options) {
    const o = document.createElement("option");
    o.value = opt.value;
    o.textContent = opt.type ? `[${opt.type}] ${opt.label}` : opt.label;
    sel.appendChild(o);
  }
  return sel;
}

function cpWidget(data: GameData): {
  label: HTMLLabelElement;
  input: HTMLInputElement;
  getCPM: (poke: PokemonStat | null, atkIV: number, defIV: number, staIV: number) => number;
} {
  const label = document.createElement("label");
  label.className = "level-label";
  label.textContent = `${JSC.cp} `;
  const input = document.createElement("input");
  input.type = "number";
  input.className = "level-input";
  input.min = "10";
  input.max = "99999";
  input.value = "3000";
  input.placeholder = JSC.cp;
  label.appendChild(input);

  function getCPM(poke: PokemonStat | null, atkIV: number, defIV: number, staIV: number): number {
    const targetCP = Math.max(10, Number(input.value) || 3000);
    if (!poke) return 0.7903;
    return cpmFromCP(data, poke, atkIV, defIV, staIV, targetCP);
  }

  return { label, input, getCPM };
}

function cardSpriteImg(src: string, alt: string, cls: string): HTMLImageElement {
  const img = document.createElement("img");
  img.src = src;
  img.alt = alt;
  img.className = cls;
  img.loading = "lazy"; img.decoding = "async";
  img.addEventListener("error", () => { img.style.visibility = "hidden"; });
  return img;
}

function buildCalcPanel(data: GameData, allEntries: PickerEntry[]): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");
    wrap.className = "dps-calc-panel";

    const form = document.createElement("div");
    form.className = "calc-form";

    const fastSel = buildMoveSelect(
      data.fastMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      DP.selectFast
    );
    const chargedSel = buildMoveSelect(
      data.chargedMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      DP.selectCharged
    );
    const { label: cpLbl, input: cpInput, getCPM } = cpWidget(data);

    const attackerPicker = createPicker({
      entries: allEntries,
      placeholder: DP.searchPokemonEg,
      onSelect: (e) => {
        const poke = e.data as PokemonStat;
        cpInput.value = String(cpForLevel(poke, 15, 15, 15, 0.7903));
      },
    });

    const calcBtn = document.createElement("button");
    calcBtn.className = "btn-primary"; calcBtn.textContent = DP.calculate;

    form.appendChild(attackerPicker.root);
    form.appendChild(fastSel);
    form.appendChild(chargedSel);
    form.appendChild(cpLbl);
    form.appendChild(calcBtn);


    const targetBlock = document.createElement("div");
    targetBlock.className = "calc-form";
    targetBlock.style.marginTop = "0.75rem";

    const targetHeader = document.createElement("span");
    targetHeader.className = "filter-label";
    targetHeader.style.cssText = "width:100%;margin-bottom:0.25rem;";
    targetHeader.textContent = DP.vsTargetOptional;
    targetBlock.appendChild(targetHeader);

    let calcTargetTypes: string[] = [];

    const targetPicker = createPicker({
      entries: allEntries,
      placeholder: DP.targetPokemonEg,
      onSelect: (e) => {
        calcTargetTypes = e.types;
        if (DPS_CTX.loggedIn) localStorage.setItem(LS_CALC_TARGET, e.name);
      },
      onClear: () => {
        calcTargetTypes = [];
        if (DPS_CTX.loggedIn) localStorage.removeItem(LS_CALC_TARGET);
      },
    });
    if (DPS_CTX.loggedIn) {
      const saved = localStorage.getItem(LS_CALC_TARGET);
      if (saved) targetPicker.selectByName(saved);
    }
    targetBlock.appendChild(targetPicker.root);


    const resultArea = document.createElement("div");
    resultArea.className = "result-area";

    calcBtn.addEventListener("click", () => {
      const poke = attackerPicker.getSelected()?.data as PokemonStat | undefined;
      const fast = data.fastMoves!.find((m) => m.name === fastSel.value);
      const charged = data.chargedMoves!.find((m) => m.name === chargedSel.value);

      if (!poke) {
        resultArea.innerHTML = `<p class="error-text">${DP.errSelectPokemon}</p>`;
        return;
      }
      if (!fast || !charged) {
        resultArea.innerHTML = `<p class="error-text">${DP.errSelectMovesLong}</p>`;
        return;
      }

      const cpMult = getCPM(poke, 15, 15, 15);
      const atkStat = (poke.base_attack + 15) * cpMult;
      const defStat = 150;
      const hasTarget = calcTargetTypes.length > 0;
      const fEff = hasTarget ? typeEffectiveness(data, fast.type, calcTargetTypes) : 1;
      const cEff = hasTarget ? typeEffectiveness(data, charged.type, calcTargetTypes) : 1;

      const card = document.createElement("div");
      card.className = "result-card";

      const cardHeader = document.createElement("div");
      cardHeader.className = "result-poke-header";
      const cardSprite = cardSpriteImg(pokeSprite(poke.pokemon_id), poke.pokemon_name, "result-poke-sprite");
      const cardTitle = document.createElement("h3");
      cardTitle.style.marginBottom = "0";
      cardTitle.textContent = pokeName(data, poke.pokemon_name);
      if (hasTarget) {
        const vsSpan = document.createElement("span");
        vsSpan.style.cssText = "font-size:0.78rem;color:var(--text-2);font-weight:400;margin-left:0.4rem;";
        vsSpan.textContent = DP.vsName.replace("{name}", targetPicker.getSelected()?.label ?? "");
        cardTitle.appendChild(vsSpan);
      }
      cardHeader.appendChild(cardSprite);
      cardHeader.appendChild(cardTitle);
      card.appendChild(cardHeader);

      const genusEl = document.createElement("span");
      genusEl.className = "poke-genus";
      card.appendChild(genusEl);

      const badgeEl = document.createElement("span");
      badgeEl.style.display = "none";
      card.appendChild(badgeEl);

      const flavorP = document.createElement("p");
      flavorP.className = "poke-flavor";
      flavorP.style.display = "none";
      card.appendChild(flavorP);

      fetchSpeciesData(poke.pokemon_name).then(d => {
        if (d.genus)  { genusEl.textContent = JSC.theGenus.replace("{genus}", d.genus); }
        if (d.isLegendary || d.isMythical) {
          badgeEl.textContent  = d.isMythical ? JSC.mythical : JSC.legendary;
          badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
          badgeEl.style.display = "";
        }
        if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
      });

      function effLabel(eff: number): HTMLElement | null {
        if (eff === 1) return null;
        const s = document.createElement("span");
        s.style.cssText = `color:${eff > 1 ? "var(--green)" : "var(--red)"};font-weight:700;font-size:0.78rem;margin-left:4px;`;
        s.textContent = `${eff.toFixed(2)}×`;
        return s;
      }

      const rows: [string, () => string | HTMLElement, boolean][] = [
        [JSC.fastMove, () => {
          const w = document.createElement("span");
          w.style.cssText = "display:flex;align-items:center;gap:6px;";
          w.appendChild(typeBadge(fast.type)); w.append(fast.name);
          const lbl = effLabel(fEff); if (lbl) w.appendChild(lbl);
          return w;
        }, false],
        [JSC.chargedMove, () => {
          const w = document.createElement("span");
          w.style.cssText = "display:flex;align-items:center;gap:6px;";
          w.appendChild(typeBadge(charged.type)); w.append(charged.name);
          const lbl = effLabel(cEff); if (lbl) w.appendChild(lbl);
          return w;
        }, false],
        [DP.cycleDps, () => trueDPS(fast, charged, atkStat, defStat, fEff, cEff).toFixed(2), true],
        [DP.fastDps,  () => fastDPS(fast, atkStat, defStat, fEff).toFixed(2), false],
        [DP.fastEps,  () => fastEPS(fast).toFixed(2), false],
      ];

      for (const [lbl, getVal, highlight] of rows) {
        const row = document.createElement("div");
        row.className = `result-row${highlight ? " highlight" : ""}`;
        const l = document.createElement("span"); l.className = "label"; l.textContent = lbl;
        const v = document.createElement("span"); v.className = "value";
        const val = getVal();
        typeof val === "string" ? (v.textContent = val) : v.appendChild(val);
        row.appendChild(l); row.appendChild(v);
        card.appendChild(row);
      }

      resultArea.innerHTML = "";
      resultArea.appendChild(card);
    });

    wrap.appendChild(form);
    wrap.appendChild(targetBlock);
    wrap.appendChild(resultArea);
    return wrap;
  };
}

interface Attacker {
  id: number;
  poke: PokemonStat;
  fast: FastMove;
  charged: ChargedMove;
  cp: number;
}

// Current raid bosses first (with tier tags), then every other Pokemon.
function targetEntries(data: GameData, all: PickerEntry[]): PickerEntry[] {
  const out: PickerEntry[] = [];
  const seen = new Set<string>();
  for (const [tier, bosses] of Object.entries(data.raids ?? {}).sort(([a], [b]) => Number(b) - Number(a))) {
    const tag = tier === "6" ? "Mega" : `T${tier}`;
    for (const boss of bosses) {
      if (!boss.types?.length || seen.has(boss.pokemon_name)) continue;
      seen.add(boss.pokemon_name);
      const base = pokemonByName(data, boss.pokemon_name);
      out.push({
        key: `raid:${boss.pokemon_name}`,
        name: boss.pokemon_name,
        label: pokeName(data, boss.pokemon_name),
        sprite: boss.image_url ?? (base ? pokeSprite(base.pokemon_id) : null),
        types: boss.types,
        tag,
        group: 0,
        data: boss,
      });
    }
  }
  for (const p of all) {
    if (!seen.has(p.name)) out.push(p);
  }
  return out;
}

function buildComparePanel(data: GameData, allEntries: PickerEntry[]): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");
    wrap.className = "dps-compare-panel";
    let targetTypes: string[] = [];
    let attackers: Attacker[] = [];
    let nextId = 0;


    const targetBlock = document.createElement("div");
    targetBlock.className = "calc-form";

    const targetHeader = document.createElement("span");
    targetHeader.className = "filter-label";
    targetHeader.style.cssText = "width:100%;margin-bottom:0.25rem;";
    targetHeader.textContent = DP.targetOptional;
    targetBlock.appendChild(targetHeader);

    const allTypes = Object.keys(TYPE_COLORS).sort();
    const makeTypeSel = (placeholder: string) => {
      const sel = document.createElement("select");
      sel.className = "move-select";
      const none = document.createElement("option"); none.value = ""; none.textContent = placeholder;
      sel.appendChild(none);
      allTypes.forEach((t) => { const o = document.createElement("option"); o.value = t; o.textContent = t; sel.appendChild(o); });
      return sel;
    };
    const type1Sel = makeTypeSel(DP.type1);
    const type2Sel = makeTypeSel(DP.type2);

    const manualTypesRow = document.createElement("div");
    manualTypesRow.style.cssText = "display:flex;gap:0.4rem;flex-wrap:wrap;align-items:center;width:100%;";
    manualTypesRow.hidden = true;

    function updateManualRow(show: boolean) {
      manualTypesRow.innerHTML = "";
      if (show && targetTypes.length) {
        const vs = document.createElement("span"); vs.className = "filter-label"; vs.textContent = JSC.vs;
        manualTypesRow.appendChild(vs);
        targetTypes.forEach((t) => manualTypesRow.appendChild(typeBadge(t)));
        manualTypesRow.hidden = false;
      } else {
        manualTypesRow.hidden = true;
      }
    }

    const targetPicker = createPicker({
      entries: targetEntries(data, allEntries),
      placeholder: DP.searchBoss,
      showOnFocus: true,
      onSelect: (e) => {
        targetTypes = e.types;
        type1Sel.value = targetTypes[0] ?? "";
        type2Sel.value = targetTypes[1] ?? "";
        updateManualRow(false);
        if (DPS_CTX.loggedIn) localStorage.setItem(LS_CMP_TARGET, e.name);
        renderTable();
      },
      onClear: () => {
        type1Sel.value = "";
        type2Sel.value = "";
        targetTypes = [];
        updateManualRow(false);
        if (DPS_CTX.loggedIn) localStorage.removeItem(LS_CMP_TARGET);
        renderTable();
      },
    });
    if (DPS_CTX.loggedIn) {
      const saved = localStorage.getItem(LS_CMP_TARGET);
      if (saved) targetPicker.selectByName(saved);
    }
    targetBlock.appendChild(targetPicker.root);

    const advancedToggle = document.createElement("button");
    advancedToggle.className = "picker-advanced-toggle";
    advancedToggle.textContent = DP.customTypes;

    const advancedRow = document.createElement("div");
    advancedRow.className = "picker-advanced-row";
    advancedRow.hidden = true;
    advancedRow.appendChild(type1Sel);
    advancedRow.appendChild(type2Sel);

    advancedToggle.addEventListener("click", () => {
      advancedRow.hidden = !advancedRow.hidden;
    });

    function onManualType() {
      targetPicker.clear(false);
      targetTypes = [type1Sel.value, type2Sel.value].filter(Boolean);
      updateManualRow(true);
      renderTable();
    }
    type1Sel.addEventListener("change", onManualType);
    type2Sel.addEventListener("change", onManualType);

    targetBlock.appendChild(advancedToggle);
    targetBlock.appendChild(advancedRow);
    targetBlock.appendChild(manualTypesRow);

    wrap.appendChild(targetBlock);


    const attackerForm = document.createElement("div");
    attackerForm.className = "calc-form";

    const fastSel = buildMoveSelect(
      data.fastMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      JSC.fastMove
    );
    const chargedSel = buildMoveSelect(
      data.chargedMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      JSC.chargedMove
    );
    const { label: cpLbl, input: cpInput } = cpWidget(data);

    const attackerPicker = createPicker({
      entries: allEntries,
      placeholder: DP.searchPokemonEg,
      onSelect: (e) => {
        const poke = e.data as PokemonStat;
        cpInput.value = String(cpForLevel(poke, 15, 15, 15, 0.7903));
      },
    });

    const addBtn = document.createElement("button");
    addBtn.className = "btn-primary"; addBtn.textContent = DP.add;

    const errText = document.createElement("p");
    errText.className = "error-text"; errText.style.display = "none";

    attackerForm.appendChild(attackerPicker.root);
    attackerForm.appendChild(fastSel);
    attackerForm.appendChild(chargedSel);
    attackerForm.appendChild(cpLbl);
    attackerForm.appendChild(addBtn);
    attackerForm.appendChild(errText);
    wrap.appendChild(attackerForm);


    const tableArea = document.createElement("div");
    tableArea.className = "result-area compare-results";
    wrap.appendChild(tableArea);

    function effSpan(eff: number): HTMLElement {
      const s = document.createElement("span");
      s.style.cssText = `color:${eff > 1 ? "var(--green)" : "var(--red)"};font-weight:700;font-size:0.78rem;`;
      s.textContent = ` ${eff.toFixed(2)}×`;
      return s;
    }

    function renderTable() {
      tableArea.innerHTML = "";

      if (attackers.length === 0) {
        const hint = document.createElement("p");
        hint.className = "empty-state";
        hint.textContent = DP.addHint;
        tableArea.appendChild(hint);
        return;
      }

      const hasTarget = targetTypes.length > 0;

      const rows = attackers.map((a) => {
        const cpMult = cpmFromCP(data, a.poke, 15, 15, 15, a.cp);
        const atkStat = (a.poke.base_attack + 15) * cpMult;
        const defStat = 150;
        const fEff = hasTarget ? typeEffectiveness(data, a.fast.type, targetTypes) : 1;
        const cEff = hasTarget ? typeEffectiveness(data, a.charged.type, targetTypes) : 1;
        return {
          a, fEff, cEff,
          dps: trueDPS(a.fast, a.charged, atkStat, defStat, fEff, cEff),
          tdo: estimateTDO(a.poke, a.fast, a.charged, atkStat, defStat, fEff, cEff, cpMult),
        };
      }).sort((x, y) => y.dps - x.dps);

      function removeAttacker(id: number) {
        attackers = attackers.filter((x) => x.id !== id);
        renderTable();
      }

      const tableWrap = document.createElement("div");
      tableWrap.className = "table-wrap";

      const table = document.createElement("table");
      table.className = "counter-table";
      table.innerHTML = `<thead><tr>
        <th>#</th><th>${JSC.pokemon}</th><th>${JSC.fastMove}</th><th>${JSC.chargedMove}</th>
        <th>${JSC.cp}</th><th>DPS</th><th>TDO</th><th></th>
      </tr></thead>`;

      const tbody = document.createElement("tbody");

      for (const { a, dps, tdo, fEff, cEff } of rows) {
        const tr = document.createElement("tr");

        const rankTd = document.createElement("td");
        rankTd.textContent = String(rows.indexOf(rows.find((r) => r.a.id === a.id)!) + 1);
        if (rows[0].a.id === a.id) rankTd.className = "rank-s";

        const pokeTd = document.createElement("td");
        pokeTd.style.whiteSpace = "nowrap";
        pokeTd.appendChild(cardSpriteImg(pokeSprite(a.poke.pokemon_id), a.poke.pokemon_name, "poke-sprite"));
        pokeTd.append(` ${pokeName(data, a.poke.pokemon_name)}`);

        const fastTd = document.createElement("td");
        fastTd.style.whiteSpace = "nowrap";
        fastTd.appendChild(typeBadge(a.fast.type));
        fastTd.append(` ${a.fast.name}`);
        if (hasTarget && fEff !== 1) fastTd.appendChild(effSpan(fEff));

        const chargedTd = document.createElement("td");
        chargedTd.style.whiteSpace = "nowrap";
        chargedTd.appendChild(typeBadge(a.charged.type));
        chargedTd.append(` ${a.charged.name}`);
        if (hasTarget && cEff !== 1) chargedTd.appendChild(effSpan(cEff));

        const cpTd = document.createElement("td"); cpTd.textContent = a.cp.toLocaleString();
        const dpsTd = document.createElement("td"); dpsTd.textContent = dps.toFixed(2);
        if (rows[0].a.id === a.id) dpsTd.className = "rank-s";
        const tdoTd = document.createElement("td"); tdoTd.textContent = tdo.toFixed(0);

        const removeTd = document.createElement("td");
        const removeBtn = document.createElement("button");
        removeBtn.textContent = "×";
        removeBtn.style.cssText = "background:none;border:none;color:var(--text-2);cursor:pointer;font-size:1.1rem;padding:0 0.3rem;line-height:1;";
        removeBtn.addEventListener("click", () => removeAttacker(a.id));
        removeTd.appendChild(removeBtn);

        tr.append(rankTd, pokeTd, fastTd, chargedTd, cpTd, dpsTd, tdoTd, removeTd);
        tbody.appendChild(tr);
      }

      if (attackers.length > 1) {
        const clearTr = document.createElement("tr");
        const clearTd = document.createElement("td");
        clearTd.colSpan = 8;
        clearTd.style.cssText = "text-align:right;padding:0.5rem 0.9rem;";
        const clearBtn = document.createElement("button");
        clearBtn.textContent = DP.clearAll;
        clearBtn.style.cssText = "background:none;border:none;color:var(--text-2);cursor:pointer;font-size:0.8rem;text-decoration:underline;font-family:var(--font);";
        clearBtn.addEventListener("click", () => { attackers = []; renderTable(); });
        clearTd.appendChild(clearBtn);
        clearTr.appendChild(clearTd);
        tbody.appendChild(clearTr);
      }

      table.appendChild(tbody);
      tableWrap.appendChild(table);
      tableArea.appendChild(tableWrap);

      const cards = document.createElement("div");
      cards.className = "compare-cards";

      rows.forEach(({ a, dps, tdo, fEff, cEff }, idx) => {
        const card = document.createElement("div");
        card.className = `attacker-card${idx === 0 ? " rank-1" : ""}`;

        const head = document.createElement("div");
        head.className = "attacker-card-head";

        const rank = document.createElement("span");
        rank.className = "attacker-card-rank";
        rank.textContent = `#${idx + 1}`;
        head.appendChild(rank);

        head.appendChild(cardSpriteImg(pokeSprite(a.poke.pokemon_id), a.poke.pokemon_name, "attacker-card-sprite"));

        const idBlock = document.createElement("div");
        idBlock.className = "attacker-card-id";
        const nameEl = document.createElement("span");
        nameEl.className = "attacker-card-name";
        nameEl.textContent = pokeName(data, a.poke.pokemon_name);
        const cpEl = document.createElement("span");
        cpEl.className = "attacker-card-cp";
        cpEl.textContent = `${JSC.cp} ${a.cp.toLocaleString()}`;
        idBlock.appendChild(nameEl);
        idBlock.appendChild(cpEl);
        head.appendChild(idBlock);

        const removeBtn = document.createElement("button");
        removeBtn.className = "attacker-card-remove";
        removeBtn.textContent = "×";
        removeBtn.setAttribute("aria-label", JSC.remove);
        removeBtn.addEventListener("click", () => removeAttacker(a.id));
        head.appendChild(removeBtn);

        card.appendChild(head);

        const moves = document.createElement("div");
        moves.className = "attacker-card-moves";
        const moveRow = (type: string, name: string, eff: number) => {
          const row = document.createElement("div");
          row.className = "attacker-move";
          row.appendChild(typeBadge(type));
          row.append(name);
          if (hasTarget && eff !== 1) row.appendChild(effSpan(eff));
          return row;
        };
        moves.appendChild(moveRow(a.fast.type, a.fast.name, fEff));
        moves.appendChild(moveRow(a.charged.type, a.charged.name, cEff));
        card.appendChild(moves);

        const stats = document.createElement("div");
        stats.className = "attacker-card-stats";
        const stat = (value: string, label: string, extraCls = "") => {
          const s = document.createElement("div");
          s.className = "attacker-stat";
          const v = document.createElement("span");
          v.className = `attacker-stat-value${extraCls ? ` ${extraCls}` : ""}`;
          v.textContent = value;
          const l = document.createElement("span");
          l.className = "attacker-stat-label";
          l.textContent = label;
          s.appendChild(v); s.appendChild(l);
          return s;
        };
        stats.appendChild(stat(dps.toFixed(2), "DPS", "attacker-stat-dps"));
        stats.appendChild(stat(tdo.toFixed(0), "TDO"));
        card.appendChild(stats);

        cards.appendChild(card);
      });

      if (attackers.length > 1) {
        const clearBtn = document.createElement("button");
        clearBtn.className = "compare-clear-btn";
        clearBtn.textContent = DP.clearAll;
        clearBtn.addEventListener("click", () => { attackers = []; renderTable(); });
        cards.appendChild(clearBtn);
      }

      tableArea.appendChild(cards);
    }

    addBtn.addEventListener("click", () => {
      errText.style.display = "none";
      const poke = attackerPicker.getSelected()?.data as PokemonStat | undefined;
      const fast = data.fastMoves!.find((m) => m.name === fastSel.value);
      const charged = data.chargedMoves!.find((m) => m.name === chargedSel.value);
      const cp = Math.max(10, Number(cpInput.value) || 3000);

      if (!poke) { errText.textContent = DP.errSelectPokemon; errText.style.display = ""; return; }
      if (!fast || !charged) { errText.textContent = DP.errSelectMoves; errText.style.display = ""; return; }

      attackers.push({ id: nextId++, poke, fast, charged, cp });
      renderTable();
      attackerPicker.clear(false);
      attackerPicker.focus();
    });

    attackerPicker.input.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !attackerPicker.isDropdownOpen() && attackerPicker.getSelected()) {
        addBtn.click();
      }
    });

    renderTable();
    return wrap;
  };
}

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";

    if (!data.pokemon || !data.fastMoves || !data.chargedMoves) {
      app.innerHTML = `<div class="error-state">${JSC.gameDataUnavailable}</div>`;
      return;
    }

    const allEntries = pokemonEntries(data);

    const tabs = buildTabs([
      { id: "calc",    label: `⚡ ${DP.tabCalc}`,    render: buildCalcPanel(data, allEntries) },
      { id: "compare", label: `📊 ${DP.tabCompare}`, render: buildComparePanel(data, allEntries) },
    ]);
    app.appendChild(tabs);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
