package handlers

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func decodeReading(t *testing.T, body string) scanReading {
	t.Helper()
	var sub scanSubmission
	if err := json.Unmarshal([]byte(body), &sub); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return sub.Extracted
}

// ---- List A: structural rejects ----

// One case per structural invariant. These are protocol facts: no Pokemon GO
// update can make any of them acceptable, which is why they are safe to reject
// outright rather than flag.
func TestValidateReadingStructureRejects(t *testing.T) {
	cases := map[string]string{
		"negative cp":              `{"extracted":{"cp":-1}}`,
		"cp overflow":              `{"extracted":{"cp":2147483647}}`,
		"negative hp":              `{"extracted":{"hp":-5}}`,
		"hp overflow":              `{"extracted":{"hp":999999999}}`,
		"negative raw dust":        `{"extracted":{"raw_dust":-200}}`,
		"raw dust overflow":        `{"extracted":{"raw_dust":999999999}}`,
		"negative normalised dust": `{"extracted":{"normalised_dust":-1}}`,
		"appraisal below sentinel": `{"extracted":{"appraisal_bars":-2}}`,
		"appraisal above three":    `{"extracted":{"appraisal_bars":4}}`,
		"negative arc level":       `{"extracted":{"arc_level":-1.5}}`,
		"arc level overflow":       `{"extracted":{"arc_level":1e6}}`,
		"shadow and purified":      `{"extracted":{"is_shadow":true,"is_purified":true}}`,
		"species name too long": `{"extracted":{"pokemon_name":"` +
			strings.Repeat("A", maxSubmittedText+1) + `"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateReadingStructure(decodeReading(t, body)); err == nil {
				t.Errorf("accepted a structurally invalid reading: %s", body)
			}
		})
	}
}

// Invalid UTF-8 cannot travel through a JSON string literal, so it is built
// directly rather than decoded.
func TestValidateReadingStructureRejectsInvalidUTF8(t *testing.T) {
	bad := string([]byte{0xff, 0xfe, 0xfd})
	if err := validateReadingStructure(scanReading{PokemonName: &bad}); err == nil {
		t.Error("accepted a species name that is not valid UTF-8")
	}
}

// The mirror of the above, and the more important half. Every one of these
// disagrees with what the server believes about the game, and every one of them
// must still be accepted: the app ships with data the owner updates promptly
// while the server pulls from upstream feeds that demonstrably lag, so a
// disagreement is at least as likely to mean the server is behind.
//
// The precedent is not hypothetical. Live pogoapi cp_multiplier.json stopped at
// level 45 while the game reached 51, and a true level 50 hundo is 106.2% of the
// level 45 maximum. A CP plausibility reject would have thrown that scan away.
func TestValidateReadingStructureAcceptsGameContentDisagreement(t *testing.T) {
	cases := map[string]string{
		"a CP no known species can reach": `{"extracted":{"cp":9000,"hp":100}}`,
		"a species the server has never heard of": `{"extracted":` +
			`{"pokemon_name":"Notamon","cp":100,"hp":10}}`,
		"a dust cost outside the known brackets": `{"extracted":{"raw_dust":777,"hp":10}}`,
		"a level above the current cap":          `{"extracted":{"arc_level":60,"hp":10}}`,
		"a nought star reading":                  `{"extracted":{"appraisal_bars":0,"hp":10}}`,
		"an unknown appraisal":                   `{"extracted":{"appraisal_bars":-1,"hp":10}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateReadingStructure(decodeReading(t, body)); err != nil {
				t.Errorf("rejected a reading the game could legitimately produce: %v", err)
			}
		})
	}
}

// ---- Sentinels ----

