package pogodata

import (
	"encoding/json"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The raid boss list from pokemon-go-api carries no dates at all: it is a snapshot
// of whatever LeekDuck's raid page said when it was last built, so at a rotation
// boundary it keeps serving the previous week's bosses until upstream gets around
// to rebuilding. On 2026-08-27 it still listed Lunala and Mega Swampert, both of
// which ended on the 25th.
//
// The events feed does know. Every rotation arrives as a "raid-battles" event with
// extraData.raidbattles.bosses and a real start and end, covering 5 star, Mega and
// Shadow raids. This file joins the two: the schedule decides who is live, the raid
// feed supplies the card, and where the schedule names a boss the raid feed has not
// caught up to, the card is built from the app's own species data.

// governedTiers are the tier keys the events feed describes. Tier 1 and tier 3
// rotate rarely and no feed anywhere carries timing for them, so they are passed
// through from upstream untouched.
var governedTiers = map[string]bool{"5": true, "6": true}

// Raid boss capture stats are fixed by the game: level 20, or level 25 when the
// weather is boosted, with IVs floored at 10. Computing at those levels reproduces
// the upstream CP ranges exactly (verified against Lunala, Pikachu and Impidimp in
// the live cache), which is what makes a synthesized card trustworthy.
const (
	raidCaptureLevel        = 20.0
	raidCaptureLevelBoosted = 25.0
	raidIVFloor             = 10
	raidIVCeiling           = 15
)

// raidCPMs are the CP multipliers for the two raid capture levels.
type raidCPMs struct{ Normal, Boosted float64 }

// defaultRaidCPMs are the level 20 and level 25 multipliers. They are fixed game
// constants and are also what the bundled cp_multipliers fallback carries, so they
// stand in when the live blob is missing or has not loaded yet.
var defaultRaidCPMs = raidCPMs{Normal: 0.5974000096321106, Boosted: 0.667934000492096}

type cpMultiplierRow struct {
	Level      float64 `json:"level"`
	Multiplier float64 `json:"multiplier"`
}

// raidCPMsFrom reads the two multipliers the raid card synthesizer needs out of the
// live cp_multipliers blob, falling back to the constants above per level so a
// truncated blob cannot produce a zero CP.
func raidCPMsFrom(cpMults json.RawMessage) raidCPMs {
	out := defaultRaidCPMs
	var rows []cpMultiplierRow
	if err := json.Unmarshal(cpMults, &rows); err != nil {
		return out
	}
	for _, r := range rows {
		switch r.Level {
		case raidCaptureLevel:
			if r.Multiplier > 0 {
				out.Normal = r.Multiplier
			}
		case raidCaptureLevelBoosted:
			if r.Multiplier > 0 {
				out.Boosted = r.Multiplier
			}
		}
	}
	return out
}

// WindowBoss is one boss named by a rotation event. This is exactly the shape the
// events feed uses under extraData.raidbattles.bosses.
type WindowBoss struct {
	Name       string `json:"name"`
	Image      string `json:"image"`
	CanBeShiny bool   `json:"canBeShiny"`
}

// RaidWindow is one scheduled rotation, resolved to the span during which it is
// live for somebody, anybody, on the planet.
//
// StartsUTC and EndsUTC are deliberately not the same reading of the feed's clock.
// See raidWindowSpan for why they cannot be.
type RaidWindow struct {
	EventID   string
	Name      string
	Tier      string
	Shadow    bool
	Bosses    []WindowBoss
	RawStart  string // verbatim feed string, so the browser can render it in the viewer's own zone
	RawEnd    string
	StartsUTC time.Time
	EndsUTC   time.Time
}

// Active reports whether the rotation is live for at least one trainer somewhere.
func (w RaidWindow) Active(now time.Time) bool {
	return !now.Before(w.StartsUTC) && now.Before(w.EndsUTC)
}

// UpcomingRaid is the serialized "up next" entry handed to the client.
type UpcomingRaid struct {
	EventID  string       `json:"event_id"`
	Name     string       `json:"name"`
	Tier     string       `json:"tier"`
	Shadow   bool         `json:"shadow,omitempty"`
	Bosses   []WindowBoss `json:"bosses"`
	StartsAt string       `json:"starts_at"`
	EndsAt   string       `json:"ends_at"`
	// Live marks a rotation whose window is already open but for which no full
	// boss card exists yet. That is the Mega case: the species data this app
	// carries has no Mega forms, so a Mega cannot be synthesized and has to wait
	// for the raid feed to list it.
	Live bool `json:"live,omitempty"`
}

// raidReconcileStats is the per rebuild tally, surfaced in the admin scraper check
// so a rule regression shows up as a number instead of a silently reshaped page.
type raidReconcileStats struct {
	Windows     int
	Active      int
	Dropped     int
	Synthesized int
	Annotated   int
	Pending     int // active rotations that could not be turned into a card
}

// raidFeedEntry is the slice of an events record this file needs.
type raidFeedEntry struct {
	EventID   string `json:"eventID"`
	Name      string `json:"name"`
	EventType string `json:"eventType"`
	Start     string `json:"start"`
	End       string `json:"end"`
	ExtraData *struct {
		RaidBattles *struct {
			Bosses []WindowBoss `json:"bosses"`
		} `json:"raidbattles"`
	} `json:"extraData"`
}

// parseRaidWindows turns the events feed into the rotation schedule.
//
// Only eventType "raid-battles" entries that actually name bosses are accepted.
// That filter is load bearing: "raid-hour" and "raid-day" spotlight a boss which
// is already live rather than defining a rotation, and treating one as a window
// would invent a phantom that starts and ends within an hour.
func parseRaidWindows(events json.RawMessage) []RaidWindow {
	if len(events) == 0 {
		return nil
	}
	var entries []raidFeedEntry
	if err := json.Unmarshal(events, &entries); err != nil {
		log.Printf("pogodata: raid schedule: events parse: %v", err)
		return nil
	}
	var out []RaidWindow
	for _, e := range entries {
		if e.EventType != "raid-battles" {
			continue
		}
		if e.ExtraData == nil || e.ExtraData.RaidBattles == nil || len(e.ExtraData.RaidBattles.Bosses) == 0 {
			continue
		}
		tier, shadow, ok := classifyRaidTier(e.EventID, e.Name)
		if !ok {
			log.Printf("pogodata: raid schedule: unclassified rotation %q, leaving its tier to upstream", e.EventID)
			continue
		}
		start, end, ok := raidWindowSpan(e.Start, e.End)
		if !ok {
			log.Printf("pogodata: raid schedule: unparseable window on %q (%q to %q)", e.EventID, e.Start, e.End)
			continue
		}
		out = append(out, RaidWindow{
			EventID:   e.EventID,
			Name:      e.Name,
			Tier:      tier,
			Shadow:    shadow,
			Bosses:    e.ExtraData.RaidBattles.Bosses,
			RawStart:  e.Start,
			RawEnd:    e.End,
			StartsUTC: start,
			EndsUTC:   end,
		})
	}
	return out
}

// raidWindowSpan resolves a rotation's feed timestamps into the widest span during
// which it is live for anyone on Earth.
//
// Rotation times float: "06:00" means six in the morning wherever the trainer is
// standing, not a single instant. So the rotation begins the moment 06:00 first
// arrives anywhere, which is in UTC+14, and it is still running until 22:00 finally
// arrives in the last zone, UTC-12. Reading both ends in one zone is what produces
// the two failures worth avoiding: a boss that vanishes while half the planet can
// still raid it, and a boss that has not appeared yet for anyone.
//
// The two readings overlap by 26 hours at every boundary. That overlap is real: the
// old and new rotations genuinely do coexist across the planet for a day.
//
// A "Z" timestamp is a true instant, and ParseFeedTime ignores the zone argument for
// one, so both readings collapse onto the same moment with no special case here.
func raidWindowSpan(rawStart, rawEnd string) (start, end time.Time, ok bool) {
	s, _, sOK := ParseFeedTime(rawStart, earliestOnEarth)
	e, _, eOK := ParseFeedTime(rawEnd, anywhereOnEarth)
	if !sOK || !eOK || !e.After(s) {
		return time.Time{}, time.Time{}, false
	}
	return s.UTC(), e.UTC(), true
}

var (
	megaRaidRe   = regexp.MustCompile(`mega[- ]raid`)
	starRaidRe   = regexp.MustCompile(`(\d)[- ]star[- ]raid`)
	shadowRaidRe = regexp.MustCompile(`shadow[- ]raid`)
	eliteRaidRe  = regexp.MustCompile(`elite[- ]raid`)
)

// classifyRaidTier reads the tier off the event slug, falling back to the display
// name. The feed carries no tier field of its own, and the slug is the most stable
// thing it does carry: "mega-gyarados-in-mega-raids-august-2026",
// "regirock-regice-registeel-in-5-star-raid-battles-august-2026".
//
// An unrecognised shape returns ok false and the rotation is skipped entirely, so an
// upstream renaming leaves that tier on the raid feed rather than emptying it.
func classifyRaidTier(eventID, name string) (tier string, shadow bool, ok bool) {
	hay := strings.ToLower(eventID + " " + name)
	shadow = shadowRaidRe.MatchString(hay)
	switch {
	case megaRaidRe.MatchString(hay):
		return "6", false, true
	case starRaidRe.MatchString(hay):
		return starRaidRe.FindStringSubmatch(hay)[1], shadow, true
	case shadow:
		// Shadow rotations are legendaries and read as 5 star in game, but the slug
		// says only "in-shadow-raids" with no number in it.
		return "5", true, true
	case eliteRaidRe.MatchString(hay):
		return "5", false, true
	}
	return "", false, false
}

var formeSuffixRe = regexp.MustCompile(`\s+formes?\s*\)`)

// normalizeBossName is the join key between the two feeds, which do not spell the
// same boss the same way. Inside a Shadow rotation the events feed says
// "Giratina (Altered)" while the raid feed says "Shadow Giratina (Altered Forme)".
// The shadow half is carried separately, by the rotation's own classification on one
// side and by the name prefix on the other, so it is stripped here.
func normalizeBossName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "shadow ")
	s = formeSuffixRe.ReplaceAllString(s, ")")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

