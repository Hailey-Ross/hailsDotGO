package pogodata

// Pokedex species text: the genus ("the Seed Pokemon"), the flavour text, and the
// legendary and mythical flags, reduced to one entry per species per language.
//
// The website reads these three straight off pokeapi.co from the browser
// (ts/shared/pokedex.ts), one lazy request per species per page load. The mobile
// app ported that faithfully and then asked for it to stop. Two reasons, both
// about the device rather than the data: a third party host learning which dex
// number every trainer just tapped is a new category of request for a client that
// otherwise talks only to this site, and an absolute URL through the app's shared
// HTTP client would have stamped a live session token onto it. Serving the text
// from here removes the hazard rather than guarding it.
//
// It also fixes a real bug the website has too. Both clients filter the upstream
// entries to English, but the app localizes species names, so a German trainer
// reads "Bibaldo" with English flavour text under it. The CSVs are fully
// translated; doing the merge here means every client gets its own language
// instead of reimplementing the fallback.
//
// Four CSVs, one host, all of it the PokeAPI repo this store already fetches from:
//
//	pokemon_species_names.csv        404 KB, and ALREADY FETCHED for localized
//	                                 names. Its fourth column is the genus, which
//	                                 this store has been downloading and throwing
//	                                 away every six hours since names moved to the
//	                                 CSV route. refreshPokemonNames now hands the
//	                                 genus map over, so genus costs no request.
//	pokemon_species.csv               57 KB, is_legendary and is_mythical.
//	versions.csv, languages.csv        1 KB, id lookups. RESOLVED rather than
//	                                 hardcoded, so a renumbering upstream fails
//	                                 loudly instead of quietly serving the wrong
//	                                 game's text under the right game's name.
//	pokemon_species_flavor_text.csv  9.2 MB, 199,724 rows: every species times
//	                                 every game times every language. Streamed and
//	                                 reduced as it arrives and never held whole.
//	                                 About 5,000 short strings survive. That
//	                                 reduction is the whole argument for doing this
//	                                 server side.
//
// The extraction rules below are ts/shared/pokedex.ts's, ported exactly, because
// the app renders what this produces and a difference here changes its rendering
// underneath it: prefer the newest mainline game that has an entry, fall back to
// the LAST remaining entry rather than the first, and normalize the whitespace.
// That last step is not cosmetic. The original game text is hard wrapped to fit a
// text box and littered with form feeds, so skipping it is exactly what makes the
// sheet render looking broken.

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pokeAPICSVBase is the PokeAPI repo's data dump directory. Not pokeapi.co: this
// is the raw CSV route, which is why a grep for the REST host finds nothing here.
//
// A var rather than a const only so the tests can point the four fetches at an
// httptest server. Nothing in the running app reassigns it.
var pokeAPICSVBase = "https://raw.githubusercontent.com/PokeAPI/pokeapi/master/data/v2/csv/"

// pokedexFlavorTimeout bounds the one download big enough to need its own bound.
//
// The store's shared client allows 15 seconds, which 9.2 MB can genuinely exceed
// on a bad link. 60 rather than something larger because CheckScrapers fetches
// this synchronously inside an admin HTTP request against a 90 second write
// timeout, and a fetch that outlives the response it is reporting into is worse
// than a fetch that gives up.
const pokedexFlavorTimeout = 60 * time.Second

// pokedexVersionOrder is the game preference from ts/shared/pokedex.ts, newest
// mainline first. Names, not ids: the CSV is keyed by version_id and the ids are
// resolved from versions.csv at fetch time.
var pokedexVersionOrder = []string{
	"scarlet", "violet", "sword", "shield", "sun",
	"moon", "x", "y", "alpha-sapphire", "omega-ruby",
}

