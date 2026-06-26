import { loadGameData, pokeName } from "./shared/gamedata";
import { fetchSpeciesData, fetchCryUrl, fetchFormSprites } from "./shared/pokedex";
import { costumeShinyUrl, TINY_POKEMON } from "./shared/costumes";
import { EVOLUTION_NEXT } from "./shared/evolutions";
import type { GameData, ShinyPokemon } from "./shared/types";

declare const JSC: Record<string, string>;
declare const SH: Record<string, string>;
declare const SITE_LANG: string;

const CSRF_TOKEN = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

let cryVolume = 0.3;

interface UserShiny {
  id: number;
  pokemon_id: string;
  form: string;
  costume: string;
  event_tag: string;
  method: string;
  caught_at: string;
  evolved_at: string | null;
}

const EVENT_OPTIONS: { value: string; label: string }[] = [
  // Recurring
  { value: "Community Day",              label: "Community Day" },
  { value: "Spotlight Hour",             label: "Spotlight Hour" },
  { value: "Raid Day",                   label: "Raid Day" },
  { value: "Raid Hour",                  label: "Raid Hour" },
  { value: "GO Battle Day",              label: "GO Battle Day" },
  { value: "GO Battle Week",             label: "GO Battle Week" },
  // GO Fest
  { value: "GO Fest 2018",               label: "GO Fest 2018" },
  { value: "GO Fest 2019",               label: "GO Fest 2019" },
  { value: "GO Fest 2020",               label: "GO Fest 2020" },
  { value: "GO Fest 2021",               label: "GO Fest 2021" },
  { value: "GO Fest 2022",               label: "GO Fest 2022" },
  { value: "GO Fest 2023",               label: "GO Fest 2023" },
  { value: "GO Fest 2024",               label: "GO Fest 2024" },
  { value: "GO Fest 2025",               label: "GO Fest 2025" },
  { value: "GO Fest 2026",               label: "GO Fest 2026" },
  // GO Tour
  { value: "GO Tour: Kanto",             label: "GO Tour: Kanto" },
  { value: "GO Tour: Johto",             label: "GO Tour: Johto" },
  { value: "GO Tour: Hoenn",             label: "GO Tour: Hoenn" },
  { value: "GO Tour: Sinnoh",            label: "GO Tour: Sinnoh" },
  { value: "GO Tour: Unova",             label: "GO Tour: Unova" },
  // Seasonal
  { value: "Halloween",                  label: "Halloween" },
  { value: "Winter Holiday",             label: "Winter Holiday" },
  { value: "Valentine's Day",            label: "Valentine's Day" },
  { value: "Spring",                     label: "Spring" },
  { value: "Summer",                     label: "Summer" },
  // Other
  { value: "Safari Zone",                label: "Safari Zone" },
  { value: "Pokemon Day",                label: "Pokemon Day" },
  { value: "GO Anniversary",             label: "GO Anniversary" },
  { value: "World Championships",        label: "World Championships" },
  { value: "Max Out",                    label: "Max Out" },
  { value: "World of Wonders",           label: "World of Wonders" },
  { value: "Shared Skies",               label: "Shared Skies" },
  { value: "Adventures Abound",          label: "Adventures Abound" },
  { value: "Timeless Travels",           label: "Timeless Travels" },
  { value: "Mythical Wishes",            label: "Mythical Wishes" },
];

const FORMS = [
  { value: "", label: JSC.formNormal },
  { value: "shadow", label: JSC.formShadow },
  { value: "purified", label: JSC.formPurified },
];

