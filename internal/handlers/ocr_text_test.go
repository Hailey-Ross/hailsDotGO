package handlers

import (
	"strings"
	"testing"
)

// Synthetic OCR lines shaped like real RapidOCR output from the reference
// screenshots (1080x2340). Line text and vertical positions matter; boxes are
// approximate.
func kartanaLines() ([]ocrLine, string) {
	lines := []ocrLine{
		{Text: "10:27", X1: 30, Y1: 30, X2: 160, Y2: 80, Score: 0.99},
		{Text: "61", X1: 990, Y1: 30, X2: 1040, Y2: 75, Score: 0.9},
		{Text: "CP2717", X1: 380, Y1: 120, X2: 640, Y2: 200, Score: 0.98},
		{Text: "Kartana", X1: 400, Y1: 950, X2: 680, Y2: 1030, Score: 0.99},
		{Text: "104 / 104 HP", X1: 430, Y1: 1090, X2: 650, Y2: 1130, Score: 0.98},
		{Text: "185,852", X1: 150, Y1: 1500, X2: 320, Y2: 1550, Score: 0.97},
		{Text: "STARDUST", X1: 150, Y1: 1560, X2: 320, Y2: 1600, Score: 0.97},
		{Text: "KARTANA CANDY", X1: 420, Y1: 1560, X2: 700, Y2: 1600, Score: 0.96},
		{Text: "13", X1: 880, Y1: 1500, X2: 920, Y2: 1550, Score: 0.9},
		{Text: "KARTANA CANDY XL", X1: 760, Y1: 1560, X2: 1030, Y2: 1620, Score: 0.96},
		{Text: "POWER UP", X1: 170, Y1: 1740, X2: 420, Y2: 1800, Score: 0.99},
		{Text: "4,000", X1: 620, Y1: 1740, X2: 740, Y2: 1800, Score: 0.98},
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Text)
		sb.WriteString("\n")
	}
	return lines, sb.String()
}

func TestExtractCPKartana(t *testing.T) {
	lines, fullText := kartanaLines()
	if got := extractCP(lines, fullText, 2340); got != 2717 {
		t.Errorf("extractCP = %d, want 2717", got)
	}
}

func TestExtractCPClockGuard(t *testing.T) {
	// No CP-labeled line: strategy 2 takes the max top-zone number, and the
	// clock line must stay excluded via the ':' guard.
	lines := []ocrLine{
		{Text: "10:27", X1: 30, Y1: 30, X2: 160, Y2: 80},
		{Text: "2717", X1: 420, Y1: 130, X2: 640, Y2: 200},
	}
	if got := extractCP(lines, "10:27\n2717", 2340); got != 2717 {
		t.Errorf("extractCP = %d, want 2717", got)
	}
}

func TestExtractHP(t *testing.T) {
	if got := extractHP("104 / 104 HP"); got != 104 {
		t.Errorf("hp = %d, want 104", got)
	}
	// OCR letter-O misread inside the fraction.
	if got := extractHP("1O4 / 104 HP"); got != 104 {
		t.Errorf("hp with O misread = %d, want 104", got)
	}
	if got := extractHP("no fraction here"); got != 0 {
		t.Errorf("hp = %d, want 0", got)
	}
}

func TestDetectRawDustRealText(t *testing.T) {
	// Altaria: total stardust 185,852 must not match; mega-evolve cost 300 is
	// a REAL displayable value (lucky half of the 600 tier) but the true
	// power-up cost 5,400 is larger and wins via descending preference.
	altaria := "Queen Hundo\n146 / 146 HP\n185,852\nSTARDUST\n14\nSWABLU CANDY\n3,700\nALTARIA MEGA ENERGY\nPOWER UP\n5,400\nMEGA EVOLVE\n300"
	if got := detectRawDust(altaria); got != 5400 {
		t.Errorf("altaria dust = %d, want 5400", got)
	}
	// Spaced thousands quirk.
	if got := detectRawDust("POWER UP 4 000"); got != 4000 {
		t.Errorf("spaced dust = %d, want 4000", got)
	}
	// No displayable value present.
	if got := detectRawDust("185,852 STARDUST"); got != 0 {
		t.Errorf("dust = %d, want 0", got)
	}
}

func TestUnanimousCP(t *testing.T) {
	if got := unanimousCP(nil); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
	same := []IVCandidate{{CP: 1790}, {CP: 1790}}
	if got := unanimousCP(same); got != 1790 {
		t.Errorf("unanimous = %d, want 1790", got)
	}
	mixed := []IVCandidate{{CP: 1790}, {CP: 1727}}
	if got := unanimousCP(mixed); got != 0 {
		t.Errorf("mixed = %d, want 0", got)
	}
}

func TestDetectNameNickname(t *testing.T) {
	// A nicknamed card yields the nickname (species resolution then falls
	// through to the candy-family path).
	lines := []ocrLine{
		{Text: "CP1964", X1: 380, Y1: 120, X2: 640, Y2: 200},
		{Text: "John Cena", X1: 360, Y1: 950, X2: 720, Y2: 1030},
		{Text: "140 / 140 HP", X1: 430, Y1: 1090, X2: 650, Y2: 1130},
	}
	name, source := detectName(lines, "CP1964\nJohn Cena\n140 / 140 HP", 2340)
	if name != "John Cena" || source != "card" {
		t.Errorf("got name=%q source=%q, want John Cena/card", name, source)
	}
}
