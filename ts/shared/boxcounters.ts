// "How do MY Pokemon do against this boss", rendered inside the counters panel.
//
// The top counters list answers what the best possible answer is. This answers
// what a trainer actually owns, which is the question they came with. It reads
// the box saved at /box and scores every entry against the boss currently open.
//
// Shared because the raids page builds two independent counter panels, one for
// raids and one for Max Battles, and this belongs in both.

import { calcMovesetRange, moveTypesFor, DEFAULT_CONFIG } from "./counters";
import type { PokemonConfig, PokemonForm } from "./counters";
import { pokeSprite, pokeName, typeEffectiveness } from "./gamedata";
import { typeBadge } from "./typecolors";
import type { GameData, RaidBoss, PokemonStat } from "./types";

declare const JSC: Record<string, string>;
declare const RD: Record<string, string>;

// The middle of a candidate set, computed server side. The raw candidate array
// is deliberately not sent: it is the biggest thing a row holds and the whole
// box is fetched at once.
interface ApproxIVs {
  atk_iv: number;
  def_iv: number;
  sta_iv: number;
}

export interface BoxEntry {
  id: number;
  pokemon_name: string;
  form: string;
  is_shadow: boolean | null;
  is_purified: boolean | null;
  cp: number;
  level: number;
  atk_iv: number | null;
  def_iv: number | null;
  sta_iv: number | null;
  approx_ivs?: ApproxIVs | null;
}

// The number shown before the expander. Enough to answer "what do I bring"
// without turning the panel into a second full length table.
const PREVIEW_COUNT = 5;

// Matches maxPokemonBoxSize in internal/handlers/iv.go. The whole box is asked
// for rather than a page of it: ranking only the most recently added Pokemon
// would answer the wrong question, since the best counter is as likely to be one
// caught years ago.
const MAX_BOX = 9000;

// Three distinct outcomes, not two: signed out, loaded, and failed. Collapsing
// the third into the first told signed in trainers to log in whenever the server
// erred, and hid the fault behind the least diagnosable message available.
export const FAILED = Symbol("box-load-failed");
export type BoxLoad = BoxEntry[] | null | typeof FAILED;

let boxPromise: Promise<BoxLoad> | null = null;

// loadBox fetches once per page load. Expanding five bosses in a row should not
// mean five identical requests, and the box does not change while you browse.
// null means "not logged in", which is a different state from an empty box.
export function loadBox(): Promise<BoxLoad> {
  if (!boxPromise) {
    // 401 means signed out. Anything else is a failure, and telling a signed in
    // trainer to log in because the server erred teaches the wrong reflex and
    // hides the real fault.
    boxPromise = fetch(`/api/iv/pokemon?limit=${MAX_BOX}`)
      .then((r) => {
        if (r.ok) return r.json();
        if (r.status === 401) return null;
        throw new Error(String(r.status));
      })
      .then((d: { pokemon?: BoxEntry[] } | null) => d?.pokemon ?? null)
      .catch(() => {
        boxPromise = null; // a failure must not stick for the whole page
        return FAILED;
      });
  }
  return boxPromise;
}

// No reset function: /box and /raids are separate page loads with separate
// bundles, so editing the box and then opening the raids page already starts
// from an empty memo. One existed and had no callers, which only invited the
// belief that some invalidation was happening.

// renderBoxPlaceholder is appended synchronously so the panel shows that
// something is coming. Without it the counters table rendered, then nothing
// happened for as long as the box took to arrive and score, with no clue that a
// section was on its way.
export function renderBoxPlaceholder(): HTMLElement {
  const section = el("div", "counter-results box-vs-boss box-vs-boss-loading");
  section.appendChild(el("h3", undefined, RD.yourPokemonLoading));
  const state = el("div", "loading-state");
  state.appendChild(el("div", "spinner"));
  section.appendChild(state);
  return section;
}

// The upstream multiplier table stops at level 45, but a box entry can be any
// level up to 50, and a maxed XL Pokemon is exactly the one a trainer wants
// ranked. Taking the table at face value silently dropped every entry above 45
// from the results, with nothing on screen to say so.
//
// The missing rows are derived with the same rules the IV calculator already
// uses server side (cpmLookup in internal/handlers/iv.go), verified there
// against GoIV and Silph:
//   whole levels above 40:  CPM(L+1)   = CPM(L) + 0.005
//   half levels:            CPM(L+0.5) = sqrt((CPM(L)^2 + CPM(L+1)^2) / 2)
let cpmTable: Map<number, number> | null = null;

