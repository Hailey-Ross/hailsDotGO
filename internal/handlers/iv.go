package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type ivPageData struct {
	TrainerLevel int
}

func (h *Handlers) GetIVPage(w http.ResponseWriter, r *http.Request) {
	pd := ivPageData{}
	if u := h.currentUser(r); u != nil {
		h.db.QueryRow(`SELECT COALESCE(trainer_level, 0) FROM users WHERE id = ?`, u.ID).Scan(&pd.TrainerLevel)
	}
	h.render(w, r, "iv", pd)
}

// GetBoxPage renders the Pokemon box. The box is a trainer's own Pokemon, so
// the page needs no server data: everything comes from /api/iv/pokemon, which
// already requires a session.
func (h *Handlers) GetBoxPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "box", nil)
}

// IV calculation types

type cpmEntry struct {
	Level      float64 `json:"level"`
	Multiplier float64 `json:"multiplier"`
}

type pokemonStatEntry struct {
	BaseAttack  int    `json:"base_attack"`
	BaseDefense int    `json:"base_defense"`
	BaseStamina int    `json:"base_stamina"`
	Form        string `json:"form"`
	PokemonName string `json:"pokemon_name"`
	PokemonID   int    `json:"pokemon_id"`
}

type IVCandidate struct {
	AtkIV int     `json:"atk_iv"`
	DefIV int     `json:"def_iv"`
	StaIV int     `json:"sta_iv"`
	Level float64 `json:"level"`
	CP    int     `json:"cp"`
	HP    int     `json:"hp"`
	IVPct float64 `json:"iv_pct"`
}

type ivRequest struct {
	PokemonName   string `json:"pokemon_name"`
	Form          string `json:"form"`
	CP            int    `json:"cp"`
	HP            int    `json:"hp"`
	DustCost      int    `json:"dust_cost"`
	TrainerLevel  int    `json:"trainer_level"`
	TopStat       string `json:"top_stat"`       // "atk", "def", "sta", or "" to skip filter
	AppraisalBars *int   `json:"appraisal_bars"` // nil or absent = skip; 0-3 = apply star filter
	IsLucky       *bool  `json:"is_lucky"`       // nil = unknown; dust interpretations stay fuzzy
	IsShadow      *bool  `json:"is_shadow"`
	IsPurified    *bool  `json:"is_purified"`
}

// dustBrackets maps the BASE stardust power-up cost to the level range it implies.
// Each bracket covers MinLvl through MaxLvl+0.5 (levelRange adds the half-step).
// Tiers change every 2.0 levels, odd-aligned; 15,000 covers only 49-49.5 because
// level 50 cannot power up. Status discounts (lucky/shadow/purified) are applied
// on top of these base tiers by dustCandidates, never baked into this table.
var dustBrackets = []struct{ Dust, MinLvl, MaxLvl int }{
	{200, 1, 2}, {400, 3, 4}, {600, 5, 6}, {800, 7, 8},
	{1000, 9, 10}, {1300, 11, 12}, {1600, 13, 14}, {1900, 15, 16},
	{2200, 17, 18}, {2500, 19, 20}, {3000, 21, 22}, {3500, 23, 24},
	{4000, 25, 26}, {4500, 27, 28}, {5000, 29, 30},
	{6000, 31, 32}, {7000, 33, 34}, {8000, 35, 36}, {9000, 37, 38},
	{10000, 39, 40}, {11000, 41, 42}, {12000, 43, 44}, {13000, 45, 46},
	{14000, 47, 48}, {15000, 49, 49},
}

// dustMods are the status modifiers that change the DISPLAYED dust cost.
// Shadow excludes lucky and purified (shadows cannot be traded or stay shadow
// once purified). Best Buddy and friendship never change power-up costs.
// Every product is an exact integer because all tiers are multiples of 100.
var dustMods = []struct {
	Mult                    float64
	Lucky, Shadow, Purified bool
}{
	{1.0, false, false, false},
	{0.5, true, false, false},
	{1.2, false, true, false},
	{0.9, false, false, true},
	{0.45, true, false, true}, // purified Pokemon received in a lucky trade
}

// levelRange is one (dust tier, status modifier) interpretation of a scanned
// dust cost: the level window to sweep plus the flags that explain it.
type levelRange struct {
	MinLvl, MaxLvl          float64 // inclusive bounds, swept in 0.5 steps
	BaseTier                int     // the base dust tier behind this interpretation
	Lucky, Shadow, Purified bool
}

