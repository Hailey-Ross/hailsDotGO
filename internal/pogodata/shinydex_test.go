package pogodata

import (
	"encoding/json"
	"strconv"
	"testing"
)

// withBaseline swaps the package level baseline for one test and restores it after. The baseline is
// embedded at init, so exercising the merge rules against a known tiny table is the only way to
// assert them without depending on the shipped data.
func withBaseline(t *testing.T, m map[int]BaselineSpecies) {
	t.Helper()
	prev := shinyBaseline
	shinyBaseline = m
	t.Cleanup(func() { shinyBaseline = prev })
}

func boolPtr(b bool) *bool { return &b }

// applyShinies mirrors what every real caller does: applyResult documents "caller must hold s.mu",
// and a test that ignores that is not exercising the code path the service runs.
func applyShinies(s *Store, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyResult("shinies", json.RawMessage(body))
}

// tinyBaseline: one released, one in GO awaiting a shiny, one not in GO at all.
func tinyBaseline() map[int]BaselineSpecies {
	return map[int]BaselineSpecies{
		1:  {ID: 1, Name: "Bulbasaur", InGo: true, ShinyReleased: true},
		2:  {ID: 2, Name: "Ivysaur", InGo: true, ShinyReleased: false},
		3:  {ID: 3, Name: "Venusaur", InGo: false, ShinyReleased: false},
	}
}

func dexTableOf(t *testing.T, s *Store) map[string]BaselineSpecies {
	t.Helper()
	var m map[string]BaselineSpecies
	if len(s.shinyDex) == 0 {
		return m
	}
	if err := json.Unmarshal(s.shinyDex, &m); err != nil {
		t.Fatalf("shinyDex is not valid JSON: %v", err)
	}
	return m
}

func releasedKeys(t *testing.T, s *Store) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if len(s.shinies) == 0 {
		return m
	}
	if err := json.Unmarshal(s.shinies, &m); err != nil {
		t.Fatalf("shinies is not valid JSON: %v", err)
	}
	return m
}

// The whole point of the feature: the baseline decides, not "is this a key upstream".
func TestBaselineDecidesReleased(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{}`)

	// Note: the real embedded supplement always merges into upstream, so the served map also
	// carries those entries. Assert on the baseline's own dex numbers rather than a total.
	rel := releasedKeys(t, s)
	if _, ok := rel["1"]; !ok {
		t.Error("dex 1 is shiny_released in the baseline but is missing from the served shinies map")
	}
	if _, ok := rel["2"]; ok {
		t.Error("dex 2 is not shiny_released but was served in the shinies map")
	}
	if _, ok := rel["3"]; ok {
		t.Error("dex 3 is not in GO at all but was served in the shinies map")
	}

	// The real embedded supplement merges in too, so assert on the baseline's own dex numbers.
	dex := dexTableOf(t, s)
	for _, want := range []string{"1", "2", "3"} {
		if _, ok := dex[want]; !ok {
			t.Errorf("shinyDex is missing baseline species %s", want)
		}
	}
	if dex["3"].InGo {
		t.Error("dex 3 should be in_go false")
	}
	if !dex["2"].InGo {
		t.Error("dex 2 should be in_go true with shiny_released false")
	}
}

// Upstream is additive: it can turn a shiny on, never off.
func TestUpstreamUnionAddsShiny(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{"2":{"id":2,"name":"Ivysaur","found_wild":true}}`)

	dex := dexTableOf(t, s)
	if !dex["2"].ShinyReleased {
		t.Error("upstream lists dex 2 as shiny, so the union should have turned shiny_released on")
	}
	if !dex["2"].InGo {
		t.Error("a released shiny must imply in_go")
	}
	rel := releasedKeys(t, s)
	if _, ok := rel["2"]; !ok {
		t.Fatal("dex 2 should now be served")
	}
	// The upstream object must survive verbatim: it is the only source of the found_* method flags.
	var entry map[string]any
	if err := json.Unmarshal(rel["2"], &entry); err != nil {
		t.Fatalf("entry 2 is not valid JSON: %v", err)
	}
	if entry["found_wild"] != true {
		t.Errorf("found_wild was dropped from the upstream entry: %v", entry)
	}
}

