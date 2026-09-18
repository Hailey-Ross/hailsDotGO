package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"pogo.hails.cc/internal/pogodata"
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

// ── Arc rescue ───────────────────────────────────────────────────────────────

// The arc rescue exists because the device cannot do it. Server-assisted scanning
// was removed from the app on 2026-08-31, and with it went the one thing that
// caught a MISREAD CP: the solver finding no spread for the scanned CP, discarding
// it, and re-solving against the arc level the device had read.
//
// The device's own CP_FROM_ARC only covers unreadable CP digits. It does nothing
// when they are readable and wrong, so without this a misread CP produces an empty
// candidate list with no explanation.
// intersectRangesWithLevel is what the arc rescue is built on, and it had no test
// of its own until it started backing a public endpoint.
//
// The third case is the one that matters most and the one the mobile handoff asked
// for a second, separate fallback for: when the arc reading and the dust reading
// disagree completely, the arc STANDS ALONE. That fallback is already in here, so
// adding another outside it would widen the window twice.
func TestIntersectRangesWithLevel(t *testing.T) {
	dust3000 := []levelRange{{MinLvl: 21, MaxLvl: 22.5}}

	// Overlapping: the window is clipped to the intersection.
	got := intersectRangesWithLevel(dust3000, 21, 0.5)
	if len(got) != 1 || got[0].MinLvl != 21 || got[0].MaxLvl != 21.5 {
		t.Errorf("overlapping arc gave %+v, want a single 21 to 21.5 window", got)
	}

	// Non-overlapping: the arc window replaces the dust window entirely.
	got = intersectRangesWithLevel(dust3000, 40, 0.5)
	if len(got) != 1 || got[0].MinLvl != 39.5 || got[0].MaxLvl != 40.5 {
		t.Errorf("non-overlapping arc gave %+v, want the bare 39.5 to 40.5 window", got)
	}

	// No dust reading at all: same fallback, which is the case where the arc is
	// the only thing bounding the sweep.
	got = intersectRangesWithLevel(nil, 15, 0.5)
	if len(got) != 1 || got[0].MinLvl != 14.5 || got[0].MaxLvl != 15.5 {
		t.Errorf("arc with no dust gave %+v, want 14.5 to 15.5", got)
	}

	// Level 1 is the floor: a level 1 Pokemon must not sweep from 0.5.
	got = intersectRangesWithLevel(nil, 1, 0.5)
	if got[0].MinLvl != 1 {
		t.Errorf("arc at level 1 swept from %v, want a floor of 1", got[0].MinLvl)
	}
}

// The end to end wiring: an arc reading supplied to a CP-free solve reaches the
// enumerator and bounds it.
//
// The data is the same Machamp the other tests use, whose HP of 140 is reachable
// only at the top of the 3000-dust bracket. That matters: an arc level
// inconsistent with the HP finds nothing on the first pass and the Best Buddy
// retry shifts the window a level, which looks exactly like the arc being ignored.
func TestArcLevelIsAppliedToACPFreeSolve(t *testing.T) {
	cpms := loadCPMs(t)

	arc := 22.5
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
		IsLucky: boolPtr(false), IsShadow: boolPtr(false), IsPurified: boolPtr(false),
		ArcLevel: &arc,
	}
	candidates, buddy := enumerateIVs(req, machamp, cpms)
	if buddy {
		t.Fatal("the Best Buddy retry fired, so the first pass found nothing and this is not testing the arc window")
	}
	if len(candidates) == 0 {
		t.Fatal("an arc-bounded CP-free solve returned nothing")
	}

	// 3000 dust alone spans 21 to 22.5; the arc cuts the bottom off at 22.
	for _, c := range candidates {
		if c.Level < 22 || c.Level > 22.5 {
			t.Errorf("candidate outside the arc-narrowed window: %+v", c)
		}
		if c.CP <= 0 {
			t.Errorf("candidate missing its computed CP, which is the value the client adopts: %+v", c)
		}
	}

	// And the real spread is still in there, which is the point: the rescue has to
	// recover the answer, not just a smaller wrong set.
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 22.5 && c.CP == 1964 {
			return
		}
	}
	t.Error("the hundo the scanned CP would have matched is not among the rescued candidates")
}

