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
	// day is the first calendar day the headings scoped this boss to, or the zero
	// time when it belongs to the whole event. dayEnd is the last, and is zero for
	// the ordinary single day scope.
	day    time.Time
	dayEnd time.Time
}

// headingScope is what the headings above a boss list have established so far.
type headingScope struct {
	// day is zero when nothing above has narrowed the boss to a calendar day,
	// which means it belongs to the whole event. dayEnd closes a heading that names
	// a range rather than a day.
	day     time.Time
	dayEnd  time.Time
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
	if eventPageLocationLimited(section) {
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
	if len(out) == 0 {
		out = eventPageProseBosses(section)
	}
	return out
}

// eventLocationLimitedRes are the ways a Raids section says its raids are not
// available where the reader is standing.
//
// The LEGO event is why this exists. Its Raids section reads "The following Pokemon
// will appear more frequently in raids at participating LEGO Store locations" and
// "These raids are local only, Remote Raid Passes cannot be used", and its one boss
// is a Pikachu. Tier 1 became governed on 2026-09-01, and from that moment the
// reader was contributing that Pikachu as an ordinary worldwide tier 1 rotation
// with a two month window, on a grid whose whole promise is "these are the raids you
// can do". The comment on the tier guard below still claimed the Pikachu was being
// excluded; it had not been for a day.
//
// Three phrases, each specific enough to be Niantic boilerplate rather than English:
// swept across all sixty cached pages on 2026-09-01, only the LEGO page matches any
// of them, and only inside its Raids section.
//
// This does NOT give an event page the power to remove a boss, which is the one
// thing eventraids.go must never do. The reader declines to ADD one. Nothing here
// makes a tier authoritative and upstream's own tier 1 list is untouched.
var eventLocationLimitedRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)remote raid pass(?:es)? cannot be used`),
	regexp.MustCompile(`(?i)\braids are local only`),
	regexp.MustCompile(`(?i)\bat participating\b[^.]{0,60}\blocations?\b`),
}

// eventPageLocationLimited reports whether the Raids section says its raids are
// confined to particular venues.
//
// Scoped to the section on purpose. The same LEGO page says "limited and subject to
// stock remaining at participating locations" about a physical giveaway further
// down, which has nothing to do with raids, and reading the whole document would
// have caught it.
func eventPageLocationLimited(section *goquery.Selection) bool {
	limited := false
	section.Each(func(_ int, node *goquery.Selection) {
		if limited {
			return
		}
		blocks := node.Find("p, li")
		if goquery.NodeName(node) == "p" || goquery.NodeName(node) == "li" {
			blocks = blocks.AddSelection(node)
		}
		blocks.EachWithBreak(func(_ int, b *goquery.Selection) bool {
			for _, sentence := range splitSentences(strings.Join(strings.Fields(b.Text()), " ")) {
				// A sentence that qualifies the boilerplate rather than stating it is
				// left alone. "Remote Raid Passes cannot be used more than five times
				// a day, but they can be used for these raids" is the shape, and it
				// says the opposite of what the matchers below read it as. The guard
				// can only ever make this rule fire LESS, which for a rule that
				// removes a roster is the safe direction to be wrong in.
				if strings.Contains(sentence, " but ") || strings.Contains(sentence, " unless ") {
					continue
				}
				for _, re := range eventLocationLimitedRes {
					if re.MatchString(sentence) {
						limited = true
						return false
					}
				}
			}
			return true
		})
	})
	return limited
}

// eventProseDebutRe reads a boss named only in a sentence.
//
// Restricted to Mega and Primal on purpose. Those two carry their tier in the name,
// so isMegaName settles it with no heading to lean on, and a sentence is not a
// structure that can be trusted to say which tier anything else belongs to.
var eventProseDebutRe = regexp.MustCompile(`(?i)\b((?:mega|primal)\s+\p{L}[\p{L}'.-]*(?:\s+[XY]\b)?)\s+` +
	`(?:will\s+(?:make|be\s+making)\s+its\b|makes\s+its\b|will\s+debut\b|will\s+appear\s+in\b)`)

// eventPageProseBosses is the last resort, used only when a Raids section produced no
// list items at all.
//
// Super Mega Raid Day is why it exists. Its Raids section is a heading, one sentence
// and an EMPTY list element:
//
//	<h2 class="event-section-header raids">Raids</h2>
//	<h2>Featured Pokémon</h2>
//	<p>Mega Staraptor will make its Pokémon GO debut in Super Mega Raids.</p>
//	<ul class="pkmn-list-flex"></ul>
//
// A brand new Mega, debuting on a headline event day, named nowhere else in any feed
// this app reads. Requiring the structured roster to be empty first keeps this off
// every page that has real markup, so the only thing it can add is a boss that would
// otherwise have been lost entirely.
func eventPageProseBosses(section *goquery.Selection) []eventPageBoss {
	seen := map[string]bool{}
	var out []eventPageBoss
	section.Each(func(_ int, node *goquery.Selection) {
		if goquery.NodeName(node) != "p" {
			return
		}
		text := strings.Join(strings.Fields(node.Text()), " ")
		for _, m := range eventProseDebutRe.FindAllStringSubmatch(text, -1) {
			name := strings.Join(strings.Fields(m[1]), " ")
			key := normalizeBossName(name)
			if seen[key] || !isMegaName(name) {
				continue
			}
			seen[key] = true
			out = append(out, eventPageBoss{boss: WindowBoss{Name: name}, tier: "6"})
		}
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
	if start, end, ok := resolveHeadingDays(text, evStart, evEnd); ok {
		base.day, base.dayEnd = start, end
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
		// govern. governedTiers has held 1 and 3 since 2026-09-01, so what is
		// refused here now is only a tier nothing established and the four star
		// raids of a Community Day page.
		//
		// The Pikachu under lego-pokemon-go-2026's "Appearing in 1-Star Raids" used
		// to be named here as the thing this line kept out. It is not: that is
		// eventPageLocationLimited's job, and it is kept out because those raids
		// happen in LEGO stores, not because of its tier.
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
		dayEnd: scope.dayEnd,
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

// headingDayRangeRe matches a heading that names a span of days rather than one day,
// in the shapes upstream writes: "September 8-15", "September 8 - September 15" and
// "Saturday, September 5 - Sunday, September 6". The separator is a hyphen, an en
// dash, an em dash or the word "to", because LeekDuck uses all four.
//
// The weekday on either side is optional and is CAPTURED rather than skipped. It used
// to be neither: a weekday before the SECOND date failed the whole pattern, so
// "Saturday, September 5 - Sunday, September 6" fell through to the single day reader,
// resolved as Saturday alone, and scoped the Sunday roster to Saturday. Upstream
// writes weekday qualified dates as a matter of course, mega-ascension heads all five
// of its day rosters that way, so a two day heading in the same house style was only
// ever one page away.
var headingDayRangeRe = regexp.MustCompile(`(?i)(?:(monday|tuesday|wednesday|thursday|friday|saturday|sunday),?\s+)?\b(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{1,2})\s*(?:[-\x{2013}\x{2014}]|to)\s*(?:(monday|tuesday|wednesday|thursday|friday|saturday|sunday),?\s+)?(?:(january|february|march|april|may|june|july|august|september|october|november|december)\s+)?(\d{1,2})\b`)

// resolveHeadingDays turns a heading into the span of days it names.
//
// A heading naming a single day answers with end equal to start, so every caller can
// treat the pair uniformly. A range used to collapse to its first day, because
// headingMonthDayRe matches once and stops: "September 8-15" became a one day window
// on the 8th. That was invisible while the feed happened to carry the same rotation
// with correct dates and preferRaidWindow picked the longer of the two, and it stops
// being invisible the moment a range headed roster is page-only, which is the exact
// situation this file exists for.
func resolveHeadingDays(text string, evStart, evEnd time.Time) (start, end time.Time, ok bool) {
	hay := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if m := headingDayRangeRe.FindStringSubmatch(hay); m != nil {
		firstWeekday, firstMonth, firstDay := m[1], m[2], m[3]
		lastWeekday, lastMonth, lastDay := m[4], m[5], m[6]
		if lastMonth == "" {
			lastMonth = firstMonth
		}
		// The weekdays are passed back through, so a range gets the same check a
		// single day heading gets: resolveHeadingDay refuses a date whose weekday the
		// prose contradicts, and that is the only defence against a wrong year on an
		// event whose span crosses a New Year. The range path used to drop them.
		s, sOK := resolveHeadingDay(strings.TrimSpace(firstWeekday+" "+firstMonth+" "+firstDay), evStart, evEnd)
		e, eOK := resolveHeadingDay(strings.TrimSpace(lastWeekday+" "+lastMonth+" "+lastDay), evStart, evEnd)
		if sOK && eOK && !e.Before(s) {
			return s, e, true
		}
		// The end of a range is routinely OUTSIDE the event's own span, and
		// resolveHeadingDay refuses anything outside it. Mega Squads is the live
		// case: its page heads two rosters "September 8-15" while its feed entry ends
		// on the 14th, so the 15th resolved to nothing and seven days of Mega
		// Beedrill were thrown away. The start has already fixed the year, so the end
		// does not need the span at all.
		if sOK {
			if e, ok := resolveRangeEnd(s, lastWeekday, lastMonth, atoiSafe(lastDay)); ok {
				return s, e, true
			}
			// A range that will not resolve as a range still resolves as its first
			// day, which is what this did before and is never worse than nothing.
			return s, s, true
		}
		return time.Time{}, time.Time{}, false
	}
	d, dOK := resolveHeadingDay(text, evStart, evEnd)
	if !dOK {
		return time.Time{}, time.Time{}, false
	}
	return d, d, true
}

// headingRangeMaxDays caps how far a range heading's end may sit from its start.
//
// A Raids section heading is describing one event's raids, so a span of weeks is
// ordinary and a span of months is a misread. The cap is what keeps this from
// turning a mangled heading into a rotation that never expires.
const headingRangeMaxDays = 60

// resolveRangeEnd closes a range whose end day the event's own span does not cover,
// using the year the START already settled.
//
// It rolls the year forward once, so "December 28 to January 3" closes on the
// January rather than refusing, and it refuses anything that is not a real date,
// runs backwards, or reaches further than headingRangeMaxDays.
func resolveRangeEnd(start time.Time, weekday, month string, dom int) (time.Time, bool) {
	mon, named := headingMonths[strings.ToLower(month)]
	if !named || dom < 1 || dom > 31 {
		return time.Time{}, false
	}
	wantDay, haveWeekday := headingWeekdays[strings.ToLower(weekday)]
	limit := start.AddDate(0, 0, headingRangeMaxDays)
	for _, year := range []int{start.Year(), start.Year() + 1} {
		d := time.Date(year, mon, dom, 0, 0, 0, 0, time.UTC)
		if d.Day() != dom || d.Month() != mon {
			continue // a date that does not exist, such as February 30
		}
		if d.Before(start) || d.After(limit) {
			continue
		}
		if haveWeekday && d.Weekday() != wantDay {
			// The prose says which day of the week this is and the calendar
			// disagrees, so one of them is wrong and neither is worth guessing on.
			continue
		}
		return d, true
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
		dayEnd string
	}
	order := make([]groupKey, 0, len(roster))
	groups := map[groupKey][]WindowBoss{}
	seen := map[groupKey]map[string]bool{}
	for _, r := range roster {
		k := groupKey{tier: r.tier, shadow: r.shadow}
		if !r.day.IsZero() {
			k.day = r.day.Format("2006-01-02")
			if !r.dayEnd.IsZero() && r.dayEnd.After(r.day) {
				k.dayEnd = r.dayEnd.Format("2006-01-02")
			}
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
			if k.dayEnd != "" {
				rawEnd = k.dayEnd + eventPageDayEnd
			}
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
	// suppressions rides along because it is derived from the same page under the
	// same key. The key already covers everything BOTH derivations read, so a second
	// memo would be a second copy of one invalidation rule and a second chance to
	// get it wrong. See raidsuppress.go.
	suppressions []RaidSuppression
}

// eventPageRaidsLocked derives the additive rotations and the suppressions from
// every scraped event page, in one pass. Caller must hold s.mu.
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
func (s *Store) eventPageRaidsLocked() ([]RaidWindow, []RaidSuppression) {
	if len(s.events) == 0 || len(s.eventDetails) == 0 {
		return nil, nil
	}
	var entries []raidFeedEntry
	if err := json.Unmarshal(s.events, &entries); err != nil {
		return nil, nil // parseRaidWindows has already logged this
	}
	if s.eventRaidCache == nil {
		s.eventRaidCache = map[string]eventRaidCacheEntry{}
	}
	seen := make(map[string]bool, len(entries))
	var out []RaidWindow
	var sups []RaidSuppression
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
			sups = append(sups, c.suppressions...)
			continue
		}
		w := eventPageRaidWindows(e, d.HTML)
		sp := eventPageSuppressions(e, d.HTML)
		s.eventRaidCache[e.EventID] = eventRaidCacheEntry{
			fetchedAt: d.FetchedAt, start: e.Start, end: e.End, windows: w, suppressions: sp,
		}
		out = append(out, w...)
		sups = append(sups, sp...)
	}
	for id := range s.eventRaidCache {
		if !seen[id] {
			delete(s.eventRaidCache, id)
		}
	}
	return out, sups
}
