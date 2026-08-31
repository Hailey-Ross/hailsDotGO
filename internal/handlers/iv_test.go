package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
