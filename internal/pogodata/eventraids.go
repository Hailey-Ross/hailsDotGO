package pogodata

import (
	"encoding/json"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// A third source of raid rotations: the Raids section of a scraped event page.
//
// The events feed models a rotation as an eventType "raid-battles" entry carrying
// extraData.raidbattles.bosses, and raidschedule.go reads exactly those. A whole
// class of rotation is invisible to that reader. Mega Ascension (2026-08-31 to
// 2026-09-04) arrives as eventType "event" with no raid data at all, yet its page
// names a different Mega line up for every day of the week, and pokemon-go-api had
// not listed one of them either: on the day the event opened, upstream's Mega tier
// still held Mega Gyarados alone. Nothing anywhere in the app knew that Mega
// Victreebel, Mega Dragonite, Mega Malamar, Mega Latias and Mega Latios were live.
//
// The roster was already on disk. cache/event_details.json holds the sanitized
// LeekDuck page for every active event, refreshed every 12 hours and served into
// the event modal, and its Raids section is structured markup. This file reads it.
//
// Two rules keep that safe.
//
// The windows are ADDITIVE: they contribute live bosses and never make a tier
// authoritative, so an event page can add a boss to the grid but can never remove
// one. That is not a cautious default, it is what the source says. Mega Ascension's
// own page reads "Mega Raids will make up the majority of raids during the Mega
// Ascension event. Seasonal Raid Bosses may also appear during this time", so an
// authoritative reading of it would have deleted Mega Gyarados, which was still
// genuinely running. Being additive also happens to close a second defect: an
// active seasonal window makes its tier authoritative, so once upstream DID catch
// up, the reconciler would have dropped every Mega Ascension boss on the grounds
// that the Mega Gyarados rotation did not name them. A boss named by an additive
// window is in the active set, so the upstream copy is annotated and kept.
//
// And a boss is only accepted when its tier can be established with confidence,
// from the name or from the heading above it. That is the only filter: there is no
// cap on how many bosses a page may contribute and no limit on how long an event
// may run, because the page must always show every raid that is actually available
// and the tier tabs and type filter chips are the tools for narrowing a long grid.

// eventPageBoss is one boss read off an event page, with the scope the headings
// above it put it in.
type eventPageBoss struct {
	boss   WindowBoss
	tier   string
	shadow bool
	// day is the calendar day the headings scoped this boss to, or the zero time
	// when it belongs to the whole event.
	day time.Time
}

// headingScope is what the headings above a boss list have established so far.
type headingScope struct {
	// day is zero when nothing above has narrowed the boss to one calendar day,
	// which means it belongs to the whole event.
	day     time.Time
	tier    string
	shadow  bool
	hasTier bool
}

// eventPageRaidSection returns the Raids section of a scraped event page, as the
// run of sibling nodes between its header and the next section header.
//
// h2.event-section-header is the reliable boundary. Upstream marks every real
// section with it ("Bonuses", "Features", "Spawns", "Raids", "Research", "Shiny",
// "GO Pass", "Sales") and never puts it on a sub heading, so a plain h2 inside the
// section is content: "Throughout Mega Ascension" and "Saturday Habitat Mega Raids"
// are both ordinary h2 elements that scope the lists under them.
func eventPageRaidSection(doc *goquery.Document) *goquery.Selection {
	var header *goquery.Selection
	doc.Find("h2.event-section-header").EachWithBreak(func(_ int, h *goquery.Selection) bool {
		if id, _ := h.Attr("id"); strings.EqualFold(id, "raids") {
			header = h
			return false
		}
		if h.HasClass("raids") {
			header = h
			return false
		}
		if strings.EqualFold(strings.TrimSpace(h.Text()), "raids") {
			header = h
			return false
		}
		return true
	})
	if header == nil {
		return nil
	}
	var nodes []*goquery.Selection
	for n := header.Next(); n.Length() > 0; n = n.Next() {
		if goquery.NodeName(n) == "h2" && n.HasClass("event-section-header") {
			break
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return nil
	}
	out := nodes[0]
	for _, n := range nodes[1:] {
		out = out.AddSelection(n)
	}
	return out
}

// parseEventPageRaids reads the roster out of one sanitized event page.
//
// evStart and evEnd are the event's own span, used only to turn a heading like
// "Monday, August 31" into a calendar date: the page never writes a year, so the
// span supplies it and the weekday validates the result.
func parseEventPageRaids(pageHTML string, evStart, evEnd time.Time) []eventPageBoss {
	if strings.TrimSpace(pageHTML) == "" {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	section := eventPageRaidSection(doc)
	if section == nil {
		return nil
	}

	// One scope per heading level, so a heading inherits from the level above it
	// and replaces anything deeper. h2 resets to the section root, which knows
	// nothing; h3 refines whatever h2 established; h4 refines h3.
	//
	// Both live pages need exactly this. On mega-ascension the h3 "Monday, August
	// 31" scopes the list under it to one day while the h2 "Throughout Mega
	// Ascension" that follows resets to the whole event, which is what Mega Latias
	// and Mega Latios genuinely are. On the GO Fest page the h2 "Saturday Habitat
	// Mega Raids" sets the day and the h3 habitat names beneath it ("Verdant
	// Overgrowth", "Eerie Alley") carry no date of their own and inherit it.
	levels := map[int]headingScope{}
	cur := headingScope{}

	var out []eventPageBoss
	section.Each(func(_ int, node *goquery.Selection) {
		name := goquery.NodeName(node)
		if lvl, ok := headingLevel(name); ok {
			base := levels[lvl-1]
			base = applyHeading(base, node.Text(), evStart, evEnd)
			levels[lvl] = base
			for deeper := lvl + 1; deeper <= 6; deeper++ {
				delete(levels, deeper)
			}
			cur = base
			return
		}
		node.Find("li.pkmn-list-item").Each(func(_ int, li *goquery.Selection) {
			b, ok := eventPageBossFrom(li, cur)
			if ok {
				out = append(out, b)
			}
		})
	})
	return out
}

// headingLevel reports the nesting level of a heading element.
func headingLevel(nodeName string) (int, bool) {
	switch nodeName {
	case "h2":
		return 2, true
	case "h3":
		return 3, true
	case "h4":
		return 4, true
	case "h5":
		return 5, true
	case "h6":
		return 6, true
	}
	return 0, false
}

// applyHeading folds one heading's text onto the scope it inherits. A heading that
// says nothing about a day or a tier leaves both as they were, which is what makes
// a habitat name inherit the Saturday above it.
func applyHeading(base headingScope, text string, evStart, evEnd time.Time) headingScope {
	if day, ok := resolveHeadingDay(text, evStart, evEnd); ok {
		base.day = day
	}
	if tier, shadow, ok := headingTier(text); ok {
		base.tier, base.shadow, base.hasTier = tier, shadow, true
	}
	return base
}

// eventPageBossFrom reads one boss out of a list item and settles its tier.
func eventPageBossFrom(li *goquery.Selection, scope headingScope) (eventPageBoss, bool) {
	name := strings.Join(strings.Fields(li.Find(".pkmn-name").First().Text()), " ")
	if name == "" {
		return eventPageBoss{}, false
	}
	tier, shadow, ok := eventPageTier(name, scope)
	if !ok || !governedTiers[tier] {
		// Either nothing established a tier, or it is one the schedule does not
		// govern. Tier 1 and tier 3 rotate rarely and no feed carries timing for
		// them, so they stay upstream's business: this is what keeps the Pikachu
		// under lego-pokemon-go-2026's "Appearing in 1-Star Raids" off the grid.
		return eventPageBoss{}, false
	}
	img, _ := li.Find(".pkmn-list-img img[src]").First().Attr("src")
	if img == "" {
		img, _ = li.Find("img[src]").First().Attr("src")
	}
	return eventPageBoss{
		boss: WindowBoss{
			Name:       name,
			Image:      img,
			CanBeShiny: li.Find("img.shiny-icon").Length() > 0,
		},
		tier:   tier,
		shadow: shadow,
		day:    scope.day,
	}, true
}

// eventPageTier settles a boss's tier from its own name first, then from the
// heading above it, and refuses to guess.
//
// The name is checked first because it is the stronger signal and the one that
// needs no heading at all: "Mega Victreebel" sitting under a bare date heading is
// unambiguously a Mega Raid. Everything else needs a heading that actually says
// what tier it is describing, which is why a Community Day page's four star raids
// and a bare date heading's non Mega contents both come back false.
func eventPageTier(name string, scope headingScope) (tier string, shadow bool, ok bool) {
	if isMegaName(name) {
		return "6", false, true
	}
	if scope.hasTier {
		return scope.tier, scope.shadow, true
	}
	return "", false, false
}

var (
	eventHeadingDigitStarRe = regexp.MustCompile(`(\d)[- ]star`)
	eventHeadingWordStarRe  = regexp.MustCompile(`\b(one|two|three|four|five|six)[- ]star`)
	eventHeadingShadowRe    = regexp.MustCompile(`shadow[- ]raid`)
	eventHeadingEliteRe     = regexp.MustCompile(`elite[- ]raid`)
	eventHeadingMegaRe      = regexp.MustCompile(`mega[- ]raid`)
)

var starWords = map[string]string{"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6"}

// headingTier reads a tier off a heading, and says so only when the heading is
// actually talking about a tier.
//
// The shapes in the live corpus are "Appearing in 1-Star Raids",
// "Appearing in 5-Star Shadow Raids", "Five-star Raids" and
// "Saturday Habitat Mega Raids". Note that the existing starRaidRe in
// raidschedule.go cannot be reused here: it requires "star" and "raid" to be
// adjacent, and "5-Star Shadow Raids" puts a word between them.
func headingTier(text string) (tier string, shadow bool, ok bool) {
	hay := strings.ToLower(strings.Join(strings.Fields(text), " "))
	shadow = eventHeadingShadowRe.MatchString(hay)
	if m := eventHeadingDigitStarRe.FindStringSubmatch(hay); m != nil {
		return m[1], shadow, true
	}
	if m := eventHeadingWordStarRe.FindStringSubmatch(hay); m != nil {
		return starWords[m[1]], shadow, true
	}
	if eventHeadingMegaRe.MatchString(hay) {
		return "6", false, true
	}
	if shadow {
		// Shadow rotations are legendaries and read as 5 star in game, the same
		// reading classifyRaidTier applies to a slug that says only "shadow raids".
		return "5", true, true
	}
	if eventHeadingEliteRe.MatchString(hay) {
		return "5", false, true
	}
	return "", false, false
}

var (
	headingMonthDayRe = regexp.MustCompile(`(?i)\b(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{1,2})\b`)
	headingWeekdayRe  = regexp.MustCompile(`(?i)\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)
)

var headingMonths = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

var headingWeekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

// resolveHeadingDay turns a heading into the calendar day it names, or reports
// false when it names none.
//
// Every shape fails closed. The page writes no year, so the year comes from the
// event's own span and the weekday is checked against the result: a heading that
// says "Monday, August 31" only resolves if 31 August really is a Monday in a year
// the event covers. A bare weekday ("Saturday", or "Saturday Habitat Mega Raids")
// resolves only when exactly one day in the span falls on it, which means a long
// event simply produces no day scoping rather than an arbitrary one.
func resolveHeadingDay(text string, evStart, evEnd time.Time) (time.Time, bool) {
	if evStart.IsZero() || evEnd.IsZero() || evEnd.Before(evStart) {
		return time.Time{}, false
	}
	hay := strings.ToLower(strings.Join(strings.Fields(text), " "))
	first := dayOf(evStart)
	last := dayOf(evEnd)

	var wantDay time.Weekday
	haveWeekday := false
	if m := headingWeekdayRe.FindStringSubmatch(hay); m != nil {
		wantDay, haveWeekday = headingWeekdays[strings.ToLower(m[1])], true
	}

	if m := headingMonthDayRe.FindStringSubmatch(hay); m != nil {
		mon := headingMonths[strings.ToLower(m[1])]
		dom := atoiSafe(m[2])
		if dom < 1 || dom > 31 {
			return time.Time{}, false
		}
		var hits []time.Time
		for year := first.Year(); year <= last.Year(); year++ {
			d := time.Date(year, mon, dom, 0, 0, 0, 0, time.UTC)
			if d.Day() != dom || d.Month() != mon {
				continue // a date that does not exist, such as February 30
			}
			if d.Before(first) || d.After(last) {
				continue
			}
			if haveWeekday && d.Weekday() != wantDay {
				continue
			}
			hits = append(hits, d)
		}
		if len(hits) != 1 {
			return time.Time{}, false
		}
		return hits[0], true
	}

	if haveWeekday {
		var hits []time.Time
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			if d.Weekday() == wantDay {
				hits = append(hits, d)
			}
		}
		if len(hits) != 1 {
			return time.Time{}, false
		}
		return hits[0], true
	}
	return time.Time{}, false
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// eventPageDayStart and eventPageDayEnd bracket a day scoped rotation.
//
// They are floating wall clock readings, like every other rotation string in this
// app, and deliberately span the whole local day rather than the event's own hours.
// Mega Ascension says so in as many words: "Mega Ascension and Mega Finale Raids
// will take their place starting Monday, August 31, at 12:01 a.m. to Sunday,
// September 6, 2026, at 11:59 p.m. local time", which is several hours before the
// event itself opens at 10 a.m. on its first day.
const (
	eventPageDayStart = "T00:01:00.000"
	eventPageDayEnd   = "T23:59:00.000"
)

// eventPageRaidWindows turns one feed entry plus its scraped page into additive
// rotations, one per tier, shadow flag and day the page distinguishes.
func eventPageRaidWindows(e raidFeedEntry, pageHTML string) []RaidWindow {
	evStart, _, okStart := ParseFeedTime(e.Start, time.UTC)
	evEnd, _, okEnd := ParseFeedTime(e.End, time.UTC)
	if !okStart || !okEnd {
		return nil
	}
	roster := parseEventPageRaids(pageHTML, evStart, evEnd)
	if len(roster) == 0 {
		return nil
	}

	type groupKey struct {
		tier   string
		shadow bool
		day    string
	}
	order := make([]groupKey, 0, len(roster))
	groups := map[groupKey][]WindowBoss{}
	seen := map[groupKey]map[string]bool{}
	for _, r := range roster {
		k := groupKey{tier: r.tier, shadow: r.shadow}
		if !r.day.IsZero() {
			k.day = r.day.Format("2006-01-02")
		}
		if seen[k] == nil {
			seen[k] = map[string]bool{}
			order = append(order, k)
		}
		norm := normalizeBossName(r.boss.Name)
		if seen[k][norm] {
			continue // the same boss listed twice under one scope
		}
		seen[k][norm] = true
		groups[k] = append(groups[k], r.boss)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].day != order[j].day {
			return order[i].day < order[j].day
		}
		if order[i].tier != order[j].tier {
			return order[i].tier < order[j].tier
		}
		return !order[i].shadow && order[j].shadow
	})

	out := make([]RaidWindow, 0, len(order))
	for _, k := range order {
		rawStart, rawEnd := e.Start, e.End
		if k.day != "" {
			rawStart = k.day + eventPageDayStart
			rawEnd = k.day + eventPageDayEnd
		}
		start, end, ok := raidWindowSpan(rawStart, rawEnd)
		if !ok {
			log.Printf("pogodata: raid schedule: event page %q produced an unusable window (%q to %q)", e.EventID, rawStart, rawEnd)
			continue
		}
		out = append(out, RaidWindow{
			// The bare event id, never a per day variant: the client opens the
			// event modal with it, and a synthetic id would open nothing.
			EventID:   e.EventID,
			Name:      e.Name,
			Tier:      k.tier,
			Shadow:    k.shadow,
			Bosses:    groups[k],
			RawStart:  rawStart,
			RawEnd:    rawEnd,
			StartsUTC: start,
			EndsUTC:   end,
			Additive:  true,
		})
	}
	return out
}

// eventRaidCacheEntry memoizes one page's derived windows.
//
// The rebuild runs on a five minute ticker and the page corpus is upwards of a
// megabyte of HTML across fifty events, so re-parsing all of it to answer a
// question whose inputs have not moved would be the most expensive thing the store
// does. The key is everything the derivation reads: the scrape timestamp, which
// changes whenever the page is re-fetched, and the feed's own span, which is what
// resolves a heading to a date.
type eventRaidCacheEntry struct {
	fetchedAt time.Time
	start     string
	end       string
	windows   []RaidWindow
}

// eventPageWindowsLocked derives the additive rotations from every scraped event
// page. Caller must hold s.mu.
//
// eventType "raid-battles" is skipped and only that: those events carry their
// roster in the feed itself, where parseRaidWindows already reads it with real
// timing, so parsing their page as well would be duplicated work.
//
// Everything else is allowed through, including raid-hour, raid-day and
// pokemon-spotlight-hour. parseRaidWindows excludes those from the FEED for a good
// reason, which is that a one hour window read as a rotation would become
// authoritative for its tier and delete the real rotation around it. An additive
// window cannot do that, and a Raid Day boss genuinely is available for those
// hours, so there is nothing left to protect against: bossKey already folds it
// together with whatever rotation is naming the same boss.
func (s *Store) eventPageWindowsLocked() []RaidWindow {
	if len(s.events) == 0 || len(s.eventDetails) == 0 {
		return nil
	}
	var entries []raidFeedEntry
	if err := json.Unmarshal(s.events, &entries); err != nil {
		return nil // parseRaidWindows has already logged this
	}
	if s.eventRaidCache == nil {
		s.eventRaidCache = map[string]eventRaidCacheEntry{}
	}
	seen := make(map[string]bool, len(entries))
	var out []RaidWindow
	for _, e := range entries {
		if e.EventID == "" || e.EventType == "raid-battles" {
			continue
		}
		d, ok := s.eventDetails[e.EventID]
		if !ok || strings.TrimSpace(d.HTML) == "" {
			continue
		}
		if seen[e.EventID] {
			continue // a duplicate id in the feed; one reading is enough
		}
		seen[e.EventID] = true
		if c, hit := s.eventRaidCache[e.EventID]; hit && c.fetchedAt.Equal(d.FetchedAt) && c.start == e.Start && c.end == e.End {
			out = append(out, c.windows...)
			continue
		}
		w := eventPageRaidWindows(e, d.HTML)
		s.eventRaidCache[e.EventID] = eventRaidCacheEntry{fetchedAt: d.FetchedAt, start: e.Start, end: e.End, windows: w}
		out = append(out, w...)
	}
	for id := range s.eventRaidCache {
		if !seen[id] {
			delete(s.eventRaidCache, id)
		}
	}
	return out
}
