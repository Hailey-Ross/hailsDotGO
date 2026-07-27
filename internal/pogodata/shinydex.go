package pogodata

// The shiny dex: which species exist in Pokemon GO, and which of them have had a shiny released.
//
// Before this existed, "is a shiny available" was answered by "is this dex a key in the PoGoAPI
// shinies map". That made every shiny release a code change plus a deploy, and PoGoAPI stopped
// adding new releases in early 2026, so the list quietly fell 141 species behind. Now the full
// National Dex ships as an embedded baseline carrying two flags, and an admin can correct either
// one from the admin panel without a rebuild.
//
// Precedence, decided once and documented only here:
//
//	admin override  >  (baseline OR upstream presence)
//
// Upstream is ADDITIVE: a species PoGoAPI lists can turn shiny_released on, but a species it omits
// can never turn it off. The 2026-07-04 audit found zero false positives in 863 upstream entries
// and 21 false negatives, so upstream-true is trustworthy and upstream-absence is not. The union
// means that if PoGoAPI ever resumes, new releases self heal with no admin action.
//
// One deliberate asymmetry, so nobody has to rediscover it: an explicit override wins in both
// directions for shiny_released, but NOT for in_go. A released shiny forces in_go true at the end
// of effectiveFlags, because a species you can catch a shiny of is self evidently in the game, and
// a card with no species behind it is nonsense. The write path refuses to store that combination
// in the first place, so the two layers agree; this is the backstop for a row written before the
// shiny existed.
//
// Everything here is recomputed by rebuildShinyLocked, which applyResult calls on every shinies
// apply. That is the single choke point: embedded fallback, disk cache, the six hour refresh and
// the admin scraper check all pass through it, so a refresh can never drop an override.

import (
	"bytes"
	"encoding/json"
	"log"
	"sort"
	"strconv"
)

// BaselineSpecies is one row of the embedded National Dex truth table.
type BaselineSpecies struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	InGo          bool   `json:"in_go"`
	ShinyReleased bool   `json:"shiny_released"`
}

// shinyBaseline is the embedded table, dex -> row. Empty is a supported state: the store then
// serves the upstream shinies map unchanged, which is exactly the behaviour that shipped before
// this file existed. That keeps a bad or missing baseline from taking the page down.
var shinyBaseline = loadShinyBaseline()

func loadShinyBaseline() map[int]BaselineSpecies {
	data, err := fallbackFS.ReadFile("fallback/shiny_baseline.json")
	if err != nil {
		log.Printf("pogodata: shiny baseline: %v (serving the upstream shiny list unchanged)", err)
		return nil
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // Go's JSON parser rejects a BOM
	var raw map[string]BaselineSpecies
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("pogodata: shiny baseline: parse error: %v (serving the upstream shiny list unchanged)", err)
		return nil
	}
	out := make(map[int]BaselineSpecies, len(raw))
	for _, e := range raw {
		if e.ID > 0 {
			out[e.ID] = e
		}
	}
	return out
}

// ShinyOverride is one admin correction. A nil pointer means "no opinion, keep the default", which
// is how a row can override in_go without touching shiny_released. Region "" is the species row;
// any other value is a regional or alternate form, keyed the way user_shinies.region is.
type ShinyOverride struct {
	Dex           int
	Region        string
	InGo          *bool
	ShinyReleased *bool
}

type shinyOverrideKey struct {
	Dex    int
	Region string
}

// AdminShinySpecies is one row of the admin editor: the effective flags, the defaults they came
// from, and why. The admin panel has to be able to answer "why is this species on?" without a
// second request, so provenance travels with the value.
type AdminShinySpecies struct {
	Dex                  int    `json:"dex"`
	Name                 string `json:"name"`
	Gen                  int    `json:"gen"`
	InGo                 bool   `json:"in_go"`
	ShinyReleased        bool   `json:"shiny_released"`
	DefaultInGo          bool   `json:"default_in_go"`
	DefaultShinyReleased bool   `json:"default_shiny_released"`
	UpstreamShiny        bool   `json:"upstream_shiny"`
	Overridden           bool   `json:"overridden"`
}

