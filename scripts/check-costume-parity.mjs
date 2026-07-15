// Asserts the TypeScript and Go costume resolvers agree, sprite URL for sprite URL.
//
// They share catalog.json and labels.json, but the resolution rules are still written twice
// (once in ts/shared/costumes.ts, once in internal/costumes/costumes.go). This is what stops
// someone editing one and not the other: the picker in the browser and the sprite on a public
// profile must never disagree about what a costume looks like.
//
//   node scripts/check-costume-parity.mjs
//
// It shells out to `go run ./cmd/costumeparity`, which dumps the Go resolver's answers, and
// compares them against the TS resolver bundled through esbuild.

import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const labels = JSON.parse(readFileSync("internal/costumes/labels.json", "utf8"));
const catalog = JSON.parse(readFileSync("internal/costumes/catalog.json", "utf8"));

// Every (species, dex, label) worth checking: each curated label against every species the
// catalog says can wear it, plus a few misses that must stay misses.
const cases = [];
for (const [species, byLabel] of Object.entries(labels.species)) {
  for (const [label, code] of Object.entries(byLabel)) {
    for (const dex of catalog.codes[code]?.dex ?? []) cases.push({ dex, species, label });
  }
}
for (const { label, code } of labels.shared) {
  for (const dex of catalog.codes[code]?.dex ?? []) cases.push({ dex, species: "", label });
}
for (const [alias] of Object.entries(labels.aliases)) {
  cases.push({ dex: 6, species: "Charizard", label: alias });
}
cases.push({ dex: 25, species: "Pikachu", label: "Visor" });          // must miss
cases.push({ dex: 6, species: "Charizard", label: "Not A Costume" }); // must miss

// The shared cases have no species; the Go side needs the same blank, so leave it blank.
const tmp = mkdtempSync(join(tmpdir(), "costume-parity-"));
try {
  const entry = join(tmp, "entry.ts");
  const out = join(tmp, "bundle.js");
  writeFileSync(
    entry,
    `import { costumeShinyUrl } from ${JSON.stringify(join(process.cwd(), "ts/shared/costumes.ts"))};
     const cases = ${JSON.stringify(cases)};
     console.log(JSON.stringify(cases.map((c) => costumeShinyUrl(c.dex, c.species, c.label) ?? "")));`
  );
  await build({ entryPoints: [entry], bundle: true, outfile: out, platform: "node", format: "esm", logLevel: "silent" });

  const tsOut = JSON.parse(execFileSync("node", [out], { encoding: "utf8" }));
  const goOut = JSON.parse(
    execFileSync("go", ["run", "./cmd/costumeparity"], { encoding: "utf8", input: JSON.stringify(cases) })
  );

  let bad = 0;
  for (let i = 0; i < cases.length; i++) {
    if (tsOut[i] !== goOut[i]) {
      const c = cases[i];
      console.error(`MISMATCH ${c.species || "(shared)"} dex=${c.dex} ${JSON.stringify(c.label)}`);
      console.error(`  ts: ${tsOut[i] || "(no match)"}`);
      console.error(`  go: ${goOut[i] || "(no match)"}`);
      bad++;
    }
  }
  if (bad) {
    console.error(`\n${bad} of ${cases.length} cases disagree`);
    process.exit(1);
  }
  console.log(`costume resolvers agree on all ${cases.length} cases`);
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
