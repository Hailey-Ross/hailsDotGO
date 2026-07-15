package main

// Turning Dittobase names into two things: a suggested label for costumes nobody has named
// yet, and a check that an existing label still describes the costume it points at.

import (
	"fmt"
	"sort"
	"strings"
)

// suggestedLabel is the name to propose for an unlabelled costume: the one Dittobase gives it
// most often across the species that can wear it. Taking the mode rather than the first entry
// keeps a species-prefixed outlier ("Noibat Headband") from beating the common name ("Headband").
func suggestedLabel(e *catalogEntry, nm names, code string) string {
	counts := map[string]int{}
	for _, dex := range e.Dex {
		if n := nm.get(code, dex); n != "" {
			counts[n]++
		}
	}
	best, bestN := "", 0
	for name, n := range counts {
		if n > bestN || (n == bestN && name < best) {
			best, bestN = name, n
		}
	}
	return best
}

// nameCheck flags a curated label that shares no word with what Dittobase calls the same
// costume. This is the guard that would have caught the visor bug: "Pikachu Visor" was bound to
// f:GOFEST_2026, which is really Caterpie's Poke Ball Hat, and those two names have nothing in
// common. The code resolved, the art existed, and it was silently the wrong costume.
//
// Advisory, never fatal: Dittobase has its own editorial naming and we are often the better of
// the two (they call Dawn's Hat "Rei's Cap"). Known-good divergences live in nameCheckIgnore, so
// the baseline is silent and a NEW mismatch stands out instead of hiding in the noise.
func nameCheck(cat *catalog, lab *labels, nameToDex map[string]int, nm names) []finding {
	ignore := map[string]bool{}
	for _, c := range lab.NameCheckIgnore {
		ignore[c] = true
	}

	var out []finding
	seen := map[string]bool{}

	check := func(where, label, code string, dex int) {
		if ignore[code] || seen[where] {
			return
		}
		ditto := nm.get(code, dex)
		if ditto == "" || sharesWord(label, ditto) {
			return
		}
		seen[where] = true
		out = append(out, finding{
			where,
			fmt.Sprintf("dittobase calls this %q (shares no words: is the code right?)", ditto),
		})
	}

	for species, byLabel := range lab.Species {
		dex, ok := nameToDex[species]
		if !ok {
			continue
		}
		for label, code := range byLabel {
			check(fmt.Sprintf("%s %q -> %s", species, label, code), label, code, dex)
		}
	}
	for _, s := range lab.Shared {
		e, ok := cat.Codes[s.Code]
		if !ok || ignore[s.Code] {
			continue
		}
		// A shared label describes one costume across many species, but Dittobase names it per
		// species and is not always consistent about it (the same Fall 2022 costume is "Spooky
		// Festival" on Gengar and "Cempasúchil Crown" on Duskull). So agreeing with ANY of its
		// names is agreement: only flag a label that matches none of them.
		var names []string
		for _, dex := range e.Dex {
			if n := nm.get(s.Code, dex); n != "" {
				names = append(names, n)
			}
		}
		if len(names) == 0 || anySharesWord(s.Label, names) {
			continue
		}
		out = append(out, finding{
			fmt.Sprintf("shared %q -> %s", s.Label, s.Code),
			fmt.Sprintf("dittobase calls this %q (shares no words: is the code right?)", names[0]),
		})
	}

	sortFindings(out)
	return out
}

func anySharesWord(label string, names []string) bool {
	for _, n := range names {
		if sharesWord(label, n) {
			return true
		}
	}
	return false
}

// sharesWord reports whether two names have any significant word in common, comparing on
// 5-character prefixes so ordinary morphology does not trip the check: "Fashion" matches
// "Fashionable", "Holiday" matches "Holidays".
func sharesWord(a, b string) bool {
	as, bs := significant(a), significant(b)
	if len(as) == 0 || len(bs) == 0 {
		return true // nothing to compare; do not cry wolf
	}
	for _, x := range as {
		for _, y := range bs {
			if strings.HasPrefix(x, y) || strings.HasPrefix(y, x) {
				return true
			}
		}
	}
	return false
}

// stopWords are too common to carry meaning: "Hat" would make "Straw Hat" and "Detective Hat"
// look like the same costume, which is exactly the confusion this check exists to catch.
var stopWords = map[string]bool{
	"the": true, "a": true, "of": true, "and": true, "s": true,
	"costume": true, "outfit": true, "hat": true, "pokemon": true,
}

func significant(s string) []string {
	var out []string
	for _, w := range strings.Fields(slugify(s)) {
		for _, part := range strings.Split(w, "-") {
			if len(part) < 3 || stopWords[part] {
				continue
			}
			if len(part) > 5 {
				part = part[:5]
			}
			out = append(out, part)
		}
	}
	return out
}

// suggestLabels prints a paste-ready labels.json fragment for every costume nobody has named.
//
// The tool deliberately does NOT write labels.json itself. Appending would be safe for the
// database (nothing references a label that does not exist yet), but some costumes need human
// judgement before they go in the picker, and marshalling that file from Go would reorder its
// keys and drop its comments.
func suggestLabels(cat *catalog, lab *labels, nm names, dexToName map[int]string) {
	labelled := lab.codes()
	hidden := map[string]bool{}
	for _, h := range lab.Hidden {
		hidden[h] = true
	}

	type suggestion struct{ code, label, species string }
	var shared, species []suggestion

	for code, e := range cat.Codes {
		if labelled[code] || hidden[code] {
			continue
		}
		name := suggestedLabel(e, nm, code)
		if name == "" {
			continue // no Dittobase page; name it yourself
		}
		// A costume several species wear, under a name that is not species-specific, is a shared
		// entry. One that only one species has is an override.
		if len(e.Dex) > 1 {
			shared = append(shared, suggestion{code: code, label: name})
		} else {
			species = append(species, suggestion{code: code, label: name, species: dexToName[e.Dex[0]]})
		}
	}
	if len(shared)+len(species) == 0 {
		return
	}

	sort.Slice(shared, func(i, j int) bool { return shared[i].label < shared[j].label })

	// Group by species: one JSON key each, or the fragment is not valid JSON to paste.
	bySpecies := map[string][]suggestion{}
	for _, s := range species {
		if s.species != "" {
			bySpecies[s.species] = append(bySpecies[s.species], s)
		}
	}
	speciesNames := make([]string, 0, len(bySpecies))
	for sp := range bySpecies {
		speciesNames = append(speciesNames, sp)
	}
	sort.Strings(speciesNames)

	fmt.Printf("Paste-ready for %s (review each: not every costume belongs in the picker)\n\n", labelsPath)
	if len(speciesNames) > 0 {
		fmt.Println("  \"species\": {")
		for _, sp := range speciesNames {
			entries := bySpecies[sp]
			sort.Slice(entries, func(i, j int) bool { return entries[i].label < entries[j].label })
			parts := make([]string, 0, len(entries))
			for _, e := range entries {
				parts = append(parts, fmt.Sprintf("%q: %q", e.label, e.code))
			}
			fmt.Printf("    %q: { %s },\n", sp, strings.Join(parts, ", "))
		}
		fmt.Println("  },")
	}
	if len(shared) > 0 {
		fmt.Println("  \"shared\": [")
		for _, s := range shared {
			fmt.Printf("    { \"label\": %q, \"code\": %q },\n", s.label, s.code)
		}
		fmt.Println("  ]")
	}
	fmt.Println()
}
