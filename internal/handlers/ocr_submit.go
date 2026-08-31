package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/httprate"
)

// The app submits a reading it has already made, and the server solves it.
//
// The screenshot does not travel. That is the point of the endpoint, not a side
// effect of it: the app runs ML Kit plus its own arc and appraisal detectors on
// the uncompressed frame, and re-doing all of it server side over a JPEG bought
// nothing except the compression artefacts. A full frame is 300 KB to 1.2 MB; a
// reading is about 400 bytes.
//
// What stays here is the solve, because it needs data the client should not have
// to carry: species base stats, the CP multiplier table and the dust brackets.

// scanClient identifies the build that produced a reading.
//
// It exists for telemetry, not for gating. A reading from a build the server has
// never heard of is still a reading, and refusing it would mean every app
// release waited on a server deploy.
type scanClient struct {
	Build       int    `json:"build"`
	Detector    string `json:"detector"`
	DataVersion string `json:"data_version"`
}

// scanReading is a reading as it arrives on the wire.
//
// Every field is a pointer, which is what makes the sentinels survive the trip.
// Go cannot tell an omitted number from an explicit zero on a value type, and
// two of these fields have a zero that means something: appraisal_bars uses -1
// for "could not read" and 0 for a genuine nought-star Pokemon, and arc_level
// uses 0.0 for "not read". Decoding those into plain ints would quietly turn
// "I could not tell" into "it is zero", which is a claim nobody made.
type scanReading struct {
	CP             *int    `json:"cp"`
	RawCP          *string `json:"raw_cp"`
	CPSource       *string `json:"cp_source"`
	HP             *int    `json:"hp"`
	RawDust        *int    `json:"raw_dust"`
	NormalisedDust *int    `json:"normalised_dust"`
	PokemonName    *string `json:"pokemon_name"`
	NameSource     *string `json:"name_source"`
	// Form is the game's variant name (Normal, Alola, Galarian, Origin, Black).
	// It is a separate column in the stat data, not part of pokemon_name, so
	// without it a Black Kyurem is solved against Normal Kyurem: same name,
	// 26% less attack, and in practice no candidates at all. The app already
	// settles the form on device, so this is the field that stops that work
	// being thrown away on the wire.
	Form          *string  `json:"form"`
	ArcLevel      *float64 `json:"arc_level"`
	AppraisalBars *int     `json:"appraisal_bars"`
	IsHundo       *bool    `json:"is_hundo"`
	IsLucky       *bool    `json:"is_lucky"`
	IsShadow      *bool    `json:"is_shadow"`
	IsPurified    *bool    `json:"is_purified"`
}

type scanSubmission struct {
	Client    scanClient  `json:"client"`
	Extracted scanReading `json:"extracted"`
}

// Structural bounds.
//
// These are NOT plausibility checks. Every one of them sits orders of magnitude
// outside anything the game could produce, because a bound drawn at what the
// game does today is a bound that rejects a real scan the day the game changes.
// They exist to stop an integer or a float from being a denial of service or an
// overflow, and for nothing else. A CP that is merely impossible for the species
// is an advisory finding, handled further down.
const (
	maxSubmittedCP    = 100_000
	maxSubmittedHP    = 100_000
	maxSubmittedDust  = 10_000_000
	maxSubmittedLevel = 100.0
	maxSubmittedText  = 64
)

// scanAdvisory is a value that survived structural validation but disagrees with
// what the server believes about the game.
//
// It is a signal, never a verdict. The server's game data comes from upstream
// feeds that demonstrably lag: live pogoapi cp_multiplier.json stopped at level
// 45 while the game had reached 51, and a real level 50 hundo is 106.2% of the
// level 45 maximum, which a plausibility check would have thrown away. So a
// disagreement between a reading and this server's tables is at least as likely
// to mean the server is stale as it is to mean the reading is wrong, and the
// component with fresher data is the app.
//
// The rate is the useful reading. Many accounts tripping the same constraint at
// once means the game changed and this server has not caught up. One account
// tripping it repeatedly is an account-level matter for review, never something
// to answer by blocking a request, which would also punish whoever scans a new
// species first.
type scanAdvisory struct {
	Field      string `json:"field"`
	Value      string `json:"value"`
	Constraint string `json:"constraint"`
	// DataVersion describes the server tables the constraint was drawn from, so
	// a flag raised against a stale table is identifiable after the fact.
	DataVersion string `json:"data_version"`
}

