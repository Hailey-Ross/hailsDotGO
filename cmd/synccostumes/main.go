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

	"pogo.hails.cc/internal/masterfile"
)

const (
	repo        = "PokeMiners/pogo_assets"
	assetDir    = "Images/Pokemon - 256x256/Addressable Assets"
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

// eventYearRe finds a four-digit year token in a costume code. It is the one cheap signal that
// separates the two kinds of .f code the masterfile leaves unflagged: a permanent battle or
// regional form (MEGA, ALOLA, GALARIAN, CROWNED_SWORD, UNOWN_A, the Vivillon patterns, the
// Spinda spot sets) never carries a year, while an event costume nearly always does. Used only
// to decide what is worth a human's attention, never to admit anything.
var eventYearRe = regexp.MustCompile(`(^|_)(19|20)\d{2}($|_)`)

// looksLikeEventCostume reports whether an unflagged .f code is probably a costume we are
// missing, and so worth printing. See unflaggedFindings for why this exists at all.
func looksLikeEventCostume(code string) bool {
	return strings.HasPrefix(code, "f:") && eventYearRe.MatchString(strings.TrimPrefix(code, "f:"))
}

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
	repin := flag.Bool("repin", false, "move the asset pin to upstream HEAD even when no catalog data changed")
	token := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token (optional; raises the 60/hr anon rate limit)")
	flag.Parse()

	sha, err := headSHA(*token)
	must(err, "resolve commit sha")
	fmt.Printf("PokeMiners/pogo_assets @ %s\n", sha[:12])

	files, err := assetFiles(sha, *token)
	must(err, "list asset tree")
	fmt.Printf("%d files in %q\n", len(files), assetDir)

	mf, err := masterfile.Load(httpc)
	must(err, "load masterfile")
	fmt.Printf("masterfile: %d costume enums, %d species\n", len(mf.Costumes), len(mf.Pokemon))

	lab, err := loadLabels()
	must(err, "read "+labelsPath)

	cat, nameToDex, unflagged := buildCatalog(sha, files, mf, lab.codes())
	fmt.Printf("%d costume codes with shiny art\n", len(cat.Codes))

	// What a costume is actually CALLED. Suggestion only: our curated labels always win.
	dexToName := make(map[int]string, len(nameToDex))
	for name, dex := range nameToDex {
		dexToName[dex] = name
	}
	nm, unnamedWhy := refreshNames(cat, dexToName, *offline)
	for code, e := range cat.Codes {
		e.Suggested = suggestedLabel(e, nm, code)
	}
	fmt.Println()

	errs, warns, review := audit(cat, lab, nameToDex, nm, noNameReasons(cat, nm, unnamedWhy))
	report("ERROR", errs)
	report("WARN", warns)
	report("NAME CHECK", nameCheck(cat, lab, nameToDex, nm))
	report("REVIEW", review)
	report("UNFLAGGED", unflaggedFindings(unflagged))
	suggestLabels(cat, lab, nm, dexToName)

	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d error(s): refusing to write %s\n", len(errs), catalogPath)
		os.Exit(1)
	}

	// Drift means the committed catalog would CHANGE, not that unlabelled costumes exist:
	// leaving a costume unnamed is a normal steady state (we simply do not offer it), so
	// failing on REVIEW would make -check red forever and train everyone to ignore it.
	d, err := catalogDiff(catalogPath, cat)
	must(err, "compare "+catalogPath)

	if d.pin {
		fmt.Printf("asset pin: catalog is at %s, upstream is at %s\n", short(d.committedSHA), short(sha))
	}

	if *check {
		if d.data {
			fmt.Fprintf(os.Stderr, "\n%s is out of date: run `make costumes`\n", catalogPath)
			os.Exit(1)
		}
		if d.pin {
			fmt.Println("catalog data is up to date; the asset pin is older than upstream HEAD, which is fine (pinned URLs are immutable)")
			return
		}
		fmt.Println("catalog is up to date")
		return
	}

	if !d.data && !*repin {
		if d.pin {
			fmt.Printf("%s data is unchanged; leaving the pin at %s (pass -repin to move it)\n", catalogPath, short(d.committedSHA))
		} else {
			fmt.Printf("%s already up to date\n", catalogPath)
		}
		return
	}
	must(writeCatalog(cat), "write "+catalogPath)
	fmt.Printf("\nwrote %s\n", catalogPath)
}

// diff separates the two things that can make the committed catalog differ from a fresh
// derivation, because only one of them is news.
type diff struct {
	data         bool   // the costume answer moved: codes, species, names or flags
	pin          bool   // only the asset pin moved: assetBase and sourceCommit
	committedSHA string // what the committed catalog is pinned to
}

