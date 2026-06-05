import { loadGameData } from "./shared/gamedata";
import { calcCounters, renderCounterTable } from "./shared/counters";
import { typeBadge, TYPE_COLORS } from "./shared/typecolors";
import type { GameData, RaidBoss } from "./shared/types";

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
        // Toggle off if already active
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
