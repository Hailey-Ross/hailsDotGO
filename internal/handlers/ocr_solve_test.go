package handlers

import (
	"strings"
	"testing"
)

// solveTestEnv is the shared game data every case below needs.
func solveTestEnv(t *testing.T) solveEnv {
	t.Helper()
	return solveEnv{
		pokeList:   loadPokeList(t),
		cpms:       loadCPMs(t),
		evolutions: loadEvolutions(t),
	}
}

func extractedFrom(t *testing.T, resp map[string]any) ocrExtracted {
	t.Helper()
	ext, ok := resp["extracted"].(ocrExtracted)
	if !ok {
		t.Fatalf("response carries no ocrExtracted: %T", resp["extracted"])
	}
	return ext
}

// The nickname fallback used to live inline in IVFromOCR and could only be
// tested through its pieces. It runs inside the solve now, so the whole path is
// exercised: a Pokemon nicknamed "John Cena" whose candy line says MACHOP
// resolves to the family member whose stats actually fit the scan.
func TestSolveScanResolvesNicknameThroughCandy(t *testing.T) {
	res := loadRecorded(t, "Machamp-original.rapidocr.json")
	env := solveTestEnv(t)
	env.trainerLevel = 46
	env.candyText = res.FullText

	ext := ocrExtracted{
		CP:            extractCP(res.Lines, res.FullText, 2340),
		HP:            extractHP(res.FullText),
		RawDust:       detectRawDust(res.FullText),
		PokemonName:   "John Cena",
		AppraisalBars: -1,
	}

	resp := solveScan(ext, env)
	got := extractedFrom(t, resp)
	if got.PokemonName != "Machamp" {
		t.Errorf("pokemon_name = %q, want %q", got.PokemonName, "Machamp")
	}
	if got.NameSource != "candy" {
		t.Errorf("name_source = %q, want %q", got.NameSource, "candy")
	}
	if resp["count"].(int) == 0 {
		t.Error("no candidates for a scan the family fit resolved")
	}
}

// Without the OCR text there is no candy line to read, so the fallback must not
// fire. This is the app path: the app resolves the species itself and sends a
// name, so a blank candyText is normal rather than a failure.
func TestSolveScanSkipsCandyWithoutText(t *testing.T) {
	res := loadRecorded(t, "Machamp-original.rapidocr.json")
	env := solveTestEnv(t)
	env.trainerLevel = 46

	ext := ocrExtracted{
		CP:            extractCP(res.Lines, res.FullText, 2340),
		HP:            extractHP(res.FullText),
		RawDust:       detectRawDust(res.FullText),
		PokemonName:   "John Cena",
		AppraisalBars: -1,
	}

	resp := solveScan(ext, env)
	if got := extractedFrom(t, resp); got.NameSource == "candy" {
		t.Error("candy fallback fired with no OCR text to read it from")
	}
	if _, ok := resp["pokemon"]; ok {
		t.Error("an unresolved species must not answer with a stat row")
	}
}

// The arc rescue is the server deciding the CP it was given is wrong. On a scan
// the server read itself that is a correction; on a reading the app submitted it
// would be the server overruling the better reader, which the mandate forbids.
// Both cases still REPORT the rescue, so the client is told what happened.
func TestSolveScanArcRescueRespectsCorrectReading(t *testing.T) {
	res := loadRecorded(t, "PURIFIED-Altaria-original.rapidocr.json")
	img := loadFixture(t, "PURIFIED-Altaria-original.png")

	base := solveTestEnv(t)
	base.trainerLevel = 46
	base.arc = detectArcLevel(img, 46, base.cpms)
	if !base.arc.OK {
		t.Fatal("arc not detected in the fixture")
	}

	name, _ := detectName(res.Lines, res.FullText, 2340)
	misreadCP := extractCP(res.Lines, res.FullText, 2340) // 17, a truncated 1790
	newExt := func() ocrExtracted {
		return ocrExtracted{
			CP:            misreadCP,
			CPSource:      "text",
			HP:            extractHP(res.FullText),
			RawDust:       detectRawDust(res.FullText),
			PokemonName:   name,
			AppraisalBars: -1,
			ArcLevel:      base.arc.Level,
		}
	}

	t.Run("server read: the reading is corrected", func(t *testing.T) {
		env := base
		env.correctReading = true
		resp := solveScan(newExt(), env)
		if resp["arc_rescue"] != true {
			t.Fatalf("arc_rescue = %v, want true", resp["arc_rescue"])
		}
		got := extractedFrom(t, resp)
		// The rescued set is CP-free, so the candidates disagree on CP and the
		// card must clear the misread rather than display it.
		if got.CP != 0 {
			t.Errorf("cp = %d, want 0 (ambiguous CP-free result set)", got.CP)
		}
		if got.CPSource != "arc-level" {
			t.Errorf("cp_source = %q, want %q", got.CPSource, "arc-level")
		}
	})

	t.Run("app read: the reading is left alone", func(t *testing.T) {
		env := base
		env.correctReading = false
		resp := solveScan(newExt(), env)
		// The rescue still runs, still finds the spreads, and still says so.
		if resp["arc_rescue"] != true {
			t.Fatalf("arc_rescue = %v, want true", resp["arc_rescue"])
		}
		if resp["count"].(int) == 0 {
			t.Error("the rescue found no candidates")
		}
		got := extractedFrom(t, resp)
		if got.CP != misreadCP {
			t.Errorf("cp = %d, want %d: the server rewrote a field the app owns", got.CP, misreadCP)
		}
		if got.CPSource != "text" {
			t.Errorf("cp_source = %q, want %q: the server relabelled the app's reading", got.CPSource, "text")
		}
	})
}

