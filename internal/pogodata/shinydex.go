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
//	explicit admin flag  >  passed release date  >  (baseline OR upstream presence)
//
// Upstream is ADDITIVE: a species PoGoAPI lists can turn shiny_released on, but a species it omits
// can never turn it off. The 2026-07-04 audit found zero false positives in 863 upstream entries
// and 21 false negatives, so upstream-true is trustworthy and upstream-absence is not. The union
// means that if PoGoAPI ever resumes, new releases self heal with no admin action.
//
// The middle tier is the announced release date. Niantic says a shiny lands on a given day; an
// admin records that day, and when it arrives the shiny turns itself on rather than waiting for
// somebody to remember. Dates slip, so the explicit flag deliberately sits ABOVE the date and wins
// in both directions: unticking shiny_released holds a delayed release back even though its date
// has passed. That ordering is the entire safety net for the automatic flip, so do not swap it.
//
// Dates are compared as "YYYY-MM-DD" strings. That sorts in date order for free and keeps a
// timezone out of the comparison; the one clock involved is UTC, via shinyNow.
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
	"time"
)

// shinyNow is the clock the release dates are judged against, in UTC. A variable rather than a
// direct time.Now call so the tests can drive a date across midnight instead of hoping the wall
// clock cooperates.
var shinyNow = func() time.Time { return time.Now().UTC() }

// shinyDateLayout is the one date format this feature speaks, end to end: the SQL column is a DATE,
// the handler formats it with DATE_FORMAT, and the client renders it. Nothing parses a timestamp.
const shinyDateLayout = "2006-01-02"

// today returns the current UTC day as a comparable "YYYY-MM-DD" string.
func today() string { return shinyNow().Format(shinyDateLayout) }

// BaselineSpecies is one row of the embedded National Dex truth table.
//
// ReleaseDate is never present in the embedded file: it is filled in by rebuildShinyLocked from the
// admin override table, and only for a species whose shiny is still unreleased, so a card that is
// already catchable does not carry a stale date. omitempty keeps the embedded file's shape.
type BaselineSpecies struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	InGo          bool   `json:"in_go"`
	ShinyReleased bool   `json:"shiny_released"`
	ReleaseDate   string `json:"shiny_release_date,omitempty"`
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
//
// ReleaseDate is "YYYY-MM-DD" or empty. It is not a pointer because there is no third state to
// express: a date is either announced or it is not.
type ShinyOverride struct {
	Dex           int
	Region        string
	InGo          *bool
	ShinyReleased *bool
	ReleaseDate   string
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
	// ReleaseDate is the announced day; ReleasedByDate says the shiny is on BECAUSE that day has
	// passed rather than because anyone ticked a box. The panel needs to tell those apart to
	// explain itself.
	ReleaseDate    string `json:"release_date,omitempty"`
	ReleasedByDate bool   `json:"released_by_date,omitempty"`
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

// RegionalShinyOverride is one admin correction to a regional or alternate form. Species is the
// English name because that is what the client keys its compiled-in table by; this package never
// interprets it. Form IDENTITY still lives in ts/shared/regionalForms.ts and
// internal/handlers/regional.go, and duplicating it here would be a third copy to keep in sync.
type RegionalShinyOverride struct {
	Species       string
	Region        string
	ShinyReleased *bool
	ReleaseDate   string
}

// SetRegionalShinyOverrides replaces the regional corrections and recomputes the served blobs.
//
// It takes the rows rather than a finished blob so that resolving a release date happens HERE, on
// the same clock and in the same rebuild as every species. When the handler resolved them itself, a
// form's announced day arriving did nothing until the next admin write, because nothing else ever
// called back into the handler layer.
func (s *Store) SetRegionalShinyOverrides(list []RegionalShinyOverride) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regionalOverrides = list
	s.rebuildShinyLocked()
}

// rebuildRegionalShinyLocked recomputes the two sparse overlays the client lays over its compiled-in
// form table: species name -> region tag -> shiny_released, and the same shape carrying the
// announced day. Two blobs rather than one object-valued map, so the flags keep the plain boolean
// shape every existing consumer and check script already reads. Caller must hold s.mu.
func (s *Store) rebuildRegionalShinyLocked() {
	flags := map[string]map[string]bool{}
	dates := map[string]map[string]string{}

	for _, ov := range s.regionalOverrides {
		if ov.Species == "" || ov.Region == "" {
			continue
		}
		// A form has no baseline row, so the precedence from the top of this file is applied to the
		// override alone: explicit flag beats a passed date. What it falls back to when neither
		// speaks is the client's own default, which is why nothing is written in that case.
		released, say := false, false
		if ShinyDateReached(ov.ReleaseDate) {
			released, say = true, true
		}
		if ov.ShinyReleased != nil {
			released, say = *ov.ShinyReleased, true
		}
		if say {
			if flags[ov.Species] == nil {
				flags[ov.Species] = map[string]bool{}
			}
			flags[ov.Species][ov.Region] = released
		}
		// The date only travels while the form is still locked, matching the species blob.
		if ov.ReleaseDate != "" && !released {
			if dates[ov.Species] == nil {
				dates[ov.Species] = map[string]string{}
			}
			dates[ov.Species][ov.Region] = ov.ReleaseDate
		}
	}

	s.regionalShiny = marshalOverlay(flags, "shiny flags")
	s.regionalShinyDates = marshalOverlay(dates, "release dates")
}

