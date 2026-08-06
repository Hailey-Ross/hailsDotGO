package handlers

import (
	"testing"
	"time"
)

// A release date drives an automatic flip, so a typo does not just look wrong: it either unlocks a
// shiny that does not exist or parks a card that never unlocks. This is the only gate on it.
func TestParseShinyReleaseDate(t *testing.T) {
	nextMonth := time.Now().UTC().AddDate(0, 1, 0).Format("2006-01-02")
	lastYear := time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02")
	tooFar := time.Now().UTC().AddDate(shinyDateHorizon, 0, 1).Format("2006-01-02")

	for _, tc := range []struct {
		name  string
		in    string
		want  string
		fails bool
	}{
		{"empty means no date", "", "", false},
		{"whitespace is empty", "   ", "", false},
		{"an announced date", nextMonth, nextMonth, false},
		// The past is allowed: it is how "this shipped on the 12th" gets recorded after the fact,
		// and it is the same write that turns the shiny on.
		{"a date that has already passed", lastYear, lastYear, false},
		{"the day Pokemon GO launched", "2016-07-06", "2016-07-06", false},
		{"surrounding whitespace is trimmed", " 2026-08-12 ", "2026-08-12", false},

		{"the day before Pokemon GO existed", "2016-07-05", "", true},
		{"just past the horizon", tooFar, "", true},
		{"a mistyped year", "2062-08-12", "", true},
		{"a timestamp, not a date", "2026-08-12T12:00:00Z", "", true},
		{"not a date at all", "next Tuesday", "", true},
		{"nonsense", "2026-13-45", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShinyReleaseDate(tc.in)
			if tc.fails {
				if err == nil {
					t.Fatalf("%q was accepted, and it should not be", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q was rejected: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("%q parsed to %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The horizon exists to catch a mistyped year, so it has to be short enough to actually catch one
// and long enough not to reject a real announcement. Anything beyond a few years is the former.
func TestShinyDateHorizonIsShort(t *testing.T) {
	if shinyDateHorizon < 1 || shinyDateHorizon > 5 {
		t.Errorf("shinyDateHorizon is %d years; it is meant to catch a mistyped year, not permit one", shinyDateHorizon)
	}
}
