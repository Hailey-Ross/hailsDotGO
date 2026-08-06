package main

import "testing"

// grammarCases is the shared truth about the asset filename grammar.
//
// It is DUPLICATED verbatim in internal/costumes/drift_test.go, and that duplication is the point.
// The two halves of this pipeline parse the same filenames with two different regexes, because
// cmd/synccostumes is package main and cannot import internal/costumes, and they have already
// drifted apart once: the drift check accepted .g2 female art as proof a code existed while
// buildCatalog filed it under "female" and never made a code of it, so the admin button would have
// nagged forever about a costume `make costumes` could not add. Asserting the same table on both
// sides is what stops the next divergence being invisible.
//
// Keep the two copies identical. If you add a case here, add it there.
var grammarCases = []struct {
	file   string
	code   string // "" means this filename must not produce a costume code
	dex    int
	female bool
}{
	{file: "pm25.cFALL_2018.s.icon.png", code: "c:FALL_2018", dex: 25},
	{file: "pm25.fVISOR_2026.s.icon.png", code: "f:VISOR_2026", dex: 25},
	{file: "pm263.fGALARIAN.cGOFEST_2021_NOEVOLVE.s.icon.png", code: "c:GOFEST_2021_NOEVOLVE", dex: 263},
	{file: "pm25.cFALL_2018.g2.s.icon.png", code: "c:FALL_2018", dex: 25, female: true},
	{file: "pm25.fVISOR_2026.g2.s.icon.png", code: "f:VISOR_2026", dex: 25, female: true},
	{file: "pm1025.cFALL_2018.s.icon.png", code: "c:FALL_2018", dex: 1025}, // dex is not zero padded
	{file: "pm25.cFALL_2018.icon.png"},                                     // not shiny
	{file: "pm25.s.icon.png"},                                              // a plain species shiny
	{file: "pm25.g2.s.icon.png"},                                           // a plain female shiny
	{file: "pm25.icon.png"},
	{file: "pm25.cFALL_2018.s.png"},
	{file: "notapokemon.png"},
}

// assetRe is the sync tool's half of the grammar. Its answers must match driftRe's, case for case.
func TestAssetGrammarMatchesTheDriftCheck(t *testing.T) {
	for _, c := range grammarCases {
		m := assetRe.FindStringSubmatch(c.file)
		code, dex, female := "", 0, false
		if m != nil && m[5] == ".s" { // shiny only, exactly as buildCatalog gates it
			switch {
			case m[3] != "":
				code = "c:" + m[3]
			case m[2] != "":
				code = "f:" + m[2]
			}
			if code != "" {
				dex = atoiOrZero(m[1])
				female = m[4] == ".g2"
			}
		}
		if code != c.code || (c.code != "" && (dex != c.dex || female != c.female)) {
			t.Errorf("%s -> code %q dex %d female %v, want code %q dex %d female %v",
				c.file, code, dex, female, c.code, c.dex, c.female)
		}
	}
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
