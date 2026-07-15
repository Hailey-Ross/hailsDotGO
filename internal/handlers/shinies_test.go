package handlers

import (
	"testing"
	"time"
)

// The 28 Unown letters are the only variant family whose sprite is not a numeric id, and the
// only one big enough to bump into the column width. Both facts are easy to break silently:
// an over-long tag would be TRUNCATED by MySQL rather than rejected, quietly turning one
// letter into another, and a bad slug would render a broken image on every card.
func TestUnownLetterRegions(t *testing.T) {
	// The full alphabet plus the two punctuation forms, and nothing else.
	if len(unownSpriteSlug) != 28 {
		t.Fatalf("got %d Unown letters, want 28", len(unownSpriteSlug))
	}

	for _, tc := range []struct{ region, slug string }{
		// Letter A is the DEFAULT form, so its art is the plain 201, not 201-a (which 404s).
		{"unown_a", "201"},
		{"unown_b", "201-b"},
		{"unown_z", "201-z"},
		// Spelled out upstream, but abbreviated in the tag to fit VARCHAR(16).
		{"unown_excl", "201-exclamation"},
		{"unown_qmark", "201-question"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			if got := regionalSpriteSlug("Unown", tc.region); got != tc.slug {
				t.Errorf("sprite slug = %q, want %q", got, tc.slug)
			}
		})
	}

	for region := range unownSpriteSlug {
		// user_shinies.region is VARCHAR(16). MySQL truncates rather than errors, so an
		// over-long tag would silently record the wrong letter.
		if len(region) > 16 {
			t.Errorf("region %q is %d chars; the column holds 16", region, len(region))
		}
		// Every tag has to be accepted by the add, update and evolve handlers, all of which
		// gate on this map. Miss one and that letter is unrecordable.
		if !validRegions[region] {
			t.Errorf("region %q is not in validRegions, so it cannot be saved", region)
		}
	}

	// The letters belong to Unown alone: they must not leak onto another species' sprite.
	if got := regionalSpriteSlug("Pikachu", "unown_b"); got != "" {
		t.Errorf("Pikachu in region unown_b resolved to %q, want no sprite", got)
	}
	// And the ordinary numeric variants must still resolve through the same function.
	if got := regionalSpriteSlug("Rattata", "alolan"); got != "10091" {
		t.Errorf("Alolan Rattata = %q, want 10091", got)
	}
	if got := regionalSpriteSlug("Rattata", ""); got != "" {
		t.Errorf("a regionless Rattata resolved to %q, want no sprite", got)
	}
}

// Vivillon's 20 wing patterns ride the same string-slug sprite path as the Unown letters, so the
// same two silent failures apply: an over-long tag would be TRUNCATED by MySQL into a different
// pattern, and a bad slug would render a broken image. The trickiest slugs are hyphenated
// (666-icy-snow, 666-high-plains, 666-poke-ball) and Meadow, which is the default form and so is
// the plain 666, not 666-meadow.
func TestVivillonPatternRegions(t *testing.T) {
	// The full pattern set, and nothing else.
	if len(vivillonSpriteSlug) != 20 {
		t.Fatalf("got %d Vivillon patterns, want 20", len(vivillonSpriteSlug))
	}

	for _, tc := range []struct{ region, slug string }{
		// Meadow is the DEFAULT form, so its art is the plain 666, not 666-meadow (which 404s).
		{"viv_meadow", "666"},
		{"viv_polar", "666-polar"},
		{"viv_elegant", "666-elegant"},
		// The hyphenated slugs: the tag abbreviates nothing, but the slug keeps the hyphens.
		{"viv_icy_snow", "666-icy-snow"},
		{"viv_high_plains", "666-high-plains"},
		{"viv_poke_ball", "666-poke-ball"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			if got := regionalSpriteSlug("Vivillon", tc.region); got != tc.slug {
				t.Errorf("sprite slug = %q, want %q", got, tc.slug)
			}
		})
	}

	for region := range vivillonSpriteSlug {
		// user_shinies.region is VARCHAR(16). MySQL truncates rather than errors, so an
		// over-long tag would silently record the wrong pattern.
		if len(region) > 16 {
			t.Errorf("region %q is %d chars; the column holds 16", region, len(region))
		}
		// Every tag has to be accepted by the add, update and evolve handlers, all of which
		// gate on this map. Miss one and that pattern is unrecordable.
		if !validRegions[region] {
			t.Errorf("region %q is not in validRegions, so it cannot be saved", region)
		}
	}

	// The patterns belong to Vivillon alone: Scatterbug and Spewpa look identical across every
	// region, so a pattern tag must not leak a sprite onto them or anything else.
	if got := regionalSpriteSlug("Scatterbug", "viv_elegant"); got != "" {
		t.Errorf("Scatterbug in region viv_elegant resolved to %q, want no sprite", got)
	}
}