// serverDataVersion summarises the game tables a constraint was measured
// against. The two things that actually go stale are how far the CP multiplier
// table reaches and how many species it knows, and both are what produce false
// advisories, so both are what the stamp records.
func serverDataVersion(pokeList []pokemonStatEntry, cpms []cpmEntry) string {
	maxLvl := 0.0
	for _, c := range cpms {
		if c.Level > maxLvl {
			maxLvl = c.Level
		}
	}
	return fmt.Sprintf("species=%d cpm_max=%.1f", len(pokeList), maxLvl)
}

// validateReadingStructure applies the checks no game update can invalidate.
//
// A protocol fact is something like "a negative HP is not a reading", or "there
// are three star positions, so a bar count outside -1 to 3 is not a value". The
// game can add species, levels, dust tiers and status effects; it cannot make
// -4 stars mean something. Anything that depends on what the game currently
// contains belongs in the advisory pass instead.
func validateReadingStructure(rd scanReading) error {
	if rd.CP != nil && (*rd.CP < 0 || *rd.CP > maxSubmittedCP) {
		return fmt.Errorf("cp out of range")
	}
	if rd.HP != nil && (*rd.HP < 0 || *rd.HP > maxSubmittedHP) {
		return fmt.Errorf("hp out of range")
	}
	if rd.RawDust != nil && (*rd.RawDust < 0 || *rd.RawDust > maxSubmittedDust) {
		return fmt.Errorf("raw_dust out of range")
	}
	if rd.NormalisedDust != nil && (*rd.NormalisedDust < 0 || *rd.NormalisedDust > maxSubmittedDust) {
		return fmt.Errorf("normalised_dust out of range")
	}
	// -1 is "unknown" and 0 to 3 are the three star positions plus none of them.
	if rd.AppraisalBars != nil && (*rd.AppraisalBars < -1 || *rd.AppraisalBars > 3) {
		return fmt.Errorf("appraisal_bars out of range")
	}
	// 0 means "not read". A negative level is not a reading, and the upper bound
	// is an overflow guard rather than a claim about the level cap: a level above
	// what this server knows about is an advisory finding, not a rejection.
	if rd.ArcLevel != nil && (*rd.ArcLevel < 0 || *rd.ArcLevel > maxSubmittedLevel) {
		return fmt.Errorf("arc_level out of range")
	}
	// A Pokemon cannot be both shadow and purified: purifying is what stops it
	// being a shadow. This is a fact about the two words, not about the roster.
	if rd.IsShadow != nil && rd.IsPurified != nil && *rd.IsShadow && *rd.IsPurified {
		return fmt.Errorf("is_shadow and is_purified cannot both be true")
	}
	for _, f := range []struct {
		name string
		val  *string
	}{
		{"pokemon_name", rd.PokemonName},
		{"form", rd.Form},
		{"name_source", rd.NameSource},
		{"cp_source", rd.CPSource},
		{"raw_cp", rd.RawCP},
	} {
		if f.val == nil {
			continue
		}
		if len(*f.val) > maxSubmittedText {
			return fmt.Errorf("%s too long", f.name)
		}
		if !utf8.ValidString(*f.val) {
			return fmt.Errorf("%s is not valid UTF-8", f.name)
		}
	}
	return nil
}