// SetShinyOverrides replaces the admin override set and recomputes the served blobs. The handlers
// layer owns the table and calls this at startup and after every write; a few hundred rows make a
// full replace cheaper and far easier to reason about than incremental invalidation.
func (s *Store) SetShinyOverrides(list []ShinyOverride) {
	m := make(map[shinyOverrideKey]ShinyOverride, len(list))
	for _, ov := range list {
		m[shinyOverrideKey{Dex: ov.Dex, Region: ov.Region}] = ov
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shinyOverrides = m
	s.rebuildShinyLocked()
}

// SetRegionalShinyOverrides stores the sparse regional map the client overlays onto its compiled
// in defaults. The shape (species name -> region tag -> shiny_released) is opaque to this package:
// regional form identity lives in ts/shared/regionalForms.ts and internal/handlers/regional.go, and
// duplicating it here would be a third copy to keep in sync.
func (s *Store) SetRegionalShinyOverrides(raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regionalShiny = raw
}

// effectiveFlags resolves one species. Caller must hold s.mu.
func (s *Store) effectiveFlags(dex int, base BaselineSpecies, upstreamShiny bool) (inGo, shiny, overridden bool) {
	shiny = base.ShinyReleased || upstreamShiny
	inGo = base.InGo
	if ov, ok := s.shinyOverrides[shinyOverrideKey{Dex: dex}]; ok {
		if ov.ShinyReleased != nil {
			shiny = *ov.ShinyReleased
			overridden = true
		}
		if ov.InGo != nil {
			inGo = *ov.InGo
			overridden = true
		}
	}
	if shiny { // a released shiny implies the species is in the game
		inGo = true
	}
	return inGo, shiny, overridden
}

// rebuildShinyLocked recomputes s.shinies (released species only, the shape every existing client
// already consumes) and s.shinyDex (all 1025 with flags, the new sibling key). Caller must hold
// s.mu. Both blobs are marshalled once here, never per request.
//
// s.shinies deliberately stays released-only. It is a shipped contract: /api/data, /api/app/data,
// /api/private/data and the mobile app all read it, and widening it to the full dex would make
// every one of them render species that cannot be caught.
func (s *Store) rebuildShinyLocked() {
	if len(shinyBaseline) == 0 {
		// No baseline: behave exactly as the store did before this file existed.
		s.shinies = s.shinyUpstream
		s.shinyDex = nil
		return
	}

	var upstream map[string]json.RawMessage
	if len(s.shinyUpstream) > 0 {
		if err := json.Unmarshal(bytes.TrimPrefix(s.shinyUpstream, []byte{0xEF, 0xBB, 0xBF}), &upstream); err != nil {
			log.Printf("pogodata: shiny dex: upstream parse error, baseline only: %v", err)
		}
	}
	// A literal JSON null unmarshals without error but leaves a nil map, which is fine to range.

	// Remember which dex numbers upstream vouches for, so the admin write path and the drift check
	// can ask without re-parsing a 170 KB blob every time (the bulk endpoint asks 200 times).
	s.upstreamShinyDex = make(map[int]bool, len(upstream))
	for key := range upstream {
		if dex, err := strconv.Atoi(key); err == nil {
			s.upstreamShinyDex[dex] = true
		}
	}

	released := make(map[string]json.RawMessage, len(upstream)+16)
	dexTable := make(map[string]BaselineSpecies, len(shinyBaseline))

	for dex, base := range shinyBaseline {
		key := strconv.Itoa(dex)
		_, upstreamShiny := upstream[key]
		inGo, shiny, _ := s.effectiveFlags(dex, base, upstreamShiny)

		dexTable[key] = BaselineSpecies{ID: dex, Name: base.Name, InGo: inGo, ShinyReleased: shiny}
		if !shiny {
			continue
		}
		// Prefer the upstream object: it carries the found_* method flags, which nothing in the
		// baseline reproduces. Synthesise a minimal entry only where upstream has nothing.
		if entry, ok := upstream[key]; ok {
			released[key] = entry
			continue
		}
		if entry, err := json.Marshal(struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{ID: dex, Name: base.Name}); err == nil {
			released[key] = entry
		}
	}

	// Anything upstream lists that the baseline does not know about at all (a new generation
	// landing upstream before we regenerate the baseline) still gets served rather than dropped.
	// It has to land in BOTH blobs: the client builds its cards from the dex table now, so serving
	// it only in `shinies` would make it invisible, which is the exact silent omission this whole
	// change exists to remove.
	for key, entry := range upstream {
		if _, known := dexTable[key]; known {
			continue
		}
		released[key] = entry
		dex, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		var e struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(entry, &e) != nil || e.Name == "" {
			continue
		}
		if e.ID == 0 {
			e.ID = dex
		}
		dexTable[key] = BaselineSpecies{ID: e.ID, Name: e.Name, InGo: true, ShinyReleased: true}
	}

	if data, err := json.Marshal(released); err == nil {
		s.shinies = data
	} else {
		log.Printf("pogodata: shiny dex: marshal released: %v", err)
		s.shinies = s.shinyUpstream
	}
	if data, err := json.Marshal(dexTable); err == nil {
		s.shinyDex = data
	} else {
		log.Printf("pogodata: shiny dex: marshal dex table: %v", err)
		s.shinyDex = nil
	}
}

// ShinyDexAdmin returns every baseline species with its effective flags, the defaults behind them
// and whether an admin has overridden either, sorted by dex. The handlers layer joins the note and
// audit columns on top from the override table.
func (s *Store) ShinyDexAdmin() []AdminShinySpecies {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]AdminShinySpecies, 0, len(shinyBaseline))
	for dex, base := range shinyBaseline {
		upstreamShiny := s.upstreamShinyDex[dex]
		inGo, shiny, overridden := s.effectiveFlags(dex, base, upstreamShiny)
		out = append(out, AdminShinySpecies{
			Dex:                  dex,
			Name:                 base.Name,
			Gen:                  GenForDex(dex),
			InGo:                 inGo,
			ShinyReleased:        shiny,
			DefaultInGo:          base.InGo,
			DefaultShinyReleased: base.ShinyReleased,
			UpstreamShiny:        upstreamShiny,
			Overridden:           overridden,
		})
	}
	// Map iteration order is random and the admin table renders in dex order.
	sort.Slice(out, func(i, j int) bool { return out[i].Dex < out[j].Dex })
	return out
}

