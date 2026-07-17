package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
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

var eventTypePattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

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

var eventICSIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,128}$`)

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
	if !eventICSIDPattern.MatchString(id) {
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
	dtStart, ok := formatICSTime(ev.Start)
	if !ok {
		return false
	}
	writeICSLine(b, "BEGIN:VEVENT")
	uid := ev.EventID
	if uid == "" {
		uid = "e" + dtStart
	}
	writeICSLine(b, "UID:"+uid+"@pogo.hails.live")
	writeICSLine(b, "DTSTAMP:"+stamp)
	writeICSLine(b, "DTSTART:"+dtStart)
	if dtEnd, ok := formatICSTime(ev.End); ok {
		writeICSLine(b, "DTEND:"+dtEnd)
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
func formatICSTime(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasSuffix(raw, "Z") {
		for _, layout := range []string{"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z", time.RFC3339} {
			if t, err := time.Parse(layout, raw); err == nil {
				return t.UTC().Format("20060102T150405Z"), true
			}
		}
		return "", false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			// Parsed as UTC, but we only take the wall-clock components, so the
			// emitted value is floating (no Z, no TZID).
			return t.Format("20060102T150405"), true
		}
	}
	return "", false
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

// writeICSLine folds a content line to 75 octets (RFC 5545 3.1) and terminates
// it with CRLF. Folding happens on rune boundaries so multibyte UTF-8 is never
// split; continuation lines begin with a single space.
func writeICSLine(b *strings.Builder, line string) {
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
