import { typeEffectiveness, pokemonByName, pokeSprite } from "./gamedata";
import { trueDPS, estimateTDO } from "./damage";
import { typeBadge } from "./typecolors";
import type { GameData, RaidBoss } from "./types";

export interface CounterResult {
  name: string;
  pokemonId: number;
  fastMove: string;
  fastType: string;
  chargedMove: string;
  chargedType: string;
  dps: number;
  tdo: number;
}

export function calcCounters(data: GameData, boss: RaidBoss): CounterResult[] {
  const bossStats = pokemonByName(data, boss.pokemon_name);
  const defDef = bossStats ? (bossStats.base_defense + 15) * 0.7903 : 200;
  const bossTypes = boss.types ?? [];
  const cpMult = data.cpMultipliers?.find((c) => c.level === 40)?.multiplier ?? 0.7903;

  const fastByName = new Map(
    (data.fastMoves ?? []).map((m) => [m.name.toLowerCase(), m])
  );
  const chargedByName = new Map(
    (data.chargedMoves ?? []).map((m) => [m.name.toLowerCase(), m])
  );

  const movesByPoke = new Map<string, { fast: string[]; charged: string[] }>();
  for (const pm of data.pokemonMoves ?? []) {
    movesByPoke.set(pm.pokemon_name.toLowerCase(), {
      fast:    [...pm.fast_moves,    ...pm.elite_fast_moves   ].map((n) => n.toLowerCase()),
      charged: [...pm.charged_moves, ...pm.elite_charged_moves].map((n) => n.toLowerCase()),
    });
  }

  const raw: CounterResult[] = [];

  for (const poke of data.pokemon ?? []) {
    const moves = movesByPoke.get(poke.pokemon_name.toLowerCase());
    if (!moves || !moves.fast.length || !moves.charged.length) continue;

    const atkStat = (poke.base_attack + 15) * cpMult;
    let best: CounterResult | null = null;

    for (const fn of moves.fast) {
      const fast = fastByName.get(fn);
      if (!fast) continue;
      const fastEff = bossTypes.length > 0 ? typeEffectiveness(data, fast.type, bossTypes) : 1;

      for (const cn of moves.charged) {
        const charged = chargedByName.get(cn);
        if (!charged) continue;
        const chargedEff = bossTypes.length > 0 ? typeEffectiveness(data, charged.type, bossTypes) : 1;

        const dps = trueDPS(fast, charged, atkStat, defDef, fastEff, chargedEff);
        if (best && dps <= best.dps) continue;

        const tdo = estimateTDO(poke, fast, charged, atkStat, defDef, fastEff, chargedEff, cpMult);
        best = {
          name: poke.pokemon_name,
          pokemonId: poke.pokemon_id,
          fastMove: fast.name,
          fastType: fast.type,
          chargedMove: charged.name,
          chargedType: charged.type,
          dps,
          tdo,
        };
      }
    }

    if (best) raw.push(best);
  }

  const bestMap = new Map<string, CounterResult>();
  for (const r of raw) {
    const prev = bestMap.get(r.name);
    if (!prev || r.dps > prev.dps) bestMap.set(r.name, r);
  }

  return [...bestMap.values()].sort((a, b) => b.dps - a.dps).slice(0, 20);
}

export function renderCounterTable(boss: RaidBoss, results: CounterResult[]): HTMLElement {
  const section = document.createElement("div");
  section.className = "counter-results";

  const header = document.createElement("h3");
  header.textContent = `Top counters for ${boss.pokemon_name.replace(/_/g, " ")} (CP ${boss.cp.toLocaleString()})`;
  section.appendChild(header);

  if (results.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty-state";
    empty.textContent = "No counter data available.";
    section.appendChild(empty);
    return section;
  }

  const wrap = document.createElement("div");
  wrap.className = "table-wrap";

  const table = document.createElement("table");
  table.className = "counter-table";
  table.innerHTML = `
    <thead>
      <tr>
        <th>#</th>
        <th>Pokémon</th>
        <th>Fast Move</th>
        <th>Charged Move</th>
        <th>DPS</th>
        <th>TDO</th>
      </tr>
    </thead>
  `;

  const tbody = document.createElement("tbody");
  results.forEach((r, i) => {
    const tr = document.createElement("tr");

    const numTd = document.createElement("td");
    numTd.textContent = String(i + 1);

    const nameTd = document.createElement("td");
    nameTd.style.whiteSpace = "nowrap";
    const spriteImg = document.createElement("img");
    spriteImg.src = pokeSprite(r.pokemonId);
    spriteImg.alt = r.name;
    spriteImg.className = "poke-sprite";
    spriteImg.loading = "lazy"; spriteImg.decoding = "async";
    nameTd.appendChild(spriteImg);
    nameTd.append(` ${r.name.replace(/_/g, " ")}`);

    const fastTd = document.createElement("td");
    fastTd.appendChild(typeBadge(r.fastType));
    fastTd.append(` ${r.fastMove}`);

    const chargedTd = document.createElement("td");
    chargedTd.appendChild(typeBadge(r.chargedType));
    chargedTd.append(` ${r.chargedMove}`);

    const dpsTd = document.createElement("td");
    dpsTd.textContent = r.dps.toFixed(2);
    const tdoTd = document.createElement("td");
    tdoTd.textContent = r.tdo.toFixed(0);

    tr.append(numTd, nameTd, fastTd, chargedTd, dpsTd, tdoTd);
    tbody.appendChild(tr);
  });

  table.appendChild(tbody);
  wrap.appendChild(table);
  section.appendChild(wrap);
  return section;
}
