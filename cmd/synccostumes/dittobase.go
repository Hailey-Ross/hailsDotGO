package main

// Dittobase (dittobase.com) is the only source that knows what a costume is actually CALLED.
//
// The masterfile names are machine names ("Spring 2020 Noevolve"); the costume that code really
// refers to is the Visor. Dittobase names it properly, which makes it the difference between
// naming a new costume by research and naming it by copy-paste.
//
// It is a SUGGESTION and CROSS-CHECK source, never an authority. Our curated labels win: they
// are user data (trainers type them into a free-text field), and adopting Dittobase's wording
// wholesale would rename ~32 existing labels and orphan the art on entries people have saved.
// Where we differ, we are usually right: they call Dawn's Hat "Rei's Cap".
//
// Everything here degrades to nothing. If Dittobase is unreachable, or a slug does not match,
// we fall back to the cache and then to the masterfile name. Sprites, codes and eligibility
// never depend on it.

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	dittoSitemap = "https://www.dittobase.com/sitemaps/pokemon-go.xml"
	dittoPage    = "https://www.dittobase.com/pokemon-go/pokedex/"
	namesPath    = "cmd/synccostumes/names.json"

	// robots.txt permits /pokemon-go/pokedex/. Be a good guest anyway: identify ourselves, and
	// only ever fetch pages we have not already cached, so the steady state is zero traffic.
	dittoUA    = "hailsDotGO/1.0 (+https://pogo.hails.live)"
	dittoDelay = 120 * time.Millisecond
)

var (
	locRe = regexp.MustCompile(`<loc>[^<]*/pokemon-go/pokedex/([^<]+)</loc>`)
	// og:title is the clean name: unlike <title> it is not HTML-escaped, so "Cap's Hat" does
	// not arrive as "Cap&#x27;s Hat".
	ogTitleRe = regexp.MustCompile(`property="og:title"\s+content="([^"]*)"`)
	suffixRe  = regexp.MustCompile(`\s*\(Pok.mon GO\).*$`)
)

// names maps a costume code to the Dittobase name per dex: {"c:SPRING_2020_NOEVOLVE": {"6": "Visor"}}.
type names map[string]map[string]string

func (n names) get(code string, dex int) string { return n[code][strconv.Itoa(dex)] }

func (n names) set(code string, dex int, name string) {
	if n[code] == nil {
		n[code] = map[string]string{}
	}
	n[code][strconv.Itoa(dex)] = name
}

func loadNames() names {
	n := names{}
	b, err := os.ReadFile(namesPath)
	if err != nil {
		return n
	}
	json.Unmarshal(b, &n)
	return n
}

func writeNames(n names) error {
	b, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(namesPath, append(b, '\n'), 0o644)
}

// refreshNames fills in any (code, dex) pair we have no cached name for. Offline, or on any
// network failure, it just returns what is already cached.
func refreshNames(cat *catalog, dexToName map[int]string, offline bool) names {
	cached := loadNames()

	var want []struct {
		code string
		dex  int
	}
	for code, e := range cat.Codes {
		for _, dex := range e.Dex {
			if cached.get(code, dex) == "" {
				want = append(want, struct {
					code string
					dex  int
				}{code, dex})
			}
		}
	}
	if len(want) == 0 {
		return cached
	}
	if offline {
		fmt.Printf("dittobase: %d name(s) uncached, skipped (-offline)\n", len(want))
		return cached
	}

	slugs, err := dittoSlugs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dittobase: sitemap unavailable (%v); using cached names only\n", err)
		return cached
	}
	fmt.Printf("dittobase: %d pages listed, fetching %d uncached name(s)\n", len(slugs), len(want))

	ambiguous := ambiguousSlugs(cat, dexToName, slugs)

	found, missed, skipped := 0, 0, 0
	for _, w := range want {
		if ambiguous[slugKey(w.dex, w.code)] {
			skipped++
			continue // two codes share this page; naming either from it would be a guess
		}
		slug := matchSlug(slugs, dexToName[w.dex], w.code)
		if slug == "" {
			missed++
			continue // editorial rename or no page; the masterfile name stands in
		}
		name, err := dittoName(slug, dexToName[w.dex])
		if err != nil || name == "" {
			missed++
			continue
		}
		cached.set(w.code, w.dex, name)
		found++
		time.Sleep(dittoDelay)
	}
	fmt.Printf("dittobase: %d named, %d without a page, %d ambiguous\n", found, missed, skipped)

	if err := writeNames(cached); err != nil {
		fmt.Fprintf(os.Stderr, "dittobase: could not write %s: %v\n", namesPath, err)
	}
	return cached
}