// The dust value implies a status when every surviving interpretation agrees on
// one. Folding that back into the reading is the same class of correction as the
// arc rescue and is gated the same way.
func TestSolveScanDustInferenceRespectsCorrectReading(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 40

	// 1560 is 1.2x the 1300 tier and is not reachable by any other modifier, so
	// every surviving interpretation is a shadow one. That makes the inference
	// unambiguous, which is precisely when the server path writes it back.
	ext := ocrExtracted{CP: 1964, HP: 140, RawDust: 1560, PokemonName: "Machamp", AppraisalBars: -1}

	corrected := extractedFrom(t, solveScan(ext, withCorrect(env, true)))
	echoed := extractedFrom(t, solveScan(ext, withCorrect(env, false)))

	if echoed.NormalisedDust != ext.NormalisedDust {
		t.Errorf("normalised_dust = %d, want %d: the server rewrote a field the app owns",
			echoed.NormalisedDust, ext.NormalisedDust)
	}
	if echoed.IsLucky != ext.IsLucky || echoed.IsShadow != ext.IsShadow || echoed.IsPurified != ext.IsPurified {
		t.Error("the server folded a dust inference into status flags the app owns")
	}
	// And the server path must still be doing both, or this test would pass
	// just as well against a solve that had quietly stopped inferring anything.
	if corrected.NormalisedDust != 1300 {
		t.Errorf("server normalised_dust = %d, want 1300", corrected.NormalisedDust)
	}
	if !corrected.IsShadow {
		t.Error("server path stopped inferring shadow from an unambiguous dust cost")
	}
}

func withCorrect(env solveEnv, v bool) solveEnv {
	env.correctReading = v
	return env
}

// -1 means "could not read the appraisal" and must not filter anything. 0 is a
// real reading meaning a nought-star Pokemon and must filter hard. Collapsing
// the two turns "I do not know" into a claim about the spread.
func TestSolveScanAppraisalSentinel(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 46

	// A real scan: this spread solves, and it includes the hundo at level 22.5,
	// so the three-star band keeps it and the nought-star band cannot.
	base := ocrExtracted{CP: 1964, HP: 140, RawDust: 3000, PokemonName: "Machamp"}

	unknown := base
	unknown.AppraisalBars = -1
	unknownResp := solveScan(unknown, env)

	nought := base
	nought.AppraisalBars = 0
	noughtResp := solveScan(nought, env)

	three := base
	three.AppraisalBars = 3
	threeResp := solveScan(three, env)

	unknownCount := unknownResp["count"].(int)
	noughtCount := noughtResp["count"].(int)
	threeCount := threeResp["count"].(int)

	if unknownCount == 0 {
		t.Fatal("the unfiltered scan found nothing, so this test proves nothing")
	}
	if noughtCount >= unknownCount {
		t.Errorf("0 stars kept %d of %d candidates: the sentinel was treated as unknown",
			noughtCount, unknownCount)
	}
	if threeCount == 0 {
		t.Error("the three-star band excluded a scan whose spreads include the hundo")
	}
	// A nought-star and a three-star reading cannot describe the same spreads.
	if noughtCount == threeCount {
		t.Errorf("nought and three stars both kept %d candidates", threeCount)
	}
}

// The expensive CP re-read needs the source image. The app path has none, and a
// nil retry must simply disable it rather than being called.
func TestSolveScanWithoutRetryDoesNotCallIt(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 46
	env.retryCP = nil

	// A CP that matches nothing, which is exactly when the retry would fire.
	ext := ocrExtracted{CP: 17, HP: 146, RawDust: 5400, PokemonName: "Altaria", AppraisalBars: -1}
	resp := solveScan(ext, env)
	if resp["count"].(int) != 0 {
		t.Errorf("count = %v, want 0", resp["count"])
	}
	if got := extractedFrom(t, resp); got.CP != 17 {
		t.Errorf("cp = %d, want 17: something re-read a CP with no image to read", got.CP)
	}
}

