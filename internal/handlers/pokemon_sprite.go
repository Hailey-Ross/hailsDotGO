package handlers

// The Pokemon sprite proxy.
//
// Every Pokemon picture URL this server hands out used to be a raw
// raw.githubusercontent.com URL, which the client then fetched itself. That is three things
// at once:
//
//   - a privacy leak of exactly the kind v0.1.8b closed for Pokedex text. Moving that text
//     onto this server was justified because tapping a card "sent the dex number a trainer
//     had just tapped, from that trainer's own IP, to a third party on every tap". The
//     pictures were still doing precisely that, on every card, to GitHub.
//   - a caching loss. GitHub serves those files with max-age=300, so a device revalidated
//     art that has not changed since it was drawn, every five minutes. We serve 30 days.
//   - a dependency nobody can see. If raw.githubusercontent.com rate limits or moves, every
//     sprite on the site breaks at once and nothing here logs a thing.
//
// Same shape as the costume proxy in internal/costumes/proxy.go, and for the same stated
// reason: we deliberately do not hotlink. The difference is that this one has to survive a
// page asking for a thousand sprites at once, which the shiny dex does.
//
// SCOPE, because it is easy to overclaim here. This covers every sprite URL the SERVER
// produces, which is the whole website. It does NOT cover the companion app end to end: the
// app derives some sprites locally from a dex number through a hardcoded upstream base
// (util/Sprites.kt), so its DPS, PvP, box-add and shiny-dex-detail screens still fetch from
// the origin directly. Reason 1 above still applies to those until the app is changed.

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// PokemonSpritePath is the route these are served from. Exported because spriteURLSlug
// builds every sprite URL in the app from it, and the two must not drift.
const PokemonSpritePath = "/api/pokemon-sprite/"

// pokemonSpriteOrigin is where a miss is fetched from.
//
// Pinned to master rather than a commit, which is why the response below is NOT marked
// immutable: we cannot promise a file will never change when we are not naming a revision.
// 30 days is the real win here anyway, against upstream's 5 minutes.
const pokemonSpriteOrigin = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/"

// maxPokemonSprite caps what will be read from the origin. These files run 1 to 3 KB; the
// cap is not a tuning knob, it is the ceiling that stops a compromised or redirected origin
// from feeding this process something enormous.
const maxPokemonSprite = 256 << 10

// pokemonSpriteNumeric matches a bare id: "25.png", "10188.png". Five digits, because the
// national dex is four and PokeAPI's variant ids are five (Crowned Sword Zacian is 10188,
// the highest this app references is 10253). Six would multiply the reachable name count by
// ten and buy nothing.
var pokemonSpriteNumeric = regexp.MustCompile(`^[0-9]{1,5}\.png$`)

// pokemonSpriteNamed is every hyphenated slug this app can ask for, derived from the tables
// that produce them rather than from a pattern.
//
// This is the correction to a real defect found in review. The allowlist was
// `^[0-9]{1,6}(-[a-z0-9]+)*\.png$`, which stops traversal but admits an INFINITE set of
// names: "1-a.png", "1-a-a.png", and so on to any length. Every novel name missed all three
// cache tiers, took a semaphore slot, made a real request to the origin from this server's
// address, and left a permanent entry in the negative cache. Measured in review: five
// crafted names, five real upstream 404s, five retained keys, and a projected ~262,000
// upstream requests an hour from an anonymous unrated route. The endpoint the file header
// calls a risk to the whole site's art was the mechanism for taking it down.
//
// Bounding the grammar is not enough on its own, so it is paired with the cap in
// rememberPokemonSpriteMiss. Together the reachable set is at most 100,000 numeric names
// plus the few dozen below, and the memory cost of probing them is fixed.
//
// The costume proxy had this right first: costumes.AllowedFile ends in a lookup against the
// real catalog, so its reachable space is the data's, not a pattern's.
var pokemonSpriteNamed = func() map[string]bool {
	m := make(map[string]bool, len(unownSpriteSlug)+len(vivillonSpriteSlug))
	for _, slug := range unownSpriteSlug {
		m[slug+".png"] = true
	}
	for _, slug := range vivillonSpriteSlug {
		m[slug+".png"] = true
	}
	return m
}()

// pokemonSpriteAllowed reports whether name is one this app can legitimately ask for.
//
// Load bearing: without it the endpoint fetches an attacker chosen path from the origin on
// demand, which is an open proxy. Neither branch admits a path separator or a second dot, so
// traversal cannot be expressed in either, and the caller runs filepath.Base first as well.
func pokemonSpriteAllowed(name string) bool {
	return pokemonSpriteNumeric.MatchString(name) || pokemonSpriteNamed[name]
}

var (
	pokemonSpriteHTTP = &http.Client{Timeout: 20 * time.Second}

	pokemonSpriteMu    sync.RWMutex
	pokemonSpriteCache = map[string][]byte{}

	// pokemonSpriteMissing remembers a 404 so a species PokeAPI has no art for is not
	// re-fetched on every page view. There genuinely are such species, which is why
	// ts/shinies.ts hides a sprite on its error handler rather than treating a miss as a
	// fault. Without this, those ids would hit the origin forever.
	pokemonSpriteMissing = map[string]time.Time{}

	// pokemonSpriteSlots bounds how many fetches are in flight at once.
	//
	// The shiny dex is 1025 cards. On a cold cache that page asks this endpoint for a
	// thousand files in one go, and without a bound this would open a thousand sockets to
	// GitHub simultaneously, which is the fastest possible way to get rate limited by the
	// host every sprite on the site depends on. Requests queue instead. Lazy loading means
	// most of them never arrive at all.
	pokemonSpriteSlots = make(chan struct{}, 8)
)