// isShadowName reports whether a raid feed display name is a shadow boss.
// bossFromPGAPI encodes the shadow tier as a name prefix and keeps nothing else, so
// this is the only way back to the flag.
func isShadowName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "shadow ")
}

// bossKey identifies a boss across both feeds.
func bossKey(name string, shadow bool) string {
	if shadow {
		return "shadow:" + normalizeBossName(name)
	}
	return normalizeBossName(name)
}

// CPForLevel is the game's CP formula. It lives here rather than in the handlers
// package because the raid card synthesizer needs it and pogodata cannot import
// handlers; internal/handlers/iv.go calls through to it so there is still exactly
// one copy.
func CPForLevel(baseAtk, baseDef, baseSta, atkIV, defIV, staIV int, cpm float64) int {
	atk := float64(baseAtk + atkIV)
	def := float64(baseDef + defIV)
	sta := float64(baseSta + staIV)
	cp := int(math.Floor(atk * math.Sqrt(def) * math.Sqrt(sta) * cpm * cpm / 10))
	if cp < 10 {
		return 10
	}
	return cp
}

// speciesStats is everything needed to build a boss card from scratch.
type speciesStats struct {
	Types         []string
	Atk, Def, Sta int
}

// speciesLookup resolves a boss name to its stats and typing. ok is false when the
// species or its form is absent from the dataset, which is always the case for a
// Mega: pokemon.json and pokemon_types.json carry no Mega forms at all, and a Mega's
// typing differs from its base species (Mega Gyarados is Water and Dark, Gyarados is
// Water and Flying), so falling back to the base species would feed the counter
// calculator the wrong answer.
type speciesLookup func(name string) (speciesStats, bool)

