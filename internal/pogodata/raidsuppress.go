package pogodata

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

// This is the one thing on an event page that can remove a boss.
//
// eventraids.go argues the opposite case at length, and is right to: a page's Raids
// section says what IS running and never what is not, so those windows are additive
// and can only ever add. But a page sometimes carries a second, separate statement,
// in prose, that a whole class of rotation is suspended. Mega Ascension carried one:
//
//	Daily Discoveries, Seasonal Mega Raids, Seasonal Five-Star Raids, Seasonal
//	Shadow Raids, Seasonal Raid Hours, and Seasonal Spotlight Hours will not take
//	place during the Mega Ascension event and Pokemon GO Fest 2026: Mega Finale.
//	Mega Ascension and Mega Finale Raids will take their place starting Monday,
//	August 31, at 12:01 a.m. to Sunday, September 6, 2026, at 11:59 p.m. local time.
//
// Nothing else in the app can see that. The events feed keeps serving the rotations
// it announced weeks earlier, unchanged and un-truncated: on 2026-09-01 it still had
// Regirock, Regice and Registeel running to the 8th, and Shadow Giratina with them.
// So the site showed all four as live for a week in which none of them existed, two
// of them synthesized out of a window the feed had simply not corrected, and the
// other two carried by a pokemon-go-api snapshot that had not caught up either.
//
// Because this rule deletes bosses rather than adding them, every judgement in this
// file goes the cautious way and every failure fails open:
//
//   - The word "seasonal" is mandatory in every category phrase, so ordinary page
//     prose cannot empty a tier by accident.
//   - The category and the "will not take place" trigger must be in the SAME
//     sentence, so two unrelated statements cannot be read as one.
//   - The span is read at its narrowest, the mirror of raidWindowSpan, so a group
//     is suppressed only while it is suppressed for everyone on Earth.
//   - A note that will not parse, or names no group this app schedules, produces no
//     suppression at all, which is exactly the behaviour that shipped before it.
//   - And reconcileRaids disarms the whole thing if applying it would leave every
//     governed tier empty.

// RaidSuppression is one event page's statement that a class of rotation is not
// running, for a span the page states itself.
type RaidSuppression struct {
	EventID string
	Name    string
	// Groups are raidGroupKey values ("5", "5:shadow", "6"), sorted and deduped.
	// Never empty: a note naming no group this app governs produces no suppression
	// rather than one that silences nothing.
	Groups []string
	// RawStart and RawEnd are floating wall clock readings, like every other
	// rotation string in this app, because that is what the prose states.
	RawStart string
	RawEnd   string
	// StartsUTC and EndsUTC are the NARROW reading of those strings. See
	// suppressionSpan for why they cannot be the wide one.
	StartsUTC time.Time
	EndsUTC   time.Time
	// Text is the note verbatim, so the admin scraper check can show what a tier
	// went missing on the strength of.
	Text string
}

// Active reports whether the suppression is in force for every trainer on Earth.
func (s RaidSuppression) Active(now time.Time) bool {
	return !now.Before(s.StartsUTC) && now.Before(s.EndsUTC)
}

