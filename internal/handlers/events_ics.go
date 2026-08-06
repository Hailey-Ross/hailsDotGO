package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"pogo.hails.cc/internal/pogodata"
)

// icsEvent is the slice of the ScrapedDuck/LeekDuck event feed we need to build
// a calendar entry. The feed is the same one served at /api/events.
type icsEvent struct {
	EventID   string `json:"eventID"`
	Name      string `json:"name"`
	EventType string `json:"eventType"`
	Heading   string `json:"heading"`
	Link      string `json:"link"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// eventTypePattern accepts the same characters as eventIDPattern, underscore
// included. Upstream identifiers do use them (every GO Battle League id does),
// and a type this refuses is dropped silently at the filter below, which turns
// a subscription into an empty calendar with nothing to explain it.
var eventTypePattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// EventsICS serves the Pokemon GO event feed as an iCalendar (RFC 5545) file so
// users can subscribe in Google/Apple Calendar and set native alarms.
//
// The whole point of the feed is correct timezones. The upstream data encodes
// two kinds of times and we must preserve the distinction:
//   - Local events (Spotlight Hour, Community Day): no "Z" suffix. These are
//     floating wall-clock times ("6pm wherever you are"), emitted as a DATE-TIME
//     with no Z and no TZID so each subscriber's app reads them in its own zone.
//   - Global events (GO Battle League rotations): "Z" suffix = true UTC, emitted
//     with the trailing Z so clients convert to the viewer's local time.
//
// Optional ?types=a,b filters to those eventType values; absent means all.
func (h *Handlers) EventsICS(w http.ResponseWriter, r *http.Request) {
	if !h.maintenanceSettings().EventsEnabled {
		http.NotFound(w, r)
		return
	}

	// Build the eventType allow-set from ?types= (validated, comma separated).
	var allow map[string]bool
	if raw := r.URL.Query().Get("types"); strings.TrimSpace(raw) != "" {
		allow = map[string]bool{}
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.TrimSpace(tok)
			if eventTypePattern.MatchString(tok) {
				allow[tok] = true
			}
		}
	}

	var events []icsEvent
	if raw := h.store.Events(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &events); err != nil {
			log.Printf("EventsICS: unmarshal events: %v", err)
			// Fall through and emit a valid, empty calendar rather than 500.
		}
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")

	var b strings.Builder
	writeICSLine(&b, "BEGIN:VCALENDAR")
	writeICSLine(&b, "VERSION:2.0")
	writeICSLine(&b, "PRODID:-//hailsDotGO//Events//EN")
	writeICSLine(&b, "CALSCALE:GREGORIAN")
	writeICSLine(&b, "METHOD:PUBLISH")
	writeICSLine(&b, "X-WR-CALNAME:Pokémon GO Events")
	writeICSLine(&b, "X-WR-CALDESC:Pokémon GO events from pogo.hails.app (data by LeekDuck)")
	// Ask subscribers to re-poll roughly as often as the upstream feed refreshes.
	writeICSLine(&b, "REFRESH-INTERVAL;VALUE=DURATION:PT6H")
	writeICSLine(&b, "X-PUBLISHED-TTL:PT6H")

	for _, ev := range events {
		if allow != nil && !allow[ev.EventType] {
			continue
		}
		writeVEVENT(&b, ev, stamp)
	}

	writeICSLine(&b, "END:VCALENDAR")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="pogo-events.ics"`)
	w.Header().Set("Cache-Control", "public, max-age=1800")
	w.Write([]byte(b.String()))
}

// eventIDPattern validates an upstream event id before it is used to look up a
// cached page or to build a download filename.
//
// The underscore is load bearing: every GO Battle League rotation is keyed like
// "gbl-forever-forward_great-league_ultra-league", so a class without it rejects
// a real event. There used to be a second, stricter copy of this in handlers.go
// that had no underscore, and it made the detail endpoint return 400 for every
// GBL event while their pages sat scraped and ready in the cache. One definition
// now, so the two cannot drift apart again.
//
// Do not widen this further. The value reaches a quoted filename in the
// Content-Disposition header below, where the current class is inert.
var eventIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,128}$`)

// EventICS serves a single event as a downloadable iCalendar file, so a phone
// opens it in its native calendar app (Apple Calendar on iOS, Google Calendar on
// Android). This is more reliable on mobile than an in-browser blob, and than a
// Google web link, which cannot be forced into the app on iOS. Same floating vs
// UTC handling as the feed. The event is selected by ?id=<eventID>.
func (h *Handlers) EventICS(w http.ResponseWriter, r *http.Request) {
	if !h.maintenanceSettings().EventsEnabled {
		http.NotFound(w, r)
		return
	}

	id := r.URL.Query().Get("id")
	if !eventIDPattern.MatchString(id) {
		http.Error(w, "invalid event id", http.StatusBadRequest)
		return
	}

	var events []icsEvent
	if raw := h.store.Events(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &events); err != nil {
			log.Printf("EventICS: unmarshal events: %v", err)
		}
	}
	var found *icsEvent
	for i := range events {
		if events[i].EventID == id {
			found = &events[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	var b strings.Builder
	writeICSLine(&b, "BEGIN:VCALENDAR")
	writeICSLine(&b, "VERSION:2.0")
	writeICSLine(&b, "PRODID:-//hailsDotGO//Events//EN")
	writeICSLine(&b, "CALSCALE:GREGORIAN")
	writeICSLine(&b, "METHOD:PUBLISH")
	if !writeVEVENT(&b, *found, stamp) {
		// No parseable start: nothing to add to a calendar.
		http.NotFound(w, r)
		return
	}
	writeICSLine(&b, "END:VCALENDAR")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pogo-`+id+`.ics"`)
	w.Header().Set("Cache-Control", "public, max-age=1800")
	w.Write([]byte(b.String()))
}