// pokedexLangs is the set of locales this serves, derived from langIDToCode so
// there is one list to maintain rather than two that can drift.
//
// English is added because it is absent there on purpose: the store keys species
// on their English name, so English is the canonical form rather than a
// translation. Here it is both a served language and the one every other language
// falls back to, and a refresh that cannot resolve it fails instead of applying.
var pokedexLangs = func() []string {
	out := make([]string, 0, len(langIDToCode)+1)
	for _, code := range langIDToCode {
		out = append(out, code)
	}
	sort.Strings(out)
	return append([]string{"en"}, out...)
}()

// PokedexFlags is the language independent half of a species entry.
type PokedexFlags struct {
	Legendary bool `json:"legendary,omitempty"`
	Mythical  bool `json:"mythical,omitempty"`
}

// PokedexText is the localized half.
type PokedexText struct {
	Genus  string `json:"genus,omitempty"`
	Flavor string `json:"flavor,omitempty"`
}

// PokedexEntry is one species as a client receives it: both halves resolved, with
// English standing in for anything the locale has not translated.
//
// No omitempty, deliberately. A species PokeAPI has no text for is a blank entry
// and not an absent one, so a client can tell "nothing to show" apart from "ask
// again later" without a second request.
type PokedexEntry struct {
	Genus     string `json:"genus"`
	Flavor    string `json:"flavor"`
	Legendary bool   `json:"legendary"`
	Mythical  bool   `json:"mythical"`
}

// pokedexPayload is exactly what cache/pokedex_species.json holds, and the only
// shape applyResult accepts for this key.
//
// Split three ways rather than one entry per species per language because the two
// halves have very different shapes. Flags are the same in every language and true
// for about ninety species, so they are stored once and SPARSELY. Text is stored
// only where there is text. Dex carries the full species list, so a species with
// neither still has a defined blank entry rather than vanishing from the set.
type pokedexPayload struct {
	Dex   []int                             `json:"dex"`
	Flags map[string]PokedexFlags           `json:"flags"`
	Text  map[string]map[string]PokedexText `json:"text"`
}

// decode converts the payload's string dex keys back to ints.
func (p pokedexPayload) decode() (map[int]PokedexFlags, map[string]map[int]PokedexText) {
	flags := make(map[int]PokedexFlags, len(p.Flags))
	for k, v := range p.Flags {
		if id, err := strconv.Atoi(k); err == nil {
			flags[id] = v
		}
	}
	text := make(map[string]map[int]PokedexText, len(p.Text))
	for lang, byDex := range p.Text {
		inner := make(map[int]PokedexText, len(byDex))
		for k, v := range byDex {
			if id, err := strconv.Atoi(k); err == nil {
				inner[id] = v
			}
		}
		text[lang] = inner
	}
	return flags, text
}

// PokedexVersion is a content hash of the applied payload, or "" when nothing has
// loaded yet.
//
// A hash and not a timestamp, for the reason recorded on the shiny dex manifest: a
// clock based version changes on every refresh and makes every client re-download
// a payload it already has. It is also what lets the handler cache a marshalled
// body per language with no TTL to guess at, since it can simply notice the store
// answering a different hash.
func (s *Store) PokedexVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pokedexVersion
}

// PokedexSpecies returns dex to resolved entry for one language.
//
// English stands in per FIELD, not per species: a locale that has a genus but no
// flavour text for a species shows its own genus above English flavour text rather
// than blanking the section. A half populated language quietly blanking part of a
// screen is the regression this exists to avoid; falling back is the same rule
// TFunc applies to every string on the website.
func (s *Store) PokedexSpecies(lang string) map[int]PokedexEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	local, en := s.pokedexText[lang], s.pokedexText["en"]
	out := make(map[int]PokedexEntry, len(s.pokedexDex))
	for _, dex := range s.pokedexDex {
		out[dex] = resolvePokedexEntry(local[dex], en[dex], s.pokedexFlags[dex])
	}
	return out
}