type speciesStatRow struct {
	BaseAttack  int    `json:"base_attack"`
	BaseDefense int    `json:"base_defense"`
	BaseStamina int    `json:"base_stamina"`
	Form        string `json:"form"`
	PokemonName string `json:"pokemon_name"`
}

type speciesTypeRow struct {
	Form        string   `json:"form"`
	PokemonName string   `json:"pokemon_name"`
	Type        []string `json:"type"`
}

// newSpeciesLookup builds a lookup over the app's own species data. The index is
// built on first use, not eagerly: most rebuilds synthesize nothing at all, and
// parsing the full dex every five minutes to answer no questions would be waste.
//
// Two shapes of typing data are accepted because two exist. Upstream, and the
// embedded fallback file, send an array with one row per form. applyResult then
// flattens that to a species keyed map, preferring the Normal form, and that
// flattened map is what the store actually holds. Reading only one of the two would
// leave the synthesizer working in tests and typeless in production, or the reverse.
func newSpeciesLookup(pokemon, types json.RawMessage) speciesLookup {
	var (
		statsByKey     map[string]speciesStats
		formsBySpecies map[string][]string
		typeByKey      map[string][]string
		typeBySpecies  map[string][]string
		built          bool
	)
	build := func() {
		statsByKey = map[string]speciesStats{}
		formsBySpecies = map[string][]string{}
		typeByKey = map[string][]string{}
		typeBySpecies = map[string][]string{}

		var trows []speciesTypeRow
		if err := json.Unmarshal(types, &trows); err == nil {
			for _, r := range trows {
				sp := normalizeBossName(r.PokemonName)
				typeByKey[speciesFormKey(r.PokemonName, r.Form)] = r.Type
				// Same precedence applyResult uses when it flattens: first row
				// wins unless a Normal form turns up.
				if _, seen := typeBySpecies[sp]; !seen || r.Form == "Normal" {
					typeBySpecies[sp] = r.Type
				}
			}
		} else {
			var flat map[string][]string
			if err := json.Unmarshal(types, &flat); err != nil {
				log.Printf("pogodata: raid schedule: pokemon_types parse: %v", err)
			}
			for name, ts := range flat {
				typeBySpecies[normalizeBossName(name)] = ts
			}
		}

		var srows []speciesStatRow
		if err := json.Unmarshal(pokemon, &srows); err != nil {
			log.Printf("pogodata: raid schedule: pokemon parse: %v", err)
		}
		for _, r := range srows {
			if r.BaseAttack == 0 {
				continue
			}
			sp := normalizeBossName(r.PokemonName)
			form := normalizeBossName(r.Form)
			statsByKey[sp+"|"+form] = speciesStats{Atk: r.BaseAttack, Def: r.BaseDefense, Sta: r.BaseStamina}
			if !containsString(formsBySpecies[sp], form) {
				formsBySpecies[sp] = append(formsBySpecies[sp], form)
			}
		}
	}
	return func(name string) (speciesStats, bool) {
		if !built {
			build()
			built = true
		}
		species, want := splitSpeciesForm(normalizeBossName(name))
		form, ok := resolveSpeciesForm(species, want, formsBySpecies)
		if !ok {
			return speciesStats{}, false
		}
		st, ok := statsByKey[species+"|"+form]
		if !ok || st.Atk == 0 {
			return speciesStats{}, false
		}
		st.Types = typeByKey[species+"|"+form]
		if len(st.Types) == 0 {
			st.Types = typeBySpecies[species]
		}
		// A card with no typing is worse than no card: the type filter cannot see
		// it and the counter calculator has nothing to work from.
		return st, len(st.Types) > 0
	}
}

