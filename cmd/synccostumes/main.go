// Command synccostumes derives the costume catalog from upstream game assets.
//
// Costume codes, which species may wear a costume, and whether its shiny exists are all
// machine-derivable and must never be typed by a human again. The mined asset filenames
// ARE the data:
//
//	pm{dex}[.f{FORM}][.c{COSTUME}][.g2][.s].icon.png
//
// dex is not zero-padded, .g2 is the female variant, .s is shiny. The app only ever renders
// the shiny costume sprite, so the existence of pm{dex}.{p}{code}.s.icon.png is the
// authoritative test for BOTH "this species can wear this costume" AND "the shiny exists".
//
// Sources:
//   - PokeMiners/pogo_assets, Images/Pokemon - 256x256/Addressable Assets  (existence, truth)
//   - WatWowMap/Masterfile-Generator                                        (pretty names, isCostume)
//
// Human-readable labels are NOT derived: user_shinies.costume is free text that trainers
// type, so a label is user data. This tool only ever READS labels.json and reports on it.
//
// Run from the repo root:
//
//	go run ./cmd/synccostumes            # sync and write catalog.json
//	go run ./cmd/synccostumes -check     # write nothing; non-zero exit on drift
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	repo        = "PokeMiners/pogo_assets"
	assetDir    = "Images/Pokemon - 256x256/Addressable Assets"
	masterfile  = "https://raw.githubusercontent.com/WatWowMap/Masterfile-Generator/master/master-latest-everything.json"
	catalogPath = "internal/costumes/catalog.json"
	labelsPath  = "internal/costumes/labels.json"

	// jsDelivr, pinned to the commit we synced. PokeMiners has already abandoned one asset
	// folder under us (the 128px tree is frozen and 404s on 2026 costumes), so an @master
	// URL would let an upstream reorg break every sprite on the live site with no deploy.
	// Pinned, the URLs are immutable and a reorg instead fails the next sync, on a dev box.
	cdnBase = "https://cdn.jsdelivr.net/gh/" + repo + "@%s/Images/Pokemon%%20-%%20256x256/Addressable%%20Assets/"
)

var httpc = &http.Client{Timeout: 60 * time.Second}

// assetRe parses an addressable asset filename. Note .f and .c can co-occur
// (pm263.fGALARIAN.cGOFEST_2021_NOEVOLVE), and .g2 must be captured explicitly so a
// female-only asset is never mistaken for the default sprite.
var assetRe = regexp.MustCompile(`^pm(\d+)(?:\.f([A-Za-z0-9_]+))?(?:\.c([A-Za-z0-9_]+))?(\.g2)?(\.s)?\.icon\.png$`)

type catalog struct {
	AssetBase    string                   `json:"assetBase"`
	SourceCommit string                   `json:"sourceCommit"`
	Codes        map[string]*catalogEntry `json:"codes"`
}

type catalogEntry struct {
	Pretty string `json:"pretty"`
	// Suggested is what Dittobase calls this costume, carried into the catalog so the admin
	// review tab can show a name a trainer would recognise ("Visor") next to the machine one
	// ("Spring 2020 Noevolve"). names.json is not embedded; this field is how it reaches the app.
	Suggested string `json:"suggested,omitempty"`
	Dex       []int  `json:"dex"`
	Female    []int  `json:"female,omitempty"`
	// Unconfirmed marks a .f code the masterfile does not flag isCostume, admitted only
	// because a curated label vouches for it (see buildCatalog).
	Unconfirmed bool `json:"unconfirmed,omitempty"`
}

type labels struct {
	Species map[string]map[string]string `json:"species"`
	Shared  []struct {
		Label string `json:"label"`
		Code  string `json:"code"`
	} `json:"shared"`
	Aliases map[string]string `json:"aliases"`
	Hidden  []string          `json:"hidden"`
	// NameCheckIgnore silences the Dittobase name check for codes where we knowingly use a
	// different word than they do (we call it "Dawn's Hat"; they call it "Rei's Cap").
	NameCheckIgnore []string `json:"nameCheckIgnore"`
}

// codes is every code a curated label points at.
func (l *labels) codes() map[string]bool {
	out := map[string]bool{}
	for _, byLabel := range l.Species {
		for _, code := range byLabel {
			out[code] = true
		}
	}
	for _, s := range l.Shared {
		out[s.Code] = true
	}
	return out
}