// advisoriesFor collects the game-content disagreements in a reading.
//
// Nothing here rejects anything. Each entry records the value, the constraint it
// failed and the tables the constraint came from, so that a spike can later be
// read as either "the game changed" or "this account is worth a look".
func advisoriesFor(ext ocrExtracted, poke *pokemonStatEntry, env solveEnv) []scanAdvisory {
	version := serverDataVersion(env.pokeList, env.cpms)
	add := func(out []scanAdvisory, field, value, constraint string) []scanAdvisory {
		return append(out, scanAdvisory{
			Field: field, Value: value, Constraint: constraint, DataVersion: version,
		})
	}

	var out []scanAdvisory
	if ext.PokemonName != "" && poke == nil {
		out = add(out, "pokemon_name", ext.PokemonName, "species not in the server's stat list")
	}
	// The species resolved but the form did not, so the solve fell back to
	// another row of the same species and its stats are not the ones the trainer
	// is looking at. Advisory rather than a reject, because a form the server has
	// never heard of is the signature of a game update the upstream feed has not
	// caught up with, which is the app being ahead rather than wrong.
	if poke != nil && env.form != "" && !strings.EqualFold(poke.Form, env.form) {
		out = add(out, "form", env.form,
			fmt.Sprintf("form not in the server's stat list for %s; solved against %q instead",
				poke.PokemonName, poke.Form))
	}
	if poke != nil && ext.CP > 0 {
		// computeMaxCP does not account for Best Buddy, which displays CP one
		// level higher than the Pokemon actually is. Without the same headroom
		// the image path allows, every Best Buddy at the level cap would raise
		// a flag, and a flag that fires on ordinary play is noise that hides the
		// signal this whole mechanism exists to carry.
		//
		// It is deliberately no wider than that. A true level 50 hundo is 106.2%
		// of the level 45 maximum, so an upstream CPM table that has fallen back
		// to 45 will still trip this. That is correct: on the image path the same
		// margin DISCARDED such a scan, and here it only raises a flag saying the
		// server's tables have gone stale, which is exactly what it would mean.
		if maxCP := computeMaxCP(*poke, env.trainerLevel, env.cpms); maxCP > 0 && ext.CP > maxCP*106/100 {
			out = add(out, "cp", fmt.Sprint(ext.CP),
				fmt.Sprintf("above the server's maximum for %s (%d, Best Buddy allowed for)",
					poke.PokemonName, maxCP))
		}
	}
	if ext.RawDust > 0 && len(dustCandidates(ext.RawDust, nil, nil, nil)) == 0 {
		out = add(out, "raw_dust", fmt.Sprint(ext.RawDust),
			"not a member of the server's dust bracket and modifier set")
	}
	if ext.ArcLevel > 0 {
		if ceiling := maxPowerUpLevel(env.trainerLevel); ext.ArcLevel > ceiling {
			out = add(out, "arc_level", fmt.Sprintf("%.1f", ext.ArcLevel),
				fmt.Sprintf("above the level this trainer can reach (%.1f)", ceiling))
		}
	}
	return out
}

// appraisalDisagreement compares the submitted star rating against the band the
// solve actually landed in.
//
// This is the one cross check the image path never performed, and it needs no
// image: the star bands are IV sum ranges, and the solve produces IV sums. It is
// separate from the rest because it can only run once the solve has answered.
func appraisalDisagreement(bars int, candidates []IVCandidate, version string) *scanAdvisory {
	if bars < 0 || bars > 3 || len(candidates) == 0 {
		return nil
	}
	lo, hi := appraisalRange[bars][0], appraisalRange[bars][1]
	for _, c := range candidates {
		sum := c.AtkIV + c.DefIV + c.StaIV
		if sum >= lo && sum <= hi {
			return nil
		}
	}
	return &scanAdvisory{
		Field:       "appraisal_bars",
		Value:       fmt.Sprint(bars),
		Constraint:  fmt.Sprintf("no solved spread falls in the %d star band (%d to %d)", bars, lo, hi),
		DataVersion: version,
	}
}

// unknownJSONFields reports body keys the request types do not define.
//
// Deliberately NOT a rejection, which is where this departs from the original
// plan. json.Decoder.DisallowUnknownFields would turn "the app added a field"
// into "the app cannot submit scans until the server is redeployed", and making
// every app release wait on a server deploy is the exact coupling this whole
// change exists to remove. But an unknown field is still worth seeing: the
// common cause is not a newer client, it is a misspelled key, which otherwise
// reads as an absent field and silently becomes a sentinel.
//
// The body is a few hundred bytes, so decoding it a second time to find out is
// affordable in a way it would not be for the image path.
func unknownJSONFields(body []byte) []string {
	var loose map[string]json.RawMessage
	if json.Unmarshal(body, &loose) != nil {
		return nil
	}
	known := map[string]map[string]bool{
		"client":    fieldNames(scanClient{}),
		"extracted": fieldNames(scanReading{}),
	}
	var out []string
	for key, raw := range loose {
		inner, ok := known[key]
		if !ok {
			out = append(out, key)
			continue
		}
		var sub map[string]json.RawMessage
		if json.Unmarshal(raw, &sub) != nil {
			continue
		}
		for k := range sub {
			if !inner[k] {
				out = append(out, key+"."+k)
			}
		}
	}
	return out
}

