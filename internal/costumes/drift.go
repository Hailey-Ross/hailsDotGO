package costumes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pogo.hails.cc/internal/masterfile"
	"pogo.hails.cc/internal/pogodata"
)

// driftRe mirrors assetRe in cmd/synccostumes/main.go, narrowed to shiny only (.s is literal here)
// because that is all this check cares about.
//
// The .g2 group is CAPTURED rather than discarded, and that is load bearing. buildCatalog files a
// .g2 hit under "female" and continues, building the catalog codes from the default art alone, so
// a code upstream has only female shiny art for never enters the catalog at all. Treating one as
// proof the code exists would report drift that `make costumes` can never clear, forever.
var driftRe = regexp.MustCompile(`^pm(\d+)(?:\.f([A-Za-z0-9_]+))?(?:\.c([A-Za-z0-9_]+))?(\.g2)?\.s\.icon\.png$`)

// DriftCheck reports whether upstream has costume art we do not know about yet.
//
// It is READ-ONLY on purpose, unlike every other row in the admin scraper panel. A new costume
// code is useless until a human gives it a label trainers would recognise ("Witch Hat", not
// "Halloween 2025 Noevolve"), and a production box must never rewrite the user-facing label
// set on its own. So this only ever says "go run `make costumes`".
//
// The upstream listing behind it is cached (see assets), so pressing the button twice, or pressing
// it and then running Check Scrapers, costs one trip upstream rather than two.
func DriftCheck() pogodata.ScraperCheck { return driftCheck(false) }

// DriftCheckFresh is DriftCheck with the cache bypassed and replaced.
//
// It exists for the workflow the cache would otherwise get in the way of: an event drops, an admin
// presses the button, is told nothing is new, and now has a five minute wall between them and the
// truth. That feeling is exactly the "dead button" this check keeps regressing into.
func DriftCheckFresh() pogodata.ScraperCheck { return driftCheck(true) }

func driftCheck(force bool) pogodata.ScraperCheck {
	start := time.Now()
	res := pogodata.ScraperCheck{Key: "costumes"}

	files, age, err := assets(force)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	// The masterfile is deliberately NOT cached alongside the listing: it is a plain raw.github
	// file rather than an API call, so it costs no quota, and re-reading it means an upstream fix
	// to an isCostume flag shows up on the next press instead of five minutes later.
	//
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
	rep := newCodes(files, CatalogDex(), labelledCodes(), forms)

	// Two different states, and reporting only the first is how the panel came to claim in-sync
	// while costumes sat invisible: a code we have never synced needs `make costumes`, but a code
	// we HAVE synced and never named is already in the catalog and still unusable.
	unnamed := len(Unlabelled())

	res.OK = true
	res.Count = len(rep.Added) + len(rep.Grown)
	res.Changed = res.Count > 0 || unnamed > 0 || len(rep.Unflagged) > 0
	res.DurationMs = time.Since(start).Milliseconds()

	var notes []string
	if len(rep.Added) > 0 {
		show := rep.Added
		if len(show) > 5 {
			show = show[:5]
		}
		notes = append(notes, fmt.Sprintf("%d new code(s) upstream: %s%s, run `make costumes`",
			len(rep.Added), strings.Join(show, ", "), more(len(rep.Added), len(show))))
	}
	if len(rep.Grown) > 0 {
		show := rep.Grown
		if len(show) > 5 {
			show = show[:5]
		}
		parts := make([]string, len(show))
		for i, g := range show {
			parts[i] = fmt.Sprintf("%s (+dex %s)", g.Code, joinInts(g.Dex))
		}
		notes = append(notes, fmt.Sprintf("%d code(s) gained species upstream: %s%s, run `make costumes`",
			len(rep.Grown), strings.Join(parts, ", "), more(len(rep.Grown), len(show))))
	}
	if len(rep.Added) == 0 && len(rep.Grown) == 0 {
		// Say the zero case out loud. Silence here is what made a working check look like a dead
		// button: with nothing new and nothing unnamed the panel used to render an empty string.
		// Count what was actually examined, too: len(files) is every blob in the folder, which is
		// what this line used to print while claiming it had counted shiny assets.
		notes = append(notes, fmt.Sprintf("no new codes and no new species upstream (%d costume shiny asset(s) among %d files)",
			rep.Scanned, len(files)))
	}
	if unnamed > 0 {
		notes = append(notes, fmt.Sprintf("%d costume(s) have no label yet and cannot be recorded, see the Costumes tab", unnamed))
	}
	// Not folded into Added: `make costumes` alone will NOT pick these up, so telling an admin to
	// run it would be a lie. They need a human to look at the sprite and write a label first.
	if len(rep.Unflagged) > 0 {
		show := rep.Unflagged
		if len(show) > 5 {
			show = show[:5]
		}
		notes = append(notes, fmt.Sprintf("%d code(s) upstream will not call costumes: %s%s, check the sprite and add a label if it is one",
			len(rep.Unflagged), strings.Join(show, ", "), more(len(rep.Unflagged), len(show))))
	}
	// A dead third party must not turn this red: the .c half of the answer is still trustworthy,
	// so report it and say plainly which half could not be checked.
	if mfErr != nil {
		notes = append(notes, fmt.Sprintf("%d full-form code(s) unverified: masterfile unreachable (%v)", rep.Unverified, mfErr))
	}
	if age > 0 {
		// Say it was cached, and how stale, but not "0s old": two presses in the same second is
		// exactly when an admin most needs to be told the answer is a repeat rather than a re-look.
		when := "just now"
		if age >= time.Second {
			when = age.Round(time.Second).String() + " ago"
		}
		notes = append(notes, fmt.Sprintf("upstream listing read %s, cached (press again to re-check now)", when))
	}
	res.Note = strings.Join(notes, " · ")
	return res
}

