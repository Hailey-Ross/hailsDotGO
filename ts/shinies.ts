import { loadGameData, pokeName } from "./shared/gamedata";
import { fetchSpeciesData, fetchCryUrl, fetchFormSprites } from "./shared/pokedex";
import { costumeShinyUrl, costumeEntries, TINY_POKEMON } from "./shared/costumes";
import { createPicker } from "./shared/picker";
import type { Picker } from "./shared/picker";
import { getEvolveTargets, getEvolutionFamily, EvolveTarget } from "./shared/evolutions";
import { cardRegionalForms, recordableRegions, setRegionalShinyOverrides, setRegionalShinyDates, isRegionalShiny, regionalShinyDate, regionalVariantId, isPatternOnlyRegion, unownLetter, vivillonGeo, REGION_ORDER, UNOWN_LETTERS, VIVILLON_PATTERNS } from "./shared/regionalForms";
import type { GameData, ShinyPokemon, ShinyDexEntry } from "./shared/types";

declare const JSC: Record<string, string>;
declare const SH: Record<string, string>;
declare const SITE_LANG: string;

const CSRF_TOKEN = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

let cryVolume = 0.3;

interface UserShiny {
  id: number;
  pokemon_id: string;
  form: string;
  region: string;
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
  { value: "GO Tour: Kalos",             label: "GO Tour: Kalos" },
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
  { value: "Road of Legends",            label: "Road of Legends" },
  // Seasons (chronological). Values for seasons already in the list before this
  // refresh are preserved verbatim because event_tag is part of the per-user
  // uniqueness key. The earliest seasons (2020 to 2022) keep the official
  // "Season of" prefix; from Mythical Wishes onward the shorter rebranded names
  // are used.
  { value: "Season of Celebration",      label: "Season of Celebration" },
  { value: "Season of Legends",          label: "Season of Legends" },
  { value: "Season of Discovery",        label: "Season of Discovery" },
  { value: "Season of Mischief",         label: "Season of Mischief" },
  { value: "Season of Heritage",         label: "Season of Heritage" },
  { value: "Season of Alola",            label: "Season of Alola" },
  { value: "Season of GO",               label: "Season of GO" },
  { value: "Season of Light",            label: "Season of Light" },
  { value: "Mythical Wishes",            label: "Mythical Wishes" },
  { value: "Rising Heroes",              label: "Rising Heroes" },
  { value: "Hidden Gems",                label: "Hidden Gems" },
  { value: "Adventures Abound",          label: "Adventures Abound" },
  { value: "Timeless Travels",           label: "Timeless Travels" },
  { value: "World of Wonders",           label: "World of Wonders" },
  { value: "Shared Skies",               label: "Shared Skies" },
  { value: "Max Out",                    label: "Max Out" },
  { value: "Dual Destiny",               label: "Dual Destiny" },
  { value: "Might and Mastery",          label: "Might and Mastery" },
  { value: "Delightful Days",            label: "Delightful Days" },
  { value: "Tales of Transformation",    label: "Tales of Transformation" },
  { value: "Precious Paths",             label: "Precious Paths" },
  { value: "Memories in Motion",         label: "Memories in Motion" },
  { value: "Forever Forward",            label: "Forever Forward" },
  // Recurring formats the live feed surfaces that the groups above never covered. The feed
  // only spans the next few weeks, so these stay useful for tagging an older catch.
  { value: "Ultra Unlock",               label: "Ultra Unlock" },
  { value: "Hatch Day",                  label: "Hatch Day" },
  { value: "Max Battle Day",             label: "Max Battle Day" },
  { value: "Choose Your Path",           label: "Choose Your Path" },
  { value: "Wild Area",                  label: "Wild Area" },
  { value: "City Safari",                label: "City Safari" },
];

// The curated list above is the stable base, but it only ever named recurring formats and
// seasons, so a one-off like "Special Anniversary Pikachu Celebration" was unsuggestable
// until someone hand-edited this file. Those names already reach us in the live LeekDuck
// feed behind /api/events, the same one the events page and the .ics subscriptions read,
// so merge them in instead of topping this list up every few weeks.
const TAGGABLE_EVENT_TYPES = new Set([
  "event", "community-day", "pokemon-go-fest",
  "raid-day", "safari-zone", "max-battles", "season",
]);

// Just the slice of the feed used here; the full shape lives in ts/events.ts.
interface FeedEvent {
  name: string;
  eventType: string;
}

const FORMS = [
  { value: "", label: JSC.formNormal },
  { value: "shadow", label: JSC.formShadow },
  { value: "purified", label: JSC.formPurified },
];

const REGION_LABELS: Record<string, string> = {
  alolan: JSC.formAlolan,
  galarian: JSC.formGalarian,
  hisuian: JSC.formHisuian,
  paldean: JSC.formPaldean,
  paldean_combat: JSC.formPaldeanCombat,
  paldean_blaze: JSC.formPaldeanBlaze,
  paldean_aqua: JSC.formPaldeanAqua,
  therian: JSC.formTherian,
  origin: JSC.formOrigin,
  attack: JSC.formAttack,
  defense: JSC.formDefense,
  speed: JSC.formSpeed,
  sky: JSC.formSky,
  dusk_mane: JSC.formDuskMane,
  dawn_wings: JSC.formDawnWings,
  crowned_sword: JSC.formCrownedSword,
  crowned_shield: JSC.formCrownedShield,
  black: JSC.formBlack,
  white: JSC.formWhite,
  resolute: JSC.formResolute,
  midnight: JSC.formMidnight,
  dusk: JSC.formDusk,
  sandy_cloak: JSC.formSandyCloak,
  trash_cloak: JSC.formTrashCloak,
  low_key: JSC.formLowKey,
  pom_pom: JSC.formPomPom,
  pau: JSC.formPau,
  sensu: JSC.formSensu,
  blue_striped: JSC.formBlueStriped,
  white_striped: JSC.formWhiteStriped,
  wash: JSC.formWash,
  // The Unown letters label themselves: a letter is the same glyph in every
  // locale, so they need no translation and no 28 locale keys.
  ...Object.fromEntries(UNOWN_LETTERS.map((u) => [u.region, u.letter])),
  // The Vivillon patterns label themselves too (Meadow, Sun, Ocean), keeping the
  // 20 pattern names out of the locale files.
  ...Object.fromEntries(VIVILLON_PATTERNS.map((p) => [p.region, p.label])),
};

// Localized display name for a species plus optional region, e.g.
// "Hisuian Growlithe". The template key lets locales reorder the words.
//
// An Unown letter reads the other way round ("Unown F", not "F Unown"), so it
// gets its own template key rather than being forced through regionalName.
function regionalDisplayName(gameData: GameData, species: string, region: string): string {
  const base = pokeName(gameData, species);
  if (!region) return base;
  const letter = unownLetter(region);
  if (letter) {
    return JSC.unownName.replace("{name}", base).replace("{letter}", letter);
  }
  return JSC.regionalName
    .replace("{region}", REGION_LABELS[region] ?? region)
    .replace("{name}", base);
}

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

