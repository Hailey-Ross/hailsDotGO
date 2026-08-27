// Generates the mobile app's domain/EvolutionTable.kt from ts/shared/evolutions.ts.
//
//   node scripts/gen-evolution-table-kt.mjs <path-to-EvolutionTable.kt>
//
// Why generate rather than transcribe. The evolution table is 444 entries and
// 463 target names, and one of those names -- Sirfetch'd -- carries a U+2019
// curly apostrophe. The shiny endpoints index species case-sensitively and
// validate nothing, so a name that is one character off is written verbatim and
// comes back as a dex-0 row with no sprite, permanently, unfixable from the app.
// Mr. Mime, Mr. Rime, Hakamo-o and Kommo-o are the same hazard in plain ASCII.
//
// Why bundle rather than parse the TypeScript. Two things defeat a regex:
// REGIONAL_EVOLUTION_NEXT's inner region keys are unquoted identifiers
// ({ galarian: [...] }), which a quoted-string scanner misses entirely; and
// REGIONAL_FORMS is not a literal at all -- Unown, Vivillon, Scatterbug and
// Spewpa rows are .map()-generated. Importing the real modules sidesteps both,
// and is the same trick scripts/check-scatterbug-carry.mjs already uses.
//
// Output encoding is deliberate: every non-ASCII character is emitted as a
// \uXXXX escape and the file is written UTF-8 with no BOM. PowerShell's
// Set-Content and Out-File default to ANSI or UTF-8-BOM on the machine this
// targets, which would corrupt exactly the names the whole exercise protects.

import { build } from "esbuild";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const outPath = process.argv[2];
if (!outPath) {
  console.error("usage: node scripts/gen-evolution-table-kt.mjs <out.kt>");
  process.exit(2);
}

const tmp = mkdtempSync(join(tmpdir(), "evo-kt-"));

/** Kotlin string literal body, ASCII-only. Escapes what Kotlin needs plus everything above 0x7e. */
function esc(s) {
  let out = "";
  for (const ch of s) {
    const c = ch.codePointAt(0);
    if (ch === "\\") out += "\\\\";
    else if (ch === '"') out += '\\"';
    else if (ch === "$") out += "\\$";
    else if (c >= 0x20 && c <= 0x7e) out += ch;
    else if (c <= 0xffff) out += "\\u" + c.toString(16).padStart(4, "0");
    else {
      // Astral plane: Kotlin escapes are UTF-16, so emit the surrogate pair.
      for (const unit of ch.split("")) {
        out += "\\u" + unit.charCodeAt(0).toString(16).padStart(4, "0");
      }
    }
  }
  return out;
}

