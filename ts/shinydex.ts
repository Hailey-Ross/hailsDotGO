import { loadGameData, pokeName } from "./shared/gamedata";
import { fetchSpeciesData, fetchCryUrl, fetchFormSprites } from "./shared/pokedex";
import type { GameData, ShinyPokemon } from "./shared/types";

// Server-injected strings: JSC from templates/base.html, SD from templates/shinydex.html.
declare const JSC: Record<string, string>;
declare const SD: Record<string, string>;

const app = document.getElementById("shinydex-app")!;

// Shinies panel

interface ShinyGroup {
  id: string;
  label: string;
  icon: string;
  flag: keyof ShinyPokemon;
  tooltip: string;
}

const SHINY_GROUPS: ShinyGroup[] = [
  { id: "wild",      label: SD.groupWild,      icon: "🌿", flag: "found_wild",      tooltip: SD.tipWild },
  { id: "raid",      label: SD.groupRaid,      icon: "⚔️", flag: "found_raid",      tooltip: SD.tipRaid },
  { id: "egg",       label: SD.groupEgg,       icon: "🥚", flag: "found_egg",       tooltip: SD.tipEgg },
  { id: "research",  label: SD.groupResearch,  icon: "📋", flag: "found_research",  tooltip: SD.tipResearch },
  { id: "evolution", label: SD.groupEvolution, icon: "⬆️", flag: "found_evolution", tooltip: SD.tipEvolution },
  { id: "photobomb", label: SD.groupPhotobomb, icon: "📸", flag: "found_photobomb", tooltip: SD.tipPhotobomb },
];

let cryVolume = 0.3;

