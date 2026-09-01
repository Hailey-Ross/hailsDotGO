import { loadGameData, reloadGameData, pokeSprite, cpForLevel, cpmFromCP, pokeName } from "./shared/gamedata";
import { calcCounters, renderCounterTable, calcSinglePokemon, DEFAULT_CONFIG } from "./shared/counters";
import { renderBoxVsBoss, renderBoxPlaceholder } from "./shared/boxcounters";
import type { PokemonConfig, PokemonForm } from "./shared/counters";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, RaidBoss, PokemonStat, UpcomingRaid } from "./shared/types";
import { parseLocal, dateFmt, monthDayFmt, weekdayFmt, startOfWeek, dayKey, relTime } from "./shared/time";

declare const JSC: Record<string, string>;
declare const RD: Record<string, string>;

const app = document.getElementById("raids-app")!;

// renderScope is aborted before each repaint, so anything buildRaidsView attached
// outside app is removed with the tree it belongs to.
let renderScope = new AbortController();

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

  // Empty tiers are filtered out, the way the Max Battles section already does it.
  // A tier can now genuinely have nothing in it: an event that suspends the seasonal
  // rotations empties five star and Shadow outright for the week it runs, and without
  // this that is a clickable tab showing nothing, or a blank page if it were ever the
  // highest tier that went quiet.
  const tiers = Object.entries(data.raids!).filter(([, b]) => b.length).sort(([a], [b]) => Number(b) - Number(a));

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

  // Torn down on the next repaint. This used to run once per page load and now
  // runs again every time a rotation flip repaints the grid, and a document level
  // listener closing over the old picker keeps that whole detached boss grid alive
  // on a page designed to be left open for days.
  document.addEventListener("click", (e) => {
    if (!picker.contains(e.target as Node)) dropdown.hidden = true;
  }, { signal: renderScope.signal });

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

// UPNEXT_CAL_NAMED is how many bosses a calendar cell names outright. Past it the
// cell shows the sprites and a count, because nobody reads seventeen names out of a
// box that size and seventeen names would make one week row taller than the grid.
const UPNEXT_CAL_NAMED = 3;

// buildUpNextSection renders what is coming, and what is already here but not yet
// on the grid.
//
// A CALENDAR. The server publishes every dated rotation it knows about, sixteen
// entries reaching a month out, so the question this section answers is "what is on,
// and when", and a month grid answers that at a glance in a way a list cannot. It
// also absorbs the thing that broke the two layouts before it: these rotations are
// wildly uneven, one GO Fest habitat day names seventeen Megas and the rotation after
// it names one, and in a calendar an uneven day is simply a fuller cell.
//
// Anything already live but with no card on the grid sits ABOVE the calendar rather
// than in it, because its start day is behind us and filing it there would bury it.
// That case is rare: it used to be every Mega, because a Mega's typing differs from
// its base species and nothing the app carried described one, and the server has
// shipped Mega stats for a while. A boss still lands there when no dataset knows the
// species at all, and naming it is the honest version.
function buildUpNextSection(data: GameData): HTMLElement | null {
  const list: UpcomingRaid[] = data.upcomingRaids ?? [];
  if (!list.length) return null;

  const section = document.createElement("section");
  section.className = "raid-upnext";

  const heading = document.createElement("h2");
  heading.className = "raid-upnext-title";
  heading.textContent = RD.upNext;
  section.appendChild(heading);

  const live = list.filter((e) => e.live);
  const dated: { entry: UpcomingRaid; start: Date }[] = [];
  for (const entry of list) {
    if (entry.live) continue;
    const start = parseLocal(entry.starts_at);
    // A rotation whose start will not parse still belongs on the page, it just has
    // no cell to sit in, so it joins the strip above the grid.
    if (start) dated.push({ entry, start });
    else live.push(entry);
  }

  if (live.length) {
    const strip = document.createElement("div");
    strip.className = "upnext-live";
    for (const entry of live) strip.appendChild(buildUpNextLiveRow(entry, data));
    section.appendChild(strip);
  }

  if (dated.length) section.appendChild(buildUpNextCalendar(dated, data));
  return section;
}

