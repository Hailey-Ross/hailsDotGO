package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

type shinyRecord struct {
	ID        uint       `json:"id"`
	PokemonID string     `json:"pokemon_id"`
	Form      string     `json:"form"`
	Region    string     `json:"region"`
	Costume   string     `json:"costume"`
	EventTag  string     `json:"event_tag"`
	Method    string     `json:"method"`
	CaughtAt  time.Time  `json:"caught_at"`
	EvolvedAt *time.Time `json:"evolved_at"`
}

func (h *Handlers) ShiniesPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "shinies", nil)
}

func (h *Handlers) APIShiniesGet(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, pokemon_id, form, region, costume, event_tag, method, caught_at, evolved_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC, id DESC`,
		u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []shinyRecord{}
	for rows.Next() {
		var s shinyRecord
		if err := rows.Scan(&s.ID, &s.PokemonID, &s.Form, &s.Region, &s.Costume, &s.EventTag, &s.Method, &s.CaughtAt, &s.EvolvedAt); err != nil {
			continue
		}
		out = append(out, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// goLaunch is the earliest date a shiny could have been caught. Pokemon GO launched on
// 2016-07-06, so anything before it is a typo rather than a very patient trainer.
var goLaunch = time.Date(2016, 7, 6, 0, 0, 0, 0, time.UTC)

// parseCaughtAt reads the "YYYY-MM-DD" a trainer picked.
//
// It is optional everywhere: empty means "do not set it", so an insert falls back to the column
// default and an update leaves the stored value alone. Until now nothing ever wrote this column, so
// it recorded when the ROW was added rather than when the Pokemon was caught -- people log catches
// days later, and the trainer page now shows this date, so it has to be the trainer's.
func parseCaughtAt(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("not a date")
	}
	// Tomorrow, not today: the viewer's clock may legitimately be a few hours ahead of the server's.
	if t.After(time.Now().UTC().AddDate(0, 0, 1)) {
		return time.Time{}, false, fmt.Errorf("that date is in the future")
	}
	if t.Before(goLaunch) {
		return time.Time{}, false, fmt.Errorf("that is before Pokemon GO existed")
	}
	return t, true, nil
}

// shinyAddInput is the JSON body both the web and mobile add endpoints accept. pokemon_id is
// the species NAME string (e.g. "Growlithe"), stored verbatim; it is only resolved to a dex id
// at read time to build the sprite.
type shinyAddInput struct {
	PokemonID string `json:"pokemon_id"`
	Form      string `json:"form"`
	Region    string `json:"region"`
	Costume   string `json:"costume"`
	EventTag  string `json:"event_tag"`
	Method    string `json:"method"`
	CaughtAt  string `json:"caught_at"`
}

// insertShiny validates a decoded add payload and inserts one shiny for the user. On failure it
// returns a non-zero HTTP status and an i18n key suitable for writeJSONError; on success the new
// row id with status 0. Duplicates are allowed by design (migration 33 dropped the unique
// constraint), so there is no 409 path.
func (h *Handlers) insertShiny(userID uint, body shinyAddInput) (int64, int, string) {
	body.PokemonID = strings.TrimSpace(body.PokemonID)
	if body.PokemonID == "" {
		return 0, http.StatusBadRequest, "error.shiny_pokemon_required"
	}
	if !validRegions[body.Region] {
		return 0, http.StatusBadRequest, "error.invalid_json"
	}
	caughtAt, hasCaughtAt, err := parseCaughtAt(body.CaughtAt)
	if err != nil {
		return 0, http.StatusBadRequest, "error.shiny_caught_at"
	}

	// No date given means today. Filling it in here rather than letting the column's
	// DEFAULT CURRENT_TIMESTAMP do it keeps every row on one clock: a calendar day at UTC midnight,
	// which is the only thing the client knows how to read back.
	if !hasCaughtAt {
		caughtAt = time.Now().UTC().Truncate(24 * time.Hour)
	}
	result, err := h.db.Exec(`
		INSERT INTO user_shinies (user_id, pokemon_id, form, region, costume, event_tag, method, caught_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, body.PokemonID, body.Form, body.Region, body.Costume, body.EventTag, body.Method, caughtAt,
	)
	if err != nil {
		return 0, http.StatusInternalServerError, "error.db"
	}
	id, _ := result.LastInsertId()
	return id, 0, ""
}

func (h *Handlers) APIShiniesAdd(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body shinyAddInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	id, code, msgKey := h.insertShiny(u.ID, body)
	if code != 0 {
		writeJSONError(w, h.t(r, msgKey), code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "ok": true})
}

