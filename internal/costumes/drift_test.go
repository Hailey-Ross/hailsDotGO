package costumes

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeForms stands in for the masterfile. Only the codes listed are costumes; everything else is
// an ordinary alternate form, which is exactly the distinction the real file draws.
type fakeForms map[string]bool

func (f fakeForms) IsCostumeForm(dex int, code string) (string, bool) {
	if !f[code] {
		return "", false
	}
	return code, true
}

// perDexForms is the same stand-in, but answering per species, which is what the real masterfile
// does: a .f code is a costume on the species that wear it and an ordinary form elsewhere.
type perDexForms map[int]map[string]bool

func (f perDexForms) IsCostumeForm(dex int, code string) (string, bool) {
	if !f[dex][code] {
		return "", false
	}
	return code, true
}

// The bug this whole rule exists for. Every costume the game has shipped since roughly 2024 is a
// .f full form code, and the check used to discard all of them and keep only .c, so it answered
// "nothing new upstream" no matter what the game released. A trainer reported a costume as missing
// and the admin panel agreed with them, wrongly.
func TestNewCodesSeesFullFormCostumes(t *testing.T) {
	files := []string{
		"pm25.fANNIVERSARY_2026.s.icon.png", // a costume, flagged upstream
		"pm25.fANNIVERSARY_2026.g2.s.icon.png",
		"pm25.fGIGANTAMAX.s.icon.png",     // a battle form, must NOT be reported
		"pm26.fALOLA.s.icon.png",          // a regional form, must NOT be reported
		"pm888.fCROWNED_SWORD.s.icon.png", // a battle form, must NOT be reported
		"pm25.cBRAND_NEW_HAT.s.icon.png",  // .c is unambiguous, always reported
		"pm25.fVISOR_2026.icon.png",       // not shiny: we never render it, so it does not count
	}
	forms := fakeForms{"ANNIVERSARY_2026": true}

	rep := newCodes(files, map[string][]int{}, map[string]bool{}, forms)

	want := []string{"c:BRAND_NEW_HAT", "f:ANNIVERSARY_2026"}
	if !slices.Equal(rep.Added, want) {
		t.Errorf("added = %v, want %v", rep.Added, want)
	}
	if rep.Unverified != 0 {
		t.Errorf("unverified = %d, want 0 when the masterfile answered", rep.Unverified)
	}
}

// A code already in the catalog is not news, however it got there.
func TestNewCodesIgnoresWhatWeAlreadyHave(t *testing.T) {
	files := []string{"pm25.fANNIVERSARY_2026.s.icon.png", "pm25.cANNIVERSARY.s.icon.png"}
	known := map[string][]int{"f:ANNIVERSARY_2026": {25}, "c:ANNIVERSARY": {25}}

	rep := newCodes(files, known, map[string]bool{}, fakeForms{"ANNIVERSARY_2026": true})
	if len(rep.Added) != 0 {
		t.Errorf("added = %v, want nothing: both codes are already synced", rep.Added)
	}
	if len(rep.Grown) != 0 {
		t.Errorf("grown = %v, want nothing: the same species, not new ones", rep.Grown)
	}
}

// isCostume has false negatives: Clone Pikachu (PIKACHU_COPY_2019) carries no flag at all despite
// its shiny asset being live. A curated label vouching for the code has to be enough on its own,
// or the check would drop a costume trainers have already recorded.
func TestNewCodesTrustsACuratedLabelOverTheFlag(t *testing.T) {
	files := []string{"pm25.fCOPY_2019.s.icon.png"}
	curated := map[string]bool{"f:COPY_2019": true}

	rep := newCodes(files, map[string][]int{}, curated, fakeForms{})
	if !slices.Equal(rep.Added, []string{"f:COPY_2019"}) {
		t.Errorf("added = %v, want [f:COPY_2019]: a curated label vouches for it", rep.Added)
	}
}

