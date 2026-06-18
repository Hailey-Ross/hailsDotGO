import { loadGameData, pokeSprite, cpForLevel, cpmFromCP, pokeName } from "./shared/gamedata";
import { calcCounters, renderCounterTable, calcSinglePokemon, DEFAULT_CONFIG } from "./shared/counters";
import type { PokemonConfig, PokemonForm } from "./shared/counters";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, RaidBoss, PokemonStat } from "./shared/types";

declare const JSC: Record<string, string>;
declare const RD: Record<string, string>;

const app = document.getElementById("raids-app")!;

function createBossCard(boss: RaidBoss, data: GameData): HTMLElement {
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
  name.textContent = pokeName(data, boss.pokemon_name);
  inner.appendChild(name);

  if (boss.cp) {
    const cp = document.createElement("span");
    cp.className = "boss-cp";
    cp.textContent = boss.cp_max && boss.cp_max > boss.cp
      ? `${boss.cp.toLocaleString()}-${boss.cp_max.toLocaleString()} CP`
      : `${boss.cp.toLocaleString()} CP`;
    inner.appendChild(cp);

    if (boss.cp_boosted_min) {
      const boosted = document.createElement("span");
      boosted.className = "boss-cp boss-cp-boosted";
      boosted.textContent = boss.cp_boosted_max && boss.cp_boosted_max > boss.cp_boosted_min
        ? `⛅ ${boss.cp_boosted_min.toLocaleString()}-${boss.cp_boosted_max.toLocaleString()} CP`
        : `⛅ ${boss.cp_boosted_min.toLocaleString()} CP`;
      inner.appendChild(boosted);
    }
  }

  card.appendChild(inner);

  if (boss.can_be_shiny) {
    const badge = document.createElement("div");
    badge.className = "shiny-badge";
    badge.textContent = RD.shinyBadge;
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

  // Single-select tier tabs: only one tier is shown at a time. tiers is sorted highest
  // first, so the highest tier is the default active tab.
  let activeTier = tiers.length ? tiers[0][0] : "";
  const activeTypes = new Set(uniqueTypes);

  const filterBar = document.createElement("div");
  filterBar.className = "raid-filter-bar";

  const tierRow = document.createElement("div");
  tierRow.className = "raid-tabs";

  const tierTabs: HTMLButtonElement[] = [];
  for (const [tier] of tiers) {
    const tab = document.createElement("button");
    tab.className = `raid-tab tier-${tier}${tier === activeTier ? " active" : ""}`;
    tab.textContent = tier === "6" ? RD.mega : RD.tierN.replace("{tier}", tier);
    tab.dataset.tier = tier;
    tab.addEventListener("click", () => {
      activeTier = tier;
      for (const t of tierTabs) t.classList.toggle("active", t.dataset.tier === tier);
      updateVisibility();
    });
    tierTabs.push(tab);
    tierRow.appendChild(tab);
  }
  filterBar.appendChild(tierRow);

  if (uniqueTypes.length > 1) {
    const typeRow = document.createElement("div");
    typeRow.className = "filter-row";
    const typeLbl = document.createElement("span");
    typeLbl.className = "filter-label";
    typeLbl.textContent = RD.types;
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

  const counterPanel = document.createElement("div");
  counterPanel.style.display = "none";
  let activeCard: HTMLElement | null = null;

  const allBosses = tiers.flatMap(([tier, bosses]) =>
    bosses.map((boss) => ({ boss, tier }))
  );

  const picker = document.createElement("div");
  picker.className = "pokemon-picker";

  const pickerLabel = document.createElement("div");
  pickerLabel.className = "picker-label";
  pickerLabel.textContent = RD.checkYour;
  picker.appendChild(pickerLabel);

  const searchRow = document.createElement("div");
  searchRow.className = "picker-search-row";

  const searchInput = document.createElement("input");
  searchInput.type = "text";
  searchInput.className = "search-input";
  searchInput.placeholder = JSC.searchPokemon;
  searchInput.setAttribute("autocomplete", "off");

  const clearBtn = document.createElement("button");
  clearBtn.className = "picker-clear-btn";
  clearBtn.textContent = "×";
  clearBtn.hidden = true;
  clearBtn.setAttribute("aria-label", JSC.clearSelection);

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
    const baseLookup = nameLower.replace(/^shadow_/, "").replace(/^primal_/, "");

    const hasPrimal = baseLookup === "groudon" || baseLookup === "kyogre";

    if (/^shadow_/.test(nameLower)) config.form = "shadow";
    else if (/^primal_/.test(nameLower)) config.form = "primal";
    else config.form = "normal";

    // Shadow/Purified apply to all Pokemon; Primal only to Groudon and Kyogre
    const forms: { value: PokemonForm; label: string }[] = [
      { value: "normal", label: JSC.formNormal },
      { value: "shadow" as PokemonForm, label: JSC.formShadow },
      { value: "purified" as PokemonForm, label: JSC.formPurified },
      ...(hasPrimal ? [{ value: "primal" as PokemonForm, label: JSC.formPrimal }] : []),
    ];

    if (!forms.some((f) => f.value === config.form)) config.form = "normal";

    const modRow = document.createElement("div");
    modRow.className = "config-row config-mods";
    const modLbl = document.createElement("span");
    modLbl.className = "config-label";
    modLbl.textContent = RD.form;
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
    cpLbl.textContent = JSC.cp;
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

    statsRow.appendChild(makeStatInput(RD.atkIv, config.atkIV, 0, 15, (v) => { config.atkIV = clampIV(v); }));
    statsRow.appendChild(makeStatInput(RD.defIv, config.defIV, 0, 15, (v) => { config.defIV = clampIV(v); }));
    statsRow.appendChild(makeStatInput(RD.staIv, config.staIV, 0, 15, (v) => { config.staIV = clampIV(v); }));

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
    heading.textContent = RD.vsAll.replace("{name}", pokeName(data, currentName));
    headingWrap.appendChild(heading);
    resultsPanel.appendChild(headingWrap);

    const tableWrap = document.createElement("div");
    tableWrap.className = "table-wrap";

    const table = document.createElement("table");
    table.className = "counter-table";
    table.innerHTML = `
      <thead>
        <tr>
          <th>${RD.colBoss}</th>
          <th>${RD.colTier}</th>
          <th>${JSC.fastMove}</th>
          <th>${JSC.chargedMove}</th>
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
      bossTd.append(` ${pokeName(data, boss.pokemon_name)}`);

      const tierTd = document.createElement("td");
      tierTd.textContent = tier === "6" ? RD.mega : `T${tier}`;

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
        emptyTd.textContent = JSC.na;
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
      none.textContent = JSC.noPokemonFound;
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

  const tierSections = new Map<string, HTMLElement>();
  const cardMeta = new Map<string, { el: HTMLElement; types: string[] }>();

  for (const [tier, bosses] of tiers) {
    if (!bosses.length) continue;

    const section = document.createElement("div");
    section.className = "tier-block fade-up";
    section.dataset.tier = tier;

    const grid = document.createElement("div");
    grid.className = "raid-boss-grid";

    for (const boss of bosses) {
      const card = createBossCard(boss, data);
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

        const flavorP = document.createElement("p");
        flavorP.className = "poke-flavor";
        flavorP.style.display = "none";
        counterPanel.appendChild(flavorP);

        const genusEl = document.createElement("span");
        genusEl.className = "poke-genus";
        counterPanel.appendChild(genusEl);

        const badgeEl = document.createElement("span");
        badgeEl.style.display = "none";
        counterPanel.appendChild(badgeEl);

        fetchSpeciesData(boss.pokemon_name).then(d => {
          if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
          if (d.genus)  { genusEl.textContent = JSC.theGenus.replace("{genus}", d.genus); }
          if (d.isLegendary || d.isMythical) {
            badgeEl.textContent  = d.isMythical ? JSC.mythical : JSC.legendary;
            badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
            badgeEl.style.display = "";
          }
        });

        counterPanel.appendChild(renderCounterTable(data, boss, calcCounters(data, boss)));
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
      if (tier !== activeTier) {
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

  updateVisibility(); // show only the default (highest) tier initially

  return wrap;
}

// buildMaxBattlesSection renders Max Battle (Dynamax) bosses as their own block below the
// raids, with single-select tier tabs ordered highest tier to lowest, reusing the same boss
// cards and on-click counter panel.
function buildMaxBattlesSection(data: GameData): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "max-battles-section";

  const heading = document.createElement("h2");
  heading.className = "section-heading";
  heading.textContent = RD.maxBattles;
  wrap.appendChild(heading);

  const counterPanel = document.createElement("div");
  counterPanel.style.display = "none";
  let activeCard: HTMLElement | null = null;

  const tiers = Object.entries(data.maxBattles!).filter(([, b]) => b.length).sort(([a], [b]) => Number(b) - Number(a));
  let activeTier = tiers.length ? tiers[0][0] : "";
  const tierSections = new Map<string, HTMLElement>();

  const tabRow = document.createElement("div");
  tabRow.className = "raid-tabs";
  const tierTabs: HTMLButtonElement[] = [];
  for (const [tier] of tiers) {
    const tab = document.createElement("button");
    tab.className = `raid-tab tier-${tier}${tier === activeTier ? " active" : ""}`;
    tab.textContent = RD.tierN.replace("{tier}", tier);
    tab.dataset.tier = tier;
    tab.addEventListener("click", () => {
      activeTier = tier;
      for (const t of tierTabs) t.classList.toggle("active", t.dataset.tier === tier);
      for (const [tk, sec] of tierSections) sec.style.display = tk === activeTier ? "" : "none";
    });
    tierTabs.push(tab);
    tabRow.appendChild(tab);
  }
  wrap.appendChild(tabRow);

  for (const [tier, bosses] of tiers) {
    if (!bosses.length) continue;

    const section = document.createElement("div");
    section.className = "tier-block fade-up";
    section.style.display = tier === activeTier ? "" : "none";

    const grid = document.createElement("div");
    grid.className = "raid-boss-grid";

    for (const boss of bosses) {
      const card = createBossCard(boss, data);
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

        const flavorP = document.createElement("p");
        flavorP.className = "poke-flavor";
        flavorP.style.display = "none";
        counterPanel.appendChild(flavorP);

        const genusEl = document.createElement("span");
        genusEl.className = "poke-genus";
        counterPanel.appendChild(genusEl);

        const badgeEl = document.createElement("span");
        badgeEl.style.display = "none";
        counterPanel.appendChild(badgeEl);

        fetchSpeciesData(boss.pokemon_name).then(d => {
          if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
          if (d.genus)  { genusEl.textContent = JSC.theGenus.replace("{genus}", d.genus); }
          if (d.isLegendary || d.isMythical) {
            badgeEl.textContent  = d.isMythical ? JSC.mythical : JSC.legendary;
            badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
            badgeEl.style.display = "";
          }
        });

        counterPanel.appendChild(renderCounterTable(data, boss, calcCounters(data, boss)));
        counterPanel.scrollIntoView({ behavior: "smooth", block: "nearest" });
      });

      grid.appendChild(card);
    }

    section.appendChild(grid);
    tierSections.set(tier, section);
    wrap.appendChild(section);
  }

  wrap.appendChild(counterPanel);
  return wrap;
}

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";

    if (!data.raids || Object.keys(data.raids).length === 0) {
      app.innerHTML = `<div class="error-state">${RD.unavailable}</div>`;
      return;
    }

    app.appendChild(buildRaidsView(data));

    if (data.maxBattles && Object.keys(data.maxBattles).length > 0) {
      app.appendChild(buildMaxBattlesSection(data));
    }
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