// costumeForms is the masterfile's answer to "is this .f code a costume, or just another form".
// An interface so the rule below can be exercised without the network.
type costumeForms interface {
	IsCostumeForm(dex int, code string) (string, bool)
}

// grown is a code we already have that upstream now has more species for.
type grown struct {
	Code string
	Dex  []int // the dex numbers upstream has and the embedded catalog does not
}

// driftReport is one scan of the upstream asset tree.
type driftReport struct {
	Added      []string // codes with shiny art we have never synced
	Grown      []grown  // codes we have, that gained species upstream
	Unflagged  []string // .f codes with a year that upstream will not call costumes, see below
	Unverified int      // .f codes nobody could judge because the masterfile was unreachable
	Scanned    int      // assets that matched the costume grammar, which is NOT len(files)
	Female     int      // of those, the .g2 variants, which never make a code on their own
}

// eventYearRe finds a four-digit year token in a costume code, and separates the two kinds of .f
// code the masterfile leaves unflagged: a permanent battle or regional form (MEGA, ALOLA,
// GALARIAN, CROWNED_SWORD, UNOWN_A, the Vivillon patterns, the Spinda spot sets) never carries a
// year, while an event costume nearly always does.
//
// It decides only what to SAY, never what to admit. cmd/synccostumes keeps an identical copy for
// its UNFLAGGED report, and the two must stay in step for the same reason the .g2 parsers do:
// this check must never name something `make costumes` would not act on.
var eventYearRe = regexp.MustCompile(`(^|_)(19|20)\d{2}($|_)`)

