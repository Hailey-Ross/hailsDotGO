package handlers

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ocrRegion is a percentage-based crop of a full screenshot (fractions 0-1).
type ocrRegion struct{ x1, y1, x2, y2 float64 }

// Approximate regions for a portrait PoGo Pokemon info screen (16:9 to 19.5:9).
// These assume the phone is held vertically and the Pokemon details screen is visible.
var (
	ocrRegionCP    = ocrRegion{0.20, 0.22, 0.80, 0.36}
	ocrRegionName  = ocrRegion{0.10, 0.10, 0.90, 0.20}
	ocrRegionHP    = ocrRegion{0.45, 0.59, 0.85, 0.67}
	ocrRegionDust  = ocrRegion{0.28, 0.68, 0.72, 0.77}
	ocrRegionStars = ocrRegion{0.20, 0.80, 0.80, 0.91}
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
	// Fallback for image types that don't implement SubImage.
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

// runTesseract writes a cropped image to a temp JPEG, runs tesseract on it,
// and returns the trimmed stdout. psm is the Tesseract page segmentation mode
// (e.g. "7" = single line). allowlist is an optional character whitelist.
func runTesseract(img image.Image, psm, allowlist string) (string, error) {
	f, err := os.CreateTemp("", "pogo-ocr-*.jpg")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 95}); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	args := []string{name, "stdout", "--psm", psm, "-l", "eng"}
	if allowlist != "" {
		args = append(args, "-c", "tessedit_char_whitelist="+allowlist)
	}
	out, err := exec.Command("tesseract", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// isGoldPixel reports whether a pixel matches PoGo's amber/gold appraisal star color.
func isGoldPixel(c color.RGBA) bool {
	return int(c.R) > 180 && int(c.G) > 120 && int(c.B) < 100
}

// detectStars counts filled appraisal stars (0-3) by sampling the horizontal
// midline of three equal zones in the appraisal region for gold pixels.
// Returns -1 if no gold is found (detection failure or 0-star Pokemon).
func detectStars(img image.Image) int {
	b := img.Bounds()
	w := b.Max.X - b.Min.X
	h := b.Max.Y - b.Min.Y
	midY := b.Min.Y + h/2
	starW := float64(w) / 3.0

	filled := 0
	for s := range 3 {
		x1 := b.Min.X + int(math.Round(starW*float64(s)+starW*0.2))
		x2 := b.Min.X + int(math.Round(starW*float64(s)+starW*0.8))
		total := x2 - x1
		if total <= 0 {
			continue
		}
		gold := 0
		for x := x1; x < x2; x++ {
			c := color.RGBAModel.Convert(img.At(x, midY)).(color.RGBA)
			if isGoldPixel(c) {
				gold++
			}
		}
		if gold > total/3 {
			filled++
		}
	}
	if filled == 0 {
		// Cannot distinguish 0 stars from detection failure -- caller passes -1 to
		// skip the IV sum filter rather than incorrectly narrowing to 0-22.
		return -1
	}
	return filled
}

var reDigitsComma = regexp.MustCompile(`[\d,]+`)

func firstIntOCR(text string) int {
	m := reDigitsComma.FindString(text)
	if m == "" {
		return 0
	}
	v, _ := strconv.Atoi(strings.ReplaceAll(m, ",", ""))
	return v
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

type ocrExtracted struct {
	CP            int    `json:"cp"`
	HP            int    `json:"hp"`
	DustCost      int    `json:"dust_cost"`
	PokemonName   string `json:"pokemon_name"`
	AppraisalBars int    `json:"appraisal_bars"`
	RawCP         string `json:"raw_cp"`
	RawHP         string `json:"raw_hp"`
	RawDust       string `json:"raw_dust"`
	RawName       string `json:"raw_name"`
}

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

	// Sniff MIME type from first 512 bytes, then seek back to start.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	sniffed := http.DetectContentType(buf[:n])
	if sniffed != "image/jpeg" && sniffed != "image/png" {
		writeJSONError(w, "unsupported format; send JPEG or PNG", http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		writeJSONError(w, "could not rewind image", http.StatusInternalServerError)
		return
	}

	img, _, err := image.Decode(file)
	if err != nil {
		writeJSONError(w, "could not decode image", http.StatusBadRequest)
		return
	}

	if _, err := exec.LookPath("tesseract"); err != nil {
		writeJSONError(w, "OCR service unavailable", http.StatusServiceUnavailable)
		return
	}

	trainerLevel := 40
	if tl := r.URL.Query().Get("trainer_level"); tl != "" {
		if v, err2 := strconv.Atoi(tl); err2 == nil && v >= 1 && v <= 50 {
			trainerLevel = v
		}
	}

	rawCP, _ := runTesseract(cropPct(img, ocrRegionCP), "7", "0123456789")
	rawName, _ := runTesseract(cropPct(img, ocrRegionName), "7", "")
	rawHP, _ := runTesseract(cropPct(img, ocrRegionHP), "7", "0123456789")
	rawDust, _ := runTesseract(cropPct(img, ocrRegionDust), "7", "0123456789,")

	ext := ocrExtracted{
		RawCP:         rawCP,
		RawHP:         rawHP,
		RawDust:       rawDust,
		RawName:       rawName,
		AppraisalBars: detectStars(cropPct(img, ocrRegionStars)),
	}
	ext.CP = firstIntOCR(rawCP)
	ext.HP = firstIntOCR(rawHP)
	ext.DustCost = firstIntOCR(rawDust)
	ext.PokemonName = cleanOCRName(rawName)

	resp := map[string]any{
		"extracted":  ext,
		"candidates": []IVCandidate{},
		"count":      0,
		"definitive": false,
	}

	if ext.CP > 0 && ext.HP > 0 && ext.DustCost > 0 && ext.PokemonName != "" {
		var pokeList []pokemonStatEntry
		if json.Unmarshal(h.store.Pokemon(), &pokeList) == nil {
			var poke *pokemonStatEntry
			for i := range pokeList {
				if strings.EqualFold(pokeList[i].PokemonName, ext.PokemonName) {
					poke = &pokeList[i]
					break
				}
			}
			if poke != nil {
				resp["pokemon"] = poke
				var cpms []cpmEntry
				if json.Unmarshal(h.store.CPMultipliers(), &cpms) == nil {
					var appraisalBars *int
					if ext.AppraisalBars >= 0 {
						appraisalBars = &ext.AppraisalBars
					}
					req := ivRequest{
						PokemonName:   ext.PokemonName,
						CP:            ext.CP,
						HP:            ext.HP,
						DustCost:      ext.DustCost,
						TrainerLevel:  trainerLevel,
						AppraisalBars: appraisalBars,
					}
					candidates := enumerateIVs(req, *poke, cpms)
					resp["candidates"] = candidates
					resp["count"] = len(candidates)
					resp["definitive"] = len(candidates) == 1
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