// An arc reading must not change an ordinary solve that already agrees with it.
func TestArcLevelLeavesAMatchingSolveAlone(t *testing.T) {
	cpms := loadCPMs(t)

	req := ivRequest{
		PokemonName: "Machamp", CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46,
	}
	without, _ := enumerateIVs(req, machamp, cpms)

	arc := 22.5
	req.ArcLevel = &arc
	with, _ := enumerateIVs(req, machamp, cpms)

	if len(with) == 0 {
		t.Fatal("adding an agreeing arc reading emptied the candidate set")
	}
	for _, c := range with {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 22.5 {
			return
		}
	}
	t.Errorf("the hundo at 22.5 fell out when an agreeing arc level was supplied (%d without, %d with)", len(without), len(with))
}

func intPtr(v int) *int { return &v }

func f64Ptr(v float64) *float64 { return &v }

func TestSnapUpToHalfStep(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{22.5, 22.5}, {22.0, 22.0}, {1.0, 1.0}, {51.0, 51.0},
		{21.8, 22.0}, {22.3, 22.5}, {1.1, 1.5}, {22.75, 23.0},
		// A hair ABOVE a half step snaps to the next one, which loses 22.0 from
		// the walk. Pinned as the known behavior rather than as a wish: no client
		// can produce it (both take their level from the CPM table), and the only
		// way in is a hand-written arc_level of 22.500000000000004, which returned
		// nothing at all before the snap existed.
		{22.000000000000004, 22.5},
	} {
		if got := snapUpToHalfStep(c.in); got != c.want {
			t.Errorf("snapUpToHalfStep(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// An arc_level that is not on a half step used to answer 200 with an empty
// list and no explanation: the level window it described was correct, but the
// walk started on its unaligned edge and stepped 0.5 at a time past every real
// level. Neither shipping client can send one (both take their level out of
// the CPM table), but /iv/scan accepts a caller-supplied float on a range check
// alone, and so does this endpoint.
func TestUnalignedArcLevelStillSolves(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
		ArcLevel: f64Ptr(22.3),
	}
	candidates, _ := enumerateIVs(req, machamp, cpms)
	if len(candidates) == 0 {
		t.Fatal("an off-grid arc level solved to nothing, so the walk is still off phase")
	}
	for _, c := range candidates {
		if c.Level*2 != float64(int(c.Level*2)) {
			t.Errorf("candidate sits off the half-step grid: %+v", c)
		}
	}
	found := false
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 22.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("the hundo at 22.5 is not inside the window an arc read of 22.3 describes")
	}
}

// The aligned case, pinned on the LOW EDGE of the arc window, because that is
// the level a snap that rounded the wrong way would eat.
//
// An earlier version of this test used dust 3000 with arc 22.5 and asserted only
// that every candidate sat between 22.0 and 23.0. That solve returns every one
// of its candidates at a single level, so the assertion was vacuous and the test
// passed just as happily with the snap reverted. This one does not: an arc of
// 23.0 with no dust describes [22.5, 23.5], and the low edge carries real
// candidates, so losing it is visible in the level set.
func TestAlignedArcWindowKeepsItsLowEdge(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, TrainerLevel: 46,
		ArcLevel: f64Ptr(23.0),
	}
	candidates, _ := enumerateIVs(req, machamp, cpms)
	if len(candidates) == 0 {
		t.Fatal("the aligned arc level stopped solving")
	}
	levels := map[float64]int{}
	for _, c := range candidates {
		levels[c.Level]++
	}
	for _, want := range []float64{22.5, 23.0, 23.5} {
		if levels[want] == 0 {
			t.Errorf("no candidate at level %v; the window [22.5, 23.5] produced %v", want, levels)
		}
	}
	for lvl := range levels {
		if lvl < 22.5 || lvl > 23.5 {
			t.Errorf("candidate outside the arc window at level %v: %v", lvl, levels)
		}
	}
}