// The masterfile is a third party. If it is unreachable the .c half of the answer is still sound,
// so the check reports that half and counts what it could not judge, rather than failing outright.
func TestNewCodesDegradesWhenTheMasterfileIsUnreachable(t *testing.T) {
	files := []string{
		"pm25.cBRAND_NEW_HAT.s.icon.png",
		"pm25.fANNIVERSARY_2026.s.icon.png",
		"pm26.fALOLA.s.icon.png",
	}

	rep := newCodes(files, map[string][]int{}, map[string]bool{}, nil)

	if !slices.Equal(rep.Added, []string{"c:BRAND_NEW_HAT"}) {
		t.Errorf("added = %v, want just the .c code", rep.Added)
	}
	if rep.Unverified != 2 {
		t.Errorf("unverified = %d, want 2 (the two .f codes nobody could judge)", rep.Unverified)
	}
}

// A code upstream has ONLY female art for never enters the catalog: buildCatalog files a .g2 hit
// under "female" and continues, and builds the catalog from the default art alone. Reporting one
// as new would be drift that `make costumes` can never clear, so the button would nag forever
// about a costume that does not exist as far as the app is concerned.
func TestNewCodesIgnoresFemaleOnlyArt(t *testing.T) {
	files := []string{"pm25.fWINTER_2026.g2.s.icon.png"}

	rep := newCodes(files, map[string][]int{}, map[string]bool{}, fakeForms{"WINTER_2026": true})

	if len(rep.Added) != 0 {
		t.Errorf("added = %v, want nothing: female-only art never becomes a catalog code", rep.Added)
	}
	// Nor is it counted. Scanned reports the assets of codes we judged costumes, and a code with no
	// default art is never one of those, so the count can only understate, never overstate.
	if rep.Scanned != 0 || rep.Female != 0 {
		t.Errorf("scanned = %d, female = %d, want 0 and 0", rep.Scanned, rep.Female)
	}
}

// The other half of that rule: excluding .g2 must not exclude a costume that has both variants.
func TestNewCodesStillSeesACodeWithBothVariants(t *testing.T) {
	files := []string{"pm25.fWINTER_2026.s.icon.png", "pm25.fWINTER_2026.g2.s.icon.png"}

	rep := newCodes(files, map[string][]int{}, map[string]bool{}, fakeForms{"WINTER_2026": true})

	if !slices.Equal(rep.Added, []string{"f:WINTER_2026"}) {
		t.Errorf("added = %v, want [f:WINTER_2026]", rep.Added)
	}
	if rep.Scanned != 2 || rep.Female != 1 {
		t.Errorf("scanned = %d, female = %d, want 2 and 1", rep.Scanned, rep.Female)
	}
}

// The failure this check could not see at all. A costume ships on three species and a later event
// wave adds more, so the code set is IDENTICAL and the old set-of-codes comparison answered "no
// new codes upstream" while the catalog was stale and trainers of the new species saw a plain
// shiny with no costume art.
func TestNewCodesReportsSpeciesAddedToAKnownCode(t *testing.T) {
	files := []string{
		"pm25.cFALL_2018.s.icon.png",
		"pm26.cFALL_2018.s.icon.png",
		"pm172.cFALL_2018.s.icon.png", // new upstream
	}
	known := map[string][]int{"c:FALL_2018": {25, 26}}

	rep := newCodes(files, known, map[string]bool{}, fakeForms{})

	if len(rep.Added) != 0 {
		t.Errorf("added = %v, want nothing: the code itself is not new", rep.Added)
	}
	if len(rep.Grown) != 1 || rep.Grown[0].Code != "c:FALL_2018" || !slices.Equal(rep.Grown[0].Dex, []int{172}) {
		t.Fatalf("grown = %+v, want c:FALL_2018 gaining dex 172", rep.Grown)
	}
}

// A dex we have and upstream does not is far more likely to be a partial listing than a real
// deletion, and acting on one would eventually mean dropping a label a trainer recorded against.
func TestNewCodesIgnoresSpeciesMissingUpstream(t *testing.T) {
	files := []string{"pm25.cFALL_2018.s.icon.png"}
	known := map[string][]int{"c:FALL_2018": {25, 26, 172}}

	rep := newCodes(files, known, map[string]bool{}, fakeForms{})

	if len(rep.Grown) != 0 {
		t.Errorf("grown = %+v, want nothing: a shrink is not drift", rep.Grown)
	}
}

