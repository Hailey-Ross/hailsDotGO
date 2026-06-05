import { loadGameData, pokeSprite, cpForLevel, cpmFromCP } from "./shared/gamedata";
import { calcCounters, renderCounterTable, calcSinglePokemon, DEFAULT_CONFIG } from "./shared/counters";
import type { PokemonConfig, PokemonForm } from "./shared/counters";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import type { GameData, RaidBoss, PokemonStat } from "./shared/types";

const app = document.getElementById("raids-app")!;

function createBossCard(boss: RaidBoss): HTMLElement {
  const card = document.createElement("div");
  card.className = "boss-card";

  if (boss.image_url) {
    const bg = document.createElement("div");
    bg.className = "boss-bg";
    bg.style.backgroundImage = `url('${boss.image_url}')`;
    card.appendChild(bg);
  }

  const inner = document.createElement("div");
  inner.className = "boss-card-inner";

  if (boss.image_url) {
    const img = document.createElement("img");
    img.src = boss.image_url;
    img.alt = boss.pokemon_name;
    img.className = "boss-img";
    img.loading = "lazy"; img.decoding = "async";
    inner.appendChild(img);
  }

  if (boss.types?.length) {
    const typeRow = document.createElement("div");
    typeRow.className = "boss-types";
    boss.types.forEach((t) => typeRow.appendChild(typeBadge(t)));
    inner.appendChild(typeRow);
  }

  const name = document.createElement("span");
  name.className = "boss-name";
  name.textContent = boss.pokemon_name.replace(/_/g, " ");
  inner.appendChild(name);

  if (boss.cp) {
    const cp = document.createElement("span");
    cp.className = "boss-cp";
    cp.textContent = boss.cp_max && boss.cp_max > boss.cp
      ? `${boss.cp.toLocaleString()}–${boss.cp_max.toLocaleString()} CP`
      : `${boss.cp.toLocaleString()} CP`;
    inner.appendChild(cp);

    if (boss.cp_boosted_min) {
      const boosted = document.createElement("span");
      boosted.className = "boss-cp boss-cp-boosted";
      boosted.textContent = boss.cp_boosted_max && boss.cp_boosted_max > boss.cp_boosted_min
        ? `⛅ ${boss.cp_boosted_min.toLocaleString()}–${boss.cp_boosted_max.toLocaleString()} CP`
        : `⛅ ${boss.cp_boosted_min.toLocaleString()} CP`;
      inner.appendChild(boosted);
    }
  }

  card.appendChild(inner);

  if (boss.can_be_shiny) {
    const badge = document.createElement("div");
    badge.className = "shiny-badge";
    badge.textContent = "SHINY";
    card.appendChild(badge);
  }

  return card;
}