// A known IV triple collapses a solve to the spread it names. Driven on the
// CP-free case, because that is the one the constraint is worth having for: CP
// pins the spread already, while without it the attack and defense axes are
// free and the uncapped response carries every one of them.
func TestIVConstraintNarrowsTheCPFreeSolve(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
		IsLucky: boolPtr(false), IsShadow: boolPtr(false), IsPurified: boolPtr(false),
	}
	wide, _, _ := solveWithIVConstraint(req, machamp, cpms)
	if len(wide) < 2 {
		t.Fatalf("need a wide list to narrow, got %d", len(wide))
	}

	req.AtkIV, req.DefIV, req.StaIV = intPtr(15), intPtr(15), intPtr(15)
	got, _, ignored := solveWithIVConstraint(req, machamp, cpms)
	if ignored {
		t.Fatal("a triple that matches a candidate must not be reported as ignored")
	}
	if len(got) == 0 || len(got) >= len(wide) {
		t.Fatalf("got %d candidates out of %d, want a strictly narrowed list", len(got), len(wide))
	}
	for _, c := range got {
		if c.AtkIV != 15 || c.DefIV != 15 || c.StaIV != 15 {
			t.Errorf("candidate outside the constraint survived: %+v", c)
		}
	}
	found := false
	for _, c := range got {
		if c.Level == 22.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("the hundo at 22.5 did not survive its own constraint: %+v", got)
	}
}

// The load-bearing case: a triple that matches nothing hands back the
// unconstrained list with the flag set, never an empty one. Either the bars
// were misread or the CP, HP or dust was, and an empty answer would hide a
// correct solve behind a bad reading.
func TestIVConstraintImpossibleKeepsTheWideList(t *testing.T) {
	cpms := loadCPMs(t)
	// Deliberately the CP-free solve rather than the CP one. With a CP the
	// unconstrained list is a single candidate, and "the wide list came back"
	// then only distinguishes 1 from 0, which is not what this test claims to
	// prove. Here it is 512 rows.
	req := ivRequest{
		PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
	}
	wide, _, _ := solveWithIVConstraint(req, machamp, cpms)
	if len(wide) < 2 {
		t.Fatalf("need a wide list to keep, got %d", len(wide))
	}

	// HP 140 pins the stamina IV per level, and 0 is not one of the values it can
	// take here, so this is the mismatch case. It doubles as the proof that a zero
	// IV is applied rather than read as absent: an absent triple reports ignored
	// false.
	req.AtkIV, req.DefIV, req.StaIV = intPtr(0), intPtr(0), intPtr(0)
	got, _, ignored := solveWithIVConstraint(req, machamp, cpms)
	if !ignored {
		t.Fatal("a triple matching no candidate must report iv_constraint_ignored")
	}
	if len(got) != len(wide) {
		t.Fatalf("got %d candidates, want the unconstrained %d back", len(got), len(wide))
	}
}

// With no triple the filter is a pass-through, which is what keeps every
// existing client byte-identical.
func TestIVConstraintAbsentIsANoOp(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46,
	}
	wide, _ := enumerateIVs(req, machamp, cpms)
	got, _, ignored := solveWithIVConstraint(req, machamp, cpms)
	if ignored {
		t.Error("no triple was sent, so nothing can have been ignored")
	}
	if len(got) != len(wide) {
		t.Errorf("got %d candidates, want %d unchanged", len(got), len(wide))
	}
}

