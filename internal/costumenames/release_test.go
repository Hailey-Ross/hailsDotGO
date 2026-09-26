package costumenames

import "testing"

// A cut-down copy of what a real page carries, escaped the way it arrives inside the script
// payload, and with the trap that matters: the same costume name appears several times, and the
// releaseDate physically NEAREST most of those occurrences belongs to a DIFFERENT costume.
//
// Taken from charmander-goggles-2026, where the correct answer is 2026-09-16 and the two wrong
// answers sitting closer in the document are 2019-10-17 and 2020-02-25.
const gogglePage = `<meta property="og:title" content="Friede&#x27;s Goggles Charmander (Pok\u00e9mon GO)"/>` +
	`<script>self.__next_f.push([1,"{\"breadcrumb\":[{\"name\":\"Friede's Goggles Charmander\"}],` +
	// The decoy that broke the first attempt: the slug's FIRST occurrence is UI state with no
	// date at all, so "first match wins" silently found nothing on every real page.
	`\"ui\":{\"slug\":\"charmander-goggles-2026\",\"active\":\"overview\",\"h\":\"44px\"},` +
	`\"forms\":[{\"slug\":\"charmander-fall-2019\",\"name\":\"Cubone Charmander\",\"isCostume\":true,\"releaseDate\":\"2019-10-17\"},` +
	`{\"slug\":\"charmander-goggles-2026\",\"name\":\"Friede's Goggles Charmander\",\"isCostume\":true,\"releaseDate\":\"2026-09-16\"},` +
	`{\"slug\":\"charmander-jan-2020\",\"name\":\"Party Hat Charmander\",\"isCostume\":true,\"releaseDate\":\"2020-02-25\"}],` +
	`\"GoPokemonPowerUpCost\":[],\"name\":\"Friede's Goggles Charmander\",\"isReleased\":true,\"isShinyReleased\":true}"])</script>`

func TestParseReleaseIsAnchoredNotNearest(t *testing.T) {
	var p Page
	parseRelease(&p, gogglePage, "charmander-goggles-2026", "Friede's Goggles Charmander")

	if p.ReleaseDate != "2026-09-16" {
		t.Errorf("releaseDate = %q, want 2026-09-16 (a nearest-match parse picks a different costume's date)", p.ReleaseDate)
	}
	if !p.HasReleased || !p.Released {
		t.Errorf("isReleased = %v (present %v), want true", p.Released, p.HasReleased)
	}
}

// A slug the page does not carry must yield nothing rather than a neighbour's date. Silence is
// the only safe answer: a wrong date releases a costume on the wrong day, by itself.
func TestParseReleaseIgnoresAForeignSlug(t *testing.T) {
	var p Page
	parseRelease(&p, gogglePage, "charmander-not-here-2027", "Nobody's Hat Charmander")

	if p.ReleaseDate != "" {
		t.Errorf("releaseDate = %q, want empty for a slug this page does not carry", p.ReleaseDate)
	}
	if p.HasReleased {
		t.Error("isReleased should not be claimed for a costume this page is not about")
	}
}

// Upstream simply not saying is a THIRD state, distinct from saying "not released". The caller
// has to tell them apart: absent means keep waiting, false means upstream actively says no.
func TestMissingReleaseFieldsAreNotFalse(t *testing.T) {
	var p Page
	parseRelease(&p, `{"slug":"x-costume","isCostume":true,"releaseDate":null}`, "x-costume", "Some Hat")

	if p.ReleaseDate != "" {
		t.Errorf("a null releaseDate should read as unknown, got %q", p.ReleaseDate)
	}
	if p.HasReleased {
		t.Error("an absent isReleased must not be reported as a present false")
	}
}
