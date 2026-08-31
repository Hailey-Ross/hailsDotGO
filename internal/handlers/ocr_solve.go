package handlers

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"strings"
)

// solveEnv is everything solveScan needs that the reading itself does not carry.
//
// The game data arrives as already-unmarshalled slices rather than through the
// Handlers store, which is what lets the solve be a plain function: it can be
// exercised against the recorded fixtures directly, and it has no reason to
// reach a database or an HTTP request.
type solveEnv struct {
	pokeList   []pokemonStatEntry
	cpms       []cpmEntry
	evolutions json.RawMessage

	// trainerLevel bounds how far a Pokemon could have been powered up, so it
	// bounds the level sweep. It is the account's own value, never the client's.
	trainerLevel int

	// arc is the level read off the power-up arc, used to pin the level when the
	// CP is unusable and to rescue a misread CP.
	arc arcReading

	// lucky, shadow and purified are the status flags AS THE READER ASSERTED
	// THEM, before any inference from the dust value is folded back in.
	//
	// This is deliberately not read off the reading's own boolean fields, which
	// by the end of a scan carry the OR of what was read and what the dust
	// implies. Feeding those back into dustCandidates would let an inference
	// drawn from the dust go on to narrow the dust interpretations it came from.
	// nil means unknown, and dustCandidates keeps both readings in play for it.
	lucky, shadow, purified *bool

	// retryCP re-reads the CP off the source image, for the case where the first
	// solve matched nothing because the CP digits were misread.
	//
	// nil disables the retry, which is how the app path runs: there is no image
	// to re-read, and the app's own reading is not something to second-guess.
	// The image path also passes nil once its cheap pre-solve retry has fired,
	// so the expensive pass still runs at most once per scan.
	retryCP func() int

	// candyText is the recognised screen text, searched for a candy line when
	// the species did not resolve by name (a nicknamed Pokemon). Empty skips it.
	candyText string

	// form pins which variant of the species to solve against: Black rather than
	// Normal Kyurem, Origin rather than Altered Giratina. Empty means the caller
	// cannot tell, which is the image path's permanent state, and falls back to
	// the Normal-preferring lookup.
	//
	// It matters more than it looks. Form is a separate column upstream, so the
	// name alone does not distinguish the three Kyurem rows, whose attack stats
	// differ by 26%. Solving a Black Kyurem against Normal Kyurem does not
	// produce a slightly wrong answer, it usually produces no answer at all.
	form string

	// correctReading allows the solve to rewrite the reading it was handed:
	// normalising the dust, folding dust-implied status flags in, and replacing
	// a CP the arc rescue proved wrong.
	//
	// True for a scan the server itself read, where those corrections are the
	// server improving its own work. False for a reading submitted by the app,
	// where the app is the source of truth and the server's job is to solve and
	// store what it was given, not to talk back to a better reader. The solve
	// still reports arc_rescue and best_buddy_assumed either way, so the client
	// is told what happened and can decide for itself.
	correctReading bool
}