// buildUpNextCalendar lays the dated rotations out as whole weeks, from the week
// containing today through the week containing the last one, so the grid always opens
// at "now" and never has a ragged first or last row.
function buildUpNextCalendar(dated: { entry: UpcomingRaid; start: Date }[], data: GameData): HTMLElement {
  const cells = new Map<string, UpcomingRaid[]>();
  let last = dated[0].start;
  for (const { entry, start } of dated) {
    const key = dayKey(start);
    const bucket = cells.get(key);
    if (bucket) bucket.push(entry);
    else cells.set(key, [entry]);
    if (start > last) last = start;
  }

  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const first = startOfWeek(today);
  const cal = document.createElement("div");
  cal.className = "upnext-cal";

  const head = document.createElement("div");
  head.className = "upnext-cal-head";
  for (let i = 0; i < 7; i++) {
    const d = new Date(first);
    d.setDate(d.getDate() + i);
    const label = document.createElement("span");
    label.textContent = weekdayFmt.format(d);
    head.appendChild(label);
  }
  cal.appendChild(head);

  const grid = document.createElement("div");
  grid.className = "upnext-cal-grid";
  // Whole weeks only, and at least one, so a schedule entirely inside this week still
  // draws a calendar rather than a single stranded cell.
  const cursor = new Date(first);
  const stop = startOfWeek(last);
  do {
    for (let i = 0; i < 7; i++) {
      grid.appendChild(buildUpNextCell(cursor, today, cells.get(dayKey(cursor)) ?? [], data));
      cursor.setDate(cursor.getDate() + 1);
    }
  } while (cursor <= stop);
  cal.appendChild(grid);
  return cal;
}

// buildUpNextCell is one day.
function buildUpNextCell(day: Date, today: Date, entries: UpcomingRaid[], data: GameData): HTMLElement {
  const cell = document.createElement("div");
  cell.className = "upnext-cal-day";
  if (!entries.length) cell.classList.add("is-empty");
  if (day < today) cell.classList.add("is-past");
  if (day.getTime() === today.getTime()) cell.classList.add("is-today");

  const date = document.createElement("div");
  date.className = "upnext-cal-date";
  const weekday = document.createElement("span");
  // Repeats the column head, and is shown only on a phone, where the grid collapses
  // to one column and the heads are gone.
  weekday.className = "upnext-cal-weekday";
  weekday.textContent = weekdayFmt.format(day);
  date.appendChild(weekday);
  const num = document.createElement("span");
  // The month rides along on its first day, so a grid running out of September into
  // October says so without needing a second header.
  num.textContent = day.getDate() === 1 ? monthDayFmt.format(day) : String(day.getDate());
  date.appendChild(num);
  cell.appendChild(date);

  for (const entry of entries) cell.appendChild(buildUpNextCalItem(entry, data));
  return cell;
}

// buildUpNextCalItem is one rotation inside a day cell: what tier, which bosses, and
// how much longer it runs when that is past the day it opens on.
function buildUpNextCalItem(entry: UpcomingRaid, data: GameData): HTMLElement {
  const item = document.createElement("div");
  item.className = "upnext-cal-item";

  const names = entry.bosses.map((b) => pokeName(data, b.name));
  item.title = tierLabel(entry.tier, entry.shadow) + ": " + names.join(", ");

  const tier = document.createElement("div");
  tier.className = "upnext-cal-tier tier-" + entry.tier;
  tier.textContent = tierLabel(entry.tier, entry.shadow);
  item.appendChild(tier);

  const mons = document.createElement("div");
  mons.className = "upnext-cal-mons";
  for (const boss of entry.bosses) {
    if (!boss.image) continue;
    const img = document.createElement("img");
    img.src = boss.image;
    img.alt = pokeName(data, boss.name);
    img.loading = "lazy";
    img.decoding = "async";
    mons.appendChild(img);
  }
  if (mons.childElementCount) item.appendChild(mons);

  const label = document.createElement("div");
  label.className = "upnext-cal-names";
  label.textContent = entry.bosses.length > UPNEXT_CAL_NAMED
    ? RD.upNextCount.replace("{n}", String(entry.bosses.length))
    : names.join(", ");
  item.appendChild(label);

  // A rotation running past its opening day says so, because the cell it sits in only
  // marks the day it starts.
  const end = parseLocal(entry.ends_at);
  const start = parseLocal(entry.starts_at);
  if (end && start && end.toDateString() !== start.toDateString()) {
    const until = document.createElement("div");
    until.className = "upnext-cal-until";
    until.textContent = RD.windowUntil.replace("{date}", dateFmt.format(end));
    item.appendChild(until);
  }
  return item;
}

