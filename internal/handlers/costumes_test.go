package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"pogo.hails.cc/internal/pogodata"
)

// costumeStore is a store with the real species list behind it, which is all MobileCostumes
// needs: it walks PokemonList and asks internal/costumes about each dex.
func costumeStore(t *testing.T) *pogodata.Store {
	t.Helper()
	poke, err := os.ReadFile("../pogodata/fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read pokemon fallback: %v", err)
	}
	s := pogodata.New()
	s.ApplySolverData(poke, nil)
	return s
}

// TestMobileCostumesRevalidates is the guard on the app's refresh window.
//
// The app polled this endpoint on a 6 hour timer for one reason: with no validator, every check
// cost the whole catalog, so checking often was expensive. That window is also how long a costume
// named in the admin panel stays invisible to trainers. A 304 makes a frequent check nearly free,
// so the window can close, and this asserts the 304 actually happens.
func TestMobileCostumesRevalidates(t *testing.T) {
	h := &Handlers{store: costumeStore(t)}

	first := httptest.NewRecorder()
	h.MobileCostumes(first, httptest.NewRequest(http.MethodGet, "/api/mobile/v1/costumes", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first fetch answered %d, want 200", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag, so the app has nothing to revalidate with")
	}
	if got := first.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}

	// The body still has to be the catalog. An endpoint that answers 304 to everything would
	// pass the assertion above and break the picker.
	var payload struct {
		SpriteBase string                       `json:"sprite_base"`
		Species    map[string][]json.RawMessage `json:"species"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not the catalog: %v", err)
	}
	if payload.SpriteBase == "" || len(payload.Species) == 0 {
		t.Fatalf("empty catalog: base %q, %d species", payload.SpriteBase, len(payload.Species))
	}

	r := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/costumes", nil)
	r.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	h.MobileCostumes(second, r)
	if second.Code != http.StatusNotModified {
		t.Errorf("unchanged catalog answered %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried a body of %d bytes", second.Body.Len())
	}
}
