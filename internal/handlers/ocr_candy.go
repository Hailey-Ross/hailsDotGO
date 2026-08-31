package handlers

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ---- Candy-line species detection ----
//
// A nicknamed Pokemon defeats every name heuristic ("John Cena" the Machamp),
// but the status screen still shows the candy family: "3 MACHOP CANDY",
// "26 MACHOP CANDY XL". Candy is always named after the family BASE species,
// so the line pins the scan to that species' evolution family; CP/HP/level
// then disambiguate which member it is.

// The space before CANDY is optional: RapidOCR frequently glues the tokens
// ("MACHOPCANDY", "SWABLUCANDYXL"), and CANDY XL may lose its own space too.
var reCandyLine = regexp.MustCompile(`(?i)([A-Za-z0-9 .':\-]{2,28}?)\s*CANDY(?:\s*XL)?\b`)

// detectCandyBase finds the candy family name in the OCR text, validating the
// captured text against known species names. OCR often glues the count onto
// the line, so leading tokens are dropped one at a time ("3 MACHOP" ->
// "MACHOP"). Multi-word names (MR. MIME, TAPU KOKO) survive because trimming
// only removes whole leading tokens.
func detectCandyBase(fullText string, isKnownSpecies func(string) bool) string {
	for _, m := range reCandyLine.FindAllStringSubmatch(fullText, -1) {
		tokens := strings.Fields(strings.TrimSpace(m[1]))
		for i := 0; i < len(tokens); i++ {
			name := strings.Join(tokens[i:], " ")
			if len(name) >= 3 && isKnownSpecies(name) {
				return name
			}
		}
	}
	return ""
}

type evolutionEntry struct {
	PokemonName string `json:"pokemon_name"`
	Form        string `json:"form"`
	Evolutions  []struct {
		PokemonName string `json:"pokemon_name"`
	} `json:"evolutions"`
}

// familySpecies returns the base species plus every transitive evolution
// (Normal forms), e.g. Machop -> [Machop, Machoke, Machamp]. Falls back to
// just the base if the evolution data is unavailable.
func familySpecies(base string, evoRaw json.RawMessage) []string {
	out := []string{base}
	var entries []evolutionEntry
	if err := json.Unmarshal(evoRaw, &entries); err != nil || len(entries) == 0 {
		return out
	}
	children := make(map[string][]string, len(entries))
	for _, e := range entries {
		if e.Form != "" && !strings.EqualFold(e.Form, "Normal") {
			continue
		}
		key := strings.ToLower(e.PokemonName)
		for _, ev := range e.Evolutions {
			children[key] = append(children[key], ev.PokemonName)
		}
	}
	seen := map[string]bool{strings.ToLower(base): true}
	queue := []string{strings.ToLower(base)}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range children[cur] {
			k := strings.ToLower(ch)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, ch)
			queue = append(queue, k)
		}
	}
	return out
}

// findSpeciesForm resolves a species name AND form in the stat list.
//
// Form is a separate column upstream, not part of the name: all three Kyurem
// rows are called "Kyurem" and differ only by Black, Normal and White, and their
// attack stats differ by 26%. Matching on the name alone therefore does not pick
// a variant, it picks whichever row findSpecies prefers, which for Kyurem is
// Normal and for Giratina (which has no Normal row) is whichever the upstream
// feed happened to list first.
//
// An empty form means the caller has nothing to say, and falls back to
// findSpecies so the behaviour is unchanged for callers that never had one. A
// form the stat list does not contain also falls back rather than failing: the
// species is still far more right than nothing, and an unrecognised form is a
// sign the server's data is behind rather than that the reading is wrong.
func findSpeciesForm(pokeList []pokemonStatEntry, name, form string) *pokemonStatEntry {
	if form == "" {
		return findSpecies(pokeList, name)
	}
	for i := range pokeList {
		if strings.EqualFold(pokeList[i].PokemonName, name) &&
			strings.EqualFold(pokeList[i].Form, form) {
			return &pokeList[i]
		}
	}
	return findSpecies(pokeList, name)
}

// findSpecies resolves a species name in the stat list, preferring the Normal
// form over regional variants.
func findSpecies(pokeList []pokemonStatEntry, name string) *pokemonStatEntry {
	var first *pokemonStatEntry
	for i := range pokeList {
		if !strings.EqualFold(pokeList[i].PokemonName, name) {
			continue
		}
		if first == nil {
			first = &pokeList[i]
		}
		if strings.EqualFold(pokeList[i].Form, "Normal") {
			return &pokeList[i]
		}
	}
	return first
}
