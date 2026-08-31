import { typeEffectiveness, pokemonByName, pokeSprite, pokeName } from "./gamedata";
import { trueDPS, estimateTDO } from "./damage";
import { typeBadge } from "./typecolors";
import type { GameData, RaidBoss } from "./types";

declare const JSC: Record<string, string>;

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

export type PokemonForm = "normal" | "shadow" | "purified" | "primal";

export interface PokemonConfig {
  cpm: number;
  atkIV: number;
  defIV: number;
  staIV: number;
  form: PokemonForm;
}

export const DEFAULT_CONFIG: PokemonConfig = {
  cpm: 0.7903,
  atkIV: 15,
  defIV: 15,
  staIV: 15,
  form: "normal",
};

// Shadow/Mega/Primal forms share their move pool with the base form.
// Strip known modifier prefixes so we can fall back to base-form moves.
function baseMovesetName(name: string): string {
  return name
    .replace(/^Shadow_/i, "")
    .replace(/^(Mega|Primal)_/i, "")
    .replace(/_(X|Y)$/i, "");
}

// Raid bosses are named with a space separated form prefix, because that is how
// they are shown: "Shadow Slowpoke", "Alolan Sandshrew", "Mega Blaziken",
// "Shadow Giratina (Altered Forme)". The Pokemon list they are looked up in
// carries base species only, so those names find nothing.
//
// This is not cosmetic. Every miss used to fall through to a hardcoded default
// defence, and 14 of the 19 bosses in a typical rotation miss, so most of the
// damage numbers on the page were computed against a stat nobody has.
function baseSpeciesName(name: string): string {
  let out = name;
  // Repeated, because decorations stack: "Shadow Alolan Marowak" carries two.
  for (let i = 0; i < 3; i++) {
    const next = out.replace(/^(Shadow|Mega|Primal|Alolan|Galarian|Hisuian|Paldean)\s+/i, "");
    if (next === out) break;
    out = next;
  }
  return out
    .replace(/\s*\([^)]*\)\s*$/, "")
    // "Mega Charizard X" leaves "Charizard X", which is no species at all and
    // fell through to the flat fallback.
    .replace(/\s+[XY]$/, "")
    .trim();
}

// bossFormHint recovers which FORM a boss name is describing.
//
// Stripping the decoration and looking the species up by name alone is not
// enough, because 60 species carry several stat lines under one name and the
// lookup returns whichever sorts first. That is how "Deoxys (Defense Forme)"
// resolved to Deoxys Attack: defence 46 against a real 330, which is further
// from the truth than the flat fallback it replaced.
function bossFormHint(name: string): string | null {
  const prefix = name.match(/^(Alolan|Galarian|Hisuian|Paldean)\s+/i);
  if (prefix) {
    const p = prefix[1].toLowerCase();
    if (p === "alolan") return "alola";
    if (p === "paldean") return "paldea";
    return p; // galarian, hisuian
  }
  // "Deoxys (Defense Forme)" -> "defense", "Giratina (Origin Forme)" -> "origin"
  const paren = name.match(/\(([^)]*)\)\s*$/);
  if (paren) {
    return paren[1].replace(/\s*forme?\s*$/i, "").trim().toLowerCase() || null;
  }
  // Shadow and Mega carry no form of their own: a shadow is the ordinary form.
  if (/^Shadow\s+/i.test(name)) return "normal";
  return null;
}

// bossDefense is the defender stat every damage calculation divides by.
//
// It falls back to the base species when the exact form is not in the list, which
// is exact for Shadow (identical base stats) and an approximation for regional
// forms. Megas are no longer in that bucket: they carry their own stat line now,
// so a Mega divides by its real defense instead of its base species'. The literal
// remains only for a boss that resolves to nothing at all.
// Exported so the parity script can assert against the shipped implementation
// rather than a copy of it, which would pass while this was broken.
export function bossDefense(data: GameData, boss: RaidBoss): number {
  const stats = bossStats(data, boss);
  return stats ? (stats.base_defense + 15) * 0.7903 : 200;
}