// A correctly read Best Buddy has to survive its own constraint.
//
// The card displays CP and HP one level above the true level, and the retry that
// finds that only fires when a pass comes back empty. While the triple was
// filtered onto the finished list instead of applied inside the sweep, this
// solve was non-empty at the displayed level, the retry never fired, and the
// trainer was told their bars did not fit: 15/15/15 at 22.5 came back with
// iv_constraint_ignored set, on a card whose real spread is 12/13/13 at 23.
func TestIVConstraintRescuesABestBuddy(t *testing.T) {
	cpms := loadCPMs(t)
	req := ivRequest{
		PokemonName: "Machamp", CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46,
		AtkIV: intPtr(12), DefIV: intPtr(13), StaIV: intPtr(13),
	}
	got, buddy, ignored := solveWithIVConstraint(req, machamp, cpms)
	if ignored {
		t.Fatalf("a correct Best Buddy read was reported as ignored: %+v", got)
	}
	if !buddy {
		t.Error("the Best Buddy interpretation was not reported")
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want the single 12/13/13 spread: %+v", len(got), got)
	}
	if got[0].AtkIV != 12 || got[0].DefIV != 13 || got[0].StaIV != 13 || got[0].Level != 23 {
		t.Errorf("wrong candidate: %+v", got[0])
	}
}

// An exact triple outranks the two hints read off the same dialog.
//
// top_stat and appraisal_bars are weaker statements about the same three
// numbers. While they were applied inside the sweep and the triple was filtered
// on afterwards, a star band off by one silently deleted the caller's own answer
// and the response blamed the IV read: the first case below returned 175 rows
// without the hundo in them, and the second returned an EMPTY list with
// iv_constraint_ignored false, which is the one outcome the contract forbids.
func TestIVConstraintOutranksTheAppraisalHints(t *testing.T) {
	cpms := loadCPMs(t)
	bars := 2 // a two-star band, IV sum 30 to 36, which excludes a hundo

	cases := []struct {
		name string
		req  ivRequest
	}{
		{"cp-free solve with a wrong star band", ivRequest{
			PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
			ArcLevel: f64Ptr(22.5), AppraisalBars: &bars,
			AtkIV: intPtr(15), DefIV: intPtr(15), StaIV: intPtr(15),
		}},
		{"cp solve with a wrong star band", ivRequest{
			PokemonName: "Machamp", CP: 1964, HP: 140, DustCost: 3000, TrainerLevel: 46,
			AppraisalBars: &bars,
			AtkIV:         intPtr(15), DefIV: intPtr(15), StaIV: intPtr(15),
		}},
		{"cp-free solve with a wrong top stat", ivRequest{
			PokemonName: "Machamp", CP: 0, HP: 140, DustCost: 3000, TrainerLevel: 46,
			ArcLevel: f64Ptr(22.5), TopStat: "def",
			AtkIV: intPtr(15), DefIV: intPtr(15), StaIV: intPtr(15),
		}},
	}

	for _, c := range cases {
		got, _, ignored := solveWithIVConstraint(c.req, machamp, cpms)
		if len(got) == 0 {
			t.Errorf("%s: returned an empty list", c.name)
			continue
		}
		if ignored {
			t.Errorf("%s: a hint overrode the triple and it was reported as ignored (%d rows)", c.name, len(got))
			continue
		}
		for _, x := range got {
			if x.AtkIV != 15 || x.DefIV != 15 || x.StaIV != 15 {
				t.Errorf("%s: candidate outside the triple survived: %+v", c.name, x)
			}
		}
	}
}

// solverStore stands a store up with the two blobs the IV solver reads.
//
// Every other test here drives the enumerator directly, which left the HANDLER
// wiring uncovered: the constraint could have been left out of IVCalculate
// altogether, or applied to the wrong request, and the whole suite stayed green.
func solverStore(t *testing.T) *pogodata.Store {
	t.Helper()
	poke, err := os.ReadFile("../pogodata/fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read pokemon fallback: %v", err)
	}
	cpm, err := os.ReadFile("../pogodata/fallback/cp_multipliers.json")
	if err != nil {
		t.Fatalf("read cp_multipliers fallback: %v", err)
	}
	s := pogodata.New()
	s.ApplySolverData(poke, cpm)
	return s
}

