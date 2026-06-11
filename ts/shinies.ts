import { loadGameData, pokeName } from "./shared/gamedata";
import { fetchSpeciesData, fetchCryUrl, fetchFormSprites } from "./shared/pokedex";
import type { GameData, ShinyPokemon } from "./shared/types";

// Server-injected strings: JSC and SITE_LANG from templates/base.html, SH from templates/shinies.html.
declare const JSC: Record<string, string>;
declare const SH: Record<string, string>;
declare const SITE_LANG: string;

const CSRF_TOKEN = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

let cryVolume = 0.3;

interface UserShiny {
  id: number;
  pokemon_id: string;
  form: string;
  method: string;
  caught_at: string;
}

const FORMS = [
  { value: "", label: JSC.formNormal },
  { value: "shadow", label: JSC.formShadow },
  { value: "purified", label: JSC.formPurified },
];

const METHODS = [
  { value: "", label: SH.methodAny },
  { value: "wild", label: SH.methodWild },
  { value: "egg", label: SH.methodEgg },
  { value: "raid", label: SH.methodRaid },
  { value: "research", label: SH.methodResearch },
  { value: "evolution", label: SH.methodEvolution },
  { value: "photobomb", label: SH.methodPhotobomb },
  { value: "trade", label: SH.methodTrade },
  { value: "go_tour", label: SH.methodGoTour },
];

function spriteUrl(id: number) {
  return `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;
}

async function fetchUserShinies(): Promise<UserShiny[]> {
  const res = await fetch("/api/shinies");
  if (!res.ok) return [];
  return res.json();
}

async function apiAdd(pokemonId: string, form: string, method: string): Promise<boolean> {
  const res = await fetch("/api/shinies", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ pokemon_id: pokemonId, form, method }),
  });
  return res.ok;
}

async function apiUpdate(id: number, form: string, method: string): Promise<Response> {
  return fetch(`/api/shinies/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ form, method }),
  });
}

