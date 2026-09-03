package handlers

// The shiny dex as a finished list of checklist cards, for the mobile app.
//
// The website builds this list in the browser, in ts/shinies.ts: it takes the dex table, expands
// every species into a base card plus one card per regional or alternate forme, resolves each
// card's sprite, decides whether its shiny is released by laying admin overrides over a
// compiled-in default table, and works out which announced date to show. That is roughly 1,400
// lines of derived logic across shinies.ts, regionalForms.ts and costumes.ts.
//
// A native client that wanted the same checklist had two options: reimplement all of it in
// Kotlin, or be told the answer. This is the second. The app renders, filters and counts; it
// derives nothing. The one thing it cannot be told is which entries the trainer owns, because it
// holds those itself, so the join is spelled out below and made as dumb as possible.
//
// What this deliberately does NOT include:
//
//   - Costume cards. Costumes are a property of an ENTRY, not a card: the website shows a costume
//     picker in the add and edit modal and swaps the sprite, but the checklist has one card per
//     (species, region) and no more. /api/mobile/v1/costumes already serves the picker.
//   - Scatterbug and Spewpa pattern cards. Those patterns are carry-only: recordable on an entry
//     and carried through evolution, but never their own card and never a different sprite. See
//     PatternCarriers below, which is how a client folds them back onto the base card.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"pogo.hails.cc/internal/pogodata"
)

// shinyDexCard is one checklist card, fully resolved.
type shinyDexCard struct {
	// Key is "<pokemon_id>:<region>" and is the ONLY thing a client should join a
	// user_shinies row to a card on. pokemon_id is the species NAME, not the dex
	// number, because that is what the column holds. It mirrors buildCaughtCardKeys
	// in ts/shinies.ts exactly; changing the format here silently unticks every card
	// in every client, so it is pinned by a test.
	Key     string `json:"key"`
	Dex     int    `json:"dex"`
	Species string `json:"species"`
	// Region is "" for the base species card.
	Region string `json:"region,omitempty"`
	// Label is the extra name a card needs beyond its species: the Unown glyph, the
	// Vivillon pattern. Absent, not empty, when the species name is the whole name.
	Label string `json:"label,omitempty"`
	// SpriteSlug is the PokeAPI slug the art is filed under. Sent alongside the
	// resolved URL so a client can build a different size or a non-shiny variant
	// without reversing the URL, which is the kind of parsing that breaks quietly
	// when the sprite host changes.
	SpriteSlug string `json:"sprite_slug"`
	SpriteURL  string `json:"sprite_url"`
	InGo       bool   `json:"in_go"`
	Released   bool   `json:"released"`
	// ReleaseDate is "YYYY-MM-DD" when the shiny is announced but not yet out, else
	// absent. Already resolved: a form waiting on its own species carries the
	// SPECIES' date, because a form's shiny cannot arrive before its species'.
	ReleaseDate string `json:"release_date,omitempty"`
}

// shinyDexManifest is the whole payload.
type shinyDexManifest struct {
	// Version changes whenever the cards do. Also the ETag's basis, so a client can
	// use it as a cache key for its own on-disk copy without hashing anything.
	Version string         `json:"version"`
	Cards   []shinyDexCard `json:"cards"`
	// PatternCarriers is species name -> the region tags that are carry-only for it.
	//
	// This is the one rule a client cannot avoid applying, because it applies to the
	// rows the client holds and the server never sees. A Scatterbug recorded as
	// viv_elegant belongs to the plain "Scatterbug:" card: the pattern rides up the
	// evolution line to Vivillon but never shows on Scatterbug itself. Without the
	// fold, that entry builds the key "Scatterbug:viv_elegant", matches no card, and
	// the trainer's catch silently stops counting.
	//
	// So: before building an entry's key, if its region appears here under its
	// species, use "" instead. That is cardRegion() in ts/shinies.ts.
	PatternCarriers map[string][]string `json:"pattern_carriers"`
}

// shinyDexCacheTTL bounds how stale the manifest can get from a change the handlers
// layer never sees.
//
// An admin flag flip invalidates explicitly (reloadShinyOverrides calls
// invalidateShinyDexManifest), so the case this covers is the other one: the store
// re-resolves announced release dates on its own hourly tick, and a date crossing
// midnight turns a shiny on with nothing calling into this package at all. A minute
// of staleness on a date that was announced weeks ago is not worth a subscription
// mechanism; a full rebuild per request for every user would be.
const shinyDexCacheTTL = time.Minute

type shinyDexCache struct {
	mu      sync.Mutex
	body    []byte
	etag    string
	builtAt time.Time
}

var shinyDexManifestCache shinyDexCache

// invalidateShinyDexManifest drops the cached manifest so the next request rebuilds.
// Called after an admin override write, which is the change that has to be instant.
func invalidateShinyDexManifest() {
	shinyDexManifestCache.mu.Lock()
	shinyDexManifestCache.body = nil
	shinyDexManifestCache.mu.Unlock()
}

