package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func boolPtr(v bool) *bool { return &v }

// ocrRegion is a percentage-based crop of a full screenshot (fractions 0-1).
type ocrRegion struct{ x1, y1, x2, y2 float64 }

var (
	// Only pixel-scan crops remain; text now comes from the neural OCR service.
	ocrRegionAppraisalScreen = ocrRegion{0.00, 0.00, 0.25, 1.00} // appraisal-results screen bars (mobile app coordinates)
)

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

func cropPct(img image.Image, reg ocrRegion) image.Image {
	b := img.Bounds()
	w := b.Max.X - b.Min.X
	h := b.Max.Y - b.Min.Y
	rect := image.Rectangle{
		Min: image.Point{
			X: b.Min.X + int(math.Round(float64(w)*reg.x1)),
			Y: b.Min.Y + int(math.Round(float64(h)*reg.y1)),
		},
		Max: image.Point{
			X: b.Min.X + int(math.Round(float64(w)*reg.x2)),
			Y: b.Min.Y + int(math.Round(float64(h)*reg.y2)),
		},
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(rect)
	}
	out := image.NewRGBA(image.Rectangle{Max: image.Point{
		X: rect.Max.X - rect.Min.X,
		Y: rect.Max.Y - rect.Min.Y,
	}})
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			out.Set(x-rect.Min.X, y-rect.Min.Y, img.At(x, y))
		}
	}
	return out
}

// contrastEnhance converts an image to grayscale with contrast x1.5 (matches the
// mobile app's grayscaleContrast used on the CP retry pass).
func contrastEnhance(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewGray(image.Rectangle{Max: image.Point{X: b.Max.X - b.Min.X, Y: b.Max.Y - b.Min.Y}})
	const contrast = 1.5
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			rr, gg, bb, _ := img.At(x, y).RGBA()
			gray := (19595*rr + 38470*gg + 7471*bb) >> 24
			enhanced := int(float64(gray)*contrast) - int(128*(contrast-1))
			if enhanced < 0 {
				enhanced = 0
			} else if enhanced > 255 {
				enhanced = 255
			}
			out.SetGray(x-b.Min.X, y-b.Min.Y, color.Gray{Y: uint8(enhanced)})
		}
	}
	return out
}

// ---- Neural OCR service client ----
//
// The Go backend posts the screenshot to a self-hosted RapidOCR microservice
// (see ocr-service/) and receives recognised text plus a bounding box per line.
// This mirrors the mobile app, which reads the same screenshots with Google
// ML Kit and filters lines by position. The engine can be swapped to PaddleOCR
// service-side without any change here.

type ocrLine struct {
	Text  string  `json:"text"`
	X1    int     `json:"x1"`
	Y1    int     `json:"y1"`
	X2    int     `json:"x2"`
	Y2    int     `json:"y2"`
	Score float64 `json:"score"`
}

func (l ocrLine) midY() int      { return (l.Y1 + l.Y2) / 2 }
func (l ocrLine) boxHeight() int { return l.Y2 - l.Y1 }

type ocrResult struct {
	FullText string    `json:"full_text"`
	Lines    []ocrLine `json:"lines"`
}

func ocrServiceURL() string {
	if v := os.Getenv("OCR_SERVICE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:18265/ocr"
}

// recognizeText posts image bytes to the OCR service and decodes the response.
// recognizeTextFn is swappable so tests can inject recorded or synthetic OCR
// service responses without a live RapidOCR instance.
var recognizeTextFn = recognizeText

func recognizeText(imgBytes []byte) (*ocrResult, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("image", "scan.jpg")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(imgBytes); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, ocrServiceURL(), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr service status %d", resp.StatusCode)
	}
	var out ocrResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- CP extraction (mobile extractCP parity) ----

var (
	reAllNumsBounded = regexp.MustCompile(`\b\d{2,5}\b`)
	reCPMisread      = regexp.MustCompile(`(?i)[co0][ph]\s*(\d{1,5})`)
	reNonDigits      = regexp.MustCompile(`\D+`)
)

// cpNumsFromLine strips a line to a single integer candidate in [10,50000].
// Lines that look like a clock ("10:27") are skipped so the device status bar
// can never be read as CP.
func cpNumsFromLine(text string) int {
	if strings.Contains(text, ":") {
		return 0
	}
	digits := reNonDigits.ReplaceAllString(text, "")
	if digits == "" {
		return 0
	}
	v, err := strconv.Atoi(digits)
	if err != nil || v < 10 || v > 50000 {
		return 0
	}
	return v
}

// extractCP reads the big "CP2717" number from the top 25% of the screen.
func extractCP(lines []ocrLine, fullText string, h int) int {
	topZone := float64(h) * 0.25
	var top []ocrLine
	for _, l := range lines {
		if float64(l.midY()) < topZone {
			top = append(top, l)
		}
	}

	// Strategy 1: prefer lines containing "CP".
	best := 0
	for _, l := range top {
		if strings.Contains(strings.ToUpper(l.Text), "CP") {
			if v := cpNumsFromLine(l.Text); v > best {
				best = v
			}
		}
	}
	if best > 0 {
		return best
	}

	// Strategy 2: any number in the top zone (CP dwarfs status-bar digits).
	for _, l := range top {
		if v := cpNumsFromLine(l.Text); v > best {
			best = v
		}
	}
	if best > 0 {
		return best
	}

	// Strategy 3: OCR misread fallback (C->O, P->H).
	if m := reCPMisread.FindStringSubmatch(fullText); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v >= 10 && v <= 50000 {
			return v
		}
	}
	return 0
}

