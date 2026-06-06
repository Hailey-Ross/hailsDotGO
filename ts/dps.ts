import { loadGameData, typeEffectiveness, pokemonByName, pokeSprite, cpForLevel, cpmFromCP, pokeName } from "./shared/gamedata";
import { trueDPS, estimateTDO, fastDPS, fastEPS } from "./shared/damage";
import { buildTabs } from "./shared/tabs";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, PokemonStat, FastMove, ChargedMove } from "./shared/types";

let pokeDatalist: HTMLDataListElement | null = null;

function getPokeDatalist(data: GameData): HTMLDataListElement {
  if (pokeDatalist) return pokeDatalist;
  pokeDatalist = document.createElement("datalist");
  pokeDatalist.id = "dps-poke-datalist";
  const seen = new Set<string>();
  for (const p of data.pokemon ?? []) {
    if (seen.has(p.pokemon_name)) continue;
    seen.add(p.pokemon_name);
    const opt = document.createElement("option");
    opt.value = p.pokemon_name;
    pokeDatalist.appendChild(opt);
  }
  document.body.appendChild(pokeDatalist);
  return pokeDatalist;
}

function typesForTarget(data: GameData, name: string): string[] {
  return data.pokemonTypes?.[name] ?? [];
}

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
  label.textContent = "CP ";
  const input = document.createElement("input");
  input.type = "number";
  input.className = "level-input";
  input.min = "10";
  input.max = "99999";
  input.value = "3000";
  input.placeholder = "CP";
  label.appendChild(input);

  function getCPM(poke: PokemonStat | null, atkIV: number, defIV: number, staIV: number): number {
    const targetCP = Math.max(10, Number(input.value) || 3000);
    if (!poke) return 0.7903;
    return cpmFromCP(data, poke, atkIV, defIV, staIV, targetCP);
  }

  return { label, input, getCPM };
}

// ── Calculator tab (single Pokémon) ──────────────────────────