function buildCPMTable(data: GameData): Map<number, number> {
  const m = new Map<number, number>();
  for (const c of data.cpMultipliers ?? []) m.set(c.level, c.multiplier);

  for (let lvl = 41; lvl <= 51; lvl++) {
    if (m.has(lvl)) continue;
    const prev = m.get(lvl - 1);
    if (prev !== undefined) m.set(lvl, prev + 0.005);
  }
  for (let lvl = 40.5; lvl <= 50.5; lvl += 1) {
    if (m.has(lvl)) continue;
    const lo = m.get(lvl - 0.5);
    const hi = m.get(lvl + 0.5);
    if (lo !== undefined && hi !== undefined) m.set(lvl, Math.sqrt((lo * lo + hi * hi) / 2));
  }
  return m;
}

function cpmForLevel(data: GameData, level: number): number | null {
  if (!cpmTable) cpmTable = buildCPMTable(data);
  return cpmTable.get(level) ?? null;
}


export interface ScoredEntry {
  entry: BoxEntry;
  bestDps: number;
  worstDps: number;
  tdo: number;
  fastMove: string;
  fastType: string;
  chargedMove: string;
  chargedType: string;
  pokemonId: number;
  approximate: boolean;
}

// SCORE_LIMIT bounds the expensive half. A full box is 9000 Pokemon and the real
// scoring walks every fast and charged move pairing for each one, which is far
// too much to do on a phone every time a boss is opened.
//
// So the box is cheaply pre-ranked first and only the best SCORE_LIMIT entries
// are scored properly. The pre-rank is attack stat times the best effectiveness
// among the move types the Pokemon can actually bring.
//
// 150 is measured, not guessed. The only test that matters is whether the
// shortlist's top five matches the top five you get by scoring the whole box,
// checked against every live boss over a box holding one of every form the game
// has (1420 entries):
//
//   cut   result
//   44    4 of 19 bosses show a wrong top five
//   100   1 of 19 wrong (Hisuian Avalugg loses Lucario)
//   150   clean
//
// check:box asserts this every run, so the number cannot quietly stop being
// enough as the dex grows.
//
// An earlier version cut at 44 AND pre-ranked on the species' own types, which
// reads Alakazam as a Psychic attacker and never notices its Shadow Ball. That
// lost the true top five on 13 of 19 bosses.
let SCORE_LIMIT = 150;

// Test only: lets the parity harness compare the shortlist against scoring
// everything. Not called by the app.
const SHIPPED_SCORE_LIMIT = 150;
export function setScoreLimitForTest(n: number | null) {
  SCORE_LIMIT = n === null ? SHIPPED_SCORE_LIMIT : n;
  scoreCache.clear();
}

// Indexed once instead of scanning the species list per entry. The scan was a
// linear pass over roughly 1100 species with two lowercase allocations per
// comparison, run for every Pokemon in the box, which on a full box is millions
// of string compares on the main thread while the page sits frozen.
let speciesIndex: Map<string, PokemonStat> | null = null;

function speciesByName(data: GameData, name: string, form?: string): PokemonStat | undefined {
  let idx = speciesIndex;
  if (!idx) {
    idx = new Map<string, PokemonStat>();
    for (const p of data.pokemon ?? []) {
      const key = p.pokemon_name.toLowerCase();
      const fk = key + "|" + (p.form ?? "").toLowerCase();
      idx.set(fk, p);
      if (!idx.has(key)) idx.set(key, p);
    }
    speciesIndex = idx;
  }
  const lower = name.toLowerCase();
  if (form) {
    const exact = idx.get(lower + "|" + form.toLowerCase());
    if (exact) return exact;
  }
  return idx.get(lower + "|normal") ?? idx.get(lower);
}

// Move types are looked up once per species and form, not per box entry. A box
// holds many copies of the same species, and this is the only part of the
// pre-rank that costs anything.
const moveTypeCache = new Map<string, string[]>();

function cachedMoveTypes(data: GameData, name: string, form: string): string[] {
  const key = `${name.toLowerCase()}|${form.toLowerCase()}`;
  let hit = moveTypeCache.get(key);
  if (!hit) {
    hit = moveTypesFor(data, name, form);
    moveTypeCache.set(key, hit);
  }
  return hit;
}

function preRank(data: GameData, boss: RaidBoss, entry: BoxEntry, atkIV: number): number {
  const poke = speciesByName(data, entry.pokemon_name, entry.form);
  if (!poke) return -1;
  const cpm = cpmForLevel(data, entry.level);
  // No multiplier means the entry cannot be scored at all, so it must not take a
  // shortlist place. It used to score 0 and pass the rank >= 0 filter, occupying
  // a slot and then being dropped.
  if (cpm === null) return -1;
  const atk = (poke.base_attack + atkIV) * cpm * (entry.is_shadow ? 1.2 : 1);
  const bossTypes = boss.types ?? [];
  if (!bossTypes.length) return atk;

  // Effectiveness of the moves it can actually bring, not of the species' own
  // typing. Alakazam is Psychic and useless on paper against a Dark boss, while
  // its Shadow Ball and Fire Punch are exactly why you would bring it.
  let bestEff = 0;
  for (const t of cachedMoveTypes(data, entry.pokemon_name, entry.form)) {
    const eff = typeEffectiveness(data, t, bossTypes);
    if (eff > bestEff) bestEff = eff;
  }
  return atk * (bestEff || 1);
}

