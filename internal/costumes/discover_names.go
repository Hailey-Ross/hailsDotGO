package costumes

// The server's side of the Dittobase name cache.
//
// cmd/synccostumes keeps its own cache (committed, so a fresh checkout starts warm). The server
// needs a second one for a reason the tool does not have: it asks about codes that usually turn
// out NOT to be costumes, and without remembering the misses an hourly job would re-fetch the same
// dead pages for every Vivillon pattern, forever. So this cache remembers both answers.
//
// Steady state is zero requests. A hit is cached permanently, a miss for a week, and the sitemap
// is only fetched when there is something to look up in it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"pogo.hails.cc/internal/costumenames"
)

// missTTL is how long a "no page for this" answer is trusted.
//
// Not forever: a costume can get a Dittobase page days after its art is mined, and this cache
// deciding otherwise once would keep it in the review queue permanently. A week is long enough
// that an ordinary alternate form is asked about roughly never.
const missTTL = 7 * 24 * time.Hour

type nameCache struct {
	Names  costumenames.Names `json:"names"`
	Misses map[string]string  `json:"misses"` // "code|dex" -> RFC3339 of the last look
}

// releaseTTL is how long a release answer is reused.
//
// Much shorter than a name, because a name is settled and a release is the thing being waited
// for. Costumes waiting on a release are a handful at a time, so re-asking about them daily is
// nothing, and the alternative is a costume going live a week late.
const releaseTTL = 20 * time.Hour

type dittoSource struct {
	// res is fetched on first genuine need, not up front. Codes that are neither admitted nor
	// queued are re-judged on every pass, by design, so an eager fetch here would mean a sitemap
	// request every hour forever even when every answer is already cached. Observed in production
	// the first time this ran: the Spinda spot patterns reach the naming step on every pass and
	// always will, because "ignored" is not a state worth persisting.
	res     *costumenames.Resolver
	resErr  error
	fetched bool

	cache nameCache
	path  string
	dirty bool

	// waiting is the set of codes whose release state is being watched, so a cached name does not
	// short-circuit the read that is the entire reason for asking.
	waiting map[string]bool
}

// resolver fetches the sitemap the first time something actually needs it, and remembers a failure
// so one pass does not retry it per code.
func (d *dittoSource) resolver() (*costumenames.Resolver, error) {
	if d.fetched {
		return d.res, d.resErr
	}
	d.fetched = true
	d.res, d.resErr = costumenames.NewResolver(nil)
	return d.res, d.resErr
}

// newDittobase opens the cache. It makes no network request: the sitemap is fetched only when a
// lookup misses the cache. The returned function writes the cache back, and must be called when
// the pass is done.
func newDittobase(path string) (nameSource, func(), error) {
	c := nameCache{Names: costumenames.Names{}, Misses: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		var loaded nameCache
		if json.Unmarshal(data, &loaded) == nil {
			if loaded.Names != nil {
				c.Names = loaded.Names
			}
			if loaded.Misses != nil {
				c.Misses = loaded.Misses
			}
		}
	}

	waiting := map[string]bool{}
	for _, code := range WaitingCodes() {
		waiting[code] = true
	}
	d := &dittoSource{cache: c, path: path, waiting: waiting}
	return d, d.save, nil
}

// Page answers from cache when it can, and otherwise reads the page.
//
// The NAME is cached permanently; release state is not cached at all. Caching "not released yet"
// would defeat the whole point of asking again, and the costumes this is asked about are the few
// that are actually waiting.
func (d *dittoSource) Page(dex int, species, code string) (costumenames.Page, error) {
	key := code + "|" + strconv.Itoa(dex)
	if n := d.cache.Names.Get(code, dex); n != "" {
		// A cached name still needs the live release state when something is waiting on it, so
		// only short-circuit when the caller is just naming things.
		if !d.wantRelease(code) {
			return costumenames.Page{Name: n}, nil
		}
	}
	if at, ok := d.cache.Misses[key]; ok && !d.wantRelease(code) {
		if t, err := time.Parse(time.RFC3339, at); err == nil && time.Since(t) < missTTL {
			return costumenames.Page{}, nil
		}
	}

	res, err := d.resolver()
	if err != nil {
		return costumenames.Page{}, err
	}
	slug := res.Match(species, code)
	if slug == "" {
		d.miss(key)
		return costumenames.Page{}, nil
	}
	p, err := res.FetchPage(slug, species)
	// Pace every request actually made, not just the ones that worked.
	time.Sleep(costumenames.Delay)
	if err != nil {
		return costumenames.Page{}, err
	}
	if p.Name == "" {
		d.miss(key)
		return p, nil
	}
	d.cache.Names.Set(code, dex, p.Name)
	d.dirty = true
	return p, nil
}

// wantRelease reports whether this code is one the caller is waiting on, in which case a cached
// name is not enough and the page has to be read for its release state.
func (d *dittoSource) wantRelease(code string) bool { return d.waiting[code] }

func (d *dittoSource) miss(key string) {
	d.cache.Misses[key] = time.Now().UTC().Format(time.RFC3339)
	d.dirty = true
}

func (d *dittoSource) save() {
	if !d.dirty || d.path == "" {
		return
	}
	data, err := json.MarshalIndent(d.cache, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(d.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
	}
	tmp := d.path + ".tmp"
	if os.WriteFile(tmp, append(data, '\n'), 0o644) == nil {
		os.Rename(tmp, d.path)
	}
}