function buildShiniesPanel(data: GameData): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");

    if (!data.shinies || Object.keys(data.shinies).length === 0) {
      wrap.innerHTML = `<div class="error-state">${SD.unavailable}</div>`;
      return wrap;
    }

    const allShinies = Object.values(data.shinies);
    const groups = SHINY_GROUPS.map((g) => ({
      ...g,
      items: allShinies.filter((s) => s[g.flag] === true),
    })).filter((g) => g.items.length > 0);

    // Shared lightbox modal (one per panel render)
    const modal = document.createElement("div");
    modal.className = "shiny-modal";
    modal.innerHTML = `<div class="shiny-modal-inner">
      <div class="shiny-compare" id="shiny-compare-wrap">
        <div class="shiny-compare-side">
          <img class="shiny-compare-img" id="shiny-modal-normal" alt="" />
          <span>${JSC.formNormal}</span>
        </div>
        <div class="shiny-compare-side">
          <img class="shiny-compare-img" id="shiny-modal-shiny" alt="" />
          <span>✨ ${JSC.shiny}</span>
        </div>
      </div>
      <div class="shiny-modal-name-row">
        <span class="shiny-modal-name"></span>
      </div>
      <span class="poke-genus" id="shiny-modal-genus"></span>
      <span class="poke-legend-badge" id="shiny-modal-badge" style="display:none"></span>
      <div class="poke-cry-controls" id="shiny-cry-controls" style="display:none">
        <button class="poke-cry-btn" id="shiny-modal-cry" title="${JSC.playCry}">🔊</button>
        <input type="range" class="poke-volume-slider" id="shiny-modal-volume" min="0" max="100" value="100" title="${JSC.volume}">
        <span class="poke-volume-label" id="shiny-modal-vlabel">100%</span>
      </div>
      <p class="poke-flavor" id="shiny-modal-flavor" style="display:none"></p>
      <div class="shiny-modal-methods"></div>
    </div>`;
    document.body.appendChild(modal);
    // Only close when clicking the backdrop, not inner content
    modal.addEventListener("click", (e) => { if (e.target === modal) modal.classList.remove("open"); });
    const onEsc = (e: KeyboardEvent) => { if (e.key === "Escape") modal.classList.remove("open"); };
    document.addEventListener("keydown", onEsc);

    // Search input
    const searchInput = document.createElement("input");
    searchInput.type = "text";
    searchInput.className = "search-input";
    searchInput.placeholder = JSC.searchNPokemon.replace("{n}", String(allShinies.length));
    searchInput.style.width = "100%";
    searchInput.style.marginBottom = "1rem";
    wrap.appendChild(searchInput);

    // Method tabs
    const methodTabs = document.createElement("div");
    methodTabs.className = "shiny-method-tabs";

    const allBtn = document.createElement("button");
    allBtn.className = "method-tab active";
    allBtn.dataset.group = "all";
    allBtn.textContent = SD.allN.replace("{n}", String(allShinies.length));
    methodTabs.appendChild(allBtn);

    for (const g of groups) {
      const btn = document.createElement("button");
      btn.className = "method-tab";
      btn.dataset.group = g.id;
      btn.dataset.tooltip = g.tooltip;
      btn.textContent = `${g.icon} ${g.label} (${g.items.length})`;
      methodTabs.appendChild(btn);
    }
    wrap.appendChild(methodTabs);

    const grid = document.createElement("div");
    grid.className = "shiny-grid";
    wrap.appendChild(grid);

    let activeGroup = "all";

    function renderGrid() {
      grid.innerHTML = "";
      const filter = searchInput.value.trim().toLowerCase();
      const source = activeGroup === "all"
        ? allShinies
        : groups.find((g) => g.id === activeGroup)?.items ?? allShinies;

      const filtered = filter
        ? source.filter((s) => s.name.toLowerCase().includes(filter))
        : source;

      if (!filtered.length) {
        grid.innerHTML = `<p class="empty-state">${JSC.noResults}</p>`;
        return;
      }

      for (const s of [...filtered].sort((a, b) => a.id - b.id)) {
        const card = document.createElement("div");
        card.className = "shiny-tag";

        const img = document.createElement("img");
        img.src = `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${s.id}.png`;
        img.alt = s.name;
        img.className = "shiny-img";
        img.loading = "lazy";
        img.decoding = "async";
        img.onerror = () => { img.style.display = "none"; };

        const label = document.createElement("span");
        label.className = "shiny-label";
        label.textContent = pokeName(data, s.name);

        card.appendChild(img);
        card.appendChild(label);

        card.addEventListener("click", () => {
          (document.getElementById("shiny-modal-normal") as HTMLImageElement).src =
            `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/${s.id}.png`;
          (document.getElementById("shiny-modal-shiny") as HTMLImageElement).src =
            `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${s.id}.png`;
          (modal.querySelector(".shiny-modal-name") as HTMLElement).textContent =
            pokeName(data, s.name);

          const flavorEl   = document.getElementById("shiny-modal-flavor")   as HTMLElement;
          const genusEl    = document.getElementById("shiny-modal-genus")    as HTMLElement;
          const badgeEl    = document.getElementById("shiny-modal-badge")    as HTMLElement;
          const cryPanel   = document.getElementById("shiny-cry-controls")   as HTMLElement;
          const cryBtn     = document.getElementById("shiny-modal-cry")      as HTMLButtonElement;
          const volSlider  = document.getElementById("shiny-modal-volume")   as HTMLInputElement;
          const volLabel   = document.getElementById("shiny-modal-vlabel")   as HTMLElement;
          const compareWrap = document.getElementById("shiny-compare-wrap")  as HTMLElement;
          flavorEl.textContent = ""; flavorEl.style.display = "none";
          genusEl.textContent  = "";
          badgeEl.textContent  = ""; badgeEl.style.display = "none";
          cryPanel.style.display = "none"; cryBtn.onclick = null;
          // Remove any primal panels from a previous open
          compareWrap.querySelectorAll(".shiny-compare-side--extra").forEach(el => el.remove());

          // Keep slider in sync with shared volume
          volSlider.value = String(Math.round((cryVolume / 0.3) * 100));
          volLabel.textContent = `${volSlider.value}%`;
          volSlider.oninput = () => {
            cryVolume = (Number(volSlider.value) / 100) * 0.3;
            volLabel.textContent = `${volSlider.value}%`;
          };

          fetchSpeciesData(s.id).then(d => {
            if (d.flavor) { flavorEl.textContent = d.flavor; flavorEl.style.display = ""; }
            if (d.genus)  { genusEl.textContent  = JSC.theGenus.replace("{genus}", d.genus); }
            if (d.isLegendary || d.isMythical) {
              badgeEl.textContent  = d.isMythical ? JSC.mythical : JSC.legendary;
              badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
              badgeEl.style.display = "";
            }
            for (const variety of d.varieties.filter(v => v.includes("primal"))) {
              fetchFormSprites(variety).then(sprites => {
                if (!sprites.normal && !sprites.shiny) return;
                if (sprites.normal) {
                  const side = document.createElement("div");
                  side.className = "shiny-compare-side shiny-compare-side--extra";
                  side.innerHTML = `<img class="shiny-compare-img" src="${sprites.normal}" alt="${JSC.formPrimal}"><span>🌋 ${JSC.formPrimal}</span>`;
                  compareWrap.appendChild(side);
                }
                if (sprites.shiny) {
                  const side = document.createElement("div");
                  side.className = "shiny-compare-side shiny-compare-side--extra";
                  side.innerHTML = `<img class="shiny-compare-img" src="${sprites.shiny}" alt="${JSC.primalShiny}"><span>✨ ${JSC.primalShiny}</span>`;
                  compareWrap.appendChild(side);
                }
              });
            }
          });
          fetchCryUrl(s.id).then(url => {
            if (!url) return;
            cryPanel.style.display = "";
            cryBtn.onclick = (e) => {
              e.stopPropagation();
              const a = new Audio(url);
              a.volume = cryVolume;
              a.play();
            };
          });

          const methodsEl = modal.querySelector(".shiny-modal-methods") as HTMLElement;
          methodsEl.innerHTML = "";
          const obtainMethods = SHINY_GROUPS.filter((g) => s[g.flag] === true);
          if (obtainMethods.length) {
            const lbl = document.createElement("span");
            lbl.className = "shiny-modal-methods-label";
            lbl.textContent = SD.howToFind;
            methodsEl.appendChild(lbl);
            for (const g of obtainMethods) {
              const chip = document.createElement("span");
              chip.className = "shiny-method-chip";
              chip.textContent = `${g.icon} ${g.label}`;
              methodsEl.appendChild(chip);
            }
          }

          modal.classList.add("open");
        });

        grid.appendChild(card);
      }
    }

    methodTabs.addEventListener("click", (e) => {
      const btn = (e.target as HTMLElement).closest(".method-tab") as HTMLButtonElement | null;
      if (!btn) return;
      methodTabs.querySelectorAll(".method-tab").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      activeGroup = btn.dataset.group ?? "all";
      renderGrid();
    });

    searchInput.addEventListener("input", renderGrid);
    renderGrid();

    return wrap;
  };
}

// Init

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";
    app.appendChild(buildShiniesPanel(data)());
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
