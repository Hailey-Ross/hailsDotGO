package handlers

import (
	"strings"
	"testing"
)

// The calendar feed is subscribed to, not downloaded once, so its output is a
// contract: every VEVENT keeps the UID a subscriber's calendar already stored,
// and every timestamp keeps its floating vs UTC character. These tests pin the
// exact bytes so a refactor of the time handling cannot quietly reschedule an
// event in somebody's calendar or duplicate every entry they follow.

// The regression lock on the bug this audit found. There were two id patterns
// for the same value in this package, and the stricter one omitted the
// underscore. Every GO Battle League rotation is keyed with underscores, so the
// detail endpoint answered 400 for all of them while their scraped pages sat
// ready in the cache, and the browser quietly fell back to an empty modal.
//
// The accepted list below is real ids taken from the live feed, so this fails if
// anyone narrows the class again.
func TestEventIDPattern(t *testing.T) {
	accepted := []string{
		// GO Battle League, the ones that used to be rejected.
		"gbl-forever-forward_ultra-league_weather-cup-great-league-edition",
		"gbl-forever-forward_master-league_evolution-cup-great-league-edition",
		"gbl-forever-forward_great-league_scroll-cup-great-league-edition",
		"gbl-forever-forward_great-league_ultra-league_master-league-split-3",
		"gbl-forever-forward_great-league_ultra-league_master-league-mega-edition",
		// Ordinary ids, which always worked.
		"raidhour20260805",
		"pokemonspotlighthour2026-08-06",
		"max-mondays-2026-08-10",
		"fire-and-ice-hatch-day-2026",
		"a",
		strings.Repeat("a", 128),
	}
	for _, id := range accepted {
		if !eventIDPattern.MatchString(id) {
			t.Errorf("eventIDPattern rejected a real event id: %q", id)
		}
	}

	// The id reaches a map lookup and a quoted Content-Disposition filename, so
	// the class stays narrow. These must never be allowed back in.
	refused := []string{
		"",
		strings.Repeat("a", 129),
		"../../etc/passwd",
		"foo/bar",
		"foo\\bar",
		"UPPERCASE",
		"has space",
		"semi;colon",
		"quote\"mark",
		"newline\nid",
		"carriage\rreturn",
		"null\x00byte",
		"percent%20encoded",
		"dot.separated",
		"unicodeé",
	}
	for _, id := range refused {
		if eventIDPattern.MatchString(id) {
			t.Errorf("eventIDPattern accepted %q, which must not reach a lookup or a filename", id)
		}
	}
}

