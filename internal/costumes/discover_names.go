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

type dittoSource struct {
	res   *costumenames.Resolver
	cache nameCache
	path  string
	dirty bool
}

// newDittobase opens the cache and, only if it has to, fetches the sitemap. The returned function
// writes the cache back, and must be called when the pass is done.
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

	res, err := costumenames.NewResolver(nil)
	if err != nil {
		return nil, nil, err
	}
	d := &dittoSource{res: res, cache: c, path: path}
	return d, d.save, nil
}

func (d *dittoSource) Name(dex int, species, code string) (string, error) {
	if n := d.cache.Names.Get(code, dex); n != "" {
		return n, nil
	}
	key := code + "|" + strconv.Itoa(dex)
	if at, ok := d.cache.Misses[key]; ok {
		if t, err := time.Parse(time.RFC3339, at); err == nil && time.Since(t) < missTTL {
			return "", nil
		}
	}

	slug := d.res.Match(species, code)
	if slug == "" {
		d.miss(key)
		return "", nil
	}
	name, err := d.res.Fetch(slug, species)
	// Pace every request actually made, not just the ones that worked.
	time.Sleep(costumenames.Delay)
	if err != nil {
		return "", err
	}
	if name == "" {
		d.miss(key)
		return "", nil
	}
	d.cache.Names.Set(code, dex, name)
	d.dirty = true
	return name, nil
}

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
