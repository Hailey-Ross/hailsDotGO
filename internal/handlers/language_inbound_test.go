package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"pogo.hails.cc/internal/pogodata"
)

// Inbound species names in a language other than English.
//
// The store keys everything on the English name, so a name arriving from a
// translated UI or a localized game screen has to be folded back before it is
// stored or matched. These tests pin the two halves of that: a translated name is
// accepted and normalized, and a name that resolves to nothing is refused rather
// than written and left to rot.

// localizedStore is solverStore plus the translated names, so the resolver has
// something to invert. Real dex numbers, real Niantic spellings.
func localizedStore(t *testing.T) *pogodata.Store {
	t.Helper()
	poke, err := os.ReadFile("../pogodata/fallback/pokemon.json")
	if err != nil {
		t.Fatalf("read pokemon fallback: %v", err)
	}
	cpm, err := os.ReadFile("../pogodata/fallback/cp_multipliers.json")
	if err != nil {
		t.Fatalf("read cp_multipliers fallback: %v", err)
	}
	s := pogodata.New()
	s.ApplySolverData(poke, cpm)
	s.ApplySpeciesNames(json.RawMessage(`{
		"68":{"fr":"Mackogneur","de":"Machomei","es":"Machamp","ja":"カイリキー"},
		"105":{"fr":"Ossatueur","de":"Knogga","es":"Marowak","ja":"ガラガラ"},
		"104":{"fr":"Osselait","de":"Tragosso","es":"Cubone","ja":"カラカラ"},
		"6":{"fr":"Dracaufeu","de":"Glurak","es":"Charizard","ja":"リザードン"}
	}`))
	return s
}

func TestIVCalculateAcceptsALocalizedSpeciesName(t *testing.T) {
	t.Setenv("CACHE_DIR", t.TempDir())
	h := &Handlers{store: localizedStore(t)}

	// The same Machamp card in four languages. Every one has to solve, and the
	// echoed species has to come back English, because the app saves from the
	// echo rather than from what it sent.
	for _, name := range []string{"Machamp", "Mackogneur", "Machomei", "カイリキー", "mackogneur"} {
		body := `{"pokemon_name":"` + name + `","cp":1964,"hp":140,"dust_cost":3000,"trainer_level":46}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(body))
		h.IVCalculate(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%q: status = %d, want 200, body %s", name, w.Code, w.Body.String())
			continue
		}
		var got struct {
			Pokemon struct {
				PokemonName string `json:"pokemon_name"`
			} `json:"pokemon"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Errorf("%q: response is not JSON: %v", name, err)
			continue
		}
		if got.Pokemon.PokemonName != "Machamp" {
			t.Errorf("%q: echoed species = %q, want Machamp", name, got.Pokemon.PokemonName)
		}
	}
}

func TestIVCalculateStillRefusesAnUnknownSpecies(t *testing.T) {
	t.Setenv("CACHE_DIR", t.TempDir())
	h := &Handlers{store: localizedStore(t)}
	body := `{"pokemon_name":"Machompf","cp":1964,"hp":140,"dust_cost":3000,"trainer_level":46}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(body))
	h.IVCalculate(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body %s", w.Code, w.Body.String())
	}
}

func TestIVCalculateDoesNotConflateTheKanaPair(t *testing.T) {
	// カラカラ (Cubone) and ガラガラ (Marowak) differ only by the voicing mark.
	// An accent insensitive fold merges them, which would solve one as the other
	// against the wrong base stats.
	t.Setenv("CACHE_DIR", t.TempDir())
	h := &Handlers{store: localizedStore(t)}
	for name, want := range map[string]string{"カラカラ": "Cubone", "ガラガラ": "Marowak"} {
		body := `{"pokemon_name":"` + name + `","cp":1000,"hp":100,"dust_cost":3000,"trainer_level":46}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/iv/calculate", strings.NewReader(body))
		h.IVCalculate(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%q: status = %d, body %s", name, w.Code, w.Body.String())
			continue
		}
		var got struct {
			Pokemon struct {
				PokemonName string `json:"pokemon_name"`
			} `json:"pokemon"`
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.Pokemon.PokemonName != want {
			t.Errorf("%q resolved to %q, want %q", name, got.Pokemon.PokemonName, want)
		}
	}
}

// insertShiny's validation runs before it touches the database, so the refusals
// and the point at which a name has been accepted are both reachable with no db.
// A species that resolves falls through to the caught_at check below it, so an
// invalid date standing in for the insert proves the name was taken.

func TestInsertShinyRefusesAnUnknownSpeciesRetryably(t *testing.T) {
	// 422, NOT 400, and the app's offline queue is why. ShinyOutboxDrain takes a
	// 400 to mean the request is malformed and drops the catch out of the queue on
	// the first attempt; any other answered status is retried with backoff up to
	// six times. The cause here is drift between the app's bundled species table
	// and this process's, which a retry does fix, so the catch has to survive it.
	h := &Handlers{store: localizedStore(t)}
	_, status, key := h.insertShiny(1, shinyAddInput{PokemonID: "Machompf", Region: ""})
	if key != "error.shiny_pokemon_unknown" {
		t.Errorf("got key %q, want error.shiny_pokemon_unknown", key)
	}
	if status == http.StatusBadRequest {
		t.Fatal("400 makes the app discard the queued catch on the first try; this must be retryable")
	}
	if status != http.StatusUnprocessableEntity {
		t.Errorf("got status %d, want 422", status)
	}
}

