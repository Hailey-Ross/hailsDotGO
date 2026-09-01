package pogodata

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
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
//
// There is a third source, in eventraids.go. Some rotations are announced only in
// the body of an ordinary event's page, with no feed entry modelling them at all:
// Mega Ascension named a different Mega line up for each of five days that way, and
// nothing here could see any of it. Those windows are ADDITIVE, meaning they add
// bosses and can never remove one, and everything below treats them like any other
// rotation apart from that.

// governedTiers are the tier keys the schedule may speak for.
//
// It was 5 and 6 alone, on the reasoning that tier 1 and tier 3 rotate rarely and no
// feed carries timing for them. Both halves of that turned out to be wrong. The LEGO
// event page names Pikachu in 1-star raids with real dates, which this app was
// already scraping and then discarding; and classifyRaidTier happily builds a tier 1
// or tier 3 window off a slug, which activeBosses then dropped silently, with no log
// line, because the tier was not in this map. Rarely is also not never, so tiers 1
// and 3 were the two left exposed to exactly the upstream staleness this whole file
// exists to correct.
var governedTiers = map[string]bool{"1": true, "3": true, "5": true, "6": true}

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
	// Additive marks a rotation read off an event's scraped page rather than out of
	// the feed's own raid data. See eventraids.go.
	//
	// It contributes live bosses exactly like any other window but never makes its
	// tier authoritative, so an event page can add a boss to the grid and can never
	// remove one. Mega Ascension is why: its page names a week of Mega line ups the
	// feed does not model at all, while saying in as many words that seasonal
	// bosses keep appearing alongside them, so reading it as the authority on the
	// Mega tier would have deleted the Mega Gyarados rotation that was still live.
	Additive bool
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
	// boss card exists yet, so the strip names it rather than leaving the tier
	// looking empty.
	//
	// This used to be the ordinary fate of every Mega, because nothing in the
	// species data describes one. It is now the exception: refreshMegas supplies
	// the stat lines and typings, so a Mega is built like anything else. What
	// still lands here is a species no dataset knows at all, such as Armored
	// Mewtwo, or a Mega reaching the schedule before the Mega table has loaded.
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
	// EventWindows is how many of the live rotations were read off an event's
	// scraped page rather than out of the feed's raid data, and FromEventPages how
	// many cards on the grid one of those is responsible for. Both are surfaced in
	// the admin scraper check, because the event page reader is the one part of
	// this that depends on upstream markup: if LeekDuck reshapes a Raids section
	// these go to zero, and a number going to zero is something a person can see.
	EventWindows   int
	FromEventPages int
	// Suppressed is how many governed groups an event page note is silencing right
	// now, and SuppressedWindows how many live feed rotations that removed from the
	// grid. Dropped keeps counting the upstream cards a suppression deleted, because
	// Dropped already means "the schedule says this is gone" and splitting it would
	// leave two numbers that have to be added together to check anything.
	//
	// These exist because suppression is the only rule in this package that can
	// empty a whole tier. A tier going quietly missing must be a number on the admin
	// screen, not something found by reading the served JSON.
	Suppressed        int
	SuppressedWindows int
	// Upcoming is how many rotations the up next list published. The fold in
	// buildUpcoming is now the only thing standing between a scraped rotation and
	// the page, and a fold that quietly collapses to one entry looks exactly like a
	// scraper that has stopped finding anything, so it has to be a number on the
	// admin screen rather than something found by reading the served JSON.
	Upcoming int
	// SuppressionDisarmed records the circuit breaker firing: a note was parsed, and
	// applying it would have left every governed tier empty, so it was ignored.
	// Always worth saying out loud, because it means one of the two event page
	// readers is working and the other is not.
	SuppressionDisarmed bool
	// PendingList is the same thing named. Pending was a count, and a count only
	// ever reached a log line: the set itself was discarded on every rebuild, so
	// the only thing that would ever fix one of these was the next scheduled
	// refresh, up to a couple of hours away, with the site showing a raid tier
	// missing a boss for the whole of it and saying nothing.
	PendingList []RaidPending
}

// RaidPending is one rotation the events feed says is live right now that could
// not be turned into a card.
//
// Exported because it outlives the rebuild that produced it: the store keeps the
// set, persists it beside the other cache blobs, retries it on a backoff, and
// shows it in the admin scraper check.
type RaidPending struct {
	Species string `json:"species"`
	Tier    string `json:"tier"`
	Shadow  bool   `json:"shadow,omitempty"`
	EventID string `json:"event_id"`
	Name    string `json:"name"`
	// Reason is why synthesizeBoss gave up, so a reader can tell "we have never
	// heard of this species" from "this is a Mega and the Mega table has not
	// landed yet". The two are fixed by different fetches.
	Reason    string    `json:"reason"`
	StartsUTC time.Time `json:"starts_utc"`
	EndsUTC   time.Time `json:"ends_utc"`
}

// Expired reports whether the rotation has ended. A rotation that finished while
// still pending is history, not work, and retrying it forever would mean the
// backoff never resets.
func (p RaidPending) Expired(now time.Time) bool { return !now.Before(p.EndsUTC) }

// pendingReason classifies why a boss could not be built, from the same inputs
// synthesizeBoss had.
//
// A Mega is worth telling apart because it is the common case and it has its own
// fix: this app's species blob carries no Mega forms, so a Mega rotation waits on
// refreshMegas or on upstream listing it, not on anything about the base species.
func pendingReason(name string, lookup speciesLookup) string {
	if lookup == nil {
		return "no species data loaded"
	}
	if isMegaName(name) {
		if st, _ := lookup(name); st.MegaTableEmpty {
			return "mega stats not loaded"
		}
		// The table is loaded and this Mega is not in it, which no refresh on our
		// side fixes: it waits on pokemon-go-api publishing the form.
		return "not in the mega table yet"
	}
	if st, ok := lookup(name); !ok {
		return "species unknown"
	} else if len(st.Types) == 0 {
		return "species has no typing"
	}
	return "could not build a card"
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
	primalRaidRe = regexp.MustCompile(`primal[- ]raid`)
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
	case megaRaidRe.MatchString(hay), primalRaidRe.MatchString(hay):
		// Primal rides the Mega tier, the same reading isMegaName applies everywhere
		// else in this package.
		return "6", false, true
	case starRaidRe.MatchString(hay):
		return starRaidRe.FindStringSubmatch(hay)[1], shadow, true
	case eventHeadingWordStarRe.MatchString(hay):
		// "five-star" rather than "5-star". eventraids.go has read both spellings
		// since it was written, because LeekDuck writes "Five-star Raids" as a
		// heading; this reader knew only digits, so a slug in words classified as
		// nothing and its whole roster was dropped on the floor.
		return starWords[eventHeadingWordStarRe.FindStringSubmatch(hay)[1]], shadow, true
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

// isMegaName reports whether a raid feed display name is a Mega boss.
//
// The same trick isShadowName uses, and for the same reason: the feed encodes the
// form in the display name and keeps nothing else. Primal Groudon and Kyogre ride
// the Mega tier and live in the same Mega pokedex slice, so they count here too.
func isMegaName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(n, "mega ") || strings.HasPrefix(n, "primal ")
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

// megaForm is one Mega's stat line and typing, trimmed out of pokemon-go-api's
// Mega pokedex slice.
//
// Megas need their own source because nothing else the app carries describes them.
// pokemon.json and pokemon_types.json have no Mega rows at all, and a Mega cannot be
// derived from its base species either: it keeps the base stamina but its attack,
// defense and typing all change (Mega Gyarados is 292/247/216 and Water plus Dark,
// where Gyarados is 237/186/216 and Water plus Flying).
type megaForm struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
	Atk   int      `json:"atk"`
	Def   int      `json:"def"`
	Sta   int      `json:"sta"`
	Image string   `json:"image,omitempty"`
	// BaseAtk, BaseDef and BaseSta are the BASE species' stat line, lifted from the
	// same pokedex record the Mega was nested inside.
	//
	// A Mega raid boss is fought as the Mega and caught as the base species, so the
	// card's typing comes from the three fields above and its CP range from these.
	// Upstream settles it: the Mega Latios card pokemon-go-api publishes reads
	// 2090 to 2178, which is base Latios at level 20, not Mega Latios' 2758 to 2861.
	//
	// json:"-" on all three deliberately. This struct is a wire type, served as
	// "megas" on /api/data and read by the browser's counter table and by the app,
	// and the served shape has to stay byte identical. Nothing outside the card
	// synthesizer wants these: the counters divide by the MEGA's defense, which is
	// the whole reason this table exists.
	BaseAtk int `json:"-"`
	BaseDef int `json:"-"`
	BaseSta int `json:"-"`
}

