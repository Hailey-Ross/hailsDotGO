import { loadGameData } from "./shared/gamedata";
import { typeBadge } from "./shared/typecolors";
import type { GameData, ShinyPokemon } from "./shared/types";

const app = document.getElementById("events-app")!;

// ── Shinies panel ─────────────────────────────────────────────

interface ShinyGroup {
  id: string;
  label: string;
  icon: string;
  flag: keyof ShinyPokemon;
}

const SHINY_GROUPS: ShinyGroup[] = [
  { id: "wild",      label: "Wild",       icon: "🌿", flag: "shiny_found_wild" },
  { id: "raid",      label: "Raids",      icon: "⚔️", flag: "shiny_found_raid" },
  { id: "egg",       label: "Eggs",       icon: "🥚", flag: "shiny_found_egg" },
  { id: "research",  label: "Research",   icon: "📋", flag: "shiny_found_research" },
  { id: "evolution", label: "Evolution",  icon: "⬆️", flag: "shiny_found_evolution" },
  { id: "photobomb", label: "Photobomb",  icon: "📸", flag: "shiny_found_photobomb" },
];

function buildShiniesPanel(data: GameData): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");

    if (!data.shinies || Object.keys(data.shinies).length === 0) {
      wrap.innerHTML = `<div class="error-state">Shiny data unavailable. Check back later.</div>`;
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
      <img class="shiny-modal-img" alt="" />
      <span class="shiny-modal-name"></span>
      <div class="shiny-modal-methods"></div>
    </div>`;
    document.body.appendChild(modal);
    modal.addEventListener("click", () => modal.classList.remove("open"));
    const onEsc = (e: KeyboardEvent) => { if (e.key === "Escape") modal.classList.remove("open"); };
    document.addEventListener("keydown", onEsc);

    // Search input
    const searchInput = document.createElement("input");
    searchInput.type = "text";
    searchInput.className = "search-input";
    searchInput.placeholder = `Search ${allShinies.length} Pokémon…`;
    searchInput.style.width = "100%";
    searchInput.style.marginBottom = "1rem";
    wrap.appendChild(searchInput);

    // Method tabs
    const methodTabs = document.createElement("div");
    methodTabs.className = "shiny-method-tabs";

    const allBtn = document.createElement("button");
    allBtn.className = "method-tab active";
    allBtn.dataset.group = "all";
    allBtn.textContent = `All (${allShinies.length})`;
    methodTabs.appendChild(allBtn);

    for (const g of groups) {
      const btn = document.createElement("button");
      btn.className = "method-tab";
      btn.dataset.group = g.id;
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
        grid.innerHTML = `<p class="empty-state">No results.</p>`;
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
        label.textContent = s.name.replace(/_/g, " ");

        card.appendChild(img);
        card.appendChild(label);

        card.addEventListener("click", () => {
          (modal.querySelector(".shiny-modal-img") as HTMLImageElement).src =
            `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${s.id}.png`;
          (modal.querySelector(".shiny-modal-name") as HTMLElement).textContent =
            s.name.replace(/_/g, " ");

          const methodsEl = modal.querySelector(".shiny-modal-methods") as HTMLElement;
          methodsEl.innerHTML = "";
          const obtainMethods = SHINY_GROUPS.filter((g) => s[g.flag] === true);
          if (obtainMethods.length) {
            const lbl = document.createElement("span");
            lbl.className = "shiny-modal-methods-label";
            lbl.textContent = "How to find:";
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

// ── Init ──────────────────────────────────────────────────────

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";
    app.appendChild(buildShiniesPanel(data)());
  } catch (err) {
    app.innerHTML = `<div class="error-state">Failed to load data. Please try again later.</div>`;
    console.error(err);
  }
}

init();