// Growth needs no masterfile call, because a code already in the catalog has already been judged a
// costume. So growth survives a dead masterfile, which Added cannot.
func TestNewCodesReportsGrowthWithoutTheMasterfile(t *testing.T) {
	files := []string{"pm25.fX.s.icon.png", "pm26.fX.s.icon.png"}
	known := map[string][]int{"f:X": {25}}

	rep := newCodes(files, known, map[string]bool{}, nil)

	if len(rep.Grown) != 1 || !slices.Equal(rep.Grown[0].Dex, []int{26}) {
		t.Fatalf("grown = %+v, want f:X gaining dex 26 even with no masterfile", rep.Grown)
	}
	if rep.Unverified != 0 {
		t.Errorf("unverified = %d, want 0: a catalogued code needs no verdict", rep.Unverified)
	}
}

// Female art for a new species is not growth: the app renders the default sprite only, so nothing
// about what a trainer sees has changed. `make costumes-check` is what catches that case.
func TestNewCodesIgnoresFemaleOnlyGrowth(t *testing.T) {
	files := []string{"pm25.fX.s.icon.png", "pm26.fX.g2.s.icon.png"}
	known := map[string][]int{"f:X": {25}}

	rep := newCodes(files, known, map[string]bool{}, nil)

	if len(rep.Grown) != 0 {
		t.Errorf("grown = %+v, want nothing: female-only art is not a species we can render", rep.Grown)
	}
}

// Scanned is what the note reports. It used to print len(files), which is every blob in the asset
// folder (3750 of them today) while claiming it had counted shiny costume assets. Counting every
// filename that merely MATCHES is the same lie one layer down: the .f prefix is shared with every
// regional, mega and battle form in the game.
func TestNewCodesCountsOnlyCostumeAssets(t *testing.T) {
	files := []string{
		"pm25.s.icon.png",          // a plain species shiny, not a costume
		"pm25.cFALL_2018.icon.png", // a costume, but not shiny
		"README.md",                // junk
		"pm26.fALOLA.s.icon.png",   // a regional form: matches the grammar, is not a costume
		"pm888.fCROWNED_SWORD.s.icon.png",
		"pm25.cFALL_2018.s.icon.png",
		"pm26.cFALL_2018.s.icon.png",
		"pm26.cFALL_2018.g2.s.icon.png",
	}

	rep := newCodes(files, map[string][]int{}, map[string]bool{}, fakeForms{})

	if rep.Scanned != 3 {
		t.Errorf("scanned = %d, want 3 (the costume's own assets, not the regional and battle forms)", rep.Scanned)
	}
	if rep.Female != 1 {
		t.Errorf("female = %d, want 1", rep.Female)
	}
}

// Growth has to apply the SAME admission rule buildCatalog does, per species. The masterfile flags
// .f forms per dex, so a costume we already have can gain art for a species upstream does not call
// a costume, and buildCatalog will not write that species. Reporting it is phantom drift of exactly
// the kind the .g2 bug was: the button asks for a `make costumes` that changes nothing, forever.
func TestNewCodesDoesNotReportGrowthTheSyncToolWouldNotWrite(t *testing.T) {
	files := []string{"pm25.fVISOR_2026.s.icon.png", "pm1025.fVISOR_2026.s.icon.png"}
	known := map[string][]int{"f:VISOR_2026": {25}}

	// A costume on Pikachu only. Dex 1025 carries the asset but the masterfile does not call it a
	// costume there, so buildCatalog keeps dex=[25].
	rep := newCodes(files, known, map[string]bool{}, perDexForms{25: {"VISOR_2026": true}})

	if len(rep.Grown) != 0 {
		t.Errorf("grown = %+v, want nothing: `make costumes` would not add dex 1025", rep.Grown)
	}
}

// The other half: a curated label vouches for the whole code, so every species it appears on counts,
// which is what keeps Clone Pikachu working.
func TestNewCodesReportsGrowthOnACuratedCode(t *testing.T) {
	files := []string{"pm25.fCOPY_2019.s.icon.png", "pm26.fCOPY_2019.s.icon.png"}
	known := map[string][]int{"f:COPY_2019": {25}}

	rep := newCodes(files, known, map[string]bool{"f:COPY_2019": true}, perDexForms{})

	if len(rep.Grown) != 1 || !slices.Equal(rep.Grown[0].Dex, []int{26}) {
		t.Errorf("grown = %+v, want f:COPY_2019 gaining dex 26", rep.Grown)
	}
}