// retryCP re-runs OCR on a grayscale, contrast-boosted top-18% crop. Large
// Pokemon models occlude the CP digits and the first pass can underread; taking
// max(primary, retry) recovers the higher value (mobile parity).
func retryCP(img image.Image) int {
	crop := cropPct(img, ocrRegion{0.00, 0.00, 1.00, 0.18})
	enhanced := contrastEnhance(crop)
	b, err := encodeJPEG(enhanced)
	if err != nil {
		return 0
	}
	res, err := recognizeTextFn(b)
	if err != nil || res == nil {
		return 0
	}
	best := 0
	for _, m := range reAllNumsBounded.FindAllString(res.FullText, -1) {
		if v, err := strconv.Atoi(m); err == nil && v >= 10 && v <= 50000 && v > best {
			best = v
		}
	}
	return best
}

// ---- Arc detection (GoIV-model level arc) ----
//
// The level arc is a semicircle squished vertically by 0.95, centered at
// (W/2, baselineY) with both endpoints ON the horizontal diameter. The white
// level dot sits at angle pi*(1+t) where t is LINEAR IN CPM:
//   t(level) = (CPM(level) - CPM(1)) / (CPM(maxPokeLevel) - CPM(1))
// Geometry priors were measured from marked 1080x2340 reference screenshots
// (2026-07-05): endpoints (0.0939W, 0.6172W) and (0.9050W, 0.6172W). All
// vertical quantities anchor to WIDTH, not height: the arc scales with screen
// width while total height varies with device aspect ratio. Per-image endpoint
// auto-detection refines the priors when the arc tips are visible.

const (
	arcPriorCYW   = 0.6172 // arc baseline (endpoint) y as a fraction of width
	arcPriorRW    = 0.4056 // arc radius as a fraction of width
	arcSquish     = 0.95   // vertical squish factor (GoIV LEVEL_ARC_SQUISH_FACTOR)
	arcTipWinW    = 0.040  // half-size of the endpoint search window, fraction of width
	arcDotMinW    = 0.010  // min white-run through the level dot, fraction of width
	arcDotMaxW    = 0.028  // max white-run through the level dot, fraction of width
	arcStrokeMaxW = 0.008  // runs at or below this are the arc stroke, not the dot
)

type arcReading struct {
	Level      float64
	OK         bool
	Calibrated bool // endpoint tips auto-detected (vs width-fraction priors)
	RunPx      int  // white-run length through the accepted dot
}

func isNearWhiteAt(img image.Image, x, y int) bool {
	b := img.Bounds()
	px, py := b.Min.X+x, b.Min.Y+y
	if px < b.Min.X || px >= b.Max.X || py < b.Min.Y || py >= b.Max.Y {
		return false
	}
	r, g, bl, _ := img.At(px, py).RGBA()
	return r>>8 > 230 && g>>8 > 230 && bl>>8 > 230
}