// A baseline species upstream has never heard of still gets served, with a usable minimal entry.
func TestBaselineOnlySpeciesIsSynthesised(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{}`)

	var entry map[string]any
	if err := json.Unmarshal(releasedKeys(t, s)["1"], &entry); err != nil {
		t.Fatalf("entry 1 is not valid JSON: %v", err)
	}
	if entry["name"] != "Bulbasaur" {
		t.Errorf("synthesised entry has name %v, want Bulbasaur", entry["name"])
	}
	if entry["id"] != float64(1) {
		t.Errorf("synthesised entry has id %v, want 1", entry["id"])
	}
}

// The regression test for the entire feature: a refresh must never drop an admin override.
func TestOverridesSurviveRefresh(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{}`)

	// Admin turns dex 2 on (a shiny released before our sources noticed) and dex 1 off.
	s.SetShinyOverrides([]ShinyOverride{
		{Dex: 2, ShinyReleased: boolPtr(true)},
		{Dex: 1, ShinyReleased: boolPtr(false)},
	})
	if dex := dexTableOf(t, s); !dex["2"].ShinyReleased || dex["1"].ShinyReleased {
		t.Fatalf("overrides did not apply: %+v", dex)
	}

	// Now a six hour refresh lands a completely fresh upstream blob.
	applyShinies(s, `{"1":{"id":1,"name":"Bulbasaur"},"3":{"id":3,"name":"Venusaur"}}`)

	dex := dexTableOf(t, s)
	if !dex["2"].ShinyReleased {
		t.Error("the dex 2 override was lost across a refresh")
	}
	if dex["1"].ShinyReleased {
		t.Error("the dex 1 override lost to upstream: an explicit false must beat upstream presence")
	}
	if _, ok := releasedKeys(t, s)["1"]; ok {
		t.Error("dex 1 is overridden off but is still served in the shinies map")
	}
}

// in_go is overridable on its own, and a released shiny always forces in_go true.
func TestInGoOverrideAndImplication(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{}`)

	s.SetShinyOverrides([]ShinyOverride{{Dex: 3, InGo: boolPtr(true)}})
	if dex := dexTableOf(t, s); !dex["3"].InGo || dex["3"].ShinyReleased {
		t.Errorf("in_go should be overridable without touching shiny_released: %+v", dex["3"])
	}

	s.SetShinyOverrides([]ShinyOverride{{Dex: 3, ShinyReleased: boolPtr(true), InGo: boolPtr(false)}})
	if dex := dexTableOf(t, s); !dex["3"].InGo {
		t.Error("a released shiny must force in_go true even when the override says otherwise")
	}
}

// A null or absent upstream body must yield the baseline, not a panic and not an empty page.
func TestRebuildNullUpstream(t *testing.T) {
	withBaseline(t, tinyBaseline())
	for _, body := range []string{`null`, `{}`, ``} {
		s := &Store{}
		applyShinies(s, body)
		dex := dexTableOf(t, s)
		for _, want := range []string{"1", "2", "3"} {
			if _, ok := dex[want]; !ok {
				t.Errorf("upstream %q: shinyDex should still carry baseline species %s", body, want)
			}
		}
		if _, ok := releasedKeys(t, s)["1"]; !ok {
			t.Errorf("upstream %q: the baseline released species should still be served", body)
		}
	}
}

// With no baseline embedded the store must behave exactly as it did before this feature existed.
func TestNoBaselineIsPassThrough(t *testing.T) {
	withBaseline(t, nil)
	s := &Store{}
	applyShinies(s, `{"7":{"id":7,"name":"Squirtle"}}`)

	if _, ok := releasedKeys(t, s)["7"]; !ok {
		t.Error("without a baseline the upstream map should be served unchanged")
	}
	if len(s.shinyDex) != 0 {
		t.Error("without a baseline there is no dex table to serve")
	}
}

// Nothing in the served shinies map may be flagged unreleased: it is the released-only view every
// existing client (including the mobile app) already reads.
func TestShiniesViewIsReleasedOnly(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{"3":{"id":3,"name":"Venusaur"}}`)

	dex := dexTableOf(t, s)
	for key := range releasedKeys(t, s) {
		if entry, ok := dex[key]; ok && !entry.ShinyReleased {
			t.Errorf("dex %s is served in shinies but is flagged shiny_released false", key)
		}
	}
}

