package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests replay REAL RapidOCR service responses recorded from the live
// VPS against the committed fixture screenshots (2026-07-05). They lock in
// the quirks the service actually produces: glued tokens ("MACHOPCANDY",
// "ALTARIAMEGAENERGY", "153/153HP"), the dropped C on Celesteela ("P2197"),
// and the truncated Altaria CP ("CP17").

func loadRecorded(t *testing.T, name string) *ocrResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ocr", name))
	if err != nil {
		t.Skipf("recorded OCR fixture %s not available: %v", name, err)
	}
	var res ocrResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return &res
}

func TestRecordedExtraction(t *testing.T) {
	known := knownFromList(loadPokeList(t))
	cases := []struct {
		file      string
		cp        int // what extractCP yields from the recorded text
		hp        int
		dust      int
		candyBase string
		note      string
	}{
		{"Kartana-original.rapidocr.json", 2717, 104, 4000, "Kartana", ""},
		{"Dugtrio-original.rapidocr.json", 985, 78, 3000, "Diglett", ""},
		{"Snorlax-original.rapidocr.json", 3102, 267, 9000, "Snorlax", ""},
		{"Machamp-original.rapidocr.json", 1964, 140, 3000, "Machop", "nicknamed John Cena"},
		{"PURIFIED-Altaria-original.rapidocr.json", 17, 146, 5400, "Swablu", "CP truncated by OCR; arc rescue handles it"},
		{"Celesteela-original.rapidocr.json", 2197, 153, 4000, "Celesteela", "C dropped from CP; strategy 2 recovers"},
	}
	for _, tc := range cases {
		res := loadRecorded(t, tc.file)
		if got := extractCP(res.Lines, res.FullText, 2340); got != tc.cp {
			t.Errorf("%s: extractCP = %d, want %d (%s)", tc.file, got, tc.cp, tc.note)
		}
		if got := extractHP(res.FullText); got != tc.hp {
			t.Errorf("%s: extractHP = %d, want %d", tc.file, got, tc.hp)
		}
		if got := detectRawDust(res.FullText); got != tc.dust {
			t.Errorf("%s: detectRawDust = %d, want %d", tc.file, got, tc.dust)
		}
		if got := detectCandyBase(res.FullText, known); !strings.EqualFold(got, tc.candyBase) {
			t.Errorf("%s: detectCandyBase = %q, want %q", tc.file, got, tc.candyBase)
		}
	}
}

// The glued mega-energy line must resolve the species directly.
func TestRecordedAltariaMegaName(t *testing.T) {
	res := loadRecorded(t, "PURIFIED-Altaria-original.rapidocr.json")
	name, source := detectName(res.Lines, res.FullText, 2340)
	if !strings.EqualFold(name, "Altaria") || source != "mega" {
		t.Errorf("detectName = %q/%q, want Altaria/mega", name, source)
	}
}

// Full pipeline on real OCR text + real pixels, no service required.
func TestRecordedFullPipeline(t *testing.T) {
	cpms := loadCPMs(t)
	pokeList := loadPokeList(t)
	evo := loadEvolutions(t)
	known := knownFromList(pokeList)

	t.Run("Machamp nickname", func(t *testing.T) {
		res := loadRecorded(t, "Machamp-original.rapidocr.json")
		base := detectCandyBase(res.FullText, known)
		req := ivRequest{
			CP: extractCP(res.Lines, res.FullText, 2340),
			HP: extractHP(res.FullText), DustCost: detectRawDust(res.FullText), TrainerLevel: 46,
		}
		var fits []string
		for _, name := range familySpecies(base, evo) {
			sp := findSpecies(pokeList, name)
			if sp == nil {
				continue
			}
			ranges := dustCandidates(req.DustCost, nil, nil, nil)
			if cands, _, _ := runOCRSearch(req, ranges, arcReading{}, *sp, cpms); len(cands) > 0 {
				fits = append(fits, sp.PokemonName)
			}
		}
		if len(fits) != 1 || !strings.EqualFold(fits[0], "Machamp") {
			t.Errorf("family fits = %v, want exactly [Machamp]", fits)
		}
	})

	t.Run("Altaria misread CP arc rescue", func(t *testing.T) {
		res := loadRecorded(t, "PURIFIED-Altaria-original.rapidocr.json")
		img := loadFixture(t, "PURIFIED-Altaria-original.png")
		arc := detectArcLevel(img, 46, cpms)
		if !arc.OK {
			t.Fatal("arc not detected")
		}
		name, _ := detectName(res.Lines, res.FullText, 2340)
		sp := findSpecies(pokeList, name)
		if sp == nil {
			t.Fatalf("species %q not resolved", name)
		}
		req := ivRequest{
			CP: extractCP(res.Lines, res.FullText, 2340), // 17, a misread
			HP: extractHP(res.FullText), DustCost: detectRawDust(res.FullText), TrainerLevel: 46,
		}
		ranges := dustCandidates(req.DustCost, nil, nil, nil)
		cands, buddy, rescue := runOCRSearch(req, ranges, arc, *sp, cpms)
		if !rescue {
			t.Fatalf("expected arc rescue for CP=%d (got %d candidates, buddy=%v)", req.CP, len(cands), buddy)
		}
		found := false
		for _, c := range cands {
			if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 32.5 && c.CP == 1790 {
				found = true
			}
		}
		if !found {
			t.Errorf("hundo @32.5/CP1790 not in rescued candidates (%d found)", len(cands))
		}
		// With CP unknown, atk/def stay free, so candidate CPs differ and the
		// card must CLEAR the misread CP rather than display it.
		if got := unanimousCP(cands); got != 0 {
			t.Errorf("unanimousCP = %d, want 0 (ambiguous CP-free result set)", got)
		}
		// Sorted by IV%% descending: the hundo must be the first row shown.
		if cands[0].AtkIV != 15 || cands[0].DefIV != 15 || cands[0].StaIV != 15 {
			t.Errorf("first candidate is not the hundo: %+v", cands[0])
		}
	})

	t.Run("Celesteela dropped-C CP", func(t *testing.T) {
		res := loadRecorded(t, "Celesteela-original.rapidocr.json")
		sp := findSpecies(pokeList, "Celesteela")
		if sp == nil {
			t.Fatal("Celesteela missing from stat list")
		}
		req := ivRequest{
			CP: extractCP(res.Lines, res.FullText, 2340), // 2197 via strategy 2
			HP: extractHP(res.FullText), DustCost: detectRawDust(res.FullText), TrainerLevel: 46,
		}
		ranges := dustCandidates(req.DustCost, nil, nil, nil)
		cands, _, _ := runOCRSearch(req, ranges, arcReading{}, *sp, cpms)
		if len(cands) == 0 {
			t.Fatal("no candidates for Celesteela scan")
		}
		for _, c := range cands {
			if c.Level < 25 || c.Level > 26.5 {
				t.Errorf("candidate outside 4000-dust bracket: %+v", c)
			}
		}
	})
}