func (h *Handlers) APIShiniesUpdate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Form     string `json:"form"`
		Region   string `json:"region"`
		Costume  string `json:"costume"`
		EventTag string `json:"event_tag"`
		Method   string `json:"method"`
		CaughtAt string `json:"caught_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if !validRegions[body.Region] {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	caughtAt, hasCaughtAt, err := parseCaughtAt(body.CaughtAt)
	if err != nil {
		writeJSONError(w, h.t(r, "error.shiny_caught_at"), http.StatusBadRequest)
		return
	}

	// An omitted date leaves the stored one alone, so a client that does not send the field (or a
	// row whose date was never set) is untouched.
	if hasCaughtAt {
		_, err = h.db.Exec(
			`UPDATE user_shinies SET form = ?, region = ?, costume = ?, event_tag = ?, method = ?, caught_at = ?
			 WHERE id = ? AND user_id = ?`,
			body.Form, body.Region, body.Costume, body.EventTag, body.Method, caughtAt, id, u.ID,
		)
	} else {
		_, err = h.db.Exec(
			`UPDATE user_shinies SET form = ?, region = ?, costume = ?, event_tag = ?, method = ? WHERE id = ? AND user_id = ?`,
			body.Form, body.Region, body.Costume, body.EventTag, body.Method, id, u.ID,
		)
	}
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIShiniesDelete(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM user_shinies WHERE id = ? AND user_id = ?`, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) APIShiniesEvolve(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Into   string `json:"into"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.Into = strings.TrimSpace(body.Into)
	if body.Into == "" {
		writeJSONError(w, "missing target form", http.StatusBadRequest)
		return
	}
	if !validRegions[body.Region] {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	// Read the row first, for two reasons that used to be missing entirely.
	//
	// One, it is the ownership check. The UPDATE below is scoped to user_id, but
	// its RowsAffected was never inspected, so evolving a row belonging to someone
	// else changed nothing and still answered 204. A caller could not tell that
	// apart from success.
	//
	// Two, it is the only way to know what the entry currently IS. Without it the
	// handler took the target on trust and wrote it verbatim, so any authenticated
	// caller could turn a Magikarp into a Rayquaza, or into anything else at all.
	var current string
	switch err := h.db.QueryRow(
		`SELECT pokemon_id FROM user_shinies WHERE id = ? AND user_id = ?`, id, u.ID,
	).Scan(&current); {
	case err == sql.ErrNoRows:
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	case err != nil:
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	// The evolution has to be one the game actually has. The table is compiled in,
	// so it is always available and there is no warm up case to handle.
	//
	// Regional and costumed entries are safe because the table is keyed on the bare
	// species name with its forms unioned, which is what this column holds: an
	// Alolan Rattata is stored as "Rattata" with its region in its own column. See
	// pogodata.evolutionTargets.
	if !pogodata.CanEvolveTo(current, body.Into) {
		writeJSONError(w, fmt.Sprintf("%s does not evolve into %s", current, body.Into), http.StatusBadRequest)
		return
	}

	res, err := h.db.Exec(
		// UTC_TIMESTAMP, not NOW: caught_at is written in UTC from Go, and the two dates on one row
		// should not be on different clocks.
		`UPDATE user_shinies SET pokemon_id = ?, region = ?, evolved_at = UTC_TIMESTAMP() WHERE id = ? AND user_id = ?`,
		body.Into, body.Region, id, u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	// Still checked, even though the row was just read: the two statements are not
	// in one transaction, so a delete in between is possible. Rare, but "changed
	// nothing" must never answer 204.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type publicShinyRecord struct {
	// ID is sent ONLY to the owner of the collection, so their own profile can link straight to the
	// entry in the shiny checklist. omitempty keeps it out of everyone else's payload entirely:
	// nobody needs another trainer's row ids.
	ID        uint64 `json:"id,omitempty"`
	PokemonID string `json:"pokemon_id"`
	// Dex is the SPECIES dex number, which is what the trainer page sorts the expanded collection
	// by. It cannot be read back off SpriteURL: costume art is not keyed by dex at all, and a
	// regional sprite carries its PokeAPI variant id (Alolan Vulpix is 10091, not 37).
	Dex       int        `json:"dex"`
	Form      string     `json:"form"`
	Region    string     `json:"region"`
	Costume   string     `json:"costume"`
	EventTag  string     `json:"event_tag"`
	Method    string     `json:"method"`
	SpriteURL string     `json:"sprite_url"`
	CaughtAt  time.Time  `json:"caught_at"`
	EvolvedAt *time.Time `json:"evolved_at"`
}

// MobileShiniesOfUser is APIShiniesOfUser with absolute sprite URLs.
//
// It exists because the app renders THIS payload without ApiClient.absoluteUrl:
// TrainerProfileScreen.kt passes shiny.spriteUrl straight to the image loader, unlike
// ShinyCollectionScreen which wraps it. Every other sprite field can be site relative
// because the app resolves it; this one cannot, and no later server change rescues a build
// already on a phone.
//
// A mobile only wrapper rather than wrapping inside APIShiniesOfUser, because that handler
// also serves the website (server.go registers it on both trees) and baseURL falls back to
// pogo.hails.app when BASE_URL is unset. Wrapping there would point a self hosted site's
// sprites at our server. Same shape as toMobileTrainer, for the same reason.
//
// This also fixes a break that predates the sprite proxy: costume sprite paths have always
// been site relative, so costumed shinies on another trainer's profile were already blank in
// the app.
func (h *Handlers) MobileShiniesOfUser(w http.ResponseWriter, r *http.Request) {
	h.shiniesOfUser(w, r, true)
}

func (h *Handlers) APIShiniesOfUser(w http.ResponseWriter, r *http.Request) {
	h.shiniesOfUser(w, r, false)
}

func (h *Handlers) shiniesOfUser(w http.ResponseWriter, r *http.Request, absolute bool) {
	username := chi.URLParam(r, "username")

	var userID int
	var profilePublic, shiniesHidden int
	err := h.db.QueryRow(`
		SELECT id, COALESCE(profile_public,0), COALESCE(shinies_hidden,0)
		FROM users WHERE username = ? AND disabled = 0 AND deleted_at IS NULL`, username,
	).Scan(&userID, &profilePublic, &shiniesHidden)
	if err != nil || profilePublic == 0 || shiniesHidden == 1 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	// The owner gets the row id back so their own profile can link straight to the entry in the
	// shiny checklist. This is the only thing the caller's identity changes here.
	caller := h.currentUser(r)
	isOwner := caller != nil && int(caller.ID) == userID

	rows, err := h.db.Query(`
		SELECT id, pokemon_id, form, region, costume, event_tag, method, caught_at, evolved_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC, id DESC`, userID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []publicShinyRecord{}
	for rows.Next() {
		var s publicShinyRecord
		var id uint64
		if err := rows.Scan(&id, &s.PokemonID, &s.Form, &s.Region, &s.Costume, &s.EventTag, &s.Method, &s.CaughtAt, &s.EvolvedAt); err != nil {
			continue
		}
		if isOwner {
			s.ID = id
		}
		s.Dex = h.store.PokemonDexID(s.PokemonID)
		s.SpriteURL = h.resolveShinySpriteURL(s.PokemonID, s.Region, s.Costume)
		if absolute {
			s.SpriteURL = absoluteURL(s.SpriteURL)
		}
		out = append(out, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// MobileShiniesGet returns the caller's own shiny collection as a bare JSON array of
// publicShinyRecord, each carrying a server-resolved sprite_url so the app renders costume,
// regional, and base sprites without duplicating the sprite tables. The row id is always
// included (this is the caller's own collection), so the app can PUT/DELETE by id.
func (h *Handlers) MobileShiniesGet(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, pokemon_id, form, region, costume, event_tag, method, caught_at, evolved_at
		FROM user_shinies WHERE user_id = ? ORDER BY caught_at DESC, id DESC`, u.ID,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []publicShinyRecord{}
	for rows.Next() {
		var s publicShinyRecord
		if err := rows.Scan(&s.ID, &s.PokemonID, &s.Form, &s.Region, &s.Costume, &s.EventTag, &s.Method, &s.CaughtAt, &s.EvolvedAt); err != nil {
			continue
		}
		s.Dex = h.store.PokemonDexID(s.PokemonID)
		s.SpriteURL = h.resolveShinySpriteURL(s.PokemonID, s.Region, s.Costume)
		out = append(out, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// MobileShiniesAdd records a shiny for the caller and returns {id, sprite_url}, so the app can
// render the new entry immediately without a follow-up fetch. Shares insertShiny with the web
// add endpoint; the only difference is the response shape.
func (h *Handlers) MobileShiniesAdd(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body shinyAddInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	id, code, msgKey := h.insertShiny(u.ID, body)
	if code != 0 {
		writeJSONError(w, h.t(r, msgKey), code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":         id,
		"sprite_url": h.resolveShinySpriteURL(strings.TrimSpace(body.PokemonID), body.Region, body.Costume),
	})
}

// shinyMethods is the canonical add-form method list, mirroring the METHODS array in
// ts/shinies.ts. Kept in order; labels resolve through i18n so they track translations.
var shinyMethods = []struct{ value, i18n string }{
	{"", "shinies.js.method_any"},
	{"wild", "shinies.js.method_wild"},
	{"egg", "shinies.js.method_egg"},
	{"raid", "shinies.js.method_raid"},
	{"research", "shinies.js.method_research"},
	{"evolution", "shinies.js.method_evolution"},
	{"photobomb", "shinies.js.method_photobomb"},
	{"trade", "shinies.js.method_trade"},
	{"go_pass", "shinies.js.method_go_pass"},
	{"go_tour", "shinies.js.method_go_tour"},
}

// regionLabel is the display name for a region tag, translated.
//
// The site already carries these labels as locale keys (`js.common.form_alolan` and friends), and
// the browser renders regional names through them. This endpoint did not: it called humanizeRegion,
// which is pure string manipulation, so a trainer reading the site in German got a localized
// species name with an English adjective welded to it. "Alolan Rattfratz".
//
// Going through the keys fixes the punctuation for free, which humanizeRegion could never have got
// right by upper-casing words. The table spells them: `pau` is "Pa'u" and not "Pau", `pom_pom` is
// "Pom-Pom", and both striped Basculin are hyphenated.
//
// humanizeRegion stays as the fallback, because the tags this is called with include the Unown
// letters and Vivillon patterns, which have no key of their own and never will: there are 48 of
// them and "Unown B" needs no translating.
func (h *Handlers) regionLabel(r *http.Request, region string) string {
	if region == "" {
		return humanizeRegion(region)
	}
	key := "js.common.form_" + region
	// TFunc answers with the key itself when it has no translation, which is the only
	// signal available that a lookup missed.
	if v := h.t(r, key); v != key {
		return v
	}
	return humanizeRegion(region)
}

// humanizeRegion turns a region tag like "dusk_mane" into a display label "Dusk Mane". Labels are
// best-effort: the authoritative ones live client-side in ts/shared/regionalForms.ts, and this
// endpoint only exists so the Add form's pickers can stay roughly in sync.
func humanizeRegion(v string) string {
	if v == "" {
		return "Base"
	}
	words := strings.Split(strings.ReplaceAll(v, "_", " "), " ")
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = upperFirst(word)
	}
	return strings.Join(words, " ")
}

// MobileShiniesReference feeds the app's Add form pickers so its region and method options stay
// in sync with the server. Regions come from validRegions (humanized), methods from the shared
// enum above (labels translated). The app degrades gracefully if this 404s, so it is not blocking.
func (h *Handlers) MobileShiniesReference(w http.ResponseWriter, r *http.Request) {
	type option struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}

	regionValues := make([]string, 0, len(validRegions))
	for v := range validRegions {
		regionValues = append(regionValues, v)
	}
	sort.Strings(regionValues)

	regions := make([]option, 0, len(regionValues))
	for _, v := range regionValues {
		regions = append(regions, option{Value: v, Label: h.regionLabel(r, v)})
	}

	methods := make([]option, 0, len(shinyMethods))
	for _, m := range shinyMethods {
		methods = append(methods, option{Value: m.value, Label: h.t(r, m.i18n)})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"regions": regions, "methods": methods})
}