// marshalOverlay renders one sparse overlay, or nil when it is empty so the key drops out of the
// payload entirely. A marshal failure logs and serves nothing, which falls back to the client's
// compiled-in defaults rather than a half written overlay.
func marshalOverlay[V bool | string](m map[string]map[string]V, what string) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	blob, err := json.Marshal(m)
	if err != nil {
		log.Printf("pogodata: regional %s: marshal: %v", what, err)
		return nil
	}
	return blob
}

// RefreshShinyDates rebuilds the served blobs when the UTC day has rolled over, so a release date
// takes effect on the day it names instead of waiting for the next six hour data refresh.
//
// Called hourly. The common case is a single string comparison and no work at all.
func (s *Store) RefreshShinyDates() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shinyBuiltFor == today() {
		return
	}
	s.rebuildShinyLocked()
}

// shinyFlags is the resolved answer for one species, plus enough provenance for the admin panel to
// explain it. A plain multi-return grew past the point where the call sites stayed readable.
type shinyFlags struct {
	InGo           bool
	ShinyReleased  bool
	Overridden     bool
	ReleaseDate    string
	ReleasedByDate bool
}

// effectiveFlags resolves one species against the precedence documented at the top of this file.
// Caller must hold s.mu.
func (s *Store) effectiveFlags(dex int, base BaselineSpecies, upstreamShiny bool) shinyFlags {
	out := shinyFlags{
		InGo:          base.InGo,
		ShinyReleased: base.ShinyReleased || upstreamShiny,
	}
	if ov, ok := s.shinyOverrides[shinyOverrideKey{Dex: dex}]; ok {
		out.ReleaseDate = ov.ReleaseDate
		// A recorded date is a decision somebody made, so the row counts as overridden even when
		// both flags are NULL. Otherwise a date-only row draws no Reset button and shows no note,
		// leaving it invisible and unremovable from the panel.
		if ov.ReleaseDate != "" {
			out.Overridden = true
			if ov.ReleaseDate <= today() {
				out.ShinyReleased = true
				out.ReleasedByDate = true
			}
		}
		// Explicit flags sit above the date and win in both directions, so an admin can hold back a
		// release Niantic delayed past its announced day.
		if ov.ShinyReleased != nil {
			out.ShinyReleased = *ov.ShinyReleased
			out.ReleasedByDate = out.ShinyReleased && out.ReleasedByDate
			out.Overridden = true
		}
		if ov.InGo != nil {
			out.InGo = *ov.InGo
			out.Overridden = true
		}
	}
	if out.ShinyReleased { // a released shiny implies the species is in the game
		out.InGo = true
	}
	return out
}

// rebuildShinyLocked recomputes s.shinies (released species only, the shape every existing client
// already consumes) and s.shinyDex (all 1025 with flags, the new sibling key). Caller must hold
// s.mu. Both blobs are marshalled once here, never per request.
//
// s.shinies deliberately stays released-only. It is a shipped contract: /api/data, /api/app/data,
// /api/private/data and the mobile app all read it, and widening it to the full dex would make
// every one of them render species that cannot be caught.
func (s *Store) rebuildShinyLocked() {
	// Remembered so RefreshShinyDates can tell whether a release date has crossed midnight since
	// the blobs were last built, without re-resolving 1025 species every hour.
	s.shinyBuiltFor = today()
	// Before the early return below: a missing baseline stops species resolution, but the regional
	// overlays are independent of it and must still be served.
	s.rebuildRegionalShinyLocked()

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
		f := s.effectiveFlags(dex, base, upstreamShiny)

		row := BaselineSpecies{ID: dex, Name: base.Name, InGo: f.InGo, ShinyReleased: f.ShinyReleased}
		// The date only travels while the shiny is still locked: that is the one moment a trainer
		// needs it. Once the card is catchable the date is history and would just be noise in a
		// blob every page load already downloads.
		if !f.ShinyReleased {
			row.ReleaseDate = f.ReleaseDate
		}
		dexTable[key] = row
		if !f.ShinyReleased {
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
		f := s.effectiveFlags(dex, base, upstreamShiny)
		out = append(out, AdminShinySpecies{
			Dex:                  dex,
			Name:                 base.Name,
			Gen:                  GenForDex(dex),
			InGo:                 f.InGo,
			ShinyReleased:        f.ShinyReleased,
			DefaultInGo:          base.InGo,
			DefaultShinyReleased: base.ShinyReleased,
			UpstreamShiny:        upstreamShiny,
			Overridden:           f.Overridden,
			ReleaseDate:          f.ReleaseDate,
			ReleasedByDate:       f.ReleasedByDate,
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

// ShinyDateReached reports whether an announced release date has arrived, on the same UTC clock the
// served blobs are resolved against. Exported so the admin write path can classify an explicit tick
// that merely agrees with the date as the no-op it is, instead of freezing it into the table.
func ShinyDateReached(date string) bool { return date != "" && date <= today() }

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