// buildShortlist pre-ranks the box and keeps the best entry per species.
//
// The per species step is what stops the answer being useless. Trainers keep
// several of the same Pokemon, so without it a box holding six Machamps returns
// "Machamp, Machamp, Machamp, Machamp, Machamp" as its top five, which answers
// nothing: you already knew you had Machamps. Keeping the strongest of each
// gives five genuinely different options, and the weaker duplicates could never
// have placed above their own better copy anyway.
//
// Deduping BEFORE the cut also stops one over represented species from eating
// the whole shortlist.
function buildShortlist(data: GameData, boss: RaidBoss, box: BoxEntry[]): BoxEntry[] {
  const bestPerSpecies = new Map<string, { entry: BoxEntry; rank: number }>();
  for (const entry of box) {
    const rank = preRank(data, boss, entry, entry.atk_iv ?? 10);
    if (rank < 0) continue;
    const key = `${entry.pokemon_name.toLowerCase()}|${(entry.form || "").toLowerCase()}|${entry.is_shadow ? "s" : ""}`;
    const held = bestPerSpecies.get(key);
    if (!held || rank > held.rank) bestPerSpecies.set(key, { entry, rank });
  }
  return [...bestPerSpecies.values()]
    .sort((a, b) => b.rank - a.rank)
    .slice(0, SCORE_LIMIT)
    .map((x) => x.entry);
}

// Memoised per boss. Expanding five bosses used to pay the full scoring cost
// five times over, and re-expanding one you already looked at paid it again.
const scoreCache = new Map<string, ScoredEntry[]>();