// Suppresses reports whether this note names one schedulable group.
func (s RaidSuppression) Suppresses(group string) bool {
	for _, g := range s.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// Silences reports whether this note, on its own, removes one rotation at one
// instant. Use silencedBy rather than this to decide anything: with two notes in
// force one of them can exempt a rotation the other would take.
//
// Naming the group is not enough, because a note does two things in one breath: it
// says the schedule announced BEFORE it does not apply, and it promises replacements.
// So a rotation that STARTS inside the note's own span is never silenced by it. By
// construction that rotation is one of the replacements.
//
// Armored Mewtwo is the live example and the reason this test exists. It arrives as
// its own raid-battles entry opening on the Mega Finale Saturday, squarely inside
// the Mega Ascension note, and reading it as a casualty of that note deleted the
// weekend's only five star raid from the grid and from the up next strip both. The
// rotations the note really is talking about are the seasonal ones that were already
// running across its start: the Regis from 26 August, Shadow Giratina from the 5th.
//
// Additive windows are exempt outright, for the reason in RaidWindow.Additive: they
// are read off the same event pages that carry these notes, and a page that suspends
// a tier is the page that repopulates it.
func (s RaidSuppression) Silences(w RaidWindow, at time.Time) bool {
	if w.Additive || !s.Active(at) || !s.Suppresses(raidGroupKey(w.Tier, w.Shadow)) {
		return false
	}
	return s.opensAfter(w)
}

// opensAfter reports whether the note begins after the rotation already had.
//
// It compares the two STATED wall clocks, not StartsUTC against StartsUTC. Those two
// fields are readings of the same kind of floating string in deliberately different
// zones: a rotation opens on the UTC+14 reading so nobody is missing a boss they can
// already raid, a note opens on the UTC-12 reading so nothing is deleted early. They
// are 26 hours apart, and comparing them directly shifted this test a full day late,
// which read every rotation opening on the note's own start day as a casualty of it
// rather than as the replacement the note was announcing.
//
// An unreadable string is not a licence to delete a boss, so it answers false.
func (s RaidSuppression) opensAfter(w RaidWindow) bool {
	ws, _, okW := ParseFeedTime(w.RawStart, time.UTC)
	ss, _, okS := ParseFeedTime(s.RawStart, time.UTC)
	if !okW || !okS {
		return false
	}
	return ws.Before(ss)
}

// suppressionCategories maps the phrase a seasonal schedule note uses for a class
// of rotation onto the group it governs.
//
// The word "seasonal" is mandatory in every phrase and is the single most load
// bearing thing in this file. Without it, page prose reading "Mega Raids will not
// take place at EX Gyms" empties the entire Mega tier. With it, the trigger is
// Niantic's own boilerplate for exactly this announcement and nothing else.
//
// Deliberately absent, though the live note names all three: "daily discoveries",
// "seasonal raid hours" and "seasonal spotlight hours". None is a rotation this app
// schedules, since parseRaidWindows already refuses raid-hour and spotlight-hour
// entries, so there is nothing for them to suppress. A note naming only those
// yields no suppression, which is the correct answer rather than a failure.
var suppressionCategories = []struct {
	phrase string
	tier   string
	shadow bool
}{
	{"seasonal five-star raids", "5", false},
	{"seasonal 5-star raids", "5", false},
	{"seasonal shadow raids", "5", true},
	{"seasonal mega raids", "6", false},
}

// suppressionTriggerRe is the announcement's own wording. A sentence without it is
// not a suppression no matter which categories it names, which is what keeps
// "Seasonal Raid Bosses may also appear during this time" inert.
var suppressionTriggerRe = regexp.MustCompile(`(?i)will not (take place|be taking place|occur|be held)`)

// suppressionBlocks is every element that can hold a whole note.
//
// Block level, not the whole document: the categories and the trigger have to share
// a sentence and the span has to share a paragraph, and flattening the page would
// let a category on one side of a section header pair with a trigger on the other.
//
// A container in this list can still match as well as the paragraph inside it, so a
// note is read twice. That is only harmless because both readings now produce the
// same span: blockText keeps the sentence boundaries the container would otherwise
// erase, and the dates are taken from the triggering sentence onwards rather than
// from the top of the block, so a sibling paragraph's dates cannot leak into the
// container's reading. The dedupe at the end of parseSuppressionNotes then collapses
// the pair. Weaken either of those two rules and this list starts inventing notes.
const suppressionBlocks = "p, li, blockquote, td"

// suppressionMinLen is the shortest a real note can be. The live one is 380
// characters; this only exists to skip the hundreds of short blocks on a page
// before any regex touches them.
const suppressionMinLen = 40

// suppressionNote is one parsed note before it is attributed to an event, the same
// shape relationship eventPageBoss has to RaidWindow.
type suppressionNote struct {
	groups   []string
	rawStart string
	rawEnd   string
	text     string
}

// parseSuppressionNotes reads every seasonal schedule note out of one sanitized
// event page. evStart and evEnd supply a year the prose leaves out, and nothing
// else: see resolveNoteDate.
func parseSuppressionNotes(pageHTML string, evStart, evEnd time.Time) []suppressionNote {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	var out []suppressionNote
	seen := map[string]bool{}
	doc.Find(suppressionBlocks).Each(func(_ int, sel *goquery.Selection) {
		text := strings.Join(strings.Fields(blockText(sel)), " ")
		if len(text) < suppressionMinLen || !suppressionTriggerRe.MatchString(text) {
			return
		}
		sentences := splitSentences(text)
		trigger := -1
		for i, s := range sentences {
			if suppressionTriggerRe.MatchString(s) {
				trigger = i
				break
			}
		}
		if trigger < 0 {
			return
		}
		groups := suppressionGroups(sentences)
		if len(groups) == 0 {
			return
		}
		// From the triggering sentence onwards, never before it. The categories are
		// in one sentence and the span is in the next, so the whole block looked like
		// the obvious place to read dates from, and it is not: an unrelated date in
		// an earlier sentence, or in a sibling paragraph swallowed by a containing
		// element, became the suppression's start. That silences three tiers for a
		// week the note says nothing about and leaves the week it does describe
		// running, both from one paragraph of ordinary event prose.
		dates := findNoteDates(strings.Join(sentences[trigger:], " "))
		if len(dates) < 2 {
			return
		}
		startDay, ok := resolveNoteDate(dates[0], evStart, evEnd)
		if !ok {
			return
		}
		endDay, ok := resolveNoteDate(dates[1], evStart, evEnd)
		if !ok {
			return
		}
		if endDay.Before(startDay) && dates[1].year == 0 {
			// The one case worth a retry: a note running across New Year writes no
			// year on either end, so the second resolves into the year the first came
			// from and lands before it.
			retry := dates[1]
			retry.year = startDay.Year() + 1
			if d, ok := resolveNoteDate(retry, evStart, evEnd); ok {
				endDay = d
			}
		}
		n := suppressionNote{
			groups:   groups,
			rawStart: startDay.Format("2006-01-02") + noteClock(dates[0], eventPageDayStart),
			rawEnd:   endDay.Format("2006-01-02") + noteClock(dates[1], eventPageDayEnd),
			text:     text,
		}
		if _, _, ok := suppressionSpan(n.rawStart, n.rawEnd); !ok {
			return
		}
		key := strings.Join(n.groups, ",") + "|" + n.rawStart + "|" + n.rawEnd
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, n)
	})
	return out
}

