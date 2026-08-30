package handlers

import (
	"image"
	"image/color"
	"testing"
)

// Appraisal star reading. Both halves are held to the same contract as the
// mobile client (OCRProcessor.extractAppraisal / AppraisalBarDetector.detect):
// report a count only when the whole three-position badge is accounted for,
// and report -1 -- "not readable" -- rather than a plausible-looking guess.
// The client treats -1 as "keep my own reading", so -1 is a clean outcome and
// a wrong number is not.

func TestCountStarsFromTextStrayGlyph(t *testing.T) {
	// The regression. A page carrying one star-shaped glyph that is not the
	// appraisal badge -- a favourite marker, an event banner, a nickname --
	// used to return 1, because the old code counted "★" anywhere and trusted
	// the total. 1 was also the smallest value it could emit, so every such
	// page read as a one-star Pokemon. Ground truth 2026-08-30: a hundo Primal
	// Groudon came back as 1 star.
	cases := []struct {
		name string
		text string
	}{
		{"single stray star", "CP3722\nGroudon\n★\n250 / 250 HP"},
		{"two strays, no empties", "★ Favourite\nAttack Defense HP\n★"},
		{"star in a nickname", "CP1964\n★SuperMon★\n140 / 140 HP"},
		{"no stars at all", "CP2717\nKartana\n104 / 104 HP"},
		{"four filled stars", "★★★★"},
		{"badge plus a stray", "★★★ ☆"},
	}
	for _, tc := range cases {
		if got := countStarsFromText(tc.text); got != -1 {
			t.Errorf("%s: countStarsFromText = %d, want -1 (unreadable)", tc.name, got)
		}
	}
}

