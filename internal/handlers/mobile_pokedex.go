package handlers

// Pokedex species text for the mobile app: the genus, the flavour text, and the
// legendary and mythical flags, for every species at once.
//
// The website fetches these one species at a time from pokeapi.co in the browser
// (ts/shared/pokedex.ts) and the app ported that faithfully, which put a request
// carrying the dex number a trainer had just tapped on a third party host, from
// the trainer's own IP, for the only screen in the app that talks to anyone but
// this site. The reduction behind this endpoint lives in internal/pogodata; this
// file is only the join between it and a request's language.
//
// Whole set rather than per species on purpose. It is roughly 1,025 short strings,
// a few hundred KB before middleware.Compress, and it answers a 304 to a client
// that already has it. One cacheable call beats 1,025 lazy ones, and it is the
// difference between a detail sheet that works on a train and one that is blank
// until the device is online.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// mobilePokedexResponse is the whole set.
type mobilePokedexResponse struct {
	// Version is a content hash of Species, so a client can use it as the cache key
	// for its own on-disk copy without hashing anything. It is also the ETag's
	// basis. Per LANGUAGE, not per store refresh: the payload differs by language
	// at one fixed URL, so a version shared across languages would tell a client
	// that had just switched language that it was already up to date.
	Version string `json:"version"`
	// Species is keyed by dex number. Every species upstream knows is present, even
	// one with nothing to say: a blank entry reads as "nothing to show" where an
	// absent one reads as "ask again later", and the app should not have to guess.
	Species map[int]pogodata.PokedexEntry `json:"species"`
}

// mobilePokedexOne is a single species, with its dex number folded in so the
// response identifies itself.
type mobilePokedexOne struct {
	Dex int `json:"dex"`
	pogodata.PokedexEntry
}

// pokedexBodies caches the marshalled response per language.
//
// Keyed on the store's own content hash rather than bounded by a TTL. The data
// behind it changes at most once every six hours and only when upstream ships a
// generation, so a TTL would be a rebuild on a timer for a payload that had not
// moved. Comparing hashes rebuilds when the answer actually changes and never
// otherwise, which is the same reason the version is a hash and not a clock.
type pokedexBodies struct {
	mu      sync.Mutex
	version string // the store version these were built from
	byLang  map[string]pokedexBody
}

type pokedexBody struct {
	body []byte
	etag string
}

var pokedexBodyCache = pokedexBodies{byLang: map[string]pokedexBody{}}

// MobilePokedex serves every species' text in the caller's language.
func (h *Handlers) MobilePokedex(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUserAPI(w, r); !ok {
		return
	}

	// detectLang reads the user's own lang column first, which is the right source
	// for a Bearer client: it sends no cookie, so a language cookie is not available
	// to fall back on and the account setting is the only thing that knows.
	body, etag := h.pokedexBytes(h.detectLang(r))
	if len(body) == 0 {
		// Nothing fetched yet. A 503 rather than an empty set, for the reason
		// MobileShinyDex gives: an empty payload renders as "this species has no
		// entry" for all 1,025 of them, which is a wrong answer rather than a
		// missing one. The store fills this in within seconds of boot and keeps a
		// disk cache across restarts, so this is a genuinely narrow window.
		writeJSONError(w, "pokedex unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("ETag", etag)
	// private because the body depends on the caller's account language at a URL
	// that does not mention it, so no shared cache may keep one trainer's copy for
	// another's request. no-cache rather than no-store because the CLIENT should
	// keep it and revalidate: that is what makes a launch cost a few hundred bytes.
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// MobilePokedexSpecies serves one species, for a client that wants a single entry
// without the set.
func (h *Handlers) MobilePokedexSpecies(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUserAPI(w, r); !ok {
		return
	}
	dex, err := strconv.Atoi(chi.URLParam(r, "dex"))
	if err != nil || dex <= 0 {
		writeJSONError(w, "bad dex number", http.StatusBadRequest)
		return
	}
	if h.store.PokedexSize() == 0 {
		writeJSONError(w, "pokedex unavailable", http.StatusServiceUnavailable)
		return
	}
	entry, ok := h.store.PokedexSpeciesOne(dex, h.detectLang(r))
	if !ok {
		// A 404 only when upstream has no such species at all. A species that exists
		// and has no text is a blank entry and a 200, which is the rule the whole set
		// follows: a missing flavour text must never make a working screen look broken.
		writeJSONError(w, "unknown species", http.StatusNotFound)
		return
	}
	writeJSONWithETag(w, r, mobilePokedexOne{Dex: dex, PokedexEntry: entry})
}

// pokedexBytes returns the marshalled response and its ETag for one language,
// rebuilding when the store's content hash has moved.
func (h *Handlers) pokedexBytes(lang string) ([]byte, string) {
	version := h.store.PokedexVersion()
	if version == "" {
		return nil, ""
	}

	c := &pokedexBodyCache
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.version != version {
		// A new reduction invalidates every language at once, since they all came out
		// of the one payload.
		c.version, c.byLang = version, map[string]pokedexBody{}
	}
	if cached, ok := c.byLang[lang]; ok {
		return cached.body, cached.etag
	}

	species := h.store.PokedexSpecies(lang)
	if len(species) == 0 {
		// Not cached: an empty build means the store has not loaded, and caching that
		// would hold the failure until the next refresh resolved it.
		return nil, ""
	}
	// Hashed over the species map alone, because the hash goes INSIDE the response
	// as Version and cannot be taken over bytes that contain it.
	inner, err := json.Marshal(species)
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(inner)
	resp := mobilePokedexResponse{Version: hex.EncodeToString(sum[:])[:16], Species: species}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, ""
	}
	entry := pokedexBody{body: body, etag: `"` + resp.Version + `"`}
	c.byLang[lang] = entry
	return entry.body, entry.etag
}