// megaPokedexEntry is the slice of a pokemon-go-api pokedex record this file needs.
type megaPokedexEntry struct {
	// Stats is the BASE species' line, which is what a Mega raid boss is caught as.
	Stats struct {
		Stamina int `json:"stamina"`
		Attack  int `json:"attack"`
		Defense int `json:"defense"`
	} `json:"stats"`
	MegaEvolutions map[string]struct {
		Names struct {
			English string `json:"English"`
		} `json:"names"`
		Stats struct {
			Stamina int `json:"stamina"`
			Attack  int `json:"attack"`
			Defense int `json:"defense"`
		} `json:"stats"`
		PrimaryType *struct {
			Type string `json:"type"`
		} `json:"primaryType"`
		SecondaryType *struct {
			Type string `json:"type"`
		} `json:"secondaryType"`
		Assets struct {
			Image string `json:"image"`
		} `json:"assets"`
	} `json:"megaEvolutions"`
}

// parseMegaForms trims the upstream payload down to what a boss card needs, keyed by
// the same normalized name the raid and events feeds are joined on.
//
// The feed lists a species once per form, so the same Mega appears more than once
// (Charizard X and Y each turn up twice). Identical repeats, so first one wins.
func parseMegaForms(data json.RawMessage) map[string]megaForm {
	var entries []megaPokedexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("pogodata: megas: parse: %v", err)
		return nil
	}
	out := map[string]megaForm{}
	for _, e := range entries {
		for _, m := range e.MegaEvolutions {
			if m.Names.English == "" || m.Stats.Attack == 0 {
				continue
			}
			key := normalizeBossName(m.Names.English)
			if _, seen := out[key]; seen {
				continue
			}
			var types []string
			if m.PrimaryType != nil {
				if t := pgapiTypeName(m.PrimaryType.Type); t != "" {
					types = append(types, t)
				}
			}
			if m.SecondaryType != nil {
				if t := pgapiTypeName(m.SecondaryType.Type); t != "" {
					types = append(types, t)
				}
			}
			if len(types) == 0 {
				continue // a card with no typing is not worth building; see speciesLookup
			}
			out[key] = megaForm{
				Name:    m.Names.English,
				Types:   types,
				Atk:     m.Stats.Attack,
				Def:     m.Stats.Defense,
				Sta:     m.Stats.Stamina,
				Image:   m.Assets.Image,
				BaseAtk: e.Stats.Attack,
				BaseDef: e.Stats.Defense,
				BaseSta: e.Stats.Stamina,
			}
		}
	}
	return out
}

// pgapiTypeName turns "POKEMON_TYPE_WATER" into "Water", which is how every other
// type string in this app is spelled.
func pgapiTypeName(raw string) string {
	t := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(raw)), "POKEMON_TYPE_")
	if t == "" {
		return ""
	}
	return strings.ToUpper(t[:1]) + strings.ToLower(t[1:])
}

// speciesStats is everything needed to build a boss card from scratch.
//
// Atk, Def and Sta are what the boss FIGHTS with. CatchAtk, CatchDef and CatchSta
// are what a trainer catches afterwards, and they differ for exactly one kind of
// boss: a Mega or a Primal is battled as the Mega and caught as the base species.
// Zero means "the same as the battle line", which is every ordinary species.
type speciesStats struct {
	Types                        []string
	Atk, Def, Sta                int
	CatchAtk, CatchDef, CatchSta int
	// Image is a last resort sprite for a boss whose rotation carried none, which
	// is every boss read out of an event page's prose rather than its markup. Only
	// the Mega table has one to offer.
	Image string
	// MegaTableEmpty is set on a FAILED Mega lookup when the Mega table holds
	// nothing at all, which is a different problem from a Mega the table has simply
	// never heard of. Carried on the zero value rather than through a wider
	// signature because pendingReason is the only thing that wants it, and telling
	// an admin to wait for refreshMegas when the table already holds sixty one forms
	// sends them after a fetch that will never help. Mega Staraptor is the live case.
	MegaTableEmpty bool
}

// catchLine is the stat line a boss's CP range is computed from.
func (s speciesStats) catchLine() (atk, def, sta int) {
	if s.CatchAtk == 0 || s.CatchDef == 0 || s.CatchSta == 0 {
		return s.Atk, s.Def, s.Sta
	}
	return s.CatchAtk, s.CatchDef, s.CatchSta
}