// dustCandidates expands a displayed power-up dust cost into every (tier,
// modifier) interpretation consistent with the status flags. A nil flag means
// unknown: the lucky banner or shadow aura may simply be off-frame, so both
// readings stay in play. A non-nil flag is authoritative in both directions
// (explicit user input on the manual tab).
func dustCandidates(displayedDust int, lucky, shadow, purified *bool) []levelRange {
	if displayedDust <= 0 {
		return nil
	}
	match := func(flag *bool, modHas bool) bool { return flag == nil || *flag == modHas }
	var out []levelRange
	for _, m := range dustMods {
		if !match(lucky, m.Lucky) || !match(shadow, m.Shadow) || !match(purified, m.Purified) {
			continue
		}
		for _, b := range dustBrackets {
			if int(math.Round(float64(b.Dust)*m.Mult)) != displayedDust {
				continue
			}
			out = append(out, levelRange{
				MinLvl:   float64(b.MinLvl),
				MaxLvl:   float64(b.MaxLvl) + 0.5,
				BaseTier: b.Dust,
				Lucky:    m.Lucky, Shadow: m.Shadow, Purified: m.Purified,
			})
		}
	}
	return out
}

// maxPowerUpLevel is the highest level a Pokemon can be powered up to:
// trainer level + 10, capped at 50. (An active Best Buddy displays stats one
// level higher; callers handle that with a +1 retry, up to 51.)
func maxPowerUpLevel(trainerLevel int) float64 {
	lvl := float64(trainerLevel + 10)
	if lvl > 50 {
		lvl = 50
	}
	return lvl
}

// appraisalRange maps PoGo star rating (0-3) to the total IV sum range it implies.
var appraisalRange = [4][2]int{
	{0, 22},  // 0 stars
	{23, 29}, // 1 star
	{30, 36}, // 2 stars
	{37, 45}, // 3 stars
}

// cpmLookup builds a level -> CPM map, extends it to level 51, and re-derives
// the XL half-levels (40.5-50.5) with the game's rules. Needed because the
// live upstream (pogoapi cp_multiplier.json) currently stops at level 45.0
// and rounds half-levels to 4 decimals, which breaks the exact-match CP
// search and the arc inversion (both need precise values through level 50/51).
// Rules, verified against GoIV/Silph to within 1e-7 on 2026-07-05:
//
//	whole levels above 40:  CPM(L+1) = CPM(L) + 0.005
//	half levels:            CPM(L+0.5) = sqrt((CPM(L)^2 + CPM(L+1)^2) / 2)
func cpmLookup(cpms []cpmEntry) map[float64]float64 {
	m := make(map[float64]float64, len(cpms)+22)
	for _, e := range cpms {
		m[e.Level] = e.Multiplier
	}
	// Whole XL levels first: fill any gap upward from the last known value.
	for lvl := 41.0; lvl <= 51.0; lvl++ {
		if _, ok := m[lvl]; ok {
			continue
		}
		if prev, ok := m[lvl-1]; ok {
			m[lvl] = prev + 0.005
		}
	}
	// Then the XL half-levels from their whole-level neighbours.
	for lvl := 40.5; lvl <= 50.5; lvl++ {
		lo, okLo := m[lvl-0.5]
		hi, okHi := m[lvl+0.5]
		if okLo && okHi {
			m[lvl] = math.Sqrt((lo*lo + hi*hi) / 2)
		}
	}
	return m
}

func cpForLevelCalc(baseAtk, baseDef, baseSta, atkIV, defIV, staIV int, cpm float64) int {
	atk := float64(baseAtk + atkIV)
	def := float64(baseDef + defIV)
	sta := float64(baseSta + staIV)
	cp := int(math.Floor(atk * math.Sqrt(def) * math.Sqrt(sta) * cpm * cpm / 10))
	if cp < 10 {
		return 10
	}
	return cp
}

func hpForLevel(baseSta, staIV int, cpm float64) int {
	hp := int(math.Floor(float64(baseSta+staIV) * cpm))
	if hp < 10 {
		return 10
	}
	return hp
}

