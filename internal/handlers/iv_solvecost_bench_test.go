package handlers

import "testing"

func benchCPMs() []cpmEntry {
	// Mirrors the embedded table's shape: whole + half levels to 45, which is
	// what live upstream supplies; cpmLookup extends the rest.
	var out []cpmEntry
	m := 0.094
	for lvl := 1.0; lvl <= 45.0; lvl += 0.5 {
		out = append(out, cpmEntry{Level: lvl, Multiplier: m})
		m += 0.008
	}
	return out
}

var benchPoke = pokemonStatEntry{
	BaseAttack: 270, BaseDefense: 228, BaseStamina: 205,
	PokemonName: "Groudon", Form: "Normal",
}

func benchRun(b *testing.B, req ivRequest, ranges []levelRange) {
	cpms := benchCPMs()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enumerateWithBuddyRetry(req, ranges, benchPoke, cpms)
	}
}

// Worst case an attacker can ask for over JSON: a CP with no readable dust, no
// appraisal constraint, the highest trainer level, and an HP no spread can
// satisfy, so the buddy retry also runs its full sweep.
func BenchmarkSolveWorstCase(b *testing.B) {
	req := ivRequest{CP: 1, HP: 999, TrainerLevel: 51}
	ranges := []levelRange{{MinLvl: 1, MaxLvl: maxPowerUpLevel(51)}}
	benchRun(b, req, ranges)
}

// Same sweep, but three stars, which is the tightest appraisal filter.
func BenchmarkSolveWorstCaseThreeStars(b *testing.B) {
	bars := 3
	req := ivRequest{CP: 1, HP: 999, TrainerLevel: 51, AppraisalBars: &bars}
	ranges := []levelRange{{MinLvl: 1, MaxLvl: maxPowerUpLevel(51)}}
	benchRun(b, req, ranges)
}

// A normal scan: CP plus a readable dust bracket.
func BenchmarkSolveTypical(b *testing.B) {
	req := ivRequest{CP: 4027, HP: 171, DustCost: 10000, TrainerLevel: 44}
	ranges := dustCandidates(10000, nil, nil, nil)
	benchRun(b, req, ranges)
}

// CP-free arc scan: the case the plan named as the expensive one.
func BenchmarkSolveArcOnly(b *testing.B) {
	req := ivRequest{CP: 0, HP: 171, TrainerLevel: 44}
	ranges := intersectRangesWithLevel(nil, 39.0, 0.5)
	benchRun(b, req, ranges)
}

// Full sweep with an HP that many spreads satisfy, so the candidate slice is
// built and sorted rather than staying empty.
func BenchmarkSolveWideResults(b *testing.B) {
	req := ivRequest{CP: 0, HP: 171, TrainerLevel: 51}
	ranges := []levelRange{{MinLvl: 1, MaxLvl: maxPowerUpLevel(51)}}
	benchRun(b, req, ranges)
}