// speciesLookup resolves a boss name to its stats and typing. ok is false when the
// species is absent from every dataset, in which case no card is built at all rather
// than one carrying a guess.
//
// Megas are answered from their own source (see megaForm). Falling back to the base
// species for one would be worse than refusing: the typing is different, so the
// counter table would rank against the wrong weaknesses entirely.
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
func newSpeciesLookup(pokemon, types json.RawMessage, megas map[string]megaForm) speciesLookup {
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
		key := normalizeBossName(name)
		// Megas first, and never fall through to the base species on a miss: a Mega
		// this app has no data for must produce no card.
		//
		// isMegaName rather than a "mega " prefix test, because Primal Kyogre and
		// Primal Groudon ride the Mega tier and live in the same Mega table. They were
		// indexed correctly and then unreachable, and pendingReason would have told an
		// admin the Mega stats had not loaded when they had.
		if isMegaName(key) {
			m, ok := megas[key]
			if !ok {
				return speciesStats{MegaTableEmpty: len(megas) == 0}, false
			}
			// The base line rides along so the card can be fought as the Mega and
			// caught as the base species. See speciesStats and megaForm.
			return speciesStats{
				Types: m.Types, Atk: m.Atk, Def: m.Def, Sta: m.Sta,
				CatchAtk: m.BaseAtk, CatchDef: m.BaseDef, CatchSta: m.BaseSta,
				Image: m.Image,
			}, true
		}
		if !built {
			build()
			built = true
		}
		species, want := splitSpeciesForm(key)
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

// speciesFormPrefixes are the forms both feeds write as a leading adjective while
// the dataset carries them as a form label.
//
// Shadow and Mega are already handled, by normalizeBossName and isMegaName, and
// nothing knew there was a third kind. Armored Mewtwo is why it matters: it is the
// only five star raid of the GO Fest Mega Finale weekend, its stats and typing have
// been sitting in pokemon.json all along under form "A", and the grid showed an
// empty tier because "armored mewtwo" was read as a species nobody has heard of.
//
// The regional prefixes are here for the same reason rather than on spec: a Hisuian
// or Galarian boss in a governed tier would have failed identically the first time
// one needed building.
//
// "paldean" deliberately maps to the stem: Tauros has three Paldean forms, none of
// them exactly "paldea", so resolveSpeciesForm finds three candidates and refuses.
// Guessing which one would put the wrong typing on the card.
var speciesFormPrefixes = map[string]string{
	"armored":  "a",
	"alolan":   "alola",
	"galarian": "galarian",
	"hisuian":  "hisuian",
	"paldean":  "paldea",
}

// splitSpeciesForm pulls "giratina (altered)" apart into species and form label. A
// name with no parenthetical is the Normal form, unless it opens with one of the
// adjectives above.
func splitSpeciesForm(key string) (species, form string) {
	open := strings.LastIndex(key, "(")
	if open < 0 || !strings.HasSuffix(key, ")") {
		if word, rest, ok := strings.Cut(key, " "); ok {
			if form, named := speciesFormPrefixes[word]; named {
				return strings.TrimSpace(rest), form
			}
		}
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

// raidGroupKey identifies one schedulable group: a tier, and whether it is the
// shadow half of that tier.
func raidGroupKey(tier string, shadow bool) string {
	if shadow {
		return tier + ":shadow"
	}
	return tier
}

// activeBoss pairs a boss the schedule says is live with the rotation that says so.
type activeBoss struct {
	boss   WindowBoss
	window RaidWindow
}

// rawDayRange reads a window's stated calendar days off its floating wall clock
// strings, which is the only reading the two sources can be compared on.
//
// StartsUTC and EndsUTC deliberately are NOT comparable that way: raidWindowSpan
// reads the start in UTC+14 and the end in UTC-12, so two windows describing
// consecutive days overlap by 26 hours and their UTC fields say so. The strings do
// not, and "which day is this rotation for" is a question about the strings.
//
// ok is false when either string is not a date, which is the case in tests that
// build a window from its UTC fields alone; preferRaidWindow falls back to those.
func rawDayRange(w RaidWindow) (start, end string, ok bool) {
	day := func(raw string) (string, bool) {
		if len(raw) < 10 || raw[4] != '-' || raw[7] != '-' {
			return "", false
		}
		return raw[:10], true
	}
	s, sOK := day(w.RawStart)
	e, eOK := day(w.RawEnd)
	return s, e, sOK && eOK
}

// raidZoneOffsets is every whole hour UTC offset a trainer can be standing in,
// UTC-12 through UTC+14. The half hour and three quarter hour zones are left out:
// this is a measure of how much of the planet a rotation is currently true for, and
// India moving the answer by a fraction of a zone would not change any comparison.
const (
	raidZoneWest = -12
	raidZoneEast = 14
)

// windowZoneReach counts the time zones whose local clock is inside this rotation's
// stated window at this instant.
//
// This is the honest form of the question "which day is it". A rotation's times are
// a floating wall clock, so at 12:00 UTC on 4 September it is the 4th nearly
// everywhere and the 5th in two zones past the date line. A window for the 4th
// therefore reaches 23 zones and a window for the 5th reaches 2, and the first is
// the one a trainer should be reading.
//
// ok is false for a window with no usable wall clock, and for a "Z" timestamp, which
// is a true instant rather than a floating reading and would make the shifted
// comparison below meaningless.
func windowZoneReach(w RaidWindow, now time.Time) (int, bool) {
	if strings.HasSuffix(w.RawStart, "Z") || strings.HasSuffix(w.RawEnd, "Z") {
		return 0, false
	}
	s, _, sOK := ParseFeedTime(w.RawStart, time.UTC)
	e, _, eOK := ParseFeedTime(w.RawEnd, time.UTC)
	if !sOK || !eOK {
		return 0, false
	}
	n := 0
	for off := raidZoneWest; off <= raidZoneEast; off++ {
		local := now.UTC().Add(time.Duration(off) * time.Hour)
		if !local.Before(s) && local.Before(e) {
			n++
		}
	}
	return n, true
}

// preferRaidWindow decides which of two live rotations naming the same boss gets to
// label its card, and reports whether the candidate should displace the one already
// held.
//
// It is a lexicographic order over five scalars, and being a total order is a
// requirement rather than a nicety. An earlier version of this expressed the same
// intent as a ladder of pairwise cases (contains, disjoint, ends last) and was NOT
// transitive: an adversarial sweep of every day range triple that can be live at
// once found 5544 triples where the winner depended on the order the windows
// happened to be appended in. A comparator that can cycle cannot be tested, because
// the answer is a property of the slice rather than of the windows.
//
// The five, in order:
//
//  1. Zone reach, highest first. This is the fix that matters and the reason the
//     function needs the clock. raidWindowSpan reads a start in UTC+14 and an end in
//     UTC-12, so consecutive day windows are both live for 26 hours; under a plain
//     "ends last" rule that handed today's card to tomorrow's window. On Friday
//     4 September, Mega Raichu X was labelled with the GO Fest SATURDAY habitat
//     window it also appears in, while Mega Raichu Y, which is on the Sunday list
//     instead, kept its own Friday window: two bosses out of one list, dated a day
//     apart. Friday reaches 23 zones at that instant and Saturday reaches 2.
//  2. The later stated end DAY. This is the changeover case: two rotations genuinely
//     overlap and the incoming one answers "how much longer do I have". Compared as
//     a day, not an instant, so an event page's whole day approximation and a feed
//     entry's real 22:00 close are not treated as different answers.
//  3. The feed over an event page. Same last day means nothing is lost either way,
//     and the feed entry is the more precise source: its event id names the rotation
//     itself rather than the event the rotation sits inside. Armored Mewtwo is the
//     live example, described identically by the GO Fest page and by its own
//     raid-battles entry, and Mega Beedrill is the second.
//  4. The earlier stated start day, so the wider rotation is the label.
//  5. The event id, so a pair identical in every way this can see still has one
//     answer rather than "whichever was appended first".
//
// A window built without its feed strings falls back to comparing EndsUTC, which is
// the whole of what this used to do.
func preferRaidWindow(candidate, held RaidWindow, now time.Time) bool {
	if cr, cOK := windowZoneReach(candidate, now); cOK {
		if hr, hOK := windowZoneReach(held, now); hOK && cr != hr {
			return cr > hr
		}
	}
	cs, ce, cOK := rawDayRange(candidate)
	hs, he, hOK := rawDayRange(held)
	if !cOK || !hOK {
		if !candidate.EndsUTC.Equal(held.EndsUTC) {
			return candidate.EndsUTC.After(held.EndsUTC)
		}
		if held.Additive != candidate.Additive {
			return held.Additive
		}
		return candidate.EventID < held.EventID
	}
	if ce != he {
		return ce > he
	}
	if held.Additive != candidate.Additive {
		return held.Additive
	}
	if cs != hs {
		return cs < hs
	}
	return candidate.EventID < held.EventID
}

// anyAdditiveWindow reports whether the event page reader produced anything at all.
func anyAdditiveWindow(windows []RaidWindow) bool {
	for _, w := range windows {
		if w.Additive {
			return true
		}
	}
	return false
}

// activeBosses folds the schedule down to what is live right now: every boss a
// window names, and which groups the feed is currently the authority on.
//
// A group is one tier's worth of one kind of raid: tier 5 normal and tier 5 shadow
// are folded separately, because the feed schedules them separately and one being
// described says nothing about the other.
//
// It is a function rather than an inline loop solely so the suppression can be
// disarmed and the fold redone without the rest of reconcileRaids knowing.
func activeBosses(windows []RaidWindow, sups []RaidSuppression, now time.Time) (
	active map[string]activeBoss, authoritative map[string]bool, eventWindows, suppressedWindows int) {

	active = map[string]activeBoss{}
	authoritative = map[string]bool{}
	for _, w := range windows {
		if !governedTiers[w.Tier] || !w.Active(now) {
			continue
		}
		group := raidGroupKey(w.Tier, w.Shadow)
		if silencedBy(sups, w, now) {
			// An event page says in as many words that this rotation is not running.
			// It contributes no live boss and is the authority on nothing, so its
			// bosses are neither annotated onto an upstream card nor synthesized into
			// one. That is what removes the Regi trio, which existed on the grid ONLY
			// as a synthesis of this window: upstream had never listed it.
			//
			// Which rotations a note reaches is RaidSuppression.Silences, and it is
			// narrower than "the group is named": a rotation opening inside the note's
			// span is one of the replacements it promised, not a casualty of it.
			suppressedWindows++
			continue
		}
		if w.Additive {
			// Read off an event's page rather than out of the feed's raid data. It
			// says what IS running, never what is not, so it adds bosses without
			// taking the group over. See RaidWindow.Additive.
			eventWindows++
		} else {
			// The schedule has something to say about this group right now, so it is
			// the authority on the whole of it. See the drop rule in reconcileRaids.
			authoritative[group] = true
		}
		for _, b := range w.Bosses {
			key := bossKey(b.Name, w.Shadow)
			if prev, ok := active[key]; ok && !preferRaidWindow(w, prev.window, now) {
				continue
			}
			active[key] = activeBoss{boss: b, window: w}
		}
	}
	return active, authoritative, eventWindows, suppressedWindows
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
func reconcileRaids(upstream json.RawMessage, windows []RaidWindow, suppressions []RaidSuppression, now time.Time, lookup speciesLookup, cpms raidCPMs) (json.RawMessage, []UpcomingRaid, raidReconcileStats) {
	stats := raidReconcileStats{Windows: len(windows)}
	if len(windows) == 0 || len(upstream) == 0 {
		return upstream, nil, stats
	}
	var tiers map[string][]raidBoss
	if err := json.Unmarshal(upstream, &tiers); err != nil {
		log.Printf("pogodata: raid schedule: upstream raids parse: %v", err)
		return upstream, nil, stats
	}

	// Which groups an event page is currently silencing, read before anything else
	// because it changes what "the schedule says is live" even means.
	suppressed := map[string]bool{}
	for _, sp := range suppressions {
		if !sp.Active(now) {
			continue
		}
		for _, g := range sp.Groups {
			suppressed[g] = true
		}
	}

	active, authoritative, eventWindows, suppressedWindows := activeBosses(windows, suppressions, now)

	// Two ways to decide the note reader is misfiring, and neither is subtle.
	//
	// No additive window anywhere means the Raids section reader found nothing on any
	// page, while the note reader found a note. A page that suspends the seasonal
	// rotations names their replacements in the same breath, so that combination is
	// one of the two readers working and the other not, which is exactly the shape a
	// LeekDuck markup change makes. Note this counts every additive window in the
	// schedule, not just the live ones: whether some other event happens to have a
	// rotation open today says nothing about whether this reader still works.
	//
	// Nothing live at all in any governed tier is the broader net behind it.
	//
	// Known limitation, and it is the reason the parser itself has to fail closed
	// rather than lean on this: neither test can see a note that wrongly empties one
	// tier while another stays populated. A safety net here is not a substitute for
	// not parsing the note wrongly in the first place.
	if len(suppressed) > 0 && (len(active) == 0 || !anyAdditiveWindow(windows)) {
		log.Printf("pogodata: raids: a suppression would empty every governed tier, ignoring it")
		suppressed = nil
		suppressions = nil
		stats.SuppressionDisarmed = true
		active, authoritative, eventWindows, suppressedWindows = activeBosses(windows, nil, now)
	}
	stats.Active = len(active)
	stats.EventWindows = eventWindows
	stats.Suppressed = len(suppressed)
	stats.SuppressedWindows = suppressedWindows

	out := make(map[string][]raidBoss, len(tiers))
	carded := map[string]bool{}
	for tier, bosses := range tiers {
		if !governedTiers[tier] {
			out[tier] = bosses
			continue
		}
		kept := make([]raidBoss, 0, len(bosses))
		for _, b := range bosses {
			shadow := isShadowName(b.PokemonName)
			key := bossKey(b.PokemonName, shadow)
			if a, ok := active[key]; ok {
				annotateBoss(&b, a.window)
				stats.Annotated++
				if a.window.Additive {
					stats.FromEventPages++
				}
				carded[key] = true
				kept = append(kept, b)
				continue
			}
			group := raidGroupKey(tier, shadow)
			if authoritative[group] || suppressed[group] {
				// The schedule knows what is running in this group right now, and
				// this boss is not it.
				//
				// The suppressed half of that test is not redundant. A suppressed feed
				// window is skipped before it can set authoritative, so without it the
				// upstream leftovers would be KEPT, which is the exact opposite of the
				// intent: this is the clause that removes Shadow Giratina and Mega
				// Gyarados, both of which pokemon-go-api was still listing.
				//
				// The drop deliberately does NOT depend on finding an expired window
				// naming this boss. It used to, and that quietly undid the whole fix:
				// a rotation is pruned from the events feed a day or so after it
				// ends, at which point the boss stopped being described by anything,
				// was read as "upstream knows best", and came straight back onto the
				// page. Lunala and Mega Swampert returned that way on 2026-08-27
				// while upstream was still serving both.
				stats.Dropped++
				continue
			}
			// Nothing is scheduled for this group at all, so there is nothing here
			// that knows better than upstream. A gap in the feed, or anything new it
			// has no vocabulary for, stays visible.
			//
			// Elite Raids used to be the example given here and it was wrong:
			// classifyRaidTier does read "elite-raid", as tier 5, so an Elite Raid
			// rotation is governed like any other. It does render under the 5 star
			// label, which is not what the game calls it, but it is not invisible.
			kept = append(kept, b)
		}
		out[tier] = kept
	}

	// Anything the schedule says is live but upstream has not listed yet.
	//
	// No suppression guard here on purpose: this iterates active, which activeBosses
	// has already emptied of every suppressed feed window. Adding one would be a
	// second copy of the same rule.
	for _, key := range sortedKeys(active) {
		if carded[key] {
			continue
		}
		a := active[key]
		rb, ok := synthesizeBoss(a.boss, a.window, lookup, cpms)
		if !ok {
			stats.Pending++
			stats.PendingList = append(stats.PendingList, RaidPending{
				Species:   a.boss.Name,
				Tier:      a.window.Tier,
				Shadow:    a.window.Shadow,
				EventID:   a.window.EventID,
				Name:      a.window.Name,
				Reason:    pendingReason(a.boss.Name, lookup),
				StartsUTC: a.window.StartsUTC,
				EndsUTC:   a.window.EndsUTC,
			})
			continue
		}
		out[a.window.Tier] = append(out[a.window.Tier], rb)
		carded[key] = true
		stats.Synthesized++
		if a.window.Additive {
			stats.FromEventPages++
		}
	}

	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("pogodata: raid schedule: marshal: %v", err)
		return upstream, nil, stats
	}
	upcoming := buildUpcoming(windows, suppressions, carded, now, lookup)
	stats.Upcoming = len(upcoming)
	return data, upcoming, stats
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
	image := b.Image
	if image == "" {
		// A boss read out of a page's prose carries no sprite, because a sentence has
		// no img tag. Mega Staraptor is the live example. See eventPageProseBosses.
		image = st.Image
	}
	// The CP range is the line a trainer CATCHES, which for a Mega or a Primal is the
	// base species rather than the thing they just fought. Upstream does the same:
	// its Mega Latios card reads 2090 to 2178, base Latios at level 20, where Mega
	// Latios' own stats give 2758 to 2861. The typing above stays the Mega's, because
	// that is what the counter table and the type filter are ranking against.
	catchAtk, catchDef, catchSta := st.catchLine()
	rb := raidBoss{
		PokemonName:  name,
		ImageURL:     image,
		Types:        st.Types,
		CanBeShiny:   b.CanBeShiny,
		CP:           CPForLevel(catchAtk, catchDef, catchSta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Normal),
		CPMax:        CPForLevel(catchAtk, catchDef, catchSta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Normal),
		CPBoostedMin: CPForLevel(catchAtk, catchDef, catchSta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Boosted),
		CPBoostedMax: CPForLevel(catchAtk, catchDef, catchSta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Boosted),
		Source:       "events",
	}
	annotateBoss(&rb, w)
	return rb, true
}

// withBossSprite gives a boss with no sprite of its own the one from the species
// data, and returns a COPY: the window it came from is memoized per event page and
// shared with the store's raidSchedule, so writing to it would corrupt both.
//
// A boss read out of a page's PROSE carries no image, because a sentence has no img
// tag. Mega Staraptor is the live case, and it is the reason this exists twice:
// synthesizeBoss got the same fallback when the prose reader landed, which fixed the
// card on the grid and did nothing for the up next entry, so Super Mega Raid Day sat
// in the schedule on 19 September as a name with a blank where every other rotation
// had a sprite. Only the Mega table has an image to offer, which is the only place a
// prose read boss can come from anyway.
func withBossSprite(b WindowBoss, lookup speciesLookup) WindowBoss {
	if b.Image != "" || lookup == nil {
		return b
	}
	if st, ok := lookup(b.Name); ok && st.Image != "" {
		b.Image = st.Image
	}
	return b
}

// upcomingKey identifies one boss's appearance in one group over one stated span.
// It is the unit the up next list is deduplicated on. See foldUpcoming.
type upcomingKey struct{ group, boss, start, end string }

// bossInGroup is one boss in one group, ignoring the span. It is the set a
// containment reduction runs within.
type bossInGroup struct{ group, boss string }

// upcomingSpan is the pair of calendar days a rotation STATES it runs between.
//
// Read off the raw feed strings rather than the UTC fields for the same reason
// rawDayRange does: those two are read in zones fourteen hours apart and are not
// comparable to each other. The UTC fallback exists only for a window built without
// feed strings, which is a thing tests do and the readers never do.
func upcomingSpan(w RaidWindow) (start, end string) {
	if s, e, ok := rawDayRange(w); ok {
		return s, e
	}
	return w.StartsUTC.Format("2006-01-02"), w.EndsUTC.Format("2006-01-02")
}

// preferUpcomingWindow picks between two rotations describing the same boss over the
// same stated days. The feed wins, because its hours are the real ones (06:00 to
// 22:00 rather than an event page's whole day approximation) and its event id opens
// the rotation's own modal; failing that, the one running longest; failing that, the
// id, so the answer is a property of the windows and not of the slice.
func preferUpcomingWindow(candidate, held RaidWindow) bool {
	if candidate.Additive != held.Additive {
		return held.Additive
	}
	if !candidate.EndsUTC.Equal(held.EndsUTC) {
		return candidate.EndsUTC.After(held.EndsUTC)
	}
	return candidate.EventID < held.EventID
}

// foldUpcoming reduces a set of rotations to one entry each, keeping only the bosses
// that chose it, and drops any rotation left with none.
//
// byRotation decides the dimension the fold works in.
//
// A rotation that has not opened yet is identified by the span it STATES, because
// the same boss on two separate runs is two real appearances and both belong on the
// page: Mega Victreebel is on the GO Fest habitat list for 5 September and is the
// Mega rotation from the 30th. Two things then collapse. Windows stating the SAME
// span are one rotation seen from two sources, settled by prefer. And a window whose
// span sits wholly INSIDE another naming the same boss is not a second appearance at
// all, it is the same uninterrupted run described again by a shorter source: on
// 25 August the strip published Mega Gyarados twice, once for its own 26 August to
// 8 September rotation and once for the GO Fest Sunday habitat list on the 6th,
// which is a day in the middle of the first. Found in review of the day keyed fold
// that shipped first.
//
// A rotation that is live now has no span dimension at all: it is happening, and one
// boss happening once is one entry however many windows describe it.
func foldUpcoming(windows []RaidWindow, byRotation bool, prefer func(candidate, held RaidWindow) bool, live bool, lookup speciesLookup) []UpcomingRaid {
	keyOf := func(w RaidWindow, boss string) upcomingKey {
		k := upcomingKey{group: raidGroupKey(w.Tier, w.Shadow), boss: boss}
		if byRotation {
			k.start, k.end = upcomingSpan(w)
		}
		return k
	}

	best := make(map[upcomingKey]int, len(windows))
	for i, w := range windows {
		for _, b := range w.Bosses {
			uk := keyOf(w, bossKey(b.Name, w.Shadow))
			if j, taken := best[uk]; taken && !prefer(w, windows[j]) {
				continue
			}
			best[uk] = i
		}
	}

	// Every surviving span is distinct within its boss, so containment between two of
	// them is strict and at least one is always maximal.
	spans := map[bossInGroup][]upcomingKey{}
	for k := range best {
		g := bossInGroup{k.group, k.boss}
		spans[g] = append(spans[g], k)
	}
	kept := make(map[upcomingKey]bool, len(best))
	for _, list := range spans {
		for _, k := range list {
			inside := false
			for _, other := range list {
				if other != k && other.start <= k.start && other.end >= k.end {
					inside = true
					break
				}
			}
			if !inside {
				kept[k] = true
			}
		}
	}

	out := make([]UpcomingRaid, 0, len(windows))
	for i, w := range windows {
		seen := make(map[string]bool, len(w.Bosses))
		bosses := make([]WindowBoss, 0, len(w.Bosses))
		for _, b := range w.Bosses {
			key := bossKey(b.Name, w.Shadow)
			uk := keyOf(w, key)
			if seen[key] || !kept[uk] || best[uk] != i {
				continue
			}
			seen[key] = true
			bosses = append(bosses, withBossSprite(b, lookup))
		}
		if len(bosses) == 0 {
			continue
		}
		u := upcomingFrom(w, live)
		u.Bosses = bosses
		out = append(out, u)
	}
	return out
}

// buildUpcoming picks what to show under the grid: EVERY rotation the schedule knows
// about that has not started yet, plus every rotation that is live but has bosses
// with no card on the grid.
//
// It used to publish the soonest rotation per tier and throw the rest away. That was
// wrong in a way nobody could see from the outside, because what it threw away
// looked exactly like something that had never been scraped: on 2026-09-01 the served
// list held three entries while the scrapers had sixteen dated rotations running out
// to 6 October. Mega Ascension's own page names a different Mega for every day of the
// week and only Thursday's reached the page, so Mega Raichu X and Mega Raichu Y on the
// Friday were parsed correctly, tested, cached, and then dropped here.
//
// Both halves fold, and they fold in different dimensions on purpose. See
// foldUpcoming. The live half used to keep one window per group, which had the same
// shape of defect as the future half and hid the second of two rotations changing over
// in one tier; it was found in review of the first fix to this function.
//
// A window that loses every one of its bosses to another window drops out entirely
// rather than becoming an empty card. That is what removes the Mega Squads windows
// once Mega Beedrill and Mega Houndoom have chosen their own rotations.
func buildUpcoming(windows []RaidWindow, suppressions []RaidSuppression, carded map[string]bool, now time.Time, lookup speciesLookup) []UpcomingRaid {
	live := make([]RaidWindow, 0, len(windows))
	future := make([]RaidWindow, 0, len(windows))
	for _, w := range windows {
		if !governedTiers[w.Tier] {
			continue
		}
		if w.Active(now) {
			if silencedBy(suppressions, w, now) {
				// Live in the feed, not live in the game. Advertising it as the Live
				// placeholder would put "Regirock, Regice and Registeel" under a tier 5
				// grid that has just been deliberately emptied.
				continue
			}
			// Named only while a boss has no card on the grid. A card says everything
			// a placeholder would, and more.
			//
			// Per boss, not per window. Skipping the whole window as soon as ANY one
			// of its bosses was carded is what this did, and on the GO Fest Sunday
			// habitat list that is 17 Megas hidden by one: Mega Falinks is on that
			// list AND on upstream's tier 6, so with the Mega table lagging a debut,
			// the other 16 were pending on the grid and skipped here, which is
			// invisible on the whole public payload. Measured in review at
			// 2026-09-05T18:00Z with the Mega table emptied: 32 pending, 16 of them
			// reachable nowhere.
			uncarded := make([]WindowBoss, 0, len(w.Bosses))
			for _, b := range w.Bosses {
				if !carded[bossKey(b.Name, w.Shadow)] {
					uncarded = append(uncarded, b)
				}
			}
			if len(uncarded) > 0 {
				w.Bosses = uncarded
				live = append(live, w)
			}
			continue
		}
		if w.StartsUTC.Before(now) {
			continue // already over
		}
		// No suppression test on this branch, deliberately. A rotation that has not
		// opened yet can only be reached by a note it opens INSIDE, and Silences
		// exempts exactly those as the replacements the note is promising. Testing
		// here anyway is what hid the whole Mega Ascension week from the up next
		// strip, which announced Mega Beedrill on the 8th while the page had a
		// different Mega for every day in between.
		future = append(future, w)
	}

	// preferRaidWindow rather than preferUpcomingWindow for the live half, because it
	// is answering the same question activeBosses just answered about the grid, and
	// the strip disagreeing with the card beside it would be worse than either answer.
	out := foldUpcoming(live, false, func(c, h RaidWindow) bool { return preferRaidWindow(c, h, now) }, true, lookup)
	out = append(out, foldUpcoming(future, true, preferUpcomingWindow, false, lookup)...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].Live != out[j].Live {
			return out[i].Live
		}
		if out[i].StartsAt != out[j].StartsAt {
			return out[i].StartsAt < out[j].StartsAt
		}
		if out[i].Tier != out[j].Tier {
			return out[i].Tier < out[j].Tier
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
func nextRaidBoundary(windows []RaidWindow, sups []RaidSuppression, now time.Time) time.Time {
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
	// A note opening or lifting changes the answer with no window moving at all.
	for _, sp := range sups {
		consider(sp.StartsUTC)
		consider(sp.EndsUTC)
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
	// Two sources of rotation, joined here rather than inside either reader. The
	// feed's own raid data comes first and governs its tiers; the event pages add
	// what the feed does not model at all. See eventraids.go.
	windows := parseRaidWindows(s.events)
	pageWindows, suppressions := s.eventPageRaidsLocked()
	windows = append(windows, pageWindows...)
	served, upcoming, stats := reconcileRaids(s.raidsUpstream, windows, suppressions, now, newSpeciesLookup(s.pokemon, s.pokemonTypes, s.megaForms), raidCPMsFrom(s.cpMults))
	s.raids = served
	s.raidSchedule = windows
	s.raidSuppressions = suppressions
	s.raidStats = stats
	s.raidsBuiltFor = nextRaidBoundary(windows, suppressions, now)
	s.setRaidsPendingLocked(stats.PendingList, now)
	if len(upcoming) > 0 {
		if data, err := json.Marshal(upcoming); err == nil {
			s.raidsUpcoming = data
		}
	} else {
		s.raidsUpcoming = nil
	}
	if stats.Dropped > 0 || stats.Synthesized > 0 || stats.Pending > 0 || stats.FromEventPages > 0 || stats.Suppressed > 0 {
		log.Printf("pogodata: raids: schedule reconciled (%d windows, %d dropped, %d synthesized, %d awaiting upstream, %d from event pages, %d groups suppressed)",
			stats.Windows, stats.Dropped, stats.Synthesized, stats.Pending, stats.FromEventPages, stats.Suppressed)
	}
}

// raidBoundaryBackstop caps how long the boundary watcher will sleep in one go.
//
// The cap is not about the boundary itself, which the timer hits exactly. It is
// about the schedule moving underneath a sleeping timer: a refresh landing a new
// rotation changes raidsBuiltFor, and a watcher already asleep until next week would
// never notice. Waking at least this often re-reads it, and if the real boundary is
// nearer than the cap the next sleep is the exact remainder.
//
// Five minutes rather than something longer for one reason: that is what the ticker
// this replaced did, and a boundary landing just after the watcher went to sleep is
// then never later than it used to be. The win is that a boundary the watcher CAN
// see is now hit exactly rather than up to five minutes late.
const raidBoundaryBackstop = 5 * time.Minute

// raidBoundaryFloor stops a boundary that is already in the past from spinning the
// loop. A rebuild that leaves raidsBuiltFor behind the clock is a bug, but it must
// cost a wakeup a second rather than a whole core.
const raidBoundaryFloor = time.Second

// watchRaidBoundaries rebuilds the served list at the instant a rotation opens or
// shuts, rather than noticing up to five minutes later.
//
// This used to be a five minute ticker doing a clock comparison, which meant a
// rotation flip was on the site somewhere between zero and five minutes late for no
// reason: the store already knows exactly when the answer changes, so it can simply
// sleep until then. The comparison inside maybeRebuildRaids is kept anyway, because
// the backstop wakeup below is deliberately early most of the time.
func (s *Store) watchRaidBoundaries() {
	timer := time.NewTimer(raidBoundaryBackstop)
	defer timer.Stop()
	for {
		<-timer.C
		s.maybeRebuildRaids()
		timer.Reset(s.untilNextRaidBoundary())
	}
}

// untilNextRaidBoundary is how long to sleep before the served list could differ.
func (s *Store) untilNextRaidBoundary() time.Duration {
	s.mu.RLock()
	next := s.raidsBuiltFor
	s.mu.RUnlock()
	if next.IsZero() {
		// Nothing scheduled at all, which is the cold start and the empty feed.
		return raidBoundaryBackstop
	}
	switch d := time.Until(next); {
	case d < raidBoundaryFloor:
		return raidBoundaryFloor
	case d > raidBoundaryBackstop:
		return raidBoundaryBackstop
	default:
		return d
	}
}

// maybeRebuildRaids re-derives the served blob only once a window boundary has been
// crossed. Called by watchRaidBoundaries, which sleeps until that instant, so the
// comparison here only bites on one of its backstop wakeups.
func (s *Store) maybeRebuildRaids() {
	s.mu.Lock()
	if !s.raidsBuiltFor.IsZero() && time.Now().Before(s.raidsBuiltFor) {
		s.mu.Unlock()
		return
	}
	s.rebuildRaidsLocked()
	s.mu.Unlock()
	// Outside the lock: the hook reaches the database, and holding the store's
	// lock across a query would block every read on the site behind it.
	s.notifyRaidsApplied()
}

// RaidsUpcoming is the "up next" list: every rotation the schedule knows about that
// has not opened yet, plus any that is live but has no card on the grid yet.
func (s *Store) RaidsUpcoming() json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.raidsUpcoming
}

// RaidSuppressions is what the event pages currently say is not running.
//
// The counters in the admin scraper check say a tier went missing; this says what
// it went missing on the strength of, which is the only way to tell a real
// suspension from a note the reader has misread.
func (s *Store) RaidSuppressions() []RaidSuppression {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RaidSuppression, len(s.raidSuppressions))
	for i, sp := range s.raidSuppressions {
		// Groups is copied too. A slice copy of the outer slice shares every inner
		// one, so a caller ranging over this could still write through into the
		// store's own schedule.
		sp.Groups = append([]string(nil), sp.Groups...)
		out[i] = sp
	}
	return out
}

// ── Pending rotations ────────────────────────────────────────────────────────

// raidsPendingFile is where the pending set is kept between restarts, beside the
// other cache blobs. Without it a restart forgets what the site was waiting for
// and the backoff starts over from nothing.
const raidsPendingFile = "raids_pending.json"

// setRaidsPendingLocked replaces the pending set in memory. Caller must hold s.mu.
//
// Expired entries are dropped on the way in: a rotation that ended while still
// pending is history, and keeping it would hold the retry loop open forever on
// work that can never succeed.
//
// It deliberately does NOT touch the disk. rebuildRaidsLocked calls this, and a
// rebuild is a pure in-memory operation that several tests drive directly; making
// it write a file would have those tests scatter cache files through the package
// directory. Persistence is persistRaidsPending, called from the two paths that
// own it.
func (s *Store) setRaidsPendingLocked(list []RaidPending, now time.Time) {
	kept := make([]RaidPending, 0, len(list))
	for _, p := range list {
		if !p.Expired(now) {
			kept = append(kept, p)
		}
	}
	s.raidsPending = kept
}

// persistRaidsPending writes the pending set beside the other cache blobs, so a
// restart does not forget what the site was waiting for. Must be called with s.mu
// NOT held.
//
// Written even when empty, or an emptied set would be read back as a stale one on
// the next boot. A store with no cache directory writes nothing, which is what
// keeps a test-constructed store from leaving a file behind.
func (s *Store) persistRaidsPending() {
	if s.cacheDir == "" {
		return
	}
	data, err := json.Marshal(s.RaidsPending())
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, raidsPendingFile), data, 0644)
}

// RaidsPending returns the rotations the schedule says are live that could not be
// turned into a card, with the expired ones already dropped. Never nil.
func (s *Store) RaidsPending() []RaidPending {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RaidPending, 0, len(s.raidsPending))
	now := time.Now()
	for _, p := range s.raidsPending {
		if !p.Expired(now) {
			out = append(out, p)
		}
	}
	return out
}

// loadRaidsPending restores the set from disk at boot. Best effort: a missing or
// unreadable file just means the next rebuild will recompute it.
func (s *Store) loadRaidsPending() {
	data, err := os.ReadFile(filepath.Join(s.cacheDir, raidsPendingFile))
	if err != nil {
		return
	}
	var list []RaidPending
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("pogodata: raids: pending cache unreadable: %v", err)
		return
	}
	s.mu.Lock()
	s.setRaidsPendingLocked(list, time.Now())
	n := len(s.raidsPending)
	s.mu.Unlock()
	// Not persisted back: this is what was just read, and rewriting it would only
	// re-serialise the same bytes minus anything that expired, which the next
	// rebuild does anyway.
	if n > 0 {
		log.Printf("pogodata: raids: %d pending rotation(s) restored from disk", n)
	}
}

