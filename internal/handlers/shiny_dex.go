package handlers

// Admin editing of shiny availability.
//
// The embedded baseline in internal/pogodata says which species are in Pokemon GO and which have a
// shiny released. This file is the escape hatch: when Niantic ships a shiny, an admin ticks a box
// and it is live, instead of a data edit plus a rebuild plus a deploy. Overrides live in
// shiny_dex_overrides and are pushed into the store, which recomputes the served blobs.
//
// The table is SPARSE on purpose. A row that agrees with the baseline in every column is deleted
// rather than stored, mirroring how sprite_locks treats min_rank 0. That way a corrected baseline
// shipped later takes effect instead of losing to a stale override nobody remembers setting.

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// shinyOverrideRow is one row of shiny_dex_overrides, joined with its author's username.
type shinyOverrideRow struct {
	Dex           int
	Region        string
	InGo          *bool
	ShinyReleased *bool
	Note          string
	UpdatedBy     string
	UpdatedAt     string
}

// loadShinyOverrides reads the whole override table. It is a few hundred rows at most.
func (h *Handlers) loadShinyOverrides() ([]shinyOverrideRow, error) {
	rows, err := h.db.Query(`
		SELECT o.dex, o.region, o.in_go, o.shiny_released, o.note,
		       COALESCE(u.username, ''), DATE_FORMAT(o.updated_at, '%Y-%m-%d')
		  FROM shiny_dex_overrides o
		  LEFT JOIN users u ON u.id = o.updated_by
		 ORDER BY o.dex, o.region`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []shinyOverrideRow{}
	for rows.Next() {
		var r shinyOverrideRow
		// TINYINT(1) NULL scans cleanly into *bool via sql.NullBool.
		var inGo, shiny sql.NullBool
		if err := rows.Scan(&r.Dex, &r.Region, &inGo, &shiny, &r.Note, &r.UpdatedBy, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if inGo.Valid {
			v := inGo.Bool
			r.InGo = &v
		}
		if shiny.Valid {
			v := shiny.Bool
			r.ShinyReleased = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// reloadShinyOverrides pushes the override table into the store. Called once at startup and after
// every admin write. A full reload beats incremental invalidation at this size, and it means there
// is exactly one code path that can put an override into the store.
//
// A missing table is tolerated with a log line, the way reloadLangs tolerates a missing locales
// table: deploying the binary before running migration 44 must not brick startup.
func (h *Handlers) reloadShinyOverrides() {
	rows, err := h.loadShinyOverrides()
	if err != nil {
		log.Printf("reloadShinyOverrides: %v (serving baseline defaults)", err)
		return
	}

	species := make([]pogodata.ShinyOverride, 0, len(rows))
	// regional is species NAME -> region tag -> shiny_released, the shape the client overlays onto
	// its compiled-in defaults.
	regional := map[string]map[string]bool{}
	for _, r := range rows {
		if r.Region == "" {
			species = append(species, pogodata.ShinyOverride{
				Dex: r.Dex, Region: "", InGo: r.InGo, ShinyReleased: r.ShinyReleased,
			})
			continue
		}
		if r.ShinyReleased == nil {
			continue // in_go is meaningless for a form; nothing to tell the client
		}
		name := h.store.ShinyBaselineName(r.Dex)
		if name == "" {
			continue
		}
		if regional[name] == nil {
			regional[name] = map[string]bool{}
		}
		regional[name][r.Region] = *r.ShinyReleased
	}

	h.store.SetShinyOverrides(species)
	if len(regional) == 0 {
		h.store.SetRegionalShinyOverrides(nil)
		return
	}
	if blob, err := json.Marshal(regional); err == nil {
		h.store.SetRegionalShinyOverrides(blob)
	} else {
		log.Printf("reloadShinyOverrides: marshal regional: %v", err)
	}
}

// ---------------------------------------------------------------------- GET

type adminShinySpeciesOut struct {
	pogodata.AdminShinySpecies
	Note      string `json:"note,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type adminShinyRegionalOut struct {
	Dex                  int    `json:"dex"`
	Name                 string `json:"name"`
	Region               string `json:"region"`
	SpriteURL            string `json:"sprite_url"`
	ShinyReleased        bool   `json:"shiny_released"`
	DefaultShinyReleased bool   `json:"default_shiny_released"`
	Overridden           bool   `json:"overridden"`
	Note                 string `json:"note,omitempty"`
	UpdatedBy            string `json:"updated_by,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

// AdminGetShinyDex returns every species and every regional form with its effective flags, the
// baseline defaults behind them, and who overrode what. Provenance travels with the value so the
// panel can answer "why is this on?" without a second request.
func (h *Handlers) AdminGetShinyDex(w http.ResponseWriter, r *http.Request) {
	overrides, err := h.loadShinyOverrides()
	if err != nil {
		writeJSONError(w, "shiny dex overrides are unavailable (has migration 44 run?)", http.StatusInternalServerError)
		return
	}
	byKey := make(map[string]shinyOverrideRow, len(overrides))
	for _, o := range overrides {
		byKey[strconv.Itoa(o.Dex)+"|"+o.Region] = o
	}

	species := h.store.ShinyDexAdmin()
	speciesOut := make([]adminShinySpeciesOut, 0, len(species))
	dexByName := make(map[string]int, len(species))
	for _, s := range species {
		dexByName[s.Name] = s.Dex
		row := adminShinySpeciesOut{AdminShinySpecies: s}
		if o, ok := byKey[strconv.Itoa(s.Dex)+"|"]; ok {
			row.Note, row.UpdatedBy, row.UpdatedAt = o.Note, o.UpdatedBy, o.UpdatedAt
		}
		speciesOut = append(speciesOut, row)
	}

	regional := regionalFormRows()
	regionalOut := make([]adminShinyRegionalOut, 0, len(regional))
	for _, f := range regional {
		dex := dexByName[f.Species]
		def := regionalShinyDefault(f.Species, f.Region)
		row := adminShinyRegionalOut{
			Dex:                  dex,
			Name:                 f.Species,
			Region:               f.Region,
			SpriteURL:            spriteURLSlug(f.Slug, "shiny"),
			ShinyReleased:        def,
			DefaultShinyReleased: def,
		}
		if o, ok := byKey[strconv.Itoa(dex)+"|"+f.Region]; ok && o.ShinyReleased != nil {
			row.ShinyReleased = *o.ShinyReleased
			row.Overridden = true
			row.Note, row.UpdatedBy, row.UpdatedAt = o.Note, o.UpdatedBy, o.UpdatedAt
		}
		regionalOut = append(regionalOut, row)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"species":  speciesOut,
		"regional": regionalOut,
	})
}

// ---------------------------------------------------------------------- writes

type shinyDexWriteInput struct {
	Region        string `json:"region"`
	InGo          *bool  `json:"in_go"`
	ShinyReleased *bool  `json:"shiny_released"`
	// A nil Note means "leave whatever is there alone". The row editor sends only the flags, and
	// wiping the reason somebody recorded an override every time a checkbox moves would destroy
	// the only record of intent the table has.
	Note *string `json:"note"`
}

// applyShinyDexWrite validates one edit and either stores it or deletes the row when the result
// matches what the app would do anyway. Returns an HTTP status and a message; 0 means success.
func (h *Handlers) applyShinyDexWrite(userID uint, dex int, in shinyDexWriteInput) (int, string) {
	if dex < 1 || !h.store.ShinyDexHasDex(dex) {
		return http.StatusNotFound, "unknown Pokedex number"
	}
	region := strings.TrimSpace(in.Region)
	if len(region) > 16 {
		return http.StatusBadRequest, "region is too long"
	}
	if in.Note != nil && len(*in.Note) > 255 {
		return http.StatusBadRequest, "note is too long"
	}

	name := h.store.ShinyBaselineName(dex)
	var defInGo, defShiny bool
	if region == "" {
		// EFFECTIVE default (baseline unioned with upstream), not the baseline alone. See
		// ShinyEffectiveDefaults: comparing to the baseline here silently discards an admin
		// turning off a shiny that only upstream provides.
		defInGo, defShiny, _ = h.store.ShinyEffectiveDefaults(dex)
	} else {
		if !validRegions[region] || !regionalFormExists(name, region) {
			return http.StatusNotFound, "unknown regional form for this species"
		}
		// in_go is meaningless for a form: the game either has the form or it does not, and that
		// is the species' business. Only shiny_released is editable here.
		if in.InGo != nil {
			return http.StatusBadRequest, "in_go cannot be set on a regional form"
		}
		defShiny = regionalShinyDefault(name, region)
		defInGo = true
	}

	inGo, shiny := defInGo, defShiny
	if in.InGo != nil {
		inGo = *in.InGo
	}
	if in.ShinyReleased != nil {
		shiny = *in.ShinyReleased
	}
	if shiny && !inGo {
		return http.StatusBadRequest, "a released shiny requires the species to be in Pokemon GO"
	}

	// Store only what actually differs, so a row is a minimal statement of intent and a corrected
	// baseline shipped later takes over instead of losing to a stale no-op.
	var inGoCol, shinyCol any
	if region == "" && inGo != defInGo {
		inGoCol = inGo
	}
	if shiny != defShiny {
		shinyCol = shiny
	}

	// An override row that overrides nothing is worse than no row: effectiveFlags reads both NULLs
	// as "no opinion", so the admin panel reports it as not overridden, draws no Reset button and
	// never shows its note. It would be invisible and unremovable from the UI. A note alone is not
	// a reason to keep one, so this drops the row whatever the note says.
	if inGoCol == nil && shinyCol == nil {
		if _, err := h.db.Exec(`DELETE FROM shiny_dex_overrides WHERE dex = ? AND region = ?`, dex, region); err != nil {
			return http.StatusInternalServerError, "could not clear the override"
		}
		return 0, ""
	}

	// updated_by is a nullable FK: an unresolved session must store NULL, not 0, which no user has.
	var actor any
	if userID != 0 {
		actor = userID
	}

	// Two statements rather than one, so a nil note genuinely leaves the stored note alone. Folding
	// that into a single query needs COALESCE gymnastics against a NOT NULL column, and getting it
	// subtly wrong is how the reason for an override quietly disappears.
	const setNote = `
		INSERT INTO shiny_dex_overrides (dex, region, in_go, shiny_released, note, updated_by)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE in_go = VALUES(in_go), shiny_released = VALUES(shiny_released),
		                        note = VALUES(note), updated_by = VALUES(updated_by)`
	const keepNote = `
		INSERT INTO shiny_dex_overrides (dex, region, in_go, shiny_released, note, updated_by)
		VALUES (?, ?, ?, ?, '', ?)
		ON DUPLICATE KEY UPDATE in_go = VALUES(in_go), shiny_released = VALUES(shiny_released),
		                        updated_by = VALUES(updated_by)`

	var err error
	if in.Note != nil {
		_, err = h.db.Exec(setNote, dex, region, inGoCol, shinyCol, *in.Note, actor)
	} else {
		_, err = h.db.Exec(keepNote, dex, region, inGoCol, shinyCol, actor)
	}
	if err != nil {
		log.Printf("shiny dex write dex=%d region=%q: %v", dex, region, err)
		return http.StatusInternalServerError, "could not save the override"
	}
	return 0, ""
}

// AdminSetShinyDexFlags sets the flags for one species or one regional form.
func (h *Handlers) AdminSetShinyDexFlags(w http.ResponseWriter, r *http.Request) {
	dex, err := strconv.Atoi(chi.URLParam(r, "dex"))
	if err != nil {
		writeJSONError(w, "invalid Pokedex number", http.StatusBadRequest)
		return
	}
	var in shinyDexWriteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if status, msg := h.applyShinyDexWrite(h.actorID(r), dex, in); status != 0 {
		writeJSONError(w, msg, status)
		return
	}
	h.reloadShinyOverrides()
	w.Header().Set("Content-Type", "application/json")
	// Trimmed, so the answer describes the row the write actually touched.
	json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"overridden": h.isOverridden(dex, strings.TrimSpace(in.Region)),
	})
}

// AdminResetShinyDexFlags drops an override so the baseline default takes over again.
func (h *Handlers) AdminResetShinyDexFlags(w http.ResponseWriter, r *http.Request) {
	dex, err := strconv.Atoi(chi.URLParam(r, "dex"))
	if err != nil {
		writeJSONError(w, "invalid Pokedex number", http.StatusBadRequest)
		return
	}
	region := r.URL.Query().Get("region")
	if _, err := h.db.Exec(`DELETE FROM shiny_dex_overrides WHERE dex = ? AND region = ?`, dex, region); err != nil {
		writeJSONError(w, "could not clear the override", http.StatusInternalServerError)
		return
	}
	h.reloadShinyOverrides()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBulkSetShinyDexFlags applies many edits in one request, for "accept all suggestions" and
// multi-select. Capped so a malformed client cannot walk the whole dex in a single call.
func (h *Handlers) AdminBulkSetShinyDexFlags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			Dex int `json:"dex"`
			shinyDexWriteInput
		} `json:"items"`
	}
	// Bounded before decoding: 200 items of this shape is a few KB, and the cap below only helps
	// after the whole body has already been buffered.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	const maxBulk = 200
	if len(body.Items) == 0 {
		writeJSONError(w, "no items", http.StatusBadRequest)
		return
	}
	if len(body.Items) > maxBulk {
		writeJSONError(w, "too many items in one request", http.StatusBadRequest)
		return
	}

	actor := h.actorID(r)
	applied := 0
	errs := []string{}
	for _, it := range body.Items {
		if status, msg := h.applyShinyDexWrite(actor, it.Dex, it.shinyDexWriteInput); status != 0 {
			errs = append(errs, strconv.Itoa(it.Dex)+": "+msg)
			continue
		}
		applied++
	}
	h.reloadShinyOverrides()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "applied": applied, "errors": errs})
}

// actorID is the acting admin's user id, or 0 when the session cannot be resolved. The column is
// nullable and only there for the audit trail, so a 0 stores as NULL rather than failing the write.
func (h *Handlers) actorID(r *http.Request) uint {
	if u := h.currentUser(r); u != nil {
		return u.ID
	}
	return 0
}

// isOverridden reports whether a row currently exists for this species or form.
func (h *Handlers) isOverridden(dex int, region string) bool {
	var n int
	h.db.QueryRow(`SELECT COUNT(*) FROM shiny_dex_overrides WHERE dex = ? AND region = ?`, dex, region).Scan(&n)
	return n > 0
}