// PokedexSpeciesOne is PokedexSpecies for a single dex number. ok is false only
// when upstream has no such species at all, which is a 404 and not a blank entry.
func (s *Store) PokedexSpeciesOne(dex int, lang string) (PokedexEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !containsInt(s.pokedexDex, dex) {
		return PokedexEntry{}, false
	}
	return resolvePokedexEntry(s.pokedexText[lang][dex], s.pokedexText["en"][dex], s.pokedexFlags[dex]), true
}

// PokedexSize reports how many species the reduction carries. Zero means nothing
// has been fetched or loaded yet, which the handler answers as a 503.
func (s *Store) PokedexSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pokedexDex)
}

func resolvePokedexEntry(local, en PokedexText, flags PokedexFlags) PokedexEntry {
	e := PokedexEntry{Genus: local.Genus, Flavor: local.Flavor, Legendary: flags.Legendary, Mythical: flags.Mythical}
	if e.Genus == "" {
		e.Genus = en.Genus
	}
	if e.Flavor == "" {
		e.Flavor = en.Flavor
	}
	return e
}

// containsInt reports membership in the sorted dex list.
func containsInt(sorted []int, v int) bool {
	i := sort.SearchInts(sorted, v)
	return i < len(sorted) && sorted[i] == v
}

// ApplyPokedexSpecies loads an already built reduction into the store without
// fetching or writing one.
//
// The same validation the refresh path applies: a payload that does not parse, or
// that carries no English, is REJECTED and the last good one kept. It exists
// because the reduction is built here but consumed a package away, so the handlers
// layer has no other way to stand a populated store up in a test, and because a
// caller holding a known good payload should not have to go back to upstream for
// it. Returns whether the payload was accepted.
func (s *Store) ApplyPokedexSpecies(data json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.pokedexVersion
	s.applyResult("pokedex_species", data)
	return s.pokedexVersion != before
}

// refreshPokedexSpecies rebuilds the reduction and applies it, keeping the last
// good one on any failure.
//
// genera comes from refreshPokemonNames, which has just parsed it out of a CSV it
// was fetching anyway. Passing it rather than re-fetching is the difference
// between two new requests per refresh and three.
func (s *Store) refreshPokedexSpecies(genera map[int]map[int]string) {
	data, n, err := s.fetchPokedexSpecies(genera)
	if err != nil {
		log.Printf("pogodata: pokedex species refresh: %v", err)
		return
	}
	s.persistAndApply("pokedex_species", data)
	log.Printf("pogodata: pokedex species: %d species reduced across %d languages (%d KB)", n, len(pokedexLangs), len(data)/1024)
}

// fetchPokedexSpecies builds the whole reduction. It does not touch the store;
// callers persist and apply the result.
func (s *Store) fetchPokedexSpecies(genera map[int]map[int]string) (json.RawMessage, int, error) {
	// The genus rides along on the species names CSV, so an empty map means that
	// fetch failed rather than that upstream has no genera. Building anyway would
	// replace a good reduction with one missing a third of its content, and a
	// successful refresh leaving the store worse off than a failed one is the exact
	// failure cpmultipliers.go records at the top of that file.
	if len(genera) == 0 {
		return nil, 0, fmt.Errorf("no genus data: the species names fetch must have failed")
	}

	langIDs, err := s.fetchPokedexLangIDs()
	if err != nil {
		return nil, 0, fmt.Errorf("languages.csv: %w", err)
	}
	// Only now can the genus be keyed by locale: the ids it arrived under are
	// PokeAPI's, and which id means English is the thing this refuses to hardcode.
	genusByLang := make(map[string]map[int]string, len(langIDs))
	for id, code := range langIDs {
		genusByLang[code] = genera[id]
	}
	if len(genusByLang["en"]) == 0 {
		return nil, 0, fmt.Errorf("no English genus rows in the species names CSV")
	}
	versionRank, err := s.fetchPokedexVersionRank()
	if err != nil {
		return nil, 0, fmt.Errorf("versions.csv: %w", err)
	}
	dex, flags, err := s.fetchPokedexFlags()
	if err != nil {
		return nil, 0, fmt.Errorf("pokemon_species.csv: %w", err)
	}
	flavor, err := s.fetchPokedexFlavor(langIDs, versionRank)
	if err != nil {
		return nil, 0, fmt.Errorf("pokemon_species_flavor_text.csv: %w", err)
	}

	p := pokedexPayload{
		Dex:   dex,
		Flags: make(map[string]PokedexFlags, len(flags)),
		Text:  make(map[string]map[string]PokedexText, len(pokedexLangs)),
	}
	for id, f := range flags {
		p.Flags[strconv.Itoa(id)] = f
	}
	for _, lang := range pokedexLangs {
		byDex := make(map[string]PokedexText)
		for _, id := range dex {
			t := PokedexText{Genus: genusByLang[lang][id], Flavor: flavor[lang][id]}
			// Skipped rather than written blank: an absent dex already reads as a
			// blank entry, so writing 1,025 empty objects per language would only
			// make the cache file bigger.
			if t.Genus != "" || t.Flavor != "" {
				byDex[strconv.Itoa(id)] = t
			}
		}
		p.Text[lang] = byDex
	}

	data, err := json.Marshal(p)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal: %w", err)
	}
	return data, len(dex), nil
}

