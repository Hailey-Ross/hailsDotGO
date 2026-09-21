package pogodata

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFoldNameDropsLatinAccentsAndPunctuation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// The game draws Flabébé; our own table stores Flabebe. Both must fold
		// together or the species is unreachable from a screenshot in any language.
		{"Flabébé", "flabebe"},
		{"Flabebe", "flabebe"},
		{"FLABEBE", "flabebe"},
		// The curly apostrophe upstream actually ships, and the straight one a
		// keyboard produces.
		{"Farfetch’d", "farfetchd"},
		{"Farfetch'd", "farfetchd"},
		{"Mr. Mime", "mrmime"},
		{"Mime Jr.", "mimejr"},
		{"Type: Null", "typenull"},
		{"Ho-Oh", "hooh"},
		{"Porygon-Z", "porygonz"},
		{"  Pikachu  ", "pikachu"},
		// Full width, which NFKC folds before anything else runs.
		{"Ｐｉｋａｃｈｕ", "pikachu"},
	}
	for _, c := range cases {
		if got := foldName(c.in); got != c.want {
			t.Errorf("foldName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFoldNameKeepsDigits(t *testing.T) {
	// Dropping digits is what made an OCR read of Porygon2 resolve to Porygon and
	// get solved against the wrong base stats, silently.
	if foldName("Porygon2") == foldName("Porygon") {
		t.Fatal("Porygon2 folds onto Porygon, so a Porygon2 would be solved as a Porygon")
	}
	if got := foldName("Porygon2"); got != "porygon2" {
		t.Errorf("foldName(Porygon2) = %q, want %q", got, "porygon2")
	}
}

func TestFoldNameKeepsJapaneseVoicingMarks(t *testing.T) {
	// Dakuten is the whole difference between these pairs. The usual one line
	// "NFD then strip every combining mark" recipe merges them, which would make
	// a Cubone and a Marowak the same species.
	pairs := [][2]string{
		{"カラカラ", "ガラガラ"}, // Cubone / Marowak
		{"コース", "ゴース"},   // (Gastly's name differs from this by the same mark)
		{"ハトーボー", "バトーボー"},
	}
	for _, p := range pairs {
		if foldName(p[0]) == foldName(p[1]) {
			t.Errorf("foldName merged %q and %q into %q; dakuten must survive", p[0], p[1], foldName(p[0]))
		}
	}
	// Half width katakana must still reach its normal form.
	if foldName("ﾋﾟｶﾁｭｳ") != foldName("ピカチュウ") {
		t.Errorf("half width katakana did not fold onto full width: %q vs %q",
			foldName("ﾋﾟｶﾁｭｳ"), foldName("ピカチュウ"))
	}
}

// resolverStore builds a store with a small species list and translations, the
// same shapes applyResult accepts in production.
func resolverStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{}
	pokemon := json.RawMessage(`[
		{"pokemon_name":"Bulbasaur","pokemon_id":1},
		{"pokemon_name":"Charizard","pokemon_id":6},
		{"pokemon_name":"Pikachu","pokemon_id":25},
		{"pokemon_name":"Cubone","pokemon_id":104},
		{"pokemon_name":"Marowak","pokemon_id":105},
		{"pokemon_name":"Porygon","pokemon_id":137},
		{"pokemon_name":"Porygon2","pokemon_id":233},
		{"pokemon_name":"Flabebe","pokemon_id":669},
		{"pokemon_name":"Nidoran♀","pokemon_id":29},
		{"pokemon_name":"Nidoran♂","pokemon_id":32}
	]`)
	names := json.RawMessage(`{
		"1":{"fr":"Bulbizarre","de":"Bisasam","es":"Bulbasaur","ja":"フシギダネ"},
		"6":{"fr":"Dracaufeu","de":"Glurak","es":"Charizard","ja":"リザードン"},
		"25":{"fr":"Pikachu","de":"Pikachu","es":"Pikachu","ja":"ピカチュウ"},
		"104":{"fr":"Osselait","de":"Tragosso","es":"Cubone","ja":"カラカラ"},
		"105":{"fr":"Ossatueur","de":"Knogga","es":"Marowak","ja":"ガラガラ"},
		"669":{"fr":"Flabébé","de":"Flabébé","es":"Flabébé","ja":"フラベベ"}
	}`)
	s.mu.Lock()
	s.applyResult("pokemon", pokemon)
	s.applyResult("pokemon_names", names)
	s.mu.Unlock()
	if s.nameIdx == nil {
		t.Fatal("name index was not built")
	}
	return s
}

func TestResolveSpeciesAcceptsEveryLanguage(t *testing.T) {
	s := resolverStore(t)
	cases := []struct {
		in, prefer, want string
	}{
		// Tier 1: already English.
		{"Marowak", "", "Marowak"},
		{"Pikachu", "fr", "Pikachu"},
		// Tier 2 and 3: exact translated, hinted and unhinted.
		{"Ossatueur", "fr", "Marowak"},
		{"Ossatueur", "", "Marowak"},
		{"Knogga", "de", "Marowak"},
		{"Knogga", "", "Marowak"},
		{"Glurak", "", "Charizard"},
		{"Dracaufeu", "fr", "Charizard"},
		{"ガラガラ", "ja", "Marowak"},
		{"カラカラ", "ja", "Cubone"},
		{"ピカチュウ", "", "Pikachu"},
		{"Bulbizarre", "fr", "Bulbasaur"},
		{"フシギダネ", "", "Bulbasaur"},
		// A wrong hint must not stop the sweep from finding it.
		{"Knogga", "fr", "Marowak"},
		// Fold tiers: case, accents, whitespace.
		{"ossatueur", "", "Marowak"},
		{"  GLURAK  ", "", "Charizard"},
		{"Flabébé", "", "Flabebe"},
		{"flabebe", "", "Flabebe"},
		{"Porygon2", "", "Porygon2"},
		{"porygon", "", "Porygon"},
	}
	for _, c := range cases {
		got, ok := s.ResolveSpecies(c.in, c.prefer)
		if !ok {
			t.Errorf("ResolveSpecies(%q, %q) did not resolve, want %q", c.in, c.prefer, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveSpecies(%q, %q) = %q, want %q", c.in, c.prefer, got, c.want)
		}
	}
}

func TestResolveSpeciesRefusesJunk(t *testing.T) {
	s := resolverStore(t)
	for _, in := range []string{"", "   ", "Glurakk", "not a pokemon", "12345", "???"} {
		if got, ok := s.ResolveSpecies(in, ""); ok {
			t.Errorf("ResolveSpecies(%q) resolved to %q, want no match", in, got)
		}
	}
}

func TestResolveSpeciesRefusesAmbiguousFold(t *testing.T) {
	s := resolverStore(t)
	// Both Nidoran forms fold to "nidoran" once the gender sign is dropped.
	// Guessing one is worse than declining, so the fold tiers must refuse.
	if got, ok := s.ResolveSpecies("Nidoran", ""); ok {
		t.Errorf("ResolveSpecies(Nidoran) = %q, want a refusal: the fold is ambiguous", got)
	}
	// The exact spellings still resolve, because tier 1 never consults the fold.
	for _, exact := range []string{"Nidoran♀", "Nidoran♂"} {
		if got, ok := s.ResolveSpecies(exact, ""); !ok || got != exact {
			t.Errorf("ResolveSpecies(%q) = %q, %v; want the name itself", exact, got, ok)
		}
	}
}

func TestResolveSpeciesEmptyStore(t *testing.T) {
	// A store that has not loaded yet must answer "no", not panic.
	s := &Store{}
	if _, ok := s.ResolveSpecies("Pikachu", "fr"); ok {
		t.Error("an empty store resolved a name")
	}
}

func TestResolveDexID(t *testing.T) {
	s := resolverStore(t)
	if got := s.ResolveDexID("Ossatueur", "fr"); got != 105 {
		t.Errorf("ResolveDexID(Ossatueur) = %d, want 105", got)
	}
	if got := s.ResolveDexID("ガラガラ", ""); got != 105 {
		t.Errorf("ResolveDexID(ガラガラ) = %d, want 105", got)
	}
	if got := s.ResolveDexID("nonsense", ""); got != 0 {
		t.Errorf("ResolveDexID(nonsense) = %d, want 0", got)
	}
}

func TestRebuildNameIndexPrefersBaselineNameForSharedDex(t *testing.T) {
	// The stats feed and the shiny baseline can both claim a dex under different
	// names. The translated table is keyed by dex alone, so it cannot tell them
	// apart; the baseline's spelling is the one a translated name should resolve to.
	s := &Store{}
	s.mu.Lock()
	s.applyResult("pokemon", json.RawMessage(`[{"pokemon_name":"Pikachu","pokemon_id":25}]`))
	s.applyResult("pokemon_names", json.RawMessage(`{"25":{"de":"Pikachu","ja":"ピカチュウ"}}`))
	s.mu.Unlock()
	if got, ok := s.ResolveSpecies("ピカチュウ", "ja"); !ok || got != "Pikachu" {
		t.Errorf("ResolveSpecies = %q, %v; want Pikachu", got, ok)
	}
}

func TestResolveSpeciesUnderConcurrentRebuild(t *testing.T) {
	// ResolveSpecies takes RLock, and ResolveDexID calls it and then takes RLock
	// again. Those are sequential rather than nested, but a reader that re-entered
	// the lock while a writer was queued would deadlock rather than race, and the
	// race detector needs cgo which is not always available. This catches the
	// deadlock, which is the failure that layering can actually produce.
	s := resolverStore(t)
	names := json.RawMessage(`{"105":{"fr":"Ossatueur","de":"Knogga","ja":"ガラガラ"}}`)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.ApplySpeciesNames(names)
		}
	}()
	for i := 0; i < 200; i++ {
		s.ResolveSpecies("Ossatueur", "fr")
		s.ResolveDexID("ガラガラ", "ja")
		s.SpeciesLoaded()
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out: a reader and the rebuild deadlocked")
	}
}