func main() {
	check := flag.Bool("check", false, "report only; write nothing and exit non-zero on drift")
	offline := flag.Bool("offline", false, "do not fetch costume names from Dittobase; use the cached ones")
	token := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token (optional; raises the 60/hr anon rate limit)")
	flag.Parse()

	sha, err := headSHA(*token)
	must(err, "resolve commit sha")
	fmt.Printf("PokeMiners/pogo_assets @ %s\n", sha[:12])

	files, err := assetFiles(sha, *token)
	must(err, "list asset tree")
	fmt.Printf("%d files in %q\n", len(files), assetDir)

	mf, err := loadMasterfile()
	must(err, "load masterfile")
	fmt.Printf("masterfile: %d costume enums, %d species\n", len(mf.Costumes), len(mf.Pokemon))

	lab, err := loadLabels()
	must(err, "read "+labelsPath)

	cat, nameToDex := buildCatalog(sha, files, mf, lab.codes())
	fmt.Printf("%d costume codes with shiny art\n", len(cat.Codes))

	// What a costume is actually CALLED. Suggestion only: our curated labels always win.
	dexToName := make(map[int]string, len(nameToDex))
	for name, dex := range nameToDex {
		dexToName[dex] = name
	}
	nm := refreshNames(cat, dexToName, *offline)
	for code, e := range cat.Codes {
		e.Suggested = suggestedLabel(e, nm, code)
	}
	fmt.Println()

	errs, warns, review := audit(cat, lab, nameToDex, nm)
	report("ERROR", errs)
	report("WARN", warns)
	report("NAME CHECK", nameCheck(cat, lab, nameToDex, nm))
	report("REVIEW", review)
	suggestLabels(cat, lab, nm, dexToName)

	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d error(s): refusing to write %s\n", len(errs), catalogPath)
		os.Exit(1)
	}

	// Drift means the committed catalog would CHANGE, not that unlabelled costumes exist:
	// leaving a costume unnamed is a normal steady state (we simply do not offer it), so
	// failing on REVIEW would make -check red forever and train everyone to ignore it.
	changed, err := catalogChanged(cat)
	must(err, "compare "+catalogPath)

	if *check {
		if changed {
			fmt.Fprintf(os.Stderr, "\n%s is out of date: run `make costumes`\n", catalogPath)
			os.Exit(1)
		}
		fmt.Println("catalog is up to date")
		return
	}

	if !changed {
		fmt.Printf("%s already up to date\n", catalogPath)
		return
	}
	must(writeCatalog(cat), "write "+catalogPath)
	fmt.Printf("\nwrote %s\n", catalogPath)
}

// catalogChanged reports whether the derived catalog differs from what is committed.
func catalogChanged(cat *catalog) (bool, error) {
	next, err := marshalCatalog(cat)
	if err != nil {
		return false, err
	}
	cur, err := os.ReadFile(catalogPath)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !bytes.Equal(cur, next), nil
}

// headSHA returns the COMMIT sha (not a tree sha) that the CDN URL is pinned to.
func headSHA(token string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := getJSON("https://api.github.com/repos/"+repo+"/commits/master", token, &out)
	return out.SHA, err
}

type ghTree struct {
	SHA       string `json:"sha"`
	Truncated bool   `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"tree"`
}

// assetFiles walks down to the asset directory one level at a time. It deliberately does
// NOT call git/trees/{sha}?recursive=1 on the repo root: that returns HTTP 500 on this repo,
// and the API silently truncates very large trees, which would look like costumes vanishing.
func assetFiles(sha, token string) ([]string, error) {
	cur := sha
	for seg := range strings.SplitSeq(assetDir, "/") {
		var t ghTree
		if err := getJSON("https://api.github.com/repos/"+repo+"/git/trees/"+cur, token, &t); err != nil {
			return nil, err
		}
		next := ""
		for _, e := range t.Tree {
			if e.Path == seg && e.Type == "tree" {
				next = e.SHA
				break
			}
		}
		if next == "" {
			return nil, fmt.Errorf("upstream layout changed: %q not found (did PokeMiners move the folder again?)", seg)
		}
		cur = next
	}

	var t ghTree
	if err := getJSON("https://api.github.com/repos/"+repo+"/git/trees/"+cur, token, &t); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, fmt.Errorf("asset tree truncated by the API: refusing to sync a partial catalog")
	}
	out := make([]string, 0, len(t.Tree))
	for _, e := range t.Tree {
		if e.Type == "blob" {
			out = append(out, e.Path)
		}
	}
	return out, nil
}

type masterData struct {
	Costumes map[string]struct {
		Name  string `json:"name"`
		Proto string `json:"proto"`
	} `json:"costumes"`
	Pokemon map[string]struct {
		Name  string `json:"name"`
		Forms map[string]struct {
			Name      string `json:"name"`
			Proto     string `json:"proto"`
			IsCostume bool   `json:"isCostume"`
		} `json:"forms"`
	} `json:"pokemon"`
}