// enumerateIVs derives candidate level ranges from the displayed dust cost and
// status flags, then enumerates matching IV combinations.
func enumerateIVs(req ivRequest, poke pokemonStatEntry, cpms []cpmEntry) ([]IVCandidate, bool) {
	ranges := dustCandidates(req.DustCost, req.IsLucky, req.IsShadow, req.IsPurified)
	return enumerateWithBuddyRetry(req, ranges, poke, cpms)
}

// intersectRangesWithLevel narrows level ranges to those overlapping
// [lvl-tol, lvl+tol] (arc-derived level with bracket-edge tolerance). When
// nothing overlaps, or no ranges are known (dust unreadable or absent on a
// maxed Pokemon), the arc level stands alone.
func intersectRangesWithLevel(ranges []levelRange, lvl, tol float64) []levelRange {
	lo, hi := lvl-tol, lvl+tol
	if lo < 1 {
		lo = 1
	}
	var out []levelRange
	for _, r := range ranges {
		if r.MaxLvl < lo || r.MinLvl > hi {
			continue
		}
		nr := r
		if nr.MinLvl < lo {
			nr.MinLvl = lo
		}
		if nr.MaxLvl > hi {
			nr.MaxLvl = hi
		}
		out = append(out, nr)
	}
	if len(out) == 0 {
		out = []levelRange{{MinLvl: lo, MaxLvl: hi}}
	}
	return out
}

// enumerateWithBuddyRetry runs the enumeration and, when the first pass finds
// nothing, retries one level higher: an active Best Buddy displays CP and HP
// boosted by one level while the dust cost reflects the true level. The bool
// result reports whether that Best-Buddy interpretation was used.
func enumerateWithBuddyRetry(req ivRequest, ranges []levelRange, poke pokemonStatEntry, cpms []cpmEntry) ([]IVCandidate, bool) {
	levelCap := maxPowerUpLevel(req.TrainerLevel)
	candidates := enumerateIVsRanges(req, ranges, levelCap, poke, cpms)
	if len(candidates) > 0 {
		return candidates, false
	}
	shifted := make([]levelRange, len(ranges))
	for i, lr := range ranges {
		shifted[i] = lr
		shifted[i].MinLvl += 1
		shifted[i].MaxLvl += 1
	}
	if buddy := enumerateIVsRanges(req, shifted, levelCap+1, poke, cpms); len(buddy) > 0 {
		return buddy, true
	}
	return candidates, false
}

// enumerateIVsRanges brute-forces all IV combinations over the given level
// ranges. req.CP <= 0 means the CP is unknown (arc-only scan): the CP filter
// is skipped and each candidate carries its computed CP instead.
func enumerateIVsRanges(req ivRequest, ranges []levelRange, levelCap float64, poke pokemonStatEntry, cpms []cpmEntry) []IVCandidate {
	candidates := make([]IVCandidate, 0)
	if len(ranges) == 0 {
		return candidates
	}

	ivSumMin, ivSumMax := 0, 45
	if req.AppraisalBars != nil && *req.AppraisalBars >= 0 && *req.AppraisalBars <= 3 {
		ivSumMin = appraisalRange[*req.AppraisalBars][0]
		ivSumMax = appraisalRange[*req.AppraisalBars][1]
	}

	cpmByLevel := cpmLookup(cpms)

	// Union the ranges into a deduplicated, sorted set of candidate levels
	// (interpretations from different modifiers can overlap).
	levelSet := make(map[float64]bool)
	for _, lr := range ranges {
		maxLvl := lr.MaxLvl
		if maxLvl > levelCap {
			maxLvl = levelCap
		}
		for lvl := lr.MinLvl; lvl <= maxLvl; lvl += 0.5 {
			levelSet[lvl] = true
		}
	}
	levels := make([]float64, 0, len(levelSet))
	for lvl := range levelSet {
		levels = append(levels, lvl)
	}
	sort.Float64s(levels)

	for atkIV := 0; atkIV <= 15; atkIV++ {
		for defIV := 0; defIV <= 15; defIV++ {
			for staIV := 0; staIV <= 15; staIV++ {
				ivSum := atkIV + defIV + staIV
				if ivSum < ivSumMin || ivSum > ivSumMax {
					continue
				}
				switch req.TopStat {
				case "atk":
					if atkIV < defIV || atkIV < staIV {
						continue
					}
				case "def":
					if defIV < atkIV || defIV < staIV {
						continue
					}
				case "sta":
					if staIV < atkIV || staIV < defIV {
						continue
					}
				}
				for _, lvl := range levels {
					cpm, ok := cpmByLevel[lvl]
					if !ok {
						continue
					}
					cp := cpForLevelCalc(poke.BaseAttack, poke.BaseDefense, poke.BaseStamina, atkIV, defIV, staIV, cpm)
					if req.CP > 0 && cp != req.CP {
						continue
					}
					if hpForLevel(poke.BaseStamina, staIV, cpm) != req.HP {
						continue
					}
					candidates = append(candidates, IVCandidate{
						AtkIV: atkIV, DefIV: defIV, StaIV: staIV,
						Level: lvl,
						CP:    cp,
						HP:    req.HP,
						IVPct: math.Round(float64(ivSum)/45.0*1000) / 10,
					})
				}
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].IVPct != candidates[j].IVPct {
			return candidates[i].IVPct > candidates[j].IVPct
		}
		return candidates[i].Level < candidates[j].Level
	})
	return candidates
}