// Retry pacing. Five minutes is short enough that a boss upstream has just
// published lands while the rotation still matters, and the doubling keeps a
// rotation nobody is ever going to publish from being asked for every five
// minutes for its whole run.
const (
	raidPendingRetryMin = 5 * time.Minute
	raidPendingRetryMax = time.Hour
)

// retryPendingRaids re-fetches what a pending rotation is waiting on, rather than
// leaving it until the next scheduled refresh up to two and a half hours away.
//
// There are exactly two things that can resolve one: upstream starts listing the
// boss, or the Mega table lands so it can be synthesized locally. So the retry is
// those two fetches and a rebuild, which is the scheduled refresh minus the Max
// Battle fetch that has nothing to do with it.
//
// The backoff resets the moment the set empties, so a quiet site is not paying an
// hourly fetch for nothing.
func (s *Store) retryPendingRaids() {
	wait := raidPendingRetryMin
	for {
		time.Sleep(wait)

		pending := s.RaidsPending()
		if len(pending) == 0 {
			wait = raidPendingRetryMin
			continue
		}

		log.Printf("pogodata: raids: retrying %d pending rotation(s) after %s (%s)",
			len(pending), wait.Round(time.Minute), pending[0].Reason)

		s.refreshRaids()
		// Only when a Mega is actually waiting. Refetching the Mega pokedex for a
		// pending ordinary species would be a request that cannot help.
		for _, p := range pending {
			if isMegaName(p.Species) {
				s.refreshMegas()
				break
			}
		}

		if len(s.RaidsPending()) < len(pending) {
			// Progress. Start over at the short interval, because a rotation
			// changeover tends to bring several at once.
			wait = raidPendingRetryMin
			continue
		}
		if wait *= 2; wait > raidPendingRetryMax {
			wait = raidPendingRetryMax
		}
	}
}