// bossStats resolves a boss to its actual stat line, form included. Exported so
// the parity script can assert the FORM is right rather than merely that some
// number came back, which is the check that let the Deoxys case through.
export function bossStats(data: GameData, boss: RaidBoss) {
  // No exact-name short circuit. pokemonByName takes the first record with a
  // matching name and has no form preference, so an undecorated boss name (which
  // is how the ORDINARY form is always written) picked up whichever form sorted
  // first: plain Mewtwo resolved to Armored, Aegislash to Blade, Eternatus to
  // Eternamax. 80 of the 152 multi form species were wrong that way, on the main
  // counters table, and the earlier version of this function only fixed the
  // decorated names.
  // Megas first, from their own dataset. Nothing in the Pokemon list describes one,
  // and the base species is the wrong answer rather than a near one: Mega Gyarados
  // defends at 247 where Gyarados defends at 247's two thirds, which put every
  // absolute DPS and TDO figure against a Mega boss about 30 percent high.
  const mega = data.megas?.[boss.pokemon_name.trim().toLowerCase()];
  if (mega) {
    return {
      pokemon_id: 0,
      pokemon_name: mega.name,
      form: "Mega",
      base_attack: mega.atk,
      base_defense: mega.def,
      base_stamina: mega.sta,
    };
  }

  const base = baseSpeciesName(boss.pokemon_name).toLowerCase();
  const hint = bossFormHint(boss.pokemon_name);
  const candidates = (data.pokemon ?? []).filter((p) => p.pokemon_name.toLowerCase() === base);
  if (!candidates.length) return undefined;

  if (hint) {
    const match = candidates.find((p) => (p.form ?? "").toLowerCase() === hint);
    if (match) return match;
  }
  // No hint, or a form this dataset does not carry. Prefer the ordinary form over
  // whatever happens to sort first. Megas used to land here, which is what made
  // them read as their base species; they are answered above now, and only reach
  // this line when the Mega dataset has not loaded.
  return candidates.find((p) => (p.form ?? "").toLowerCase() === "normal") ?? candidates[0];
}