// ShinyEffectiveDefaults must report the baseline UNIONED WITH UPSTREAM, never the baseline alone.
//
// This is what the admin write path compares against to decide whether an edit is a real override
// or a no-op it should drop. Get it wrong and unticking a shiny that only upstream provides looks
// like a no-op, the row is deleted, the union turns it straight back on, and the admin's decision
// disappears with no error.
func TestEffectiveDefaultsIncludeUpstream(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{"2":{"id":2,"name":"Ivysaur"}}`)

	inGo, shiny, ok := s.ShinyEffectiveDefaults(2)
	if !ok {
		t.Fatal("dex 2 is in the baseline but ShinyEffectiveDefaults said otherwise")
	}
	if !shiny {
		t.Error("dex 2 is false in the baseline but present upstream, so its effective default is released")
	}
	if !inGo {
		t.Error("a released shiny implies in_go in the effective default too")
	}

	// A species neither source lists keeps the baseline's answer.
	if _, shiny, _ := s.ShinyEffectiveDefaults(3); shiny {
		t.Error("dex 3 is in neither source, so its effective default must stay false")
	}
	// Overrides must NOT leak in: this reports what the app would do with no override at all.
	s.SetShinyOverrides([]ShinyOverride{{Dex: 2, ShinyReleased: boolPtr(false)}})
	if _, shiny, _ := s.ShinyEffectiveDefaults(2); !shiny {
		t.Error("ShinyEffectiveDefaults must ignore overrides; it is the baseline the override is measured against")
	}
	if dex := dexTableOf(t, s); dex["2"].ShinyReleased {
		t.Error("the explicit false override should still beat upstream in the served data")
	}
	if _, _, ok := s.ShinyEffectiveDefaults(9999); ok {
		t.Error("an unknown dex must report ok=false")
	}
}

// A species upstream serves that the baseline has never heard of has to reach BOTH blobs. It used
// to land only in shinies, and since the client now builds its cards from the dex table, that made
// it served and invisible: the same silent omission this whole feature removes.
func TestUnknownUpstreamSpeciesReachesDexTable(t *testing.T) {
	withBaseline(t, tinyBaseline())
	s := &Store{}
	applyShinies(s, `{"1030":{"id":1030,"name":"Futuremon"}}`)

	if _, ok := releasedKeys(t, s)["1030"]; !ok {
		t.Error("an upstream species the baseline lacks should still be served in shinies")
	}
	entry, ok := dexTableOf(t, s)["1030"]
	if !ok {
		t.Fatal("an upstream species the baseline lacks is missing from shinyDex, so it would render no card")
	}
	if entry.Name != "Futuremon" || !entry.ShinyReleased || !entry.InGo {
		t.Errorf("dex table entry for an upstream-only species is wrong: %+v", entry)
	}
}

func TestGenForDex(t *testing.T) {
	cases := map[int]int{1: 1, 151: 1, 152: 2, 251: 2, 252: 3, 386: 3, 387: 4, 493: 4,
		494: 5, 649: 5, 650: 6, 721: 6, 722: 7, 809: 7, 810: 8, 905: 8, 906: 9, 1025: 9,
		0: 0, 1026: 0, -1: 0}
	for dex, want := range cases {
		if got := GenForDex(dex); got != want {
			t.Errorf("GenForDex(%d) = %d, want %d", dex, got, want)
		}
	}
}

// ---------------------------------------------------------------- the shipped baseline

// TestShinyBaselineIntegrity guards the generated data file itself. It is the reason a bad
// regeneration cannot reach users: names must match the stats feed exactly (user_shinies stores
// the NAME, so a drifted name orphans real collection rows), and the released set can never shrink
// below what the app already shows.
func TestShinyBaselineIntegrity(t *testing.T) {
	raw, err := fallbackFS.ReadFile("fallback/shiny_baseline.json")
	if err != nil {
		t.Fatalf("the shiny baseline is missing: %v", err)
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		t.Fatal("shiny_baseline.json starts with a UTF-8 BOM; Go's JSON parser rejects it")
	}

	var table map[string]BaselineSpecies
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("shiny_baseline.json does not parse: %v", err)
	}

	const wantSpecies = 1025
	if len(table) != wantSpecies {
		t.Errorf("baseline has %d entries, want %d (the full National Dex)", len(table), wantSpecies)
	}
	for dex := 1; dex <= wantSpecies; dex++ {
		key := strconv.Itoa(dex)
		e, ok := table[key]
		if !ok {
			t.Errorf("baseline is missing dex %d", dex)
			continue
		}
		if e.ID != dex {
			t.Errorf("baseline key %s holds id %d", key, e.ID)
		}
		if e.Name == "" {
			t.Errorf("baseline dex %d has an empty name", dex)
		}
		if e.ShinyReleased && !e.InGo {
			t.Errorf("baseline dex %d (%s) is shiny_released but not in_go", dex, e.Name)
		}
	}

	// Names must match the PoGoAPI stats feed byte for byte, curly apostrophes and all, because
	// that is the map PokemonDexID resolves against and what user_shinies rows already store.
	statsRaw, err := fallbackFS.ReadFile("fallback/pokemon.json")
	if err != nil {
		t.Fatalf("pokemon.json fallback missing: %v", err)
	}
	var stats []struct {
		Name string `json:"pokemon_name"`
		ID   int    `json:"pokemon_id"`
	}
	if err := json.Unmarshal(statsRaw, &stats); err != nil {
		t.Fatalf("pokemon.json does not parse: %v", err)
	}
	statName := make(map[int]string, len(stats))
	for _, e := range stats {
		if _, seen := statName[e.ID]; !seen {
			statName[e.ID] = e.Name
		}
	}
	// National Dex species the stats feed has no row for. It only carries species with GO stats,
	// and the baseline spans the whole National Dex, so a gap here is expected rather than wrong.
	knownStatsGaps := map[int]string{902: "Basculegion"}
	for dex, e := range table {
		id, _ := strconv.Atoi(dex)
		want, ok := statName[id]
		if !ok {
			if gap, isGap := knownStatsGaps[id]; isGap {
				if e.Name != gap {
					t.Errorf("dex %d: name %q, want the documented exception %q", id, e.Name, gap)
				}
				continue
			}
			t.Errorf("dex %d (%s) is not in pokemon.json and is not a documented exception", id, e.Name)
			continue
		}
		if e.Name != want {
			t.Errorf("dex %d: baseline name %q does not match pokemon.json %q", id, e.Name, want)
		}
	}

	// The released set must be a superset of everything the app can already show, so a
	// regeneration can never silently remove a species a user is currently collecting.
	for _, file := range []string{"fallback/shinies.json", "fallback/shinies_supplement.json"} {
		blob, err := fallbackFS.ReadFile(file)
		if err != nil {
			t.Fatalf("%s missing: %v", file, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(blob, &m); err != nil {
			t.Fatalf("%s does not parse: %v", file, err)
		}
		for key := range m {
			e, ok := table[key]
			if !ok {
				t.Errorf("%s lists dex %s but the baseline does not have it at all", file, key)
				continue
			}
			if !e.ShinyReleased {
				t.Errorf("%s lists dex %s (%s) as shiny, but the baseline says shiny_released false", file, key, e.Name)
			}
		}
	}

	// The bug that started all of this.
	if e := table["791"]; !e.ShinyReleased {
		t.Errorf("dex 791 Solgaleo must be shiny_released: that is the reported bug (got %+v)", e)
	}
}