// A scan with nothing to constrain the search must not reach the solver at all.
func TestSolveScanUnsearchable(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 40

	cases := map[string]ocrExtracted{
		"no hp":            {CP: 1964, HP: 0, RawDust: 3000, PokemonName: "Machamp", AppraisalBars: -1},
		"no cp and no arc": {CP: 0, HP: 140, RawDust: 3000, PokemonName: "Machamp", AppraisalBars: -1},
	}
	for name, ext := range cases {
		t.Run(name, func(t *testing.T) {
			resp := solveScan(ext, env)
			if resp["count"].(int) != 0 {
				t.Errorf("count = %v, want 0", resp["count"])
			}
			if _, ok := resp["pokemon"]; ok {
				t.Error("an unsearchable scan answered with a stat row")
			}
		})
	}
}

// Form is a separate column in the stat data, not part of the name. All three
// Kyurem rows are called "Kyurem", and Black has 310 attack against Normal's
// 246. Matching on the name alone therefore does not pick a variant, it picks
// whichever row the Normal preference lands on, and solves a Black Kyurem
// against a stat line 26% weaker.
func TestSolveScanHonoursForm(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 50

	black := findSpeciesForm(env.pokeList, "Kyurem", "Black")
	normal := findSpeciesForm(env.pokeList, "Kyurem", "Normal")
	if black == nil || normal == nil {
		t.Skip("Kyurem forms not present in the fallback stat data")
	}
	if black.BaseAttack == normal.BaseAttack {
		t.Skip("the two Kyurem rows do not differ, so this proves nothing")
	}

	// Build a scan that is a real Black Kyurem: a hundo at a known level.
	cpm := cpmFor(t, env.cpms, 30.0)
	ext := ocrExtracted{
		CP:            cpForLevelCalc(black.BaseAttack, black.BaseDefense, black.BaseStamina, 15, 15, 15, cpm),
		HP:            hpForLevel(black.BaseStamina, 15, cpm),
		PokemonName:   "Kyurem",
		ArcLevel:      30.0,
		AppraisalBars: -1,
	}

	t.Run("with the form, it solves against the right row", func(t *testing.T) {
		e := env
		e.form = "Black"
		e.arc = arcReading{Level: 30.0, OK: true}
		resp := solveScan(ext, e)
		if resp["count"].(int) == 0 {
			t.Fatal("a real Black Kyurem produced no candidates")
		}
		got, ok := resp["pokemon"].(*pokemonStatEntry)
		if !ok || !strings.EqualFold(got.Form, "Black") {
			t.Fatalf("solved against form %v, want Black", resp["pokemon"])
		}
	})

	t.Run("without the form, the wrong row is used", func(t *testing.T) {
		e := env
		e.form = ""
		e.arc = arcReading{Level: 30.0, OK: true}
		resp := solveScan(ext, e)
		got, ok := resp["pokemon"].(*pokemonStatEntry)
		if !ok || strings.EqualFold(got.Form, "Black") {
			t.Fatal("the name-only lookup picked Black, so the form field is not load bearing")
		}
	})
}

// Giratina has no Normal row at all (Altered and Origin), so the name-only
// lookup falls through to whichever the upstream feed listed first. Their attack
// and defence are swapped, so getting it wrong is not a rounding error.
func TestFindSpeciesFormFallbacks(t *testing.T) {
	list := loadPokeList(t)

	origin := findSpeciesForm(list, "Giratina", "Origin")
	if origin == nil || !strings.EqualFold(origin.Form, "Origin") {
		t.Fatalf("findSpeciesForm(Giratina, Origin) = %v", origin)
	}
	altered := findSpeciesForm(list, "Giratina", "Altered")
	if altered == nil || !strings.EqualFold(altered.Form, "Altered") {
		t.Fatalf("findSpeciesForm(Giratina, Altered) = %v", altered)
	}
	if origin.BaseAttack == altered.BaseAttack {
		t.Error("the two Giratina rows are identical, so the lookup cannot be checked")
	}

	// An empty form must behave exactly as the old lookup did, or the image
	// path has quietly changed.
	if got, want := findSpeciesForm(list, "Kyurem", ""), findSpecies(list, "Kyurem"); got != want {
		t.Errorf("empty form = %v, want the findSpecies result %v", got, want)
	}
	// A form the data does not have falls back rather than failing: the species
	// is still far more right than nothing.
	if got := findSpeciesForm(list, "Kyurem", "Chartreuse"); got == nil {
		t.Error("an unknown form returned nothing instead of falling back")
	}
}