// fetchPokedexLangIDs resolves PokeAPI's language ids for the locales this site
// serves, keyed the way the flavour CSV is: id to our locale code.
//
// English missing is fatal, because every other language falls back to it and a
// reduction without it would blank the section for anyone whose own language is
// short an entry. Any other language missing is a warning and a skip, which leaves
// that locale reading English rather than reading nothing.
func (s *Store) fetchPokedexLangIDs() (map[int]string, error) {
	r, body, err := s.openPokeAPICSV("languages.csv", 0)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	byIdentifier, err := readCSVIndex(r, "id", "identifier")
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(pokedexLangs))
	for _, code := range pokedexLangs {
		id, ok := byIdentifier[code]
		if !ok {
			if code == "en" {
				return nil, fmt.Errorf("no language identifier %q", code)
			}
			log.Printf("pogodata: pokedex species: no language identifier %q upstream, that locale will read English", code)
			continue
		}
		out[id] = code
	}
	return out, nil
}

// fetchPokedexVersionRank resolves pokedexVersionOrder to version ids, mapping
// each id to its position in the preference order.
//
// Resolved rather than hardcoded on purpose. The ids are stable in practice, but
// hardcoding them means a renumbering upstream serves Sword's text under Scarlet's
// preference with nothing to notice. This way a name that stops resolving is a
// line in the log, and a file that stops resolving anything is a failed refresh
// that keeps the last good reduction.
func (s *Store) fetchPokedexVersionRank() (map[int]int, error) {
	r, body, err := s.openPokeAPICSV("versions.csv", 0)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	byIdentifier, err := readCSVIndex(r, "id", "identifier")
	if err != nil {
		return nil, err
	}
	out := make(map[int]int, len(pokedexVersionOrder))
	for rank, name := range pokedexVersionOrder {
		id, ok := byIdentifier[name]
		if !ok {
			log.Printf("pogodata: pokedex species: version %q not in versions.csv, dropped from the preference order", name)
			continue
		}
		out[id] = rank
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of the %d preferred versions resolved", len(pokedexVersionOrder))
	}
	return out, nil
}

// fetchPokedexFlags reads is_legendary and is_mythical, and the full species list
// with them. The flags map is SPARSE: only species with one set appear.
func (s *Store) fetchPokedexFlags() ([]int, map[int]PokedexFlags, error) {
	r, body, err := s.openPokeAPICSV("pokemon_species.csv", 0)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()
	return readPokedexFlags(r)
}