// resolveSpeciesForm picks which stat line a boss name refers to.
//
// The two feeds label forms differently: the events feed says
// "Zacian (Hero of Many Battles)" where the dataset says form "Hero". An exact match
// wins; otherwise a label that is a whole word prefix of the other will do, but only
// if exactly one form qualifies. Guessing between Zacian's two forms, or Giratina's,
// would hand back the wrong stats and the wrong typing.
func resolveSpeciesForm(species, want string, formsBySpecies map[string][]string) (string, bool) {
	forms := formsBySpecies[species]
	switch len(forms) {
	case 0:
		return "", false
	case 1:
		// Nothing to be ambiguous about, whatever the parenthetical says.
		return forms[0], true
	}
	for _, f := range forms {
		if f == want {
			return f, true
		}
	}
	var hit string
	n := 0
	for _, f := range forms {
		if formLabelsAgree(want, f) {
			hit, n = f, n+1
		}
	}
	if n != 1 {
		return "", false
	}
	return hit, true
}

// speciesFormKey is the index key for one stat line.
func speciesFormKey(name, form string) string {
	return normalizeBossName(name) + "|" + normalizeBossName(form)
}

// splitSpeciesForm pulls "giratina (altered)" apart into species and form label. A
// name with no parenthetical is the Normal form.
func splitSpeciesForm(key string) (species, form string) {
	open := strings.LastIndex(key, "(")
	if open < 0 || !strings.HasSuffix(key, ")") {
		return key, "normal"
	}
	return strings.TrimSpace(key[:open]), strings.TrimSpace(key[open+1 : len(key)-1])
}