func (h *Handlers) IVCalculate(w http.ResponseWriter, r *http.Request) {
	var req ivRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.PokemonName == "" || req.CP < 10 || req.CP > 50000 ||
		req.HP < 1 || req.HP > 999 || req.TrainerLevel < 1 || req.TrainerLevel > 51 {
		writeJSONError(w, "invalid parameters", http.StatusBadRequest)
		return
	}

	var pokeList []pokemonStatEntry
	if err := json.Unmarshal(h.store.Pokemon(), &pokeList); err != nil {
		writeJSONError(w, "data unavailable", http.StatusServiceUnavailable)
		return
	}
	var poke *pokemonStatEntry
	var firstMatch *pokemonStatEntry
	for i := range pokeList {
		if !strings.EqualFold(pokeList[i].PokemonName, req.PokemonName) {
			continue
		}
		if req.Form != "" {
			if strings.EqualFold(pokeList[i].Form, req.Form) {
				poke = &pokeList[i]
				break
			}
			continue
		}
		// No form specified: prefer Normal form, but track first match as fallback.
		if firstMatch == nil {
			firstMatch = &pokeList[i]
		}
		if strings.EqualFold(pokeList[i].Form, "Normal") {
			poke = &pokeList[i]
			break
		}
	}
	if poke == nil {
		poke = firstMatch
	}
	if poke == nil {
		writeJSONError(w, "pokemon not found", http.StatusNotFound)
		return
	}

	var cpms []cpmEntry
	if err := json.Unmarshal(h.store.CPMultipliers(), &cpms); err != nil {
		writeJSONError(w, "data unavailable", http.StatusServiceUnavailable)
		return
	}

	candidates, buddyAssumed := enumerateIVs(req, *poke, cpms)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"candidates":         candidates,
		"count":              len(candidates),
		"definitive":         len(candidates) == 1,
		"pokemon":            poke,
		"best_buddy_assumed": buddyAssumed,
	})
}

// Pokemon box types and handlers

type pokemonBoxEntry struct {
	ID           uint64          `json:"id"`
	PokemonName  string          `json:"pokemon_name"`
	Form         string          `json:"form"`
	IsShadow     *bool           `json:"is_shadow"`
	IsPurified   *bool           `json:"is_purified"`
	CP           int             `json:"cp"`
	Level        float64         `json:"level"`
	AtkIV        *int            `json:"atk_iv"`
	DefIV        *int            `json:"def_iv"`
	StaIV        *int            `json:"sta_iv"`
	IVPct        *float64        `json:"iv_pct,omitempty"`
	ApproxIVs    *approxIVs      `json:"approx_ivs,omitempty"`
	IVCandidates json.RawMessage `json:"iv_candidates,omitempty"`
	CaughtAt     *time.Time      `json:"caught_at,omitempty"`
	Note         string          `json:"note"`
	CreatedAt    time.Time       `json:"created_at"`
}

// boxFormPattern bounds the form field without pinning it to a list.
//
// This is the game's own form name (Normal, Alola, Galarian, Therian, and a
// long tail of costumes: 273 distinct values in the current dataset), not the
// shadow or purified state, so an allow list here would reject almost every
// real save. It only has to stay inside the column and carry nothing exotic.
var boxFormPattern = regexp.MustCompile(`^[A-Za-z0-9_ -]{0,64}$`)