// The two fields whose zero means something. Getting this wrong silently turns
// "I could not read it" into "it is zero", which is a claim about the Pokemon
// that no reader made, and which the solve would then filter on.
func TestReadingSentinelsSurviveTheWire(t *testing.T) {
	t.Run("absent appraisal is unknown, not nought stars", func(t *testing.T) {
		ext := readingToExtracted(decodeReading(t, `{"extracted":{"cp":100}}`))
		if ext.AppraisalBars != -1 {
			t.Errorf("appraisal_bars = %d, want -1", ext.AppraisalBars)
		}
	})
	t.Run("explicit nought stars is preserved", func(t *testing.T) {
		ext := readingToExtracted(decodeReading(t, `{"extracted":{"appraisal_bars":0}}`))
		if ext.AppraisalBars != 0 {
			t.Errorf("appraisal_bars = %d, want 0", ext.AppraisalBars)
		}
	})
	t.Run("explicit null is unknown", func(t *testing.T) {
		ext := readingToExtracted(decodeReading(t, `{"extracted":{"appraisal_bars":null}}`))
		if ext.AppraisalBars != -1 {
			t.Errorf("appraisal_bars = %d, want -1", ext.AppraisalBars)
		}
	})
	t.Run("absent arc level is not read", func(t *testing.T) {
		ext := readingToExtracted(decodeReading(t, `{"extracted":{"cp":100}}`))
		if ext.ArcLevel != 0 {
			t.Errorf("arc_level = %v, want 0", ext.ArcLevel)
		}
	})
	t.Run("a real arc level is preserved", func(t *testing.T) {
		ext := readingToExtracted(decodeReading(t, `{"extracted":{"arc_level":39.5}}`))
		if ext.ArcLevel != 39.5 {
			t.Errorf("arc_level = %v, want 39.5", ext.ArcLevel)
		}
	})
}

// A false status flag must reach the solver as nil, not as a pointer to false.
// dustCandidates treats a non-nil flag as authoritative in BOTH directions, and
// the lucky banner or shadow aura is frequently just off the top of the frame,
// so asserting its absence would discard the interpretation that explains the
// dust cost.
func TestTrueOrNilKeepsUnsetFlagsUnknown(t *testing.T) {
	if got := trueOrNil(false); got != nil {
		t.Errorf("trueOrNil(false) = %v, want nil", got)
	}
	if got := trueOrNil(true); got == nil || !*got {
		t.Errorf("trueOrNil(true) = %v, want a pointer to true", got)
	}
}

// ---- List B: advisories, which flag but never reject ----

func advisoryFields(as []scanAdvisory) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Field)
	}
	sort.Strings(out)
	return out
}

func TestAdvisoriesFlagWithoutRejecting(t *testing.T) {
	env := solveTestEnv(t)
	env.trainerLevel = 40

	t.Run("a clean reading raises nothing", func(t *testing.T) {
		ext := ocrExtracted{CP: 1964, HP: 140, RawDust: 3000, PokemonName: "Machamp", AppraisalBars: -1}
		if as := advisoriesFor(ext, findSpecies(env.pokeList, ext.PokemonName), env); len(as) != 0 {
			t.Errorf("advisories = %v, want none", advisoryFields(as))
		}
	})

	t.Run("an unknown species is flagged", func(t *testing.T) {
		ext := ocrExtracted{CP: 100, HP: 10, PokemonName: "Notamon", AppraisalBars: -1}
		as := advisoriesFor(ext, findSpecies(env.pokeList, ext.PokemonName), env)
		if len(as) != 1 || as[0].Field != "pokemon_name" {
			t.Fatalf("advisories = %v, want one for pokemon_name", advisoryFields(as))
		}
		// A flag is only useful later if it says what it was measured against.
		if as[0].DataVersion == "" {
			t.Error("advisory carries no data version")
		}
	})

	t.Run("a CP above the species maximum is flagged", func(t *testing.T) {
		ext := ocrExtracted{CP: 90000, HP: 140, PokemonName: "Machamp", AppraisalBars: -1}
		as := advisoriesFor(ext, findSpecies(env.pokeList, ext.PokemonName), env)
		if got := advisoryFields(as); len(got) != 1 || got[0] != "cp" {
			t.Errorf("advisories = %v, want one for cp", got)
		}
	})

	// A Best Buddy displays CP a level above the Pokemon's real one, so a flag
	// drawn at the bare maximum would fire during ordinary play and drown the
	// signal. computeMaxCP does not allow for it, so the advisory has to.
	t.Run("a Best Buddy CP is not flagged", func(t *testing.T) {
		poke := findSpecies(env.pokeList, "Machamp")
		maxCP := computeMaxCP(*poke, env.trainerLevel, env.cpms)
		if maxCP <= 0 {
			t.Skip("no CPM data to compute a maximum from")
		}
		ext := ocrExtracted{CP: maxCP * 104 / 100, HP: 140, PokemonName: "Machamp", AppraisalBars: -1}
		if as := advisoriesFor(ext, poke, env); len(as) != 0 {
			t.Errorf("advisories = %v, want none for a Best Buddy CP", advisoryFields(as))
		}
		// But something well clear of that headroom must still flag, or the
		// margin above would have simply disabled the check.
		ext.CP = maxCP * 130 / 100
		if as := advisoriesFor(ext, poke, env); len(as) != 1 {
			t.Errorf("advisories = %v, want one for a CP far above the maximum", advisoryFields(as))
		}
	})

	t.Run("a dust cost outside the bracket set is flagged", func(t *testing.T) {
		ext := ocrExtracted{CP: 1964, HP: 140, RawDust: 777, PokemonName: "Machamp", AppraisalBars: -1}
		as := advisoriesFor(ext, findSpecies(env.pokeList, ext.PokemonName), env)
		if got := advisoryFields(as); len(got) != 1 || got[0] != "raw_dust" {
			t.Errorf("advisories = %v, want one for raw_dust", got)
		}
	})

	t.Run("an arc level above the trainer's reach is flagged", func(t *testing.T) {
		ext := ocrExtracted{CP: 1964, HP: 140, PokemonName: "Machamp", ArcLevel: 55, AppraisalBars: -1}
		as := advisoriesFor(ext, findSpecies(env.pokeList, ext.PokemonName), env)
		if got := advisoryFields(as); len(got) != 1 || got[0] != "arc_level" {
			t.Errorf("advisories = %v, want one for arc_level", got)
		}
	})
}