// catalogDiff compares the derived catalog against what is committed, separating a DATA change
// from PIN churn.
//
// Data is the answer: which codes exist, which species wear them, the names, the flags. If that
// moved, the embedded catalog is genuinely stale and someone has to rebuild and deploy.
//
// The pin is bookkeeping. assetBase and sourceCommit follow PokeMiners HEAD, and that repo commits
// several times a day, so byte comparing the whole file made -check red permanently while the data
// underneath had not moved for weeks. That is the exact alarm fatigue the comment above the -check
// branch was written to prevent, arriving through the back door.
//
// An older pin is not a problem to fix. The pin exists so the sprite URLs are immutable (an
// upstream reorg must fail on a dev box, never on the live site), and every asset the catalog
// names existed at the pinned commit, so jsDelivr will serve it forever. Re-pinning for its own
// sake costs a rebuild of a go:embed'ed file and a manual deploy for no user visible effect, and
// leaves `make costumes` dirtying the tree on every run. When the data DOES change the fresh pin
// is adopted with it, which is required: the new asset only exists at the newer commit.
func catalogDiff(path string, next *catalog) (diff, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return diff{data: true, pin: true}, nil
	}
	if err != nil {
		return diff{}, err
	}
	var cur catalog
	if err := json.Unmarshal(b, &cur); err != nil {
		// A file we cannot parse is a file we must rewrite, not a reason to refuse. The byte
		// comparison this replaced recovered from a corrupt or BOM-prefixed catalog by definition,
		// and given the BOM has crashed this service before, `make costumes` has to stay the repair
		// rather than becoming another thing that needs one.
		fmt.Fprintf(os.Stderr, "%s is unreadable (%v); treating it as out of date\n", path, err)
		return diff{data: true, pin: true}, nil
	}

	curCodes, err := json.Marshal(cur.Codes)
	if err != nil {
		return diff{}, err
	}
	nextCodes, err := json.Marshal(next.Codes)
	if err != nil {
		return diff{}, err
	}
	d := diff{
		data:         !bytes.Equal(curCodes, nextCodes),
		pin:          cur.SourceCommit != next.SourceCommit,
		committedSHA: cur.SourceCommit,
	}
	// An assetBase that is not what the committed pin implies means the URL SHAPE changed, not the
	// pin: someone edited cdnBase because upstream moved the folder again, which is the one thing
	// the pinning exists to survive. Every sprite URL on the site is wrong until it is written, so
	// it counts as data and must not sit behind -repin.
	if cur.AssetBase != fmt.Sprintf(cdnBase, cur.SourceCommit) {
		d.data = true
	}
	return d, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
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
func buildCatalog(sha string, files []string, mf *masterfile.Data, curated map[string]bool) (*catalog, map[string]int, map[string]map[int]bool) {
	cat := &catalog{
		AssetBase:    fmt.Sprintf(cdnBase, sha),
		SourceCommit: sha,
		Codes:        map[string]*catalogEntry{},
	}
	dexOf := map[string]map[int]bool{}
	femOf := map[string]map[int]bool{}
	pretty := map[string]string{}
	unconfirmed := map[string]bool{}
	unflagged := map[string]map[int]bool{}

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
			name, confirmed := mf.IsCostumeForm(dex, m[2])
			if !confirmed && !curated[key] {
				// An ordinary alternate form (Alolan, Mega, Crowned, ...), not a costume.
				// Unless it carries a year, in which case say so rather than dropping it in
				// silence: that silence is how f:COPY_2019 stayed missing for months.
				if m[4] != ".g2" && looksLikeEventCostume(key) {
					if unflagged[key] == nil {
						unflagged[key] = map[int]bool{}
					}
					unflagged[key][dex] = true
				}
				continue
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
	return cat, mf.NameToDex(), unflagged
}

type finding struct{ what, detail string }

// audit compares the curated labels against the derived catalog. It never modifies labels.json.
//
// noName explains, per code, why a REVIEW entry has no suggested name. Without it a blank
// suggestion is unreadable: a source that has no page for the costume looks exactly like a run
// that never asked, and the admin naming tab shows the same nothing either way.
func audit(cat *catalog, lab *labels, nameToDex map[string]int, nm names, noName map[string]string) (errs, warns, review []finding) {
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
		} else if why := noName[code]; why != "" {
			detail = fmt.Sprintf("masterfile: %-28q %s", e.Pretty, why)
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

// unflaggedFindings lists .f codes that have shiny art and a year in their code, but that the
// masterfile does not flag isCostume and no curated label vouches for.
//
// That combination is the one blind spot in this pipeline. buildCatalog drops such a code, and
// the admin drift check applies the same rule, so it never names it either: the costume is
// simply absent, and trainers who caught it see a plain shiny with no art and nothing anywhere
// says why. f:COPY_2019 (Clone Pikachu) sat in that hole for months, and the 2026 regional
// anniversary Pikachu and the Psyduck swim ring were found the same way.
//
// Reported only, never admitted. A code still enters the catalog exactly one way, by a human
// looking at the sprite and writing a label for it, so an unreviewed sync stays safe.
func unflaggedFindings(unflagged map[string]map[int]bool) []finding {
	out := make([]finding, 0, len(unflagged))
	for code, set := range unflagged {
		out = append(out, finding{code, fmt.Sprintf(
			"shiny art for %s, but upstream does not flag it a costume and no label vouches for it; look at the sprite, then add a label to %s if it is one",
			compactDex(sortedKeys(set)), labelsPath)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].what < out[j].what })
	return out
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