async function apiRemove(id: number): Promise<boolean> {
  const res = await fetch(`/api/shinies/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": CSRF_TOKEN },
  });
  return res.ok;
}

function buildCaughtIndex(shinies: UserShiny[]): Map<string, UserShiny> {
  const m = new Map<string, UserShiny>();
  for (const s of shinies) m.set(`${s.pokemon_id}:${s.form}`, s);
  return m;
}

function anyFormCaught(name: string, index: Map<string, UserShiny>): boolean {
  for (const key of index.keys()) {
    if (key.startsWith(`${name}:`)) return true;
  }
  return false;
}

function makeSelect(options: { value: string; label: string }[], selected: string, disabled: string[] = []) {
  const sel = document.createElement("select");
  sel.className = "move-select";
  for (const o of options) {
    const opt = document.createElement("option");
    opt.value = o.value;
    opt.textContent = o.label;
    if (o.value === selected) opt.selected = true;
    if (disabled.includes(o.value)) opt.disabled = true;
    sel.appendChild(opt);
  }
  return sel;
}

async function init() {
  const app = document.getElementById("shinies-app")!;

  let gameData: GameData;
  let userShinies: UserShiny[];
  try {
    [gameData, userShinies] = await Promise.all([loadGameData(), fetchUserShinies()]);
  } catch {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    return;
  }

  if (!gameData.shinies || Object.keys(gameData.shinies).length === 0) {
    app.innerHTML = `<div class="error-state">${SH.unavailable}</div>`;
    return;
  }

  const allShinies = Object.values(gameData.shinies);
  const shinyByName = new Map(allShinies.map((s) => [s.name, s]));
  let caughtIndex = buildCaughtIndex(userShinies);

  // ── Layout ───────────────────────────────────────────────────

  app.innerHTML = `
    <div class="page-header">
      <h1>${SH.heading}</h1>
      <p id="sc-counter" class="page-header-sub"></p>
    </div>
    <div class="tabs" id="sc-tabs">
      <button class="tab-btn active" data-tab="all">${SH.tabAll}</button>
      <button class="tab-btn" data-tab="caught">${SH.tabCaught}</button>
      <button class="tab-btn" data-tab="missing">${SH.tabMissing}</button>
    </div>
    <input id="sc-search" type="text" class="search-input"
           style="width:100%;margin-bottom:1rem;display:block"
           placeholder="${JSC.search}">
    <div id="sc-content"></div>
  `;

  const counterEl  = document.getElementById("sc-counter")!;
  const tabsEl     = document.getElementById("sc-tabs")!;
  const searchEl   = document.getElementById("sc-search") as HTMLInputElement;
  const contentEl  = document.getElementById("sc-content")!;

  // ── Add modal ────────────────────────────────────────────────

  const modal = document.createElement("div");
  modal.className = "sc-modal";
  modal.innerHTML = `
    <div class="sc-modal-inner">
      <button class="sc-modal-close">&times;</button>
      <div class="shiny-compare" id="sc-compare-wrap">
        <div class="shiny-compare-side">
          <img class="shiny-compare-img" id="sc-modal-normal" alt="">
          <span>${JSC.formNormal}</span>
        </div>
        <div class="shiny-compare-side">
          <img class="shiny-compare-img sc-modal-img" id="sc-modal-shiny" alt="">
          <span>✨ ${JSC.shiny}</span>
        </div>
      </div>
      <div class="shiny-modal-name-row">
        <div class="sc-modal-name"></div>
      </div>
      <div class="poke-cry-controls" id="sc-cry-controls" style="display:none">
        <button class="poke-cry-btn" id="sc-modal-cry" title="${JSC.playCry}">🔊</button>
        <input type="range" class="poke-volume-slider" id="sc-modal-volume" min="0" max="100" value="100" title="${JSC.volume}">
        <span class="poke-volume-label" id="sc-modal-vlabel">100%</span>
      </div>
      <span class="poke-genus" id="sc-modal-genus"></span>
      <span class="poke-legend-badge" id="sc-modal-badge" style="display:none"></span>
      <p class="poke-flavor" id="sc-modal-flavor" style="display:none"></p>
      <div class="sc-add-title">${SH.addToCollection}</div>
      <div id="sc-modal-fields" style="width:100%;display:flex;flex-direction:column;gap:0.5rem"></div>
      <button class="btn-primary" id="sc-modal-add" style="width:100%;margin-top:0.25rem">${SH.add}</button>
      <div id="sc-modal-status" class="sc-status"></div>
    </div>
  `;
  document.body.appendChild(modal);

  const modalName   = modal.querySelector(".sc-modal-name") as HTMLElement;
  const modalFields = document.getElementById("sc-modal-fields")!;
  const modalAddBtn = document.getElementById("sc-modal-add") as HTMLButtonElement;
  const modalStatus = document.getElementById("sc-modal-status")!;

  modal.querySelector(".sc-modal-close")!.addEventListener("click", closeModal);
  modal.addEventListener("click", (e) => { if (e.target === modal) closeModal(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeModal(); });

  function closeModal() { modal.classList.remove("open"); }

  let modalFormSel: HTMLSelectElement;
  let modalMethodSel: HTMLSelectElement;
  let modalTarget: ShinyPokemon | null = null;

  function openAddModal(s: ShinyPokemon) {
    modalTarget = s;
    (document.getElementById("sc-modal-normal") as HTMLImageElement).src =
      `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/${s.id}.png`;
    (document.getElementById("sc-modal-shiny") as HTMLImageElement).src = spriteUrl(s.id);
    modalName.textContent = pokeName(gameData, s.name);
    modalStatus.textContent = "";

    const flavorP    = document.getElementById("sc-modal-flavor")  as HTMLElement;
    const genusEl    = document.getElementById("sc-modal-genus")   as HTMLElement;
    const badgeEl    = document.getElementById("sc-modal-badge")   as HTMLElement;
    const cryPanel   = document.getElementById("sc-cry-controls")  as HTMLElement;
    const cryBtn     = document.getElementById("sc-modal-cry")     as HTMLButtonElement;
    const volSlider  = document.getElementById("sc-modal-volume")  as HTMLInputElement;
    const volLabel   = document.getElementById("sc-modal-vlabel")  as HTMLElement;
    const compareWrap = document.getElementById("sc-compare-wrap") as HTMLElement;
    flavorP.textContent = ""; flavorP.style.display = "none";
    genusEl.textContent  = "";
    badgeEl.textContent  = ""; badgeEl.style.display = "none";
    cryPanel.style.display = "none"; cryBtn.onclick = null;
    compareWrap.querySelectorAll(".shiny-compare-side--extra").forEach(el => el.remove());

    volSlider.value = String(Math.round((cryVolume / 0.3) * 100));
    volLabel.textContent = `${volSlider.value}%`;
    volSlider.oninput = () => {
      cryVolume = (Number(volSlider.value) / 100) * 0.3;
      volLabel.textContent = `${volSlider.value}%`;
    };

    fetchSpeciesData(s.id).then(d => {
      if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
      if (d.genus)  { genusEl.textContent = JSC.theGenus.replace("{genus}", d.genus); }
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
    modalAddBtn.disabled = false;
    modalAddBtn.textContent = SH.add;

    // Disable forms already caught
    const caughtForms = FORMS.filter((f) => caughtIndex.has(`${s.name}:${f.value}`)).map((f) => f.value);
    modalFields.innerHTML = "";
    modalFormSel = makeSelect(FORMS, "", caughtForms);
    modalMethodSel = makeSelect(METHODS, "");
    modalFields.appendChild(modalFormSel);
    modalFields.appendChild(modalMethodSel);

    // Pick first uncaught form by default
    const firstAvailable = FORMS.find((f) => !caughtForms.includes(f.value));
    if (firstAvailable) modalFormSel.value = firstAvailable.value;

    modal.classList.add("open");
  }

  modalAddBtn.addEventListener("click", async () => {
    if (!modalTarget) return;
    const form   = modalFormSel.value;
    const method = modalMethodSel.value;

    if (caughtIndex.has(`${modalTarget.name}:${form}`)) {
      modalStatus.textContent = SH.alreadyOwned;
      return;
    }

    modalAddBtn.disabled = true;
    modalAddBtn.textContent = SH.adding;
    modalStatus.textContent = "";

    const ok = await apiAdd(modalTarget.name, form, method);
    if (ok) {
      userShinies  = await fetchUserShinies();
      caughtIndex  = buildCaughtIndex(userShinies);
      updateCounter();
      renderTab();
      closeModal();
    } else {
      modalStatus.textContent = JSC.somethingWrong;
      modalAddBtn.disabled = false;
      modalAddBtn.textContent = SH.add;
    }
  });

  // ── Helpers ───────────────────────────────────────────────────

  function timeAgo(dateStr: string): string {
    const d    = new Date(dateStr);
    const diff = Date.now() - d.getTime();
    const days = Math.floor(diff / 86400000);
    const locale = typeof SITE_LANG !== "undefined" ? SITE_LANG : "en-GB";
    if (days === 0) return SH.today;
    if (days === 1) return SH.yesterday;
    if (days < 30)  return SH.daysAgo.replace("{n}", String(days));
    if (days < 365) return d.toLocaleDateString(locale, { month: "short", day: "numeric" });
    return d.toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
  }

  // ── Counter / Stats bar ──────────────────────────────────────

  const METHOD_ICONS: Record<string, string> = {
    wild: "🌿", egg: "🥚", raid: "⚔️", research: "📋",
    evolution: "⬆️", photobomb: "📸", trade: "🤝", go_tour: "🎟️",
  };

  function updateCounter() {
    const unique = new Set(Array.from(caughtIndex.keys()).map((k) => k.split(":")[0])).size;
    const total  = userShinies.length;

    const methodCounts: Record<string, number> = {};
    for (const s of userShinies) {
      if (s.method) methodCounts[s.method] = (methodCounts[s.method] ?? 0) + 1;
    }

    const chipsHtml = Object.entries(methodCounts)
      .sort(([, a], [, b]) => b - a)
      .map(([m, n]) => {
        const icon = METHOD_ICONS[m] ?? "";
        const label = METHODS.find((o) => o.value === m)?.label ?? m.replace(/_/g, " ");
        return `<span class="sc-stat-chip">${icon} ${label} (${n})</span>`;
      }).join("");

    const counts = SH.counts
      .replace("{unique}", String(unique))
      .replace("{total}", String(total));
    counterEl.innerHTML =
      `<span class="sc-stat-counts">${counts}</span>` +
      (chipsHtml ? `<span class="sc-stat-chips">${chipsHtml}</span>` : "");
  }

  // ── Render: All / Missing grid ───────────────────────────────

  function renderGrid(source: ShinyPokemon[]) {
    const q = searchEl.value.trim().toLowerCase();
    const filtered = q ? source.filter((s) => s.name.toLowerCase().includes(q)) : source;

    if (!filtered.length) {
      contentEl.innerHTML = `<p class="empty-state">${JSC.noResults}</p>`;
      return;
    }

    const grid = document.createElement("div");
    grid.className = "shiny-grid";

    for (const s of [...filtered].sort((a, b) => a.id - b.id)) {
      const caught = anyFormCaught(s.name, caughtIndex);
      const card = document.createElement("div");
      card.className = "shiny-tag" + (caught ? " sc-caught" : "");

      if (caught) {
        const badge = document.createElement("span");
        badge.className = "sc-badge";
        badge.textContent = "✓";
        card.appendChild(badge);
      }

      const img = document.createElement("img");
      img.src = spriteUrl(s.id);
      img.alt = s.name;
      img.className = "shiny-img";
      img.loading = "lazy";
      img.decoding = "async";
      img.onerror = () => { img.style.display = "none"; };

      const label = document.createElement("span");
      label.className = "shiny-label";
      label.textContent = pokeName(gameData, s.name);

      card.appendChild(img);
      card.appendChild(label);
      card.addEventListener("click", () => openAddModal(s));
      grid.appendChild(card);
    }

    contentEl.innerHTML = "";
    contentEl.appendChild(grid);
  }

  // ── Render: Caught list ───────────────────────────────────────

  function renderCaughtList() {
    const q = searchEl.value.trim().toLowerCase();
    const entries = userShinies.filter((s) =>
      !q || s.pokemon_id.toLowerCase().includes(q)
    );

    contentEl.innerHTML = "";

    if (!entries.length) {
      contentEl.innerHTML = q
        ? `<p class="empty-state">${JSC.noResults}</p>`
        : `<p class="empty-state">${SH.nothingCaught}</p>`;
      return;
    }

    const list = document.createElement("div");
    list.className = "sc-list";

    for (const rec of entries) {
      const poke = shinyByName.get(rec.pokemon_id);
      const row  = document.createElement("div");
      row.className = "sc-entry";

      // Sprite
      const img = document.createElement("img");
      img.className = "sc-entry-img";
      img.alt = rec.pokemon_id;
      if (poke) {
        img.src = spriteUrl(poke.id);
        img.onerror = () => { img.style.display = "none"; };
      } else {
        img.style.display = "none";
      }

      // Name + date
      const nameWrap = document.createElement("div");
      nameWrap.className = "sc-entry-namewrap";
      const name = document.createElement("span");
      name.className = "sc-entry-name";
      name.textContent = pokeName(gameData, rec.pokemon_id);
      const dateEl = document.createElement("span");
      dateEl.className = "sc-caught-date";
      dateEl.textContent = rec.caught_at ? timeAgo(rec.caught_at) : "";
      nameWrap.appendChild(name);
      nameWrap.appendChild(dateEl);

      // Form selector
      const formSel = makeSelect(FORMS, rec.form);

      // Method selector
      const methodSel = makeSelect(METHODS, rec.method);

      // Save status
      const statusEl = document.createElement("span");
      statusEl.className = "sc-save-status";

      let saveTimer: ReturnType<typeof setTimeout>;

      async function saveUpdate() {
        const newForm   = formSel.value;
        const newMethod = methodSel.value;
        const res = await apiUpdate(rec.id, newForm, newMethod);
        if (res.ok) {
          caughtIndex.delete(`${rec.pokemon_id}:${rec.form}`);
          rec.form   = newForm;
          rec.method = newMethod;
          caughtIndex.set(`${rec.pokemon_id}:${rec.form}`, rec);
          statusEl.textContent = SH.saved;
          clearTimeout(saveTimer);
          saveTimer = setTimeout(() => { statusEl.textContent = ""; }, 1500);
        } else if (res.status === 409) {
          formSel.value   = rec.form;
          methodSel.value = rec.method;
          statusEl.textContent = SH.alreadyCaught;
          clearTimeout(saveTimer);
          saveTimer = setTimeout(() => { statusEl.textContent = ""; }, 2000);
        } else {
          formSel.value   = rec.form;
          methodSel.value = rec.method;
          statusEl.textContent = JSC.error;
        }
      }

      formSel.addEventListener("change", saveUpdate);
      methodSel.addEventListener("change", saveUpdate);

      // Remove button
      const removeBtn = document.createElement("button");
      removeBtn.className = "sc-remove-btn";
      removeBtn.textContent = JSC.remove;
      removeBtn.addEventListener("click", async () => {
        removeBtn.disabled = true;
        removeBtn.textContent = "…";
        const ok = await apiRemove(rec.id);
        if (ok) {
          userShinies = await fetchUserShinies();
          caughtIndex = buildCaughtIndex(userShinies);
          updateCounter();
          renderCaughtList();
        } else {
          removeBtn.disabled = false;
          removeBtn.textContent = JSC.remove;
        }
      });

      row.appendChild(img);
      row.appendChild(nameWrap);
      row.appendChild(formSel);
      row.appendChild(methodSel);
      row.appendChild(statusEl);
      row.appendChild(removeBtn);
      list.appendChild(row);
    }

    contentEl.appendChild(list);
  }

  // ── Tab routing ──────────────────────────────────────────────

  let activeTab = "all";

  function renderTab() {
    if (activeTab === "caught") {
      searchEl.placeholder = SH.filterCaught;
      renderCaughtList();
    } else {
      const source = activeTab === "missing"
        ? allShinies.filter((s) => !anyFormCaught(s.name, caughtIndex))
        : allShinies;
      searchEl.placeholder = JSC.searchNPokemon.replace("{n}", String(source.length));
      renderGrid(source);
    }
  }

  tabsEl.addEventListener("click", (e) => {
    const btn = (e.target as HTMLElement).closest(".tab-btn") as HTMLButtonElement | null;
    if (!btn) return;
    tabsEl.querySelectorAll(".tab-btn").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    activeTab = btn.dataset.tab ?? "all";
    renderTab();
  });

  searchEl.addEventListener("input", renderTab);

  updateCounter();
  renderTab();
}

init();