func TestInsertShinyKeepsMalformedInputAt400(t *testing.T) {
	// The counterpart to the test above. A malformed body IS a client bug that no
	// retry fixes, so these must stay 400 and keep being dropped from the queue
	// rather than spinning at its head.
	h := &Handlers{store: localizedStore(t)}
	long := strings.Repeat("x", 200)
	cases := map[string]shinyAddInput{
		"empty species":  {PokemonID: "   "},
		"bad region":     {PokemonID: "Charizard", Region: "not-a-region"},
		"over long form": {PokemonID: "Charizard", Form: long},
		"bad token":      {PokemonID: "Charizard", ClientToken: "!"},
	}
	for name, in := range cases {
		_, status, _ := h.insertShiny(1, in)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, status)
		}
	}
}

func TestInsertShinyAcceptsALocalizedSpeciesName(t *testing.T) {
	h := &Handlers{store: localizedStore(t)}
	// A bad date refuses AFTER the species has been resolved, so reaching it is
	// the proof the name was accepted rather than rejected as unknown.
	for _, name := range []string{"Glurak", "Dracaufeu", "リザードン", "charizard"} {
		_, status, key := h.insertShiny(1, shinyAddInput{PokemonID: name, Region: "", CaughtAt: "not-a-date"})
		if key == "error.shiny_pokemon_unknown" {
			t.Errorf("%q was refused as unknown, want it resolved to Charizard", name)
			continue
		}
		if status != http.StatusBadRequest || key != "error.shiny_caught_at" {
			t.Errorf("%q: got status %d key %q, want the caught_at refusal below the name check", name, status, key)
		}
	}
}

func TestInsertShinyStillRefusesAnEmptySpecies(t *testing.T) {
	h := &Handlers{store: localizedStore(t)}
	_, status, key := h.insertShiny(1, shinyAddInput{PokemonID: "   ", Region: ""})
	if status != http.StatusBadRequest || key != "error.shiny_pokemon_required" {
		t.Errorf("got status %d key %q, want 400 error.shiny_pokemon_required", status, key)
	}
}

func TestInsertShinyCapsTheFieldsItStores(t *testing.T) {
	h := &Handlers{store: localizedStore(t)}
	long := strings.Repeat("ガ", 200) // 200 characters, 600 bytes
	cases := []struct {
		name string
		in   shinyAddInput
	}{
		{"form", shinyAddInput{PokemonID: "Charizard", Form: long}},
		{"costume", shinyAddInput{PokemonID: "Charizard", Costume: long}},
		{"event_tag", shinyAddInput{PokemonID: "Charizard", EventTag: long}},
		{"method", shinyAddInput{PokemonID: "Charizard", Method: long}},
	}
	for _, c := range cases {
		_, status, _ := h.insertShiny(1, c.in)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 rather than a 500 off the column", c.name, status)
		}
	}
}

func TestInsertShinyFailsOpenWhenNoSpeciesAreLoaded(t *testing.T) {
	// A store whose embedded fallback did not parse must not refuse every
	// trainer's catch. Refusing an unresolvable name is a judgment about the
	// input, and with no species loaded there is no basis for it.
	h := &Handlers{store: pogodata.New()}
	_, _, key := h.insertShiny(1, shinyAddInput{PokemonID: "Charizard", CaughtAt: "not-a-date"})
	if key == "error.shiny_pokemon_unknown" {
		t.Error("an empty store refused a name; the check must fail open")
	}
}

func TestBoxFormPatternAcceptsRealFormNames(t *testing.T) {
	ok := []string{
		"Normal", "Alola", "Galarian", "Therian", "Paldea Combat",
		"Pa'u",           // Oricorio, refused outright by the old ASCII shape
		"d'Alola",        // the French regional label
		"アローラのすがた",       // the Japanese one
		"Alola-Form", "", // the German shape, and absent
	}
	for _, f := range ok {
		if !boxFormPattern.MatchString(f) {
			t.Errorf("boxFormPattern refused %q, which is a real form name", f)
		}
	}
	bad := []string{
		`<svg onload=a()>`,
		`" onmouseover="x`,
		"a&b",
		strings.Repeat("x", 65),
	}
	for _, f := range bad {
		if boxFormPattern.MatchString(f) {
			t.Errorf("boxFormPattern accepted %q", f)
		}
	}
}

func TestCanonicalBossNameFoldsTheMatchmakingKey(t *testing.T) {
	// boss_name is the key raid_queue is joined to raid_lobbies on, so two
	// languages naming one boss have to land on one string.
	h := &Handlers{store: localizedStore(t)}
	cases := []struct{ in, want string }{
		{"Charizard", "Charizard"},
		{"Glurak", "Charizard"},
		{"Dracaufeu", "Charizard"},
		{"リザードン", "Charizard"},
		// The variant prefix is split off and put back, since the species table
		// knows nothing about "Shadow".
		{"Shadow Glurak", "Shadow Charizard"},
		{"Mega Dracaufeu", "Mega Charizard"},
		{"shadow glurak", "Shadow Charizard"},
		// A genuinely custom boss is left exactly as it arrived.
		{"Unlisted event raid", "Unlisted event raid"},
		{"  Glurak  ", "Charizard"},
	}
	for _, c := range cases {
		if got := h.canonicalBossName(c.in); got != c.want {
			t.Errorf("canonicalBossName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
