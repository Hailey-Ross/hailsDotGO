package handlers

import (
	"encoding/json"
	"os"
	"testing"
)

func loadCPMs(t *testing.T) []cpmEntry {
	t.Helper()
	raw, err := os.ReadFile("../pogodata/fallback/cp_multipliers.json")
	if err != nil {
		t.Fatalf("read cp_multipliers fallback: %v", err)
	}
	var cpms []cpmEntry
	if err := json.Unmarshal(raw, &cpms); err != nil {
		t.Fatalf("parse cp_multipliers fallback: %v", err)
	}
	return cpms
}

// cpmFor uses the corrected lookup (not the raw file values): real scans come
// from the game, which uses the precise sqrt-interpolated XL half-level CPMs.
func cpmFor(t *testing.T, cpms []cpmEntry, level float64) float64 {
	t.Helper()
	if v, ok := cpmLookup(cpms)[level]; ok {
		return v
	}
	t.Fatalf("no CPM for level %v", level)
	return 0
}

// machamp: base stats from fallback/pokemon.json; scan-validated against a
// real hundo screenshot (CP 1964, HP 140, dust 3000 => level 22.5).
var machamp = pokemonStatEntry{
	BaseAttack: 234, BaseDefense: 159, BaseStamina: 207,
	Form: "Normal", PokemonName: "Machamp", PokemonID: 68,
}

type wantRange struct {
	tier                    int
	minLvl, maxLvl          float64
	lucky, shadow, purified bool
}

func assertRanges(t *testing.T, got []levelRange, want []wantRange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d interpretations, want %d: %+v", len(got), len(want), got)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g.BaseTier == w.tier && g.MinLvl == w.minLvl && g.MaxLvl == w.maxLvl &&
				g.Lucky == w.lucky && g.Shadow == w.shadow && g.Purified == w.purified {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing interpretation %+v in %+v", w, got)
		}
	}
}

func TestDustCandidatesFuzzy(t *testing.T) {
	// 3000 displayed: base 3000, shadow of 2500, lucky of 6000.
	assertRanges(t, dustCandidates(3000, nil, nil, nil), []wantRange{
		{3000, 21, 22.5, false, false, false},
		{2500, 19, 20.5, false, true, false},
		{6000, 31, 32.5, true, false, false},
	})

	// 5400 displayed: shadow of 4500, purified of 6000, lucky+purified of 12000.
	assertRanges(t, dustCandidates(5400, nil, nil, nil), []wantRange{
		{4500, 27, 28.5, false, true, false},
		{6000, 31, 32.5, false, false, true},
		{12000, 43, 44.5, true, false, true},
	})

	// 4500 displayed: base, purified of 5000, lucky of 9000, lucky+purified of 10000.
	assertRanges(t, dustCandidates(4500, nil, nil, nil), []wantRange{
		{4500, 27, 28.5, false, false, false},
		{5000, 29, 30.5, false, false, true},
		{9000, 37, 38.5, true, false, false},
		{10000, 39, 40.5, true, false, true},
	})

	// Corrected XL tiers resolve; retired values do not.
	assertRanges(t, dustCandidates(11000, nil, nil, nil), []wantRange{
		{11000, 41, 42.5, false, false, false},
	})
	if got := dustCandidates(17500, nil, nil, nil); len(got) != 0 {
		t.Errorf("17500 is not a real dust value, got %+v", got)
	}
	if got := dustCandidates(0, nil, nil, nil); got != nil {
		t.Errorf("zero dust should give nil, got %+v", got)
	}
}

func TestDustCandidatesFlagTrust(t *testing.T) {
	// Positive purified flag keeps only purified-compatible interpretations.
	assertRanges(t, dustCandidates(5400, nil, nil, boolPtr(true)), []wantRange{
		{6000, 31, 32.5, false, false, true},
		{12000, 43, 44.5, true, false, true},
	})
	// Manual path: all flags explicit, purified only.
	assertRanges(t, dustCandidates(5400, boolPtr(false), boolPtr(false), boolPtr(true)), []wantRange{
		{6000, 31, 32.5, false, false, true},
	})
	// Explicit normal excludes discounted readings entirely.
	assertRanges(t, dustCandidates(3000, boolPtr(false), boolPtr(false), boolPtr(false)), []wantRange{
		{3000, 21, 22.5, false, false, false},
	})
}

func TestSummariseDustInterpretations(t *testing.T) {
	// Unique interpretation: lucky+purified 90 => base tier 200, both flags set.
	ranges := dustCandidates(90, nil, nil, nil)
	norm, lucky, shadow, purified := summariseDustInterpretations(90, ranges)
	if norm != 200 || !lucky || shadow || !purified {
		t.Errorf("90 dust: got norm=%d lucky=%v shadow=%v purified=%v", norm, lucky, shadow, purified)
	}
	// Ambiguous tiers keep the raw value and set no flags.
	ranges = dustCandidates(3000, nil, nil, nil)
	norm, lucky, shadow, purified = summariseDustInterpretations(3000, ranges)
	if norm != 3000 || lucky || shadow || purified {
		t.Errorf("3000 dust: got norm=%d lucky=%v shadow=%v purified=%v", norm, lucky, shadow, purified)
	}
}

func TestBuildDisplayableDust(t *testing.T) {
	set := make(map[int]bool)
	for _, v := range buildDisplayableDust() {
		set[v] = true
	}
	for _, want := range []int{600, 720, 3600, 5400, 11000, 13000, 14000, 15000, 90} {
		if !set[want] {
			t.Errorf("displayable dust missing %d", want)
		}
	}
	for _, gone := range []int{17500, 20000, 48000, 60000} {
		if set[gone] {
			t.Errorf("displayable dust still contains retired/bogus value %d", gone)
		}
	}
}