// The cross check the image path never performed. The star bands are IV sum
// ranges and the solve produces IV sums, so the two can be compared with no
// image at all.
func TestAppraisalDisagreement(t *testing.T) {
	perfect := []IVCandidate{{AtkIV: 15, DefIV: 15, StaIV: 15}}
	poor := []IVCandidate{{AtkIV: 0, DefIV: 1, StaIV: 2}}

	if a := appraisalDisagreement(3, perfect, "v"); a != nil {
		t.Errorf("three stars against a hundo flagged: %+v", a)
	}
	if a := appraisalDisagreement(3, poor, "v"); a == nil {
		t.Error("three stars against a 3 percent spread was not flagged")
	}
	if a := appraisalDisagreement(0, perfect, "v"); a == nil {
		t.Error("nought stars against a hundo was not flagged")
	}
	// An unknown appraisal asserts nothing, so it cannot disagree with anything.
	if a := appraisalDisagreement(-1, poor, "v"); a != nil {
		t.Errorf("an unknown appraisal was flagged: %+v", a)
	}
	// Nothing solved means nothing to compare against.
	if a := appraisalDisagreement(3, nil, "v"); a != nil {
		t.Errorf("an empty candidate set was flagged: %+v", a)
	}
}

// ---- Unknown fields: reported, never rejected ----

func TestUnknownJSONFields(t *testing.T) {
	full := `{"client":{"build":31,"detector":"mlkit+bars/2","data_version":"x"},
	          "extracted":{"cp":4027,"raw_cp":"4027","cp_source":"text","hp":171,
	          "raw_dust":10000,"normalised_dust":10000,"pokemon_name":"Groudon",
	          "name_source":"card","arc_level":39.0,"appraisal_bars":3,
	          "is_hundo":true,"is_lucky":false,"is_shadow":false,"is_purified":false}}`

	if got := unknownJSONFields([]byte(full)); len(got) != 0 {
		t.Errorf("unknown fields in a complete valid body: %v", got)
	}

	// The case this is actually for: a misspelled key. Without the report it
	// reads as an absent field and silently becomes a sentinel.
	typo := `{"extracted":{"appraisal_bar":3}}`
	got := unknownJSONFields([]byte(typo))
	if len(got) != 1 || got[0] != "extracted.appraisal_bar" {
		t.Errorf("unknownJSONFields = %v, want [extracted.appraisal_bar]", got)
	}

	if got := unknownJSONFields([]byte(`{"nonsense":1,"extracted":{}}`)); len(got) != 1 || got[0] != "nonsense" {
		t.Errorf("unknownJSONFields = %v, want [nonsense]", got)
	}

	// And a body carrying a field only a newer client knows about must still
	// validate, because rejecting it would make every app release wait on a
	// server deploy. Reporting is the whole response.
	newer := `{"extracted":{"cp":100,"hp":10,"is_mega":true}}`
	if err := validateReadingStructure(decodeReading(t, newer)); err != nil {
		t.Errorf("a body from a newer client was rejected: %v", err)
	}
	if got := unknownJSONFields([]byte(newer)); len(got) != 1 {
		t.Errorf("unknownJSONFields = %v, want the one new field reported", got)
	}
}

func TestServerDataVersionRecordsWhatGoesStale(t *testing.T) {
	v := serverDataVersion(loadPokeList(t), loadCPMs(t))
	if !strings.Contains(v, "species=") || !strings.Contains(v, "cpm_max=") {
		t.Errorf("data version = %q, want the species count and the CPM ceiling", v)
	}
}