// Costumes valid for any Pokémon (shown in datalist regardless of species)
const GENERIC_COSTUMES = [
  "Party Hat", "Flower Crown", "Witch Hat",
];
// Costumes shown only when the matching species is selected
const POKEMON_EXTRA_COSTUMES: Record<string, string[]> = {
  "Pikachu": [
    "Ash Hat",
    "Red's Hat", "Leaf's Hat",
    "Lucas's Hat", "Dawn's Hat",
    "Hilbert's Hat", "Hilda's Hat", "Nate's Visor", "Rosa's Visor",
    "Akari's Kerchief",
    "Detective Hat", "Straw Hat", "World Cap", "Safari Hat", "Cake Hat",
    "Rock Star", "Pop Star", "Libre", "Dracula",
    "Kariyushi Shirt", "Blue Shirt", "Green Shirt", "Purple Shirt", "Batik Shirt",
    "Pyrite Crown", "Quartz Crown", "Malachite Crown", "Aquamarine Crown", "Amethyst Crown",
    "Sun Crown", "Moon Crown", "Top Hat", "Team Instinct Hat", "Team Mystic Hat",
    "Santa Hat", "Holiday Outfit",
  ],
  "Pichu":     ["Santa Hat"],
  "Raichu":    ["Santa Hat", "Ash Hat"],
  "Eevee":     ["Sun Crown", "Moon Crown"],
  "Espeon":    ["Day Scarf"],
  "Umbreon":   ["Night Scarf"],
  "Squirtle":  ["Sunglasses"],
  "Wartortle": ["Sunglasses"],
  "Blastoise": ["Sunglasses"],
  "Snorlax":   ["Nightcap", "Studded Jacket"],
  "Slowpoke":  ["New Year Costume"],
  "Slowbro":   ["New Year Costume"],
  "Slowking":  ["New Year Costume"],
  "Jigglypuff": ["Ribbon"],
  "Wigglytuff": ["Ribbon"],
  "Noibat":    ["Headband"],
  "Noivern":   ["Headband"],
  "Cubchoo":   ["Holiday Outfit"],
  "Beartic":   ["Holiday Outfit"],
  "Delibird":  ["Holiday Ribbon", "Holiday Outfit"],
  "Spheal":    ["Festive Outfit"],
  "Sealeo":    ["Festive Outfit"],
  "Walrein":   ["Festive Outfit"],
  "Psyduck":   ["Holiday Attire"],
  "Gengar":    ["Halloween Costume"],
  "Lapras":    ["Drip Scarf"],
  "Dragonite": ["Bowtie & Sunglasses"],
  "Minccino":  ["Fashion Outfit"],
  "Cinccino":  ["Fashion Outfit"],
  "Shinx":     ["Fashion Outfit"],
  "Luxio":     ["Fashion Outfit"],
  "Luxray":    ["Fashion Outfit"],
  "Kirlia":    ["Fashion Outfit"],
  "Gardevoir": ["Fashion Outfit"],
  "Croagunk":  ["Fashion Outfit"],
  "Toxicroak": ["Fashion Outfit"],
  "Diglett":   ["Fashion Outfit"],
  "Butterfree":["Fashion Outfit"],
  "Aerodactyl":["Satchel"],
  "Caterpie":  ["Cowboy Hat"],
  "Cubone":    ["Cempasúchil Crown"],
  "Ponyta":    ["Candela Costume"],
  "Ditto":     ["Hat", "Cap"],
  "Vulpix":    ["Spooky Festival Costume"],
};

const METHODS = [
  { value: "", label: SH.methodAny },
  { value: "wild", label: SH.methodWild },
  { value: "egg", label: SH.methodEgg },
  { value: "raid", label: SH.methodRaid },
  { value: "research", label: SH.methodResearch },
  { value: "evolution", label: SH.methodEvolution },
  { value: "photobomb", label: SH.methodPhotobomb },
  { value: "trade", label: SH.methodTrade },
  { value: "go_pass", label: SH.methodGoPass },
  { value: "go_tour", label: SH.methodGoTour },
];

function setSprite(img: HTMLImageElement, dexId: number, pokemonName: string, costume: string) {
  const costumeUrl = costume ? costumeShinyUrl(dexId, pokemonName, costume) : null;
  const fallback = spriteUrl(dexId);
  if (costumeUrl) {
    img.src = costumeUrl;
    img.onerror = () => { img.src = fallback; img.onerror = () => { img.style.display = "none"; }; };
  } else {
    img.src = fallback;
    img.onerror = () => { img.style.display = "none"; };
  }
}

function refreshCostumeDatalist(pokemonName: string) {
  const dl = document.getElementById("sc-costume-list") as HTMLDataListElement;
  if (!dl) return;
  dl.innerHTML = "";
  const seen = new Set<string>();
  for (const c of [...GENERIC_COSTUMES, ...(POKEMON_EXTRA_COSTUMES[pokemonName] ?? [])]) {
    if (seen.has(c)) continue;
    seen.add(c);
    const opt = document.createElement("option");
    opt.value = c;
    dl.appendChild(opt);
  }
}

