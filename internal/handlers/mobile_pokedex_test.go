package handlers

import (
	"encoding/json"
	"testing"

	"pogo.hails.cc/internal/pogodata"
)

// pokedexTestPayload is a reduction in the shape internal/pogodata builds, with
// German short one field so the per-field English fallback is visible.
const pokedexTestPayload = `{
	"dex": [1, 150, 9999],
	"flags": {"150": {"legendary": true}},
	"text": {
		"en": {
			"1":   {"genus": "Seed Pokémon",    "flavor": "A strange seed was planted on its back."},
			"150": {"genus": "Genetic Pokémon", "flavor": "Created by a scientist."}
		},
		"de": {
			"1":   {"genus": "Samen-Pokémon", "flavor": "Es trägt einen Samen auf dem Rücken."},
			"150": {"flavor": "Von einem Wissenschaftler erschaffen."}
		}
	}
}`

func pokedexTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	// The cache is package level, so a test that leaves a previous test's bodies in
	// it would pass for the wrong reason.
	pokedexBodyCache = pokedexBodies{byLang: map[string]pokedexBody{}}

	store := pogodata.New()
	if !store.ApplyPokedexSpecies(json.RawMessage(pokedexTestPayload)) {
		t.Fatal("ApplyPokedexSpecies rejected the fixture")
	}
	return &Handlers{store: store}
}

func decodePokedexBody(t *testing.T, body []byte) mobilePokedexResponse {
	t.Helper()
	var got mobilePokedexResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not the documented shape: %v (%s)", err, body)
	}
	return got
}

// The whole set is one URL whose body depends on the caller's account language, so
// the cache MUST key on language and the version must differ between them. Sharing
// one version across languages would tell a trainer who had just switched language
// that their cached copy was still current, and they would keep reading the old one
// until the next upstream refresh, up to six hours later.
func TestPokedexBytesPerLanguage(t *testing.T) {
	h := pokedexTestHandlers(t)

	enBody, enETag := h.pokedexBytes("en")
	deBody, deETag := h.pokedexBytes("de")
	if len(enBody) == 0 || len(deBody) == 0 {
		t.Fatal("pokedexBytes returned nothing for a populated store")
	}
	if enETag == deETag {
		t.Errorf("English and German share an ETag (%s), so a language switch would not refetch", enETag)
	}

	en, de := decodePokedexBody(t, enBody), decodePokedexBody(t, deBody)
	if en.Version == de.Version {
		t.Errorf("English and German share a version (%s)", en.Version)
	}
	if `"`+en.Version+`"` != enETag {
		t.Errorf("ETag %s does not quote the body's own version %q", enETag, en.Version)
	}
	if de.Species[1].Genus != "Samen-Pokémon" {
		t.Errorf("German genus = %q, want the German one", de.Species[1].Genus)
	}
	// Per field: Mewtwo has no German genus in the fixture, so that one field falls
	// back while its German flavour text stays German.
	if de.Species[150].Genus != "Genetic Pokémon" {
		t.Errorf("German Mewtwo genus = %q, want the English fallback", de.Species[150].Genus)
	}
	if de.Species[150].Flavor == en.Species[150].Flavor {
		t.Errorf("German Mewtwo flavour fell back to English when it had its own")
	}

	// Every species is present, blank ones included, and the flags ride along.
	if len(en.Species) != 3 {
		t.Errorf("English set has %d species, want 3 including the blank one", len(en.Species))
	}
	if got := en.Species[9999]; got.Genus != "" || got.Flavor != "" {
		t.Errorf("a species with no text = %+v, want a blank entry", got)
	}
	if !en.Species[150].Legendary || en.Species[1].Legendary {
		t.Errorf("legendary flags did not survive: 150=%v 1=%v", en.Species[150].Legendary, en.Species[1].Legendary)
	}
}

// A repeat call must return the cached bytes rather than rebuilding, and a rebuild
// must not change the answer for a store that has not moved.
func TestPokedexBytesStable(t *testing.T) {
	h := pokedexTestHandlers(t)

	first, firstETag := h.pokedexBytes("en")
	second, secondETag := h.pokedexBytes("en")
	if firstETag != secondETag || string(first) != string(second) {
		t.Errorf("two calls disagreed: %s vs %s", firstETag, secondETag)
	}
}

// An empty store is the 503 precondition. Answering an empty set instead would
// render as "none of these 1,025 species has an entry", which is a wrong answer
// rather than a missing one.
func TestPokedexBytesEmptyStore(t *testing.T) {
	pokedexBodyCache = pokedexBodies{byLang: map[string]pokedexBody{}}
	h := &Handlers{store: pogodata.New()}

	if body, etag := h.pokedexBytes("en"); len(body) != 0 || etag != "" {
		t.Errorf("empty store produced a body (%d bytes, etag %s)", len(body), etag)
	}
}

// The single species route folds the dex number in beside the entry's own fields
// rather than nesting it, which is the shape the app decodes.
func TestPokedexOneIsFlat(t *testing.T) {
	body, err := json.Marshal(mobilePokedexOne{
		Dex:          150,
		PokedexEntry: pogodata.PokedexEntry{Genus: "Genetic Pokémon", Flavor: "Created.", Legendary: true},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var flat map[string]any
	if err := json.Unmarshal(body, &flat); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"dex", "genus", "flavor", "legendary", "mythical"} {
		if _, ok := flat[key]; !ok {
			t.Errorf("key %q missing from %s", key, body)
		}
	}
}
