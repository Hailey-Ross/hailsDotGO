import { loadGameData, pokeSprite, cpForLevel, cpmFromCP, pokeName } from "./shared/gamedata";
import { calcCounters, renderCounterTable, calcSinglePokemon, DEFAULT_CONFIG } from "./shared/counters";
import { renderBoxVsBoss, renderBoxPlaceholder } from "./shared/boxcounters";
import type { PokemonConfig, PokemonForm } from "./shared/counters";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, RaidBoss, PokemonStat, UpcomingRaid } from "./shared/types";
import { parseLocal, dateFmt, relTime } from "./shared/time";

declare const JSC: Record<string, string>;
declare const RD: Record<string, string>;

const app = document.getElementById("raids-app")!;

// Past this much time left, a countdown stops being useful and a date reads better.
const WINDOW_DATE_THRESHOLD_MS = 48 * 60 * 60 * 1000;

// tierLabel names a tier the way the tabs already do, so the up next strip and the
// grid agree on what to call things.
function tierLabel(tier: string, shadow?: boolean): string {
  if (shadow) return RD.shadowRaids;
  return tier === "6" ? RD.mega : RD.tierN.replace("{tier}", tier);
}

// paintWindowPill writes a rotation's state in the VIEWER'S OWN time.
//
// The server decided this boss belongs on the page using the widest possible reading
// of the window, UTC+14 through UTC-12, so nobody loses a boss they can still raid.
// That is why a card can legitimately say "not started yet" or "ended" and still be
// here: at a changeover the old and new rotations genuinely coexist across the planet
// for about 26 hours, and these two states are what make that legible instead of
// looking like a bug.
function paintWindowPill(pill: HTMLElement): void {
  const end = parseLocal(pill.dataset.end);
  if (!end) {
    pill.style.display = "none";
    return;
  }
  const start = parseLocal(pill.dataset.start);
  const now = Date.now();
  let cls = "raid-window raid-window-live";
  let text: string;
  let note = "";

  if (pill.dataset.live === "1") {
    text = RD.windowLiveNow;
  } else if (start && now < start.getTime()) {
    cls = "raid-window raid-window-early";
    text = RD.windowStartsIn.replace("{t}", relTime(start.getTime() - now));
    note = RD.windowEarlyNote;
  } else if (now >= end.getTime()) {
    cls = "raid-window raid-window-late";
    text = RD.windowEndedLocal;
    note = RD.windowLateNote;
  } else if (end.getTime() - now > WINDOW_DATE_THRESHOLD_MS) {
    text = RD.windowUntil.replace("{date}", dateFmt.format(end));
  } else {
    text = RD.windowEndsIn.replace("{t}", relTime(end.getTime() - now));
  }

  pill.className = cls;
  pill.textContent = text;
  if (note) pill.title = note;
}

function windowPill(startsAt: string | undefined, endsAt: string | undefined, live?: boolean): HTMLElement | null {
  if (!endsAt) return null;
  const pill = document.createElement("span");
  pill.dataset.start = startsAt ?? "";
  pill.dataset.end = endsAt;
  if (live) pill.dataset.live = "1";
  paintWindowPill(pill);
  return pill;
}