// The response IVCalculate actually serves, key by key.
func TestIVCalculateServesTheConstraint(t *testing.T) {
	t.Setenv("CACHE_DIR", t.TempDir())
	h := &Handlers{store: solverStore(t)}

	post := func(t *testing.T, body string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(body))
		h.IVCalculate(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, w.Body.String())
		}
		return got
	}

	const machampCard = `"pokemon_name":"Machamp","cp":1964,"hp":140,"dust_cost":3000,"trainer_level":46`

	t.Run("no triple is unchanged and the flag is still published", func(t *testing.T) {
		got := post(t, `{`+machampCard+`}`)
		if got["count"] != float64(1) || got["definitive"] != true {
			t.Errorf("count = %v, definitive = %v, want 1 and true", got["count"], got["definitive"])
		}
		v, ok := got["iv_constraint_ignored"]
		if !ok {
			t.Fatal("iv_constraint_ignored is missing from the response")
		}
		if v != false {
			t.Errorf("iv_constraint_ignored = %v, want false when no triple was sent", v)
		}
	})

	t.Run("a matching triple pins it", func(t *testing.T) {
		got := post(t, `{`+machampCard+`,"atk_iv":15,"def_iv":15,"sta_iv":15}`)
		if got["count"] != float64(1) || got["definitive"] != true || got["iv_constraint_ignored"] != false {
			t.Errorf("count = %v, definitive = %v, ignored = %v; want 1, true, false",
				got["count"], got["definitive"], got["iv_constraint_ignored"])
		}
	})

	t.Run("a triple that fits nothing keeps the list and drops definitive", func(t *testing.T) {
		got := post(t, `{`+machampCard+`,"atk_iv":0,"def_iv":0,"sta_iv":0}`)
		if got["count"] != float64(1) {
			t.Errorf("count = %v, want the unconstrained 1", got["count"])
		}
		if got["iv_constraint_ignored"] != true {
			t.Errorf("iv_constraint_ignored = %v, want true", got["iv_constraint_ignored"])
		}
		if got["definitive"] != false {
			t.Error("definitive must not be true on a solve whose own triple fit nothing")
		}
	})

	t.Run("a solve that is empty anyway does not blame the triple", func(t *testing.T) {
		// No spread reaches HP 999 at CP 1964, so this solve is empty with or
		// without the triple and the triple eliminated nothing.
		got := post(t, `{"pokemon_name":"Machamp","cp":1964,"hp":999,"dust_cost":3000,"trainer_level":46,"atk_iv":15,"def_iv":15,"sta_iv":15}`)
		if got["count"] != float64(0) {
			t.Fatalf("count = %v, want 0", got["count"])
		}
		if got["iv_constraint_ignored"] != false {
			t.Error("an empty solve was reported as the triple being ignored")
		}
	})

	t.Run("a wrong star band does not delete the answer", func(t *testing.T) {
		got := post(t, `{`+machampCard+`,"appraisal_bars":2,"atk_iv":15,"def_iv":15,"sta_iv":15}`)
		if got["count"] != float64(1) || got["iv_constraint_ignored"] != false {
			t.Errorf("count = %v, ignored = %v; want 1 and false", got["count"], got["iv_constraint_ignored"])
		}
	})

	t.Run("a Best Buddy is solved rather than blamed", func(t *testing.T) {
		got := post(t, `{`+machampCard+`,"atk_iv":12,"def_iv":13,"sta_iv":13}`)
		if got["count"] != float64(1) || got["iv_constraint_ignored"] != false {
			t.Fatalf("count = %v, ignored = %v; want 1 and false", got["count"], got["iv_constraint_ignored"])
		}
		if got["best_buddy_assumed"] != true {
			t.Error("best_buddy_assumed = false on a card that only solves one level up")
		}
	})
}