try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  const p = (rel) => JSON.stringify(join(process.cwd(), rel));
  writeFileSync(
    entry,
    `export { EVOLUTION_NEXT, REGIONAL_EVOLUTION_NEXT } from ${p("ts/shared/evolutions.ts")};\n` +
      `export { REGIONAL_FORMS } from ${p("ts/shared/regionalForms.ts")};\n`,
  );

  await build({
    entryPoints: [entry],
    bundle: true,
    outfile: out,
    platform: "node",
    format: "esm",
    logLevel: "silent",
  });

  const mod = await import("file://" + out.replace(/\\/g, "/"));
  const { EVOLUTION_NEXT, REGIONAL_EVOLUTION_NEXT, REGIONAL_FORMS } = mod;

  // Sanity: refuse to emit a table that has obviously lost something. A
  // generator that silently produces a shorter file is worse than no generator.
  const evoKeys = Object.keys(EVOLUTION_NEXT);
  if (evoKeys.length < 400) throw new Error(`EVOLUTION_NEXT has only ${evoKeys.length} entries`);
  if (Object.keys(REGIONAL_EVOLUTION_NEXT).length !== 10) {
    throw new Error(`REGIONAL_EVOLUTION_NEXT has ${Object.keys(REGIONAL_EVOLUTION_NEXT).length} entries, expected 10`);
  }

  // Every name that can appear as an evolution TARGET. Region propagation asks
  // recordableRegions() about the target only, so this is the exact set of
  // species whose regional rows the app needs -- far short of all 175.
  const targets = new Set();
  for (const tos of Object.values(EVOLUTION_NEXT)) for (const t of tos) targets.add(t);
  for (const byRegion of Object.values(REGIONAL_EVOLUTION_NEXT)) {
    for (const tos of Object.values(byRegion)) for (const t of tos ?? []) targets.add(t);
  }

  const regionalNeeded = [...targets]
    .filter((n) => REGIONAL_FORMS[n]?.length)
    .sort();

  // Every target must resolve against the real species list. This is the check
  // the whole generate-don't-transcribe approach exists to make possible: an
  // unresolvable name is not a compile error and not a runtime error, it is a
  // row written to a trainer's collection with dex 0 and no sprite, forever.
  const species = new Set(
    JSON.parse(readFileSync(join(process.cwd(), "cache/pokemon.json"), "utf8"))
      .map((p) => p.pokemon_name),
  );
  const unresolved = [...targets].filter((n) => n && !species.has(n));
  if (unresolved.length) {
    throw new Error(
      `${unresolved.length} evolution target(s) do not resolve against cache/pokemon.json:\n  ` +
        unresolved.map((n) => `${n}  [${[...n].map((c) => c.codePointAt(0).toString(16)).join(" ")}]`)
          .join("\n  "),
    );
  }

  // ---- emit -------------------------------------------------------------
  const L = [];
  L.push("package live.hails.hailsdotgo.domain");
  L.push("");
  L.push("// GENERATED by scripts/gen-evolution-table-kt.mjs in the hailsDotGO site repo.");
  L.push("// Source of truth: ts/shared/evolutions.ts and ts/shared/regionalForms.ts.");
  L.push("// Do not edit by hand -- regenerate. Every non-ASCII name is a \\uXXXX escape");
  L.push("// on purpose: Sirfetch\\u2019d is the one evolution target with a curly");
  L.push("// apostrophe, and a straight one produces a permanent dex-0 row.");
  L.push("");
  L.push("/** Carry-only regional rows never get their own checklist card. */");
  L.push("data class RegionalFormRow(");
  L.push("    val region: String,");
  L.push("    val shiny: Boolean,");
  L.push("    val patternOnly: Boolean = false,");
  L.push(")");
  L.push("");
  L.push("internal object EvolutionTable {");
  L.push("");

  // EVOLUTION_NEXT as a packed string: one map initialiser of 444 pairs
  // compiles to ~22KB in a single method, against a 64KB JVM cap, and leaves no
  // headroom once the regional rows join it. One constant, parsed lazily, has
  // neither problem and allocates nothing until first use.
  L.push("    // species=target|target;species=...   Empty target list is meaningful:");
  L.push("    // Mr. Mime, Qwilfish and Linoone have one, and the site relies on it being");
  L.push("    // falsy so lookup falls through to the regional override.");
  L.push("    private const val EVOLUTIONS =");
  const evoPacked = evoKeys
    .map((k) => `${k}=${EVOLUTION_NEXT[k].join("|")}`)
    .join(";");
  emitChunked(L, evoPacked);
  L.push("");

  L.push("    // species/region=target|target;...");
  L.push("    private const val REGIONAL_EVOLUTIONS =");
  const regPacked = Object.entries(REGIONAL_EVOLUTION_NEXT)
    .flatMap(([species, byRegion]) =>
      Object.entries(byRegion).map(([region, tos]) => `${species}/${region}=${(tos ?? []).join("|")}`),
    )
    .join(";");
  emitChunked(L, regPacked);
  L.push("");

  L.push("    // species=region:shiny:patternOnly,...   Only species that can be an");
  L.push("    // evolution TARGET are here, because that is the only question the");
  L.push("    // propagation rule asks. This is NOT the whole regional table.");
  L.push("    private const val REGIONAL_FORM_ROWS =");
  const formsPacked = regionalNeeded
    .map(
      (n) =>
        `${n}=` +
        REGIONAL_FORMS[n]
          .map((f) => `${f.region}:${f.shiny ? 1 : 0}:${f.patternOnly ? 1 : 0}`)
          .join(","),
    )
    .join(";");
  emitChunked(L, formsPacked);
  L.push("");

  L.push("    val evolutionNext: Map<String, List<String>> by lazy { parseTargets(EVOLUTIONS) }");
  L.push("");
  L.push("    val regionalEvolutionNext: Map<String, List<String>> by lazy {");
  L.push("        parseTargets(REGIONAL_EVOLUTIONS)");
  L.push("    }");
  L.push("");
  L.push("    val regionalForms: Map<String, List<RegionalFormRow>> by lazy {");
  L.push("        REGIONAL_FORM_ROWS.split(';').filter { it.isNotEmpty() }.associate { row ->");
  L.push("            val name = row.substringBefore('=')");
  L.push("            name to row.substringAfter('=').split(',').filter { it.isNotEmpty() }.map { f ->");
  L.push("                val parts = f.split(':')");
  L.push("                RegionalFormRow(parts[0], parts[1] == \"1\", parts[2] == \"1\")");
  L.push("            }");
  L.push("        }");
  L.push("    }");
  L.push("");
  L.push("    /** \"key=a|b;key2=\" -> map. A trailing '=' is an entry with no targets, not a dropped one. */");
  L.push("    private fun parseTargets(packed: String): Map<String, List<String>> =");
  L.push("        packed.split(';').filter { it.isNotEmpty() }.associate { row ->");
  L.push("            val key = row.substringBefore('=')");
  L.push("            val rest = row.substringAfter('=')");
  L.push("            key to if (rest.isEmpty()) emptyList() else rest.split('|')");
  L.push("        }");
  L.push("}");
  L.push("");

  writeFileSync(outPath, L.join("\n"), { encoding: "utf8" });

  const emptyCount = evoKeys.filter((k) => EVOLUTION_NEXT[k].length === 0).length;
  console.log(
    `wrote ${outPath}\n` +
      `  EVOLUTION_NEXT        ${evoKeys.length} entries (${emptyCount} deliberately empty)\n` +
      `  REGIONAL_EVOLUTIONS   ${Object.keys(REGIONAL_EVOLUTION_NEXT).length} species\n` +
      `  REGIONAL_FORM_ROWS    ${regionalNeeded.length} species that can be an evolution target\n` +
      `  distinct targets      ${targets.size}`,
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}

/** Emits a long string as concatenated Kotlin literals so no source line is absurd. */
function emitChunked(lines, s) {
  const CHUNK = 110;
  const parts = [];
  for (let i = 0; i < s.length; i += CHUNK) parts.push(s.slice(i, i + CHUNK));
  parts.forEach((part, i) => {
    const prefix = i === 0 ? "        " : "            ";
    const suffix = i === parts.length - 1 ? "" : " +";
    lines.push(`${prefix}"${esc(part)}"${suffix}`);
  });
}