func loadMasterfile() (*masterData, error) {
	var mf masterData
	if err := getJSON(masterfile, "", &mf); err != nil {
		return nil, err
	}
	if len(mf.Costumes) == 0 || len(mf.Pokemon) == 0 {
		return nil, fmt.Errorf("masterfile is missing costumes or pokemon")
	}
	return &mf, nil
}

// isCostumeForm reports whether a .f code on this dex is a COSTUME rather than an ordinary
// alternate form, and returns its display name.
//
// This filter is mandatory. The .f prefix is shared with regional, mega, battle and cosmetic
// forms, so without it the catalog would happily ingest pm26.fALOLA (Alolan Raichu) and
// pm888.fCROWNED_SWORD as costumes.
//
// Rather than normalise species names to strip the proto prefix (Mr. Mime, Farfetch'd and
// Nidoran-F all break naive casing rules), match the form proto against the code the asset
// tree produced for that same dex: PIKACHU_VISOR_2026 ends with _VISOR_2026.
func (mf *masterData) isCostumeForm(dex int, code string) (string, bool) {
	pk, ok := mf.Pokemon[strconv.Itoa(dex)]
	if !ok {
		return "", false
	}
	for _, form := range pk.Forms {
		if form.Proto != code && !strings.HasSuffix(form.Proto, "_"+code) {
			continue
		}
		if !form.IsCostume {
			return "", false
		}
		return form.Name, true
	}
	return "", false
}

func (mf *masterData) nameToDex() map[string]int {
	out := make(map[string]int, len(mf.Pokemon))
	for dexStr, pk := range mf.Pokemon {
		if dex, err := strconv.Atoi(dexStr); err == nil && pk.Name != "" {
			out[pk.Name] = dex
		}
	}
	return out
}

// buildCatalog keeps a (p, code) only if at least one SHINY asset exists for it, and records
// exactly the dex numbers whose shiny asset is present. That existence test is what makes the
// catalog authoritative for both eligibility and shiny availability.
//
// The .c prefix is unambiguous (it only ever means a costume), so a .c code is trusted from the
// tree alone, which keeps a brand-new costume drop working even when the masterfile lags.
//
// The .f prefix is NOT: it is shared with regional, mega and battle forms, so a .f code is
// admitted only if the masterfile flags the form isCostume, or a curated label already vouches
// for it. That second clause is not a loophole, it is load-bearing: isCostume has false
// negatives (PIKACHU_COPY_2019, Clone Pikachu, carries no flag at all despite its shiny asset
// being live), and trusting the flag alone would silently drop a costume users have already
// recorded. Curated-but-unconfirmed codes are reported so a human can eyeball them.
func buildCatalog(sha string, files []string, mf *masterData, curated map[string]bool) (*catalog, map[string]int) {
	cat := &catalog{
		AssetBase:    fmt.Sprintf(cdnBase, sha),
		SourceCommit: sha,
		Codes:        map[string]*catalogEntry{},
	}
	dexOf := map[string]map[int]bool{}
	femOf := map[string]map[int]bool{}
	pretty := map[string]string{}
	unconfirmed := map[string]bool{}

	cName := make(map[string]string, len(mf.Costumes))
	for _, c := range mf.Costumes {
		cName[c.Proto] = c.Name
	}

	for _, f := range files {
		m := assetRe.FindStringSubmatch(f)
		if m == nil || m[5] != ".s" { // shiny only: that is what the app renders
			continue
		}
		dex, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}

		// A form and a costume can co-occur (pm263.fGALARIAN.cGOFEST_2021_NOEVOLVE); when both
		// are present the costume is what makes it a costume.
		var key string
		switch {
		case m[3] != "":
			key = "c:" + m[3]
			pretty[key] = cName[m[3]]
		case m[2] != "":
			key = "f:" + m[2]
			name, confirmed := mf.isCostumeForm(dex, m[2])
			if !confirmed && !curated[key] {
				continue // an ordinary alternate form (Alolan, Mega, Crowned, ...), not a costume
			}
			if !confirmed {
				unconfirmed[key] = true
			}
			if name != "" {
				pretty[key] = name
			}
		default:
			continue // a plain species shiny
		}

		if m[4] == ".g2" {
			if femOf[key] == nil {
				femOf[key] = map[int]bool{}
			}
			femOf[key][dex] = true
			continue
		}
		if dexOf[key] == nil {
			dexOf[key] = map[int]bool{}
		}
		dexOf[key][dex] = true
	}

	for key, set := range dexOf {
		cat.Codes[key] = &catalogEntry{
			Pretty:      pretty[key],
			Dex:         sortedKeys(set),
			Female:      sortedKeys(femOf[key]),
			Unconfirmed: unconfirmed[key],
		}
	}
	return cat, mf.nameToDex()
}