// pokemonSpriteMissTTL is how long a 404 is remembered. Long enough to stop the repeat
// traffic, short enough that art added upstream appears the same day.
const pokemonSpriteMissTTL = 24 * time.Hour

// pokemonSpriteMissCap bounds the negative cache.
//
// Real misses number in the dozens: there are only so many ids PokeAPI has no art for. This
// cap is not for them, it is for someone walking the allowed name space to grow the map. Far
// above any honest working set, so reaching it means something is wrong and dropping the lot
// costs a handful of refetches.
const pokemonSpriteMissCap = 4096

// pngMagic is the eight byte PNG signature.
var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// rememberPokemonSpriteMiss records a 404, clearing the map wholesale if it has grown past
// anything an honest workload would produce. Wholesale rather than an eviction policy on
// purpose: an LRU here would be machinery guarding a map that should never be large.
func rememberPokemonSpriteMiss(key string) {
	pokemonSpriteMu.Lock()
	defer pokemonSpriteMu.Unlock()
	if len(pokemonSpriteMissing) >= pokemonSpriteMissCap {
		pokemonSpriteMissing = make(map[string]time.Time, pokemonSpriteMissCap)
	}
	pokemonSpriteMissing[key] = time.Now()
}

// PokemonSpriteProxy serves a Pokemon sprite from our own origin, caching it in memory and
// on disk on the way through.
//
// shiny selects the upstream directory and keeps the two caches apart: 25.png and
// shiny/25.png are different pictures under the same file name.
func PokemonSpriteProxy(cacheDir string, shiny bool) http.HandlerFunc {
	dir := filepath.Join(cacheDir, "pokemon-sprites")
	if shiny {
		dir = filepath.Join(dir, "shiny")
	}
	os.MkdirAll(dir, 0o755)

	// The cache key has to carry the variant, or a shiny would be served for its own
	// normal form and the disk tier would be read across directories.
	keyPrefix := ""
	if shiny {
		keyPrefix = "shiny/"
	}

	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(chi.URLParam(r, "file"))
		if !pokemonSpriteAllowed(name) {
			http.NotFound(w, r)
			return
		}
		key := keyPrefix + name

		if data, ok := pokemonSpriteFromCache(dir, key, name); ok {
			writePokemonSprite(w, data)
			return
		}
		if pokemonSpriteIsMissing(key) {
			http.NotFound(w, r)
			return
		}

		// Wait for a slot, but not past the point where the client still wants it. A
		// browser that navigated away must not hold one of the eight.
		select {
		case pokemonSpriteSlots <- struct{}{}:
			defer func() { <-pokemonSpriteSlots }()
		case <-r.Context().Done():
			return
		}

		// Re-check after queueing. On a cold shiny dex a caller can sit here for seconds
		// while the sprite it wants is fetched by whoever was ahead of it, and answering
		// from the cache is both faster and one less request to the origin.
		if data, ok := pokemonSpriteFromCache(dir, key, name); ok {
			writePokemonSprite(w, data)
			return
		}

		upstream := pokemonSpriteOrigin + name
		if shiny {
			upstream = pokemonSpriteOrigin + "shiny/" + name
		}
		resp, err := pokemonSpriteHTTP.Get(upstream)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			// A real answer, not a failure: PokeAPI has no art for this id. Remember it.
			rememberPokemonSpriteMiss(key)
			http.NotFound(w, r)
			return
		}
		if resp.StatusCode != http.StatusOK {
			// Deliberately NOT cached as missing. A 403 is a rate limit and a 500 is
			// theirs; remembering either would turn a passing outage into a day of
			// blank sprites.
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		// One past the cap, so a file that exceeds it is REJECTED rather than silently
		// stored truncated. io.LimitReader reports no error on truncation, so reading
		// exactly maxPokemonSprite would cache a corrupt image permanently.
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxPokemonSprite+1))
		if err != nil || len(data) == 0 || len(data) > maxPokemonSprite {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		// The positive tier has no TTL and survives deploys, so anything stored here is
		// stored forever and clearing it means a shell on the VPS. A 200 carrying an error
		// page rather than an image is exactly the passing upstream problem the negative
		// cache's 24 hour TTL exists to avoid making permanent, so check that this is
		// actually a PNG before believing it.
		if !bytes.HasPrefix(data, pngMagic) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}

		os.WriteFile(filepath.Join(dir, name), data, 0o644)
		pokemonSpriteMu.Lock()
		pokemonSpriteCache[key] = data
		pokemonSpriteMu.Unlock()

		writePokemonSprite(w, data)
	}
}

// pokemonSpriteFromCache checks memory, then disk, promoting a disk hit into memory.
//
// Disk is the durable tier and the reason a deploy is not followed by a thousand refetches:
// cache/ is never touched by deploy.ps1, so the whole set survives a release. Memory is
// there so the common case does not touch the filesystem at all.
func pokemonSpriteFromCache(dir, key, name string) ([]byte, bool) {
	pokemonSpriteMu.RLock()
	data, ok := pokemonSpriteCache[key]
	pokemonSpriteMu.RUnlock()
	if ok {
		return data, true
	}

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	pokemonSpriteMu.Lock()
	pokemonSpriteCache[key] = data
	pokemonSpriteMu.Unlock()
	return data, true
}

// pokemonSpriteIsMissing reports whether this key 404ed recently enough to answer from
// memory rather than asking the origin again.
func pokemonSpriteIsMissing(key string) bool {
	pokemonSpriteMu.RLock()
	at, ok := pokemonSpriteMissing[key]
	pokemonSpriteMu.RUnlock()
	return ok && time.Since(at) < pokemonSpriteMissTTL
}

func writePokemonSprite(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	// 30 days, and not immutable: see pokemonSpriteOrigin. Upstream says 5 minutes.
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	w.Write(data)
}