// buildShinyDexManifest assembles every card. Pure with respect to the request: it
// reads the store and the compiled-in form tables and nothing else.
func (h *Handlers) buildShinyDexManifest() shinyDexManifest {
	species := h.store.ShinyDexCards()
	flags, dates := h.store.RegionalShinyState()

	// Forms grouped by species, so each species is one pass rather than a scan of
	// the whole form table per species.
	formsBySpecies := map[string][]RegionalFormRow{}
	for _, f := range regionalFormRows() {
		formsBySpecies[f.Species] = append(formsBySpecies[f.Species], f)
	}

	out := shinyDexManifest{
		Cards:           make([]shinyDexCard, 0, len(species)+len(regionalFormRows())),
		PatternCarriers: patternCarriers(),
	}

	for _, s := range species {
		out.Cards = append(out.Cards, shinyDexCard{
			Key:         s.Name + ":",
			Dex:         s.ID,
			Species:     s.Name,
			SpriteSlug:  strconv.Itoa(s.ID),
			SpriteURL:   absoluteURL(spriteURLSlug(strconv.Itoa(s.ID), "shiny")),
			InGo:        s.InGo,
			Released:    s.ShinyReleased,
			ReleaseDate: s.ReleaseDate,
		})

		forms := formsBySpecies[s.Name]
		sort.Slice(forms, func(i, j int) bool {
			return regionRank(forms[i].Region) < regionRank(forms[j].Region)
		})
		for _, f := range forms {
			out.Cards = append(out.Cards, formCard(s, f, flags, dates))
		}
	}

	// Species order is the dex, which ShinyDexCards already sorted; form order
	// inside a species is REGION_ORDER, applied above. Sorting the whole slice
	// again would only risk disagreeing with that.
	out.Version = manifestVersion(out.Cards)
	return out
}

// formCard resolves one regional or alternate forme card.
//
// The three rules here are the ones ts/shinies.ts applies, in the same order:
//
//  1. A form's shiny is released only if the SPECIES' shiny is. A form cannot
//     arrive before the species it is a form of.
//  2. Whether the form itself is released is the compiled-in default, overridden
//     by an admin flag when one exists. The overlay is SPARSE: an absent pair
//     means no admin has an opinion, not that the answer is false.
//  3. A form waiting on its own species shows the SPECIES' date, not its own.
//     Showing an earlier form date would promise something that cannot happen.
func formCard(s pogodata.BaselineSpecies, f RegionalFormRow, flags map[string]map[string]bool, dates map[string]map[string]string) shinyDexCard {
	formShiny := regionalShinyDefault(f.Species, f.Region)
	if v, ok := flags[f.Species][f.Region]; ok {
		formShiny = v
	}

	releaseDate := s.ReleaseDate
	if s.ShinyReleased {
		releaseDate = dates[f.Species][f.Region]
	}

	return shinyDexCard{
		Key:         f.Species + ":" + f.Region,
		Dex:         s.ID,
		Species:     f.Species,
		Region:      f.Region,
		Label:       formLabel(f.Species, f.Region),
		SpriteSlug:  f.Slug,
		SpriteURL:   absoluteURL(spriteURLSlug(f.Slug, "shiny")),
		InGo:        s.InGo,
		Released:    s.ShinyReleased && formShiny,
		ReleaseDate: releaseDate,
	}
}

// formLabel returns the extra name a card needs beyond its species, or "" when the
// species name says everything: "Alolan Rattata" is drawn from the region tag by
// the client, but there is no way to derive "!" from "unown_excl".
func formLabel(species, region string) string {
	switch species {
	case "Unown":
		return labelFor(unownVariants, region)
	case "Vivillon":
		return labelFor(vivillonVariants, region)
	}
	return ""
}

// patternCarriers is the carry-only table: species that record a Vivillon pattern
// on an entry without ever showing it. Derived from vivillonVariants rather than
// restated, so a new pattern cannot be added to one and forgotten in the other.
func patternCarriers() map[string][]string {
	regions := make([]string, 0, len(vivillonVariants))
	for _, v := range vivillonVariants {
		regions = append(regions, v.Region)
	}
	return map[string][]string{
		"Scatterbug": regions,
		"Spewpa":     regions,
	}
}

// manifestVersion is a content hash, not a timestamp.
//
// A timestamp would change on every rebuild and defeat the ETag: the cache expires
// once a minute, so a clock-based version would make every client re-download an
// identical payload up to sixty times an hour. A content hash changes only when a
// card does, which is what both the ETag and the client's own disk cache want.
func manifestVersion(cards []shinyDexCard) string {
	sum := sha256.New()
	enc := json.NewEncoder(sum)
	for _, c := range cards {
		enc.Encode(c)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// MobileShinyDex serves the shiny dex manifest.
func (h *Handlers) MobileShinyDex(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUserAPI(w, r); !ok {
		return
	}

	body, etag := h.shinyDexManifestBytes()
	if len(body) == 0 {
		// No baseline embedded: the store is in pass-through mode and there is no
		// dex table to expand. Answering an empty card list would render an empty
		// checklist, which reads as "you own nothing" rather than "ask again later".
		writeJSONError(w, "shiny dex unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("ETag", etag)
	// no-cache, not no-store: the client SHOULD keep it and SHOULD revalidate. An
	// admin flag flip has to be live on the next launch, and the ETag makes that
	// revalidation almost free.
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// shinyDexManifestBytes returns the marshalled manifest and its ETag, rebuilding
// when the cache is empty or stale.
func (h *Handlers) shinyDexManifestBytes() ([]byte, string) {
	c := &shinyDexManifestCache
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.body != nil && time.Since(c.builtAt) < shinyDexCacheTTL {
		return c.body, c.etag
	}

	m := h.buildShinyDexManifest()
	if len(m.Cards) == 0 {
		// Do not cache an empty build. It means the baseline has not loaded, and
		// caching that would hold the failure for a minute after it resolved.
		return nil, ""
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, ""
	}
	c.body, c.etag, c.builtAt = body, `"`+m.Version+`"`, time.Now()
	return c.body, c.etag
}