// readPokedexFlags is fetchPokedexFlags' parse half.
func readPokedexFlags(r *csv.Reader) ([]int, map[int]PokedexFlags, error) {
	header, err := r.Read()
	if err != nil {
		return nil, nil, err
	}
	// By name, not by position. This file carries twenty columns and upstream
	// inserting one would shift a positional read silently onto the wrong field,
	// which for a boolean column means wrong answers rather than a parse error.
	iID, iLeg, iMyth := csvColumn(header, "id"), csvColumn(header, "is_legendary"), csvColumn(header, "is_mythical")
	if iID < 0 || iLeg < 0 || iMyth < 0 {
		return nil, nil, fmt.Errorf("columns id/is_legendary/is_mythical resolved to %d/%d/%d", iID, iLeg, iMyth)
	}
	width := max(iID, iLeg, iMyth)

	var dex []int
	flags := make(map[int]PokedexFlags)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if len(rec) <= width {
			continue
		}
		id, err := strconv.Atoi(rec[iID])
		if err != nil || id <= 0 {
			continue
		}
		dex = append(dex, id)
		if f := (PokedexFlags{Legendary: rec[iLeg] == "1", Mythical: rec[iMyth] == "1"}); f.Legendary || f.Mythical {
			flags[id] = f
		}
	}
	if len(dex) == 0 {
		return nil, nil, fmt.Errorf("no species rows parsed")
	}
	sort.Ints(dex)
	return dex, flags, nil
}

// flavorPick is the running reduction for one (language, species) pair: the best
// ranked entry seen so far, and the last one seen whatever its rank.
type flavorPick struct {
	rank int // index in pokedexVersionOrder; -1 until a preferred version turns up
	best string
	last string
}

// fetchPokedexFlavor streams the 199,724 row flavour text file and reduces it to
// one entry per (language, species) as it goes.
//
// Streamed, not buffered: this is 9.2 MB of which about 300 KB survives, and
// holding the raw blob to filter it afterwards would be the largest allocation in
// the store for no gain.
//
// encoding/csv, not a line scan. flavor_text embeds newlines inside its quoted
// fields, and the very FIRST data row wraps mid sentence, so a line based parse
// does not fail, it silently corrupts.
func (s *Store) fetchPokedexFlavor(langIDs map[int]string, versionRank map[int]int) (map[string]map[int]string, error) {
	r, body, err := s.openPokeAPICSV("pokemon_species_flavor_text.csv", pokedexFlavorTimeout)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return reducePokedexFlavor(r, langIDs, versionRank)
}

// reducePokedexFlavor is fetchPokedexFlavor's parse half: the reduction itself,
// separated so it can be tested against a fixture rather than against upstream.
func reducePokedexFlavor(r *csv.Reader, langIDs map[int]string, versionRank map[int]int) (map[string]map[int]string, error) {
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	iSpecies := csvColumn(header, "species_id")
	iVersion := csvColumn(header, "version_id")
	iLang := csvColumn(header, "language_id")
	iText := csvColumn(header, "flavor_text")
	if iSpecies < 0 || iVersion < 0 || iLang < 0 || iText < 0 {
		return nil, fmt.Errorf("columns species_id/version_id/language_id/flavor_text resolved to %d/%d/%d/%d", iSpecies, iVersion, iLang, iText)
	}
	width := max(iSpecies, iVersion, iLang, iText)

	acc := make(map[string]map[int]*flavorPick, len(langIDs))
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Deliberately fatal rather than "stop early with what we have". A read
			// error part way through a 9 MB body is a truncated download, and half a
			// reduction applied over a whole one is exactly the partial write this
			// must never perform.
			return nil, err
		}
		if len(rec) <= width {
			continue
		}
		langID, errLang := strconv.Atoi(rec[iLang])
		if errLang != nil {
			continue
		}
		lang, ok := langIDs[langID]
		if !ok {
			continue
		}
		id, errID := strconv.Atoi(rec[iSpecies])
		if errID != nil || id <= 0 {
			continue
		}
		if acc[lang] == nil {
			acc[lang] = make(map[int]*flavorPick)
		}
		p := acc[lang][id]
		if p == nil {
			p = &flavorPick{rank: -1}
			acc[lang][id] = p
		}
		p.last = rec[iText]
		if versionID, err := strconv.Atoi(rec[iVersion]); err == nil {
			if rank, ok := versionRank[versionID]; ok && (p.rank < 0 || rank < p.rank) {
				p.rank, p.best = rank, rec[iText]
			}
		}
	}

	out := make(map[string]map[int]string, len(acc))
	for lang, byDex := range acc {
		inner := make(map[int]string, len(byDex))
		for id, p := range byDex {
			// The LAST remaining entry when no preferred version matched, not the
			// first. ts/shared/pokedex.ts takes en[en.length-1], and in this file's
			// ordering that is the newest game to have said anything about the
			// species, which is the better answer as well as the faithful one.
			text := p.best
			if p.rank < 0 {
				text = p.last
			}
			inner[id] = normalizePokedexFlavor(text)
		}
		out[lang] = inner
	}
	return out, nil
}