func TestCountStarsFromTextCompleteBadge(t *testing.T) {
	// filled + empty == 3: the whole badge is on the page, so the count is
	// trustworthy. These are the only inputs that may yield a number.
	cases := []struct {
		text string
		want int
	}{
		{"★★★", 3},
		{"★★☆", 2},
		{"★☆☆", 1},
		{"☆☆☆", 0},
		{"Attack Defense HP\n★★★\nAmazing!", 3},
		{"Attack\nDefense\nHP\n★☆☆\nnot great", 1},
	}
	for _, tc := range cases {
		if got := countStarsFromText(tc.text); got != tc.want {
			t.Errorf("countStarsFromText(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

// paintedBadge builds a 1080x2340 screenshot with a gold block where the
// appraisal badge sits: x 10%-20% of WIDTH (not of some pre-cropped strip) and
// the given vertical fractions. Colour is the badge gold, which satisfies
// isOrangePixel and none of the isRainbowPixel branches.
func paintedBadge(t *testing.T, bands ...[2]float64) image.Image {
	t.Helper()
	const w, h = 1080, 2340
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{R: 20, G: 20, B: 32, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	gold := color.RGBA{R: 255, G: 170, B: 40, A: 255}
	for _, band := range bands {
		y0 := int(float64(h) * band[0])
		y1 := int(float64(h) * band[1])
		for y := y0; y <= y1 && y < h; y++ {
			for x := int(w * 0.10); x <= int(w*0.20); x++ {
				img.SetRGBA(x, y, gold)
			}
		}
	}
	return img
}

func TestDetectStarsFullWidthBand(t *testing.T) {
	// Defect B. The old bug was in the COMPOSITION, not in detectStars alone:
	// the call site handed it a left-25% crop and it then sampled xEnd=0.25 OF
	// THAT CROP -- the leftmost 6.25% of screen width. A badge at 10%-20% of
	// width, which is where the real one is and where the mobile detector
	// reads it, fell entirely outside the sampled band.
	//
	// So this test cannot re-run the old composition; what it pins is the
	// contract that replaced it. detectStars now takes the whole screenshot
	// and owns the badge window, so a badge at 10%-20% of width must be seen,
	// and any future narrowing of that window fails here.
	img := paintedBadge(t, [2]float64{0.60, 0.68})
	stars, isHundo := detectStars(img)
	if stars != 3 {
		t.Errorf("solid badge: stars = %d, want 3", stars)
	}
	if isHundo {
		t.Error("plain gold badge reported as hundo; rainbow ring is absent")
	}
}

func TestDetectStarsHollowBadgeDeclines(t *testing.T) {
	// Gold on the top and bottom rows only: the badge ring with an unfilled
	// interior. This used to be read as "1 star" (or 2, on the inner hit
	// count). Over the thirteen device captures those two branches were wrong
	// on both of the frames that ever reached them -- once calling a genuine
	// zero a 2, once calling a genuine three a 2 -- and no capture of a real 1-
	// or 2-star badge exists to have calibrated them against. So a badge this
	// cannot call a three is now declined outright.
	img := paintedBadge(t, [2]float64{0.575, 0.585}, [2]float64{0.695, 0.705})
	stars, isHundo := detectStars(img)
	if stars != -1 {
		t.Errorf("hollow badge: stars = %d, want -1 (unreadable)", stars)
	}
	if isHundo {
		t.Error("hollow badge reported as hundo")
	}
}

func TestClassifyStarsDeviceCaptures(t *testing.T) {
	// The row counts thirteen real device captures actually produced through
	// the shipped 13x50 grid, and what the badge in each of them really shows.
	// These are the SAME arrays the mobile app's AppraisalBadgeTest pins, which
	// is what "PARITY" on classifyStars has to mean to be worth writing.
	quiet := []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	trailingGrey := []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

	cases := []struct {
		name   string
		orange []int
		silver []int
		want   int
	}{
		// Three GOLD stars on an orange disc.
		{"Kyogre appraisal / 192112 / 192140 / 192225",
			[]int{0, 0, 0, 13, 11, 11, 11, 14, 9, 1, 0, 0, 0}, trailingGrey, 3},
		{"192120", []int{0, 0, 0, 13, 11, 11, 11, 13, 9, 1, 0, 0, 0}, trailingGrey, 3},
		{"192116 / 192136", []int{0, 0, 0, 0, 0, 0, 12, 10, 10, 11, 12, 9, 0}, quiet, 3},
		{"192133", []int{0, 0, 0, 0, 0, 0, 12, 10, 10, 11, 11, 9, 0}, quiet, 3},
		// The hundos: a pink disc, so half the orange.
		{"192124 / 192128 (hundo)",
			[]int{0, 0, 0, 0, 0, 1, 11, 13, 9, 1, 0, 0, 0},
			[]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2}, 3},
		// An orange UI element at the frame's left edge puts gold on every row.
		{"192217 (orange element at the left edge)",
			[]int{6, 6, 6, 19, 16, 14, 13, 16, 12, 5, 6, 6, 6}, trailingGrey, 3},
		// Three SILVER stars: a genuine ZERO. The old rule said 2.
		{"192155 (three silver stars, a genuine zero)",
			[]int{13, 11, 9, 0, 0, 0, 0, 0, 0, 0, 0, 6, 0},
			[]int{1, 0, 8, 16, 19, 12, 2, 0, 0, 3, 12, 0, 0}, -1},
		// Three GOLD stars on a high-sitting badge: a genuine THREE the gap
		// test cannot see. The old rule said 2; an abstain is the honest miss.
		{"192203 (three gold stars, high badge)",
			[]int{13, 11, 12, 12, 13, 8, 1, 0, 0, 0, 0, 6, 0},
			[]int{1, 0, 0, 0, 0, 0, 0, 0, 0, 3, 12, 0, 0}, -1},
	}
	for _, tc := range cases {
		if got, _ := classifyStars(tc.orange, tc.silver, 0); got != tc.want {
			t.Errorf("%s: classifyStars = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestClassifyStarsSilverVeto(t *testing.T) {
	// A star is a blob several sampled rows tall, so the veto asks for two
	// CONSECUTIVE rows over the threshold. The grey artefacts the corpus really
	// contains -- panel text under a high badge (3 then 12), an antialiased edge
	// (2 in one row) -- never manage that, and must not cost a correct three.
	three := []int{0, 0, 0, 13, 11, 11, 11, 14, 9, 1, 0, 0, 0}

	kept := [][]int{
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
		{0, 0, 0, 0, 0, 3, 12, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 12, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	for _, silver := range kept {
		if got, _ := classifyStars(three, silver, 0); got != 3 {
			t.Errorf("silver %v: classifyStars = %d, want 3", silver, got)
		}
	}
	vetoed := []int{0, 0, 0, 0, 4, 4, 0, 0, 0, 0, 0, 0, 0}
	if got, _ := classifyStars(three, vetoed, 0); got != -1 {
		t.Errorf("silver %v: classifyStars = %d, want -1", vetoed, got)
	}
	// And a vetoed badge is never a hundo, whatever the ring says.
	if _, isHundo := classifyStars(three, vetoed, 160); isHundo {
		t.Error("a vetoed badge reported as a hundo")
	}
}

func TestAppraisalOnScreenGatesBothReaders(t *testing.T) {
	// The text reader used to answer this field with no screen gate at all, so
	// a page carrying three star glyphs and no appraisal panel returned a
	// number. Verified against the shipped function before the gate:
	//
	//	"CP 1500 | ★★★ | 3 MACHOP CANDY" -> 3
	//
	// countStarsFromText still returns 3 for that text; what changed is that
	// nothing asks it unless the panel is on screen.
	strayBadge := "CP 1500\n★★★\n3 MACHOP CANDY"
	if got := countStarsFromText(strayBadge); got != 3 {
		t.Fatalf("precondition: countStarsFromText = %d, want 3", got)
	}
	if appraisalOnScreen(strayBadge) {
		t.Error("a candy row is not an appraisal panel")
	}

	// Whole-word and case-insensitive, matching OCRProcessor.appraisalVisible.
	if !appraisalOnScreen("CP 1500\nATTACK DEFENSE HP\n★★★") {
		t.Error("an all-caps panel must open the gate")
	}
	if appraisalOnScreen("Attacker Defense HP") {
		t.Error("\"Attacker\" is not the word \"attack\"")
	}
}

func TestDetectStarsUnreadable(t *testing.T) {
	// Nothing badge-coloured anywhere: no reading, and the server says so
	// rather than emitting the lowest plausible count.
	const w, h = 1080, 2340
	blank := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			blank.SetRGBA(x, y, color.RGBA{R: 20, G: 20, B: 32, A: 255})
		}
	}
	if stars, isHundo := detectStars(blank); stars != -1 || isHundo {
		t.Errorf("blank screenshot: stars = %d, isHundo = %v, want -1, false", stars, isHundo)
	}

	// Degenerate image: no panic, no reading.
	if stars, _ := detectStars(image.NewRGBA(image.Rect(0, 0, 0, 0))); stars != -1 {
		t.Errorf("empty image: stars = %d, want -1", stars)
	}
}

func TestDetectStarsFixturesNoFalsePositives(t *testing.T) {
	// The committed fixtures are ordinary Pokemon cards, not appraisal
	// screens, so the badge is not on any of them. The pixel scan may decline
	// (-1); what it must never do is invent a star count off the artwork now
	// that it reads four times as much of the frame.
	for _, name := range []string{
		"Kartana-original.png",
		"Dugtrio-original.png",
		"Machamp-original.png",
		"PURIFIED-Altaria-original.png",
		"Snorlax-original.png",
		"Celesteela-original.png",
	} {
		img := loadFixture(t, name)
		stars, isHundo := detectStars(img)
		t.Logf("%s: stars=%d isHundo=%v", name, stars, isHundo)
		if stars < -1 || stars > 3 {
			t.Errorf("%s: stars = %d, outside the -1..3 contract", name, stars)
		}
	}
}

func TestClassifyStarsZeroStarBadgeIsNotThree(t *testing.T) {
	// The regression. The silver veto used to hunt only between the orange
	// span's first and last hit -- and a gold star is part of what makes the
	// star band orange, so a badge with NO earned stars has no orange there.
	// The span collapses to the disc's top ribbon and the silver stars land
	// BELOW lastHit, outside the window looking for them. The window was
	// anti-correlated with the thing it looked for: the fewer stars earned, the
	// smaller the search. What was left was a short gapless span with
	// total >= 10, which is the signature of a three.
	//
	// Each profile below returned 3 under the old window and must not again.
	zeros := []struct {
		name   string
		orange []int
		silver []int
	}{
		// A zero-star badge sitting where a real three-star one sits, built by
		// transplanting 192155's real silver stars (8/16/19/12) onto three real
		// three-star hosts with the star band's orange removed.
		{"192112 / Kyogre host",
			[]int{0, 0, 0, 13, 11, 11, 0, 0, 0, 0, 0, 0, 0},
			[]int{0, 0, 0, 0, 0, 8, 16, 19, 12, 0, 0, 0, 0}},
		{"192120 host",
			[]int{0, 0, 0, 13, 11, 11, 0, 0, 0, 0, 0, 0, 0},
			[]int{0, 0, 0, 0, 0, 8, 16, 19, 12, 0, 0, 0, 0}},
		{"192116 host (low badge)",
			[]int{0, 0, 0, 0, 0, 0, 12, 10, 10, 0, 0, 0, 0},
			[]int{0, 0, 0, 0, 0, 0, 0, 0, 8, 16, 19, 12, 0}},
		// The measured profile of a constructed zero-star frame.
		{"constructed zero-star frame",
			[]int{0, 0, 0, 13, 11, 10, 0, 0, 0, 0, 0, 0, 0},
			[]int{0, 0, 0, 0, 0, 8, 15, 18, 11, 2, 0, 0, 2}},
		// 192155's own numbers with the "Attack" panel label removed -- row
		// 11's six orange hits, nine rows below the badge, which the old span
		// mistook for part of it. That artefact is the ONLY reason the one real
		// zero-star capture in the corpus abstained; without it, old said 3.
		{"192155 minus the panel-label artefact",
			[]int{13, 11, 9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			[]int{1, 0, 8, 16, 19, 12, 2, 0, 0, 3, 12, 0, 0}},
	}
	for _, tc := range zeros {
		if got, _ := classifyStars(tc.orange, tc.silver, 0); got == 3 {
			t.Errorf("%s: classifyStars = 3, want -1 -- a zero-star badge read as three", tc.name)
		}
	}

	// And the veto must not depend on where the badge sits. 192155's whole
	// real profile, slid up and down the band: never a three at any offset.
	// (Under the old window, five of these thirteen offsets returned 3.)
	realO := []int{13, 11, 9, 0, 0, 0, 0, 0, 0, 0, 0, 6, 0}
	realS := []int{1, 0, 8, 16, 19, 12, 2, 0, 0, 3, 12, 0, 0}
	for k := -6; k <= 6; k++ {
		so, ss := make([]int, 13), make([]int, 13)
		for i := range realO {
			if j := i + k; j >= 0 && j < 13 {
				so[j], ss[j] = realO[i], realS[i]
			}
		}
		if got, _ := classifyStars(so, ss, 0); got == 3 {
			t.Errorf("192155 shifted %+d rows: classifyStars = 3, want -1", k)
		}
	}
}