// resolveShinySpriteURL builds the server-side sprite URL for a stored shiny,
// preferring a regional/forme slug, then a costume sprite, then the plain base shiny.
// Regional variants keep their own PokeAPI sprite slug and intentionally ignore the costume,
// matching the client where regional cards do not carry costume art. Nothing sorts on this
// string: the species dex travels as publicShinyRecord.Dex. Returns "" when nothing resolves.
func (h *Handlers) resolveShinySpriteURL(pokemonID, region, costume string) string {
	if slug := regionalSpriteSlug(pokemonID, region); slug != "" {
		return spriteURLSlug(slug, "shiny")
	}
	if id := h.store.PokemonDexID(pokemonID); id != 0 {
		if curl, ok := costumeSpriteURL(id, pokemonID, costume); ok {
			return curl
		}
		return pokemonSpriteURL(id, "shiny")
	}
	return ""
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeJSONErrorCode is writeJSONError with a machine readable `code` beside the
// prose, so a client can attach a failure to the field that caused it.
//
// The settings and auth routes have answered this shape for a while and the app's
// error routing was built against it; everything else answered `{"error": msg}`
// alone, so a native client had two choices, match on English prose or
// reimplement the validation rule locally to know which field to mark. The report
// form was doing the second for two of its rules.
//
// `code` is a STABLE identifier and part of the contract, so it must not be
// reworded when the message is. Where a route already has an i18n key for the
// message, that key is the natural code and the settings routes use it that way.
// Where the message is a bare English string, the code is a short snake_case name
// chosen once and left alone.
func writeJSONErrorCode(w http.ResponseWriter, msg, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}
