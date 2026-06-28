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
	"strconv"
	"strings"
	"time"
)

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
	res, err := recognizeText(b)
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

// ---- Arc detection (mobile CPArcDetector parity -- fallback only) ----

const (
	arcCXFrac    = 0.500
	arcCYFrac    = 0.259
	arcRFrac     = 0.452
	arcEmptyDeg  = 195
	arcFullDeg   = 96
	arcSpanDeg   = 261
	arcBrightMin = 580
)

func detectArcCP(img image.Image, maxCP int) int {
	if maxCP <= 0 {
		return 0
	}
	b := img.Bounds()
	fw := float64(b.Max.X - b.Min.X)
	fh := float64(b.Max.Y - b.Min.Y)
	cx := fw * arcCXFrac
	cy := fh * arcCYFrac
	r := fw * arcRFrac

	endpointDeg := arcEmptyDeg
	endpointStep := arcSpanDeg + 1
	for step := 0; step <= arcSpanDeg; step++ {
		deg := ((arcFullDeg-step)%360 + 360) % 360
		rad := float64(deg-90) * math.Pi / 180
		x := math.Round(cx + r*math.Cos(rad))
		y := math.Round(cy + r*math.Sin(rad))
		if x/fw > 0.83 && y/fh < 0.18 { // camera icon exclusion (mobile-exact)
			continue
		}
		px := b.Min.X + int(x)
		py := b.Min.Y + int(y)
		if px < b.Min.X || px >= b.Max.X || py < b.Min.Y || py >= b.Max.Y {
			continue
		}
		rr, gg, bb, _ := img.At(px, py).RGBA()
		if int(rr>>8)+int(gg>>8)+int(bb>>8) > arcBrightMin { // sum-based brightness (mobile-exact)
			endpointDeg = deg
			endpointStep = step
			break
		}
	}

	dist := ((endpointDeg - arcEmptyDeg) + 360) % 360
	fillPct := math.Min(float64(dist)/float64(arcSpanDeg), 1.0)
	arcCP := int(math.Round(fillPct * float64(maxCP)))
	log.Printf("OCR arc: maxCP=%d endpointStep=%d endpointDeg=%d xFrac=%.3f yFrac=%.3f fillPct=%.4f arcCP=%d",
		maxCP, endpointStep, endpointDeg,
		math.Round(cx+r*math.Cos(float64(endpointDeg-90)*math.Pi/180))/fw,
		math.Round(cy+r*math.Sin(float64(endpointDeg-90)*math.Pi/180))/fh,
		fillPct, arcCP)
	if arcCP <= 10 {
		return 0
	}
	return arcCP
}