// writeVEVENT writes one VEVENT for ev and reports whether it was written (it is
// skipped, returning false, when the start time is missing or unparseable). Both
// the feed and the single-event endpoint use it so their output stays identical.
func writeVEVENT(b *strings.Builder, ev icsEvent, stamp string) bool {
	dtStart, startFloating, ok := formatICSTimeForm(ev.Start)
	if !ok {
		return false
	}
	writeICSLine(b, "BEGIN:VEVENT")
	// The feed endpoint iterates raw upstream records, so unlike the single event
	// endpoint nothing has checked this id. Fall back to a synthetic one rather
	// than let an unexpected shape become part of a subscriber's stored UID.
	uid := ev.EventID
	if !eventIDPattern.MatchString(uid) {
		uid = "e" + dtStart
	}
	writeICSLine(b, "UID:"+uid+"@pogo.hails.live")
	writeICSLine(b, "DTSTAMP:"+stamp)
	writeICSLine(b, "DTSTART:"+dtStart)
	if dtEnd, endFloating, ok := formatICSTimeForm(ev.End); ok {
		// Both endpoints must be the same kind of time. A floating start beside a
		// UTC end leaves the duration undefined: the client has to invent a zone
		// for the floating half, and they do not agree on which. An event with no
		// DTEND is a well defined instant, so dropping it beats an ambiguous pair.
		if endFloating == startFloating {
			writeICSLine(b, "DTEND:"+dtEnd)
		} else {
			log.Printf("EventsICS: %s has a %s start and a %s end, omitting DTEND",
				ev.EventID, formName(startFloating), formName(endFloating))
		}
	}
	writeICSLine(b, "SUMMARY:"+icsEscape(ev.Name))
	desc := ev.Heading
	if ev.Link != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += ev.Link
	}
	if desc != "" {
		writeICSLine(b, "DESCRIPTION:"+icsEscape(desc))
	}
	if ev.Link != "" {
		// URL is a URI-typed property, so it is not TEXT-escaped.
		writeICSLine(b, "URL:"+ev.Link)
	}
	if ev.EventType != "" {
		writeICSLine(b, "CATEGORIES:"+icsEscape(ev.EventType))
	}
	writeICSLine(b, "END:VEVENT")
	return true
}

// formatICSTime converts an upstream ISO timestamp to an iCalendar DATE-TIME.
// A trailing "Z" is preserved as UTC; otherwise the value is emitted floating
// (no zone), so calendar clients interpret it in the viewer's local timezone.
//
// The layouts live in pogodata.ParseFeedTime because the detail scraper needs
// exactly the same understanding of the feed's formats. It used to carry its
// own single layout and read floating times as UTC, which is the drift this
// shared helper exists to prevent.
func formatICSTime(raw string) (string, bool) {
	s, _, ok := formatICSTimeForm(raw)
	return s, ok
}

// formatICSTimeForm is formatICSTime plus which form the value came out as, so
// a caller can refuse to pair a floating endpoint with an absolute one.
func formatICSTimeForm(raw string) (formatted string, floating bool, ok bool) {
	// time.UTC, not time.Local: only the wall clock components are wanted here.
	// A floating value is emitted with no zone at all, so the location it was
	// parsed in must not be allowed to shift the digits.
	t, floating, ok := pogodata.ParseFeedTime(raw, time.UTC)
	if !ok {
		return "", false, false
	}
	if floating {
		return t.Format("20060102T150405"), true, true
	}
	return t.UTC().Format("20060102T150405Z"), false, true
}

// formName labels a DATE-TIME form for a log line.
func formName(floating bool) string {
	if floating {
		return "floating"
	}
	return "UTC"
}

// icsEscape escapes a value for an iCalendar TEXT property per RFC 5545 3.3.11.
func icsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	return s
}

// icsStripControls removes the characters that would end a content line early.
//
// RFC 5545 forbids them outright, but the reason this exists is structural: a CR
// or LF inside a value does not corrupt the line, it CREATES one. Since a feed
// supplied value reaches UID and URL without going through icsEscape (URL is
// URI typed and must not be TEXT escaped, so escaping is not the answer there),
// a newline in either would write an attacker chosen iCalendar property into
// every subscriber's calendar, where it would outlive any fix here until each
// client next polls.
//
// It is safe on legitimate values: icsEscape has already turned real newlines
// into the literal two character \n before a TEXT property gets here, and no
// valid URL or event id contains a raw newline. Commas and semicolons, which do
// appear in real links, are untouched.
func icsStripControls(s string) string {
	if !strings.ContainsAny(s, "\r\n\x00") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// writeICSLine folds a content line to 75 octets (RFC 5545 3.1) and terminates
// it with CRLF. Folding happens on rune boundaries so multibyte UTF-8 is never
// split; continuation lines begin with a single space.
//
// Stripping happens here rather than at the call sites so that every property,
// including any added later, inherits it. This is the only function that writes
// a line into the calendar.
func writeICSLine(b *strings.Builder, line string) {
	line = icsStripControls(line)
	const max = 75
	if len(line) <= max {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	col := 0
	for _, r := range line {
		rl := utf8.RuneLen(r)
		if col+rl > max {
			b.WriteString("\r\n ")
			col = 1 // the leading space counts toward the next line
		}
		b.WriteRune(r)
		col += rl
	}
	b.WriteString("\r\n")
}