// formLabelsAgree reports whether two form labels describe the same form, allowing
// one to be a whole word prefix of the other.
func formLabelsAgree(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+" ") || strings.HasPrefix(b, a+" ")
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// activeBoss pairs a boss the schedule says is live with the rotation that says so.
type activeBoss struct {
	boss   WindowBoss
	window RaidWindow
}

// reconcileRaids is the whole rule, kept pure so it can be tested without a store, a
// network or a clock. It returns the blob to serve, the "up next" list, and a tally.
//
// The fail open guard comes first and matters most: with no usable schedule the
// upstream blob is served exactly as it arrived, so the worst case is today's
// behaviour rather than an empty raids page. That is a different question from the
// per boss rule below, which drops an expired boss whether or not a replacement is
// known, because by then the schedule has been read successfully and it says the
// boss is gone.
func reconcileRaids(upstream json.RawMessage, windows []RaidWindow, now time.Time, lookup speciesLookup, cpms raidCPMs) (json.RawMessage, []UpcomingRaid, raidReconcileStats) {
	stats := raidReconcileStats{Windows: len(windows)}
	if len(windows) == 0 || len(upstream) == 0 {
		return upstream, nil, stats
	}
	var tiers map[string][]raidBoss
	if err := json.Unmarshal(upstream, &tiers); err != nil {
		log.Printf("pogodata: raid schedule: upstream raids parse: %v", err)
		return upstream, nil, stats
	}

	active := map[string]activeBoss{}
	governed := map[string]bool{}
	for _, w := range windows {
		if !governedTiers[w.Tier] {
			continue
		}
		for _, b := range w.Bosses {
			key := bossKey(b.Name, w.Shadow)
			governed[key] = true
			if !w.Active(now) {
				continue
			}
			// A boss can be named by two overlapping rotations at a changeover.
			// Keep the one that ends last, which is the incoming one.
			if prev, ok := active[key]; ok && prev.window.EndsUTC.After(w.EndsUTC) {
				continue
			}
			active[key] = activeBoss{boss: b, window: w}
		}
	}
	stats.Active = len(active)

	out := make(map[string][]raidBoss, len(tiers))
	carded := map[string]bool{}
	for tier, bosses := range tiers {
		if !governedTiers[tier] {
			out[tier] = bosses
			continue
		}
		kept := make([]raidBoss, 0, len(bosses))
		for _, b := range bosses {
			key := bossKey(b.PokemonName, isShadowName(b.PokemonName))
			if a, ok := active[key]; ok {
				annotateBoss(&b, a.window)
				stats.Annotated++
				carded[key] = true
				kept = append(kept, b)
				continue
			}
			if governed[key] {
				// The schedule named this boss and its window is shut. This is the
				// stale entry the whole exercise exists to remove.
				stats.Dropped++
				continue
			}
			// Never described by any rotation, so nothing here knows better than
			// upstream does. An Elite Raid, or anything new the events feed has no
			// vocabulary for, stays visible.
			kept = append(kept, b)
		}
		out[tier] = kept
	}

	// Anything the schedule says is live but upstream has not listed yet.
	for _, key := range sortedKeys(active) {
		if carded[key] {
			continue
		}
		a := active[key]
		rb, ok := synthesizeBoss(a.boss, a.window, lookup, cpms)
		if !ok {
			stats.Pending++
			continue
		}
		out[a.window.Tier] = append(out[a.window.Tier], rb)
		carded[key] = true
		stats.Synthesized++
	}

	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("pogodata: raid schedule: marshal: %v", err)
		return upstream, nil, stats
	}
	return data, buildUpcoming(windows, carded, now), stats
}