// A minute is plenty: the shortest thing on screen is an hours-and-minutes countdown.
function tickRaidWindows(): void {
  app.querySelectorAll<HTMLElement>(".raid-window").forEach(paintWindowPill);
}

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

  const pill = windowPill(boss.starts_at, boss.ends_at);
  if (pill) inner.appendChild(pill);

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
  // Bumped every time a panel is opened, so a box lookup that returns after the
  // trainer has moved on does not append its answer to a different boss.
  let panelSeq = 0;

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

        // The trainer's own Pokemon comes FIRST, above the generic list. It is
        // the answer they actually came for: what to bring, out of what they
        // own. The top counters below it are the reference for what is possible.
        //
        // The placeholder is appended now so the box section keeps its place in
        // the order while it loads, rather than appearing under the table and
        // shoving it down when it arrives.
        const panelToken = ++panelSeq;
        const placeholder = renderBoxPlaceholder();
        counterPanel.appendChild(placeholder);

        counterPanel.appendChild(renderCounterTable(data, boss, calcCounters(data, boss)));
        renderBoxVsBoss(data, boss).then((section) => {
          if (panelToken === panelSeq && counterPanel.style.display !== "none") {
            placeholder.replaceWith(section);
          } else {
            placeholder.remove();
          }
        });
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
  // Bumped every time a panel is opened, so a box lookup that returns after the
  // trainer has moved on does not append its answer to a different boss.
  let panelSeq = 0;

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

        // The trainer's own Pokemon comes FIRST, above the generic list. It is
        // the answer they actually came for: what to bring, out of what they
        // own. The top counters below it are the reference for what is possible.
        //
        // The placeholder is appended now so the box section keeps its place in
        // the order while it loads, rather than appearing under the table and
        // shoving it down when it arrives.
        const panelToken = ++panelSeq;
        const placeholder = renderBoxPlaceholder();
        counterPanel.appendChild(placeholder);

        counterPanel.appendChild(renderCounterTable(data, boss, calcCounters(data, boss)));
        renderBoxVsBoss(data, boss).then((section) => {
          if (panelToken === panelSeq && counterPanel.style.display !== "none") {
            placeholder.replaceWith(section);
          } else {
            placeholder.remove();
          }
        });
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

// buildUpNextSection renders what is coming, and what is already here but not yet
// on the grid.
//
// The second case is now rare. It used to be every Mega, because a Mega's typing
// differs from its base species and nothing the app carried described one; the
// server has shipped Mega stats and typings for a while, so a Mega is built like
// anything else. A boss still lands here when no dataset knows the species at all,
// and naming it is the honest version: the card appears by itself once the data
// arrives.
function buildUpNextSection(data: GameData): HTMLElement | null {
  const list: UpcomingRaid[] = data.upcomingRaids ?? [];
  if (!list.length) return null;

  const section = document.createElement("section");
  section.className = "raid-upnext";

  const heading = document.createElement("h2");
  heading.className = "raid-upnext-title";
  heading.textContent = RD.upNext;
  section.appendChild(heading);

  const row = document.createElement("div");
  row.className = "raid-upnext-row";

  for (const entry of list) {
    const card = document.createElement("div");
    card.className = entry.live ? "upnext-card upnext-card-live" : "upnext-card";

    const tier = document.createElement("span");
    tier.className = `upnext-tier tier-${entry.tier}`;
    tier.textContent = tierLabel(entry.tier, entry.shadow);
    card.appendChild(tier);

    const mons = document.createElement("div");
    mons.className = "upnext-mons";
    for (const boss of entry.bosses) {
      const chip = document.createElement("span");
      chip.className = "upnext-mon";
      if (boss.image) {
        const img = document.createElement("img");
        img.src = boss.image;
        img.alt = boss.name;
        img.loading = "lazy";
        img.decoding = "async";
        chip.appendChild(img);
      }
      chip.append(pokeName(data, boss.name));
      if (boss.canBeShiny) {
        const shiny = document.createElement("span");
        shiny.className = "upnext-shiny";
        shiny.textContent = "✨";
        shiny.title = RD.shinyBadge;
        chip.appendChild(shiny);
      }
      mons.appendChild(chip);
    }
    card.appendChild(mons);

    const pill = windowPill(entry.starts_at, entry.ends_at, entry.live);
    if (pill) card.appendChild(pill);

    if (entry.live) {
      const note = document.createElement("span");
      note.className = "upnext-note";
      note.textContent = RD.upNextDetailsPending;
      card.appendChild(note);
    }

    row.appendChild(card);
  }

  section.appendChild(row);
  return section;
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

    const upNext = buildUpNextSection(data);
    if (upNext) app.appendChild(upNext);

    if (data.maxBattles && Object.keys(data.maxBattles).length > 0) {
      app.appendChild(buildMaxBattlesSection(data));
    }

    // Countdowns go stale on their own; nothing else on this page re-renders.
    setInterval(tickRaidWindows, 60000);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