// pokedexWhitespace matches any run of whitespace, including the Unicode spaces
// JavaScript's \s matches and Go's does not. The site's extraction runs in a
// browser, so a non breaking space it would have collapsed has to collapse here
// too or the two render differently.
var pokedexWhitespace = regexp.MustCompile(`[\s\p{Zs}\x{feff}]+`)

// pokedexLineBreaks are the characters the game text box wraps with.
var pokedexLineBreaks = strings.NewReplacer("\f", " ", "\n", " ", "\r", " ")

// normalizePokedexFlavor flattens one entry's hard wrapping into a single line.
//
// Not cosmetic and not optional. Every entry arrives wrapped to the width of the
// game's text box, with a form feed where the box scrolls. Rendered as is in a
// sheet that does its own wrapping, it breaks in the wrong places and shows gaps.
func normalizePokedexFlavor(s string) string {
	return strings.TrimSpace(pokedexWhitespace.ReplaceAllString(pokedexLineBreaks.Replace(s), " "))
}

// openPokeAPICSV starts a streaming read of one of PokeAPI's CSV data dumps. The
// caller closes the returned body. A non zero timeout gets its own client, for the
// one file the store's shared 15 second client is too tight for.
func (s *Store) openPokeAPICSV(name string, timeout time.Duration) (*csv.Reader, io.Closer, error) {
	client := s.client
	if timeout > 0 {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Get(pokeAPICSVBase + name)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	r := csv.NewReader(resp.Body)
	// Variable width: these files are well formed, but a fixed count would reject a
	// whole file over one trailing column upstream added, and every read below
	// checks the indices it actually uses.
	r.FieldsPerRecord = -1
	// LazyQuotes stays OFF. The quoting here is correct, and turning it on would
	// make a genuinely corrupt download parse into plausible garbage rather than
	// fail the refresh and keep the last good reduction.
	return r, resp.Body, nil
}

// readCSVIndex reads a whole two column lookup out of a small CSV, resolving both
// columns by header name and returning value to id.
func readCSVIndex(r *csv.Reader, idCol, nameCol string) (map[string]int, error) {
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	iID, iName := csvColumn(header, idCol), csvColumn(header, nameCol)
	if iID < 0 || iName < 0 {
		return nil, fmt.Errorf("columns %s/%s resolved to %d/%d", idCol, nameCol, iID, iName)
	}
	width := max(iID, iName)
	out := make(map[string]int)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) <= width {
			continue
		}
		if id, err := strconv.Atoi(rec[iID]); err == nil && rec[iName] != "" {
			out[rec[iName]] = id
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no rows parsed")
	}
	return out, nil
}

// csvColumn returns the index of a named column in a header row, or -1. The BOM
// trim matters on the first column only, and only if upstream ever writes one.
func csvColumn(header []string, name string) int {
	for i, h := range header {
		if strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")) == name {
			return i
		}
	}
	return -1
}

// pokedexContentHash is the version clients cache on. Over the marshalled payload,
// so it changes when and only when the served text does.
func pokedexContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}