// maxPokemonBoxSize caps a trainer's box. It also caps what the list endpoint
// will return in one page, because the raids page asks for the whole box to rank
// it against a boss, and a lower ceiling there would quietly score only the most
// recently added Pokemon.
const maxPokemonBoxSize = 9000

// pokemonGoLaunch is the earliest a Pokemon could have been caught.
var pokemonGoLaunch = time.Date(2016, 7, 6, 0, 0, 0, 0, time.UTC)

// approxIVs is the middle of a candidate set, for a Pokemon the calculator could
// not narrow to a single spread. Callers show it marked as approximate.
type approxIVs struct {
	AtkIV int `json:"atk_iv"`
	DefIV int `json:"def_iv"`
	StaIV int `json:"sta_iv"`
}

// averageIVCandidates reduces a stored candidate set to its mean spread. It
// returns nil for anything it cannot read, so a corrupt or empty set leaves the
// Pokemon marked as unknown rather than inventing a number for it.
func averageIVCandidates(raw []byte) *approxIVs {
	var cands []IVCandidate
	if err := json.Unmarshal(raw, &cands); err != nil || len(cands) == 0 {
		return nil
	}
	var a, d, s int
	for _, c := range cands {
		a += c.AtkIV
		d += c.DefIV
		s += c.StaIV
	}
	n := len(cands)
	return &approxIVs{
		AtkIV: int(math.Round(float64(a) / float64(n))),
		DefIV: int(math.Round(float64(d) / float64(n))),
		StaIV: int(math.Round(float64(s) / float64(n))),
	}
}