// findArcTip locates the bottom tip of the arc stroke inside a search window:
// the lowest cluster of near-white pixels. Returns the cluster centroid.
func findArcTip(img image.Image, xLo, xHi, yLo, yHi int) (float64, float64, bool) {
	maxY := -1
	for y := yLo; y <= yHi; y++ {
		for x := xLo; x <= xHi; x++ {
			if isNearWhiteAt(img, x, y) && isNearWhiteAt(img, x, y-1) {
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxY < 0 {
		return 0, 0, false
	}
	// Centroid of white pixels in the bottom rows of the cluster.
	var sx, sy, n float64
	for y := maxY - 4; y <= maxY; y++ {
		for x := xLo; x <= xHi; x++ {
			if isNearWhiteAt(img, x, y) {
				sx += float64(x)
				sy += float64(y)
				n++
			}
		}
	}
	if n < 6 {
		return 0, 0, false
	}
	return sx / n, sy / n, true
}

// detectArcLevel reads the Pokemon's level from the white dot on the level
// arc. Species-independent: only the trainer level (arc endpoint = max
// power-up level) and the CPM table are needed.
func detectArcLevel(img image.Image, trainerLevel int, cpms []cpmEntry) arcReading {
	b := img.Bounds()
	fw := float64(b.Dx())
	fh := float64(b.Dy())
	if fw < 200 || fh <= fw { // landscape or tiny images are not status screens
		return arcReading{}
	}

	maxLvl := maxPowerUpLevel(trainerLevel)
	cpmByLevel := cpmLookup(cpms)
	base, okBase := cpmByLevel[1.0]
	top, okTop := cpmByLevel[maxLvl]
	if !okBase || !okTop || top <= base {
		return arcReading{}
	}

	// Geometry: width-anchored priors, refined by endpoint auto-detection.
	cx := 0.5 * fw
	cy := arcPriorCYW * fw
	radius := arcPriorRW * fw
	calibrated := false
	win := int(arcTipWinW * fw)
	lxp := int((0.5 - arcPriorRW) * fw)
	rxp := int((0.5 + arcPriorRW) * fw)
	cyp := int(arcPriorCYW * fw)
	lx, ly, okL := findArcTip(img, lxp-win, lxp+win, cyp-win, cyp+win)
	rx, ry, okR := findArcTip(img, rxp-win, rxp+win, cyp-win, cyp+win)
	if okL && okR {
		sameRow := math.Abs(ly-ry) <= 0.012*fw
		centered := math.Abs((lx+rx)/2-0.5*fw) <= 0.02*fw
		radOK := (rx-lx)/2 >= 0.30*fw && (rx-lx)/2 <= 0.48*fw
		if sameRow && centered && radOK {
			cx = (lx + rx) / 2
			cy = (ly + ry) / 2
			radius = (rx - lx) / 2
			calibrated = true
		}
	}
	if cy-arcSquish*radius < 0 || cy >= fh {
		return arcReading{} // arc would leave the frame; not a status screen
	}

	dotMin := int(arcDotMinW * fw)
	dotMax := int(arcDotMaxW * fw)
	strokeMax := int(arcStrokeMaxW * fw)

	// Walk candidate half-levels from the arc maximum down to 1, probing for
	// a dot-sized white run along the radial normal. Large white shapes
	// (buddy bubbles, clouds) exceed dotMax; the bare arc stroke stays under
	// strokeMax; only the level dot fits between.
	bestLevel, bestScore, bestRun := 0.0, -1, 0
	for lvl := maxLvl; lvl >= 1; lvl -= 0.5 {
		cpm, ok := cpmByLevel[lvl]
		if !ok {
			continue
		}
		t := (cpm - base) / (top - base)
		theta := math.Pi * (1 + t)
		px := cx + radius*math.Cos(theta)
		py := cy + arcSquish*radius*math.Sin(theta)
		if !isNearWhiteAt(img, int(math.Round(px)), int(math.Round(py))) {
			continue
		}
		nx, ny := math.Cos(theta), arcSquish*math.Sin(theta)
		nl := math.Hypot(nx, ny)
		nx, ny = nx/nl, ny/nl
		pos, neg := 0, 0
		for off := 1; off <= dotMax+2; off++ {
			if !isNearWhiteAt(img, int(math.Round(px+float64(off)*nx)), int(math.Round(py+float64(off)*ny))) {
				break
			}
			pos++
		}
		for off := 1; off <= dotMax+2; off++ {
			if !isNearWhiteAt(img, int(math.Round(px-float64(off)*nx)), int(math.Round(py-float64(off)*ny))) {
				break
			}
			neg++
		}
		run := pos + neg + 1
		if run <= strokeMax || run < dotMin || run > dotMax {
			continue
		}
		// Prefer the probe passing closest to the dot center: long run,
		// balanced on both sides.
		score := run - int(math.Abs(float64(pos-neg)))
		if score > bestScore {
			bestScore = score
			bestRun = run
			bestLevel = lvl
		}
	}
	if bestLevel == 0 {
		return arcReading{Calibrated: calibrated}
	}

	// Refine tangentially: at high levels the half-level spacing (a few px)
	// is far smaller than the dot, so several candidates touch it. Measure
	// the white run ALONG the arc to locate the dot center, then snap to the
	// nearest half-level.
	if cpm, ok := cpmByLevel[bestLevel]; ok {
		t := (cpm - base) / (top - base)
		theta := math.Pi * (1 + t)
		// Probe on lines radially offset from the arc: the stroke (which is
		// also white and runs along the tangent) is ~strokeMax/2 wide, while
		// the dot bulges well past it, so at this offset only the dot is hit.
		radialOff := float64(strokeMax)/2 + 2
		nx, ny := math.Cos(theta), arcSquish*math.Sin(theta)
		nl := math.Hypot(nx, ny)
		nx, ny = nx/nl, ny/nl
		// Tangent (direction of increasing level).
		tx, ty := -math.Sin(theta), arcSquish*math.Cos(theta)
		tl := math.Hypot(tx, ty)
		tx, ty = tx/tl, ty/tl
		var offsets []float64
		for _, side := range []float64{radialOff, -radialOff} {
			bx := cx + radius*math.Cos(theta) + side*nx
			by := cy + arcSquish*radius*math.Sin(theta) + side*ny
			if !isNearWhiteAt(img, int(math.Round(bx)), int(math.Round(by))) {
				continue
			}
			posT, negT := 0, 0
			for off := 1; off <= dotMax; off++ {
				if !isNearWhiteAt(img, int(math.Round(bx+float64(off)*tx)), int(math.Round(by+float64(off)*ty))) {
					break
				}
				posT++
			}
			for off := 1; off <= dotMax; off++ {
				if !isNearWhiteAt(img, int(math.Round(bx-float64(off)*tx)), int(math.Round(by-float64(off)*ty))) {
					break
				}
				negT++
			}
			offsets = append(offsets, float64(posT-negT)/2)
		}
		offsetPx := 0.0
		for _, o := range offsets {
			offsetPx += o
		}
		if len(offsets) > 0 {
			offsetPx /= float64(len(offsets))
		}
		// Pixels of arc per half-level step around bestLevel.
		if next, ok2 := cpmByLevel[bestLevel+0.5]; ok2 && offsetPx != 0 {
			perHalf := radius * math.Pi * (next - cpm) / (top - base)
			if perHalf > 0.5 {
				halves := math.Round(offsetPx / perHalf)
				if halves > 2 {
					halves = 2
				}
				if halves < -2 {
					halves = -2
				}
				refined := bestLevel + 0.5*halves
				if refined >= 1 && refined <= maxLvl {
					bestLevel = refined
				}
			}
		}
	}
	return arcReading{Level: bestLevel, OK: true, Calibrated: calibrated, RunPx: bestRun}
}

// computeMaxCP returns the max CP for a Pokemon at all-15 IVs at trainer level.
func computeMaxCP(poke pokemonStatEntry, trainerLevel int, cpms []cpmEntry) int {
	// Highest level this trainer could have powered the Pokemon up to.
	// cpmLookup extends the table to 51 (live upstream stops at 45).
	pokeLvl := maxPowerUpLevel(trainerLevel)
	bestCPM := cpmLookup(cpms)[pokeLvl]
	if bestCPM == 0 {
		return 0
	}
	atk := float64(poke.BaseAttack + 15)
	def := float64(poke.BaseDefense + 15)
	sta := float64(poke.BaseStamina + 15)
	return int(math.Floor(atk * math.Sqrt(def) * math.Sqrt(sta) * bestCPM * bestCPM / 10))
}

// ---- Name detection (mobile extractName parity) ----

var (
	reFooterName   = regexp.MustCompile(`This\s+([A-Z][a-z]+(?:[- ][A-Z][a-z]+)?)\s+was\s+caught`)
	// RapidOCR often drops the spaces ("ALTARIAMEGAENERGY"), so they are optional.
	reMegaName     = regexp.MustCompile(`(?i)([A-Za-z][A-Za-z\-]{2,}?)\s*MEGA\s*ENERGY`)
	nameExclusions = map[string]bool{
		"normal": true, "fire": true, "water": true, "grass": true, "electric": true,
		"ice": true, "fighting": true, "poison": true, "ground": true, "flying": true,
		"psychic": true, "bug": true, "rock": true, "ghost": true, "dragon": true,
		"dark": true, "steel": true, "fairy": true, "attack": true, "defense": true,
		"hp": true, "cp": true, "candy": true, "stardust": true, "mega": true,
		"energy": true, "lucky": true, "weather": true, "boosted": true, "purified": true,
		"shadow": true, "height": true, "weight": true, "evolve": true, "power": true,
		"shortest": true, "shorter": true, "short": true,
		"tall": true, "taller": true, "tallest": true,
		"lightest": true, "lighter": true, "light": true,
		"heavy": true, "heavier": true, "heaviest": true,
		"average": true,
	}
)

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = upperFirst(w)
		}
	}
	return strings.Join(words, " ")
}