// ShinyEffectiveDefaults returns the flags a species would have with NO admin override: the
// baseline unioned with upstream presence.
//
// The admin write path must compare against this, not against the baseline alone. Upstream can
// turn a shiny on, so for a species the baseline says false and PoGoAPI lists, the effective
// default is already true. Comparing to the baseline would classify "admin unticks that box" as a
// no-op and delete the row, and the union would immediately turn it back on: the admin's decision
// would vanish with no error shown.
func (s *Store) ShinyEffectiveDefaults(dex int) (inGo, shinyReleased, ok bool) {
	base, ok := shinyBaseline[dex]
	if !ok {
		return false, false, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	shiny := base.ShinyReleased || s.upstreamShinyDex[dex]
	inGo = base.InGo || shiny
	return inGo, shiny, true
}

// ShinyBaselineName maps a dex to its English species name, or "" if the baseline does not have it.
// The override table is keyed by dex, so the name has to be recovered somewhere to reach the client.
func (s *Store) ShinyBaselineName(dex int) string { return shinyBaseline[dex].Name }

// ShinyBaselineSize reports how many species the embedded baseline carries. Zero means the store is
// running in pass-through mode and the admin dex editor has nothing to show.
func (s *Store) ShinyBaselineSize() int { return len(shinyBaseline) }

// ShinyDexHasDex reports whether the baseline knows this dex, so the admin API can reject a write
// for a species that does not exist rather than silently storing an orphan override row.
func (s *Store) ShinyDexHasDex(dex int) bool {
	_, ok := shinyBaseline[dex]
	return ok
}

// genLastDex is the final dex of each generation, ascending. Used only for the admin filter.
var genLastDex = []int{151, 251, 386, 493, 649, 721, 809, 905, 1025}

// GenForDex returns the generation a National Dex number belongs to, or 0 if out of range.
func GenForDex(dex int) int {
	if dex < 1 {
		return 0
	}
	for i, last := range genLastDex {
		if dex <= last {
			return i + 1
		}
	}
	return 0
}