// pendingSummary renders the pending set for the admin scraper check.
//
// Capped at three names because this ends up on one line in the panel, and a
// changeover can leave a whole tier pending at once. The count in the sentence
// before it already carries the total, so the tail is only ever detail.
func pendingSummary(list []RaidPending) string {
	if len(list) == 0 {
		return ""
	}
	const max = 3
	parts := make([]string, 0, max)
	for i, p := range list {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(list)-max))
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s", p.Species, p.Reason))
	}
	return strings.Join(parts, "; ")
}

// ── Archive export ───────────────────────────────────────────────────────────

// RaidArchiveRow is one boss exactly as it was served, with its rotation window
// resolved to instants.
//
// The served blob carries the feed's own floating wall clock strings, which are
// right for a browser rendering a countdown in the viewer's zone and useless as a
// database key. The window is joined back on here, from the schedule the same
// rebuild produced.
type RaidArchiveRow struct {
	Species      string
	Tier         string
	Shadow       bool
	Types        []string
	CP           int
	CPMax        int
	CPBoostedMin int
	CPBoostedMax int
	ImageURL     string
	CanBeShiny   bool
	IsMega       bool
	Source       string

	// EventID and the window are empty and zero for a boss no rotation describes,
	// which is every tier 1 and tier 3 entry: no feed anywhere carries timing for
	// them. Those still identify a boss worth remembering, so they belong in the
	// dimension, but there is no window to key an appearance on and inventing one
	// would put a row in the fact table that records nothing that happened.
	EventID     string
	WindowStart time.Time
	WindowEnd   time.Time
}