func dittoSlugs() ([]string, error) {
	req, _ := http.NewRequest("GET", dittoSitemap, nil)
	req.Header.Set("User-Agent", dittoUA)
	resp, err := httpc.Do(req)
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
	return out, nil
}

// matchSlug finds the Dittobase page for a (species, code).
//
// Slugs are {species}-{code}, with the code lowercased, underscores hyphenated, and a trailing
// _NOEVOLVE dropped: charizard-spring-2020. Some carry a form segment in the middle
// (pumpkaboo-average-fall-2022, zigzagoon-galarian-gofest-2021), so fall back to a
// prefix/suffix match. Together that covers ~90% of our pairs; the rest are editorial renames
// (nidoking-crown for ROYAL) that no rule can derive.
func matchSlug(slugs []string, species, code string) string {
	if species == "" {
		return ""
	}
	s, k := slugify(species), codeSlug(code)
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

func slugKey(dex int, code string) string { return strconv.Itoa(dex) + "|" + code }

// ambiguousSlugs finds (dex, code) pairs whose Dittobase page is shared with another code, and
// which therefore cannot be named from it.
//
// The cause is the _NOEVOLVE suffix. Dittobase has no page for the no-evolve twin of a costume,
// so c:FALL_2022 and c:FALL_2022_NOEVOLVE both slugify to "fall-2022", and Vulpix's Spooky
// Festival page would be used to name a completely different costume. Refusing to name either
// is the honest answer: the masterfile name stands in, and the name check stays quiet rather
// than accusing a correct label of being wrong.
func ambiguousSlugs(cat *catalog, dexToName map[int]string, slugs []string) map[string]bool {
	type pair struct {
		dex  int
		code string
	}
	bySlug := map[string][]pair{}
	for code, e := range cat.Codes {
		for _, dex := range e.Dex {
			if slug := matchSlug(slugs, dexToName[dex], code); slug != "" {
				bySlug[strconv.Itoa(dex)+"|"+slug] = append(bySlug[strconv.Itoa(dex)+"|"+slug], pair{dex, code})
			}
		}
	}

	out := map[string]bool{}
	for _, ps := range bySlug {
		if len(ps) < 2 {
			continue
		}
		for _, p := range ps {
			out[slugKey(p.dex, p.code)] = true
		}
	}
	return out
}

// codeSlug turns c:SPRING_2020_NOEVOLVE into spring-2020.
func codeSlug(code string) string {
	_, name, _ := strings.Cut(code, ":")
	name = strings.TrimSuffix(name, "_NOEVOLVE")
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

func slugify(s string) string {
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

// dittoName reads the costume name off a page: og:title is "Visor Charizard (Pokémon GO)", so
// drop the suffix and the trailing species and what remains is the label: "Visor".
func dittoName(slug, species string) (string, error) {
	req, _ := http.NewRequest("GET", dittoPage+slug, nil)
	req.Header.Set("User-Agent", dittoUA)
	resp, err := httpc.Do(req)
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
	return strings.TrimSpace(stripSpecies(title, species)), nil
}

// stripSpecies removes the trailing species from "Visor Charizard". Compared on a prefix so
// that Dittobase's own spelling variations still match.
func stripSpecies(title, species string) string {
	words := strings.Fields(title)
	if len(words) < 2 || species == "" {
		return title
	}
	last, sp := slugify(words[len(words)-1]), slugify(species)
	if len(sp) >= 4 && strings.HasPrefix(last, sp[:min(5, len(sp))]) {
		return strings.Join(words[:len(words)-1], " ")
	}
	return title
}
