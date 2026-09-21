// Package costumenames asks Dittobase what a costume is actually CALLED, and compares names.
//
// It lives in its own package because two callers need the same answers and must not disagree:
// cmd/synccostumes, when it derives the catalog on a dev box, and the server's costume discovery
// job, which has to judge a brand new code on its own at 3am with nobody watching.
//
// Dittobase is a SUGGESTION and CROSS-CHECK source, never an authority. Curated labels win: they
// are user data (trainers type them into a free-text field), and adopting Dittobase's wording
// wholesale would rename existing labels and orphan the art on entries people have already saved.
// Where we differ we are usually right: they call Dawn's Hat "Rei's Cap".
//
// Everything here degrades to nothing. If Dittobase is unreachable, or a slug does not match, the
// caller falls back to the cache and then to the masterfile name. Sprites, codes and eligibility
// never depend on it.
package costumenames

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sitemapURL = "https://www.dittobase.com/sitemaps/pokemon-go.xml"
	pageURL    = "https://www.dittobase.com/pokemon-go/pokedex/"

	// robots.txt permits /pokemon-go/pokedex/. Be a good guest anyway: identify ourselves, and
	// only ever fetch pages we have not already cached, so the steady state is zero traffic.
	userAgent = "hailsDotGO/1.0 (+https://pogo.hails.app)"

	// Delay is how long a caller should wait between requests it actually makes. Exported because
	// the pacing is part of being a good guest, not an implementation detail of one caller.
	Delay = 120 * time.Millisecond
)

var (
	locRe = regexp.MustCompile(`<loc>[^<]*/pokemon-go/pokedex/([^<]+)</loc>`)
	// og:title is the clean name: unlike <title> it is not HTML-escaped, so "Cap's Hat" does
	// not arrive as "Cap&#x27;s Hat".
	ogTitleRe = regexp.MustCompile(`property="og:title"\s+content="([^"]*)"`)
	suffixRe  = regexp.MustCompile(`\s*\(Pok.mon GO\).*$`)
)

// Names maps a costume code to the Dittobase name per dex:
// {"c:SPRING_2020_NOEVOLVE": {"6": "Visor"}}.
//
// Keyed by dex-as-string because it is written to disk as JSON, where object keys are strings
// anyway, and round-tripping through ints would churn the committed file.
type Names map[string]map[string]string

func (n Names) Get(code string, dex int) string { return n[code][strconv.Itoa(dex)] }

func (n Names) Set(code string, dex int, name string) {
	if n[code] == nil {
		n[code] = map[string]string{}
	}
	n[code][strconv.Itoa(dex)] = name
}

// Load reads a names cache. A missing or unreadable file yields an empty set rather than an
// error: a cold cache is a slow run, not a broken one.
func Load(path string) Names {
	n := Names{}
	data, err := os.ReadFile(path)
	if err != nil {
		return n
	}
	if err := json.Unmarshal(data, &n); err != nil {
		return Names{}
	}
	return n
}

