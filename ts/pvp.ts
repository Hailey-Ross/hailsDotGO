import { loadGameData, pokemonByName, pokeSprite } from "./shared/gamedata";
import { buildTabs } from "./shared/tabs";
import { fetchSpeciesData } from "./shared/pokedex";
import type { GameData, PokemonStat, CPMultiplier } from "./shared/types";

const app = document.getElementById("pvp-app")!;

const LEAGUES = [
  { id: "great",  name: "Great League",  cap: 1500 },
  { id: "ultra",  name: "Ultra League",  cap: 2500 },
  { id: "master", name: "Master League", cap: Infinity },
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
    input.placeholder = "Pokémon name (e.g. Azumarill)";

    const btn = document.createElement("button");
    btn.className = "btn-primary";
    btn.textContent = "Show IV Rankings";

    form.appendChild(input);
    form.appendChild(btn);

    const resultArea = document.createElement("div");
    resultArea.className = "result-area";

    btn.addEventListener("click", () => {
      const poke = pokemonByName(data, input.value.trim());
      if (!poke) {
        resultArea.innerHTML = `<p class="error-text">Pokémon "${input.value.trim()}" not found.</p>`;
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
      h3.textContent = `${poke.pokemon_name.replace(/_/g, " ")}: ${league.name}`;
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
        if (d.genus)  { genusEl.textContent = `The ${d.genus}`; }
        if (d.isLegendary || d.isMythical) {
          badgeEl.textContent  = d.isMythical ? "Mythical" : "Legendary";
          badgeEl.className    = `poke-legend-badge ${d.isMythical ? "poke-badge-mythical" : "poke-badge-legendary"}`;
          badgeEl.style.display = "";
        }
        if (d.flavor) { flavorP.textContent = d.flavor; flavorP.style.display = ""; }
      });

      const note = document.createElement("p");
      note.className = "league-note";
      note.textContent = `Ranked by stat product. Showing top 25 of ${ranks.length.toLocaleString()} IV combinations.`;
      resultArea.appendChild(note);

      const tableWrap = document.createElement("div");
      tableWrap.className = "table-wrap fade-up";

      const table = document.createElement("table");
      table.className = "iv-table";
      table.innerHTML = `<thead><tr>
        <th>Rank</th><th>Atk IV</th><th>Def IV</th><th>Sta IV</th>
        <th>Level</th><th>CP</th><th>Stat Product</th>
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

    // Enter key triggers calc
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
      app.innerHTML = `<div class="error-state">Game data unavailable. Please try again later.</div>`;
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
    app.innerHTML = `<div class="error-state">Failed to load data. Please try again later.</div>`;
    console.error(err);
  }
}

init();