// IVFromScan solves a reading the app made, without the app sending the frame.
//
// The response is the same OCRResponse shape the image path answers with, so
// every client type downstream keeps working unchanged. Two things differ, and
// both follow from the app being the source of truth here:
//
//   - extracted is echoed back exactly as submitted. The server does not
//     normalise the dust, does not fold a dust-implied status into the flags,
//     and does not replace a CP the arc rescue disagreed with. It still REPORTS
//     the rescue through arc_rescue, so the app knows and decides for itself.
//   - species_candidates is never present. Resolving a nickname through its
//     candy label needs the recognised screen text, which no longer leaves the
//     device. The app does this itself with SpeciesFromCandy.
func (h *Handlers) IVFromScan(w http.ResponseWriter, r *http.Request) {
	// The route already sits behind MobileAuthMiddleware, which gates on a Bearer
	// token. This is the second, independent lookup: a session that expires
	// between the two would otherwise hand the handler a nil user.
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, "could not read request", http.StatusBadRequest)
		return
	}

	var sub scanSubmission
	if err := json.Unmarshal(body, &sub); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := validateReadingStructure(sub.Extracted); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	rd := sub.Extracted
	ext := readingToExtracted(rd)

	// Nothing to solve from. Answered as a rejection rather than as an empty
	// result so the client can tell "your reading was not enough" apart from
	// "no spread fits your reading", which are different problems with different
	// fixes. It also means the enumerator is never entered for a request that
	// could not have produced an answer.
	if ext.HP <= 0 || (ext.CP <= 0 && ext.ArcLevel <= 0) {
		writeJSONError(w,
			"not enough to solve from: need an HP and either a CP or an arc level",
			http.StatusBadRequest)
		return
	}

	trainerLevel := 40
	h.db.QueryRow(`SELECT COALESCE(trainer_level, 40) FROM users WHERE id = ?`, u.ID).Scan(&trainerLevel)

	var pokeList []pokemonStatEntry
	_ = json.Unmarshal(h.store.Pokemon(), &pokeList)
	var cpms []cpmEntry
	_ = json.Unmarshal(h.store.CPMultipliers(), &cpms)
	if len(cpms) == 0 {
		writeJSONError(w, "game data unavailable", http.StatusServiceUnavailable)
		return
	}

	env := solveEnv{
		pokeList:     pokeList,
		cpms:         cpms,
		trainerLevel: trainerLevel,
		// The app read the arc itself. 0 means it could not, and the solve reads
		// that as "no level to pin to" rather than as level zero.
		arc:      arcReading{Level: ext.ArcLevel, OK: ext.ArcLevel > 0},
		lucky:    trueOrNil(ext.IsLucky),
		shadow:   trueOrNil(ext.IsShadow),
		purified: trueOrNil(ext.IsPurified),
		// No image and no recognised text on this path, so neither the CP re-read
		// nor the candy nickname fallback can run. Both are nil rather than
		// stubbed, so the solve skips them instead of calling something empty.
		retryCP:   nil,
		candyText: "",
		// The app knows which variant it is looking at. Passing it means Black
		// Kyurem is solved as Black Kyurem.
		form: strOr(rd.Form, ""),
		// The app is the source of truth for what it read. The server solves it
		// and hands it back; it does not talk over it.
		correctReading: false,
	}

	resp := solveScan(ext, env)

	advisories := advisoriesFor(ext, findSpeciesForm(pokeList, ext.PokemonName, env.form), env)
	if cands, isList := resp["candidates"].([]IVCandidate); isList {
		if a := appraisalDisagreement(ext.AppraisalBars, cands, serverDataVersion(pokeList, cpms)); a != nil {
			advisories = append(advisories, *a)
		}
	}
	if unknown := unknownJSONFields(body); len(unknown) > 0 {
		advisories = append(advisories, scanAdvisory{
			Field:       "body",
			Value:       strings.Join(unknown, ","),
			Constraint:  "fields the server does not define; a newer client, or a misspelled key",
			DataVersion: serverDataVersion(pokeList, cpms),
		})
	}
	if len(advisories) > 0 {
		resp["advisories"] = advisories
	}

	// One line per submission, carrying what the telemetry questions need: which
	// path served it, which build sent it, whether it solved, and how many
	// advisory flags it raised. A rate of flags across accounts is the signal
	// that the server's game data has fallen behind; a rate on one account is a
	// review matter.
	log.Printf("IV scan submit: user=%d build=%d detector=%q species=%q cp=%d hp=%d dust=%d arc=%.1f bars=%d count=%v advisories=%d",
		u.ID, sub.Client.Build, sub.Client.Detector, ext.PokemonName,
		ext.CP, ext.HP, ext.RawDust, ext.ArcLevel, ext.AppraisalBars,
		resp["count"], len(advisories))
	for _, a := range advisories {
		log.Printf("IV scan advisory: user=%d field=%s value=%q constraint=%q data=%q",
			u.ID, a.Field, a.Value, a.Constraint, a.DataVersion)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// readingToExtracted resolves the wire form into the internal reading.
//
// This is where the sentinels are restored, and it is the only place they are.
// An absent appraisal_bars is -1, "the app could not read the appraisal", NOT 0,
// which is a genuine nought-star Pokemon; an absent arc_level is 0.0, which the
// solve reads as "no level to pin to". Defaulting the first of those to zero
// would invent a claim about the spread that no reader made, and it would then
// be used to filter the solve.
func readingToExtracted(rd scanReading) ocrExtracted {
	return ocrExtracted{
		CP:             intOr(rd.CP, 0),
		CPSource:       strOr(rd.CPSource, ""),
		HP:             intOr(rd.HP, 0),
		RawDust:        intOr(rd.RawDust, 0),
		NormalisedDust: intOr(rd.NormalisedDust, 0),
		PokemonName:    strOr(rd.PokemonName, ""),
		NameSource:     strOr(rd.NameSource, ""),
		AppraisalBars:  intOr(rd.AppraisalBars, -1),
		IsHundo:        boolOr(rd.IsHundo, false),
		IsLucky:        boolOr(rd.IsLucky, false),
		IsShadow:       boolOr(rd.IsShadow, false),
		IsPurified:     boolOr(rd.IsPurified, false),
		RawCP:          strOr(rd.RawCP, ""),
		ArcLevel:       floatOr(rd.ArcLevel, 0),
	}
}

// fieldNames returns the JSON names a struct defines, for unknownJSONFields.
func fieldNames(v any) map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		name := t.Field(i).Tag.Get("json")
		if comma := strings.Index(name, ","); comma >= 0 {
			name = name[:comma]
		}
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func floatOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// trueOrNil converts an asserted status flag into the solver's tri-state.
//
// The solver reads nil as "unknown", which keeps every dust interpretation in
// play, and a non-nil value as authoritative in BOTH directions. A false flag
// therefore must not become a pointer to false: the lucky banner or the shadow
// aura is often simply off the top of the frame, and asserting its absence would
// throw away the interpretation that explains the dust cost.
func trueOrNil(v bool) *bool {
	if v {
		return boolPtr(true)
	}
	return nil
}

// LimitByAccount rate limits on the authenticated account rather than the
// address it connected from.
//
// Every other limiter in the app keys on the IP, which is the right key for
// login and registration, where there is no account yet. It is the wrong key
// here. A shared mobile network puts a whole town behind one address, so a
// per-IP limit either throttles innocent trainers or is set so loose it stops
// nothing; and one person wanting more than their share needs only a VPN.
//
// Submission requires a session, so the account is available and is the thing
// actually worth bounding. The IP stays as the fallback key for a request with
// no session, which on this route means one that is about to be refused anyway.
func (h *Handlers) LimitByAccount(requests int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(requests, window, httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
		if u := h.currentUser(r); u != nil {
			return fmt.Sprintf("account:%d", u.ID), nil
		}
		return httprate.KeyByRealIP(r)
	}))
}