function spriteUrl(id: number) {
  return `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;
}

async function fetchUserShinies(): Promise<UserShiny[]> {
  const res = await fetch("/api/shinies");
  if (!res.ok) return [];
  return res.json();
}

async function apiAdd(pokemonId: string, form: string, costume: string, eventTag: string, method: string): Promise<boolean> {
  const res = await fetch("/api/shinies", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ pokemon_id: pokemonId, form, costume, event_tag: eventTag, method }),
  });
  return res.ok;
}

async function apiUpdate(id: number, form: string, costume: string, eventTag: string, method: string): Promise<Response> {
  return fetch(`/api/shinies/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ form, costume, event_tag: eventTag, method }),
  });
}

async function apiRemove(id: number): Promise<boolean> {
  const res = await fetch(`/api/shinies/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": CSRF_TOKEN },
  });
  return res.ok;
}

async function apiEvolve(id: number, into: string): Promise<boolean> {
  const res = await fetch(`/api/shinies/${id}/evolve`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ into }),
  });
  return res.ok;
}

function buildCaughtIndex(shinies: UserShiny[]): Map<string, UserShiny> {
  const m = new Map<string, UserShiny>();
  for (const s of shinies) m.set(`${s.pokemon_id}:${s.form}:${s.costume}:${s.event_tag}`, s);
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

  let evolvedShinies: UserShiny[] = [];
  let caughtIndex: Map<string, UserShiny>;
  let countMap: Map<string, number>;

  function buildCountMap(shinies: UserShiny[]): Map<string, number> {
    const m = new Map<string, number>();
    for (const s of shinies) m.set(s.pokemon_id, (m.get(s.pokemon_id) ?? 0) + 1);
    return m;
  }

  function rebuildState(shinies: UserShiny[]) {
    evolvedShinies = shinies.filter((s) => !!s.evolved_at);
    caughtIndex    = buildCaughtIndex(shinies);
    countMap       = buildCountMap(shinies);
  }

  rebuildState(userShinies);

  app.innerHTML = `
    <div class="page-header">
      <h1>${SH.heading}</h1>
      <p id="sc-counter" class="page-header-sub"></p>
    </div>
    <div class="tabs" id="sc-tabs">
      <button class="tab-btn active" data-tab="all">${SH.tabAll}</button>
      <button class="tab-btn" data-tab="caught">${SH.tabCaught}</button>
      <button class="tab-btn" data-tab="evolved">${SH.tabEvolved}</button>
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
  let modalCostumeInput: HTMLInputElement;
  let modalEventInput: HTMLInputElement;
  let modalMethodSel: HTMLSelectElement;
  let modalTarget: ShinyPokemon | null = null;

  const costumeDatalist = document.createElement("datalist");
  costumeDatalist.id = "sc-costume-list";
  for (const c of GENERIC_COSTUMES) {
    const opt = document.createElement("option");
    opt.value = c;
    costumeDatalist.appendChild(opt);
  }
  document.body.appendChild(costumeDatalist);

  const eventDatalist = document.createElement("datalist");
  eventDatalist.id = "sc-event-list";
  for (const e of EVENT_OPTIONS) {
    const opt = document.createElement("option");
    opt.value = e.value;
    eventDatalist.appendChild(opt);
  }
  document.body.appendChild(eventDatalist);

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

    modalFields.innerHTML = "";

    modalFormSel = makeSelect(FORMS, "");
    modalFields.appendChild(modalFormSel);

    const costumeWrap = document.createElement("div");
    costumeWrap.className = "sc-field-wrap";
    const costumeLabel = document.createElement("label");
    costumeLabel.className = "sc-field-label";
    costumeLabel.textContent = SH.costumeLabel;
    modalCostumeInput = document.createElement("input");
    modalCostumeInput.type = "text";
    modalCostumeInput.className = "move-select";
    modalCostumeInput.placeholder = SH.costumePlaceholder;
    modalCostumeInput.setAttribute("list", "sc-costume-list");
    costumeWrap.appendChild(costumeLabel);
    costumeWrap.appendChild(modalCostumeInput);
    modalFields.appendChild(costumeWrap);

    const shinyImgEl = document.getElementById("sc-modal-shiny") as HTMLImageElement;
    const updateModalSprite = () => {
      const costume = modalCostumeInput.value.trim();
      const url = costume ? costumeShinyUrl(s.id, s.name, costume) : null;
      shinyImgEl.onerror = url
        ? () => { shinyImgEl.src = spriteUrl(s.id); shinyImgEl.onerror = null; }
        : null;
      shinyImgEl.src = url ?? spriteUrl(s.id);
    };
    modalCostumeInput.addEventListener("input", updateModalSprite);
    modalCostumeInput.addEventListener("change", updateModalSprite);

    const eventWrap = document.createElement("div");
    eventWrap.className = "sc-field-wrap";
    const eventLabel = document.createElement("label");
    eventLabel.className = "sc-field-label";
    eventLabel.textContent = SH.eventLabel;
    modalEventInput = document.createElement("input");
    modalEventInput.type = "text";
    modalEventInput.className = "move-select";
    modalEventInput.placeholder = SH.eventPlaceholder;
    modalEventInput.setAttribute("list", "sc-event-list");
    eventWrap.appendChild(eventLabel);
    eventWrap.appendChild(modalEventInput);
    modalFields.appendChild(eventWrap);

    modalMethodSel = makeSelect(METHODS, "");
    modalFields.appendChild(modalMethodSel);

    refreshCostumeDatalist(s.name);
    modal.classList.add("open");
  }

  modalAddBtn.addEventListener("click", async () => {
    if (!modalTarget) return;
    const form    = modalFormSel.value;
    const costume = modalCostumeInput.value.trim();
    const eventTag = modalEventInput.value.trim();
    const method  = modalMethodSel.value;

    modalAddBtn.disabled = true;
    modalAddBtn.textContent = SH.adding;
    modalStatus.textContent = "";

    const ok = await apiAdd(modalTarget.name, form, costume, eventTag, method);
    if (ok) {
      userShinies = await fetchUserShinies();
      rebuildState(userShinies);
      updateCounter();
      renderTab();
      closeModal();
    } else {
      modalStatus.textContent = JSC.somethingWrong;
      modalAddBtn.disabled = false;
      modalAddBtn.textContent = SH.add;
    }
  });

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

  const METHOD_ICONS: Record<string, string> = {
    wild: "🌿", egg: "🥚", raid: "⚔️", research: "📋",
    evolution: "⬆️", photobomb: "📸", trade: "🤝", go_pass: "🎫", go_tour: "🎟️",
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
    const evolvedChip = evolvedShinies.length
      ? ` <span class="sc-evolved-counter-chip">⬆️ ${SH.countsEvolved.replace("{n}", String(evolvedShinies.length))}</span>`
      : "";
    counterEl.innerHTML =
      `<span class="sc-stat-counts">${counts}</span>${evolvedChip}` +
      (chipsHtml ? `<span class="sc-stat-chips">${chipsHtml}</span>` : "");
  }

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

      const count = countMap.get(s.name) ?? 0;
      if (count > 1) {
        const countBadge = document.createElement("span");
        countBadge.className = "sc-count-badge";
        countBadge.textContent = `×${count}`;
        card.appendChild(countBadge);
      }

      const img = document.createElement("img");
      img.src = spriteUrl(s.id);
      img.alt = s.name;
      img.className = "shiny-img";
      if (TINY_POKEMON.has(s.id)) img.classList.add("sprite-sm-poke");
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

  function showEvolvePicker(
    row: HTMLElement,
    rec: UserShiny,
    options: string[],
    triggerBtn: HTMLButtonElement,
  ) {
    triggerBtn.remove();

    const picker = document.createElement("div");
    picker.className = "sc-evolve-picker";

    if (options.length === 1) {
      const label = document.createElement("span");
      label.textContent = SH.evolveInto.replace("{name}", options[0]);
      const confirmBtn = document.createElement("button");
      confirmBtn.className = "sc-evolve-confirm btn-primary";
      confirmBtn.textContent = "✓";
      const cancelBtn = document.createElement("button");
      cancelBtn.className = "sc-evolve-cancel";
      cancelBtn.textContent = "✕";

      confirmBtn.addEventListener("click", async () => {
        confirmBtn.disabled = true;
        cancelBtn.disabled = true;
        const ok = await apiEvolve(rec.id, options[0]);
        if (ok) {
          userShinies = await fetchUserShinies();
          rebuildState(userShinies);
          updateCounter();
          renderTab();
        } else {
          picker.remove();
          row.appendChild(triggerBtn);
        }
      });
      cancelBtn.addEventListener("click", () => {
        picker.remove();
        row.appendChild(triggerBtn);
      });

      picker.appendChild(label);
      picker.appendChild(confirmBtn);
      picker.appendChild(cancelBtn);
    } else {
      const label = document.createElement("span");
      label.textContent = SH.evolvePick + ":";
      picker.appendChild(label);

      for (const optName of options) {
        const optPoke = shinyByName.get(optName);
        const optBtn = document.createElement("button");
        optBtn.className = "sc-evolve-option";
        optBtn.title = optName;

        if (optPoke) {
          const optImg = document.createElement("img");
          optImg.src = spriteUrl(optPoke.id);
          optImg.alt = optName;
          optImg.onerror = () => { optImg.style.display = "none"; };
          optBtn.appendChild(optImg);
        }
        const optLabel = document.createElement("span");
        optLabel.textContent = pokeName(gameData, optName);
        optBtn.appendChild(optLabel);

        optBtn.addEventListener("click", async () => {
          picker.querySelectorAll("button").forEach((b) => { (b as HTMLButtonElement).disabled = true; });
          const ok = await apiEvolve(rec.id, optName);
          if (ok) {
            userShinies = await fetchUserShinies();
            rebuildState(userShinies);
            updateCounter();
            renderTab();
          } else {
            picker.querySelectorAll("button").forEach((b) => { (b as HTMLButtonElement).disabled = false; });
          }
        });
        picker.appendChild(optBtn);
      }

      const cancelBtn = document.createElement("button");
      cancelBtn.className = "sc-evolve-cancel";
      cancelBtn.textContent = "✕";
      cancelBtn.addEventListener("click", () => {
        picker.remove();
        row.appendChild(triggerBtn);
      });
      picker.appendChild(cancelBtn);
    }

    row.appendChild(picker);
  }

  function renderShinyRows(entries: UserShiny[]) {
    const list = document.createElement("div");
    list.className = "sc-list";

    for (const rec of entries) {
      const poke = shinyByName.get(rec.pokemon_id);
      const row  = document.createElement("div");
      row.className = "sc-entry" + (rec.evolved_at ? " sc-row-evolved" : "");

      // Sprite
      const img = document.createElement("img");
      img.className = "sc-entry-img";
      img.alt = rec.pokemon_id;
      if (poke) {
        setSprite(img, poke.id, poke.name, rec.costume);
        if (TINY_POKEMON.has(poke.id)) img.classList.add("sprite-sm-poke");
      } else {
        img.style.display = "none";
      }

      // Name + date + evolved chip + costume/event labels
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
      if (rec.evolved_at) {
        const chip = document.createElement("span");
        chip.className = "sc-evolved-chip";
        chip.textContent = `⬆️ ${SH.evolved}`;
        nameWrap.appendChild(chip);
      }
      const subParts: string[] = [];
      if (rec.form === "shadow")   subParts.push(JSC.formShadow);
      if (rec.form === "purified") subParts.push(JSC.formPurified);
      if (rec.costume)             subParts.push(rec.costume);
      if (rec.event_tag)           subParts.push(rec.event_tag);
      if (subParts.length) {
        const sub = document.createElement("span");
        sub.className = "sc-entry-sub";
        sub.textContent = subParts.join(" · ");
        nameWrap.appendChild(sub);
      }

      // Form selector
      const formSel = makeSelect(FORMS, rec.form);

      // Costume input
      const costumeSel = document.createElement("input");
      costumeSel.type = "text";
      costumeSel.className = "move-select";
      costumeSel.value = rec.costume;
      costumeSel.placeholder = SH.costumePlaceholder;
      costumeSel.setAttribute("list", "sc-costume-list");
      costumeSel.addEventListener("focus", () => refreshCostumeDatalist(rec.pokemon_id));

      // Event tag input
      const eventSel = document.createElement("input");
      eventSel.type = "text";
      eventSel.className = "move-select";
      eventSel.value = rec.event_tag;
      eventSel.placeholder = SH.eventPlaceholder;
      eventSel.setAttribute("list", "sc-event-list");

      // Method selector
      const methodSel = makeSelect(METHODS, rec.method);

      // Save status
      const statusEl = document.createElement("span");
      statusEl.className = "sc-save-status";

      let saveTimer: ReturnType<typeof setTimeout>;

      const saveUpdate = async () => {
        const newForm     = formSel.value;
        const newCostume  = costumeSel.value;
        const newEventTag = eventSel.value;
        const newMethod   = methodSel.value;
        let res: Response;
        try {
          res = await apiUpdate(rec.id, newForm, newCostume, newEventTag, newMethod);
        } catch (e) {
          console.error("shiny update failed (network):", e);
          statusEl.textContent = JSC.error;
          return;
        }
        if (res.ok) {
          caughtIndex.delete(`${rec.pokemon_id}:${rec.form}:${rec.costume}:${rec.event_tag}`);
          rec.form      = newForm;
          rec.costume   = newCostume;
          rec.event_tag = newEventTag;
          rec.method    = newMethod;
          caughtIndex.set(`${rec.pokemon_id}:${rec.form}:${rec.costume}:${rec.event_tag}`, rec);
          statusEl.textContent = SH.saved;
          clearTimeout(saveTimer);
          saveTimer = setTimeout(() => { statusEl.textContent = ""; }, 1500);
        } else if (res.status === 409) {
          formSel.value    = rec.form;
          costumeSel.value = rec.costume;
          eventSel.value   = rec.event_tag;
          methodSel.value  = rec.method;
          statusEl.textContent = SH.alreadyCaught;
          clearTimeout(saveTimer);
          saveTimer = setTimeout(() => { statusEl.textContent = ""; }, 2000);
        } else {
          console.error("shiny update failed:", res.status, rec.pokemon_id);
          formSel.value    = rec.form;
          costumeSel.value = rec.costume;
          eventSel.value   = rec.event_tag;
          methodSel.value  = rec.method;
          statusEl.textContent = JSC.error;
        }
      };

      formSel.addEventListener("change", saveUpdate);
      costumeSel.addEventListener("change", saveUpdate);
      costumeSel.addEventListener("change", () => {
        if (poke) setSprite(img, poke.id, poke.name, costumeSel.value.trim());
      });
      eventSel.addEventListener("change", saveUpdate);
      methodSel.addEventListener("change", saveUpdate);

      // Evolve button -- transforms entry into next form
      const evolveBtn = document.createElement("button");
      evolveBtn.className = "sc-evolve-btn";
      evolveBtn.textContent = SH.evolveBtn;
      evolveBtn.title = SH.evolveBtn;
      evolveBtn.addEventListener("click", () => {
        const nextForms = EVOLUTION_NEXT[rec.pokemon_id] ?? [];
        const available = nextForms.filter((n) => n && shinyByName.has(n));
        if (!available.length) {
          evolveBtn.textContent = SH.evolveNone;
          evolveBtn.disabled = true;
          setTimeout(() => {
            evolveBtn.textContent = SH.evolveBtn;
            evolveBtn.disabled = false;
          }, 2000);
          return;
        }
        showEvolvePicker(row, rec, available, evolveBtn);
      });

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
          rebuildState(userShinies);
          updateCounter();
          renderTab();
        } else {
          removeBtn.disabled = false;
          removeBtn.textContent = JSC.remove;
        }
      });

      row.appendChild(img);
      row.appendChild(nameWrap);
      row.appendChild(formSel);
      row.appendChild(costumeSel);
      row.appendChild(eventSel);
      row.appendChild(methodSel);
      row.appendChild(statusEl);
      row.appendChild(evolveBtn);
      row.appendChild(removeBtn);
      list.appendChild(row);
    }

    contentEl.appendChild(list);
  }

  function renderCaughtList(evolvedOnly = false) {
    const source = evolvedOnly ? evolvedShinies : userShinies;
    const q = searchEl.value.trim().toLowerCase();
    const entries = source.filter((s) => !q || s.pokemon_id.toLowerCase().includes(q));

    contentEl.innerHTML = "";

    if (!entries.length) {
      contentEl.innerHTML = q
        ? `<p class="empty-state">${JSC.noResults}</p>`
        : `<p class="empty-state">${SH.nothingCaught}</p>`;
      return;
    }

    renderShinyRows(entries);
  }

  let activeTab = "all";

  function renderTab() {
    if (activeTab === "caught") {
      searchEl.placeholder = SH.filterCaught;
      renderCaughtList(false);
    } else if (activeTab === "evolved") {
      searchEl.placeholder = SH.filterCaught;
      renderCaughtList(true);
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