func detectName(lines []ocrLine, fullText string, h int) (string, string) {
	if m := reFooterName.FindStringSubmatch(fullText); len(m) > 1 {
		if name := strings.TrimSpace(m[1]); name != "" {
			return titleCase(name), "footer"
		}
	}
	if m := reMegaName.FindStringSubmatch(fullText); len(m) > 1 {
		if name := strings.TrimSpace(m[1]); name != "" {
			return titleCase(name), "mega"
		}
	}

	// Card zone: tallest text line between 35% and 65% height that looks like a name.
	cardTop := float64(h) * 0.35
	cardBot := float64(h) * 0.65
	bestName, bestH := "", 0
	for _, l := range lines {
		my := float64(l.midY())
		if my < cardTop || my > cardBot {
			continue
		}
		name := cleanOCRName(l.Text)
		if name == "" || len(name) < 2 || len(name) > 20 {
			continue
		}
		if nameExclusions[strings.ToLower(name)] {
			continue
		}
		skip := false
		for _, w := range strings.Fields(strings.ToLower(name)) {
			if nameExclusions[w] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if l.boxHeight() > bestH {
			bestH = l.boxHeight()
			bestName = name
		}
	}
	if bestName != "" {
		return bestName, "card"
	}
	return "", ""
}

func cleanOCRName(text string) string {
	var b strings.Builder
	for _, c := range text {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' || c == '\'' || c == ' ' {
			b.WriteRune(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ---- HP detection ----

var (
	reOCRDigitFix = regexp.MustCompile(`(\d)[Oo](\d)`)
	reOCRSlashFix = regexp.MustCompile(`([\s/])[Oo](\d)`)
	reHPFraction  = regexp.MustCompile(`(?i)(\d{1,4})\s*/\s*\d{1,4}\s*HP`)
)

func fixOCRDigits(s string) string {
	s = reOCRDigitFix.ReplaceAllString(s, "${1}0${2}")
	s = reOCRSlashFix.ReplaceAllString(s, "${1}0${2}")
	return s
}

// extractHP pulls the current HP from "104 / 104 HP" anywhere in the OCR text.
func extractHP(fullText string) int {
	fixed := fixOCRDigits(fullText)
	if m := reHPFraction.FindStringSubmatch(fixed); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 0
}

// ---- Dust detection ----

// buildDisplayableDust derives every dust value the game can display from the
// base tiers in dustBrackets (iv.go) crossed with the status modifiers in
// dustMods. Single source of truth: fixing a tier fixes detection too.
func buildDisplayableDust() []int {
	seen := make(map[int]bool)
	for _, b := range dustBrackets {
		for _, m := range dustMods {
			if v := int(math.Round(float64(b.Dust) * m.Mult)); v > 0 {
				seen[v] = true
			}
		}
	}
	all := make([]int, 0, len(seen))
	for v := range seen {
		all = append(all, v)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(all)))
	return all
}

var (
	dustDisplayable   = buildDisplayableDust()
	reDustNum         = regexp.MustCompile(`\b(\d{1,6})\b`)
	reSpacedThousands = regexp.MustCompile(`\b(\d{1,3}) (\d{3})\b`)
)

func detectRawDust(text string) int {
	// Normalize Tesseract/OCR quirk: "4 000" -> "4000" before comma removal
	normalized := reSpacedThousands.ReplaceAllString(text, "$1$2")
	clean := strings.ReplaceAll(normalized, ",", "")
	clean = strings.ReplaceAll(clean, ".", "")
	nums := make(map[int]bool)
	for _, m := range reDustNum.FindAllString(clean, -1) {
		if v, err := strconv.Atoi(m); err == nil {
			nums[v] = true
		}
	}
	for _, target := range dustDisplayable {
		if nums[target] {
			return target
		}
	}
	return 0
}

// summariseDustInterpretations reduces the fuzzy interpretation set to display
// values for the extracted card: the base tier when every interpretation
// agrees on it (else the raw value), and status flags only when unambiguous.
func summariseDustInterpretations(rawDust int, ranges []levelRange) (normDust int, lucky, shadow, purified bool) {
	normDust = rawDust
	if len(ranges) == 0 {
		return
	}
	tier := ranges[0].BaseTier
	lucky, shadow, purified = ranges[0].Lucky, ranges[0].Shadow, ranges[0].Purified
	for _, lr := range ranges[1:] {
		if lr.BaseTier != tier {
			tier = 0
		}
		lucky = lucky && lr.Lucky
		shadow = shadow && lr.Shadow
		purified = purified && lr.Purified
	}
	if tier != 0 {
		normDust = tier
	}
	return
}

// ---- Appraisal star detection (gap-based, mobile AppraisalBarDetector parity) ----

func isOrangePixel(r, g, b uint8) bool {
	return r > 180 && g > 80 && b < 120 && r > g && (int(r)-int(b)) > 100
}

func isRainbowPixel(r, g, b uint8) bool {
	isMagenta := r > 200 && b > 120 && g < 190 && (int(r)-int(g)) > 60 && b > g
	isPurple := b > 150 && b >= r && g < 150
	isTeal := g > 160 && b > 140 && r < 160
	return isMagenta || isPurple || isTeal
}

func countStarsFromText(text string) int {
	n := strings.Count(text, "★")
	if n >= 1 && n <= 3 {
		return n
	}
	return -1
}

// detectStars returns (starCount 0-3, isHundo). Returns -1 if detection fails.
func detectStars(img image.Image) (int, bool) {
	b := img.Bounds()
	w := b.Max.X - b.Min.X
	h := b.Max.Y - b.Min.Y
	if w <= 0 || h <= 0 {
		return -1, false
	}

	const numRows = 13
	const samplesPerRow = 50
	const yStart = 0.58
	const yEnd = 0.70
	const xEnd = 0.25

	orangeHits := make([]int, numRows)
	rainbowTotal := 0

	for i := 0; i < numRows; i++ {
		yFrac := yStart + float64(i)*(yEnd-yStart)/float64(numRows-1)
		py := b.Min.Y + int(math.Round(float64(h)*yFrac))
		if py < b.Min.Y || py >= b.Max.Y {
			continue
		}
		for j := 0; j < samplesPerRow; j++ {
			xFrac := float64(j) / float64(samplesPerRow-1) * xEnd
			px := b.Min.X + int(math.Round(float64(w)*xFrac))
			if px < b.Min.X || px >= b.Max.X {
				continue
			}
			rr, gg, bb, _ := img.At(px, py).RGBA()
			rv, gv, bv := uint8(rr>>8), uint8(gg>>8), uint8(bb>>8)
			if isOrangePixel(rv, gv, bv) {
				orangeHits[i]++
			}
			if isRainbowPixel(rv, gv, bv) {
				rainbowTotal++
			}
		}
	}

	total, firstHit, lastHit := 0, -1, -1
	for i, hits := range orangeHits {
		total += hits
		if hits > 0 {
			if firstHit == -1 {
				firstHit = i
			}
			lastHit = i
		}
	}

	if total < 2 || firstHit == -1 {
		return -1, false
	}

	hasGap := false
	for i := firstHit + 1; i < lastHit; i++ {
		if orangeHits[i] == 0 {
			hasGap = true
			break
		}
	}

	stars := 0
	switch {
	case !hasGap && total >= 10:
		stars = 3
	case hasGap:
		inner := 0
		for i := firstHit + 1; i < lastHit; i++ {
			inner += orangeHits[i]
		}
		if inner >= 10 {
			stars = 2
		} else {
			stars = 1
		}
	case total >= 3:
		stars = 1
	default:
		return -1, false
	}

	return stars, stars == 3 && rainbowTotal >= 3
}

// ---- Status keyword detection ----

func containsWordCI(text, word string) bool {
	lower := strings.ToLower(text)
	for {
		idx := strings.Index(lower, word)
		if idx < 0 {
			return false
		}
		before := idx == 0 || !isAlpha(lower[idx-1])
		after := idx+len(word) >= len(lower) || !isAlpha(lower[idx+len(word)])
		if before && after {
			return true
		}
		lower = lower[idx+1:]
	}
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ---- Response type ----

type ocrExtracted struct {
	CP             int    `json:"cp"`
	CPSource       string `json:"cp_source"`
	HP             int    `json:"hp"`
	RawDust        int    `json:"raw_dust"`
	NormalisedDust int    `json:"normalised_dust"`
	PokemonName    string `json:"pokemon_name"`
	NameSource     string `json:"name_source"`
	AppraisalBars  int    `json:"appraisal_bars"`
	IsHundo        bool   `json:"is_hundo"`
	IsLucky        bool    `json:"is_lucky"`
	IsShadow       bool    `json:"is_shadow"`
	IsPurified     bool    `json:"is_purified"`
	RawCP          string  `json:"raw_cp"`
	ArcLevel       float64 `json:"arc_level"` // 0 = arc not read
}

// unanimousCP returns the CP shared by every candidate, or 0 when they
// disagree (or there are none).
func unanimousCP(candidates []IVCandidate) int {
	cp := 0
	for _, c := range candidates {
		if cp == 0 {
			cp = c.CP
		} else if c.CP != cp {
			return 0
		}
	}
	return cp
}

// runOCRSearch picks the strongest available constraint set for a scan:
// exact CP + fuzzy dust (with an arc rescue when the CP digits were likely
// misread), exact CP over all reachable levels when dust is unreadable, or
// the arc level (+dust intersection) when no trustworthy CP exists.
// Returns (candidates, bestBuddyAssumed, arcRescueUsed).
func runOCRSearch(req ivRequest, dustRanges []levelRange, arc arcReading, poke pokemonStatEntry, cpms []cpmEntry) ([]IVCandidate, bool, bool) {
	switch {
	case req.CP > 0 && len(dustRanges) > 0:
		candidates, buddy := enumerateWithBuddyRetry(req, dustRanges, poke, cpms)
		// Rescue: exact CP+dust matched nothing but the arc pinned a level;
		// the CP digits were probably misread.
		if len(candidates) == 0 && arc.OK {
			cpFree := req
			cpFree.CP = 0
			ranges := intersectRangesWithLevel(dustRanges, arc.Level, 0.5)
			if rescued, b := enumerateWithBuddyRetry(cpFree, ranges, poke, cpms); len(rescued) > 0 {
				return rescued, b, true
			}
		}
		return candidates, buddy, false
	case req.CP > 0:
		// Dust unreadable: exact CP+HP across every reachable level.
		full := []levelRange{{MinLvl: 1, MaxLvl: maxPowerUpLevel(req.TrainerLevel)}}
		candidates, buddy := enumerateWithBuddyRetry(req, full, poke, cpms)
		return candidates, buddy, false
	default:
		// No trustworthy CP: arc level (+dust when readable) with HP.
		cpFree := req
		cpFree.CP = 0
		ranges := intersectRangesWithLevel(dustRanges, arc.Level, 0.5)
		candidates, buddy := enumerateWithBuddyRetry(cpFree, ranges, poke, cpms)
		return candidates, buddy, false
	}
}

// ---- Handler ----

func (h *Handlers) IVFromOCR(w http.ResponseWriter, r *http.Request) {
	const maxBytes = 8 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSONError(w, "image too large (max 8 MB)", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		writeJSONError(w, "missing 'image' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	imgBytes, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, "could not read image", http.StatusBadRequest)
		return
	}
	sniffed := http.DetectContentType(imgBytes)
	if sniffed != "image/jpeg" && sniffed != "image/png" {
		writeJSONError(w, "unsupported format; send JPEG or PNG", http.StatusBadRequest)
		return
	}

	// Check the declared dimensions before decoding. The 8 MB cap above is on the
	// COMPRESSED bytes, and PNG happily compresses a solid 30000x30000 image into
	// about a kilobyte; image.Decode would then allocate 3.6 GB of pixel buffer and
	// take the process down. DecodeConfig reads only the header.
	const maxPixels = 40 << 20 // 40 MP, roughly 4x the largest phone screenshot
	cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes))
	if err != nil {
		writeJSONError(w, "could not decode image", http.StatusBadRequest)
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		writeJSONError(w, "image dimensions too large", http.StatusBadRequest)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		writeJSONError(w, "could not decode image", http.StatusBadRequest)
		return
	}
	bounds := img.Bounds()
	imgH := bounds.Max.Y - bounds.Min.Y

	// Trainer level: DB value takes precedence for authenticated users;
	// query param only applies for unauthenticated requests.
	trainerLevel := 40
	isAuthed := false
	if u := h.currentUser(r); u != nil {
		isAuthed = true
		h.db.QueryRow(`SELECT COALESCE(trainer_level, 40) FROM users WHERE id = ?`, u.ID).Scan(&trainerLevel)
	}
	if !isAuthed {
		if tl := r.URL.Query().Get("trainer_level"); tl != "" {
			if v, err2 := strconv.Atoi(tl); err2 == nil && v >= 1 && v <= 51 {
				trainerLevel = v
			}
		}
	}

	// Neural OCR pass (RapidOCR service). Degrade gracefully if unreachable.
	var lines []ocrLine
	fullText := ""
	if res, err := recognizeTextFn(imgBytes); err != nil {
		log.Printf("OCR service error: %v", err)
	} else {
		lines = res.Lines
		fullText = res.FullText
	}

	isLucky := containsWordCI(fullText, "lucky")
	isShadow := containsWordCI(fullText, "shadow")
	isPurified := containsWordCI(fullText, "purified")

	pokemonName, nameSource := detectName(lines, fullText, imgH)

	cpText := extractCP(lines, fullText, imgH)
	bestTextCP := cpText
	// Only pay for the contrast retry pass when the primary read found no CP
	// (the neural OCR almost always gets it first try; retry is the occlusion safety net).
	cpRetry := 0
	if cpText < 10 {
		cpRetry = retryCP(img)
		if cpRetry > bestTextCP {
			bestTextCP = cpRetry
		}
	}
	log.Printf("OCR CP text: primary=%d retry=%d bestTextCP=%d", cpText, cpRetry, bestTextCP)

	// Load game data
	var pokeList []pokemonStatEntry
	_ = json.Unmarshal(h.store.Pokemon(), &pokeList)
	var cpms []cpmEntry
	_ = json.Unmarshal(h.store.CPMultipliers(), &cpms)

	var poke *pokemonStatEntry
	if pokemonName != "" {
		var firstMatch *pokemonStatEntry
		for i := range pokeList {
			if !strings.EqualFold(pokeList[i].PokemonName, pokemonName) {
				continue
			}
			if firstMatch == nil {
				firstMatch = &pokeList[i]
			}
			if strings.EqualFold(pokeList[i].Form, "Normal") {
				poke = &pokeList[i]
				break
			}
		}
		if poke == nil {
			poke = firstMatch
		}
	}

	// Fallback: scan full OCR text for the earliest-appearing known Pokémon name.
	if poke == nil && len(pokeList) > 0 {
		lft := strings.ToLower(fullText)
		bestPos := len(lft) + 1
		for i := range pokeList {
			name := strings.ToLower(pokeList[i].PokemonName)
			if len(name) < 4 {
				continue
			}
			idx := strings.Index(lft, name)
			if idx < 0 || idx >= bestPos {
				continue
			}
			before := idx == 0 || !isAlpha(lft[idx-1])
			after := idx+len(name) >= len(lft) || !isAlpha(lft[idx+len(name)])
			if before && after {
				bestPos = idx
				pokemonName = titleCase(pokeList[i].PokemonName)
				nameSource = "fulltext"
				poke = &pokeList[i]
			}
		}
		// Prefer Normal form over regional variants when names collide.
		if poke != nil && !strings.EqualFold(poke.Form, "Normal") {
			for i := range pokeList {
				if strings.EqualFold(pokeList[i].PokemonName, pokemonName) &&
					strings.EqualFold(pokeList[i].Form, "Normal") {
					poke = &pokeList[i]
					break
				}
			}
		}
	}

	arc := arcReading{}
	if len(cpms) > 0 {
		arc = detectArcLevel(img, trainerLevel, cpms)
	}
	log.Printf("OCR arc: ok=%v level=%.1f calibrated=%v runPx=%d", arc.OK, arc.Level, arc.Calibrated, arc.RunPx)

	bestCP := bestTextCP
	cpSource := "text"
	// Plausibility: a text CP above the species' absolute max for this trainer
	// (plus Best-Buddy headroom) is a misread; fall back to the arc level.
	if bestCP >= 10 && poke != nil {
		if maxCP := computeMaxCP(*poke, trainerLevel, cpms); maxCP > 0 && bestCP > maxCP*106/100 {
			log.Printf("OCR CP implausible: bestCP=%d speciesMax=%d, discarding", bestCP, maxCP)
			bestCP = 0
		}
	}
	if bestCP < 10 {
		bestCP = 0
		if arc.OK {
			cpSource = "arc-level"
		} else {
			cpSource = "none"
		}
	}
	log.Printf("OCR result: pokemon=%q trainerLevel=%d arcLevel=%.1f bestCP=%d source=%s",
		pokemonName, trainerLevel, arc.Level, bestCP, cpSource)

	hp := extractHP(fullText)

	rawDust := detectRawDust(fullText)
	// Text-detected status flags are trusted only when positive: the lucky
	// banner or shadow/purified marker can be off-frame, so absence must not
	// exclude those dust interpretations.
	var luckyFlag, shadowFlag, purifiedFlag *bool
	if isLucky {
		luckyFlag = boolPtr(true)
	}
	if isShadow {
		shadowFlag = boolPtr(true)
	}
	if isPurified {
		purifiedFlag = boolPtr(true)
	}
	dustRanges := dustCandidates(rawDust, luckyFlag, shadowFlag, purifiedFlag)
	normDust, dustLucky, dustShadow, dustPurified := summariseDustInterpretations(rawDust, dustRanges)
	isLucky = isLucky || dustLucky
	isShadow = isShadow || dustShadow
	isPurified = isPurified || dustPurified
	log.Printf("OCR dust: rawDust=%d interpretations=%d normDust=%d", rawDust, len(dustRanges), normDust)

	stars := countStarsFromText(fullText)
	isHundo := false
	if stars < 0 && containsWordCI(fullText, "attack") &&
		containsWordCI(fullText, "defense") && containsWordCI(fullText, "hp") {
		stars, isHundo = detectStars(cropPct(img, ocrRegionAppraisalScreen))
	}

	var appraisalBars *int
	if stars >= 0 {
		appraisalBars = &stars
	}
	baseReq := ivRequest{
		CP:            bestCP,
		HP:            hp,
		DustCost:      rawDust,
		TrainerLevel:  trainerLevel,
		AppraisalBars: appraisalBars,
		IsLucky:       luckyFlag,
		IsShadow:      shadowFlag,
		IsPurified:    purifiedFlag,
	}
	searchable := hp > 0 && len(cpms) > 0 && (bestCP > 0 || arc.OK)

	// Nickname fallback: no species matched by name, but the candy line names
	// the family base ("3 MACHOP CANDY" on a mon nicknamed "John Cena").
	// Disambiguate by which family member's stats actually fit the scan.
	var speciesCandidates []string
	if poke == nil && searchable && len(pokeList) > 0 {
		knownSet := make(map[string]bool, len(pokeList))
		for i := range pokeList {
			knownSet[strings.ToLower(pokeList[i].PokemonName)] = true
		}
		base := detectCandyBase(fullText, func(n string) bool { return knownSet[strings.ToLower(n)] })
		if base != "" {
			type fit struct {
				poke  *pokemonStatEntry
				count int
			}
			var fits []fit
			for _, name := range familySpecies(base, h.store.Evolutions()) {
				sp := findSpecies(pokeList, name)
				if sp == nil {
					continue
				}
				req := baseReq
				req.PokemonName = sp.PokemonName
				if cands, _, _ := runOCRSearch(req, dustRanges, arc, *sp, cpms); len(cands) > 0 {
					fits = append(fits, fit{sp, len(cands)})
				}
			}
			sort.Slice(fits, func(i, j int) bool { return fits[i].count < fits[j].count })
			if len(fits) > 0 {
				poke = fits[0].poke
				pokemonName = titleCase(poke.PokemonName)
				nameSource = "candy"
				for _, f := range fits {
					speciesCandidates = append(speciesCandidates, titleCase(f.poke.PokemonName))
				}
			}
			log.Printf("OCR candy: base=%q familyFits=%d", base, len(fits))
		}
	}

	ext := ocrExtracted{
		CP:             bestCP,
		CPSource:       cpSource,
		HP:             hp,
		RawDust:        rawDust,
		NormalisedDust: normDust,
		PokemonName:    pokemonName,
		NameSource:     nameSource,
		AppraisalBars:  stars,
		IsHundo:        isHundo,
		IsLucky:        isLucky,
		IsShadow:       isShadow,
		IsPurified:     isPurified,
		RawCP:          strconv.Itoa(cpText) + "/" + strconv.Itoa(cpRetry),
	}
	if arc.OK {
		ext.ArcLevel = arc.Level
	}

	resp := map[string]any{
		"extracted":  ext,
		"candidates": []IVCandidate{},
		"count":      0,
		"definitive": false,
	}
	if len(speciesCandidates) > 1 {
		resp["species_candidates"] = speciesCandidates
	}

	if searchable && poke != nil {
		resp["pokemon"] = poke
		req := baseReq
		req.PokemonName = pokemonName
		candidates, buddyAssumed, arcRescue := runOCRSearch(req, dustRanges, arc, *poke, cpms)
		// The primary CP read produced no exact match (empty or arc-rescued):
		// pay for the contrast-enhanced re-OCR of the CP zone before settling.
		// A partial read like "CP17" for 1790 passes the primary validity
		// check, so the cheap retry never fired earlier in the handler.
		if (len(candidates) == 0 || arcRescue) && bestCP > 0 && cpRetry == 0 {
			if rv := retryCP(img); rv >= 10 && rv != bestCP {
				retryReq := req
				retryReq.CP = rv
				if c2, b2, r2 := runOCRSearch(retryReq, dustRanges, arc, *poke, cpms); len(c2) > 0 && !r2 {
					log.Printf("OCR CP retry recovered: primary=%d retry=%d candidates=%d", bestCP, rv, len(c2))
					candidates, buddyAssumed, arcRescue = c2, b2, false
					bestCP = rv
					ext.CP = rv
					ext.RawCP = ext.RawCP + "/" + strconv.Itoa(rv)
					resp["extracted"] = ext
				}
			}
		}
		if arcRescue {
			// The rescue proved the text CP wrong (no IV spread matched it).
			// Correct the extracted card: show the rescued CP when the
			// candidates agree on one, else clear it. raw_cp keeps the
			// misread for debugging.
			ext.CP = unanimousCP(candidates)
			ext.CPSource = "arc-level"
			resp["extracted"] = ext
			log.Printf("OCR arc rescue: textCP=%d correctedCP=%d candidates=%d", bestCP, ext.CP, len(candidates))
		}
		fullCount := len(candidates)
		if fullCount > 0 {
			// Aggregate view for wide (CP-free) result sets.
			resp["iv_summary"] = map[string]any{
				"max_pct": candidates[0].IVPct,
				"min_pct": candidates[fullCount-1].IVPct,
			}
		}
		if fullCount > 100 {
			resp["truncated_from"] = fullCount
			candidates = candidates[:100]
		}
		resp["candidates"] = candidates
		resp["count"] = fullCount
		resp["definitive"] = fullCount == 1
		resp["best_buddy_assumed"] = buddyAssumed
		resp["arc_rescue"] = arcRescue
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
