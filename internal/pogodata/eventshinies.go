package pogodata

// Shiny releases read off the scraped event pages.
//
// PoGoAPI stopped adding shiny releases in early 2026, which froze the upstream tier of the shiny
// dex: every release since then needed an admin to tick a box, and nine species sat locked for
// weeks after their debut (the Applin line, Shroodle and Grafaiai, Frosmoth, Flamigo). The signal
// was in our own cache the whole time. LeekDuck's page for Harvest Festival 2026 has a "Shiny Debut"
// section listing pm840.s.icon.png through pm1019.s.icon.png, and a season page marks a Snom egg
// with img.shiny-icon. Nothing read either, because the event page reader only ever looked for
// raid bosses.
//
// This file reads them. A pkmn-list-item counts as a shiny when its sprite is the shiny one
// (pmNNN.s.icon.png, pokemon_icon_NNN_00_shiny.png) or when it carries img.shiny-icon. Only the
// plain species sprite counts: a form or costume sprite (pm25.fTSHIRT_03, pm149.fMEGA,
// pokemon_icon_094_51) says nothing about the base species, and the dex comes from the filename
// rather than the visible name, so no name matching is involved.
//
// An observation is ADDITIVE, exactly like upstream presence: it can turn shiny_released on, never
// off, and an explicit admin flag still beats it in both directions (see effectiveFlags). It takes
// effect on the event's start day, so a debut LeekDuck publishes a week ahead stays locked until it
// is live, and RefreshShinyDates flips it at midnight like an admin entered date.
//
// A sighting also carries forward along evolution lines (rebuildShinyObservedDaysLocked), because a
// shiny Snom evolves into a shiny Frosmoth whether or not the page bothered to show one.
//
// Observations are PERSISTED to cache/shiny_observed.json. That is load bearing: refreshEventDetails
// evicts a page as soon as its event leaves the feed, so a debut would otherwise unlock for the
// week of the event and lock again the moment it ended.

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// shinyObservation is the first event page that showed a species' shiny, and the day it applies.
type shinyObservation struct {
	Date    string `json:"date"`  // "YYYY-MM-DD", the event's start day
	EventID string `json:"event"` // the LeekDuck event id, for the admin panel and the log
}

const shinyObservedFile = "shiny_observed.json"

var (
	// pm840.icon.png, pm840.s.icon.png. Anything with a .f or .c segment does not match.
	eventSpritePmRe = regexp.MustCompile(`/pm(\d{1,4})(\.s)?\.icon\.png(?:[?#].*)?$`)
	// pokemon_icon_111_00.png, pokemon_icon_111_00_shiny.png. Form 00 only.
	eventSpriteLegacyRe = regexp.MustCompile(`/pokemon_icon_(\d{1,4})_00(_shiny)?\.png(?:[?#].*)?$`)
)

// eventSpriteDex reads the dex out of a species level sprite URL, and whether it is the shiny sprite.
func eventSpriteDex(src string) (dex int, shiny, ok bool) {
	if m := eventSpritePmRe.FindStringSubmatch(src); m != nil {
		dex, _ = strconv.Atoi(m[1])
		return dex, m[2] != "", dex > 0
	}
	if m := eventSpriteLegacyRe.FindStringSubmatch(src); m != nil {
		dex, _ = strconv.Atoi(m[1])
		return dex, m[2] != "", dex > 0
	}
	return 0, false, false
}

// parseEventPageShinies returns every dex an event page shows a shiny for, ascending.
func parseEventPageShinies(pageHTML string) []int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	doc.Find("li.pkmn-list-item").Each(func(_ int, li *goquery.Selection) {
		src, _ := li.Find(".pkmn-list-img img[src]").First().Attr("src")
		if src == "" {
			src, _ = li.Find("img[src]:not(.shiny-icon)").First().Attr("src")
		}
		dex, shinySprite, ok := eventSpriteDex(src)
		if !ok {
			return
		}
		if shinySprite || li.Find("img.shiny-icon").Length() > 0 {
			seen[dex] = true
		}
	})
	out := make([]int, 0, len(seen))
	for dex := range seen {
		out = append(out, dex)
	}
	sort.Ints(out)
	return out
}

