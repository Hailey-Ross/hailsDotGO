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
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// shinyOverrideRow is one row of shiny_dex_overrides, joined with its author's username.
type shinyOverrideRow struct {
	Dex           int
	Region        string
	InGo          *bool
	ShinyReleased *bool
	ReleaseDate   string
	Note          string
	UpdatedBy     string
	UpdatedAt     string
}

// loadShinyOverrides reads the whole override table. It is a few hundred rows at most.
func (h *Handlers) loadShinyOverrides() ([]shinyOverrideRow, error) {
	// Both dates are formatted in SQL and scanned as strings rather than parsed into time.Time. A
	// release date is a calendar day, and giving it a clock would only invite a timezone bug.
	rows, err := h.db.Query(`
		SELECT o.dex, o.region, o.in_go, o.shiny_released,
		       COALESCE(DATE_FORMAT(o.release_date, '%Y-%m-%d'), ''), o.note,
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
		if err := rows.Scan(&r.Dex, &r.Region, &inGo, &shiny, &r.ReleaseDate, &r.Note, &r.UpdatedBy, &r.UpdatedAt); err != nil {
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
	regional := make([]pogodata.RegionalShinyOverride, 0, len(rows))
	for _, r := range rows {
		if r.Region == "" {
			species = append(species, pogodata.ShinyOverride{
				Dex: r.Dex, Region: "", InGo: r.InGo, ShinyReleased: r.ShinyReleased,
				ReleaseDate: r.ReleaseDate,
			})
			continue
		}
		// in_go is meaningless for a form, so a row with neither a flag nor a date has nothing to
		// tell the client. Checking the date too is what keeps a date-only form row alive.
		if r.ShinyReleased == nil && r.ReleaseDate == "" {
			continue
		}
		// The override table is keyed by dex; the client keys its form table by species name, so
		// the name has to be recovered before the row can reach it.
		name := h.store.ShinyBaselineName(r.Dex)
		if name == "" {
			continue
		}
		regional = append(regional, pogodata.RegionalShinyOverride{
			Species: name, Region: r.Region, ShinyReleased: r.ShinyReleased, ReleaseDate: r.ReleaseDate,
		})
	}

	h.store.SetShinyOverrides(species)
	// Rows, not a finished blob: the store resolves a passed release date on its own clock, so a
	// form's announced day arrives on the hourly tick rather than waiting for the next admin write.
	h.store.SetRegionalShinyOverrides(regional)
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
	ReleaseDate          string `json:"release_date,omitempty"`
	ReleasedByDate       bool   `json:"released_by_date,omitempty"`
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
		// A form has no baseline row, so the same precedence is applied here rather than in the
		// store: explicit flag beats a passed date beats the compiled-in default.
		if o, ok := byKey[strconv.Itoa(dex)+"|"+f.Region]; ok {
			row.ReleaseDate = o.ReleaseDate
			if pogodata.ShinyDateReached(o.ReleaseDate) {
				row.ShinyReleased = true
				row.ReleasedByDate = true
			}
			if o.ShinyReleased != nil {
				row.ShinyReleased = *o.ShinyReleased
				row.ReleasedByDate = row.ShinyReleased && row.ReleasedByDate
			}
			row.Overridden = o.ShinyReleased != nil || o.ReleaseDate != ""
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
	// the only record of intent the table has. ReleaseDate follows the same contract: nil leaves
	// the stored date alone, "" clears it, "YYYY-MM-DD" sets it.
	Note        *string `json:"note"`
	ReleaseDate *string `json:"release_date"`
}

// shinyDateHorizon caps how far ahead a release date may be set. Niantic announces weeks out, not
// years, so anything past this is a mistyped year rather than a plan, and catching it here beats
// leaving it parked in the table until somebody notices a card that never unlocks.
const shinyDateHorizon = 3 // years

// parseShinyReleaseDate validates an announced release date. Empty means "no date", which is always
// allowed. Mirrors parseCaughtAt in shinies.go: same layout, same UTC-midnight reading, same refusal
// to accept a day before Pokemon GO existed.
func parseShinyReleaseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", errors.New("release date must look like 2026-08-12")
	}
	if t.Before(goLaunch) {
		return "", errors.New("release date is before Pokemon GO launched")
	}
	if t.After(time.Now().UTC().AddDate(shinyDateHorizon, 0, 0)) {
		return "", errors.New("release date is too far in the future")
	}
	return t.Format("2006-01-02"), nil
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

	// Whatever the request did not mention keeps whatever is already stored. One read up front is
	// what lets the write below be a single statement instead of one variant per tri-state field.
	stored, err := h.shinyOverrideNoteAndDate(dex, region)
	if err != nil {
		return http.StatusInternalServerError, "could not read the existing override"
	}
	note := stored.note
	if in.Note != nil {
		note = *in.Note
	}
	date := stored.date
	if in.ReleaseDate != nil {
		parsed, err := parseShinyReleaseDate(*in.ReleaseDate)
		if err != nil {
			return http.StatusBadRequest, err.Error()
		}
		date = parsed
	}

	// dateShiny is what shiny_released resolves to with NO explicit flag: the default, turned on by
	// an announced date that has arrived. Comparing against THIS rather than defShiny is what stops
	// an automatic release from being frozen into the table as an explicit one the next time an
	// admin edits anything else on the row.
	dateShiny := defShiny || pogodata.ShinyDateReached(date)

	inGo, shiny := defInGo, dateShiny
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
	var inGoCol, shinyCol, dateCol any
	if region == "" && inGo != defInGo {
		inGoCol = inGo
	}
	if shiny != dateShiny {
		shinyCol = shiny
	}
	if date != "" {
		dateCol = date
	}

	// An override row that overrides nothing is worse than no row: effectiveFlags reads both NULLs
	// as "no opinion", so the admin panel reports it as not overridden, draws no Reset button and
	// never shows its note. It would be invisible and unremovable from the UI. A note alone is not
	// a reason to keep one, so this drops the row whatever the note says.
	//
	// A DATE is different and must keep the row alive: it is data trainers see on the checklist and
	// the thing that will release the shiny on its own, not a comment about a decision.
	if inGoCol == nil && shinyCol == nil && dateCol == nil {
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

	// One statement, because note and date were already resolved above. The read-then-write is not
	// atomic, but the racing scenario is two admins editing the same species in the same second.
	const upsert = `
		INSERT INTO shiny_dex_overrides (dex, region, in_go, shiny_released, release_date, note, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE in_go = VALUES(in_go), shiny_released = VALUES(shiny_released),
		                        release_date = VALUES(release_date), note = VALUES(note),
		                        updated_by = VALUES(updated_by)`
	if _, err := h.db.Exec(upsert, dex, region, inGoCol, shinyCol, dateCol, note, actor); err != nil {
		log.Printf("shiny dex write dex=%d region=%q: %v", dex, region, err)
		return http.StatusInternalServerError, "could not save the override"
	}
	return 0, ""
}

// shinyOverrideStored is the part of a row the write path has to preserve when the request stays
// silent about it.
type shinyOverrideStored struct {
	note string
	date string
}

// shinyOverrideNoteAndDate reads the note and release date already on a row. A missing row is not an
// error: it is the ordinary case of a first write, and both fields come back empty.
func (h *Handlers) shinyOverrideNoteAndDate(dex int, region string) (shinyOverrideStored, error) {
	var out shinyOverrideStored
	err := h.db.QueryRow(`
		SELECT note, COALESCE(DATE_FORMAT(release_date, '%Y-%m-%d'), '')
		  FROM shiny_dex_overrides WHERE dex = ? AND region = ?`, dex, region).Scan(&out.note, &out.date)
	if errors.Is(err, sql.ErrNoRows) {
		return shinyOverrideStored{}, nil
	}
	return out, err
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
	// The resolved row, not an echo of the request: a passed release date can turn shiny_released
	// on by itself, so the panel has to be told what the row actually says now rather than assuming
	// its checkboxes were the last word.
	state := h.shinyRowState(dex, strings.TrimSpace(in.Region))
	state["ok"] = true
	json.NewEncoder(w).Encode(state)
}

// shinyRowState resolves one row the way the GET endpoint would, so a save can patch the panel in
// place without reloading the whole dex.
func (h *Handlers) shinyRowState(dex int, region string) map[string]any {
	var inGo, shiny bool
	if region == "" {
		inGo, shiny, _ = h.store.ShinyEffectiveDefaults(dex)
	} else {
		inGo = true
		shiny = regionalShinyDefault(h.store.ShinyBaselineName(dex), region)
	}

	var releasedByDate, overridden bool
	date := ""
	var dbInGo, dbShiny sql.NullBool
	var dbDate string
	err := h.db.QueryRow(`
		SELECT in_go, shiny_released, COALESCE(DATE_FORMAT(release_date, '%Y-%m-%d'), '')
		  FROM shiny_dex_overrides WHERE dex = ? AND region = ?`, dex, region).Scan(&dbInGo, &dbShiny, &dbDate)
	if err == nil {
		overridden = true
		date = dbDate
		if pogodata.ShinyDateReached(date) {
			shiny, releasedByDate = true, true
		}
		if dbShiny.Valid {
			shiny = dbShiny.Bool
			releasedByDate = shiny && releasedByDate
		}
		if dbInGo.Valid {
			inGo = dbInGo.Bool
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("shinyRowState dex=%d region=%q: %v", dex, region, err)
	}
	if shiny {
		inGo = true
	}

	return map[string]any{
		"in_go":            inGo,
		"shiny_released":   shiny,
		"release_date":     date,
		"released_by_date": releasedByDate,
		"overridden":       overridden,
	}
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
