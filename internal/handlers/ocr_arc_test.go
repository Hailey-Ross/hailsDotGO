package handlers

import (
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/draw"
)

func loadFixture(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "ocr", name))
	if err != nil {
		t.Skipf("fixture %s not available: %v", name, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return img
}

// Reference scans from a level-46 trainer's 1080x2340 device. True levels
// verified three ways: power-up dust bracket, known-hundo CP/HP
// back-calculation, and manual dot measurement (see
// .claude/OCR-Pokegenie-Parity-Attempt.md). The arc contract is +-0.5 level:
// above level ~30 a half-level step is only a few pixels, so the search
// intersects the arc reading with dust brackets at that tolerance.
var arcFixtures = []struct {
	file           string
	trueLvl        float64
	minLvl, maxLvl float64 // trueLvl +-0.5, tightened where the dust bracket bounds it
	note           string
}{
	{"Kartana-original.png", 26, 25.5, 26.5, "clean arc"},
	{"Dugtrio-original.png", 22, 21.5, 22.5, "hundo, exact level 22.0"},
	{"Machamp-original.png", 22.5, 22, 23, "hundo, exact level 22.5"},
	{"PURIFIED-Altaria-original.png", 32.5, 32, 33, "hundo purified; white cloud wings near arc"},
	{"Snorlax-original.png", 38, 37, 38.5, "white buddy speech bubble on arc path"},
	{"Celesteela-original.png", 25, 24.5, 26.5, "arc partially obscured by the Pokemon"},
}

func TestDetectArcLevelFixtures(t *testing.T) {
	cpms := loadCPMs(t)
	for _, fx := range arcFixtures {
		img := loadFixture(t, fx.file)
		arc := detectArcLevel(img, 46, cpms)
		if !arc.OK {
			t.Errorf("%s: arc not detected (%s)", fx.file, fx.note)
			continue
		}
		if arc.Level < fx.minLvl || arc.Level > fx.maxLvl {
			t.Errorf("%s: arc level %.1f outside [%.1f, %.1f] (%s, calibrated=%v run=%d)",
				fx.file, arc.Level, fx.minLvl, fx.maxLvl, fx.note, arc.Calibrated, arc.RunPx)
		}
	}
}

// The user-visible guarantee: arc level (+-0.5) intersected with the fuzzy
// dust brackets plus HP must produce the true IV set even when CP is
// unreadable. Altaria is the hardest case: nicknamed (no species text via
// name), purified 5400 dust (three interpretations), CP hard to read, clouds
// near the arc.
func TestArcOnlySearchAltaria(t *testing.T) {
	cpms := loadCPMs(t)
	img := loadFixture(t, "PURIFIED-Altaria-original.png")
	arc := detectArcLevel(img, 46, cpms)
	if !arc.OK {
		t.Fatal("arc not detected")
	}
	altaria := pokemonStatEntry{
		BaseAttack: 141, BaseDefense: 201, BaseStamina: 181,
		Form: "Normal", PokemonName: "Altaria", PokemonID: 334,
	}
	req := ivRequest{
		PokemonName: "Altaria", CP: 0, HP: 146, TrainerLevel: 46,
	}
	ranges := intersectRangesWithLevel(dustCandidates(5400, nil, nil, nil), arc.Level, 0.5)
	candidates, buddy := enumerateWithBuddyRetry(req, ranges, altaria, cpms)
	if buddy {
		t.Fatal("buddy retry should not trigger")
	}
	found := false
	for _, c := range candidates {
		if c.AtkIV == 15 && c.DefIV == 15 && c.StaIV == 15 && c.Level == 32.5 {
			if c.CP != 1790 {
				t.Errorf("hundo candidate CP = %d, want 1790", c.CP)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("hundo @32.5 not in arc-only candidates (arc=%.1f, %d candidates)", arc.Level, len(candidates))
	}
}

// Arc detection must survive the live upstream's truncated CPM table
// (levels stop at 45.0; cpmLookup synthesizes the rest).
func TestDetectArcLevelTruncatedCPMs(t *testing.T) {
	var truncated []cpmEntry
	for _, e := range loadCPMs(t) {
		if e.Level <= 45.0 {
			truncated = append(truncated, e)
		}
	}
	img := loadFixture(t, "Machamp-original.png")
	arc := detectArcLevel(img, 48, truncated)
	if !arc.OK || arc.Level < 22 || arc.Level > 23 {
		t.Errorf("truncated CPMs: got ok=%v level=%.1f, want 22.5 +-0.5", arc.OK, arc.Level)
	}
}

// The pixel constants must scale with image width: a downscaled screenshot
// (older phone, messenger compression) has a proportionally smaller dot.
func TestDetectArcLevelScaled(t *testing.T) {
	cpms := loadCPMs(t)
	img := loadFixture(t, "Machamp-original.png")
	b := img.Bounds()
	for _, w := range []int{720, 1440} {
		h := b.Dy() * w / b.Dx()
		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, b, draw.Over, nil)
		arc := detectArcLevel(scaled, 46, cpms)
		if !arc.OK || arc.Level < 22 || arc.Level > 23 {
			t.Errorf("width %d: got ok=%v level=%.1f, want 22.5 +-0.5", w, arc.OK, arc.Level)
		}
	}
}
