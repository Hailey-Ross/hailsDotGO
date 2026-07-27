package costumes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pogo.hails.cc/internal/masterfile"
	"pogo.hails.cc/internal/pogodata"
)

// driftRe mirrors the sync tool's filename grammar, but only needs the codes.
var driftRe = regexp.MustCompile(`^pm(\d+)(?:\.f([A-Za-z0-9_]+))?(?:\.c([A-Za-z0-9_]+))?(?:\.g2)?\.s\.icon\.png$`)

// DriftCheck reports whether upstream has costume art we do not know about yet.
//
// It is READ-ONLY on purpose, unlike every other row in the admin scraper panel. A new costume
// code is useless until a human gives it a label trainers would recognise ("Witch Hat", not
// "Halloween 2025 Noevolve"), and a production box must never rewrite the user-facing label
// set on its own. So this only ever says "go run `make costumes`".
func DriftCheck() pogodata.ScraperCheck {
	start := time.Now()
	res := pogodata.ScraperCheck{Key: "costumes"}

	files, err := listAssets()
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	// The .f prefix is shared with regional, mega and battle forms, so a new .f code counts as a
	// costume only if the masterfile flags it, or a curated label already vouches for it. That is
	// the rule buildCatalog applies, and this check has to apply the SAME one: when it had its own
	// cheaper rule (keep .c, discard every .f) it could not see a single costume the game had
	// shipped since 2024, because they are all .f, and it answered "nothing new" every time.
	mf, mfErr := masterfile.Load(&http.Client{Timeout: 30 * time.Second})

	var forms costumeForms // a typed nil in an interface is not nil, so assign only when loaded
	if mf != nil {
		forms = mf
	}
	added, unverified := newCodes(files, Codes(), labelledCodes(), forms)

	// Two different states, and reporting only the first is how the panel came to claim in-sync
	// while costumes sat invisible: a code we have never synced needs `make costumes`, but a code
	// we HAVE synced and never named is already in the catalog and still unusable.
	unnamed := len(Unlabelled())

	res.OK = true
	res.Count = len(added)
	res.Changed = len(added) > 0 || unnamed > 0
	res.DurationMs = time.Since(start).Milliseconds()

	var notes []string
	if len(added) > 0 {
		show := added
		if len(show) > 5 {
			show = show[:5]
		}
		notes = append(notes, fmt.Sprintf("%d new code(s) upstream: %s%s -- run `make costumes`",
			len(added), strings.Join(show, ", "), more(len(added), len(show))))
	} else {
		// Say the zero case out loud. Silence here is what made a working check look like a dead
		// button: with nothing new and nothing unnamed the panel used to render an empty string.
		notes = append(notes, fmt.Sprintf("no new codes upstream (%d shiny assets scanned)", len(files)))
	}
	if unnamed > 0 {
		notes = append(notes, fmt.Sprintf("%d costume(s) have no label yet and cannot be recorded -- see the Costumes tab", unnamed))
	}
	// A dead third party must not turn this red: the .c half of the answer is still trustworthy,
	// so report it and say plainly which half could not be checked.
	if mfErr != nil {
		notes = append(notes, fmt.Sprintf("%d full-form code(s) unverified: masterfile unreachable (%v)", unverified, mfErr))
	}
	res.Note = strings.Join(notes, " · ")
	return res
}

// costumeForms is the masterfile's answer to "is this .f code a costume, or just another form".
// An interface so the rule below can be exercised without the network.
type costumeForms interface {
	IsCostumeForm(dex int, code string) (string, bool)
}

// newCodes reports which upstream codes we have not synced, and how many .f codes could not be
// judged because the masterfile was unreachable (forms is nil).
//
// known is the embedded catalog; curated is every code a label points at. A curated label is
// enough on its own, and that clause is load bearing rather than a loophole: isCostume has false
// negatives (PIKACHU_COPY_2019, Clone Pikachu, carries no flag at all), so trusting the flag alone
// would quietly drop a costume trainers have already recorded.
func newCodes(files []string, known, curated map[string]bool, forms costumeForms) ([]string, int) {
	// Keyed by code, valued by the dex numbers upstream has shiny art for. The dex is not
	// decoration: asking the masterfile whether a .f code is a costume needs one.
	upstream := map[string][]int{}
	for _, f := range files {
		m := driftRe.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		dex, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		switch {
		case m[3] != "":
			upstream["c:"+m[3]] = append(upstream["c:"+m[3]], dex)
		case m[2] != "":
			upstream["f:"+m[2]] = append(upstream["f:"+m[2]], dex)
		}
	}

	var added []string
	unverified := 0
	for code, dexes := range upstream {
		if known[code] {
			continue
		}
		// A .c code only ever means a costume, so it is trusted from the asset tree alone. That is
		// what keeps a brand-new costume drop visible here even when the masterfile lags.
		if strings.HasPrefix(code, "c:") || curated[code] {
			added = append(added, code)
			continue
		}
		if forms == nil {
			unverified++
			continue
		}
		form := strings.TrimPrefix(code, "f:")
		for _, dex := range dexes {
			if _, ok := forms.IsCostumeForm(dex, form); ok {
				added = append(added, code)
				break
			}
		}
	}
	sort.Strings(added)
	return added, unverified
}

func more(total, shown int) string {
	if total > shown {
		return fmt.Sprintf(", +%d more", total-shown)
	}
	return ""
}

// listAssets walks the GitHub tree down to the asset folder. Two API calls per level, no
// tickers, superadmin-triggered only, so the 60/hr anonymous limit is not a concern.
func listAssets() ([]string, error) {
	const (
		repo = "PokeMiners/pogo_assets"
		dir  = "Images/Pokemon - 256x256/Addressable Assets"
	)
	client := &http.Client{Timeout: 30 * time.Second}

	get := func(url string, v any) error {
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s -> %s", url, strconv.Itoa(resp.StatusCode))
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}

	var head struct {
		SHA string `json:"sha"`
	}
	if err := get("https://api.github.com/repos/"+repo+"/commits/master", &head); err != nil {
		return nil, err
	}

	type tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}

	cur := head.SHA
	for _, seg := range strings.Split(dir, "/") {
		var t tree
		if err := get("https://api.github.com/repos/"+repo+"/git/trees/"+cur, &t); err != nil {
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
			return nil, fmt.Errorf("upstream layout changed: %q not found", seg)
		}
		cur = next
	}

	var t tree
	if err := get("https://api.github.com/repos/"+repo+"/git/trees/"+cur, &t); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, fmt.Errorf("asset tree truncated by the API")
	}
	out := make([]string, 0, len(t.Tree))
	for _, e := range t.Tree {
		if e.Type == "blob" {
			out = append(out, e.Path)
		}
	}
	return out, nil
}
