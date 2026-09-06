package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The allowlist is the only thing standing between this endpoint and an open proxy, so it
// is tested from both directions: everything PokeAPI actually files a sprite under has to
// pass, and anything that could name a different resource has to fail.
func TestPokemonSpriteAllowlist(t *testing.T) {
	accept := []string{
		"25.png",              // Pikachu
		"1.png",               // lowest dex
		"10188.png",           // Crowned Sword Zacian, a variant id
		"201.png",             // Unown A, which IS the default form
		"201-b.png",           // an Unown letter, filed as a pokemon-form record
		"201-exclamation.png", // and its punctuation glyphs
		"201-question.png",
		"666.png",             // Meadow Vivillon
		"666-poke-ball.png",   // a two word pattern
		"666-high-plains.png", // and another
		"666-icy-snow.png",
	}
	for _, name := range accept {
		if !pokemonSpriteAllowed(name) {
			t.Errorf("allowlist rejected a real sprite name: %q", name)
		}
	}

	reject := []string{
		"",
		".png",
		"25",          // no extension
		"25.PNG",      // the origin is case sensitive; so is this
		"25.jpg",      // only PNG is served
		"../25.png",   // traversal
		"..%2F25.png", // encoded traversal
		"a.png",       // must start with digits
		"25-B.png",    // upstream slugs are lowercase
		"25_b.png",    // and hyphenated, not underscored
		"25-.png",     // a hyphen must lead somewhere
		"123456.png",  // six digits: beyond any id upstream files
		"1234567.png", // and further still

		// The grammar used to be `^[0-9]{1,6}(-[a-z0-9]+)*\.png$`, which admits an
		// INFINITE set of names. Each novel one cost a real upstream request and a
		// permanent negative cache entry, from an anonymous unrated route. The hyphenated
		// half is a fixed set derived from the slug tables now, so these are refused.
		"1-a.png",
		"1-a-a.png",
		"1-zzzzzzzzzzzzzzzzzzzz.png",
		"666-not-a-pattern.png",
		"201-zz.png",
		"25.png/../../etc/passwd",
		"25.png?x=1",
		"shiny/25.png", // the variant is chosen by the route, never by the filename
	}
	for _, name := range reject {
		if pokemonSpriteAllowed(name) {
			t.Errorf("allowlist accepted something it must not: %q", name)
		}
	}
}

// Serves from the disk tier, which is also how this runs with no network at all.
//
// Disk is the durable tier and the reason a deploy does not re-fetch a thousand files, so
// "a file that is already on disk is served without asking upstream" is the property worth
// pinning rather than an implementation detail.
func TestPokemonSpriteServesFromDiskCache(t *testing.T) {
	cacheDir := t.TempDir()
	want := []byte("\x89PNG\r\n\x1a\n-normal")
	wantShiny := []byte("\x89PNG\r\n\x1a\n-shiny")

	if err := os.MkdirAll(filepath.Join(cacheDir, "pokemon-sprites", "shiny"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "pokemon-sprites", "10188.png"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "pokemon-sprites", "shiny", "10188.png"), wantShiny, 0o644); err != nil {
		t.Fatal(err)
	}

	// The memory tier is a package level map shared by every instance, so a key left by
	// another test would mask a disk read. Use an id nothing else touches and clear it.
	pokemonSpriteMu.Lock()
	delete(pokemonSpriteCache, "10188.png")
	delete(pokemonSpriteCache, "shiny/10188.png")
	pokemonSpriteMu.Unlock()

	r := chi.NewRouter()
	r.Get("/api/pokemon-sprite/{file}", PokemonSpriteProxy(cacheDir, false))
	r.Get("/api/pokemon-sprite/shiny/{file}", PokemonSpriteProxy(cacheDir, true))

	for _, tc := range []struct {
		path string
		want []byte
	}{
		// The shiny and the normal form share a file name and are different pictures.
		// Serving one for the other is the failure this pair exists to catch.
		{"/api/pokemon-sprite/10188.png", want},
		{"/api/pokemon-sprite/shiny/10188.png", wantShiny},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
		}
		if got := rec.Body.String(); got != string(tc.want) {
			t.Errorf("GET %s served %q, want %q", tc.path, got, tc.want)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("GET %s Content-Type = %q, want image/png", tc.path, ct)
		}
		// Thirty days, against upstream's five minutes, which is the whole point of
		// standing in front of it. Not immutable: we pin to master, not to a commit.
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, "max-age=2592000") {
			t.Errorf("GET %s Cache-Control = %q, want a 30 day max-age", tc.path, cc)
		}
		if strings.Contains(cc, "immutable") {
			t.Errorf("GET %s claims immutable, but the origin is pinned to a branch: %q", tc.path, cc)
		}
	}

	// A name the allowlist refuses must not reach the origin or the filesystem. If this
	// regressed the endpoint would fetch arbitrary paths from GitHub on demand.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/pokemon-sprite/nope.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a rejected name answered %d, want 404", rec.Code)
	}
}

