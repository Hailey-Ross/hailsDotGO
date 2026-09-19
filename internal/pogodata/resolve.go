package pogodata

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Resolving a species name that did not come from our own English table.
//
// The store keys everything on the English species name: pokemonIDMap, the shiny
// dex card key, user_shinies.pokemon_id, the box, the raid lobby boss. That stays
// true, and this file does not change it. What it adds is a way IN: a name a
// trainer typed, or a phone read off a localized game screen, resolved back to the
// canonical English name before anything is stored or looked up.
//
// The translated names are the ones already fetched into pokemonNamesById for
// display (fr, de, es, ja, from PokeAPI). Until now they were only ever written
// out. This inverts them.

// ambiguous marks an index entry that two different species claim. It is stored
// rather than deleted so a later insert cannot resurrect the key by overwriting
// a removed one, and so the lookup can tell "never seen" from "refuse to guess".
const ambiguous = "\x00ambiguous"

// foldName is the loose matching key: the form in which "Flabébé", "flabebe" and
// "FLABEBE" are the same string.
//
// The fold has to survive two opposite pressures. Latin diacritics are noise here,
// because the game draws "Flabébé" while our own table stores "Flabebe" and a
// trainer types whatever their keyboard gives them. Japanese combining marks are
// NOT noise: dakuten is the entire difference between カラカラ (Cubone) and
// ガラガラ (Marowak), and between コース and ゴース. Stripping every combining
// mark, which is the usual one line recipe, silently merges those pairs.
//
// So marks are dropped only when the base they attach to is Latin, and a mark
// kept over a non Latin base is preserved into the output rather than filtered out
// with the punctuation. For kana this is belt and braces, since NFC recomposes
// カ plus dakuten back into the single rune ガ before the filter ever sees a mark;
// it matters for a script whose marks have no precomposed form, which is what a
// fifth locale would likely bring.
//
// NFKC first folds the width variants, so a full width "Ｐｉｋａｃｈｕ" and a half
// width katakana "ﾋﾟｶﾁｭｳ" both land on their normal forms before any of that.
//
// Digits are kept. Dropping them is what made an OCR read of "Porygon2" resolve
// to Porygon and get solved against the wrong base stats.
func foldName(s string) string {
	s = norm.NFKC.String(strings.TrimSpace(s))

	// Decompose so Latin accents become base + mark, then drop only those marks.
	d := []rune(norm.NFD.String(s))
	kept := make([]rune, 0, len(d))
	lastBaseLatin := false
	for _, r := range d {
		if unicode.Is(unicode.Mn, r) {
			if lastBaseLatin {
				continue // é -> e
			}
			kept = append(kept, r) // カ + dakuten stays ガ
			continue
		}
		lastBaseLatin = unicode.Is(unicode.Latin, r)
		kept = append(kept, r)
	}

	// Recompose so the kana that kept their marks are single runes again, then
	// keep only what carries meaning: letters and digits.
	var b strings.Builder
	b.Grow(len(kept))
	for _, r := range norm.NFC.String(string(kept)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// FoldName exposes the loose matching key for callers that match against a list
// they already hold rather than against the store, notably the OCR species
// lookups working over the stat list. Same rules as foldName, documented there.
func FoldName(s string) string { return foldName(s) }

// nameIndex is the inbound half of the name tables, rebuilt whenever the species
// list or the translated names change. Every value is a canonical English name.
type nameIndex struct {
	foldEN      map[string]string            // fold(english) -> english
	exactByLang map[string]map[string]string // lang -> translated -> english
	foldByLang  map[string]map[string]string // lang -> fold(translated) -> english
	exactAny    map[string]string            // translated (any lang) -> english
	foldAny     map[string]string            // fold(translated, any lang) -> english
}

// put records name -> english, demoting the entry to ambiguous if some other
// species already claimed it. Two species that fold together are both refused
// rather than one of them winning at random: Nidoran♀ and Nidoran♂ both fold to
// "nidoran", and guessing a gender is worse than not answering.
func put(m map[string]string, key, english string) {
	if key == "" {
		return
	}
	switch prev, seen := m[key]; {
	case !seen:
		m[key] = english
	case prev == english || prev == ambiguous:
		// already settled
	default:
		m[key] = ambiguous
	}
}

func get(m map[string]string, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	v, ok := m[key]
	if !ok || v == ambiguous {
		return "", false
	}
	return v, true
}

// rebuildNameIndexLocked recomputes the inbound index from pokemonIDMap and
// pokemonNamesById.
//
// The caller already holds s.mu: every applyResult call site takes the write lock
// around it, so taking it here would deadlock.
func (s *Store) rebuildNameIndexLocked() {
	if len(s.pokemonIDMap) == 0 {
		s.nameIdx = nil
		return
	}

	idx := &nameIndex{
		foldEN:      make(map[string]string, len(s.pokemonIDMap)),
		exactByLang: make(map[string]map[string]string, len(langIDToCode)),
		foldByLang:  make(map[string]map[string]string, len(langIDToCode)),
		exactAny:    make(map[string]string),
		foldAny:     make(map[string]string),
	}
	for _, code := range langIDToCode {
		idx.exactByLang[code] = make(map[string]string, len(s.pokemonIDMap))
		idx.foldByLang[code] = make(map[string]string, len(s.pokemonIDMap))
	}

	// A dex number can be claimed by more than one English name: the stats feed
	// and the shiny baseline disagree on a handful, and forms share a dex. The
	// translated table is keyed by dex alone and cannot tell them apart, so the
	// baseline's name is preferred where there is one and the rest fall out as
	// ambiguous on their own.
	preferred := make(map[int]string, len(s.pokemonIDMap))
	for name, dex := range s.pokemonIDMap {
		if base, ok := shinyBaseline[dex]; ok && base.Name == name {
			preferred[dex] = name
		}
	}

	for name, dex := range s.pokemonIDMap {
		put(idx.foldEN, foldName(name), name)

		english := name
		if p, ok := preferred[dex]; ok {
			english = p
		}
		for lang, translated := range s.pokemonNamesById[dex] {
			if translated == "" {
				continue
			}
			if byLang, ok := idx.exactByLang[lang]; ok {
				put(byLang, translated, english)
				put(idx.foldByLang[lang], foldName(translated), english)
			}
			put(idx.exactAny, translated, english)
			put(idx.foldAny, foldName(translated), english)
		}
	}
	s.nameIdx = idx
}

// ResolveSpecies turns a species name in any language the store knows into the
// canonical English name.
//
// prefer is a language hint, normally the requesting client's language. It only
// breaks ties: an unhinted lookup still sweeps every language, which is what makes
// this work today, while detectLang answers "en" for most callers.
//
// Every exact tier runs before every fuzzy one, so a name that is exactly some
// language's spelling is never beaten by another language's loose match.
//
// ok is false for a name that resolves to nothing AND for one that resolves to
// more than one species. Callers must not treat the empty string as a species.
func (s *Store) ResolveSpecies(name, prefer string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolveSpeciesLocked(name, prefer)
}

// resolveSpeciesLocked is ResolveSpecies' body, for callers that already hold the
// read lock and would otherwise take it twice around one logical lookup.
func (s *Store) resolveSpeciesLocked(name, prefer string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}

	// Tier 1: it is already the English name we store.
	if _, ok := s.pokemonIDMap[name]; ok {
		return name, true
	}

	idx := s.nameIdx
	if idx == nil {
		return "", false
	}

	// Tier 2 and 3: an exact translated spelling, the hinted language first.
	if m, ok := idx.exactByLang[prefer]; ok {
		if english, ok := get(m, name); ok {
			return english, true
		}
	}
	if english, ok := get(idx.exactAny, name); ok {
		return english, true
	}

	// Tier 4 through 6: the same again, folded.
	folded := foldName(name)
	if english, ok := get(idx.foldEN, folded); ok {
		return english, true
	}
	if m, ok := idx.foldByLang[prefer]; ok {
		if english, ok := get(m, folded); ok {
			return english, true
		}
	}
	if english, ok := get(idx.foldAny, folded); ok {
		return english, true
	}
	return "", false
}

// SpeciesLoaded reports whether the store knows any species at all.
//
// It is false only when the embedded fallback itself failed to parse, since
// Start loads that synchronously before anything serves. Write paths that refuse
// an unresolvable name check it first, so a broken data load degrades to the old
// accept-anything behavior rather than rejecting every trainer's catch.
func (s *Store) SpeciesLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pokemonIDMap) > 0
}

// ResolveDexID is ResolveSpecies followed by the dex lookup, for the callers that
// only ever wanted the number. It answers 0 the same way PokemonDexID does, so it
// is a drop in for a call site that already treats 0 as "unknown".
func (s *Store) ResolveDexID(name, prefer string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	english, ok := s.resolveSpeciesLocked(name, prefer)
	if !ok {
		return 0
	}
	return s.pokemonIDMap[english]
}