// formatICSTime has to preserve the difference the upstream feed encodes: a
// trailing Z is a real instant (GO Battle League rotations, which start at the
// same moment worldwide), and anything else is a wall clock that means the same
// reading in every timezone (Spotlight Hour is 6pm wherever you are).
func TestFormatICSTime(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"floating with milliseconds", "2026-08-05T18:00:00.000", "20260805T180000", true},
		{"floating without milliseconds", "2026-08-05T18:00:00", "20260805T180000", true},
		{"utc with milliseconds", "2026-08-04T20:00:00.000Z", "20260804T200000Z", true},
		{"utc without milliseconds", "2026-08-04T20:00:00Z", "20260804T200000Z", true},
		{"utc with odd fractional digits", "2026-08-04T20:00:00.123456Z", "20260804T200000Z", true},
		{"surrounding whitespace", "  2026-08-05T18:00:00.000  ", "20260805T180000", true},
		{"midnight floating", "2026-01-01T00:00:00.000", "20260101T000000", true},
		{"empty", "", "", false},
		{"garbage", "not a time", "", false},
		// A numeric offset is neither floating nor Z. The feed has never sent one,
		// and guessing at it would put the event an hour or two out, so it is
		// refused and the VEVENT is skipped rather than written wrong.
		{"numeric offset is refused", "2026-08-04T20:00:00+02:00", "", false},
		{"date only is refused", "2026-08-04", "", false},
	}
	for _, tc := range cases {
		got, ok := formatICSTime(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: formatICSTime(%q) = (%q, %v), want (%q, %v)",
				tc.name, tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// A floating time must never gain a Z, and a UTC time must never lose one. This
// is the single property that decides whether an alarm fires at the right hour.
func TestFormatICSTimeKeepsFloatingAndUTCApart(t *testing.T) {
	floating, ok := formatICSTime("2026-08-05T18:00:00.000")
	if !ok || strings.HasSuffix(floating, "Z") {
		t.Errorf("floating time gained a Z suffix: %q", floating)
	}
	utc, ok := formatICSTime("2026-08-05T18:00:00.000Z")
	if !ok || !strings.HasSuffix(utc, "Z") {
		t.Errorf("utc time lost its Z suffix: %q", utc)
	}
	if strings.TrimSuffix(utc, "Z") != floating {
		t.Errorf("same wall clock formatted differently: floating %q, utc %q", floating, utc)
	}
}

func TestICSEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain text`, `plain text`},
		{`a,b`, `a\,b`},
		{`a;b`, `a\;b`},
		{`a\b`, `a\\b`},
		{"line1\nline2", `line1\nline2`},
		{"line1\r\nline2", `line1\nline2`},
		{"line1\rline2", `line1\nline2`},
		// The backslash must be escaped first, or the escapes it introduces get
		// escaped again and the value arrives mangled.
		{`a\,b`, `a\\\,b`},
		{`Uxie, Mesprit, and Azelf Raid Hour`, `Uxie\, Mesprit\, and Azelf Raid Hour`},
	}
	for _, tc := range cases {
		if got := icsEscape(tc.in); got != tc.want {
			t.Errorf("icsEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// RFC 5545 caps a content line at 75 octets and continues with a leading space.
// Folding on a byte boundary would split a multibyte rune and corrupt the name
// of any event with an accent in it, which the feed has plenty of.
func TestWriteICSLineFolding(t *testing.T) {
	var b strings.Builder
	writeICSLine(&b, "SHORT:line")
	if got := b.String(); got != "SHORT:line\r\n" {
		t.Errorf("short line = %q, want %q", got, "SHORT:line\r\n")
	}

	b.Reset()
	long := "SUMMARY:" + strings.Repeat("a", 200)
	writeICSLine(&b, long)
	out := b.String()
	if !strings.HasSuffix(out, "\r\n") {
		t.Error("folded line does not end with CRLF")
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("folded line is %d octets, want at most 75: %q", len(line), line)
		}
	}
	// Unfolding is defined as removing CRLF plus the single following space.
	if unfolded := strings.ReplaceAll(out, "\r\n ", ""); strings.TrimSuffix(unfolded, "\r\n") != long {
		t.Error("unfolding the output did not reproduce the original line")
	}

	b.Reset()
	multibyte := "SUMMARY:" + strings.Repeat("Pokémon GO Fest ", 12)
	writeICSLine(&b, multibyte)
	out = b.String()
	if !strings.Contains(out, "é") {
		t.Error("multibyte rune was lost while folding")
	}
	if unfolded := strings.ReplaceAll(out, "\r\n ", ""); strings.TrimSuffix(unfolded, "\r\n") != multibyte {
		t.Error("unfolding a multibyte line did not reproduce the original")
	}
}

// A newline inside a feed value does not corrupt a content line, it creates one.
// UID and URL reach the output without icsEscape (URL is URI typed and must not
// be TEXT escaped), so before writeICSLine stripped controls, a crafted link or
// id wrote arbitrary properties into every subscriber's calendar, where they
// would survive any server side fix until each client next polled.
func TestICSInjectionThroughFeedValues(t *testing.T) {
	cases := []struct {
		name string
		ev   icsEvent
	}{
		{"newline in link", icsEvent{
			EventID: "ok-id",
			Name:    "Raid Hour",
			Start:   "2026-08-05T18:00:00.000",
			Link:    "https://leekduck.com/e/\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT1M\r\nDESCRIPTION:pwned\r\nEND:VALARM\r\nX-J:",
		}},
		{"newline in event id", icsEvent{
			EventID: "x\r\nATTENDEE;CN=attacker:mailto:a@evil.tld\r\nX-J:y",
			Name:    "Raid Hour",
			Start:   "2026-08-05T18:00:00.000",
		}},
		{"bare LF in name", icsEvent{
			EventID: "ok-id",
			Name:    "Raid\nHour",
			Start:   "2026-08-05T18:00:00.000",
		}},
	}
	for _, tc := range cases {
		var b strings.Builder
		if !writeVEVENT(&b, tc.ev, "20260806T000000Z") {
			t.Fatalf("%s: writeVEVENT refused a parseable event", tc.name)
		}
		out := b.String()
		// What matters is structure, not substrings. The payload text surviving
		// inside a single URL value is harmless; the attack is it becoming its own
		// content line. So unfold, split, and look at what each LINE starts with.
		unfolded := strings.ReplaceAll(out, "\r\n ", "")
		lines := strings.Split(strings.TrimSuffix(unfolded, "\r\n"), "\r\n")
		for _, line := range lines {
			for _, forbidden := range []string{"BEGIN:VALARM", "ACTION:", "ATTENDEE", "TRIGGER:", "X-J:"} {
				if strings.HasPrefix(line, forbidden) {
					t.Errorf("%s: injected content line %q:\n%s", tc.name, line, out)
				}
			}
		}
		if got, want := lines[0], "BEGIN:VEVENT"; got != want {
			t.Errorf("%s: first line = %q, want %q", tc.name, got, want)
		}
		if got, want := lines[len(lines)-1], "END:VEVENT"; got != want {
			t.Errorf("%s: last line = %q, want %q", tc.name, got, want)
		}
		var begins, ends int
		for _, line := range lines {
			if strings.HasPrefix(line, "BEGIN:") {
				begins++
			}
			if strings.HasPrefix(line, "END:") {
				ends++
			}
		}
		if begins != 1 || ends != 1 {
			t.Errorf("%s: %d BEGIN and %d END lines, want 1 each:\n%s", tc.name, begins, ends, out)
		}
	}
}

// Stripping control characters must not touch the characters that legitimately
// appear in a link. Commas and semicolons are meaningful inside a URI and are
// deliberately not TEXT escaped there.
func TestICSStripControlsLeavesRealURLsAlone(t *testing.T) {
	url := "https://leekduck.com/events/a,b;c/?x=1,2&y=a;b"
	if got := icsStripControls(url); got != url {
		t.Errorf("icsStripControls mangled a legitimate URL:\n got %q\nwant %q", got, url)
	}
	if got := icsStripControls("a\r\nb\x00c"); got != "abc" {
		t.Errorf("icsStripControls(%q) = %q, want %q", "a\\r\\nb\\x00c", got, "abc")
	}
}

// A floating start beside a UTC end leaves the duration undefined, so DTEND is
// dropped rather than emitted as an ambiguous pair.
func TestWriteVEVENTRefusesMixedDateTimeForms(t *testing.T) {
	var b strings.Builder
	writeVEVENT(&b, icsEvent{
		EventID: "mixed",
		Name:    "Odd one",
		Start:   "2026-08-05T18:00:00.000",  // floating
		End:     "2026-08-05T20:00:00.000Z", // absolute
	}, "20260806T000000Z")
	out := b.String()
	if !strings.Contains(out, "DTSTART:20260805T180000\r\n") {
		t.Errorf("floating DTSTART was not preserved:\n%s", out)
	}
	if strings.Contains(out, "DTEND:") {
		t.Errorf("a mismatched DTEND was emitted:\n%s", out)
	}

	// Matching forms still pair normally.
	b.Reset()
	writeVEVENT(&b, icsEvent{
		EventID: "matched", Name: "Normal",
		Start: "2026-08-05T18:00:00.000", End: "2026-08-05T20:00:00.000",
	}, "20260806T000000Z")
	if !strings.Contains(b.String(), "DTEND:20260805T200000\r\n") {
		t.Errorf("a matching floating pair lost its DTEND:\n%s", b.String())
	}
}

// The UID is what a subscribed calendar matches on. It deliberately stays on the
// old pogo.hails.live host: changing it would make every existing subscriber's
// calendar treat all events as new and recreate the lot.
func TestWriteVEVENTShape(t *testing.T) {
	var b strings.Builder
	ev := icsEvent{
		EventID:   "gbl-forever-forward_great-league_ultra-league",
		Name:      "Great League, Ultra League",
		EventType: "go-battle-league",
		Heading:   "GO Battle League",
		Link:      "https://leekduck.com/events/x/",
		Start:     "2026-08-04T20:00:00.000Z",
		End:       "2026-08-11T20:00:00.000Z",
	}
	if !writeVEVENT(&b, ev, "20260806T000000Z") {
		t.Fatal("writeVEVENT refused a well formed event")
	}
	out := b.String()
	for _, want := range []string{
		"BEGIN:VEVENT\r\n",
		"UID:gbl-forever-forward_great-league_ultra-league@pogo.hails.live\r\n",
		"DTSTART:20260804T200000Z\r\n",
		"DTEND:20260811T200000Z\r\n",
		"CATEGORIES:go-battle-league\r\n",
		"END:VEVENT\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("VEVENT is missing %q\ngot:\n%s", want, out)
		}
	}

	// An unparseable start means no VEVENT at all, rather than one that lands at
	// the zero time in every subscriber's calendar.
	b.Reset()
	ev.Start = "nonsense"
	if writeVEVENT(&b, ev, "20260806T000000Z") {
		t.Error("writeVEVENT wrote an event with an unparseable start")
	}
	if b.Len() != 0 {
		t.Errorf("writeVEVENT wrote %d bytes for a skipped event", b.Len())
	}
}