// grammarCases is the shared truth about the asset filename grammar.
//
// It is DUPLICATED verbatim in cmd/synccostumes/grammar_test.go, and that duplication is the point.
// The two halves of this pipeline parse the same filenames with two different regexes, because
// cmd/synccostumes is package main and cannot import this package, and they have already drifted
// apart once: this check accepted .g2 female art as proof a code existed while buildCatalog filed
// it under "female" and never made a code of it, so the admin button would have nagged forever
// about a costume `make costumes` could not add. Asserting the same table on both sides is what
// stops the next divergence being invisible.
//
// Keep the two copies identical. If you add a case here, add it there.
var grammarCases = []struct {
	file   string
	code   string // "" means this filename must not produce a costume code
	dex    int
	female bool
}{
	{file: "pm25.cFALL_2018.s.icon.png", code: "c:FALL_2018", dex: 25},
	{file: "pm25.fVISOR_2026.s.icon.png", code: "f:VISOR_2026", dex: 25},
	{file: "pm263.fGALARIAN.cGOFEST_2021_NOEVOLVE.s.icon.png", code: "c:GOFEST_2021_NOEVOLVE", dex: 263},
	{file: "pm25.cFALL_2018.g2.s.icon.png", code: "c:FALL_2018", dex: 25, female: true},
	{file: "pm25.fVISOR_2026.g2.s.icon.png", code: "f:VISOR_2026", dex: 25, female: true},
	{file: "pm1025.cFALL_2018.s.icon.png", code: "c:FALL_2018", dex: 1025}, // dex is not zero padded
	{file: "pm25.cFALL_2018.icon.png"},                                     // not shiny
	{file: "pm25.s.icon.png"},                                              // a plain species shiny
	{file: "pm25.g2.s.icon.png"},                                           // a plain female shiny
	{file: "pm25.icon.png"},
	{file: "pm25.cFALL_2018.s.png"},
	{file: "notapokemon.png"},
}

// driftRe is this package's half of the grammar. Its answers must match assetRe's, case for case.
func TestDriftGrammarMatchesTheSyncTool(t *testing.T) {
	for _, c := range grammarCases {
		m := driftRe.FindStringSubmatch(c.file)
		code, dex, female := "", 0, false
		if m != nil {
			switch {
			case m[3] != "":
				code = "c:" + m[3]
			case m[2] != "":
				code = "f:" + m[2]
			}
			if code != "" {
				fmt.Sscanf(m[1], "%d", &dex)
				female = m[4] == ".g2"
			}
		}
		if code != c.code || (c.code != "" && (dex != c.dex || female != c.female)) {
			t.Errorf("%s -> code %q dex %d female %v, want code %q dex %d female %v",
				c.file, code, dex, female, c.code, c.dex, c.female)
		}
	}
}

// resetAssetCache puts the package back to a cold cache and restores the real seams.
func resetAssetCache(t *testing.T) {
	t.Helper()
	realFetch, realNow := fetchAssets, now
	t.Cleanup(func() {
		assetMu.Lock()
		defer assetMu.Unlock()
		fetchAssets, now = realFetch, realNow
		assetList, assetAt, assetHold, assetHoldErr = nil, time.Time{}, time.Time{}, nil
	})
	assetMu.Lock()
	defer assetMu.Unlock()
	assetList, assetAt, assetHold, assetHoldErr = nil, time.Time{}, time.Time{}, nil
}

// Two admin surfaces reach for this listing and each scan costs five GitHub API calls, so a second
// press inside the window must not spend the quota again.
func TestAssetCacheServesASecondPressWithoutRefetching(t *testing.T) {
	resetAssetCache(t)
	calls := 0
	fetchAssets = func() ([]string, error) {
		calls++
		return []string{"pm25.cFALL_2018.s.icon.png"}, nil
	}
	clock := time.Now()
	now = func() time.Time { return clock }

	if _, age, err := assets(false); err != nil || age != 0 {
		t.Fatalf("first call: age = %v, err = %v, want a fresh fetch", age, err)
	}
	clock = clock.Add(30 * time.Second)
	if _, age, err := assets(false); err != nil || age != 30*time.Second {
		t.Fatalf("second call: age = %v, err = %v, want a cached answer aged 30s", age, err)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1", calls)
	}
}

