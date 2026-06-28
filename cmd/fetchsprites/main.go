// Command fetchsprites is a one-time ingestion tool for the trainer-avatar library.
//
// It pulls a NEW distinct-art source that the Showdown set does not cover:
//   - pret/pokeplatinum: native Gen 4 DPPt trainer VS sprites (animation sheets ->
//     cropped to the top frame, DS green key color made transparent).
//
// It downloads + processes the PNGs into static/sprites/platinum/ AND generates
// internal/pogodata/trainers_local.go plus a tag-stub file in the system temp dir
// for refining settings.html.
//
// Run from the repo root:  go run ./cmd/fetchsprites
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	platDir = "static/sprites/platinum"
	goOut   = "internal/pogodata/trainers_local.go"
)

var httpc = &http.Client{Timeout: 30 * time.Second}

type ghTree struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"tree"`
}

func getJSON(url string, v any) error {
	resp, err := httpc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func download(url string) ([]byte, error) {
	resp, err := httpc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s -> %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// cropTopSquareTransparent decodes a PNG, crops the top frame (a square the width
// of the image, since these are vertical animation strips), and for paletted
// sprites makes the top-left pixel's palette entry (the DS key color) transparent.
func cropTopSquareTransparent(raw []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	dim := b.Dx()
	if b.Dy() < dim {
		dim = b.Dy()
	}
	rect := image.Rect(b.Min.X, b.Min.Y, b.Min.X+dim, b.Min.Y+dim)

	var out image.Image
	if pal, ok := img.(*image.Paletted); ok {
		keyIdx := pal.ColorIndexAt(b.Min.X, b.Min.Y)
		newPal := make(color.Palette, len(pal.Palette))
		copy(newPal, pal.Palette)
		newPal[keyIdx] = color.RGBA{0, 0, 0, 0}
		pal.Palette = newPal
		out = pal.SubImage(rect)
	} else if si, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		out = si.SubImage(rect)
	} else {
		out = img
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- label helpers -------------------------------------------------------

func title(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// frontier brain class -> real name
var frontierNames = map[string]string{
	"tower_tycoon": "Palmer",
	"factory_head": "Thorton",
	"arcade_star":  "Dahlia",
	"hall_matron":  "Argenta",
	"castle_valet": "Darach",
}

func platLabel(class string) string {
	if n, ok := frontierNames[class]; ok {
		return n + " (Platinum)"
	}
	if class == "galactic_boss" {
		return "Cyrus (Platinum)"
	}
	for _, pfx := range []string{"champion_", "leader_", "elite_four_", "commander_", "trainer_"} {
		if rest, ok := strings.CutPrefix(class, pfx); ok {
			return title(rest) + " (Platinum)"
		}
	}
	l := title(class)
	l = strings.ReplaceAll(l, " Female", " F")
	l = strings.ReplaceAll(l, " Male", " M")
	return l + " (Platinum)"
}

// ---- main ----------------------------------------------------------------

type entry struct{ slug, label, file string }

func main() {
	if err := os.MkdirAll(platDir, 0o755); err != nil {
		panic(err)
	}

	var platEntries []entry
	var tagStub strings.Builder

	fmt.Println("Enumerating pret/pokeplatinum ...")
	var pt ghTree
	if err := getJSON("https://api.github.com/repos/pret/pokeplatinum/git/trees/main?recursive=1", &pt); err != nil {
		panic(err)
	}
	var platClasses []string
	for _, n := range pt.Tree {
		if strings.HasPrefix(n.Path, "res/trainers/classes/") && strings.HasSuffix(n.Path, "/front.png") {
			c := strings.TrimSuffix(strings.TrimPrefix(n.Path, "res/trainers/classes/"), "/front.png")
			platClasses = append(platClasses, c)
		}
	}
	sort.Strings(platClasses)
	fmt.Printf("  %d platinum classes\n", len(platClasses))
	for _, c := range platClasses {
		raw, err := download("https://raw.githubusercontent.com/pret/pokeplatinum/main/res/trainers/classes/" + c + "/front.png")
		if err != nil {
			fmt.Printf("  SKIP %s: %v\n", c, err)
			continue
		}
		proc, err := cropTopSquareTransparent(raw)
		if err != nil {
			fmt.Printf("  PROC FAIL %s: %v\n", c, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(platDir, c+".png"), proc, 0o644); err != nil {
			panic(err)
		}
		slug := "plat-" + c
		platEntries = append(platEntries, entry{slug, platLabel(c), c + ".png"})
		fmt.Fprintf(&tagStub, "      %q: %q,\n", slug, platTagBaseline(c))
	}

	writeGo(platEntries)

	stubPath := filepath.Join(os.TempDir(), "tagstub.txt")
	_ = os.WriteFile(stubPath, []byte(tagStub.String()), 0o644)
	fmt.Printf("\nDONE. platinum=%d\nGo file: %s\nTag stub: %s\n",
		len(platEntries), goOut, stubPath)
}

// platTagBaseline produces a starting search-tag string; named characters get
// hand-refined in settings.html afterward.
func platTagBaseline(class string) string {
	base := "sinnoh gen 4 diamond pearl platinum"
	switch {
	case strings.HasPrefix(class, "leader_"):
		return "gym leader " + base
	case strings.HasPrefix(class, "elite_four_"):
		return "elite four " + base
	case strings.HasPrefix(class, "champion_"):
		return "champion " + base
	case strings.HasPrefix(class, "commander_"):
		return "team galactic commander villain " + base
	case class == "galactic_boss":
		return "team galactic boss villain cyrus " + base
	case strings.HasPrefix(class, "galactic_grunt"):
		return "team galactic grunt villain " + base
	}
	g := ""
	if strings.Contains(class, "female") {
		g = " female woman girl"
	} else if strings.Contains(class, "male") {
		g = " male man boy"
	}
	return base + g
}

func writeGo(plat []entry) {
	var b strings.Builder
	b.WriteString("package pogodata\n\n")
	b.WriteString("// Code generated by cmd/fetchsprites. DO NOT EDIT BY HAND.\n")
	b.WriteString("// Local trainer-sprite groups with art distinct from the Showdown set:\n")
	b.WriteString("//   platinum: pret/pokeplatinum native Gen 4 DPPt VS sprites (cropped + keyed).\n\n")

	b.WriteString("var platinumTrainerClasses = []TrainerClass{\n")
	for _, e := range plat {
		fmt.Fprintf(&b, "\t{Slug: %q, Label: %q, SpriteURL: %q},\n",
			e.slug, e.label, "/static/sprites/platinum/"+e.file)
	}
	b.WriteString("}\n")

	if err := os.WriteFile(goOut, []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
}
