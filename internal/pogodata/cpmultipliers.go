package pogodata

import (
	"bytes"
	"encoding/json"
	"log"
	"math"
	"sort"
)

// The CP multiplier table is the one dataset where a successful refresh can leave
// the store strictly worse off than a failed one. PoGoAPI's cp_multiplier.json is
// itself truncated: 89 rows ending at level 45.0 (verified 2026-08-28), while the
// game reaches 50, or 51 for a Best Buddy, and the embedded fallback carries all
// 101 rows. applyResult used to assign the blob wholesale, so the first successful
// refresh after boot replaced a complete table with a short one. Every consumer
// that iterates the rows rather than extrapolating from them - the mobile app's IV
// solver reached through /api/mobile/v1/data among them - then found no candidate
// at all above level 45 and computed max CP about 6% low.
//
// This is a different mechanism from raidCPMsFrom in raidschedule.go, and does not
// replace it. That one reads two fixed levels (20 and 25) out of the live blob with
// per-level constants to fall back on, guarding the raid card synthesizer against a
// blob that is missing or has not loaded yet. This guards the blob itself. Merging
// here only means raidCPMsFrom always finds the two levels it looks for.
//
// The site's own handlers already compensate at read time (cpmLookup in
// internal/handlers/iv.go extends the table to 51 and re-derives the XL half
// levels), which is why the truncation never showed on the website. Everything
// served the raw blob was on its own.

// cpMultiplierFallback is the embedded table. cpMultiplierRow is declared in
// raidschedule.go, which reads the same shape.
var cpMultiplierFallback = loadCPMultiplierFallback()

// cpmHalfKey normalises a level to a whole-number key. Levels only ever move in
// halves, and the JSON carries ints and floats for the same value ("level": 1 next
// to "level": 45.0), so keying on the raw JSON text or comparing floats directly
// would treat equal levels as different ones and duplicate them in the merge.
func cpmHalfKey(level float64) int { return int(math.Round(level * 2)) }

func loadCPMultiplierFallback() []cpMultiplierRow {
	data, err := fallbackFS.ReadFile("fallback/cp_multipliers.json")
	if err != nil {
		log.Printf("pogodata: cp_multipliers: embedded fallback missing: %v", err)
		return nil
	}
	rows, err := parseCPMultipliers(data)
	if err != nil {
		log.Printf("pogodata: cp_multipliers: embedded fallback parse error: %v", err)
		return nil
	}
	return rows
}

// parseCPMultipliers decodes the array shape both PoGoAPI and the embedded file
// use, dropping rows that carry no usable level or multiplier so a junk row can
// never shadow a good fallback one.
func parseCPMultipliers(data json.RawMessage) ([]cpMultiplierRow, error) {
	var rows []cpMultiplierRow
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &rows); err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Level <= 0 || r.Multiplier <= 0 {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// maxCPMLevel reports the highest level in a table, or 0 for an empty one.
func maxCPMLevel(rows []cpMultiplierRow) float64 {
	max := 0.0
	for _, r := range rows {
		if r.Level > max {
			max = r.Level
		}
	}
	return max
}

// mergeCPMultipliers overlays an incoming table onto the embedded one. Upstream
// wins for every level it actually carries, so a genuine correction still lands,
// and any level only the embedded table knows is kept rather than dropped. The
// result is sorted by level with one row per level.
//
// An incoming table that stops short of the embedded one is a regression by
// definition, so it is logged loudly. Silent acceptance is what let the truncation
// ship in the first place.
func mergeCPMultipliers(data json.RawMessage) json.RawMessage {
	if len(cpMultiplierFallback) == 0 {
		return data // nothing trustworthy to merge against
	}
	rows, err := parseCPMultipliers(data)
	if err != nil || len(rows) == 0 {
		log.Printf("pogodata: cp_multipliers: unusable payload (err=%v, %d usable rows), serving the embedded table", err, len(rows))
		return marshalCPMultipliers(cpMultiplierFallback, data)
	}

	byLevel := make(map[int]cpMultiplierRow, len(rows)+len(cpMultiplierFallback))
	for _, r := range rows {
		byLevel[cpmHalfKey(r.Level)] = r
	}
	restored := 0
	for _, r := range cpMultiplierFallback {
		if _, ok := byLevel[cpmHalfKey(r.Level)]; ok {
			continue
		}
		byLevel[cpmHalfKey(r.Level)] = r
		restored++
	}

	incomingMax, embeddedMax := maxCPMLevel(rows), maxCPMLevel(cpMultiplierFallback)
	switch {
	case incomingMax < embeddedMax:
		log.Printf("pogodata: cp_multipliers: REGRESSION: upstream table stops at level %g but the game reaches %g; restored %d row(s) from the embedded table",
			incomingMax, embeddedMax, restored)
	case restored > 0:
		log.Printf("pogodata: cp_multipliers: restored %d level(s) upstream did not carry", restored)
	}

	merged := make([]cpMultiplierRow, 0, len(byLevel))
	for _, r := range byLevel {
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Level < merged[j].Level })
	return marshalCPMultipliers(merged, data)
}

// marshalCPMultipliers re-encodes the merged table, falling back to the bytes we
// were handed if that somehow fails: a short table still beats no table.
func marshalCPMultipliers(rows []cpMultiplierRow, orig json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(rows)
	if err != nil {
		log.Printf("pogodata: cp_multipliers: marshal error, serving the payload unmerged: %v", err)
		return orig
	}
	return encoded
}