// newCodes reports what upstream has that the embedded catalog does not.
//
// known is the embedded catalog, code to the species it has art for; curated is every code a label
// points at. A curated label is enough on its own to admit a .f code, and that clause is load
// bearing rather than a loophole: isCostume has false negatives (PIKACHU_COPY_2019, Clone Pikachu,
// carries no flag at all), so trusting the flag alone would quietly drop a costume trainers have
// already recorded.
func newCodes(files []string, known map[string][]int, curated map[string]bool, forms costumeForms) driftReport {
	// Keyed by code, valued by the dex numbers upstream has shiny art for. The dex is not
	// decoration: asking the masterfile whether a .f code is a costume needs one, and a code we
	// already have is news only when that list has grown.
	upstream := map[string][]int{}
	assets := map[string]int{} // assets seen per code, so the count can be reported honestly
	female := map[string]int{}
	var rep driftReport

	for _, f := range files {
		m := driftRe.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		dex, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// A form and a costume can co-occur (pm263.fGALARIAN.cGOFEST_2021_NOEVOLVE); when both
		// are present the costume is what makes it a costume. Same order as buildCatalog.
		var code string
		switch {
		case m[3] != "":
			code = "c:" + m[3]
		case m[2] != "":
			code = "f:" + m[2]
		default:
			continue // a plain species shiny, not a costume asset
		}
		assets[code]++
		if m[4] == ".g2" {
			female[code]++
			continue
		}
		upstream[code] = append(upstream[code], dex)
	}

	// isCostume applies buildCatalog's admission rule to one (code, dex). A .c code only ever means
	// a costume, so it is trusted from the asset tree alone, which keeps a brand-new costume drop
	// visible here even when the masterfile lags. A .f code is shared with regional, mega and battle
	// forms, so it needs the masterfile's verdict for THAT species, or a curated label vouching for
	// the whole code.
	isCostume := func(code string, dex int) bool {
		if strings.HasPrefix(code, "c:") || curated[code] {
			return true
		}
		if forms == nil {
			return false
		}
		_, ok := forms.IsCostumeForm(dex, strings.TrimPrefix(code, "f:"))
		return ok
	}

	counted := map[string]bool{}
	for code, dexes := range upstream {
		if have, ok := known[code]; ok {
			counted[code] = true
			// Growth only, never shrinkage. Upstream does not delete assets, so a dex we have and
			// they do not is far more likely to be a partial listing or a transient than a real
			// removal, and acting on one would eventually mean dropping a label a trainer has
			// already recorded against.
			//
			// Each extra species goes through the same admission rule buildCatalog uses, per dex.
			// Skipping that check is how the .g2 bug worked: report a species `make costumes` will
			// not write, and the button nags about it forever. The masterfile flags .f forms per
			// species, so a code we already have can perfectly well gain art for a dex upstream
			// does not call a costume, and that dex must not be reported.
			//
			// Compared against the catalog's default art, so female-only art for a new species is
			// deliberately not growth: the app never renders it. That does change catalog.json's
			// "female" list without this check noticing, and `make costumes-check` is what catches
			// that case.
			var extra []int
			for _, d := range missingDex(dexes, have) {
				// With no masterfile there is no verdict to apply, and no `make costumes` either
				// (it refuses to run without one), so report and let the note say the masterfile
				// was unreachable. Silence would be the worse half of the trade: this is the one
				// signal that still works when a third party is down.
				if forms == nil || isCostume(code, d) {
					extra = append(extra, d)
				}
			}
			if len(extra) > 0 {
				rep.Grown = append(rep.Grown, grown{Code: code, Dex: extra})
			}
			continue
		}
		if strings.HasPrefix(code, "c:") || curated[code] {
			counted[code] = true
			rep.Added = append(rep.Added, code)
			continue
		}
		if forms == nil {
			// A .f code nobody could judge. It is not counted as a costume asset either: we do not
			// know that it is one.
			rep.Unverified++
			continue
		}
		admitted := false
		for _, dex := range dexes {
			if isCostume(code, dex) {
				counted[code] = true
				rep.Added = append(rep.Added, code)
				admitted = true
				break
			}
		}
		// Upstream has shiny art, we do not have the code, and nothing will admit it: neither the
		// masterfile nor a label calls it a costume. Usually correct (every Mega, Alolan and Unown
		// lands here), but it is also exactly where a real costume goes missing in silence, which
		// f:COPY_2019 did for months. Say the ones carrying a year out loud so a human can look.
		if !admitted && eventYearRe.MatchString(strings.TrimPrefix(code, "f:")) {
			rep.Unflagged = append(rep.Unflagged, code)
		}
	}

	// Count only the assets of codes we actually judged costumes. Counting every match would fold
	// in every Alolan, Mega and Crowned form in the game and then print the total as "costume
	// shiny assets", which is the same kind of lie as printing len(files).
	for code := range counted {
		rep.Scanned += assets[code]
		rep.Female += female[code]
	}

	sort.Strings(rep.Added)
	sort.Strings(rep.Unflagged)
	sort.Slice(rep.Grown, func(i, j int) bool { return rep.Grown[i].Code < rep.Grown[j].Code })
	return rep
}