// annotateBoss stamps a boss card with the rotation that governs it. The raw feed
// strings are passed through untouched: they are floating wall clock readings, and
// the browser renders them in the viewer's own zone. Membership is decided here, on
// the union window; the countdown a trainer reads is their own local time.
func annotateBoss(b *raidBoss, w RaidWindow) {
	b.EventID = w.EventID
	b.StartsAt = w.RawStart
	b.EndsAt = w.RawEnd
}

// synthesizeBoss builds a card for a live boss the raid feed has not caught up to.
// It reports false when the species data cannot support one, which is the Mega case;
// the caller then leaves that rotation to the up next strip rather than putting a
// typeless, counterless card in the grid where the type filter cannot see it.
func synthesizeBoss(b WindowBoss, w RaidWindow, lookup speciesLookup, cpms raidCPMs) (raidBoss, bool) {
	if lookup == nil {
		return raidBoss{}, false
	}
	st, ok := lookup(b.Name)
	if !ok || len(st.Types) == 0 {
		return raidBoss{}, false
	}
	name := b.Name
	if w.Shadow && !isShadowName(name) {
		// bossFromPGAPI's convention, which currentBossTiers and the raid finder
		// boss picker both match on exactly.
		name = "Shadow " + name
	}
	rb := raidBoss{
		PokemonName:  name,
		ImageURL:     b.Image,
		Types:        st.Types,
		CanBeShiny:   b.CanBeShiny,
		CP:           CPForLevel(st.Atk, st.Def, st.Sta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Normal),
		CPMax:        CPForLevel(st.Atk, st.Def, st.Sta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Normal),
		CPBoostedMin: CPForLevel(st.Atk, st.Def, st.Sta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Boosted),
		CPBoostedMax: CPForLevel(st.Atk, st.Def, st.Sta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Boosted),
		Source:       "events",
	}
	annotateBoss(&rb, w)
	return rb, true
}

