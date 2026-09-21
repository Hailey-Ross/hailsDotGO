package main

// Naming this tool's catalog from Dittobase.
//
// The fetching, slug matching, og:title parsing and word comparison all live in
// internal/costumenames, because the server's costume discovery job has to reach the same
// conclusions about the same codes and the two must not drift. What stays here is the part that
// is this tool's alone: the whole-catalog sweep, the committed names.json cache, and the reporting
// that tells a human why a costume went unnamed.
//
// Dittobase is a SUGGESTION and CROSS-CHECK source, never an authority. Our curated labels win:
// they are user data, and adopting Dittobase's wording wholesale would rename existing labels and
// orphan the art on entries people have saved. Where we differ, we are usually right: they call
// Dawn's Hat "Rei's Cap".

import (
	"fmt"
	"os"
	"time"

	"pogo.hails.cc/internal/costumenames"
)

// namesPath is this tool's cache, committed so the steady state is zero traffic to their site and
// so -offline works on a fresh checkout. The server keeps its own cache elsewhere: it needs a
// negative cache this one deliberately does not have.
const namesPath = "cmd/synccostumes/names.json"

type names = costumenames.Names

func loadNames() names { return costumenames.Load(namesPath) }

func writeNames(n names) error { return costumenames.Save(namesPath, n) }

// nameOutcome is one (code, dex) pair we ended the run with no Dittobase name for, and why.
//
// The reason is kept per pair rather than tallied, because the aggregate counters it sits beside
// cannot tell "their pokedex has no page for this costume" apart from "two of our codes collide on
// one page, so naming either would be a guess" or from "we never asked", and those want completely
// different responses from a human. Every costume in the admin review queue today has an empty
// suggestion and nothing anywhere said which of them it was.
type nameOutcome struct {
	code   string
	dex    int
	reason string
}

// refreshNames fills in any (code, dex) pair we have no cached name for. Offline, or on any
// network failure, it just returns what is already cached. The second return is why each pair it
// could not name went unnamed, for the NO NAME report.
func refreshNames(cat *catalog, dexToName map[int]string, offline bool) (names, []nameOutcome) {
	cached := loadNames()

	var want []struct {
		code string
		dex  int
	}
	for code, e := range cat.Codes {
		for _, dex := range e.Dex {
			if cached.Get(code, dex) == "" {
				want = append(want, struct {
					code string
					dex  int
				}{code, dex})
			}
		}
	}
	if len(want) == 0 {
		return cached, nil
	}

	all := func(reason string) []nameOutcome {
		out := make([]nameOutcome, 0, len(want))
		for _, w := range want {
			out = append(out, nameOutcome{code: w.code, dex: w.dex, reason: reason})
		}
		return out
	}

	if offline {
		fmt.Printf("dittobase: %d name(s) uncached, skipped (-offline)\n", len(want))
		return cached, all("not attempted (-offline); re-run without -offline to find out why")
	}

	res, err := costumenames.NewResolver(httpc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dittobase: sitemap unavailable (%v); using cached names only\n", err)
		return cached, all(fmt.Sprintf("not attempted: the dittobase sitemap was unreachable this run (%v)", err))
	}
	slugs := res.Slugs()
	fmt.Printf("dittobase: %d pages listed, fetching %d uncached name(s)\n", len(slugs), len(want))

	// Ambiguity is judged across the WHOLE catalog, not just the uncached pairs: a collision with
	// a code we already have a name for is still a collision.
	codes := make(map[string][]int, len(cat.Codes))
	for code, e := range cat.Codes {
		codes[code] = e.Dex
	}
	ambiguous := costumenames.Ambiguous(codes, dexToName, slugs)

	var outcomes []nameOutcome
	note := func(w struct {
		code string
		dex  int
	}, reason string) {
		outcomes = append(outcomes, nameOutcome{code: w.code, dex: w.dex, reason: reason})
	}

	found, missed, skipped := 0, 0, 0
	for _, w := range want {
		if other, clash := ambiguous[costumenames.SlugKey(w.dex, w.code)]; clash {
			skipped++
			note(w, fmt.Sprintf("ambiguous: %s matches the same page, so naming either would be a guess", other))
			continue // two codes share this page; naming either from it would be a guess
		}
		species := dexToName[w.dex]
		if species == "" {
			missed++
			note(w, "no slug: the masterfile has no species name for this dex, so nothing could be looked up")
			continue
		}
		slug := res.Match(species, w.code)
		if slug == "" {
			missed++
			note(w, fmt.Sprintf("no page: tried %q exactly and as a prefix/suffix match",
				costumenames.Slugify(species)+"-"+costumenames.CodeSlug(w.code)))
			continue // editorial rename or no page; the masterfile name stands in
		}

		name, err := res.Fetch(slug, species)
		// Pace every request we actually make, not just the ones that worked. Every uncached pair
		// is retried on every run, and when they all miss (which is the state today) skipping the
		// delay on failure turns the polite path into a burst at their server.
		time.Sleep(costumenames.Delay)
		if err != nil {
			missed++
			note(w, fmt.Sprintf("page %q could not be read (%v)", slug, err))
			continue
		}
		if name == "" {
			missed++
			note(w, fmt.Sprintf("page %q has no og:title we could read", slug))
			continue
		}
		cached.Set(w.code, w.dex, name)
		found++
	}
	fmt.Printf("dittobase: %d named, %d without a page, %d ambiguous\n", found, missed, skipped)

	if err := writeNames(cached); err != nil {
		fmt.Fprintf(os.Stderr, "dittobase: could not write %s: %v\n", namesPath, err)
	}
	return cached, outcomes
}

// noNameReasons maps a costume code to why we have no Dittobase name for it.
//
// It feeds the REVIEW lines rather than a section of its own. A second list of the same 14 codes
// would double the length of a report whose baseline is meant to be quiet, and REVIEW is where
// someone is already reading about exactly these costumes.
//
// A code named on any one species is left out: suggestedLabel has something to offer, so nothing
// needs explaining.
func noNameReasons(cat *catalog, nm names, outcomes []nameOutcome) map[string]string {
	byCode := map[string][]string{}
	for _, o := range outcomes {
		byCode[o.code] = append(byCode[o.code], o.reason)
	}

	out := map[string]string{}
	for code, e := range cat.Codes {
		reasons := byCode[code]
		if len(reasons) == 0 {
			continue
		}
		named := false
		for _, dex := range e.Dex {
			if nm.Get(code, dex) != "" {
				named = true
				break
			}
		}
		if named {
			continue
		}

		detail := reasons[0]
		for _, r := range reasons[1:] {
			if r != detail {
				detail = "reasons differ by species, first: " + reasons[0]
				break
			}
		}
		out[code] = detail
	}
	return out
}