func (h *Handlers) SavePokemonIV(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var body struct {
		PokemonName  string          `json:"pokemon_name"`
		Form         string          `json:"form"`
		IsShadow     *bool           `json:"is_shadow"`
		IsPurified   *bool           `json:"is_purified"`
		CP           int             `json:"cp"`
		Level        float64         `json:"level"`
		AtkIV        *int            `json:"atk_iv"`
		DefIV        *int            `json:"def_iv"`
		StaIV        *int            `json:"sta_iv"`
		IVCandidates json.RawMessage `json:"iv_candidates"`
		CaughtAt     *time.Time      `json:"caught_at"`
		Note         string          `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.PokemonName == "" || body.CP < 10 || body.CP > 9999 || body.Level < 1 || body.Level > 50 {
		writeJSONError(w, "invalid parameters", http.StatusBadRequest)
		return
	}
	// Levels come in half steps. A value like 33.3 stores fine in DECIMAL(4,1),
	// displays fine, and then matches no CP multiplier, so the Pokemon silently
	// vanishes from every ranking with nothing to explain why.
	if body.Level*2 != math.Trunc(body.Level*2) {
		writeJSONError(w, "level must be a whole or half number", http.StatusBadRequest)
		return
	}

	// The three IVs and the form used to be taken on trust, because the only
	// caller was the calculator, which could not produce anything else. The box
	// page lets a trainer type them in directly, so they are checked here now.
	//
	// A nil IV is allowed and means "not worked out yet"; the box stores the
	// candidate spreads for that case.
	for _, iv := range []*int{body.AtkIV, body.DefIV, body.StaIV} {
		if iv != nil && (*iv < 0 || *iv > 15) {
			writeJSONError(w, "IVs must be between 0 and 15", http.StatusBadRequest)
			return
		}
	}
	if body.Form == "" {
		body.Form = "Normal"
	}
	if !boxFormPattern.MatchString(body.Form) {
		writeJSONError(w, "invalid form", http.StatusBadRequest)
		return
	}
	if len(body.PokemonName) > 64 {
		writeJSONError(w, "invalid parameters", http.StatusBadRequest)
		return
	}
	// Truncate on runes, not bytes. The column is VARCHAR(160), which MySQL
	// counts in characters, and the note field accepts 160 of them. A Japanese
	// or emoji note is three or four bytes per character, so a byte slice landed
	// mid rune, produced invalid utf8mb4, and the insert failed with a 500.
	if r := []rune(body.Note); len(r) > 160 {
		body.Note = string(r[:160])
	}

	// iv_candidates is stored as raw JSON. The body cap allows 2 MB, and with a
	// 9000 row box that is a great deal of storage per account for a field the
	// calculator fills with at most a few hundred spreads. Bound it here rather
	// than trusting the caller, since this endpoint now has a form in front of it.
	const maxCandidatesBytes = 256 << 10
	if len(body.IVCandidates) > maxCandidatesBytes {
		writeJSONError(w, "too many IV candidates", http.StatusBadRequest)
		return
	}

	// caught_at is optional and only ever a past date. An unbounded value either
	// fails the DATETIME range and 500s, or quietly stores a catch in the year
	// 9999.
	if body.CaughtAt != nil {
		if body.CaughtAt.Before(pokemonGoLaunch) || body.CaughtAt.After(time.Now().AddDate(0, 0, 1)) {
			writeJSONError(w, "invalid catch date", http.StatusBadRequest)
			return
		}
	}

	var boxCount int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_pokemon_box WHERE user_id = ?`, u.ID).Scan(&boxCount); err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if boxCount >= maxPokemonBoxSize {
		writeJSONError(w, "It's over 9000!", http.StatusConflict)
		return
	}

	var candidatesJSON any
	if len(body.IVCandidates) > 0 && string(body.IVCandidates) != "null" {
		candidatesJSON = []byte(body.IVCandidates)
	}

	res, err := h.db.Exec(`
		INSERT INTO user_pokemon_box
		    (user_id, pokemon_name, form, is_shadow, is_purified, cp, level,
		     atk_iv, def_iv, sta_iv, iv_candidates, caught_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, body.PokemonName, body.Form, body.IsShadow, body.IsPurified,
		body.CP, body.Level,
		body.AtkIV, body.DefIV, body.StaIV,
		candidatesJSON, body.CaughtAt, body.Note,
	)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id})
}

func (h *Handlers) ListPokemonIV(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	// The page list asks for 50. The raids page asks for the whole box, because
	// it ranks every Pokemon a trainer owns against the boss they have open, and
	// a cap there would silently judge only the most recently added ones.
	if limit <= 0 || limit > maxPokemonBoxSize {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	h.db.QueryRow(`SELECT COUNT(*) FROM user_pokemon_box WHERE user_id = ?`, u.ID).Scan(&total)

	rows, err := h.db.Query(`
		-- iv_candidates is the largest column a row holds and the raids page asks
		-- for the whole box, so it is read ONLY for the rows that can use it:
		-- those whose IVs were never pinned down. Selecting it unconditionally
		-- shipped megabytes across the wire from a separate database host for
		-- data this endpoint then deliberately threw away.
		SELECT id, pokemon_name, form, is_shadow, is_purified, cp, level,
		       atk_iv, def_iv, sta_iv,
		       CASE WHEN atk_iv IS NULL THEN iv_candidates END,
		       caught_at, COALESCE(note,''), created_at
		FROM user_pokemon_box WHERE user_id = ?
		ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		u.ID, limit, offset,
	)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	entries := make([]pokemonBoxEntry, 0)
	for rows.Next() {
		var e pokemonBoxEntry
		var candidatesRaw []byte
		if err := rows.Scan(
			&e.ID, &e.PokemonName, &e.Form, &e.IsShadow, &e.IsPurified,
			&e.CP, &e.Level,
			&e.AtkIV, &e.DefIV, &e.StaIV,
			&candidatesRaw, &e.CaughtAt, &e.Note, &e.CreatedAt,
		); err != nil {
			continue
		}
		// The raw candidate set is deliberately NOT returned by the list.
		//
		// It is the largest thing a row holds, and the raids page asks for the
		// whole box at once, so shipping it would make this the most expensive
		// read on the site for data no caller wants in full. Everything that
		// reads a half identified Pokemon wants one thing: the middle of the
		// possibilities. So that is computed here and the array stays on the
		// server.
		if e.AtkIV == nil && len(candidatesRaw) > 0 {
			if avg := averageIVCandidates(candidatesRaw); avg != nil {
				e.ApproxIVs = avg
			}
		}
		if e.AtkIV != nil && e.DefIV != nil && e.StaIV != nil {
			pct := math.Round(float64(*e.AtkIV+*e.DefIV+*e.StaIV)/45.0*1000) / 10
			e.IVPct = &pct
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"pokemon": entries,
		"total":   total,
	})
}

func (h *Handlers) DeletePokemonIV(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	res, err := h.db.Exec(
		`DELETE FROM user_pokemon_box WHERE id = ? AND user_id = ?`, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
