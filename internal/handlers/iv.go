package handlers

import (
	"encoding/json"
	"math"
	"net/http"
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
	TopStat       string `json:"top_stat"`        // "atk", "def", "sta", or "" to skip filter
	AppraisalBars *int   `json:"appraisal_bars"` // nil or absent = skip; 0-3 = apply star filter
}

// dustBrackets maps stardust power-up cost to the level range it implies.
// Each bracket covers MinLvl through MaxLvl+0.5 (the loop adds an extra half-step).
// 10,000 dust appears twice (levels 39-40 and 41-42) to cover both regular and XL candy ranges.
// Purified Pokémon cost 10% less; 5400 is the purified equivalent of the standard 6000 tier.
var dustBrackets = []struct{ Dust, MinLvl, MaxLvl int }{
	{200, 1, 2}, {400, 3, 4}, {600, 5, 6}, {800, 7, 8},
	{1000, 9, 10}, {1300, 11, 12}, {1600, 13, 14}, {1900, 15, 16},
	{2200, 17, 18}, {2500, 19, 20}, {3000, 21, 22}, {3500, 23, 24},
	{4000, 25, 26}, {4500, 27, 28}, {5000, 29, 30},
	{5400, 31, 32}, {6000, 31, 32}, {7000, 33, 34}, {8000, 35, 36}, {9000, 37, 38},
	{10000, 39, 40}, {10000, 41, 42},
	{12000, 43, 44}, {15000, 45, 46}, {17500, 47, 48}, {20000, 49, 51},
}

// appraisalRange maps PoGo star rating (0-3) to the total IV sum range it implies.
var appraisalRange = [4][2]int{
	{0, 22},  // 0 stars
	{23, 29}, // 1 star
	{30, 36}, // 2 stars
	{37, 45}, // 3 stars
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

func enumerateIVs(req ivRequest, poke pokemonStatEntry, cpms []cpmEntry) []IVCandidate {
	// Collect all dust brackets matching the requested cost (10k dust covers two ranges).
	var minLvl, maxLvl int
	for _, b := range dustBrackets {
		if b.Dust != req.DustCost {
			continue
		}
		if minLvl == 0 || b.MinLvl < minLvl {
			minLvl = b.MinLvl
		}
		if b.MaxLvl > maxLvl {
			maxLvl = b.MaxLvl
		}
	}
	if minLvl == 0 {
		return []IVCandidate{}
	}

	// Wild/hatched Pokemon cannot exceed trainer level + 2, capped at 51.
	if maxAllowed := req.TrainerLevel + 2; maxLvl > maxAllowed {
		maxLvl = maxAllowed
	}
	if maxLvl > 51 {
		maxLvl = 51
	}

	ivSumMin, ivSumMax := 0, 45
	if req.AppraisalBars != nil && *req.AppraisalBars >= 0 && *req.AppraisalBars <= 3 {
		ivSumMin = appraisalRange[*req.AppraisalBars][0]
		ivSumMax = appraisalRange[*req.AppraisalBars][1]
	}

	cpmByLevel := make(map[float64]float64, len(cpms))
	for _, e := range cpms {
		cpmByLevel[e.Level] = e.Multiplier
	}

	candidates := make([]IVCandidate, 0)
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
				for lvl := float64(minLvl); lvl <= float64(maxLvl)+0.5; lvl += 0.5 {
					cpm, ok := cpmByLevel[lvl]
					if !ok {
						continue
					}
					if cpForLevelCalc(poke.BaseAttack, poke.BaseDefense, poke.BaseStamina, atkIV, defIV, staIV, cpm) != req.CP {
						continue
					}
					if hpForLevel(poke.BaseStamina, staIV, cpm) != req.HP {
						continue
					}
					candidates = append(candidates, IVCandidate{
						AtkIV: atkIV, DefIV: defIV, StaIV: staIV,
						Level: lvl,
						CP:    req.CP,
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

	candidates := enumerateIVs(req, *poke, cpms)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"candidates": candidates,
		"count":      len(candidates),
		"definitive": len(candidates) == 1,
		"pokemon":    poke,
	})
}

// Pokemon box types and handlers

type pokemonBoxEntry struct {
	ID           uint64          `json:"id"`
	PokemonName  string          `json:"pokemon_name"`
	Form         string          `json:"form"`
	CP           int             `json:"cp"`
	Level        float64         `json:"level"`
	AtkIV        *int            `json:"atk_iv"`
	DefIV        *int            `json:"def_iv"`
	StaIV        *int            `json:"sta_iv"`
	IVPct        *float64        `json:"iv_pct,omitempty"`
	IVCandidates json.RawMessage `json:"iv_candidates,omitempty"`
	CaughtAt     *time.Time      `json:"caught_at,omitempty"`
	Note         string          `json:"note"`
	CreatedAt    time.Time       `json:"created_at"`
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
	if len(body.Note) > 160 {
		body.Note = body.Note[:160]
	}

	const maxPokemonBoxSize = 3000
	var boxCount int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_pokemon_box WHERE user_id = ?`, u.ID).Scan(&boxCount); err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if boxCount >= maxPokemonBoxSize {
		writeJSONError(w, "box is full (3000 Pokémon limit)", http.StatusConflict)
		return
	}

	var candidatesJSON any
	if len(body.IVCandidates) > 0 && string(body.IVCandidates) != "null" {
		candidatesJSON = []byte(body.IVCandidates)
	}

	res, err := h.db.Exec(`
		INSERT INTO user_pokemon_box
		    (user_id, pokemon_name, form, cp, level, atk_iv, def_iv, sta_iv, iv_candidates, caught_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, body.PokemonName, body.Form, body.CP, body.Level,
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
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	h.db.QueryRow(`SELECT COUNT(*) FROM user_pokemon_box WHERE user_id = ?`, u.ID).Scan(&total)

	rows, err := h.db.Query(`
		SELECT id, pokemon_name, form, cp, level, atk_iv, def_iv, sta_iv,
		       iv_candidates, caught_at, COALESCE(note,''), created_at
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
			&e.ID, &e.PokemonName, &e.Form, &e.CP, &e.Level,
			&e.AtkIV, &e.DefIV, &e.StaIV,
			&candidatesRaw, &e.CaughtAt, &e.Note, &e.CreatedAt,
		); err != nil {
			continue
		}
		if len(candidatesRaw) > 0 {
			e.IVCandidates = json.RawMessage(candidatesRaw)
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

