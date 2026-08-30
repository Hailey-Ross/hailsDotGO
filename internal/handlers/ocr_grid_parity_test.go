package handlers

import "testing"

// TestDetectStarsGridParity pins the sampling GRID, separately from the
// decision made about it.
//
// "PARITY: same 13x50 grid" was a comment and not a fact. Kotlin truncated a
// float32 fraction where this rounded a float64 one, and over these six
// fixtures FIVE of six landed on different pixels and produced different
// rainbow counts (Celesteela 7 vs 9, Dugtrio 7 vs 8, Kartana 7 vs 9, Machamp 10
// vs 11, Altaria 8 vs 7). One pixel per axis, but a 20% phase shift on a 5.4px
// stride.
//
// The numbers below came from running the MOBILE detector's arithmetic
// (AppraisalBarDetector.detect: float64 throughout, xEnd truncated to a pixel
// first, both axes truncated and never rounded) over these same six committed
// fixtures on 2026-08-30. They match here exactly. If this test fails, one of
// the two grids has moved and the PARITY comment on detectStars is a lie again.
//
// These are ordinary Pokemon cards, not appraisal screens, so no badge is on
// any of them: every orange row is legitimately zero and every reading is -1.
// The silver and rainbow counts are what actually discriminate the grid, and
// they are sensitive to a single pixel of drift.
func TestDetectStarsGridParity(t *testing.T) {
	cases := []struct {
		name    string
		silver  [starGridRows]int
		rainbow int
	}{
		{"Kartana-original.png", [starGridRows]int{0, 0, 0, 0, 0, 0, 0, 5, 1, 0, 11, 0, 0}, 7},
		{"Dugtrio-original.png", [starGridRows]int{0, 0, 0, 0, 0, 0, 0, 5, 1, 0, 11, 0, 0}, 7},
		{"Machamp-original.png", [starGridRows]int{2, 1, 0, 8, 0, 0, 0, 0, 0, 0, 0, 5, 9}, 10},
		{"PURIFIED-Altaria-original.png", [starGridRows]int{0, 0, 0, 0, 0, 0, 2, 1, 0, 3, 0, 0, 0}, 9},
		{"Snorlax-original.png", [starGridRows]int{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 11, 0, 0}, 10},
		{"Celesteela-original.png", [starGridRows]int{0, 0, 0, 0, 0, 0, 0, 5, 1, 0, 11, 0, 0}, 7},
	}
	var noBadge [starGridRows]int
	for _, tc := range cases {
		img := loadFixture(t, tc.name)
		orange, silver, rainbow := sampleStarGrid(img)
		if orange != noBadge {
			t.Errorf("%s: orange = %v, want all zero (no badge on this card)", tc.name, orange)
		}
		if silver != tc.silver {
			t.Errorf("%s: silver = %v, want %v (the grid has moved)", tc.name, silver, tc.silver)
		}
		if rainbow != tc.rainbow {
			t.Errorf("%s: rainbow = %d, want %d (the grid has moved)", tc.name, rainbow, tc.rainbow)
		}
		if stars, isHundo := detectStars(img); stars != -1 || isHundo {
			t.Errorf("%s: stars = %d, isHundo = %v, want -1, false", tc.name, stars, isHundo)
		}
	}
}