// HasWindow reports whether this row can be recorded as an appearance.
func (r RaidArchiveRow) HasWindow() bool { return !r.WindowStart.IsZero() && !r.WindowEnd.IsZero() }

// RaidArchiveRows returns the currently served bosses, ready to be warehoused.
//
// Built from s.raids, the SERVED blob, not from upstream: what is worth recording
// is what trainers actually saw, which is upstream reconciled against the schedule
// and is not the same list.
func (s *Store) RaidArchiveRows() []RaidArchiveRow {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tiers map[string][]raidBoss
	if err := json.Unmarshal(s.raids, &tiers); err != nil {
		return nil
	}

	// Windows by event id AND boss, so the join below is a lookup rather than a scan
	// per boss. A rotation can appear more than once at a changeover; the one ending
	// last is the incoming one, which is the same tie-break reconcileRaids uses.
	//
	// Keying on the event id alone was wrong, and quietly. eventPageRaidWindows emits
	// the BARE event id for every group it builds, deliberately, so that the client
	// can open the event modal with it: mega-ascension alone produces six windows
	// under one id, one per day plus the whole-event one. So every Mega Ascension day
	// boss was warehoused with the LAST day's window. Mega Skarmory was served
	// correctly as the Wednesday boss and archived as Friday, and the fact table is
	// keyed on (boss, window start), so the appearance history was recorded on the
	// wrong day for every day-scoped roster this reader has ever produced.
	//
	// Ties are settled on the earliest start, so the answer does not depend on the
	// order the two window sources were concatenated in. mega-ascension's Friday day
	// window and its whole-event window really do end at the same instant, and the
	// whole-event one is the better answer for a boss no day window names.
	better := func(candidate, held RaidWindow) bool {
		if !candidate.EndsUTC.Equal(held.EndsUTC) {
			return candidate.EndsUTC.After(held.EndsUTC)
		}
		return candidate.StartsUTC.Before(held.StartsUTC)
	}
	windows := make(map[string]RaidWindow, len(s.raidSchedule))
	byEvent := make(map[string]RaidWindow, len(s.raidSchedule))
	for _, w := range s.raidSchedule {
		if prev, ok := byEvent[w.EventID]; !ok || better(w, prev) {
			byEvent[w.EventID] = w
		}
		for _, b := range w.Bosses {
			k := w.EventID + "|" + bossKey(b.Name, w.Shadow)
			if prev, ok := windows[k]; ok && !better(w, prev) {
				continue
			}
			windows[k] = w
		}
	}

	out := make([]RaidArchiveRow, 0, 32)
	for tier, bosses := range tiers {
		for _, b := range bosses {
			row := RaidArchiveRow{
				Species:      b.PokemonName,
				Tier:         tier,
				Shadow:       isShadowName(b.PokemonName),
				Types:        b.Types,
				CP:           b.CP,
				CPMax:        b.CPMax,
				CPBoostedMin: b.CPBoostedMin,
				CPBoostedMax: b.CPBoostedMax,
				ImageURL:     b.ImageURL,
				CanBeShiny:   b.CanBeShiny,
				IsMega:       isMegaName(b.PokemonName),
				Source:       b.Source,
				EventID:      b.EventID,
			}
			if row.Source == "" {
				// Everything the schedule did not synthesize came from upstream.
				row.Source = "upstream"
			}
			if b.EventID != "" {
				w, ok := windows[b.EventID+"|"+bossKey(b.PokemonName, row.Shadow)]
				if !ok {
					// No window under that id names this boss, which happens when
					// upstream and the schedule spell it differently enough that
					// bossKey does not join them. The rotation's own span is still a
					// better answer than none.
					w, ok = byEvent[b.EventID]
				}
				if ok {
					row.WindowStart, row.WindowEnd = w.StartsUTC, w.EndsUTC
				}
			}
			out = append(out, row)
		}
	}

	// Stable order, so a diff of two archive runs reads as a diff and not as a
	// reshuffle. Map iteration over tiers is random.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return out[i].Tier < out[j].Tier
		}
		return out[i].Species < out[j].Species
	})
	return out
}

// SetRaidsAppliedHook registers fn to run each time the served raid list has been
// rebuilt. Call it before Start; passing nil clears it.
//
// fn runs with no lock held, on whichever goroutine did the rebuild, so anything
// slow belongs in a goroutine of its own. Same contract as SetEventsAppliedHook,
// and it exists for the same reason: the store knows nothing about the database
// and must not learn.
func (s *Store) SetRaidsAppliedHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raidsApplied = fn
}

// notifyRaidsApplied fires the hook. Must be called with s.mu NOT held.
func (s *Store) notifyRaidsApplied() {
	// The pending set is rebuilt alongside the served list, so the two are saved
	// together. Both callers of this reach it with the lock released.
	s.persistRaidsPending()

	s.mu.RLock()
	fn := s.raidsApplied
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