type finding struct{ what, detail string }

// audit compares the curated labels against the derived catalog. It never modifies labels.json.
func audit(cat *catalog, lab *labels, nameToDex map[string]int, nm names) (errs, warns, review []finding) {
	labelled := map[string]bool{}

	for species, byLabel := range lab.Species {
		dex, known := nameToDex[species]
		for label, code := range byLabel {
			labelled[code] = true
			where := fmt.Sprintf("%s / %q -> %s", species, label, code)

			e, ok := cat.Codes[code]
			if !ok {
				errs = append(errs, finding{where, "code has no shiny art upstream"})
				continue
			}
			if !known {
				warns = append(warns, finding{where, "species name not found in the masterfile; cannot verify eligibility"})
				continue
			}
			if !inDex(e.Dex, dex) {
				errs = append(errs, finding{where, fmt.Sprintf("no shiny art for dex %d; the code covers %s", dex, compactDex(e.Dex))})
			}
		}
	}

	for _, s := range lab.Shared {
		labelled[s.Code] = true
		e, ok := cat.Codes[s.Code]
		if !ok {
			errs = append(errs, finding{
				fmt.Sprintf("shared %q -> %s", s.Label, s.Code),
				"code has no shiny art upstream",
			})
			continue
		}
		// A species override under the same label shadows the shared code: the override always
		// wins, so this species can never be shown the shared art even though it is eligible.
		for species, byLabel := range lab.Species {
			oc, has := byLabel[s.Label]
			if !has || oc == s.Code {
				continue
			}
			if dex, known := nameToDex[species]; known && inDex(e.Dex, dex) {
				warns = append(warns, finding{
					fmt.Sprintf("%s / %q", species, s.Label),
					fmt.Sprintf("override %s shadows shared %s, which this species is also eligible for", oc, s.Code),
				})
			}
		}
	}

	hidden := map[string]bool{}
	for _, h := range lab.Hidden {
		hidden[h] = true
	}
	for code, e := range cat.Codes {
		if e.Unconfirmed {
			warns = append(warns, finding{
				code,
				fmt.Sprintf("upstream does not flag this form a costume; kept because a label vouches for it (dex=%s)", compactDex(e.Dex)),
			})
		}
		if labelled[code] || hidden[code] {
			continue
		}
		// Lead with the Dittobase name: it is the one a trainer would recognise. "Visor", not
		// the masterfile's "Spring 2020 Noevolve".
		detail := fmt.Sprintf("masterfile: %q", e.Pretty)
		if suggested := suggestedLabel(e, nm, code); suggested != "" {
			detail = fmt.Sprintf("dittobase: %-28q masterfile: %q", suggested, e.Pretty)
		}
		review = append(review, finding{
			fmt.Sprintf("%-34s dex=%s", code, compactDex(e.Dex)),
			detail,
		})
	}

	sortFindings(errs)
	sortFindings(warns)
	sortFindings(review)
	return
}

func inDex(dex []int, d int) bool {
	return slices.Contains(dex, d)
}

func report(kind string, fs []finding) {
	if len(fs) == 0 {
		return
	}
	fmt.Printf("%s (%d)\n", kind, len(fs))
	for _, f := range fs {
		fmt.Printf("  %-46s %s\n", f.what, f.detail)
	}
	fmt.Println()
}

func loadLabels() (*labels, error) {
	b, err := os.ReadFile(labelsPath)
	if err != nil {
		return nil, err
	}
	var l labels
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// marshalCatalog emits deterministic JSON: Go sorts map keys, and the dex lists are sorted at
// build time, so the same upstream always produces byte-identical output.
func marshalCatalog(cat *catalog) ([]byte, error) {
	b, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeCatalog writes UTF-8 with LF endings and no BOM. Never let PowerShell's Out-File near
// this: a BOM in a go:embed'ed JSON file crashes the service on boot.
func writeCatalog(cat *catalog) error {
	b, err := marshalCatalog(cat)
	if err != nil {
		return err
	}
	return os.WriteFile(catalogPath, b, 0o644)
}

func sortedKeys(m map[int]bool) []int {
	if len(m) == 0 {
		return nil
	}
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func compactDex(d []int) string {
	if len(d) <= 6 {
		return fmt.Sprint(d)
	}
	return fmt.Sprintf("[%d..%d] (%d species)", d[0], d[len(d)-1], len(d))
}

func sortFindings(fs []finding) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].what < fs[j].what })
}

func getJSON(url, token string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