// Costume is free text, so the picker is a suggestion list and never a constraint: whatever is
// typed is what gets saved, including a costume that has no label yet.
//
// onInput fires on every keystroke (live sprite preview); onCommit fires when the value is settled
// (the edit modal saves there). Choosing a row from the dropdown counts as BOTH, because setting
// .value from code fires neither event, and a native datalist pick used to fire both.
function costumePicker(
  dexId: number,
  pokemonName: string,
  value: string,
  onInput: () => void,
  onCommit?: () => void,
): Picker {
  const picker = createPicker({
    entries: costumeEntries(dexId, pokemonName),
    placeholder: SH.costumePlaceholder,
    noMatchText: SH.costumeNoMatch,
    showOnFocus: true,
    preview: false,
    maxResults: 100,
    onSelect: () => { onInput(); onCommit?.(); },
  });
  picker.input.classList.add("move-select");
  picker.input.value = value;
  picker.input.addEventListener("input", onInput);
  if (onCommit) picker.input.addEventListener("change", onCommit);
  return picker;
}

let liveEventsMerged = false;

// Adds current and upcoming event names from the live feed to the event datalist, ahead of
// the curated options because a shiny is usually logged during the event that produced it.
// Fetched at most once per page and only once the field can actually be reached, so the
// many visits that just browse the checklist pay nothing. Stays silent on failure: the
// input is free text, so losing the feed costs suggestions, never the ability to record
// a catch.
async function mergeLiveEventOptions() {
  if (liveEventsMerged) return;
  liveEventsMerged = true;
  const dl = document.getElementById("sc-event-list") as HTMLDataListElement | null;
  if (!dl) return;
  try {
    const res = await fetch("/api/events");
    if (!res.ok) return;
    const events: FeedEvent[] = await res.json();
    // event_tag is part of the per-user uniqueness key, so a live name that already exists
    // verbatim in EVENT_OPTIONS ("Forever Forward" is both a live season and curated) must
    // resolve to one option, not two.
    const seen = new Set(EVENT_OPTIONS.map((e) => e.value));
    const frag = document.createDocumentFragment();
    for (const ev of events) {
      if (!ev?.name || !TAGGABLE_EVENT_TYPES.has(ev.eventType) || seen.has(ev.name)) continue;
      seen.add(ev.name);
      const opt = document.createElement("option");
      opt.value = ev.name;
      frag.appendChild(opt);
    }
    dl.insertBefore(frag, dl.firstChild);
  } catch {
    // Feed unreachable. The curated options are already in the datalist.
  }
}

// id is usually a dex or variant id, but the Unown letters have no id of their own and are
// filed under a string slug (201-b) instead. See UNOWN_LETTERS in shared/regionalForms.
function spriteUrl(id: number | string) {
  return `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/shiny/${id}.png`;
}

async function fetchUserShinies(): Promise<UserShiny[]> {
  const res = await fetch("/api/shinies");
  if (!res.ok) return [];
  return res.json();
}

async function apiAdd(pokemonId: string, form: string, region: string, costume: string, eventTag: string, method: string, caughtAt: string): Promise<boolean> {
  const res = await fetch("/api/shinies", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    // caught_at is the day you caught it, which is not always the day you log it. Empty falls back
    // to now, so leaving it alone behaves exactly as before.
    body: JSON.stringify({ pokemon_id: pokemonId, form, region, costume, event_tag: eventTag, method, caught_at: caughtAt }),
  });
  return res.ok;
}

async function apiUpdate(id: number, form: string, region: string, costume: string, eventTag: string, method: string, caughtAt: string): Promise<Response> {
  return fetch(`/api/shinies/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    // caught_at is "YYYY-MM-DD" or empty. Empty leaves the stored date alone, so a row whose date
    // was never set stays as it was.
    body: JSON.stringify({ form, region, costume, event_tag: eventTag, method, caught_at: caughtAt }),
  });
}

const pad2 = (n: number) => String(n).padStart(2, "0");

// The date input wants "YYYY-MM-DD"; the API sends a full timestamp.
//
// A stored caught_at is a calendar DAY pinned to UTC midnight (the server parses "2006-01-02" with
// no zone), so it has to be read back in UTC. Local getters would report the previous day for
// everyone west of UTC, and because the row editor saves what the input shows, that shifted day
// used to be written straight back, walking the catch one day earlier on every edit.
function dateInputValue(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return `${d.getUTCFullYear()}-${pad2(d.getUTCMonth() + 1)}-${pad2(d.getUTCDate())}`;
}

// Today as the trainer's own calendar reckons it, for defaulting and capping the input. This is the
// one case that IS local: "today" means the day it is where they are, not where the server is.
function todayInputValue(): string {
  const n = new Date();
  return `${n.getFullYear()}-${pad2(n.getMonth() + 1)}-${pad2(n.getDate())}`;
}

// An announced release day, short enough for a 0.55rem corner badge ("12 Aug"). Read back in UTC
// for the same reason dateInputValue is: the server sent a calendar day, and a local reading would
// show the day before for everyone west of UTC.
function shortReleaseDate(iso: string): string {
  const d = new Date(`${iso}T00:00:00Z`);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleDateString(SITE_LANG ?? "en-GB", { timeZone: "UTC", day: "numeric", month: "short" });
}

// The same day written out, for the tooltip that explains what the badge means.
function longReleaseDate(iso: string): string {
  const d = new Date(`${iso}T00:00:00Z`);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(SITE_LANG ?? "en-GB", {
    timeZone: "UTC", year: "numeric", month: "long", day: "numeric",
  });
}

async function apiRemove(id: number): Promise<boolean> {
  const res = await fetch(`/api/shinies/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": CSRF_TOKEN },
  });
  return res.ok;
}

async function apiEvolve(id: number, into: string, region: string): Promise<boolean> {
  const res = await fetch(`/api/shinies/${id}/evolve`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": CSRF_TOKEN },
    body: JSON.stringify({ into, region }),
  });
  return res.ok;
}

// The region as the checklist sees it. A carry-only pattern (a Scatterbug or
// Spewpa Vivillon pattern) collapses to "" so it ticks and counts against the
// single base card, while the entry itself keeps the real region for evolution.
function cardRegion(species: string, region: string): string {
  return isPatternOnlyRegion(species, region) ? "" : region;
}

function entryKey(s: UserShiny): string {
  return `${s.pokemon_id}:${cardRegion(s.pokemon_id, s.region)}:${s.form}:${s.costume}:${s.event_tag}`;
}

