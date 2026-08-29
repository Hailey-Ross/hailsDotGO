package pogodata

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// loadFallbackCPMs reads the embedded table from disk, the way iv_test.go does,
// so a test failure points at the file rather than at package init.
func loadFallbackCPMs(t *testing.T) []cpMultiplierRow {
	t.Helper()
	raw, err := os.ReadFile("fallback/cp_multipliers.json")
	if err != nil {
		t.Fatalf("read fallback/cp_multipliers.json: %v", err)
	}
	var rows []cpMultiplierRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("parse fallback/cp_multipliers.json: %v", err)
	}
	return rows
}

func encodeCPMs(t *testing.T, rows []cpMultiplierRow) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	return b
}

func decodeCPMs(t *testing.T, data json.RawMessage) []cpMultiplierRow {
	t.Helper()
	var rows []cpMultiplierRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("parse merged table: %v (%s)", err, string(data))
	}
	return rows
}

func cpmByLevel(rows []cpMultiplierRow) map[float64]float64 {
	m := make(map[float64]float64, len(rows))
	for _, r := range rows {
		m[r.Level] = r.Multiplier
	}
	return m
}

// truncateCPMs models what PoGoAPI actually serves: the same rows, cut off at 45.
func truncateCPMs(rows []cpMultiplierRow, max float64) []cpMultiplierRow {
	var out []cpMultiplierRow
	for _, r := range rows {
		if r.Level <= max {
			out = append(out, r)
		}
	}
	return out
}

// The embedded table is the trustworthy one, so assert what it must contain
// before any test leans on it.
func TestFallbackCPMultipliersAreComplete(t *testing.T) {
	rows := loadFallbackCPMs(t)
	if got := maxCPMLevel(rows); got != 51.0 {
		t.Errorf("embedded table stops at level %v, want 51 (the Best Buddy ceiling)", got)
	}
	if len(rows) != 101 {
		t.Errorf("embedded table has %d rows, want 101 (levels 1 to 51 in halves)", len(rows))
	}
	// Niantic derives a half level from its whole-level neighbours as
	// sqrt((cpm(L)^2 + cpm(L+1)^2) / 2). The XL half levels shipped rounded to four
	// decimals, which is a linear interpolation in disguise; they were corrected on
	// 2026-08-28. handlers.cpmLookup re-derives these for the website, but everything
	// served the raw blob (the mobile API) gets exactly what this file says.
	m := cpmByLevel(rows)
	for lvl := 1.5; lvl <= 50.5; lvl += 1.0 {
		lo, okLo := m[lvl-0.5]
		hi, okHi := m[lvl+0.5]
		if !okLo || !okHi {
			t.Fatalf("level %v has no whole-level neighbours", lvl)
		}
		want := math.Sqrt((lo*lo + hi*hi) / 2)
		if diff := m[lvl] - want; diff > 1e-7 || diff < -1e-7 {
			t.Errorf("CPM(%.1f) = %.9f, want %.9f (sqrt of the neighbours' mean square)", lvl, m[lvl], want)
		}
	}
}

// The bug: PoGoAPI's cp_multiplier.json stops at level 45.0, and applying it used
// to replace the complete embedded table wholesale.
func TestMergeCPMultipliersRestoresTruncatedUpstream(t *testing.T) {
	full := loadFallbackCPMs(t)
	truncated := truncateCPMs(full, 45.0)
	if got := maxCPMLevel(truncated); got != 45.0 {
		t.Fatalf("fixture max level = %v, want 45", got)
	}

	merged := decodeCPMs(t, mergeCPMultipliers(encodeCPMs(t, truncated)))
	if got := maxCPMLevel(merged); got != 51.0 {
		t.Errorf("merged table stops at level %v, want 51", got)
	}
	if len(merged) != len(full) {
		t.Errorf("merged table has %d rows, want %d", len(merged), len(full))
	}
	m := cpmByLevel(merged)
	for _, r := range full {
		if got, ok := m[r.Level]; !ok {
			t.Errorf("level %v missing from the merged table", r.Level)
		} else if got != r.Multiplier {
			t.Errorf("CPM(%v) = %v, want %v", r.Level, got, r.Multiplier)
		}
	}
}

// The two levels the truncation actually cost a trainer: a Best Buddy maxed
// Pokemon. Reference figures from .claude/todo_trainer_level_formula.md.
func TestMergeCPMultipliersRestoresTopLevels(t *testing.T) {
	merged := decodeCPMs(t, mergeCPMultipliers(encodeCPMs(t, truncateCPMs(loadFallbackCPMs(t), 45.0))))
	m := cpmByLevel(merged)
	for _, tc := range []struct {
		level float64
		want  float64
	}{
		{50.0, 0.84029999},
		{50.5, 0.84280371}, // sqrt-derived: 0.842803698, within the tolerance below
		{51.0, 0.84529999},
	} {
		got, ok := m[tc.level]
		if !ok {
			t.Errorf("level %v missing after a truncated refresh", tc.level)
			continue
		}
		if diff := got - tc.want; diff > 1e-7 || diff < -1e-7 {
			t.Errorf("CPM(%v) = %.9f, want ~%.8f", tc.level, got, tc.want)
		}
	}
}