export function scoreBox(data: GameData, boss: RaidBoss, box: BoxEntry[]): ScoredEntry[] {
  const cacheKey = `${boss.pokemon_name}|${box.length}`;
  const cached = scoreCache.get(cacheKey);
  if (cached) return cached;

  const shortlist = buildShortlist(data, boss, box);

  const out: ScoredEntry[] = [];
  for (const entry of shortlist) {
    let atk = entry.atk_iv;
    let def = entry.def_iv;
    let sta = entry.sta_iv;
    let approximate = false;

    // A Pokemon the calculator could not pin down still appears, scored on the
    // middle of its possibilities and marked, rather than being dropped or
    // quietly treated as perfect. One with nothing to go on at all is skipped.
    if (atk === null || def === null || sta === null) {
      const avg = entry.approx_ivs;
      if (!avg) continue;
      atk = avg.atk_iv;
      def = avg.def_iv;
      sta = avg.sta_iv;
      approximate = true;
    }

    const cpm = cpmForLevel(data, entry.level);
    if (cpm === null) continue;

    const form: PokemonForm = entry.is_shadow ? "shadow" : entry.is_purified ? "purified" : "normal";
    const config: PokemonConfig = { ...DEFAULT_CONFIG, cpm, atkIV: atk, defIV: def, staIV: sta, form };

    const range = calcMovesetRange(data, entry.pokemon_name, boss, config, entry.form);
    if (!range) continue;

    out.push({
      entry,
      bestDps: range.best.dps,
      worstDps: range.worst.dps,
      tdo: range.best.tdo,
      fastMove: range.best.fastMove,
      fastType: range.best.fastType,
      chargedMove: range.best.chargedMove,
      chargedType: range.best.chargedType,
      pokemonId: range.best.pokemonId,
      approximate,
    });
  }
  out.sort((a, b) => b.bestDps - a.bestDps);
  scoreCache.set(cacheKey, out);
  return out;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

function stateBlock(heading: string, message: string, linkHref?: string, linkText?: string): HTMLElement {
  const section = el("div", "counter-results box-vs-boss");
  section.appendChild(el("h3", undefined, heading));
  const p = el("p", "empty-state", message);
  section.appendChild(p);
  if (linkHref && linkText) {
    const wrap = el("p");
    const a = el("a", undefined, linkText);
    a.setAttribute("href", linkHref);
    wrap.appendChild(a);
    section.appendChild(wrap);
  }
  return section;
}

function buildRow(data: GameData, s: ScoredEntry, rank: number): HTMLElement {
  const tr = el("tr");

  tr.appendChild(el("td", undefined, String(rank)));

  const nameTd = el("td");
  nameTd.style.whiteSpace = "nowrap";
  const img = el("img");
  img.src = pokeSprite(s.pokemonId);
  img.alt = s.entry.pokemon_name;
  img.className = "poke-sprite";
  img.loading = "lazy";
  img.decoding = "async";
  nameTd.appendChild(img);
  nameTd.append(` ${pokeName(data, s.entry.pokemon_name)}`);
  // Both forms of a species can now rank, so the row has to say which one it is.
  if (s.entry.form && s.entry.form !== "Normal") nameTd.append(` (${s.entry.form})`);
  if (s.entry.is_shadow) nameTd.appendChild(el("span", "box-tag box-tag-shadow", RD.shadowTag));
  if (s.approximate) {
    const q = el("span", "box-tag box-tag-approx", RD.approxTag);
    q.title = RD.approxHint;
    nameTd.appendChild(q);
  }
  tr.appendChild(nameTd);

  tr.appendChild(el("td", undefined, String(s.entry.level)));

  const fastTd = el("td");
  fastTd.appendChild(typeBadge(s.fastType));
  fastTd.append(` ${s.fastMove}`);
  tr.appendChild(fastTd);

  const chargedTd = el("td");
  chargedTd.appendChild(typeBadge(s.chargedType));
  chargedTd.append(` ${s.chargedMove}`);
  tr.appendChild(chargedTd);

  // Best on its own line, worst underneath: the box does not record which moves
  // a Pokemon actually knows, so a single figure would present the best case as
  // if it were certain.
  const dpsTd = el("td");
  dpsTd.appendChild(el("span", "box-dps-best", s.bestDps.toFixed(2)));
  if (s.worstDps < s.bestDps) {
    dpsTd.appendChild(el("span", "box-dps-worst", RD.worstDps.replace("{dps}", s.worstDps.toFixed(2))));
  }
  tr.appendChild(dpsTd);

  tr.appendChild(el("td", undefined, s.tdo.toFixed(0)));
  return tr;
}

// renderBoxVsBoss returns the section to append under the counters table.
export async function renderBoxVsBoss(data: GameData, boss: RaidBoss): Promise<HTMLElement> {
  // Substituted once, here, so no path can reach the DOM still carrying the
  // literal placeholder. It used to be built twice and the state blocks skipped
  // the replace, which put "Your Pokemon vs {name}" in front of every logged out
  // visitor who opened a boss.
  const heading = RD.yourPokemon.replace("{name}", pokeName(data, boss.pokemon_name));

  const box = await loadBox();
  if (box === FAILED) return stateBlock(heading, RD.boxLoadFailed);
  if (box === null) return stateBlock(heading, RD.boxLoggedOut, "/login?next=/raids", RD.boxLogIn);
  if (!box.length) return stateBlock(heading, RD.boxEmpty, "/box", RD.boxAdd);

  const scored = scoreBox(data, boss, box);
  if (!scored.length) return stateBlock(heading, RD.boxNoneUsable, "/box", RD.boxAdd);

  const section = el("div", "counter-results box-vs-boss");
  section.appendChild(el("h3", undefined, heading));

  const wrap = el("div", "table-wrap");
  const table = el("table", "counter-table");
  const thead = el("thead");
  const hr = el("tr");
  for (const h of ["#", JSC.pokemon, RD.colLevel ?? "Level", JSC.fastMove, JSC.chargedMove, "DPS", "TDO"]) {
    hr.appendChild(el("th", undefined, h));
  }
  thead.appendChild(hr);
  table.appendChild(thead);

  const tbody = el("tbody");
  scored.slice(0, PREVIEW_COUNT).forEach((s, i) => tbody.appendChild(buildRow(data, s, i + 1)));
  table.appendChild(tbody);
  wrap.appendChild(table);
  section.appendChild(wrap);

  if (scored.length > PREVIEW_COUNT) {
    // A toggle, not a one way door. On a phone, expanding 44 rows with no way
    // back means scrolling past all of them to reach anything below, and the
    // only escape was collapsing the whole boss panel and losing the counters
    // with it.
    const more = el("button", "btn btn-sm box-show-all", RD.showAll.replace("{n}", String(scored.length)));
    more.type = "button";
    more.setAttribute("aria-expanded", "false");
    let expanded = false;
    more.addEventListener("click", () => {
      expanded = !expanded;
      more.setAttribute("aria-expanded", expanded ? "true" : "false");
      if (expanded) {
        scored.slice(PREVIEW_COUNT).forEach((s, i) => tbody.appendChild(buildRow(data, s, PREVIEW_COUNT + i + 1)));
        more.textContent = RD.showFewer;
      } else {
        while (tbody.children.length > PREVIEW_COUNT) tbody.lastElementChild?.remove();
        more.textContent = RD.showAll.replace("{n}", String(scored.length));
      }
    });
    section.appendChild(more);
  }

  const note = el("p", "subscribe-hint", RD.movesetNote);
  section.appendChild(note);

  return section;
}