// solveScan turns a finished reading into a solved response.
//
// This is the half of an IV scan that does not care where the numbers came
// from. Given a CP, HP, dust cost, species and level arc it works out which IV
// spreads are consistent with all of them. It needs the species base stats, the
// CP multiplier table and the dust brackets to do that, which is exactly why it
// stays on the server when the reading itself moves to the device.
//
// The returned map is the response body: extracted, candidates, count and
// definitive always, plus pokemon, species_candidates, iv_summary,
// truncated_from, best_buddy_assumed and arc_rescue when they apply.
func solveScan(ext ocrExtracted, env solveEnv) map[string]any {
	dustRanges := dustCandidates(ext.RawDust, env.lucky, env.shadow, env.purified)
	normDust, dustLucky, dustShadow, dustPurified := summariseDustInterpretations(ext.RawDust, dustRanges)
	if env.correctReading {
		ext.NormalisedDust = normDust
		ext.IsLucky = ext.IsLucky || dustLucky
		ext.IsShadow = ext.IsShadow || dustShadow
		ext.IsPurified = ext.IsPurified || dustPurified
	}
	log.Printf("OCR dust: rawDust=%d interpretations=%d normDust=%d", ext.RawDust, len(dustRanges), normDust)

	// The wire field carries -1 for "could not read the appraisal", and 0 is a
	// real nought-star reading. The solver's field is a pointer where nil means
	// "do not filter on stars at all", so the sentinel is translated here and
	// nowhere else.
	var appraisalBars *int
	if ext.AppraisalBars >= 0 {
		bars := ext.AppraisalBars
		appraisalBars = &bars
	}

	baseReq := ivRequest{
		CP:            ext.CP,
		HP:            ext.HP,
		DustCost:      ext.RawDust,
		TrainerLevel:  env.trainerLevel,
		AppraisalBars: appraisalBars,
		IsLucky:       env.lucky,
		IsShadow:      env.shadow,
		IsPurified:    env.purified,
	}

	poke := findSpeciesForm(env.pokeList, ext.PokemonName, env.form)
	searchable := ext.HP > 0 && len(env.cpms) > 0 && (ext.CP > 0 || env.arc.OK)

	// Nickname fallback: no species matched by name, but the candy line names
	// the family base ("3 MACHOP CANDY" on a mon nicknamed "John Cena").
	// Disambiguate by which family member's stats actually fit the scan.
	var speciesCandidates []string
	if poke == nil && searchable && len(env.pokeList) > 0 && env.candyText != "" {
		knownSet := make(map[string]bool, len(env.pokeList))
		for i := range env.pokeList {
			knownSet[strings.ToLower(env.pokeList[i].PokemonName)] = true
		}
		base := detectCandyBase(env.candyText, func(n string) bool { return knownSet[strings.ToLower(n)] })
		if base != "" {
			type fit struct {
				poke  *pokemonStatEntry
				count int
			}
			var fits []fit
			for _, name := range familySpecies(base, env.evolutions) {
				sp := findSpecies(env.pokeList, name)
				if sp == nil {
					continue
				}
				req := baseReq
				req.PokemonName = sp.PokemonName
				if cands, _, _ := runOCRSearch(req, dustRanges, env.arc, *sp, env.cpms); len(cands) > 0 {
					fits = append(fits, fit{sp, len(cands)})
				}
			}
			sort.Slice(fits, func(i, j int) bool { return fits[i].count < fits[j].count })
			if len(fits) > 0 {
				poke = fits[0].poke
				ext.PokemonName = titleCase(poke.PokemonName)
				ext.NameSource = "candy"
				for _, f := range fits {
					speciesCandidates = append(speciesCandidates, titleCase(f.poke.PokemonName))
				}
			}
			log.Printf("OCR candy: base=%q familyFits=%d", base, len(fits))
		}
	}

	resp := map[string]any{
		"candidates": []IVCandidate{},
		"count":      0,
		"definitive": false,
	}
	if len(speciesCandidates) > 1 {
		resp["species_candidates"] = speciesCandidates
	}

	if searchable && poke != nil {
		resp["pokemon"] = poke
		req := baseReq
		req.PokemonName = ext.PokemonName
		candidates, buddyAssumed, arcRescue := runOCRSearch(req, dustRanges, env.arc, *poke, env.cpms)

		// The primary CP read produced no exact match (empty or arc-rescued):
		// pay for the contrast-enhanced re-OCR of the CP zone before settling.
		// A partial read like "CP17" for 1790 passes the primary validity
		// check, so the cheap retry never fired earlier in the handler.
		if (len(candidates) == 0 || arcRescue) && ext.CP > 0 && env.retryCP != nil {
			if rv := env.retryCP(); rv >= 10 && rv != ext.CP {
				retryReq := req
				retryReq.CP = rv
				if c2, b2, r2 := runOCRSearch(retryReq, dustRanges, env.arc, *poke, env.cpms); len(c2) > 0 && !r2 {
					log.Printf("OCR CP retry recovered: primary=%d retry=%d candidates=%d", ext.CP, rv, len(c2))
					candidates, buddyAssumed, arcRescue = c2, b2, false
					ext.CP = rv
					ext.RawCP = ext.RawCP + "/" + strconv.Itoa(rv)
				}
			}
		}
		if arcRescue && env.correctReading {
			// The rescue proved the text CP wrong (no IV spread matched it).
			// Correct the extracted card: show the rescued CP when the
			// candidates agree on one, else clear it. raw_cp keeps the misread
			// for debugging.
			corrected := unanimousCP(candidates)
			log.Printf("OCR arc rescue: textCP=%d correctedCP=%d candidates=%d", ext.CP, corrected, len(candidates))
			ext.CP = corrected
			ext.CPSource = "arc-level"
		}

		fullCount := len(candidates)
		if fullCount > 0 {
			// Aggregate view for wide (CP-free) result sets.
			resp["iv_summary"] = map[string]any{
				"max_pct": candidates[0].IVPct,
				"min_pct": candidates[fullCount-1].IVPct,
			}
		}
		if fullCount > 100 {
			resp["truncated_from"] = fullCount
			candidates = candidates[:100]
		}
		resp["candidates"] = candidates
		resp["count"] = fullCount
		resp["definitive"] = fullCount == 1
		resp["best_buddy_assumed"] = buddyAssumed
		resp["arc_rescue"] = arcRescue
	}

	// Set last, so it carries every correction the solve made above rather than
	// a copy taken before them.
	resp["extracted"] = ext
	return resp
}