// The two refusals carry different messages on purpose, so a caller can tell a
// malformed triple from an out-of-range one in a log. TestIVCalculateRequestBounds
// compares status codes only, so the text is pinned here.
func TestIVTripleRefusalMessages(t *testing.T) {
	h := &Handlers{}
	for _, c := range []struct{ name, body, want string }{
		{
			"two of three",
			`{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":14,"def_iv":9}`,
			"atk_iv, def_iv and sta_iv must be sent together",
		},
		{
			"an iv out of range",
			`{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":16,"def_iv":9,"sta_iv":15}`,
			"invalid parameters",
		},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(c.body))
		h.IVCalculate(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, w.Code)
		}
		if !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: body = %s, want it to carry %q", c.name, strings.TrimSpace(w.Body.String()), c.want)
		}
	}
}

// The request bounds, checked before the store is ever consulted, so a bare
// Handlers is enough to drive them.
//
// CP 0 is now legal and means "unknown", which is what makes the arc rescue
// possible. Everything else about the old bound stays: 1 through 9 is a misread
// digit rather than a signal, and something has to constrain the sweep.
func TestIVCalculateRequestBounds(t *testing.T) {
	h := &Handlers{}

	cases := []struct {
		name string
		body string
		want int
	}{
		{"cp 0 with an arc level is the rescue", `{"pokemon_name":"Machamp","cp":0,"hp":140,"trainer_level":46,"arc_level":22.5}`, 0},
		{"cp 0 with no arc level has nothing to solve against", `{"pokemon_name":"Machamp","cp":0,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"a single digit cp is a misread, not a signal", `{"pokemon_name":"Machamp","cp":7,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"cp 9 is still refused", `{"pokemon_name":"Machamp","cp":9,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"cp 10 is the floor and is accepted", `{"pokemon_name":"Machamp","cp":10,"hp":140,"trainer_level":46}`, 0},
		{"a negative cp is refused", `{"pokemon_name":"Machamp","cp":-5,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"an absurd cp is refused", `{"pokemon_name":"Machamp","cp":50001,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"an arc level below 1 is refused", `{"pokemon_name":"Machamp","cp":0,"hp":140,"trainer_level":46,"arc_level":0.5}`, http.StatusBadRequest},
		{"an arc level above 51 is refused", `{"pokemon_name":"Machamp","cp":0,"hp":140,"trainer_level":46,"arc_level":52}`, http.StatusBadRequest},
		{"no name is refused", `{"pokemon_name":"","cp":1964,"hp":140,"trainer_level":46}`, http.StatusBadRequest},
		{"a full in-range iv triple is accepted", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":14,"def_iv":9,"sta_iv":15}`, 0},
		{"an all-zero iv triple is a real reading", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":0,"def_iv":0,"sta_iv":0}`, 0},
		{"an iv above 15 is refused", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":16,"def_iv":9,"sta_iv":15}`, http.StatusBadRequest},
		{"a negative iv is refused", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":14,"def_iv":9,"sta_iv":-1}`, http.StatusBadRequest},
		{"two of three ivs is refused", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"atk_iv":14,"def_iv":9}`, http.StatusBadRequest},
		{"one of three ivs is refused", `{"pokemon_name":"Machamp","cp":1964,"hp":140,"trainer_level":46,"sta_iv":15}`, http.StatusBadRequest},
	}

	for _, c := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(c.body))

		// A request that passes validation goes on to read the store, which a bare
		// Handlers does not have. Only the refusals can be driven to completion
		// here, so an accepted request is recognised by NOT being a 400.
		func() {
			defer func() { recover() }()
			h.IVCalculate(w, r)
		}()

		if c.want == http.StatusBadRequest && w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, w.Code)
		}
		if c.want == 0 && w.Code == http.StatusBadRequest {
			t.Errorf("%s: refused with 400 and body %s", c.name, w.Body.String())
		}
	}
}