function buildCaughtIndex(shinies: UserShiny[]): Map<string, UserShiny> {
  const m = new Map<string, UserShiny>();
  for (const s of shinies) m.set(entryKey(s), s);
  return m;
}

// The set of `name:region` pairs the trainer owns at least one entry for.
//
// This used to be a linear scan of every caught key, run once per card, on every keystroke: with
// a thousand cards and a few hundred entries that is hundreds of thousands of string comparisons
// per character typed. A prefix set makes the same question one lookup.
function buildCaughtCardKeys(shinies: UserShiny[]): Set<string> {
  const s = new Set<string>();
  for (const e of shinies) s.add(`${e.pokemon_id}:${cardRegion(e.pokemon_id, e.region)}`);
  return s;
}

// True when any entry exists for this species in this region ('' = original form).
function cardCaught(name: string, region: string, keys: Set<string>): boolean {
  return keys.has(`${name}:${region}`);
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

  setRegionalShinyOverrides(gameData.regionalShinyOverrides);
  setRegionalShinyDates(gameData.regionalShinyDates);

  // shinyDex is the full National Dex with availability flags; shinies is the released-only view.
  //
  // The fallback is NOT optional. A server with no baseline embedded omits shinyDex entirely, and
  // so does every stub payload the check-* scripts feed this page. In that case everything present
  // is treated as released and in GO, which reproduces the behaviour that shipped before the flags
  // existed rather than rendering an empty grid.
  const dexEntries: ShinyDexEntry[] = gameData.shinyDex
    ? Object.values(gameData.shinyDex)
    : Object.values(gameData.shinies).map((s) => ({
        id: s.id, name: s.name, in_go: true, shiny_released: true,
      }));

  // Anything the server serves as released but the dex table has never heard of still gets a card.
  // Today the baseline spans the whole National Dex so this is empty, but the day a new generation
  // lands upstream before the baseline is regenerated, the alternative is a species that is served
  // and invisible: precisely the silent-omission bug this whole change exists to kill.
  if (gameData.shinyDex) {
    const known = new Set(dexEntries.map((e) => e.id));
    for (const s of Object.values(gameData.shinies)) {
      if (!known.has(s.id)) {
        dexEntries.push({ id: s.id, name: s.name, in_go: true, shiny_released: true });
      }
    }
  }
  const dexByName = new Map(dexEntries.map((e) => [e.name, e]));

  // shinyByName spans the FULL dex, not just released species, so an entry someone already owns
  // for an unreleased species still resolves a sprite instead of rendering a blank card.
  const allShinies: ShinyPokemon[] = dexEntries.map(
    (e) => gameData.shinies?.[String(e.id)] ?? { id: e.id, name: e.name },
  );
  const shinyByName = new Map(allShinies.map((s) => [s.name, s]));

  // One checklist card per species plus one per regional form that gets a card. Unreleased cards
  // are built too and filtered at render time, so the "show unreleased" toggle costs no rebuild.
  interface ShinyCard {
    species: ShinyPokemon;
    region: string;
    // A dex id for most cards, but the Unown letters have no id of their own and carry a
    // PokeAPI sprite slug ("201-b") instead.
    spriteId: number | string;
    released: boolean;
    inGo: boolean;
    // "YYYY-MM-DD" when the shiny has been announced but not shipped, else "".
    releaseDate: string;
  }
  const allCards: ShinyCard[] = allShinies.flatMap((s) => {
    const e = dexByName.get(s.name);
    const inGo = e?.in_go ?? true;
    const released = e?.shiny_released ?? true;
    return [
      { species: s, region: "", spriteId: s.id, released, inGo, releaseDate: e?.shiny_release_date ?? "" },
      ...cardRegionalForms(s.name).map((f) => ({
        species: s,
        region: f.region as string,
        spriteId: f.variantId,
        // A form's shiny can only exist if the species itself is in the game.
        released: released && isRegionalShiny(s.name, f),
        inGo,
        // A form waiting on its own species has the species' date, not its own: the form cannot
        // arrive first, and showing an earlier form date would promise something that cannot happen.
        releaseDate: released ? regionalShinyDate(s.name, f) : (e?.shiny_release_date ?? ""),
      })),
    ];
  });
  // Sorted once here rather than on every render: filtering preserves order.
  const regionRankMap = new Map(REGION_ORDER.map((r, i) => [r as string, i + 1]));
  const regionRank = (r: string) => (r ? regionRankMap.get(r) ?? 0 : 0);
  allCards.sort((a, b) => a.species.id - b.species.id || regionRank(a.region) - regionRank(b.region));

  let evolvedShinies: UserShiny[] = [];
  let caughtIndex: Map<string, UserShiny>;
  let caughtCardKeys: Set<string>;
  let countMap: Map<string, number>;

  function buildCountMap(shinies: UserShiny[]): Map<string, number> {
    const m = new Map<string, number>();
    for (const s of shinies) {
      const k = `${s.pokemon_id}:${cardRegion(s.pokemon_id, s.region)}`;
      m.set(k, (m.get(k) ?? 0) + 1);
    }
    return m;
  }

  function rebuildState(shinies: UserShiny[]) {
    evolvedShinies = shinies.filter((s) => !!s.evolved_at);
    caughtIndex    = buildCaughtIndex(shinies);
    caughtCardKeys = buildCaughtCardKeys(shinies);
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
           style="width:100%;margin-bottom:0.5rem;display:block"
           placeholder="${JSC.search}">
    <label class="sc-filter-toggle" id="sc-unreleased-wrap">
      <input type="checkbox" id="sc-show-unreleased">
      <span>${SH.showUnreleased}</span>
    </label>
    <div id="sc-content"></div>
  `;

  const counterEl  = document.getElementById("sc-counter")!;
  const tabsEl     = document.getElementById("sc-tabs")!;
  const searchEl   = document.getElementById("sc-search") as HTMLInputElement;
  const contentEl  = document.getElementById("sc-content")!;
  const unreleasedEl = document.getElementById("sc-show-unreleased") as HTMLInputElement;

  const SHOW_UNRELEASED_KEY = "sc-show-unreleased";
  try {
    if (localStorage.getItem(SHOW_UNRELEASED_KEY) === "1") unreleasedEl.checked = true;
  } catch { /* private mode: the toggle just does not persist */ }

  // A card is visible when its shiny is released, or a release date has been announced for it, or
  // the trainer asked to see what is coming, or they already own one. That last clause matters:
  // flags can be wrong or an admin can toggle something off, and hiding somebody's own catch from
  // their own checklist would read as data loss. A caught unreleased card renders as caught.
  //
  // The date clause is the point of announcing one: a handful of dated cards showing by default is
  // how a trainer finds out something is coming, and it is a handful, not the 140 undated ones.
  function cardVisible(c: ShinyCard): boolean {
    return c.released || !!c.releaseDate || unreleasedEl.checked
        || cardCaught(c.species.name, c.region, caughtCardKeys);
  }

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
      <details class="sc-form-details" id="sc-form-details" style="display:none">
        <summary class="sc-form-summary">${JSC.otherForms}</summary>
        <div class="sc-form-body" id="sc-form-body"></div>
      </details>
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
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    closeModal();
    closeEditModal();
  });

  function closeModal() { modal.classList.remove("open"); }

  // The edit modal, for one already recorded entry. Deliberately a second element rather than
  // a mode of the add modal above: that one is a fixed skeleton of sprite comparison, cry
  // controls, flavour text and an Add button that openAddModal resets piece by piece, and
  // sharing it would mean hiding half of it on every edit and restoring it on every add.
  //
  // It keeps the .sc-modal class because main.css excludes that class from the stacking rule
  // it puts on every other direct child of body. The class is what is matched, not the
  // element, so a second one is exempt too (ts/trainer.ts already ships another).
  const editModal = document.createElement("div");
  editModal.className = "sc-modal sc-edit-modal";
  editModal.innerHTML = `
    <div class="sc-modal-inner">
      <button class="sc-modal-close">&times;</button>
      <div class="sc-edit-head">
        <img class="sc-edit-img" id="sc-edit-img" alt="">
        <div class="sc-modal-name" id="sc-edit-name"></div>
      </div>
      <div id="sc-edit-fields" style="width:100%;display:flex;flex-direction:column;gap:0.5rem"></div>
      <div class="sc-edit-actions" id="sc-edit-actions"></div>
    </div>
  `;
  document.body.appendChild(editModal);

  editModal.querySelector(".sc-modal-close")!.addEventListener("click", closeEditModal);
  editModal.addEventListener("click", (e) => { if (e.target === editModal) closeEditModal(); });

  // Bumped on every open and every close. A save that resolves after either has happened is a
  // save for an entry nobody is looking at any more, so its continuation bails out instead of
  // writing a status into the modal the next entry is now using.
  let editGeneration = 0;

  function closeEditModal() {
    editGeneration++;
    editModal.classList.remove("open");
  }

  let modalFormSel: HTMLSelectElement;
  let modalCostumePicker: Picker;
  let modalCostumeInput: HTMLInputElement;
  let modalEventInput: HTMLInputElement;
  let modalMethodSel: HTMLSelectElement;
  let modalCaughtInput: HTMLInputElement;
  let modalTarget: ShinyCard | null = null;

  const eventDatalist = document.createElement("datalist");
  eventDatalist.id = "sc-event-list";
  for (const e of EVENT_OPTIONS) {
    const opt = document.createElement("option");
    opt.value = e.value;
    eventDatalist.appendChild(opt);
  }
  document.body.appendChild(eventDatalist);

  function openAddModal(c: ShinyCard) {
    const s = c.species;
    modalTarget = c;
    mergeLiveEventOptions();
    // Hide on error like every other sprite on the page: this one was the sole image without a
    // handler, so a species PokeAPI has no art for showed the browser's broken image glyph.
    const normalImg = document.getElementById("sc-modal-normal") as HTMLImageElement;
    normalImg.style.display = "";
    normalImg.onerror = () => { normalImg.style.display = "none"; };
    normalImg.src =
      `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/${c.spriteId}.png`;
    (document.getElementById("sc-modal-shiny") as HTMLImageElement).src = spriteUrl(c.spriteId);
    modalName.textContent = regionalDisplayName(gameData, s.name, c.region);
    modalStatus.textContent = "";

    const flavorP    = document.getElementById("sc-modal-flavor")  as HTMLElement;
    const genusEl    = document.getElementById("sc-modal-genus")   as HTMLElement;
    const badgeEl    = document.getElementById("sc-modal-badge")   as HTMLElement;
    const cryPanel   = document.getElementById("sc-cry-controls")  as HTMLElement;
    const cryBtn     = document.getElementById("sc-modal-cry")     as HTMLButtonElement;
    const volSlider  = document.getElementById("sc-modal-volume")  as HTMLInputElement;
    const volLabel   = document.getElementById("sc-modal-vlabel")  as HTMLElement;
    flavorP.textContent = ""; flavorP.style.display = "none";
    genusEl.textContent  = "";
    badgeEl.textContent  = ""; badgeEl.style.display = "none";
    cryPanel.style.display = "none"; cryBtn.onclick = null;
    const formDetails = document.getElementById("sc-form-details") as HTMLDetailsElement;
    const formBody    = document.getElementById("sc-form-body") as HTMLElement;
    formBody.innerHTML = "";
    formDetails.open = false;
    formDetails.style.display = "none";

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
            formBody.appendChild(side);
          }
          if (sprites.shiny) {
            const side = document.createElement("div");
            side.className = "shiny-compare-side shiny-compare-side--extra";
            side.innerHTML = `<img class="shiny-compare-img" src="${sprites.shiny}" alt="${JSC.primalShiny}"><span>✨ ${JSC.primalShiny}</span>`;
            formBody.appendChild(side);
          }
          if (formBody.childElementCount > 0) formDetails.style.display = "";
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
    // No onCommit: nothing is persisted until the Add button is pressed.
    modalCostumePicker = costumePicker(s.id, s.name, "", () => updateModalSprite());
    modalCostumeInput = modalCostumePicker.input;
    costumeWrap.appendChild(costumeLabel);
    costumeWrap.appendChild(modalCostumePicker.root);
    modalFields.appendChild(costumeWrap);

    const shinyImgEl = document.getElementById("sc-modal-shiny") as HTMLImageElement;
    const updateModalSprite = () => {
      // Costume art is only keyed by base dex; regional cards keep their
      // regional shiny sprite regardless of the costume text.
      const costume = c.region ? "" : modalCostumeInput.value.trim();
      const url = costume ? costumeShinyUrl(s.id, s.name, costume) : null;
      shinyImgEl.onerror = url
        ? () => { shinyImgEl.src = spriteUrl(c.spriteId); shinyImgEl.onerror = null; }
        : null;
      shinyImgEl.src = url ?? spriteUrl(c.spriteId);
    };

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

    // When you caught it, which is often not the day you get round to logging it.
    const caughtWrap = document.createElement("div");
    caughtWrap.className = "sc-field-wrap";
    const caughtLabel = document.createElement("label");
    caughtLabel.className = "sc-field-label";
    caughtLabel.textContent = SH.caught;
    modalCaughtInput = document.createElement("input");
    modalCaughtInput.type = "date";
    modalCaughtInput.className = "move-select";
    modalCaughtInput.max = todayInputValue();
    modalCaughtInput.value = modalCaughtInput.max;
    caughtWrap.appendChild(caughtLabel);
    caughtWrap.appendChild(modalCaughtInput);
    modalFields.appendChild(caughtWrap);

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

    const ok = await apiAdd(modalTarget.species.name, form, modalTarget.region, costume, eventTag, method, modalCaughtInput.value);
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

  // Whole calendar days apart, not elapsed milliseconds: the stored day is UTC midnight while
  // "today" is the trainer's own day, so subtracting the raw timestamps would call this evening's
  // catch "Yesterday" for anyone west of UTC. Both sides are reduced to a day number first, and the
  // absolute dates are formatted in UTC so they name the day that was actually stored.
  function timeAgo(dateStr: string): string {
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return "";
    const n = new Date();
    const days = Math.round(
      (Date.UTC(n.getFullYear(), n.getMonth(), n.getDate())
        - Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate())) / 86400000,
    );
    const locale = typeof SITE_LANG !== "undefined" ? SITE_LANG : "en-GB";
    const fmt: Intl.DateTimeFormatOptions = { timeZone: "UTC" };
    if (days <= 0) return SH.today;
    if (days === 1) return SH.yesterday;
    if (days < 30)  return SH.daysAgo.replace("{n}", String(days));
    if (days < 365) return d.toLocaleDateString(locale, { ...fmt, month: "short", day: "numeric" });
    return d.toLocaleDateString(locale, { ...fmt, year: "numeric", month: "short", day: "numeric" });
  }

  const METHOD_ICONS: Record<string, string> = {
    wild: "🌿", egg: "🥚", raid: "⚔️", research: "📋",
    evolution: "⬆️", photobomb: "📸", trade: "🤝", go_pass: "🎫", go_tour: "🎟️",
  };

  function updateCounter() {
    // Unique counts species plus region pairs, so a Hisuian Growlithe is a
    // distinct unique from a Kanto Growlithe.
    const unique = new Set(
      Array.from(caughtIndex.keys()).map((k) => k.split(":").slice(0, 2).join(":")),
    ).size;
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

  // Builds the set of names to keep for a search query: any candidate whose name
  // matches the query is kept, and so is its whole evolution family. This makes
  // searching any family member (e.g. "Deino", "Zweilous", or "Hydreigon") show
  // the entire line, even when the names share no common substring.
  function familyMatchSet(q: string, names: string[]): Set<string> {
    const keep = new Set<string>();
    for (const n of names) {
      if (n.toLowerCase().includes(q)) {
        for (const m of getEvolutionFamily(n)) keep.add(m);
      }
    }
    return keep;
  }

  function cardMatchesQuery(c: ShinyCard, q: string, keep: Set<string> | null): boolean {
    if (!keep) return true;
    if (keep.has(c.species.name)) return true;
    return !!c.region && (REGION_LABELS[c.region] ?? c.region).toLowerCase().includes(q);
  }

  function renderGrid(source: ShinyCard[]) {
    const q = searchEl.value.trim().toLowerCase();
    const keep = q ? familyMatchSet(q, source.map((c) => c.species.name)) : null;
    const filtered = source.filter((c) => cardMatchesQuery(c, q, keep));

    if (!filtered.length) {
      contentEl.innerHTML = `<p class="empty-state">${JSC.noResults}</p>`;
      return;
    }

    const grid = document.createElement("div");
    grid.className = "shiny-grid";

    // allCards is pre-sorted at init and filtering preserves order, so no per-render sort.
    for (const c of filtered) {
      const caught = cardCaught(c.species.name, c.region, caughtCardKeys);
      // Owning one beats the flag: it is recorded, so it is shown normally and stays editable.
      const locked = !c.released && !caught;
      const card = document.createElement("div");
      card.className = "shiny-tag" + (caught ? " sc-caught" : "") + (locked ? " sc-card-unreleased" : "");

      if (locked) {
        card.setAttribute("aria-disabled", "true");
        const soon = document.createElement("span");
        soon.className = "sc-soon-badge";
        const dated = c.inGo && !!c.releaseDate;
        if (dated) {
          // An announced date is strictly better than "soon", so it replaces it rather than
          // sitting next to it: the corner has room for one badge.
          soon.classList.add("sc-date-badge");
          soon.textContent = shortReleaseDate(c.releaseDate);
          card.setAttribute("title", SH.tipShinyDated.replace("{date}", longReleaseDate(c.releaseDate)));
        } else {
          // Two honestly different states: in the game but no shiny yet, versus not in the game.
          soon.textContent = c.inGo ? SH.badgeSoon : SH.badgeNotInGo;
          card.setAttribute("title", c.inGo ? SH.tipShinyUnreleased : SH.tipNotInGo);
        }
        card.appendChild(soon);
      }

      if (caught) {
        const badge = document.createElement("span");
        badge.className = "sc-badge";
        badge.textContent = "✓";
        card.appendChild(badge);
      }

      const count = countMap.get(`${c.species.name}:${c.region}`) ?? 0;
      if (count > 1) {
        const countBadge = document.createElement("span");
        countBadge.className = "sc-count-badge";
        countBadge.textContent = `×${count}`;
        card.appendChild(countBadge);
      }

      const img = document.createElement("img");
      img.src = spriteUrl(c.spriteId);
      img.alt = c.species.name;
      img.className = "shiny-img";
      if (TINY_POKEMON.has(c.species.id)) img.classList.add("sprite-sm-poke");
      img.loading = "lazy";
      img.decoding = "async";
      img.onerror = () => { img.style.display = "none"; };

      const label = document.createElement("span");
      label.className = "shiny-label";
      label.textContent = regionalDisplayName(gameData, c.species.name, c.region);

      // Vivillon patterns show their real world collection region on hover. An unreleased card
      // keeps its "why can I not click this" tooltip instead.
      const geo = vivillonGeo(c.region);
      if (geo && !locked) card.setAttribute("title", geo);

      card.appendChild(img);
      card.appendChild(label);
      // No listener at all on a locked card, rather than a handler that declines: there is
      // genuinely nothing to record yet.
      if (!locked) card.addEventListener("click", () => openAddModal(c));
      grid.appendChild(card);
    }

    contentEl.innerHTML = "";
    contentEl.appendChild(grid);
  }

  // The picker is appended into `host` and the trigger button is put back there if it is
  // cancelled. Evolving succeeds by rebuilding the whole tab, which detaches `host`, so
  // onEvolved runs synchronously first to let the caller dismiss whatever owns it.
  function showEvolvePicker(
    host: HTMLElement,
    rec: UserShiny,
    options: EvolveTarget[],
    triggerBtn: HTMLButtonElement,
    onEvolved: () => void,
  ) {
    triggerBtn.remove();

    const picker = document.createElement("div");
    picker.className = "sc-evolve-picker";

    if (options.length === 1) {
      const label = document.createElement("span");
      label.textContent = SH.evolveInto.replace(
        "{name}",
        regionalDisplayName(gameData, options[0].name, options[0].region),
      );
      const confirmBtn = document.createElement("button");
      confirmBtn.className = "sc-evolve-confirm btn-primary";
      confirmBtn.textContent = "✓";
      const cancelBtn = document.createElement("button");
      cancelBtn.className = "sc-evolve-cancel";
      cancelBtn.textContent = "✕";

      confirmBtn.addEventListener("click", async () => {
        confirmBtn.disabled = true;
        cancelBtn.disabled = true;
        const ok = await apiEvolve(rec.id, options[0].name, options[0].region);
        if (ok) {
          onEvolved();
          userShinies = await fetchUserShinies();
          rebuildState(userShinies);
          updateCounter();
          renderTab();
        } else {
          picker.remove();
          host.appendChild(triggerBtn);
        }
      });
      cancelBtn.addEventListener("click", () => {
        picker.remove();
        host.appendChild(triggerBtn);
      });

      picker.appendChild(label);
      picker.appendChild(confirmBtn);
      picker.appendChild(cancelBtn);
    } else {
      const label = document.createElement("span");
      label.textContent = SH.evolvePick + ":";
      picker.appendChild(label);

      for (const opt of options) {
        const optPoke = shinyByName.get(opt.name);
        const optBtn = document.createElement("button");
        optBtn.className = "sc-evolve-option";
        optBtn.title = opt.name;

        if (optPoke) {
          const optImg = document.createElement("img");
          optImg.src = spriteUrl(
            opt.region ? regionalVariantId(opt.name, opt.region) || optPoke.id : optPoke.id,
          );
          optImg.alt = opt.name;
          optImg.onerror = () => { optImg.style.display = "none"; };
          optBtn.appendChild(optImg);
        }
        const optLabel = document.createElement("span");
        optLabel.textContent = regionalDisplayName(gameData, opt.name, opt.region);
        optBtn.appendChild(optLabel);

        optBtn.addEventListener("click", async () => {
          picker.querySelectorAll("button").forEach((b) => { (b as HTMLButtonElement).disabled = true; });
          const ok = await apiEvolve(rec.id, opt.name, opt.region);
          if (ok) {
            onEvolved();
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
        host.appendChild(triggerBtn);
      });
      picker.appendChild(cancelBtn);
    }

    host.appendChild(picker);
  }

  // A rendered entry card, kept so the edit modal can refresh the card underneath it after a
  // save without rebuilding the whole tab (which would detach the card the modal was opened
  // from). Invalidated wholesale by any renderTab().
  interface EntryCard {
    rec: UserShiny;
    el: HTMLElement;
    refreshSprite: () => void;
    refreshText: () => void;
  }

  const entryCards = new Map<number, EntryCard>();

  // The Caught and Evolved tabs. One card per recorded entry, in the same grid the checklist
  // tabs use, with editing moved into a modal. Duplicates therefore get one card each, which
  // is why the checklist's x2 count badge has no place here.
  function renderEntryCards(entries: UserShiny[]) {
    entryCards.clear();

    const grid = document.createElement("div");
    // Same grid as the checklist tabs, but on a wider track: these cards carry pills, and the
    // checklist's 90px column (68px on a small phone) cannot hold one legibly.
    grid.className = "shiny-grid shiny-grid--entries";

    for (const rec of entries) {
      const poke = shinyByName.get(rec.pokemon_id);
      const card = document.createElement("div");
      card.className = "shiny-tag sc-entry-card" + (rec.evolved_at ? " sc-row-evolved" : "");
      // The trainer page's detail view links here with ?entry=<id>, so the card needs to be findable.
      card.id = `shiny-${rec.id}`;

      // Method badge, top left. The icon alone would be a guess, so the method's own label
      // rides along as the tooltip. Nothing renders for an untagged catch, because
      // METHOD_ICONS has no "" key.
      const methodBadge = document.createElement("span");
      methodBadge.className = "sc-method-badge";
      card.appendChild(methodBadge);

      // Evolved marker, top right. Top left is the method badge's corner now.
      if (rec.evolved_at) {
        const chip = document.createElement("span");
        chip.className = "sc-evolved-chip";
        chip.textContent = "⬆️";
        chip.title = SH.evolved;
        card.appendChild(chip);
      }

      // Sprite. Regional entries use their variant sprite id and skip costume
      // art, which is only keyed by base dex.
      const stage = document.createElement("div");
      stage.className = "sc-card-stage";
      const img = document.createElement("img");
      img.className = "shiny-img";
      img.alt = rec.pokemon_id;
      img.loading = "lazy";
      img.decoding = "async";
      const refreshSprite = () => {
        if (!poke) { img.style.display = "none"; return; }
        img.style.display = "";
        if (rec.region) {
          img.src = spriteUrl(regionalVariantId(rec.pokemon_id, rec.region) || poke.id);
          img.onerror = () => { img.style.display = "none"; };
        } else {
          setSprite(img, poke.id, poke.name, rec.costume);
        }
      };
      refreshSprite();
      if (poke && TINY_POKEMON.has(poke.id)) img.classList.add("sprite-sm-poke");
      stage.appendChild(img);

      const label = document.createElement("span");
      label.className = "shiny-label";

      // How long ago it was caught. Every entry has a date, so this is the one line that is
      // always here, which is what stops a card with no costume or event reading as unfinished.
      const meta = document.createElement("span");
      meta.className = "sc-card-meta";

      // Form, costume and event as pills. Capped so a heavily tagged catch cannot grow taller
      // than a bare one; the editor behind a click has the full detail either way.
      const tags = document.createElement("span");
      tags.className = "sc-card-tags";
      const MAX_PILLS = 2;

      const refreshText = () => {
        label.textContent = regionalDisplayName(gameData, rec.pokemon_id, rec.region);

        const icon = METHOD_ICONS[rec.method];
        methodBadge.textContent = icon ?? "";
        methodBadge.title = icon ? (METHODS.find((m) => m.value === rec.method)?.label ?? "") : "";
        methodBadge.style.display = icon ? "" : "none";

        const when = rec.caught_at ? timeAgo(rec.caught_at) : "";
        meta.textContent = when;
        meta.style.display = when ? "" : "none";

        const pills: { text: string; kind: string }[] = [];
        if (rec.form === "shadow")   pills.push({ text: JSC.formShadow,   kind: "shadow" });
        if (rec.form === "purified") pills.push({ text: JSC.formPurified, kind: "purified" });
        if (rec.costume)             pills.push({ text: rec.costume,      kind: "costume" });
        if (rec.event_tag)           pills.push({ text: rec.event_tag,    kind: "event" });

        tags.textContent = "";
        for (const p of pills.slice(0, MAX_PILLS)) {
          const pill = document.createElement("span");
          pill.className = `sc-pill sc-pill--${p.kind}`;
          pill.textContent = p.text;
          // The pill ellipsises, so the full value has to stay reachable on hover.
          pill.title = p.text;
          tags.appendChild(pill);
        }
        if (pills.length > MAX_PILLS) {
          const more = document.createElement("span");
          more.className = "sc-pill sc-pill--more";
          more.textContent = `+${pills.length - MAX_PILLS}`;
          more.title = pills.slice(MAX_PILLS).map((p) => p.text).join(" · ");
          tags.appendChild(more);
        }
        tags.style.display = pills.length ? "" : "none";
      };
      refreshText();

      card.appendChild(stage);
      card.appendChild(label);
      card.appendChild(meta);
      card.appendChild(tags);

      const handle: EntryCard = { rec, el: card, refreshSprite, refreshText };
      entryCards.set(rec.id, handle);
      card.addEventListener("click", () => openEditModal(handle));
      grid.appendChild(card);
    }

    contentEl.appendChild(grid);
  }

  const editImg    = document.getElementById("sc-edit-img") as HTMLImageElement;
  const editName   = document.getElementById("sc-edit-name")!;
  const editFields = document.getElementById("sc-edit-fields")!;
  const editActions = document.getElementById("sc-edit-actions")!;

  // Edit one recorded entry. Everything mutable lives in this call's closure rather than at
  // init() scope: both containers are emptied on every open, so no listener from a previous
  // open can survive to be re-entered, and nothing needs an explicit reset.
  function openEditModal(card: EntryCard) {
    const rec  = card.rec;
    const poke = shinyByName.get(rec.pokemon_id);
    const gen  = ++editGeneration;

    editFields.innerHTML = "";
    editActions.innerHTML = "";
    mergeLiveEventOptions();

    editName.textContent = regionalDisplayName(gameData, rec.pokemon_id, rec.region);
    editImg.className = "sc-edit-img" + (poke && TINY_POKEMON.has(poke.id) ? " sprite-sm-poke" : "");

    // The header sprite previews the costume being typed, so it follows the live input value.
    // The card underneath follows rec, and so only moves once a save succeeds.
    const refreshHeadSprite = () => {
      if (!poke) { editImg.style.display = "none"; return; }
      editImg.style.display = "";
      if (rec.region) {
        editImg.src = spriteUrl(regionalVariantId(rec.pokemon_id, rec.region) || poke.id);
        editImg.onerror = () => { editImg.style.display = "none"; };
      } else {
        setSprite(editImg, poke.id, poke.name, costumeInput.value.trim());
      }
    };

    const field = (labelText: string, control: HTMLElement) => {
      const wrap = document.createElement("div");
      wrap.className = "sc-field-wrap";
      const lbl = document.createElement("label");
      lbl.className = "sc-field-label";
      lbl.textContent = labelText;
      wrap.appendChild(lbl);
      wrap.appendChild(control);
      editFields.appendChild(wrap);
    };

    const formSel = makeSelect(FORMS, rec.form);
    editFields.appendChild(formSel);

    // Region selector, for species that have recordable regional forms in GO
    // (card forms like Vivillon patterns, plus carry-only forms like the
    // Scatterbug/Spewpa patterns). Lets older entries be marked retroactively.
    const regionalOptions = recordableRegions(rec.pokemon_id);
    let regionSel: HTMLSelectElement | null = null;
    if (regionalOptions.length || rec.region) {
      const opts = [
        { value: "", label: JSC.formOriginal },
        ...regionalOptions.map((f) => ({
          value: f.region as string,
          label: REGION_LABELS[f.region] ?? f.region,
        })),
      ];
      // An entry may carry a region the constant no longer offers; keep it
      // selectable so the modal does not silently misrepresent the entry.
      if (rec.region && !opts.some((o) => o.value === rec.region)) {
        opts.push({ value: rec.region, label: REGION_LABELS[rec.region] ?? rec.region });
      }
      regionSel = makeSelect(opts, rec.region);
      editFields.appendChild(regionSel);
    }

    const costumeSuggest = costumePicker(
      poke?.id ?? 0,
      rec.pokemon_id,
      rec.costume,
      () => refreshHeadSprite(),
      () => saveUpdate(),
    );
    const costumeInput = costumeSuggest.input;
    field(SH.costumeLabel, costumeSuggest.root);

    const eventInput = document.createElement("input");
    eventInput.type = "text";
    eventInput.className = "move-select";
    eventInput.value = rec.event_tag;
    eventInput.placeholder = SH.eventPlaceholder;
    eventInput.setAttribute("list", "sc-event-list");
    eventInput.addEventListener("focus", () => mergeLiveEventOptions());
    field(SH.eventLabel, eventInput);

    const methodSel = makeSelect(METHODS, rec.method);
    editFields.appendChild(methodSel);

    // dateInputValue reads the stored day back in UTC (that is how it is stored);
    // todayInputValue caps the picker by the trainer's own clock. They are not interchangeable.
    const caughtInput = document.createElement("input");
    caughtInput.type = "date";
    caughtInput.className = "move-select";
    caughtInput.title = SH.caught;
    caughtInput.value = dateInputValue(rec.caught_at);
    caughtInput.max = todayInputValue();
    field(SH.caught, caughtInput);

    // What the modal was showing when it opened. saveUpdate sends the date only when this
    // changes, so editing a costume or a method can never rewrite the catch date as a side
    // effect. Reseeded on every open, and unreachable from any earlier open's listeners.
    let savedCaughtAt = caughtInput.value;

    const statusEl = document.createElement("span");
    statusEl.className = "sc-save-status";

    let saveTimer: ReturnType<typeof setTimeout>;

    const saveUpdate = async () => {
      const newForm     = formSel.value;
      const newRegion   = regionSel ? regionSel.value : rec.region;
      const newCostume  = costumeInput.value;
      const newEventTag = eventInput.value;
      const newMethod   = methodSel.value;
      // Empty means "leave the stored date alone", so an edit to any other field never touches it.
      const newCaughtAt = caughtInput.value === savedCaughtAt ? "" : caughtInput.value;
      let res: Response;
      try {
        res = await apiUpdate(rec.id, newForm, newRegion, newCostume, newEventTag, newMethod, newCaughtAt);
      } catch (e) {
        console.error("shiny update failed (network):", e);
        if (gen === editGeneration) statusEl.textContent = JSC.error;
        return;
      }
      // The modal has been closed or reopened on another entry while this was in flight, so
      // there is nothing left to update and the status line now belongs to someone else.
      if (gen !== editGeneration) return;
      if (res.ok) {
        rec.form      = newForm;
        rec.region    = newRegion;
        rec.costume   = newCostume;
        rec.event_tag = newEventTag;
        rec.method    = newMethod;
        if (newCaughtAt) {
          rec.caught_at = newCaughtAt;
          savedCaughtAt = newCaughtAt;
        }
        // Full rebuild rather than incremental delete/set: with duplicate
        // entries sharing a key, an incremental update would drop the
        // surviving duplicate from the index until the next refetch.
        rebuildState(userShinies);
        updateCounter();
        editName.textContent = regionalDisplayName(gameData, rec.pokemon_id, rec.region);
        refreshHeadSprite();
        card.refreshText();
        card.refreshSprite();
        statusEl.textContent = SH.saved;
        clearTimeout(saveTimer);
        saveTimer = setTimeout(() => { statusEl.textContent = ""; }, 1500);
      } else {
        console.error("shiny update failed:", res.status, rec.pokemon_id);
        formSel.value = rec.form;
        if (regionSel) regionSel.value = rec.region;
        costumeInput.value = rec.costume;
        eventInput.value   = rec.event_tag;
        methodSel.value    = rec.method;
        // Restore the date too. Leaving the rejected value in the input would let the next
        // successful save of any other field commit it, because it would read as a change.
        caughtInput.value  = savedCaughtAt;
        statusEl.textContent = JSC.error;
      }
    };

    formSel.addEventListener("change", saveUpdate);
    if (regionSel) regionSel.addEventListener("change", saveUpdate);
    // The costume field wires its own change/input handlers in costumePicker(), because choosing a
    // row from the dropdown has to count as a commit even though it fires no native change event.
    eventInput.addEventListener("change", saveUpdate);
    methodSel.addEventListener("change", saveUpdate);
    caughtInput.addEventListener("change", saveUpdate);

    // Evolve button -- transforms entry into next form
    const evolveBtn = document.createElement("button");
    evolveBtn.className = "sc-evolve-btn";
    evolveBtn.textContent = SH.evolveBtn;
    evolveBtn.title = SH.evolveBtn;
    evolveBtn.addEventListener("click", () => {
      const targets = getEvolveTargets(rec.pokemon_id, rec.region);
      // Gated on "is in Pokemon GO", not "has a shiny released". If you caught a shiny Cosmog you
      // can evolve it the moment the game lets you, rather than waiting for our flag to catch up:
      // that lag is precisely what this whole feature exists to remove. Species that are not in
      // the game at all still do not appear.
      const available = targets.filter((t) => t.name && (dexByName.get(t.name)?.in_go ?? shinyByName.has(t.name)));
      if (!available.length) {
        evolveBtn.textContent = SH.evolveNone;
        evolveBtn.disabled = true;
        setTimeout(() => {
          evolveBtn.textContent = SH.evolveBtn;
          evolveBtn.disabled = false;
        }, 2000);
        return;
      }
      showEvolvePicker(editActions, rec, available, evolveBtn, closeEditModal);
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
        closeEditModal();
        userShinies = await fetchUserShinies();
        rebuildState(userShinies);
        updateCounter();
        renderTab();
      } else {
        removeBtn.disabled = false;
        removeBtn.textContent = JSC.remove;
      }
    });

    editActions.appendChild(evolveBtn);
    editActions.appendChild(removeBtn);
    editActions.appendChild(statusEl);

    refreshHeadSprite();
    editModal.classList.add("open");
  }

  function renderCaughtList(evolvedOnly = false) {
    const source = evolvedOnly ? evolvedShinies : userShinies;
    const q = searchEl.value.trim().toLowerCase();
    const keep = q ? familyMatchSet(q, source.map((s) => s.pokemon_id)) : null;
    const entries = keep
      ? source.filter(
          (s) =>
            keep.has(s.pokemon_id) ||
            (!!s.region && (REGION_LABELS[s.region] ?? s.region).toLowerCase().includes(q)),
        )
      : source;

    contentEl.innerHTML = "";

    if (!entries.length) {
      contentEl.innerHTML = q
        ? `<p class="empty-state">${JSC.noResults}</p>`
        : `<p class="empty-state">${SH.nothingCaught}</p>`;
      return;
    }

    renderEntryCards(entries);
  }

  let activeTab = "all";

  function renderTab() {
    // The unreleased toggle is only meaningful on the two grid tabs.
    const toggleWrap = document.getElementById("sc-unreleased-wrap");
    if (toggleWrap) {
      toggleWrap.style.display = activeTab === "all" || activeTab === "missing" ? "" : "none";
    }

    if (activeTab === "caught") {
      searchEl.placeholder = SH.filterCaught;
      renderCaughtList(false);
    } else if (activeTab === "evolved") {
      searchEl.placeholder = SH.filterCaught;
      renderCaughtList(true);
    } else {
      // With the toggle off this is byte for byte the list that shipped before the flags existed.
      const visible = allCards.filter(cardVisible);
      // Missing is a to-do list of shinies you could go out and catch, so an announced one does not
      // belong on it however visible it is on All. Ticking the toggle still opts into the wish list.
      const source = activeTab === "missing"
        ? visible.filter((c) => (c.released || unreleasedEl.checked)
            && !cardCaught(c.species.name, c.region, caughtCardKeys))
        : visible;
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

  // Debounced: a keystroke re-filters, re-runs a family search per matching name, and rebuilds the
  // whole grid, and nobody types one character per frame.
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  searchEl.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(renderTab, 150);
  });

  unreleasedEl.addEventListener("change", () => {
    try {
      localStorage.setItem(SHOW_UNRELEASED_KEY, unreleasedEl.checked ? "1" : "0");
    } catch { /* private mode: the toggle just does not persist */ }
    renderTab();
  });

  updateCounter();
  renderTab();

  // Deep link from the detail view on your own trainer page: /shinies?entry=<id>. Switch to the
  // Caught tab (the entry only exists there), then scroll to it and flash it, because the grid can
  // be hundreds of cards long and "we took you to the right page" is not the same as finding it.
  // Opening the editor too, since editing that entry is the only reason to follow the link.
  const entryId = new URLSearchParams(location.search).get("entry");
  if (entryId) {
    const caughtBtn = tabsEl.querySelector('.tab-btn[data-tab="caught"]') as HTMLButtonElement | null;
    // Populates entryCards synchronously, so the lookup below is safe.
    if (caughtBtn) caughtBtn.click();
    const handle = entryCards.get(Number(entryId));
    if (handle) {
      handle.el.scrollIntoView({ behavior: "smooth", block: "center" });
      handle.el.classList.add("sc-entry-flash");
      setTimeout(() => handle.el.classList.remove("sc-entry-flash"), 2000);
      openEditModal(handle);
    }
  }
}

init();