// buildUpNextLiveRow is a rotation that is already open but has no card on the grid.
// It sits above the calendar, as a line rather than a cell, because it has no start
// day worth filing it under any more.
function buildUpNextLiveRow(entry: UpcomingRaid, data: GameData): HTMLElement {
  const row = document.createElement("div");
  row.className = "upnext-row upnext-row-live";

  const tier = document.createElement("span");
  tier.className = "upnext-tier tier-" + entry.tier;
  tier.textContent = tierLabel(entry.tier, entry.shadow);
  row.appendChild(tier);

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
  row.appendChild(mons);

  const when = document.createElement("div");
  when.className = "upnext-when";
  const pill = windowPill(entry.starts_at, entry.ends_at, entry.live);
  if (pill) when.appendChild(pill);
  const note = document.createElement("span");
  note.className = "upnext-note";
  note.textContent = RD.upNextDetailsPending;
  when.appendChild(note);
  row.appendChild(when);

  return row;
}

// renderRaids paints the whole page from one game data blob.
function renderRaids(data: GameData): void {
  renderScope.abort();
  renderScope = new AbortController();
  app.innerHTML = "";

  // Every tier being present but empty counts as nothing to show, not as a grid of
  // empty tabs.
  if (!data.raids || !Object.values(data.raids).some((b) => b.length)) {
    app.innerHTML = `<div class="error-state">${RD.unavailable}</div>`;
    return;
  }

  app.appendChild(buildRaidsView(data));

  const upNext = buildUpNextSection(data);
  if (upNext) app.appendChild(upNext);

  if (data.maxBattles && Object.keys(data.maxBattles).length > 0) {
    app.appendChild(buildMaxBattlesSection(data));
  }
}

// paintedFrom fingerprints what is currently on screen, so a revalidation that
// brings back the same grid does not throw away the tab and filters the trainer had
// selected. Rotations change a few times a week; this is almost always a no-op.
let paintedFrom = "";

function raidsFingerprint(data: GameData): string {
  return JSON.stringify([data.raids ?? null, data.upcomingRaids ?? null, data.maxBattles ?? null]);
}

// refreshRaids re-reads the game data and repaints only if the grid actually
// changed.
//
// The server rebuilds the served list at the exact instant a rotation opens or
// shuts, and the blob revalidates rather than caching for five minutes, so this is
// the last link: without it a tab left open on this page shows the rotation that was
// running when it loaded, forever.
//
// Skipped while the tab is hidden, and run immediately when it comes back, because a
// backgrounded tab is the case where the grid has most likely gone stale and the one
// where polling helps nobody.
// MIN_REFRESH_MS is the floor between two revalidations, however they were
// triggered.
//
// /api/data allows 10 requests per 2 minutes per IP and it is shared with every
// other page: alt-tabbing back and forth ten times, or a household behind one
// address with a few tabs open, would spend that budget on nothing and then break
// the IV calculator and the pokedex, which read the same blob. The minute timer on
// its own is well inside the limit; this exists so the visibility trigger cannot
// stack on top of it.
const MIN_REFRESH_MS = 45_000;
let lastRefresh = Date.now();

async function refreshRaids(): Promise<void> {
  if (document.visibilityState !== "visible") return;
  if (Date.now() - lastRefresh < MIN_REFRESH_MS) return;
  lastRefresh = Date.now();
  try {
    const data = await reloadGameData();
    const print = raidsFingerprint(data);
    if (print === paintedFrom) return;
    paintedFrom = print;
    renderRaids(data);
  } catch (err) {
    // Keep whatever is on screen: a stale grid beats an error state.
    console.error(err);
  }
}

async function init() {
  try {
    const data = await loadGameData();
    paintedFrom = raidsFingerprint(data);
    renderRaids(data);

    // Countdowns go stale on their own, and the rotations behind them change on a
    // schedule nobody reloads a tab for.
    setInterval(tickRaidWindows, 60000);
    setInterval(refreshRaids, 60000);
    document.addEventListener("visibilitychange", refreshRaids);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