// Every Pokemon picture in the app and on the site is built from spriteURLSlug, so this is
// the one function that decides whether a trainer's device talks to a third party to draw a
// Pokemon. A regression here is silent: the images still load.
func TestSpriteURLSlugUsesOurProxy(t *testing.T) {
	cases := []struct{ slug, form, want string }{
		{"25", "", "/api/pokemon-sprite/25.png"},
		{"25", "shiny", "/api/pokemon-sprite/shiny/25.png"},
		{"10188", "", "/api/pokemon-sprite/10188.png"},
		{"201-b", "shiny", "/api/pokemon-sprite/shiny/201-b.png"},
		{"666-poke-ball", "shiny", "/api/pokemon-sprite/shiny/666-poke-ball.png"},
		{"", "shiny", ""}, // no slug, no URL
	}
	for _, c := range cases {
		got := spriteURLSlug(c.slug, c.form)
		if got != c.want {
			t.Errorf("spriteURLSlug(%q, %q) = %q, want %q", c.slug, c.form, got, c.want)
		}
		if strings.Contains(got, "githubusercontent.com") {
			t.Errorf("spriteURLSlug(%q, %q) still hotlinks: %s", c.slug, c.form, got)
		}
	}

	// Whatever it produces must be a path the proxy will actually serve, or the site
	// renders broken images while every test above still passes.
	for _, slug := range []string{"25", "10188", "201-b", "201-exclamation", "666-poke-ball"} {
		for _, form := range []string{"", "shiny"} {
			url := spriteURLSlug(slug, form)
			file := url[strings.LastIndex(url, "/")+1:]
			if !pokemonSpriteAllowed(file) {
				t.Errorf("spriteURLSlug(%q, %q) produced %q, which the proxy allowlist rejects", slug, form, url)
			}
		}
	}
}

// users.fav_sprite_url is the only place a sprite URL was ever written to the database, so
// rows saved before the proxy existed still hold a hotlink and nothing else would ever
// rewrite them.
func TestNormalizePokemonSpriteURL(t *testing.T) {
	const legacy = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/"
	cases := []struct{ in, want string }{
		{legacy + "25.png", "/api/pokemon-sprite/25.png"},
		{legacy + "shiny/25.png", "/api/pokemon-sprite/shiny/25.png"},
		{legacy + "10007.png", "/api/pokemon-sprite/10007.png"},

		// Already ours, or nothing at all, or somebody else's entirely: left alone.
		{"/api/pokemon-sprite/25.png", "/api/pokemon-sprite/25.png"},
		{"", ""},
		{"/api/costume-sprite/pm25.fANNIVERSARY.icon.png", "/api/costume-sprite/pm25.fANNIVERSARY.icon.png"},
		{"https://example.com/25.png", "https://example.com/25.png"},
	}
	for _, c := range cases {
		if got := normalizePokemonSpriteURL(c.in); got != c.want {
			t.Errorf("normalizePokemonSpriteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// The rewrite has to land on something the proxy serves, not merely on a shorter
	// string. A legacy row for an Unown letter is the awkward case.
	got := normalizePokemonSpriteURL(legacy + "shiny/201-exclamation.png")
	if !pokemonSpriteAllowed(got[strings.LastIndex(got, "/")+1:]) {
		t.Errorf("healed URL %q is not one the proxy allowlist accepts", got)
	}
}
