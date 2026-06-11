import { loadGameData, pokemonByName, pokeSprite, pokeName } from "./shared/gamedata";
import { buildTabs } from "./shared/tabs";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, PokemonStat, CPMultiplier } from "./shared/types";

declare const JSC: Record<string, string>;
declare const PV: Record<string, string>;

const app = document.getElementById("pvp-app")!;

const LEAGUES = [
  { id: "great",  name: PV.leagueGreat,  cap: 1500 },
  { id: "ultra",  name: PV.leagueUltra,  cap: 2500 },
  { id: "master", name: PV.leagueMaster, cap: Infinity },
];

function calcCP(
  baseAtk: number, baseDef: number, baseSta: number,
  ivAtk: number, ivDef: number, ivSta: number,
  cpMult: number
): number {
  const atk = (baseAtk + ivAtk) * cpMult;
  const def = (baseDef + ivDef) * cpMult;
  const sta = (baseSta + ivSta) * cpMult;
  return Math.max(10, Math.floor(atk * Math.sqrt(def) * Math.sqrt(sta) / 10));
}

function bestLevel(
  mults: CPMultiplier[], poke: PokemonStat,
  ivAtk: number, ivDef: number, ivSta: number, cap: number
): { level: number; cp: number; cpMult: number } | null {
  let best: { level: number; cp: number; cpMult: number } | null = null;
  for (const m of mults) {
    const cp = calcCP(poke.base_attack, poke.base_defense, poke.base_stamina, ivAtk, ivDef, ivSta, m.multiplier);
    if (cp <= cap) best = { level: m.level, cp, cpMult: m.multiplier };
  }
  return best;
}

function statProduct(poke: PokemonStat, ivAtk: number, ivDef: number, ivSta: number, cpMult: number): number {
  return (poke.base_attack + ivAtk) * cpMult
    * (poke.base_defense + ivDef) * cpMult
    * Math.floor((poke.base_stamina + ivSta) * cpMult);
}

interface IVRank { ivAtk: number; ivDef: number; ivSta: number; level: number; cp: number; sp: number; rank: number; }

function rankIVs(data: GameData, poke: PokemonStat, cap: number): IVRank[] {
  const mults = data.cpMultipliers ?? [];
  const raw: Omit<IVRank, "rank">[] = [];
  for (let a = 0; a <= 15; a++)
    for (let d = 0; d <= 15; d++)
      for (let s = 0; s <= 15; s++) {
        const b = bestLevel(mults, poke, a, d, s, cap);
        if (!b) continue;
        raw.push({ ivAtk: a, ivDef: d, ivSta: s, level: b.level, cp: b.cp, sp: statProduct(poke, a, d, s, b.cpMult) });
      }
  raw.sort((a, b) => b.sp - a.sp);
  return raw.map((r, i) => ({ ...r, rank: i + 1 }));
}

function buildLeaguePanel(data: GameData, cap: number): () => HTMLElement {
  return () => {
    const wrap = document.createElement("div");

    const form = document.createElement("div");
    form.className = "calc-form";

    const input = document.createElement("input");
    input.type = "text";
    input.className = "search-input";
    input.placeholder = PV.namePh;

    const btn = document.createElement("button");
    btn.className = "btn-primary";
    btn.textContent = PV.showRankings;

    form.appendChild(input);
    form.appendChild(btn);

    const resultArea = document.createElement("div");
    resultArea.className = "result-area";

    btn.addEventListener("click", () => {
      const poke = pokemonByName(data, input.value.trim());
      if (!poke) {
        resultArea.innerHTML = "";
        const errP = document.createElement("p");
        errP.className = "error-text";
        errP.textContent = PV.notFound.replace("{name}", input.value.trim());
        resultArea.appendChild(errP);
        return;
      }
      const league = LEAGUES.find((l) => l.cap === cap)!;
      const ranks = rankIVs(data, poke, cap);

      resultArea.innerHTML = "";

      const pvpHeader = document.createElement("div");
      pvpHeader.className = "result-poke-header";
      const pvpSprite = document.createElement("img");
      pvpSprite.src = pokeSprite(poke.pokemon_id);
      pvpSprite.alt = poke.pokemon_name;
      pvpSprite.className = "result-poke-sprite";
      pvpSprite.loading = "lazy"; pvpSprite.decoding = "async";
      const h3 = document.createElement("h3");
      h3.style.marginBottom = "0";
      h3.textContent = `${pokeName(data, poke.pokemon_name)}: ${league.name}`;
      pvpHeader.appendChild(pvpSprite);
      pvpHeader.appendChild(h3);
      resultArea.appendChild(pvpHeader);

      const genusEl = document.createElement("span");
      genusEl.className = "poke-genus";
      resultArea.appendChild(genusEl);

      const badgeEl = document.createElement("span");
      badgeEl.style.display = "none";
      resultArea.appendChild(badgeEl);

      const flavorP = document.createElement("p");
      flavorP.className = "poke-flavor";
      flavorP.style.display = "none";
      resultArea.appendChild(flavorP);

      fetchSpeciesData(poke.pokemon_name).then(d => {
        if (d.genus)  { genusEl.textContent = JSC.theGenus.replace("{genus}", d.genus); }
        if (d.isLegendary || d.isMythical) {
          badgeEl.textContent  = d.isMythical ? JSC.mythical : JSC.legendary;
          badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
          badgeEl.style.display = "";
        }
        if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
      });

      const note = document.createElement("p");
      note.className = "league-note";
      note.textContent = PV.rankedNote.replace("{n}", ranks.length.toLocaleString());
      resultArea.appendChild(note);

      const tableWrap = document.createElement("div");
      tableWrap.className = "table-wrap fade-up";

      const table = document.createElement("table");
      table.className = "iv-table";
      table.innerHTML = `<thead><tr>
        <th>${PV.colRank}</th><th>${PV.colAtk}</th><th>${PV.colDef}</th><th>${PV.colSta}</th>
        <th>${PV.colLevel}</th><th>${JSC.cp}</th><th>${PV.colSp}</th>
      </tr></thead>`;

      const tbody = document.createElement("tbody");
      for (const r of ranks.slice(0, 25)) {
        const cls = r.rank === 1 ? "rank-s" : r.rank <= 10 ? "rank-a" : "";
        const tr = document.createElement("tr");
        tr.innerHTML = `
          <td class="${cls}">${r.rank === 1 ? "🥇 1" : r.rank}</td>
          <td>${r.ivAtk}</td><td>${r.ivDef}</td><td>${r.ivSta}</td>
          <td>${r.level}</td><td>${r.cp}</td>
          <td class="${cls}">${Math.round(r.sp).toLocaleString()}</td>
        `;
        tbody.appendChild(tr);
      }
      table.appendChild(tbody);
      tableWrap.appendChild(table);
      resultArea.appendChild(tableWrap);
    });

    input.addEventListener("keydown", (e) => { if (e.key === "Enter") btn.click(); });

    wrap.appendChild(form);
    wrap.appendChild(resultArea);
    return wrap;
  };
}

async function init() {
  try {
    const data = await loadGameData();
    app.innerHTML = "";

    if (!data.pokemon) {
      app.innerHTML = `<div class="error-state">${JSC.gameDataUnavailable}</div>`;
      return;
    }

    const tabs = buildTabs(
      LEAGUES.map((l) => ({
        id: l.id,
        label: l.name,
        render: buildLeaguePanel(data, l.cap),
      })),
      "great"
    );

    app.appendChild(tabs);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${JSC.failedLoad}</div>`;
    console.error(err);
  }
}

init();