// missingDex is the sorted set of dex numbers upstream has and we do not.
func missingDex(upstream, have []int) []int {
	got := make(map[int]bool, len(have))
	for _, d := range have {
		got[d] = true
	}
	var out []int
	seen := map[int]bool{}
	for _, d := range upstream {
		if got[d] || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

func joinInts(d []int) string {
	s := make([]string, len(d))
	for i, n := range d {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

func more(total, shown int) string {
	if total > shown {
		return fmt.Sprintf(", +%d more", total-shown)
	}
	return ""
}

// The upstream listing is cached because two admin surfaces reach for it: the Costumes tab's
// "Fetch new costumes" button (RequireAdmin, five presses per minute per IP) and the superadmin
// Check Scrapers panel. Each scan costs five GitHub API calls, so without this they take turns
// draining the same anonymous budget for the whole VPS.
//
// The raw listing is cached, NOT the finished answer. Unlabelled and labelledCodes read the
// runtime label overlay, which changes the instant an admin names a costume, and the tab reloads
// straight afterwards: caching the answer would show a stale backlog count and make a successful
// naming look like it did nothing. Everything downstream of the listing is pure and cheap.
//
// Deliberately not the RWMutex in costumes.go. That one guards the label overlay and is taken for
// read by every request that resolves a costume sprite, so holding it across five network calls
// with a 30 second timeout each would stall the site. This lock guards the listing and nothing
// else. A plain Mutex held for the whole fetch, so two admins pressing at the same moment produce
// one upstream call and the second waits for and reuses the answer.
var (
	assetMu      sync.Mutex
	assetList    []string
	assetAt      time.Time
	assetHold    time.Time // do not retry before the quota resets
	assetHoldErr error

	// Seams for the tests: they must be able to drive the cache without a network.
	fetchAssets = listAssets
	now         = time.Now
)

const (
	assetTTL = 5 * time.Minute

	// How long to stop asking after a rate limit that did not say when it resets. Long enough to
	// stop a button-masher spending the rest of the quota, short enough that a mistake costs one
	// coffee break rather than an afternoon.
	defaultHold = 15 * time.Minute

	// A whole-walk deadline, not just a per-call one. Five sequential calls at 30 seconds each plus
	// the masterfile load could outlast the server's 90 second WriteTimeout, and then the admin sees
	// "Failed to fetch" for a check that actually succeeded and populated the cache.
	listDeadline = 45 * time.Second
)

// assets returns the upstream file listing and how stale the answer is, fetching when the cache
// has expired or force is set.
func assets(force bool) ([]string, time.Duration, error) {
	waiting := now()

	assetMu.Lock()
	defer assetMu.Unlock()

	if age := now().Sub(assetAt); assetList != nil && (age < assetTTL || assetAt.After(waiting)) {
		// A forced call still takes an answer that arrived while it queued for the lock. Otherwise
		// six admins pressing refresh at once would each wait for the one in front and then go and
		// fetch again anyway, which is the pile-up the lock was supposed to prevent.
		if !force || assetAt.After(waiting) {
			return assetList, age, nil
		}
	}
	// The hold outranks force. A forced retry inside a rate limit window cannot succeed, and a
	// rejected request still counts against us, so the honest answer is the reason we are waiting.
	if now().Before(assetHold) {
		return nil, 0, assetHoldErr
	}

	files, err := fetchAssets()
	if err != nil {
		// An ordinary failure is NOT cached: locking the button out for five minutes after a
		// transient is worse than the transient. Only a rate limit sets a hold.
		var rl rateLimitError
		if errors.As(err, &rl) {
			// A limit with no usable reset header still has to hold, or the guard above is a no-op
			// and the button hammers a limit it has already hit. Never shorten a hold already set:
			// a second, vaguer 403 must not clear a precise one.
			until := rl.reset
			if until.IsZero() {
				until = now().Add(defaultHold)
			}
			if until.After(assetHold) {
				assetHold, assetHoldErr = until, err
			}
		}
		return nil, 0, err
	}
	assetList, assetAt = files, now()
	assetHold, assetHoldErr = time.Time{}, nil
	return files, 0, nil
}

// rateLimitError is an exhausted GitHub quota, carrying the moment it resets so the cache can
// stop asking until then.
type rateLimitError struct {
	reset time.Time
	msg   string
}

func (e rateLimitError) Error() string { return e.msg }

// ghAPI is a var so a test can point the walk at an httptest server.
var ghAPI = "https://api.github.com"

const ghUA = "hailsDotGO/1.0 (+https://pogo.hails.app)"

// httpErr turns a failed GitHub response into a message that says what actually went wrong.
//
// An exhausted quota used to surface as an opaque "url -> 403", which reads like the repo is gone.
// Unauthenticated callers share one 60/hr budget per IP, so the whole VPS has one, and this route
// is RequireAdmin at five presses a minute: it can be drained in under three minutes by one
// impatient admin. Setting GITHUB_TOKEN on the server (the same one the label sync already uses)
// raises it to 5000/hr, so the message has to name it.
func httpErr(url string, resp *http.Response) error {
	// Three shapes, all of them "stop asking". The primary limit spends X-RateLimit-Remaining down
	// to 0. The secondary (abuse) limit answers 403 or 429 with Retry-After and a remaining count
	// that is often NOT zero, and GitHub's documented response to ignoring one is a longer block.
	// A bare 429 is a limit whatever the headers say.
	rem := resp.Header.Get("X-RateLimit-Remaining")
	retry := resp.Header.Get("Retry-After")
	limited := resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && (rem == "0" || retry != ""))
	if limited {
		limit := resp.Header.Get("X-RateLimit-Limit")
		if limit == "" {
			limit = "the hourly"
		}
		var reset time.Time
		if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset = time.Unix(sec, 0)
		} else if secs, err := strconv.Atoi(retry); err == nil {
			reset = now().Add(time.Duration(secs) * time.Second)
		}
		wait := "shortly"
		if !reset.IsZero() {
			wait = "in " + reset.Sub(now()).Round(time.Minute).String()
		}
		return rateLimitError{
			reset: reset,
			msg: fmt.Sprintf("github rate limit reached (%s of %s requests left, resets %s); "+
				"set GITHUB_TOKEN on the server to raise the limit", orUnknown(rem), limit, wait),
		}
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown number"
	}
	return s
}

// listAssets walks the GitHub tree down to the asset folder, two API calls per level.
//
// It sends GITHUB_TOKEN when the server has one. This was the only GitHub caller in the tree that
// did not, which is why an admin pressing the button repeatedly could exhaust the anonymous budget
// for everything else on the box.
func listAssets() ([]string, error) {
	const (
		repo = "PokeMiners/pogo_assets"
		dir  = "Images/Pokemon - 256x256/Addressable Assets"
	)
	client := &http.Client{Timeout: 30 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), listDeadline)
	defer cancel()

	get := func(url string, v any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", ghUA)
		if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return httpErr(url, resp)
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}

	var head struct {
		SHA string `json:"sha"`
	}
	if err := get(ghAPI+"/repos/"+repo+"/commits/master", &head); err != nil {
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
	for seg := range strings.SplitSeq(dir, "/") {
		var t tree
		if err := get(ghAPI+"/repos/"+repo+"/git/trees/"+cur, &t); err != nil {
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
	if err := get(ghAPI+"/repos/"+repo+"/git/trees/"+cur, &t); err != nil {
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