// observeEventShiniesLocked reads every cached event page, records any shiny not yet observed, and
// rebuilds the shiny blobs when something changed. Caller must hold s.mu. It writes the small
// observation file itself, because forgetting to persist is the one way this can lose data.
func (s *Store) observeEventShiniesLocked() {
	if len(s.events) == 0 || len(s.eventDetails) == 0 {
		return
	}
	var entries []raidFeedEntry
	if err := json.Unmarshal(s.events, &entries); err != nil {
		return
	}
	if s.shinyObserved == nil {
		s.shinyObserved = map[int]shinyObservation{}
	}
	var added []string
	for _, e := range entries {
		d, ok := s.eventDetails[e.EventID]
		if e.EventID == "" || !ok || strings.TrimSpace(d.HTML) == "" {
			continue
		}
		start, _, ok := ParseFeedTime(e.Start, time.UTC)
		if !ok {
			continue // no date to apply it from; the next feed will usually carry one
		}
		day := start.Format(shinyDateLayout)
		for _, dex := range parseEventPageShinies(d.HTML) {
			if _, known := shinyBaseline[dex]; !known && len(shinyBaseline) > 0 {
				continue
			}
			prev, had := s.shinyObserved[dex]
			if had && prev.Date <= day {
				continue // the earliest sighting wins; a later event re-featuring it changes nothing
			}
			s.shinyObserved[dex] = shinyObservation{Date: day, EventID: e.EventID}
			added = append(added, strconv.Itoa(dex)+"@"+day)
		}
	}
	if len(added) == 0 {
		return
	}
	log.Printf("pogodata: shiny dex: %d shiny release(s) observed on event pages: %s", len(added), strings.Join(added, " "))
	s.persistShinyObservedLocked()
	s.rebuildShinyLocked()
}

// shinyObservedReached reports whether an event page has shown this shiny, or the shiny of a species
// that evolves into it, and its day has come. Caller must hold s.mu (read or write).
func (s *Store) shinyObservedReached(dex int) bool {
	date, ok := s.shinyObservedDays[dex]
	return ok && date <= today()
}

// evolutionChildren maps a dex to the dex numbers it evolves into, through Normal forms only.
//
// Built from the compiled-in table for the same reasons as evolutionTargets. The form filter is the
// point: a shiny Meowth says nothing about Perrserker, which only Galarian Meowth evolves into, and
// whose shiny depends on the Galarian form's release.
var evolutionChildren = loadEvolutionChildren()

func loadEvolutionChildren() map[int][]int {
	data, err := fallbackFS.ReadFile("fallback/pokemon_evolutions.json")
	if err != nil {
		return nil
	}
	var entries []struct {
		ID         int    `json:"pokemon_id"`
		Form       string `json:"form"`
		Evolutions []struct {
			ID   int    `json:"pokemon_id"`
			Form string `json:"form"`
		} `json:"evolutions"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &entries); err != nil {
		return nil
	}
	out := map[int][]int{}
	for _, e := range entries {
		if e.Form != "Normal" {
			continue
		}
		for _, to := range e.Evolutions {
			if to.Form == "Normal" && to.ID > 0 && to.ID != e.ID && !slices.Contains(out[e.ID], to.ID) {
				out[e.ID] = append(out[e.ID], to.ID)
			}
		}
	}
	return out
}

// rebuildShinyObservedDaysLocked expands the sightings forward along evolution lines: a trainer who
// catches a shiny Snom can evolve it, so shiny Frosmoth exists from the same day, though LeekDuck's
// page only ever showed the Snom. Forward only, never back to a pre-evolution, and only into species
// the baseline says are in the game, because a released shiny forces in_go on. The earliest date
// reaching a species wins. Caller must hold s.mu.
func (s *Store) rebuildShinyObservedDaysLocked() {
	days := make(map[int]string, len(s.shinyObserved)*2)
	var walk func(dex int, date string)
	walk = func(dex int, date string) {
		if prev, ok := days[dex]; ok && prev <= date {
			return // already reached as early or earlier; also stops any cycle
		}
		days[dex] = date
		for _, child := range evolutionChildren[dex] {
			if base, known := shinyBaseline[child]; len(shinyBaseline) > 0 && (!known || !base.InGo) {
				continue
			}
			walk(child, date)
		}
	}
	for dex, o := range s.shinyObserved {
		if o.Date != "" {
			walk(dex, o.Date)
		}
	}
	s.shinyObservedDays = days
}

func (s *Store) persistShinyObservedLocked() {
	out := make(map[string]shinyObservation, len(s.shinyObserved))
	for dex, o := range s.shinyObserved {
		out[strconv.Itoa(dex)] = o
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(s.cacheDir, shinyObservedFile), data, 0644); err != nil {
		log.Printf("pogodata: shiny dex: could not save observations: %v", err)
	}
}

// loadShinyObservedLocked reads the persisted observations. Caller must hold s.mu. A missing or
// unreadable file is an empty set: the next event details pass re-observes whatever is current.
func (s *Store) loadShinyObservedLocked() {
	s.shinyObserved = map[int]shinyObservation{}
	data, err := os.ReadFile(filepath.Join(s.cacheDir, shinyObservedFile))
	if err != nil {
		return
	}
	var in map[string]shinyObservation
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &in); err != nil {
		log.Printf("pogodata: shiny dex: observation file parse: %v", err)
		return
	}
	for key, o := range in {
		if dex, err := strconv.Atoi(key); err == nil && o.Date != "" {
			s.shinyObserved[dex] = o
		}
	}
	if len(s.shinyObserved) > 0 {
		log.Printf("pogodata: shiny dex: %d event page observation(s) loaded", len(s.shinyObserved))
	}
}