export function calcCounters(data: GameData, boss: RaidBoss): CounterResult[] {
  const defDef = bossDefense(data, boss);
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
    const key = poke.pokemon_name.toLowerCase();
    const moves = movesByPoke.get(key)
      ?? movesByPoke.get(baseMovesetName(poke.pokemon_name).toLowerCase());
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

// MovesetRange is the same Pokemon at both ends of its move pool against one
// boss. The box records which Pokemon a trainer owns but not which moves it
// knows, so a single number would quietly present the best case as fact. The
// spread is the honest answer: worst is what it does on its weakest pairing.
//
// best and worst are the same object when only one usable pairing exists.
export interface MovesetRange {
  best: CounterResult;
  worst: CounterResult;
}

export function calcSinglePokemon(
  data: GameData,
  pokemonName: string,
  boss: RaidBoss,
  config: PokemonConfig = DEFAULT_CONFIG
): CounterResult | null {
  return calcMovesetRange(data, pokemonName, boss, config)?.best ?? null;
}

// pickForm chooses among several records sharing one pokemon_name.
//
// 60 species carry more than one, and a plain name lookup returns whichever
// sorts first, which is usually not the ordinary one. A Kantonian Raichu was
// being given Alolan stats AND the Alolan move pool, so the page recommended it
// bring Psychic, a move it cannot learn.
function pickForm<T extends { pokemon_name: string; form?: string }>(
  list: T[],
  name: string,
  gameForm?: string
): T | undefined {
  const lower = name.toLowerCase();
  const matches = list.filter((x) => x.pokemon_name.toLowerCase() === lower);
  if (!matches.length) return undefined;
  if (gameForm) {
    const want = gameForm.toLowerCase();
    const exact = matches.find((x) => (x.form ?? "").toLowerCase() === want);
    if (exact) return exact;
  }
  return matches.find((x) => (x.form ?? "").toLowerCase() === "normal") ?? matches[0];
}

// moveTypesFor is every damage type a species can actually bring.
//
// It exists for the shortlist pre-rank, which used to stand in the species' OWN
// types for this. That reads Alakazam as Psychic and never notices it carries
// Shadow Ball, Dazzling Gleam and Fire Punch, so coverage attackers were being
// dropped before they were ever scored.
export function moveTypesFor(data: GameData, pokemonName: string, gameForm?: string): string[] {
  const entry =
    pickForm(data.pokemonMoves ?? [], pokemonName, gameForm) ??
    pickForm(data.pokemonMoves ?? [], baseMovesetName(pokemonName), gameForm);
  if (!entry) return [];

  const fastTypes = new Map((data.fastMoves ?? []).map((m) => [m.name.toLowerCase(), m.type]));
  const chargedTypes = new Map((data.chargedMoves ?? []).map((m) => [m.name.toLowerCase(), m.type]));
  const out = new Set<string>();
  for (const n of [...entry.fast_moves, ...entry.elite_fast_moves]) {
    const t = fastTypes.get(n.toLowerCase());
    if (t) out.add(t);
  }
  for (const n of [...entry.charged_moves, ...entry.elite_charged_moves]) {
    const t = chargedTypes.get(n.toLowerCase());
    if (t) out.add(t);
  }
  return [...out];
}

export function calcMovesetRange(
  data: GameData,
  pokemonName: string,
  boss: RaidBoss,
  config: PokemonConfig = DEFAULT_CONFIG,
  // The game's own form (Alola, Galarian, Therian...). Distinct from
  // config.form, which is the shadow and purified axis.
  gameForm?: string
): MovesetRange | null {
  const defDef = bossDefense(data, boss);
  const bossTypes = boss.types ?? [];

  const cpMult = config.cpm;

  const fastByName = new Map(
    (data.fastMoves ?? []).map((m) => [m.name.toLowerCase(), m])
  );
  const chargedByName = new Map(
    (data.chargedMoves ?? []).map((m) => [m.name.toLowerCase(), m])
  );

  // Primal form: use Primal_<name> base stats if that variant exists.
  const primalName = `Primal_${pokemonName}`;
  const pokeStats = (config.form === "primal" && !pokemonName.toLowerCase().startsWith("primal_"))
    ? (pokemonByName(data, primalName) ?? pokemonByName(data, pokemonName))
    : pickForm(data.pokemon ?? [], pokemonName, gameForm);
  const poke = pokeStats;
  if (!poke) return null;

  // Move pool from the same form as the stats, falling back to the base name for
  // Shadow, Mega and Primal, which share their base form's pool.
  const baseName = baseMovesetName(pokemonName);
  const movesEntry =
    pickForm(data.pokemonMoves ?? [], pokemonName, gameForm) ??
    pickForm(data.pokemonMoves ?? [], baseName, gameForm);
  if (!movesEntry) return null;

  const fastNames = [...movesEntry.fast_moves, ...movesEntry.elite_fast_moves].map((n) => n.toLowerCase());
  const chargedNames = [...movesEntry.charged_moves, ...movesEntry.elite_charged_moves].map((n) => n.toLowerCase());
  if (!fastNames.length || !chargedNames.length) return null;

  // Shadow: +20% ATK, DEF ×5/6 (takes 20% more damage → survives less long).
  // Purified/Primal/Normal: no multipliers beyond base stats.
  const atkMult = config.form === "shadow" ? 1.2 : 1;
  const defMult = config.form === "shadow" ? 5 / 6 : 1;
  const atkStat = (poke.base_attack + config.atkIV) * cpMult * atkMult;

  let best: CounterResult | null = null;
  let worst: CounterResult | null = null;

  for (const fn of fastNames) {
    const fast = fastByName.get(fn);
    if (!fast) continue;
    const fastEff = bossTypes.length > 0 ? typeEffectiveness(data, fast.type, bossTypes) : 1;

    for (const cn of chargedNames) {
      const charged = chargedByName.get(cn);
      if (!charged) continue;
      const chargedEff = bossTypes.length > 0 ? typeEffectiveness(data, charged.type, bossTypes) : 1;

      const dps = trueDPS(fast, charged, atkStat, defDef, fastEff, chargedEff);
      const better = !best || dps > best.dps;
      const poorer = !worst || dps < worst.dps;
      // TDO is the expensive half, so only pay for it on a pairing that is
      // actually going to be kept at one end or the other.
      if (!better && !poorer) continue;

      const tdo = estimateTDO(poke, fast, charged, atkStat, defDef, fastEff, chargedEff, cpMult, config.staIV, defMult);
      const entry: CounterResult = {
        name: poke.pokemon_name,
        pokemonId: poke.pokemon_id,
        fastMove: fast.name,
        fastType: fast.type,
        chargedMove: charged.name,
        chargedType: charged.type,
        dps,
        tdo,
      };
      if (better) best = entry;
      if (poorer) worst = entry;
    }
  }

  if (!best || !worst) return null;
  return { best, worst };
}

export function renderCounterTable(data: GameData, boss: RaidBoss, results: CounterResult[]): HTMLElement {
  const section = document.createElement("div");
  section.className = "counter-results";

  const header = document.createElement("h3");
  header.textContent = JSC.topCounters
    .replace("{name}", pokeName(data, boss.pokemon_name))
    .replace("{cp}", boss.cp.toLocaleString());
  section.appendChild(header);

  if (results.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty-state";
    empty.textContent = JSC.noCounterData;
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
        <th>${JSC.pokemon}</th>
        <th>${JSC.fastMove}</th>
        <th>${JSC.chargedMove}</th>
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
    nameTd.append(` ${pokeName(data, r.name)}`);

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