func TestAssetCacheExpires(t *testing.T) {
	resetAssetCache(t)
	calls := 0
	fetchAssets = func() ([]string, error) {
		calls++
		return []string{"pm25.cFALL_2018.s.icon.png"}, nil
	}
	clock := time.Now()
	now = func() time.Time { return clock }

	assets(false)
	clock = clock.Add(assetTTL + time.Second)
	assets(false)

	if calls != 2 {
		t.Errorf("fetched %d times, want 2: the cache had expired", calls)
	}
}

// Locking the button out for five minutes after a blip is worse than the blip.
func TestAssetCacheDoesNotCacheAnOrdinaryFailure(t *testing.T) {
	resetAssetCache(t)
	calls := 0
	fetchAssets = func() ([]string, error) {
		calls++
		return nil, errors.New("dial tcp: connection refused")
	}

	assets(false)
	assets(false)

	if calls != 2 {
		t.Errorf("fetched %d times, want 2: a transient error must not be cached", calls)
	}
}

// A retry inside a rate limit window cannot succeed and still counts against us, so the honest
// answer is the reason we are waiting, without spending another request to rediscover it.
func TestAssetCacheHoldsOffAfterARateLimit(t *testing.T) {
	resetAssetCache(t)
	calls := 0
	limit := rateLimitError{reset: time.Now().Add(40 * time.Minute), msg: "github rate limit reached, set GITHUB_TOKEN"}
	fetchAssets = func() ([]string, error) {
		calls++
		return nil, limit
	}

	_, _, first := assets(false)
	_, _, second := assets(false)

	if calls != 1 {
		t.Errorf("fetched %d times, want 1: the hold must survive the second press", calls)
	}
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Errorf("errors differ: %v then %v, want the same explanation both times", first, second)
	}
	// Even a forced refresh must respect it: forcing cannot conjure quota.
	if _, _, err := assets(true); err == nil || calls != 1 {
		t.Errorf("forced call: err = %v after %d fetches, want the held error and no new fetch", err, calls)
	}
}

func TestForcedRefreshBypassesTheCache(t *testing.T) {
	resetAssetCache(t)
	calls := 0
	fetchAssets = func() ([]string, error) {
		calls++
		return []string{fmt.Sprintf("pm%d.cFALL_2018.s.icon.png", 24+calls)}, nil
	}

	assets(false)
	files, age, err := assets(true)
	if err != nil {
		t.Fatalf("forced call: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2", calls)
	}
	if age != 0 || !slices.Equal(files, []string{"pm26.cFALL_2018.s.icon.png"}) {
		t.Errorf("forced call returned %v (age %v), want the fresh listing", files, age)
	}
	// And the fresh answer replaces the cache, so the next Check Scrapers run benefits.
	cached, _, _ := assets(false)
	if !slices.Equal(cached, files) {
		t.Errorf("cache holds %v, want the forced answer %v", cached, files)
	}
}

// An exhausted quota used to surface as "url -> 403", which reads like the repo is gone.
func TestRateLimitErrorNamesTheLimitAndTheFix(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{},
		Body:       http.NoBody,
	}
	resp.Header.Set("X-RateLimit-Remaining", "0")
	resp.Header.Set("X-RateLimit-Limit", "60")
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(42*time.Minute).Unix()))

	err := httpErr("https://api.github.com/repos/x/y", resp)

	var rl rateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("got %T (%v), want a rateLimitError so the cache can hold off", err, err)
	}
	for _, want := range []string{"rate limit", "60", "GITHUB_TOKEN", "42m"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "-> 403") {
		t.Errorf("message %q still reads like a dead URL", err.Error())
	}
}