// computeMaxCP returns the max CP for a Pokemon at all-15 IVs at trainer level.
func computeMaxCP(poke pokemonStatEntry, trainerLevel int, cpms []cpmEntry) int {
	pokeLvl := float64(trainerLevel + 2)
	if pokeLvl > 51 {
		pokeLvl = 51
	}
	bestCPM, bestDist := 0.0, math.MaxFloat64
	for _, e := range cpms {
		if d := math.Abs(e.Level - pokeLvl); d < bestDist {
			bestDist = d
			bestCPM = e.Multiplier
		}
	}
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
	reMegaName     = regexp.MustCompile(`(?i)([A-Za-z][A-Za-z\-]{2,})\s+MEGA\s+ENERGY`)
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
			words[i] = strings.ToUpper(w[:1]) + w[1:]
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

// ---- Dust detection + normalisation (mobile parity) ----

var standardDust = []int{
	200, 400, 800, 1000, 1300, 1600, 1900, 2200, 2500,
	3000, 3500, 4000, 4500, 5000, 6000, 7000, 8000, 9000,
	10000, 12000, 15000, 20000,
}

func standardDustSet() map[int]bool {
	s := make(map[int]bool, len(standardDust))
	for _, d := range standardDust {
		s[d] = true
	}
	return s
}

func buildDisplayableDust() []int {
	seen := make(map[int]bool)
	for _, d := range standardDust {
		seen[d] = true
		seen[d/2] = true
		seen[int(float64(d)*0.9)] = true
		seen[d*6] = true
	}
	all := make([]int, 0, len(seen))
	for v := range seen {
		if v > 0 {
			all = append(all, v)
		}
	}
	// sort descending
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j] > all[i] {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
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

func normaliseDust(rawDust int, isLucky, isPurified, isShadow bool) int {
	if isLucky {
		return rawDust * 2
	}
	if isPurified {
		return int(math.Floor(float64(rawDust) * 10.0 / 9.0))
	}
	if isShadow {
		return rawDust / 6
	}
	return rawDust
}

func inferDustFlags(rawDust int, isLucky, isPurified, isShadow bool) (bool, bool, bool) {
	if isLucky || isPurified || isShadow || rawDust == 0 {
		return isLucky, isPurified, isShadow
	}
	stdSet := standardDustSet()
	if stdSet[rawDust] {
		return false, false, false
	}
	// Lucky: displayed cost = standard / 2
	if stdSet[rawDust*2] {
		return true, false, false
	}
	purifiedBase := int(math.Round(float64(rawDust) * 10.0 / 9.0))
	if stdSet[purifiedBase] {
		return false, true, false
	}
	if rawDust%6 == 0 && stdSet[rawDust/6] {
		return false, false, true
	}
	return false, false, false
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
	IsLucky        bool   `json:"is_lucky"`
	IsShadow       bool   `json:"is_shadow"`
	IsPurified     bool   `json:"is_purified"`
	RawCP          string `json:"raw_cp"`
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
	if res, err := recognizeText(imgBytes); err != nil {
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

	arcCP := 0
	if poke != nil {
		arcCP = detectArcCP(img, computeMaxCP(*poke, trainerLevel, cpms))
	}

	bestCP := bestTextCP
	cpSource := "text"
	if bestCP < 10 {
		if arcCP > 0 {
			bestCP = arcCP
			cpSource = "arc"
		} else {
			cpSource = "none"
		}
	}
	log.Printf("OCR result: pokemon=%q trainerLevel=%d arcCP=%d bestCP=%d source=%s",
		pokemonName, trainerLevel, arcCP, bestCP, cpSource)

	hp := extractHP(fullText)

	rawDust := detectRawDust(fullText)
	log.Printf("OCR dust: rawDust=%d", rawDust)
	isLucky, isPurified, isShadow = inferDustFlags(rawDust, isLucky, isPurified, isShadow)
	normDust := normaliseDust(rawDust, isLucky, isPurified, isShadow)
	if normDust == 0 {
		normDust = rawDust
	}

	stars := countStarsFromText(fullText)
	isHundo := false
	if stars < 0 && containsWordCI(fullText, "attack") &&
		containsWordCI(fullText, "defense") && containsWordCI(fullText, "hp") {
		stars, isHundo = detectStars(cropPct(img, ocrRegionAppraisalScreen))
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

	resp := map[string]any{
		"extracted":  ext,
		"candidates": []IVCandidate{},
		"count":      0,
		"definitive": false,
	}

	if bestCP > 0 && hp > 0 && normDust > 0 && poke != nil && len(cpms) > 0 {
		resp["pokemon"] = poke
		var appraisalBars *int
		if stars >= 0 {
			appraisalBars = &stars
		}
		req := ivRequest{
			PokemonName:   pokemonName,
			CP:            bestCP,
			HP:            hp,
			DustCost:      normDust,
			TrainerLevel:  trainerLevel,
			AppraisalBars: appraisalBars,
		}
		candidates := enumerateIVs(req, *poke, cpms)
		resp["candidates"] = candidates
		resp["count"] = len(candidates)
		resp["definitive"] = len(candidates) == 1
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
