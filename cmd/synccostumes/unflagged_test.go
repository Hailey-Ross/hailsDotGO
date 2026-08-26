package main

import (
	"encoding/json"
	"testing"

	"pogo.hails.cc/internal/masterfile"
)

// yearTokenCases is the shared truth about which unflagged .f codes are worth reporting.
//
// It is DUPLICATED verbatim in internal/costumes/drift_test.go, for the same reason grammarCases
// is: cmd/synccostumes is package main and cannot import internal/costumes, so the year rule
// exists twice. If the two copies drift, the admin drift check would name a code `make costumes`
// would not act on, which is precisely the class of bug the .g2 case table was written to stop.
//
// Keep the two copies identical. If you add a case here, add it there.
var yearTokenCases = []struct {
	code string // the code WITHOUT its "f:" prefix
	want bool
}{
	// Event costumes: every one of these carries a year.
	{"WCS_2026", true},
	{"PXP_2026", true},
	{"SWIM_2025", true},
	{"ANNIVERSARY_2026_MALAYSIA_01", true},
	{"ANNIVERSARY_2026_PHILIPPINE_01", true},
	{"COPY_2019", true}, // the false negative this whole rule exists for
	{"COIN_A2_2026", true},
	{"GOTOUR_2024_B_02", true},
	{"2020", true}, // a bare year is still a year (Slowpoke's New Year Costume)

	// Permanent battle, regional and pattern forms: none of these carries a year, and reporting
	// any of them would be the noise that teaches people to scroll past the whole section.
	{"MEGA", false},
	{"MEGA_X", false},
	{"ALOLA", false},
	{"GALARIAN", false},
	{"HISUIAN", false},
	{"CROWNED_SWORD", false},
	{"GIGANTAMAX", false},
	{"UNOWN_A", false},
	{"UNOWN_QUESTION_MARK", false},
	{"BLUE_STRIPED", false},
	{"COMPLETE_TEN_PERCENT", false},
	{"FAMILY_OF_THREE", false},
	{"BREAD_DOUGH_MODE_2", false},
	{"TSHIRT_01", false},
	{"FLYING_01", false},
	{"00", false}, // a Spinda spot set, two digits, not a year
	{"A", false},
}

// The sync tool's half of the year rule. Its answers must match the drift check's, case for case.
func TestYearTokenRuleMatchesTheDriftCheck(t *testing.T) {
	for _, c := range yearTokenCases {
		if got := looksLikeEventCostume("f:" + c.code); got != c.want {
			t.Errorf("looksLikeEventCostume(%q) = %v, want %v", "f:"+c.code, got, c.want)
		}
	}
}

// The rule is about .f codes only. A .c code is unambiguous and is admitted from the asset tree
// alone, so it can never be in the unflagged bucket and must never be reported as if it were.
func TestYearTokenRuleIgnoresCostumeOverlayCodes(t *testing.T) {
	for _, code := range []string{"c:FALL_2018", "c:HOLIDAY_2023", "c:SPRING_2024"} {
		if looksLikeEventCostume(code) {
			t.Errorf("looksLikeEventCostume(%q) = true, want false: .c codes are never unflagged", code)
		}
	}
}

// buildCatalog must collect the blind spot rather than dropping it silently, and must still not
// admit any of it: a code enters the catalog only when a human writes a label for it.
func TestBuildCatalogCollectsUnflaggedCodesWithoutAdmittingThem(t *testing.T) {
	files := []string{
		"pm25.fANNIVERSARY_2026_MALAYSIA_01.s.icon.png",
		"pm25.fANNIVERSARY_2026_MALAYSIA_01.g2.s.icon.png", // female-only art is not proof on its own
		"pm54.fSWIM_2025.s.icon.png",
		"pm26.fALOLA.s.icon.png",          // no year: stays silent
		"pm888.fCROWNED_SWORD.s.icon.png", // no year: stays silent
		"pm25.fVISOR_2026.s.icon.png",     // flagged upstream, so admitted, not reported
	}
	mf := masterfileWith(t, `{
		"costumes": {},
		"pokemon": {
			"25": {"name": "Pikachu", "forms": {
				"1": {"name": "Marathon Visor", "proto": "PIKACHU_VISOR_2026", "isCostume": true},
				"2": {"name": "Malaysia", "proto": "PIKACHU_ANNIVERSARY_2026_MALAYSIA_01"}
			}}
		}
	}`)

	cat, _, unflagged := buildCatalog("deadbeef", files, mf, map[string]bool{})

	if _, ok := cat.Codes["f:ANNIVERSARY_2026_MALAYSIA_01"]; ok {
		t.Error("f:ANNIVERSARY_2026_MALAYSIA_01 was admitted; reporting must never admit")
	}
	if _, ok := cat.Codes["f:VISOR_2026"]; !ok {
		t.Error("f:VISOR_2026 is flagged upstream and should have been admitted")
	}
	for _, want := range []string{"f:ANNIVERSARY_2026_MALAYSIA_01", "f:SWIM_2025"} {
		if _, ok := unflagged[want]; !ok {
			t.Errorf("%s missing from the unflagged report", want)
		}
	}
	for _, notWant := range []string{"f:ALOLA", "f:CROWNED_SWORD", "f:VISOR_2026"} {
		if _, ok := unflagged[notWant]; ok {
			t.Errorf("%s reported as unflagged, which is noise", notWant)
		}
	}
	if dex := unflagged["f:SWIM_2025"]; !dex[54] {
		t.Errorf("f:SWIM_2025 dex = %v, want 54 recorded", dex)
	}
	// The .g2 asset must not be what put the code in the report, and must not add a phantom dex.
	if dex := unflagged["f:ANNIVERSARY_2026_MALAYSIA_01"]; len(dex) != 1 || !dex[25] {
		t.Errorf("dex = %v, want exactly {25} from the default art", dex)
	}
}

// masterfileWith parses a masterfile literal. Built from JSON rather than a struct literal because
// masterfile.Data nests anonymous structs, and because parsing is what production does.
func masterfileWith(t *testing.T, raw string) *masterfile.Data {
	t.Helper()
	var d masterfile.Data
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("parse masterfile literal: %v", err)
	}
	return &d
}