// suppressionGroups reads the groups a block suspends, taking categories only from
// sentences that carry the trigger themselves.
//
// That scoping is the whole defence against a page saying two things at once. It is
// what keeps "Seasonal Mega Raids will continue as normal. Daily Adventure Incense
// will not take place." from emptying the Mega tier.
func suppressionGroups(sentences []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sentence := range sentences {
		if !suppressionTriggerRe.MatchString(sentence) {
			continue
		}
		hay := strings.ToLower(strings.Join(strings.Fields(sentence), " "))
		for _, c := range suppressionCategories {
			if !strings.Contains(hay, c.phrase) {
				continue
			}
			g := raidGroupKey(c.tier, c.shadow)
			if seen[g] {
				continue
			}
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// blockText is the text of one block with element boundaries kept as whitespace.
//
// goquery's Text() concatenates descendant text nodes with nothing between them, and
// the cached pages are minified, so "…as normal.</p><p>Daily Adventure Incense…"
// arrives as "as normal.Daily Adventure Incense". splitSentences needs a full stop, a
// space and a capital, finds none, and hands back two unrelated paragraphs as one
// sentence. That defeats the single defence this file leans on hardest: a category
// and a trigger are supposed to have to share a sentence, and here they never did.
// "Seasonal Mega Raids will continue as normal" plus a suspension of something else
// entirely emptied the Mega tier for a week.
func blockText(sel *goquery.Selection) string {
	var sb strings.Builder
	var walk func(*goquery.Selection)
	walk = func(s *goquery.Selection) {
		s.Contents().Each(func(_ int, c *goquery.Selection) {
			if goquery.NodeName(c) == "#text" {
				sb.WriteString(c.Text())
				return
			}
			sb.WriteByte(' ')
			walk(c)
			sb.WriteByte(' ')
		})
	}
	walk(sel)
	return sb.String()
}

// splitSentences cuts prose at a full stop followed by a space and a capital.
//
// A scan rather than regexp.Split for two reasons: Go has no lookahead, so the
// capital that ends the boundary must not be eaten, and the capital rule is what
// survives the one abbreviation these notes actually contain. "12:01 a.m. to
// Sunday" is not a boundary because "to" is lower case, while "Mega Finale. Mega
// Ascension" is.
func splitSentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for i := 0; i+2 < len(runes); i++ {
		if runes[i] != '.' || runes[i+1] != ' ' || !unicode.IsUpper(runes[i+2]) {
			continue
		}
		if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
			out = append(out, s)
		}
		start = i + 2
	}
	if s := strings.TrimSpace(string(runes[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// noteDate is one "Monday, August 31, 2026, at 12:01 a.m." occurrence, unresolved.
type noteDate struct {
	month      time.Month
	dom        int
	year       int // 0 when the prose writes none
	weekday    time.Weekday
	hasWeekday bool
	hour, min  int
	pm         bool
	hasClock   bool
}

// noteDateRe keeps weekday, month, day, optional year and optional clock together
// and in order, so a two date sentence yields two ordered results.
//
// headingMonthDayRe and headingWeekdayRe in eventraids.go cannot be reused: they are
// two separate expressions with no year and no clock, and matching them
// independently would pair the first weekday on the block with the second month.
var noteDateRe = regexp.MustCompile(`(?i)` +
	`(?:\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday),?\s+)?` +
	`\b(january|february|march|april|may|june|july|august|september|october|november|december)\s+` +
	`(\d{1,2})(?:st|nd|rd|th)?` +
	`(?:,?\s+(\d{4}))?` +
	`(?:,?\s+at\s+(\d{1,2}):(\d{2})\s*([ap])\.?\s?m\.?)?`)

// findNoteDates returns every date the note names, in the order it names them.
//
// A bare "Month D" is not accepted. One of the three corroborating parts has to be
// there as well: a weekday, a year, or a time of day. "May" is also an ordinary
// English verb, so "Trainers may 2 times over" parses as a date otherwise, and a
// date this reader believes is a date it will suspend three tiers on. Every real
// note states at least a weekday and a clock, so nothing is lost by insisting.
func findNoteDates(text string) []noteDate {
	var out []noteDate
	for _, m := range noteDateRe.FindAllStringSubmatch(text, -1) {
		d := noteDate{
			month: headingMonths[strings.ToLower(m[2])],
			dom:   atoiSafe(m[3]),
		}
		if m[1] != "" {
			d.weekday, d.hasWeekday = headingWeekdays[strings.ToLower(m[1])], true
		}
		if m[4] != "" {
			d.year = atoiSafe(m[4])
		}
		if m[5] != "" {
			d.hour, d.min, d.hasClock = atoiSafe(m[5]), atoiSafe(m[6]), true
			d.pm = strings.EqualFold(m[7], "p")
		}
		if !d.hasWeekday && d.year == 0 && !d.hasClock {
			continue
		}
		out = append(out, d)
	}
	return out
}

// resolveNoteDate turns one parsed date into a calendar day, failing closed.
//
// resolveHeadingDay is deliberately not reused. It CLAMPS to the event's own span,
// and the note that motivated this file outlives its event: Mega Ascension ends
// 2026-09-04 while its note runs to 2026-09-06. Clamping would truncate the
// suppression and let every seasonal rotation back onto the grid for the whole GO
// Fest weekend. The span is used here only as a source of candidate years.
func resolveNoteDate(d noteDate, evStart, evEnd time.Time) (time.Time, bool) {
	if d.dom < 1 || d.dom > 31 || d.month == 0 {
		return time.Time{}, false
	}
	var years []int
	if d.year > 0 {
		years = []int{d.year}
	} else {
		if evStart.IsZero() || evEnd.IsZero() || evEnd.Before(evStart) {
			return time.Time{}, false
		}
		years = append(years, evStart.Year())
		if evEnd.Year() != evStart.Year() {
			years = append(years, evEnd.Year())
		}
	}
	var hits []time.Time
	for _, y := range years {
		t := time.Date(y, d.month, d.dom, 0, 0, 0, 0, time.UTC)
		if t.Day() != d.dom || t.Month() != d.month {
			continue // a date that does not exist, such as February 30
		}
		if d.hasWeekday && t.Weekday() != d.weekday {
			continue
		}
		hits = append(hits, t)
	}
	if len(hits) != 1 {
		return time.Time{}, false
	}
	return hits[0], true
}

// noteClock renders a parsed time of day as a floating wall clock suffix, falling
// back to the caller's day bound when the prose states no time.
//
// The fallbacks are eventPageDayStart and eventPageDayEnd, the same two constants a
// day scoped rotation uses, so a note that states only days covers the same ground.
func noteClock(d noteDate, fallback string) string {
	if !d.hasClock || d.hour < 1 || d.hour > 12 || d.min < 0 || d.min > 59 {
		return fallback
	}
	h := d.hour
	if h == 12 {
		h = 0
	}
	if d.pm {
		h += 12
	}
	return fmt.Sprintf("T%02d:%02d:00.000", h, d.min)
}

// suppressionSpan resolves a note's stated span into the window during which the
// suppression holds for everyone on Earth. It is the mirror of raidWindowSpan.
//
// raidWindowSpan reads a rotation at its WIDEST: it opens in UTC+14, the first zone
// to reach the wall clock, and closes in UTC-12, the last. That is the right reading
// for "is this boss live for anybody".
//
// A suppression asks the opposite question. It REMOVES bosses, and removing one
// people can still raid is the failure worth avoiding, so it is read at its
// NARROWEST: it opens once the wall clock has reached the last zone on Earth and
// closes the moment the first zone leaves it.
//
// The two readings differ by 26 hours at each edge, so the seasonal rotations stay
// on the grid for a day either side of a note. That is not slack, it is the truth:
// for that day they really are still running for somebody.
//
// One consequence worth knowing: a note stating a span shorter than 26 hours
// suppresses nothing at all, because the narrow reading inverts and !e.After(s)
// refuses it. A one afternoon "Seasonal Mega Raids will not take place from 10:00
// a.m. to 6:00 p.m." is therefore ignored, which is the right answer for a rule that
// deletes bosses off a page it scraped.
func suppressionSpan(rawStart, rawEnd string) (start, end time.Time, ok bool) {
	s, _, sOK := ParseFeedTime(rawStart, anywhereOnEarth)
	e, _, eOK := ParseFeedTime(rawEnd, earliestOnEarth)
	if !sOK || !eOK || !e.After(s) {
		return time.Time{}, time.Time{}, false
	}
	return s.UTC(), e.UTC(), true
}

// eventPageSuppressions turns one feed entry plus its scraped page into
// suppressions, mirroring eventPageRaidWindows.
func eventPageSuppressions(e raidFeedEntry, pageHTML string) []RaidSuppression {
	evStart, _, okStart := ParseFeedTime(e.Start, time.UTC)
	evEnd, _, okEnd := ParseFeedTime(e.End, time.UTC)
	if !okStart || !okEnd {
		return nil
	}
	notes := parseSuppressionNotes(pageHTML, evStart, evEnd)
	if len(notes) == 0 {
		return nil
	}
	out := make([]RaidSuppression, 0, len(notes))
	for _, n := range notes {
		start, end, ok := suppressionSpan(n.rawStart, n.rawEnd)
		if !ok {
			continue // parseSuppressionNotes already refused these; belt and braces
		}
		log.Printf("pogodata: raid schedule: event page %q suspends %v from %s to %s",
			e.EventID, n.groups, n.rawStart, n.rawEnd)
		out = append(out, RaidSuppression{
			EventID:   e.EventID,
			Name:      e.Name,
			Groups:    n.groups,
			RawStart:  n.rawStart,
			RawEnd:    n.rawEnd,
			StartsUTC: start,
			EndsUTC:   end,
			Text:      n.text,
		})
	}
	return out
}

// suppressionSummary names the pages a suspension was read off, and the span each
// one states, for the admin scraper check.
func suppressionSummary(sups []RaidSuppression) string {
	if len(sups) == 0 {
		return ""
	}
	seen := map[string]bool{}
	parts := make([]string, 0, len(sups))
	for _, s := range sups {
		part := fmt.Sprintf("%s %v %s to %s", s.EventID, s.Groups, s.RawStart, s.RawEnd)
		if seen[part] {
			continue
		}
		seen[part] = true
		parts = append(parts, part)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// suppressionHoldsPage reports whether a scraped page is still the source of a
// suspension that has not expired, and must therefore survive cache eviction.
//
// A note may legitimately outlive the event that carries it: resolveNoteDate does
// not clamp to the event span, deliberately, and the live Mega Ascension note runs
// to 2026-09-06 while its own event ends on the 4th. The page cache has the opposite
// rule, evicting a page the moment its event is over anywhere on Earth, so without
// this the note would vanish around 22 hours before it expires and every seasonal
// rotation would come back onto the grid for the rest of the suspension. Today that
// is masked only by coincidence: the GO Fest Mega Finale page carries a byte
// identical note and outlives the Mega Ascension one.
//
// It holds only the page a live note was read FROM, so nothing else lingers.
func suppressionHoldsPage(sups []RaidSuppression, eventID string, now time.Time) bool {
	if eventID == "" {
		return false
	}
	for _, s := range sups {
		if s.EventID == eventID && now.Before(s.EndsUTC) {
			return true
		}
	}
	return false
}

// silencedBy reports whether the notes in force remove this rotation.
//
// Being a replacement is a VETO, not a vote. A rotation that opens inside any live
// note is one of the replacements that note promised, and stays even if a second,
// later starting note would otherwise take it. Two pages carrying overlapping notes
// is the normal case, not an exotic one: Mega Ascension and the GO Fest Mega Finale
// both carry the same wording today, and the moment the Finale page states its own
// weekend instead, Armored Mewtwo opens before that note and would be deleted by it
// while being the very raid it is promising.
func silencedBy(sups []RaidSuppression, w RaidWindow, at time.Time) bool {
	if w.Additive {
		return false
	}
	group := raidGroupKey(w.Tier, w.Shadow)
	silenced := false
	for _, s := range sups {
		if !s.Active(at) || !s.Suppresses(group) {
			continue
		}
		if !s.opensAfter(w) {
			return false
		}
		silenced = true
	}
	return silenced
}