// A healthy upstream must still win outright: the merge corrects omissions, it does
// not overrule the feed.
func TestMergeCPMultipliersPassesCompleteUpstreamThrough(t *testing.T) {
	full := loadFallbackCPMs(t)
	upstream := make([]cpMultiplierRow, len(full))
	for i, r := range full {
		upstream[i] = cpMultiplierRow{Level: r.Level, Multiplier: r.Multiplier + 0.001} // visibly different
	}

	merged := decodeCPMs(t, mergeCPMultipliers(encodeCPMs(t, upstream)))
	if len(merged) != len(upstream) {
		t.Fatalf("merged table has %d rows, want %d", len(merged), len(upstream))
	}
	for i, r := range merged {
		if r.Level != upstream[i].Level || r.Multiplier != upstream[i].Multiplier {
			t.Fatalf("row %d = %+v, want %+v (upstream must pass through unchanged)", i, r, upstream[i])
		}
	}
}

// If the game ever gains a level, the feed carrying it must not be trimmed back to
// what the binary was built with.
func TestMergeCPMultipliersKeepsLevelsTheFallbackLacks(t *testing.T) {
	upstream := append(truncateCPMs(loadFallbackCPMs(t), 45.0),
		cpMultiplierRow{Level: 51.5, Multiplier: 0.8478},
		cpMultiplierRow{Level: 52.0, Multiplier: 0.85029999},
	)

	merged := decodeCPMs(t, mergeCPMultipliers(encodeCPMs(t, upstream)))
	m := cpmByLevel(merged)
	if got, ok := m[52.0]; !ok || got != 0.85029999 {
		t.Errorf("level 52 = %v (present=%v), want 0.85029999 preserved from upstream", got, ok)
	}
	if got, ok := m[51.5]; !ok || got != 0.8478 {
		t.Errorf("level 51.5 = %v (present=%v), want 0.8478 preserved from upstream", got, ok)
	}
	if got := maxCPMLevel(merged); got != 52.0 {
		t.Errorf("merged max level = %v, want 52", got)
	}
}

// Levels arrive as ints and floats for the same value ("level": 45 next to
// "level": 45.0). Keying on raw JSON or on unnormalised floats would duplicate them.
func TestMergeCPMultipliersSortedAndDeduplicated(t *testing.T) {
	// Hand-built payload: an integer-encoded level that the fallback stores as a
	// float, a duplicate row, and deliberately reversed order.
	upstream := json.RawMessage(`[
		{"level": 45, "multiplier": 0.81529999},
		{"level": 20, "multiplier": 0.5974000096321106},
		{"level": 1, "multiplier": 0.09399999678134918},
		{"level": 20.0, "multiplier": 0.5974000096321106}
	]`)

	merged := decodeCPMs(t, mergeCPMultipliers(upstream))
	seen := make(map[float64]bool, len(merged))
	for i, r := range merged {
		if i > 0 && merged[i-1].Level >= r.Level {
			t.Fatalf("row %d level %v follows %v: the table must be sorted ascending", i, r.Level, merged[i-1].Level)
		}
		if seen[r.Level] {
			t.Fatalf("level %v appears twice", r.Level)
		}
		seen[r.Level] = true
	}
	if len(merged) != len(loadFallbackCPMs(t)) {
		t.Errorf("merged table has %d rows, want %d: the int-encoded 45 must collide with the fallback's 45.0",
			len(merged), len(loadFallbackCPMs(t)))
	}
	if got := maxCPMLevel(merged); got != 51.0 {
		t.Errorf("merged table stops at %v, want 51", got)
	}
}

// A payload that cannot be used at all is worse than no refresh, so the embedded
// table is served instead of letting the store hold junk.
func TestMergeCPMultipliersRejectsUnusablePayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"garbage":     `not json`,
		"null":        `null`,
		"empty array": `[]`,
		"wrong shape": `{"1": 0.094}`,
		"zeroed rows": `[{"level": 0, "multiplier": 0}, {"level": 40, "multiplier": 0}]`,
	} {
		merged := decodeCPMs(t, mergeCPMultipliers(json.RawMessage(payload)))
		if got := maxCPMLevel(merged); got != 51.0 {
			t.Errorf("%s: merged table stops at level %v, want the embedded table's 51", name, got)
		}
	}
}

// The real path: whatever reaches applyResult (embedded fallback at boot, disk
// cache, the six hour refresh, the admin scraper check) goes through the merge.
func TestApplyResultCannotTruncateCPMultipliers(t *testing.T) {
	s := &Store{}
	s.applyResult("cp_multipliers", encodeCPMs(t, loadFallbackCPMs(t)))
	if got := maxCPMLevel(decodeCPMs(t, s.cpMults)); got != 51.0 {
		t.Fatalf("after the fallback load the store stops at level %v, want 51", got)
	}
	// Now the refresh lands with upstream's short table, which is what shipped the bug.
	s.applyResult("cp_multipliers", encodeCPMs(t, truncateCPMs(loadFallbackCPMs(t), 45.0)))
	if got := maxCPMLevel(decodeCPMs(t, s.cpMults)); got != 51.0 {
		t.Fatalf("a refresh degraded the store to level %v, want 51", got)
	}
	// raidschedule.go reads levels 20 and 25 straight out of this blob, with
	// per-level constants behind it. The merge must leave those rows where it
	// found them rather than shadow them with the constants.
	if got := raidCPMsFrom(s.cpMults); got != defaultRaidCPMs {
		t.Errorf("raidCPMsFrom(merged) = %+v, want %+v", got, defaultRaidCPMs)
	}
}