// buildUpcoming picks what to show under the grid: the soonest rotation per tier
// that has not started, plus any rotation that is live but has no card yet.
func buildUpcoming(windows []RaidWindow, carded map[string]bool, now time.Time) []UpcomingRaid {
	best := map[string]RaidWindow{}
	live := map[string]RaidWindow{}
	for _, w := range windows {
		if !governedTiers[w.Tier] {
			continue
		}
		group := w.Tier
		if w.Shadow {
			group += ":shadow"
		}
		if w.Active(now) {
			// Only interesting while none of its bosses made it onto the grid.
			anyCarded := false
			for _, b := range w.Bosses {
				if carded[bossKey(b.Name, w.Shadow)] {
					anyCarded = true
					break
				}
			}
			if !anyCarded {
				if prev, ok := live[group]; !ok || w.StartsUTC.After(prev.StartsUTC) {
					live[group] = w
				}
			}
			continue
		}
		if w.StartsUTC.Before(now) {
			continue // already over
		}
		if prev, ok := best[group]; !ok || w.StartsUTC.Before(prev.StartsUTC) {
			best[group] = w
		}
	}
	out := make([]UpcomingRaid, 0, len(best)+len(live))
	for _, w := range live {
		out = append(out, upcomingFrom(w, true))
	}
	for group, w := range best {
		if _, ok := live[group]; ok {
			// The live placeholder is the more urgent thing to say about this tier.
			continue
		}
		out = append(out, upcomingFrom(w, false))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Live != out[j].Live {
			return out[i].Live
		}
		if out[i].StartsAt != out[j].StartsAt {
			return out[i].StartsAt < out[j].StartsAt
		}
		return out[i].EventID < out[j].EventID
	})
	return out
}

func upcomingFrom(w RaidWindow, live bool) UpcomingRaid {
	return UpcomingRaid{
		EventID:  w.EventID,
		Name:     w.Name,
		Tier:     w.Tier,
		Shadow:   w.Shadow,
		Bosses:   w.Bosses,
		StartsAt: w.RawStart,
		EndsAt:   w.RawEnd,
		Live:     live,
	}
}

func sortedKeys(m map[string]activeBoss) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// nextRaidBoundary is the next instant at which the reconciliation could produce a
// different answer. Storing it lets the periodic rebuild be a clock comparison until
// a window actually opens or shuts, the same trick shinyBuiltFor plays with the day.
func nextRaidBoundary(windows []RaidWindow, now time.Time) time.Time {
	var next time.Time
	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, w := range windows {
		consider(w.StartsUTC)
		consider(w.EndsUTC)
	}
	return next
}

// rebuildRaidsLocked derives the served raid blob from the pristine upstream copy
// and the event schedule. Caller must hold s.mu.
//
// s.raids is DERIVED and never assigned anywhere else, the same arrangement
// rebuildShinyLocked uses for s.shinies. s.raidsUpstream holds what pokemon-go-api
// actually said, which is also what stays in cache/raids.json so the byte comparison
// in CheckScrapers keeps working.
func (s *Store) rebuildRaidsLocked() {
	now := time.Now()
	windows := parseRaidWindows(s.events)
	served, upcoming, stats := reconcileRaids(s.raidsUpstream, windows, now, newSpeciesLookup(s.pokemon, s.pokemonTypes), raidCPMsFrom(s.cpMults))
	s.raids = served
	s.raidSchedule = windows
	s.raidStats = stats
	s.raidsBuiltFor = nextRaidBoundary(windows, now)
	if len(upcoming) > 0 {
		if data, err := json.Marshal(upcoming); err == nil {
			s.raidsUpcoming = data
		}
	} else {
		s.raidsUpcoming = nil
	}
	if stats.Dropped > 0 || stats.Synthesized > 0 || stats.Pending > 0 {
		log.Printf("pogodata: raids: schedule reconciled (%d windows, %d dropped, %d synthesized, %d awaiting upstream)",
			stats.Windows, stats.Dropped, stats.Synthesized, stats.Pending)
	}
}

// maybeRebuildRaids re-derives the served blob only once a window boundary has been
// crossed. Called on a short ticker so a rotation opening or shutting takes effect
// without waiting on any upstream fetch.
func (s *Store) maybeRebuildRaids() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.raidsBuiltFor.IsZero() && time.Now().Before(s.raidsBuiltFor) {
		return
	}
	s.rebuildRaidsLocked()
}

// RaidsUpcoming is the "up next" list: the soonest rotation per tier, plus any that
// is live but has no card on the grid yet.
func (s *Store) RaidsUpcoming() json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.raidsUpcoming
}