func TestEnumerateIVsMachampHundo(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46,
	}
	candidates, buddy := enumerateIVs(req, machamp, cpms)
	if buddy {
		t.Fatal("buddy retry should not trigger for a direct match")
	}
	found := false
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 22.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("hundo @22.5 not in candidates: %+v", candidates)
	}
}

func TestEnumerateIVsBestBuddyRetry(t *testing.T) {
	cpms := loadCPMs(t)
	// A Best Buddy at true level 22.5 displays CP/HP computed at 23.5, while
	// dust still shows the 3000 tier (levels 21-22.5).
	boosted := cpmFor(t, cpms, 23.5)
	cp := cpForLevelCalc(machamp.BaseAttack, machamp.BaseDefense, machamp.BaseStamina, 15, 15, 15, boosted)
	hp := hpForLevel(machamp.BaseStamina, 15, boosted)
	req := ivRequest{
		PokemonName: "Machamp", CP: cp, HP: hp, DustCost: 3000, TrainerLevel: 46,
	}
	candidates, buddy := enumerateIVs(req, machamp, cpms)
	if !buddy {
		t.Fatalf("expected best-buddy interpretation, got %d candidates without it", len(candidates))
	}
	found := false
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 23.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("boosted hundo @23.5 not in buddy candidates: %+v", candidates)
	}
}

func TestEnumerateIVsPoweredBeyondOldCap(t *testing.T) {
	cpms := loadCPMs(t)
	// Level 45.5 mon (13000 dust tier) owned by a level 40 trainer: the old
	// trainerLevel+2 clamp returned zero candidates for this legitimate case.
	cpm := cpmFor(t, cpms, 45.5)
	cp := cpForLevelCalc(machamp.BaseAttack, machamp.BaseDefense, machamp.BaseStamina, 15, 15, 15, cpm)
	hp := hpForLevel(machamp.BaseStamina, 15, cpm)
	req := ivRequest{
		PokemonName: "Machamp", CP: cp, HP: hp, DustCost: 13000, TrainerLevel: 40,
	}
	candidates, buddy := enumerateIVs(req, machamp, cpms)
	if buddy {
		t.Fatal("buddy retry should not trigger")
	}
	found := false
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 45.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("powered hundo @45.5 not found: %+v", candidates)
	}
}

func TestEnumerateIVsCPUnknown(t *testing.T) {
	cpms := loadCPMs(t)
	// Arc-only mode: CP omitted, HP + dust constrain. Every candidate must
	// carry its computed CP and stay inside the dust-derived level union.
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
		IsLucky: boolPtr(false), IsShadow: boolPtr(false), IsPurified: boolPtr(false),
	}
	candidates, _ := enumerateIVs(req, machamp, cpms)
	if len(candidates) == 0 {
		t.Fatal("expected candidates in CP-unknown mode")
	}
	for _, c := range candidates {
		if c.CP <= 0 {
			t.Fatalf("candidate missing computed CP: %+v", c)
		}
		if c.Level < 21 || c.Level > 22.5 {
			t.Fatalf("candidate outside 3000-dust base range: %+v", c)
		}
	}
}

func TestCPMLookupXLHalfLevels(t *testing.T) {
	m := cpmLookup(loadCPMs(t))
	// pogoapi rounds XL half-levels to 4 decimals; the lookup must restore the
	// sqrt-interpolated values (GoIV/Silph reference figures).
	want := map[float64]float64{40.5: 0.7928040, 45.5: 0.8178038, 49.5: 0.8378038}
	for lvl, ref := range want {
		got, ok := m[lvl]
		if !ok {
			t.Fatalf("missing CPM for %.1f", lvl)
		}
		if diff := got - ref; diff > 1e-7 || diff < -1e-7 {
			t.Errorf("CPM(%.1f) = %.9f, want ~%.7f", lvl, got, ref)
		}
	}
}

// The live upstream (pogoapi cp_multiplier.json) currently stops at level
// 45.0. The lookup must synthesize 45.5 through 51 from the game's rules, or
// the arc read and high-level searches silently fail in production (found
// live on 2026-07-05: arc ok=false because cpmByLevel[50] was missing).
func TestCPMLookupExtendsTruncatedData(t *testing.T) {
	full := cpmLookup(loadCPMs(t))
	var truncated []cpmEntry
	for _, e := range loadCPMs(t) {
		if e.Level <= 45.0 {
			truncated = append(truncated, e)
		}
	}
	m := cpmLookup(truncated)
	for lvl := 45.5; lvl <= 51.0; lvl += 0.5 {
		got, ok := m[lvl]
		if !ok {
			t.Fatalf("CPM(%.1f) missing from extended lookup", lvl)
		}
		if diff := got - full[lvl]; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("CPM(%.1f) = %.9f, want %.9f (from full table)", lvl, got, full[lvl])
		}
	}
}

func TestMaxPowerUpLevel(t *testing.T) {
	for _, tc := range []struct {
		tl   int
		want float64
	}{{30, 40}, {38, 48}, {40, 50}, {46, 50}, {50, 50}} {
		if got := maxPowerUpLevel(tc.tl); got != tc.want {
			t.Errorf("maxPowerUpLevel(%d) = %v, want %v", tc.tl, got, tc.want)
		}
	}
}