// humanizeRegion feeds the mobile Add form's region picker. Its labels are best-effort (the
// authoritative ones live client-side), but the base region and the common shapes have to read
// sensibly, and the empty tag must never render as a blank option.
func TestHumanizeRegion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "Base"},
		{"alolan", "Alolan"},
		{"dusk_mane", "Dusk Mane"},
		{"unown_a", "Unown A"},
		{"viv_icy_snow", "Viv Icy Snow"},
	} {
		if got := humanizeRegion(tc.in); got != tc.want {
			t.Errorf("humanizeRegion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// shinyMethods mirrors the METHODS array in ts/shinies.ts, and the mobile Add form trusts its
// order and values. A drift here silently desyncs the app's method picker from the site's.
func TestShinyMethodsMirrorClient(t *testing.T) {
	want := []string{"", "wild", "egg", "raid", "research", "evolution", "photobomb", "trade", "go_pass", "go_tour"}
	if len(shinyMethods) != len(want) {
		t.Fatalf("got %d methods, want %d", len(shinyMethods), len(want))
	}
	for i, v := range want {
		if shinyMethods[i].value != v {
			t.Errorf("method %d = %q, want %q", i, shinyMethods[i].value, v)
		}
		if shinyMethods[i].i18n == "" {
			t.Errorf("method %q has no i18n key", v)
		}
	}
}

// The caught date is the one thing on a shiny entry that the trainer alone knows, and until now
// nothing ever wrote the column: it recorded when the ROW was added, not when the Pokemon was
// caught. The trainer page now shows it, so it has to be theirs and it has to be sane.
func TestParseCaughtAt(t *testing.T) {
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	nextWeek := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")

	for _, tc := range []struct {
		name  string
		in    string
		set   bool // did it yield a date to store?
		fails bool
	}{
		{"empty leaves the stored date alone", "", false, false},
		{"whitespace is empty", "   ", false, false},
		{"a normal date", "2024-02-29", true, false},
		{"the day Pokemon GO launched", "2016-07-06", true, false},

		{"the day before Pokemon GO existed", "2016-07-05", false, true},
		{"long before Pokemon GO existed", "1999-01-01", false, true},
		{"a week from now", nextWeek, false, true},
		{"not a date at all", "yesterday", false, true},
		{"a timestamp, not a date", "2024-02-29T12:00:00Z", false, true},
		{"nonsense", "2024-13-45", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, set, err := parseCaughtAt(tc.in)
			if tc.fails {
				if err == nil {
					t.Fatalf("%q was accepted, and it should not be", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q was rejected: %v", tc.in, err)
			}
			if set != tc.set {
				t.Fatalf("%q: set=%v, want %v", tc.in, set, tc.set)
			}
			if set && got.Format("2006-01-02") != tc.in {
				t.Errorf("%q round-tripped as %s", tc.in, got.Format("2006-01-02"))
			}
		})
	}

	// Today must always be allowed: most people log a catch the day they make it.
	if _, set, err := parseCaughtAt(time.Now().UTC().Format("2006-01-02")); err != nil || !set {
		t.Errorf("today was rejected: %v", err)
	}
	// Tomorrow is tolerated on purpose: a trainer's clock can legitimately be hours ahead of the
	// server's, and refusing them a date they are currently living in would be nonsense.
	if _, _, err := parseCaughtAt(tomorrow); err != nil {
		t.Errorf("tomorrow was rejected, but timezones make it reachable: %v", err)
	}
}