// A plain 403 that is not a quota problem must keep its body, and must NOT set a hold.
func TestNonRateLimitErrorIsNotHeld(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)),
	}

	err := httpErr("https://api.github.com/repos/x/y", resp)

	var rl rateLimitError
	if errors.As(err, &rl) {
		t.Fatal("a 404 must not be treated as a rate limit")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("message %q dropped the body", err.Error())
	}
}

// This was the only GitHub caller in the tree that did not send the token the server already has.
func TestListAssetsSendsTheToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	// Atomic: the counter is written on the server's goroutines and read on this one.
	var missing atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			missing.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits/master"):
			fmt.Fprint(w, `{"sha":"head"}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/head"):
			fmt.Fprint(w, `{"tree":[{"path":"Images","type":"tree","sha":"a"}]}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/a"):
			fmt.Fprint(w, `{"tree":[{"path":"Pokemon - 256x256","type":"tree","sha":"b"}]}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/b"):
			fmt.Fprint(w, `{"tree":[{"path":"Addressable Assets","type":"tree","sha":"c"}]}`)
		default:
			fmt.Fprint(w, `{"truncated":false,"tree":[{"path":"pm25.cFALL_2018.s.icon.png","type":"blob"}]}`)
		}
	}))
	defer srv.Close()

	realAPI := ghAPI
	ghAPI = srv.URL
	defer func() { ghAPI = realAPI }()

	files, err := listAssets()
	if err != nil {
		t.Fatalf("listAssets: %v", err)
	}
	if n := missing.Load(); n != 0 {
		t.Errorf("%d request(s) went out without the token", n)
	}
	if !slices.Equal(files, []string{"pm25.cFALL_2018.s.icon.png"}) {
		t.Errorf("files = %v, want the one blob", files)
	}
}

// A truncated listing looks exactly like costumes vanishing, so it must be an error rather than a
// partial answer that would report every missing code as a deletion.
func TestListAssetsRefusesATruncatedTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits/master"):
			fmt.Fprint(w, `{"sha":"head"}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/head"):
			fmt.Fprint(w, `{"tree":[{"path":"Images","type":"tree","sha":"a"}]}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/a"):
			fmt.Fprint(w, `{"tree":[{"path":"Pokemon - 256x256","type":"tree","sha":"b"}]}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees/b"):
			fmt.Fprint(w, `{"tree":[{"path":"Addressable Assets","type":"tree","sha":"c"}]}`)
		default:
			fmt.Fprint(w, `{"truncated":true,"tree":[]}`)
		}
	}))
	defer srv.Close()

	realAPI := ghAPI
	ghAPI = srv.URL
	defer func() { ghAPI = realAPI }()

	if _, err := listAssets(); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("err = %v, want a refusal to sync a partial listing", err)
	}
}

// The catalog and the labels must agree that the costume behind the July 2026 Willow event is
// real, has art on Pikachu, and is reachable by the name the game and every event site use for it.
func TestWillowCostumeResolves(t *testing.T) {
	url, ok := SpriteURL(25, "Pikachu", "Willow's Lab Coat")
	if !ok {
		t.Fatal("Willow's Lab Coat did not resolve for Pikachu")
	}
	if !strings.Contains(url, ".fANNIVERSARY_2026.") {
		t.Errorf("resolved to %s, want the f:ANNIVERSARY_2026 art", url)
	}
}

// Trainers type the name they read in the event notes, not ours. Both apostrophes matter: a phone
// keyboard produces the curly one, a desktop keyboard the straight one, and alias lookup is an
// exact map hit with no normalisation, so every spelling has to be listed.
func TestWillowAliasesResolveToTheSameArt(t *testing.T) {
	canonical, ok := SpriteURL(25, "Pikachu", "Willow's Lab Coat")
	if !ok {
		t.Fatal("Willow's Lab Coat did not resolve for Pikachu")
	}
	for _, alias := range []string{
		"Professor Willow's Assistant",
		"Professor Willow's assistant",
		"Professor Willow’s Assistant",
		"Professor Willow’s assistant",
	} {
		url, ok := SpriteURL(25, "Pikachu", alias)
		if !ok {
			t.Errorf("%q did not resolve", alias)
			continue
		}
		if url != canonical {
			t.Errorf("%q -> %s, want %s", alias, url, canonical)
		}
	}
}