function buildCalcPanel(data: GameData): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");

    const form = document.createElement("div");
    form.className = "calc-form";

    const pokeInput = document.createElement("input");
    pokeInput.type = "text"; pokeInput.className = "search-input";
    pokeInput.placeholder = "Pokémon name (e.g. Mewtwo)";

    const fastSel = buildMoveSelect(
      data.fastMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      "Select Fast Move"
    );
    const chargedSel = buildMoveSelect(
      data.chargedMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      "Select Charged Move"
    );
    const { label: cpLbl, input: cpInput, getCPM } = cpWidget(data);

    const calcBtn = document.createElement("button");
    calcBtn.className = "btn-primary"; calcBtn.textContent = "Calculate";

    form.appendChild(pokeInput);
    form.appendChild(fastSel);
    form.appendChild(chargedSel);
    form.appendChild(cpLbl);
    form.appendChild(calcBtn);

    // ── Target section ─────────────────────────────────────────

    const targetBlock = document.createElement("div");
    targetBlock.className = "calc-form";
    targetBlock.style.marginTop = "0.75rem";

    const targetHeader = document.createElement("span");
    targetHeader.className = "filter-label";
    targetHeader.style.cssText = "width:100%;margin-bottom:0.25rem;";
    targetHeader.textContent = "vs. Target (optional)";
    targetBlock.appendChild(targetHeader);

    const dl = getPokeDatalist(data);
    const targetInput = document.createElement("input");
    targetInput.type = "text";
    targetInput.className = "search-input";
    targetInput.placeholder = "Target Pokémon (e.g. Machamp)";
    targetInput.setAttribute("list", dl.id);
    targetInput.setAttribute("autocomplete", "off");

    const targetTypesRow = document.createElement("div");
    targetTypesRow.style.cssText = "display:flex;gap:0.4rem;flex-wrap:wrap;align-items:center;width:100%;min-height:1.5rem;";

    let calcTargetTypes: string[] = [];

    function updateCalcTarget() {
      const name = targetInput.value.trim();
      calcTargetTypes = name ? typesForTarget(data, name) : [];
      targetTypesRow.innerHTML = "";
      if (calcTargetTypes.length) {
        calcTargetTypes.forEach((t) => targetTypesRow.appendChild(typeBadge(t)));
      }
    }

    targetInput.addEventListener("change", updateCalcTarget);
    targetInput.addEventListener("input", updateCalcTarget);

    const clearTargetBtn = document.createElement("button");
    clearTargetBtn.textContent = "Clear";
    clearTargetBtn.style.cssText = "background:none;border:1px solid var(--border);color:var(--text-2);cursor:pointer;font-size:0.78rem;padding:0.2rem 0.6rem;border-radius:4px;font-family:var(--font);";
    clearTargetBtn.addEventListener("click", () => {
      targetInput.value = "";
      calcTargetTypes = [];
      targetTypesRow.innerHTML = "";
    });

    const targetRow = document.createElement("div");
    targetRow.style.cssText = "display:flex;gap:0.5rem;align-items:center;width:100%;";
    targetRow.appendChild(targetInput);
    targetRow.appendChild(clearTargetBtn);

    targetBlock.appendChild(targetRow);
    targetBlock.appendChild(targetTypesRow);

    // ── Result area ────────────────────────────────────────────

    const resultArea = document.createElement("div");
    resultArea.className = "result-area";

    // Auto-fill CP when Pokémon name is typed
    pokeInput.addEventListener("change", () => {
      const poke = pokemonByName(data, pokeInput.value.trim());
      if (poke) {
        const defaultCP = cpForLevel(poke, 15, 15, 15, 0.7903);
        cpInput.value = String(defaultCP);
      }
    });

    calcBtn.addEventListener("click", () => {
      const poke = pokemonByName(data, pokeInput.value.trim());
      const fast = data.fastMoves!.find((m) => m.name === fastSel.value);
      const charged = data.chargedMoves!.find((m) => m.name === chargedSel.value);

      if (!poke) {
        resultArea.innerHTML = `<p class="error-text">Pokémon not found. Check the spelling.</p>`;
        return;
      }
      if (!fast || !charged) {
        resultArea.innerHTML = `<p class="error-text">Select both a fast move and charged move.</p>`;
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
      const cardSprite = document.createElement("img");
      cardSprite.src = pokeSprite(poke.pokemon_id);
      cardSprite.alt = poke.pokemon_name;
      cardSprite.className = "result-poke-sprite";
      cardSprite.loading = "lazy"; cardSprite.decoding = "async";
      const cardTitle = document.createElement("h3");
      cardTitle.style.marginBottom = "0";
      cardTitle.textContent = pokeName(data, poke.pokemon_name);
      if (hasTarget) {
        const vsSpan = document.createElement("span");
        vsSpan.style.cssText = "font-size:0.78rem;color:var(--text-2);font-weight:400;margin-left:0.4rem;";
        vsSpan.textContent = `vs. ${targetInput.value.trim()}`;
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
        if (d.genus)  { genusEl.textContent = `The ${d.genus}`; }
        if (d.isLegendary || d.isMythical) {
          badgeEl.textContent  = d.isMythical ? "Mythical" : "Legendary";
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
        ["Fast Move", () => {
          const w = document.createElement("span");
          w.style.cssText = "display:flex;align-items:center;gap:6px;";
          w.appendChild(typeBadge(fast.type)); w.append(fast.name);
          const lbl = effLabel(fEff); if (lbl) w.appendChild(lbl);
          return w;
        }, false],
        ["Charged Move", () => {
          const w = document.createElement("span");
          w.style.cssText = "display:flex;align-items:center;gap:6px;";
          w.appendChild(typeBadge(charged.type)); w.append(charged.name);
          const lbl = effLabel(cEff); if (lbl) w.appendChild(lbl);
          return w;
        }, false],
        ["Cycle DPS", () => trueDPS(fast, charged, atkStat, defStat, fEff, cEff).toFixed(2), true],
        ["Fast DPS",  () => fastDPS(fast, atkStat, defStat, fEff).toFixed(2), false],
        ["Fast EPS",  () => fastEPS(fast).toFixed(2), false],
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

// ── Compare tab ───────────────────────────────────────────────

interface Attacker {
  id: number;
  poke: PokemonStat;
  fast: FastMove;
  charged: ChargedMove;
  cp: number;
}

function buildComparePanel(data: GameData): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");
    let targetTypes: string[] = [];
    let attackers: Attacker[] = [];
    let nextId = 0;

    // ── Target section ─────────────────────────────────────────

    const targetBlock = document.createElement("div");
    targetBlock.className = "calc-form";

    const targetHeader = document.createElement("span");
    targetHeader.className = "filter-label";
    targetHeader.style.cssText = "width:100%;margin-bottom:0.25rem;";
    targetHeader.textContent = "Target (optional)";
    targetBlock.appendChild(targetHeader);

    // Raid boss dropdown (primary), all Pokémon as fallback
    const raidSel = document.createElement("select");
    raidSel.className = "move-select";
    const noTarget = document.createElement("option");
    noTarget.value = ""; noTarget.textContent = "Select target...";
    raidSel.appendChild(noTarget);

    const raidBossNames = new Set<string>();

    if (data.raids) {
      const raidGroup = document.createElement("optgroup");
      raidGroup.label = "Current Raids";
      for (const [tier, bosses] of Object.entries(data.raids).sort(([a], [b]) => Number(b) - Number(a))) {
        const tierLabel = tier === "6" ? "Mega" : `T${tier}`;
        for (const boss of bosses) {
          if (boss.types?.length) {
            raidBossNames.add(boss.pokemon_name);
            const o = document.createElement("option");
            o.value = boss.types.join(",");
            o.textContent = `[${tierLabel}] ${pokeName(data, boss.pokemon_name)} (${boss.types.join("/")})`;
            raidGroup.appendChild(o);
          }
        }
      }
      if (raidGroup.children.length) raidSel.appendChild(raidGroup);
    }

    if (data.pokemon && data.pokemonTypes) {
      const allGroup = document.createElement("optgroup");
      allGroup.label = "All Pokémon";
      for (const p of data.pokemon) {
        if (raidBossNames.has(p.pokemon_name)) continue;
        const types = data.pokemonTypes[p.pokemon_name];
        if (types?.length) {
          const o = document.createElement("option");
          o.value = types.join(",");
          o.textContent = `${pokeName(data, p.pokemon_name)} (${types.join("/")})`;
          allGroup.appendChild(o);
        }
      }
      if (allGroup.children.length) raidSel.appendChild(allGroup);
    }
    targetBlock.appendChild(raidSel);

    // Pokémon name input (fallback)
    const orPokeSpan = document.createElement("span");
    orPokeSpan.className = "level-label"; orPokeSpan.textContent = "or Pokémon:";
    targetBlock.appendChild(orPokeSpan);

    const dl = getPokeDatalist(data);
    const targetPokeInput = document.createElement("input");
    targetPokeInput.type = "text";
    targetPokeInput.className = "search-input";
    targetPokeInput.placeholder = "Pokémon name (e.g. Machamp)";
    targetPokeInput.setAttribute("list", dl.id);
    targetPokeInput.setAttribute("autocomplete", "off");
    targetBlock.appendChild(targetPokeInput);

    // Manual type dropdowns
    const allTypes = Object.keys(TYPE_COLORS).sort();
    const makeTypeSel = (placeholder: string) => {
      const sel = document.createElement("select");
      sel.className = "move-select";
      const none = document.createElement("option"); none.value = ""; none.textContent = placeholder;
      sel.appendChild(none);
      allTypes.forEach((t) => { const o = document.createElement("option"); o.value = t; o.textContent = t; sel.appendChild(o); });
      return sel;
    };

    const orSpan = document.createElement("span");
    orSpan.className = "level-label"; orSpan.textContent = "or types:";
    const type1Sel = makeTypeSel("Type 1");
    const type2Sel = makeTypeSel("Type 2");
    targetBlock.appendChild(orSpan);
    targetBlock.appendChild(type1Sel);
    targetBlock.appendChild(type2Sel);

    const activeTypesRow = document.createElement("div");
    activeTypesRow.style.cssText = "display:flex;gap:0.4rem;flex-wrap:wrap;align-items:center;width:100%;min-height:1.5rem;";
    targetBlock.appendChild(activeTypesRow);

    function updateTarget() {
      const pokeName = targetPokeInput.value.trim();
      const pokeTypes = pokeName ? typesForTarget(data, pokeName) : [];
      if (pokeTypes.length) {
        targetTypes = pokeTypes;
        raidSel.value = "";
        type1Sel.value = targetTypes[0] ?? "";
        type2Sel.value = targetTypes[1] ?? "";
      } else if (raidSel.value) {
        targetTypes = raidSel.value.split(",").filter(Boolean);
        type1Sel.value = targetTypes[0] ?? "";
        type2Sel.value = targetTypes[1] ?? "";
      } else {
        targetTypes = [type1Sel.value, type2Sel.value].filter(Boolean);
      }
      activeTypesRow.innerHTML = "";
      if (targetTypes.length) {
        const vs = document.createElement("span"); vs.className = "filter-label"; vs.textContent = "vs";
        activeTypesRow.appendChild(vs);
        targetTypes.forEach((t) => activeTypesRow.appendChild(typeBadge(t)));
      }
      renderTable();
    }

    targetPokeInput.addEventListener("change", () => { raidSel.value = ""; type1Sel.value = ""; type2Sel.value = ""; updateTarget(); });
    targetPokeInput.addEventListener("input", () => {
      const pokeName = targetPokeInput.value.trim();
      if (!pokeName) { updateTarget(); return; }
      const types = typesForTarget(data, pokeName);
      if (types.length) updateTarget();
    });
    raidSel.addEventListener("change", () => { targetPokeInput.value = ""; type1Sel.value = ""; type2Sel.value = ""; updateTarget(); });
    type1Sel.addEventListener("change", () => { targetPokeInput.value = ""; raidSel.value = ""; updateTarget(); });
    type2Sel.addEventListener("change", () => { targetPokeInput.value = ""; raidSel.value = ""; updateTarget(); });

    wrap.appendChild(targetBlock);

    // ── Attacker builder ───────────────────────────────────────

    const attackerForm = document.createElement("div");
    attackerForm.className = "calc-form";

    const pokeInput = document.createElement("input");
    pokeInput.type = "text"; pokeInput.className = "search-input";
    pokeInput.placeholder = "Pokémon name (e.g. Mewtwo)";

    const fastSel = buildMoveSelect(
      data.fastMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      "Fast Move"
    );
    const chargedSel = buildMoveSelect(
      data.chargedMoves!.map((m) => ({ value: m.name, label: m.name, type: m.type })),
      "Charged Move"
    );
    const { label: cpLbl, input: cpInput } = cpWidget(data);

    const addBtn = document.createElement("button");
    addBtn.className = "btn-primary"; addBtn.textContent = "+ Add";

    const errText = document.createElement("p");
    errText.className = "error-text"; errText.style.display = "none";

    // Auto-fill CP when Pokémon name is typed
    pokeInput.addEventListener("change", () => {
      const poke = pokemonByName(data, pokeInput.value.trim());
      if (poke) {
        const defaultCP = cpForLevel(poke, 15, 15, 15, 0.7903);
        cpInput.value = String(defaultCP);
      }
    });

    attackerForm.appendChild(pokeInput);
    attackerForm.appendChild(fastSel);
    attackerForm.appendChild(chargedSel);
    attackerForm.appendChild(cpLbl);
    attackerForm.appendChild(addBtn);
    attackerForm.appendChild(errText);
    wrap.appendChild(attackerForm);

    // ── Comparison table ───────────────────────────────────────

    const tableArea = document.createElement("div");
    tableArea.className = "result-area";
    wrap.appendChild(tableArea);

    function renderTable() {
      tableArea.innerHTML = "";

      if (attackers.length === 0) {
        const hint = document.createElement("p");
        hint.className = "empty-state";
        hint.textContent = "Add Pokémon above to compare.";
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

      const tableWrap = document.createElement("div");
      tableWrap.className = "table-wrap";

      const table = document.createElement("table");
      table.className = "counter-table";
      table.innerHTML = `<thead><tr>
        <th>#</th><th>Pokémon</th><th>Fast Move</th><th>Charged Move</th>
        <th>CP</th><th>DPS</th><th>TDO</th><th></th>
      </tr></thead>`;

      const tbody = document.createElement("tbody");

      for (const { a, dps, tdo, fEff, cEff } of rows) {
        const tr = document.createElement("tr");

        const rankTd = document.createElement("td");
        rankTd.textContent = String(rows.indexOf(rows.find((r) => r.a.id === a.id)!) + 1);
        if (rows[0].a.id === a.id) rankTd.className = "rank-s";

        const pokeTd = document.createElement("td");
        pokeTd.style.whiteSpace = "nowrap";
        const pokeSpImg = document.createElement("img");
        pokeSpImg.src = pokeSprite(a.poke.pokemon_id);
        pokeSpImg.alt = a.poke.pokemon_name;
        pokeSpImg.className = "poke-sprite";
        pokeSpImg.loading = "lazy"; pokeSpImg.decoding = "async";
        pokeTd.appendChild(pokeSpImg);
        pokeTd.append(` ${pokeName(data, a.poke.pokemon_name)}`);

        const fastTd = document.createElement("td");
        fastTd.style.whiteSpace = "nowrap";
        fastTd.appendChild(typeBadge(a.fast.type));
        fastTd.append(` ${a.fast.name}`);
        if (hasTarget && fEff !== 1) {
          const s = document.createElement("span");
          s.style.cssText = `color:${fEff > 1 ? "var(--green)" : "var(--red)"};font-weight:700;font-size:0.78rem;`;
          s.textContent = ` ${fEff.toFixed(2)}×`;
          fastTd.appendChild(s);
        }

        const chargedTd = document.createElement("td");
        chargedTd.style.whiteSpace = "nowrap";
        chargedTd.appendChild(typeBadge(a.charged.type));
        chargedTd.append(` ${a.charged.name}`);
        if (hasTarget && cEff !== 1) {
          const s = document.createElement("span");
          s.style.cssText = `color:${cEff > 1 ? "var(--green)" : "var(--red)"};font-weight:700;font-size:0.78rem;`;
          s.textContent = ` ${cEff.toFixed(2)}×`;
          chargedTd.appendChild(s);
        }

        const cpTd = document.createElement("td"); cpTd.textContent = a.cp.toLocaleString();
        const dpsTd = document.createElement("td"); dpsTd.textContent = dps.toFixed(2);
        if (rows[0].a.id === a.id) dpsTd.className = "rank-s";
        const tdoTd = document.createElement("td"); tdoTd.textContent = tdo.toFixed(0);

        const removeTd = document.createElement("td");
        const removeBtn = document.createElement("button");
        removeBtn.textContent = "×";
        removeBtn.style.cssText = "background:none;border:none;color:var(--text-2);cursor:pointer;font-size:1.1rem;padding:0 0.3rem;line-height:1;";
        removeBtn.addEventListener("click", () => { attackers = attackers.filter((x) => x.id !== a.id); renderTable(); });
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
        clearBtn.textContent = "Clear all";
        clearBtn.style.cssText = "background:none;border:none;color:var(--text-2);cursor:pointer;font-size:0.8rem;text-decoration:underline;font-family:var(--font);";
        clearBtn.addEventListener("click", () => { attackers = []; renderTable(); });
        clearTd.appendChild(clearBtn);
        clearTr.appendChild(clearTd);
        tbody.appendChild(clearTr);
      }

      table.appendChild(tbody);
      tableWrap.appendChild(table);
      tableArea.appendChild(tableWrap);
    }

    addBtn.addEventListener("click", () => {
      errText.style.display = "none";
      const poke = pokemonByName(data, pokeInput.value.trim());
      const fast = data.fastMoves!.find((m) => m.name === fastSel.value);
      const charged = data.chargedMoves!.find((m) => m.name === chargedSel.value);
      const cp = Math.max(10, Number(cpInput.value) || 3000);

      if (!poke) { errText.textContent = `Pokémon not found.`; errText.style.display = ""; return; }
      if (!fast || !charged) { errText.textContent = "Select both moves."; errText.style.display = ""; return; }

      attackers.push({ id: nextId++, poke, fast, charged, cp });
      renderTable();
      pokeInput.value = "";
      pokeInput.focus();
    });

    pokeInput.addEventListener("keydown", (e) => { if (e.key === "Enter") addBtn.click(); });

    renderTable();
    return wrap;
  };
}

// ── Init ──────────────────────────────────────────────────────

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";

    if (!data.pokemon || !data.fastMoves || !data.chargedMoves) {
      app.innerHTML = `<div class="error-state">Game data unavailable. Please try again later.</div>`;
      return;
    }

    const tabs = buildTabs([
      { id: "calc",    label: "⚡ Calculator", render: buildCalcPanel(data) },
      { id: "compare", label: "📊 Compare",    render: buildComparePanel(data) },
    ]);
    app.appendChild(tabs);
  } catch (err) {
    app.innerHTML = `<div class="error-state">Failed to load data. Please try again later.</div>`;
    console.error(err);
  }
}

init();