// Save writes a names cache, creating the directory if needed. UTF-8, LF, no BOM: the sync tool's
// copy is committed, and a BOM in a go:embed'ed sibling has taken the service down before.
func Save(path string, n Names) error {
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Resolver holds one run's sitemap listing, so a batch of lookups costs a single fetch.
type Resolver struct {
	client *http.Client
	slugs  []string
}

// NewResolver fetches the sitemap. client may be nil, in which case a 60 second one is used.
func NewResolver(client *http.Client) (*Resolver, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, _ := http.NewRequest("GET", sitemapURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sitemap -> %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, m := range locRe.FindAllStringSubmatch(string(body), -1) {
		if !strings.Contains(m[1], "/") { // skip /counters, /cp-iv-chart subpages
			out = append(out, m[1])
		}
	}
	return &Resolver{client: client, slugs: out}, nil
}

// Slugs is the listing this resolver fetched.
func (r *Resolver) Slugs() []string { return r.slugs }

// Match finds the Dittobase page for a (species, code), or "" if there is none.
//
// Slugs are {species}-{code}, with the code lowercased, underscores hyphenated, and a trailing
// _NOEVOLVE dropped: charizard-spring-2020. Some carry a form segment in the middle
// (pumpkaboo-average-fall-2022, zigzagoon-galarian-gofest-2021), so fall back to a prefix/suffix
// match. Together that covers most pairs; the rest are editorial renames (nidoking-crown for
// ROYAL) that no rule can derive.
func (r *Resolver) Match(species, code string) string {
	return MatchSlug(r.slugs, species, code)
}

// MatchSlug is Match against a caller-supplied listing, for tests and for callers that already
// hold one.
func MatchSlug(slugs []string, species, code string) string {
	if species == "" {
		return ""
	}
	s, k := Slugify(species), CodeSlug(code)
	exact := s + "-" + k
	for _, x := range slugs {
		if x == exact {
			return x
		}
	}
	for _, x := range slugs {
		if strings.HasPrefix(x, s+"-") && strings.HasSuffix(x, "-"+k) {
			return x
		}
	}
	return ""
}

// Fetch reads the costume name off a page. og:title is "Visor Charizard (Pokémon GO)", so drop
// the suffix and the trailing species and what remains is the label: "Visor".
//
// An empty name with a nil error means the page had no og:title we could read, which is a
// different outcome from the page being unreachable and is reported differently.
func (r *Resolver) Fetch(slug, species string) (string, error) {
	req, _ := http.NewRequest("GET", pageURL+slug, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s -> %d", slug, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return "", err
	}

	m := ogTitleRe.FindSubmatch(body)
	if m == nil {
		return "", nil
	}
	// The attribute is HTML-escaped even in og:title, so "Cap's Hat" arrives as "Cap&#x27;s Hat".
	title := suffixRe.ReplaceAllString(html.UnescapeString(string(m[1])), "")
	return strings.TrimSpace(StripSpecies(title, species)), nil
}

// StripSpecies removes the trailing species from "Visor Charizard". Compared on a prefix so that
// Dittobase's own spelling variations still match.
func StripSpecies(title, species string) string {
	words := strings.Fields(title)
	if len(words) < 2 || species == "" {
		return title
	}
	last, sp := Slugify(words[len(words)-1]), Slugify(species)
	if len(sp) >= 4 && strings.HasPrefix(last, sp[:min(5, len(sp))]) {
		return strings.Join(words[:len(words)-1], " ")
	}
	return title
}

// SlugKey identifies a (dex, code) pair.
func SlugKey(dex int, code string) string { return strconv.Itoa(dex) + "|" + code }

// Ambiguous finds (dex, code) pairs whose Dittobase page is shared with another code, and which
// therefore cannot be named from it. It maps each such pair to the code it collides with, so a
// report can say who the other one is rather than just "ambiguous".
//
// The cause is the _NOEVOLVE suffix. Dittobase has no page for the no-evolve twin of a costume,
// so c:FALL_2022 and c:FALL_2022_NOEVOLVE both slugify to "fall-2022", and Vulpix's Spooky
// Festival page would be used to name a completely different costume. Refusing to name either is
// the honest answer. Do not "fix" this by picking one.
func Ambiguous(codes map[string][]int, dexToName map[int]string, slugs []string) map[string]string {
	type pair struct {
		dex  int
		code string
	}
	bySlug := map[string][]pair{}
	for code, dexes := range codes {
		for _, dex := range dexes {
			if slug := MatchSlug(slugs, dexToName[dex], code); slug != "" {
				key := strconv.Itoa(dex) + "|" + slug
				bySlug[key] = append(bySlug[key], pair{dex, code})
			}
		}
	}

	out := map[string]string{}
	for _, ps := range bySlug {
		if len(ps) < 2 {
			continue
		}
		for _, p := range ps {
			var others []string
			for _, q := range ps {
				if q.code != p.code {
					others = append(others, q.code)
				}
			}
			sort.Strings(others)
			out[SlugKey(p.dex, p.code)] = strings.Join(others, ", ")
		}
	}
	return out
}

// CodeSlug turns c:SPRING_2020_NOEVOLVE into spring-2020.
func CodeSlug(code string) string {
	_, name, _ := strings.Cut(code, ":")
	name = strings.TrimSuffix(name, "_NOEVOLVE")
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

// Slugify lowercases and collapses every run of non-alphanumerics to a single hyphen.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// stopWords are too common to carry meaning: "Hat" would make "Straw Hat" and "Detective Hat"
// look like the same costume, which is exactly the confusion the name check exists to catch.
var stopWords = map[string]bool{
	"the": true, "a": true, "of": true, "and": true, "s": true,
	"costume": true, "outfit": true, "hat": true, "pokemon": true,
}

// Significant splits a name into comparable tokens: lowercased, punctuation dropped, short and
// too-common words removed, each truncated to five characters.
func Significant(s string) []string {
	var out []string
	for w := range strings.FieldsSeq(Slugify(s)) {
		for part := range strings.SplitSeq(w, "-") {
			if len(part) < 3 || stopWords[part] {
				continue
			}
			if len(part) > 5 {
				part = part[:5]
			}
			out = append(out, part)
		}
	}
	return out
}

// SharesWord reports whether two names have any significant word in common, comparing on
// 5-character prefixes so ordinary morphology does not trip the check: "Fashion" matches
// "Fashionable", "Holiday" matches "Holidays".
func SharesWord(a, b string) bool {
	as, bs := Significant(a), Significant(b)
	if len(as) == 0 || len(bs) == 0 {
		return true // nothing to compare; do not cry wolf
	}
	for _, x := range as {
		for _, y := range bs {
			if strings.HasPrefix(x, y) || strings.HasPrefix(y, x) {
				return true
			}
		}
	}
	return false
}

// AnySharesWord reports whether a label agrees with any of several names. A code shared across
// species is named differently per species by Dittobase ("Spooky Festival" on Gengar,
// "Cempasúchil Crown" on Duskull), so agreement with any one of them counts.
func AnySharesWord(label string, names []string) bool {
	for _, n := range names {
		if SharesWord(label, n) {
			return true
		}
	}
	return false
}

// IsEcho reports whether a Dittobase name is merely the species and the code read back, carrying
// no human name at all: "Vivillon Meadow" for VIVILLON_MEADOW, "Pikachu K 2026 A 01" for
// K_2026_A_01.
//
// This is the corroboration test the discovery job turns on, so be clear about what it means.
// A NON-echo ("Friede's Goggles", "Clone") means a human somewhere gave this thing a name, which
// ordinary alternate forms do not get. An echo means only that Dittobase has no name for it, and
// that happens BOTH for ordinary forms AND for genuinely new costumes nobody has named yet. So an
// echo must always mean "uncertain, ask a human", never "not a costume".
//
// Compared on tokens rather than strings so word order, punctuation and the species' own spelling
// cannot produce a false negative: Dittobase writes the species first on some pages and last on
// others.
func IsEcho(name, species, code string) bool {
	tokens := Significant(name)
	if len(tokens) == 0 {
		return true // nothing left after stopwords is not a name we can use
	}
	known := map[string]bool{}
	for _, t := range Significant(species) {
		known[t] = true
	}
	for _, t := range Significant(strings.ReplaceAll(CodeSlug(code), "-", " ")) {
		known[t] = true
	}
	for _, t := range tokens {
		if !known[t] {
			return false // a word from neither the species nor the code: a real name
		}
	}
	return true
}