function buildRaidsView(data: GameData): HTMLElement {
  const wrap = document.createElement("div");

  const tiers = Object.entries(data.raids!).sort(([a], [b]) => Number(b) - Number(a));

  const uniqueTypes = [...new Set(
    tiers.flatMap(([, bosses]) => bosses.flatMap((b) => b.types ?? []))
  )].sort();

  const activeTiers = new Set(tiers.map(([t]) => t));
  const activeTypes = new Set(uniqueTypes);

  // ── Filter bar ─────────────────────────────────────────────
  const filterBar = document.createElement("div");
  filterBar.className = "raid-filter-bar";

  const tierRow = document.createElement("div");
  tierRow.className = "filter-row";
  const tierLbl = document.createElement("span");
  tierLbl.className = "filter-label";
  tierLbl.textContent = "Tiers";
  tierRow.appendChild(tierLbl);

  for (const [tier] of tiers) {
    const chip = document.createElement("button");
    chip.className = `filter-chip tier-chip tier-${tier} active`;
    chip.textContent = tier === "6" ? "Mega" : `T${tier}`;
    chip.addEventListener("click", () => {
      if (activeTiers.has(tier)) { activeTiers.delete(tier); chip.classList.remove("active"); }
      else { activeTiers.add(tier); chip.classList.add("active"); }
      updateVisibility();
    });
    tierRow.appendChild(chip);
  }
  filterBar.appendChild(tierRow);

  if (uniqueTypes.length > 1) {
    const typeRow = document.createElement("div");
    typeRow.className = "filter-row";
    const typeLbl = document.createElement("span");
    typeLbl.className = "filter-label";
    typeLbl.textContent = "Types";
    typeRow.appendChild(typeLbl);

    for (const type of uniqueTypes) {
      const chip = document.createElement("button");
      chip.className = "filter-chip type-chip active";
      chip.textContent = type;
      chip.style.setProperty("--type-color", TYPE_COLORS[type] ?? "#888");
      chip.addEventListener("click", () => {
        if (activeTypes.has(type)) { activeTypes.delete(type); chip.classList.remove("active"); }
        else { activeTypes.add(type); chip.classList.add("active"); }
        updateVisibility();
      });
      typeRow.appendChild(chip);
    }
    filterBar.appendChild(typeRow);
  }

  wrap.appendChild(filterBar);

  // ── Counter panel (shared, updated on boss click) ──────────
  const counterPanel = document.createElement("div");
  counterPanel.style.display = "none";
  let activeCard: HTMLElement | null = null;

  // ── Pokémon picker ──────────────────────────────────────────
  const allBosses = tiers.flatMap(([tier, bosses]) =>
    bosses.map((boss) => ({ boss, tier }))
  );

  const picker = document.createElement("div");
  picker.className = "pokemon-picker";

  const pickerLabel = document.createElement("div");
  pickerLabel.className = "picker-label";
  pickerLabel.textContent = "Check your Pokémon";
  picker.appendChild(pickerLabel);

  const searchRow = document.createElement("div");
  searchRow.className = "picker-search-row";

  const searchInput = document.createElement("input");
  searchInput.type = "text";
  searchInput.className = "search-input";
  searchInput.placeholder = "Search Pokémon...";
  searchInput.setAttribute("autocomplete", "off");

  const clearBtn = document.createElement("button");
  clearBtn.className = "picker-clear-btn";
  clearBtn.textContent = "×";
  clearBtn.hidden = true;
  clearBtn.setAttribute("aria-label", "Clear selection");

  searchRow.appendChild(searchInput);
  searchRow.appendChild(clearBtn);
  picker.appendChild(searchRow);

  const dropdown = document.createElement("div");
  dropdown.className = "picker-dropdown";
  dropdown.hidden = true;
  picker.appendChild(dropdown);

  const configPanel = document.createElement("div");
  configPanel.className = "picker-config";
  configPanel.hidden = true;
  picker.appendChild(configPanel);

  const resultsPanel = document.createElement("div");
  resultsPanel.className = "picker-results-panel";
  resultsPanel.hidden = true;
  picker.appendChild(resultsPanel);

  let currentName = "";
  let currentPoke: PokemonStat | null = null;
  let currentCP = 0;
  const config: PokemonConfig = { ...DEFAULT_CONFIG };

  function clampIV(v: number) { return Math.max(0, Math.min(15, v | 0)); }

  function updateCPM() {
    if (!currentPoke || !currentCP) return;
    config.cpm = cpmFromCP(data, currentPoke, config.atkIV, config.defIV, config.staIV, currentCP);
  }

  function buildConfigPanel(pokemonName: string) {
    configPanel.innerHTML = "";

    const nameLower = pokemonName.toLowerCase();
    // Strip known prefixes to find the base name for variant lookups
    const baseLookup = nameLower.replace(/^shadow_/, "").replace(/^primal_/, "");

    const allPokeNames = new Set((data.pokemon ?? []).map((p) => p.pokemon_name.toLowerCase()));
    const hasShadow = allPokeNames.has(`shadow_${baseLookup}`);
    const hasPrimal = allPokeNames.has(`primal_${baseLookup}`);

    // Set default form from name prefix
    if (/^shadow_/.test(nameLower)) config.form = "shadow";
    else if (/^primal_/.test(nameLower)) config.form = "primal";
    else config.form = "normal";

    // Build only the applicable forms for this Pokémon
    const forms: { value: PokemonForm; label: string }[] = [
      { value: "normal", label: "Normal" },
      ...(hasShadow ? [
        { value: "shadow" as PokemonForm, label: "Shadow" },
        { value: "purified" as PokemonForm, label: "Purified" },
      ] : []),
      ...(hasPrimal ? [{ value: "primal" as PokemonForm, label: "Primal" }] : []),
    ];

    // Guard: if active form is no longer in the list, reset
    if (!forms.some((f) => f.value === config.form)) config.form = "normal";

    // Only render the form row if there's more than just Normal
    if (forms.length > 1) {
      const modRow = document.createElement("div");
      modRow.className = "config-row config-mods";
      const modLbl = document.createElement("span");
      modLbl.className = "config-label";
      modLbl.textContent = "Form";
      modRow.appendChild(modLbl);

      for (const f of forms) {
        const btn = document.createElement("button");
        btn.className = `filter-chip${config.form === f.value ? " active" : ""}`;
        btn.textContent = f.label;
        btn.addEventListener("click", () => {
          config.form = f.value;
          modRow.querySelectorAll(".filter-chip").forEach((c) => c.classList.remove("active"));
          btn.classList.add("active");
          renderResults();
        });
        modRow.appendChild(btn);
      }
      configPanel.appendChild(modRow);
    }

    // CP + IV inputs
    const statsRow = document.createElement("div");
    statsRow.className = "config-row config-stats";

    function makeStatInput(
      labelText: string,
      value: number,
      min: number,
      max: number,
      onChange: (v: number) => void
    ) {
      const group = document.createElement("div");
      group.className = "config-group";
      const lbl = document.createElement("label");
      lbl.className = "config-label";
      lbl.textContent = labelText;
      const inp = document.createElement("input");
      inp.type = "number";
      inp.className = "config-input";
      inp.min = String(min);
      inp.max = String(max);
      inp.value = String(value);
      inp.addEventListener("change", () => {
        const v = Number(inp.value);
        if (isNaN(v)) return;
        const clamped = Math.max(min, Math.min(max, Math.round(v)));
        inp.value = String(clamped);
        onChange(clamped);
        updateCPM();
        renderResults();
      });
      group.appendChild(lbl);
      group.appendChild(inp);
      return group;
    }

    const cpGroup = document.createElement("div");
    cpGroup.className = "config-group";
    const cpLbl = document.createElement("label");
    cpLbl.className = "config-label";
    cpLbl.textContent = "CP";
    const cpInp = document.createElement("input");
    cpInp.type = "number";
    cpInp.className = "config-input config-input-cp";
    cpInp.min = "10";
    cpInp.max = "99999";
    cpInp.value = String(currentCP);
    cpInp.addEventListener("change", () => {
      const v = Math.max(10, Math.round(Number(cpInp.value)));
      cpInp.value = String(v);
      currentCP = v;
      updateCPM();
      renderResults();
    });
    cpGroup.appendChild(cpLbl);
    cpGroup.appendChild(cpInp);
    statsRow.appendChild(cpGroup);

    statsRow.appendChild(makeStatInput("ATK IV", config.atkIV, 0, 15, (v) => { config.atkIV = clampIV(v); }));
    statsRow.appendChild(makeStatInput("DEF IV", config.defIV, 0, 15, (v) => { config.defIV = clampIV(v); }));
    statsRow.appendChild(makeStatInput("STA IV", config.staIV, 0, 15, (v) => { config.staIV = clampIV(v); }));

    configPanel.appendChild(statsRow);
    configPanel.hidden = false;
  }

  function renderResults() {
    if (!currentName) return;

    const rows = allBosses.map(({ boss, tier }) => ({
      boss,
      tier,
      result: calcSinglePokemon(data, currentName, boss, config),
    }));
    rows.sort((a, b) => (b.result?.dps ?? -1) - (a.result?.dps ?? -1));

    resultsPanel.innerHTML = "";

    const headingWrap = document.createElement("div");
    headingWrap.className = "picker-heading-wrap";

    if (currentPoke) {
      const sprite = document.createElement("img");
      sprite.src = pokeSprite(currentPoke.pokemon_id);
      sprite.alt = currentName;
      sprite.className = "picker-sprite";
      sprite.loading = "lazy"; sprite.decoding = "async";
      headingWrap.appendChild(sprite);
    }

    const heading = document.createElement("h3");
    heading.className = "picker-results-heading";
    heading.textContent = `${currentName.replace(/_/g, " ")} vs all current raids`;
    headingWrap.appendChild(heading);
    resultsPanel.appendChild(headingWrap);

    const tableWrap = document.createElement("div");
    tableWrap.className = "table-wrap";

    const table = document.createElement("table");
    table.className = "counter-table";
    table.innerHTML = `
      <thead>
        <tr>
          <th>Boss</th>
          <th>Tier</th>
          <th>Fast Move</th>
          <th>Charged Move</th>
          <th>DPS</th>
          <th>TDO</th>
        </tr>
      </thead>
    `;

    const tbody = document.createElement("tbody");
    for (const { boss, tier, result } of rows) {
      const tr = document.createElement("tr");

      const bossTd = document.createElement("td");
      bossTd.style.whiteSpace = "nowrap";
      if (boss.image_url) {
        const img = document.createElement("img");
        img.src = boss.image_url;
        img.alt = boss.pokemon_name;
        img.className = "poke-sprite";
        img.loading = "lazy"; img.decoding = "async";
        bossTd.appendChild(img);
      }
      bossTd.append(` ${boss.pokemon_name.replace(/_/g, " ")}`);

      const tierTd = document.createElement("td");
      tierTd.textContent = tier === "6" ? "Mega" : `T${tier}`;

      tr.appendChild(bossTd);
      tr.appendChild(tierTd);

      if (result) {
        const fastTd = document.createElement("td");
        fastTd.appendChild(typeBadge(result.fastType));
        fastTd.append(` ${result.fastMove}`);

        const chargedTd = document.createElement("td");
        chargedTd.appendChild(typeBadge(result.chargedType));
        chargedTd.append(` ${result.chargedMove}`);

        const dpsTd = document.createElement("td");
        dpsTd.textContent = result.dps.toFixed(2);

        const tdoTd = document.createElement("td");
        tdoTd.textContent = result.tdo.toFixed(0);

        tr.append(fastTd, chargedTd, dpsTd, tdoTd);
      } else {
        const emptyTd = document.createElement("td");
        emptyTd.colSpan = 4;
        emptyTd.className = "text-dim";
        emptyTd.textContent = "n/a";
        tr.appendChild(emptyTd);
      }

      tbody.appendChild(tr);
    }

    table.appendChild(tbody);
    tableWrap.appendChild(table);
    resultsPanel.appendChild(tableWrap);
    resultsPanel.hidden = false;
  }

  function selectPokemon(name: string) {
    currentName = name;
    currentPoke = (data.pokemon ?? []).find(
      (p) => p.pokemon_name.toLowerCase() === name.toLowerCase()
    ) ?? null;

    searchInput.value = name.replace(/_/g, " ");
    dropdown.hidden = true;
    clearBtn.hidden = false;

    // Reset config, compute default CP at L40 15/15/15
    Object.assign(config, DEFAULT_CONFIG);
    currentCP = currentPoke
      ? cpForLevel(currentPoke, 15, 15, 15, DEFAULT_CONFIG.cpm)
      : 0;

    // Clear boss counter panel if open
    if (activeCard) { activeCard.classList.remove("active"); activeCard = null; }
    counterPanel.style.display = "none";
    counterPanel.innerHTML = "";

    buildConfigPanel(name);
    renderResults();
    resultsPanel.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  function buildDropdown(query: string) {
    dropdown.innerHTML = "";
    if (!query) { dropdown.hidden = true; return; }

    const q = query.toLowerCase();
    const norm = (n: string) => n.toLowerCase().replace(/_/g, " ");
    const matches = (data.pokemon ?? [])
      .filter((p) => norm(p.pokemon_name).includes(q))
      .sort((a, b) => {
        const na = norm(a.pokemon_name);
        const nb = norm(b.pokemon_name);
        const rank = (s: string) => s === q ? 0 : s.startsWith(q) ? 1 : 2;
        return rank(na) - rank(nb);
      })
      .slice(0, 10);

    if (!matches.length) {
      const none = document.createElement("button");
      none.className = "picker-option no-match";
      none.textContent = "No Pokémon found";
      none.disabled = true;
      dropdown.appendChild(none);
    } else {
      for (const p of matches) {
        const opt = document.createElement("button");
        opt.className = "picker-option";
        opt.textContent = p.pokemon_name.replace(/_/g, " ");
        opt.addEventListener("mousedown", (e) => {
          e.preventDefault();
          selectPokemon(p.pokemon_name);
        });
        dropdown.appendChild(opt);
      }
    }
    dropdown.hidden = false;
  }

  searchInput.addEventListener("input", () => {
    buildDropdown(searchInput.value.trim());
  });

  searchInput.addEventListener("blur", () => {
    setTimeout(() => { dropdown.hidden = true; }, 150);
  });

  searchInput.addEventListener("keydown", (e) => {
    if (e.key === "Escape") dropdown.hidden = true;
    if (e.key === "Enter") {
      const first = dropdown.querySelector<HTMLButtonElement>(".picker-option:not(.no-match)");
      if (first) first.dispatchEvent(new MouseEvent("mousedown"));
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      const first = dropdown.querySelector<HTMLButtonElement>(".picker-option");
      if (first) first.focus();
    }
  });

  clearBtn.addEventListener("click", () => {
    currentName = "";
    currentPoke = null;
    currentCP = 0;
    searchInput.value = "";
    clearBtn.hidden = true;
    dropdown.hidden = true;
    configPanel.hidden = true;
    configPanel.innerHTML = "";
    resultsPanel.hidden = true;
    resultsPanel.innerHTML = "";
    searchInput.focus();
  });

  document.addEventListener("click", (e) => {
    if (!picker.contains(e.target as Node)) dropdown.hidden = true;
  });

  wrap.appendChild(picker);

  // ── Tier sections ──────────────────────────────────────────
  const tierSections = new Map<string, HTMLElement>();
  const cardMeta = new Map<string, { el: HTMLElement; types: string[] }>();

  for (const [tier, bosses] of tiers) {
    if (!bosses.length) continue;

    const section = document.createElement("div");
    section.className = "tier-block fade-up";
    section.dataset.tier = tier;

    const label = document.createElement("div");
    label.className = `tier-label tier-${tier}`;
    label.textContent = tier === "6" ? "Mega / Primal" : `Tier ${tier}`;
    section.appendChild(label);

    const grid = document.createElement("div");
    grid.className = "raid-boss-grid";

    for (const boss of bosses) {
      const card = createBossCard(boss);
      card.style.cursor = "pointer";

      card.addEventListener("click", () => {
        if (activeCard === card) {
          card.classList.remove("active");
          activeCard = null;
          counterPanel.style.display = "none";
          counterPanel.innerHTML = "";
          return;
        }

        if (activeCard) activeCard.classList.remove("active");
        activeCard = card;
        card.classList.add("active");

        counterPanel.innerHTML = "";
        counterPanel.style.display = "";
        counterPanel.appendChild(renderCounterTable(boss, calcCounters(data, boss)));
        counterPanel.scrollIntoView({ behavior: "smooth", block: "nearest" });
      });

      const key = `${tier}::${boss.pokemon_name}`;
      cardMeta.set(key, { el: card, types: boss.types ?? [] });
      grid.appendChild(card);
    }

    section.appendChild(grid);
    tierSections.set(tier, section);
    wrap.appendChild(section);
  }

  wrap.appendChild(counterPanel);

  function updateVisibility() {
    for (const [tier, section] of tierSections) {
      if (!activeTiers.has(tier)) {
        section.style.display = "none";
        continue;
      }
      section.style.display = "";
      for (const [key, { el, types }] of cardMeta) {
        if (!key.startsWith(tier + "::")) continue;
        const show = activeTypes.size === 0 || types.some((t) => activeTypes.has(t));
        el.style.display = show ? "" : "none";
      }
    }
  }

  return wrap;
}

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";

    if (!data.raids || Object.keys(data.raids).length === 0) {
      app.innerHTML = `<div class="error-state">Raid data is temporarily unavailable. Check back later.</div>`;
      return;
    }

    app.appendChild(buildRaidsView(data));
  } catch (err) {
    app.innerHTML = `<div class="error-state">Failed to load data. Please try again later.</div>`;
    console.error(err);
  }
}

init();
