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

	known := Codes()
	upstream := map[string]bool{}
	for _, f := range files {
		m := driftRe.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		switch {
		case m[3] != "":
			upstream["c:"+m[3]] = true
		case m[2] != "":
			upstream["f:"+m[2]] = true
		}
	}

	var added []string
	for code := range upstream {
		if !known[code] {
			added = append(added, code)
		}
	}
	// Only .c codes are counted as genuinely new: a .f code may just be an ordinary alternate
	// form (Alolan, Mega), which the sync tool filters out against the masterfile. Reporting
	// those here would cry wolf on every new regional form the game ships.
	added = onlyCostumeLike(added)
	sort.Strings(added)

	// Two different states, and reporting only the first is how the panel came to claim in-sync
	// while costumes sat invisible: a code we have never synced needs `make costumes`, but a code
	// we HAVE synced and never named is already in the catalog and still unusable.
	unnamed := len(Unlabelled())

	res.OK = true
	res.Count = len(added)
	res.Bytes = len(files)
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
	}
	if unnamed > 0 {
		notes = append(notes, fmt.Sprintf("%d costume(s) have no label yet and cannot be recorded -- see the Costumes tab", unnamed))
	}
	res.Note = strings.Join(notes, " · ")
	return res
}

func more(total, shown int) string {
	if total > shown {
		return fmt.Sprintf(", +%d more", total-shown)
	}
	return ""
}

// onlyCostumeLike keeps the .c codes, which are unambiguously costumes.
func onlyCostumeLike(codes []string) []string {
	out := codes[:0]
	for _, c := range codes {
		if strings.HasPrefix(c, "c:") {
			out = append(out, c)
		}
	}
	return out
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
