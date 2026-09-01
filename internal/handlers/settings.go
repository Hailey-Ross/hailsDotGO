package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/pogodata"
)

var trainerCodeRe = regexp.MustCompile(`^\d{12}$`)

// trainerNamePunct is the small punctuation set allowed in a trainer name, on top
// of Unicode letters, marks, numbers and spaces.
//
// Deliberately absent: < > " ' and &. The first two end any chance of injecting a
// tag, and the quotes end any chance of breaking out of an attribute value. The
// trainer name is rendered into innerHTML in the blocked-users list on the settings
// page, both as element content and inside a data-trainer="..." attribute, and until
// now it was checked for length and nothing else. Sixteen bytes is enough for
// <svg onload=a()>, which needs no interaction at all, or <base href=//a.bc, which
// silently re-points every relative URL on the page.
//
// Letters and marks are matched by Unicode category rather than by an ASCII range,
// so accented, Cyrillic, Greek, CJK and other non-Latin names all still work.
const trainerNamePunct = ".,!?-_()[]:;+*/#@"

func validTrainerName(name string) bool {
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsMark(r), unicode.IsDigit(r):
			continue
		case r == ' ':
			continue
		case strings.ContainsRune(trainerNamePunct, r):
			continue
		default:
			return false
		}
	}
	return true
}

var predefinedPronouns = map[string]bool{
	"":          true,
	"he/him":    true,
	"she/her":   true,
	"they/them": true,
	"any/all":   true,
}

var validLocationDisplays = map[string]bool{"none": true, "country": true, "full": true}

var validFavPokemonForms = map[string]bool{"": true, "shadow": true, "shiny": true, "primal": true}

// settingsInput is one trainer's profile settings as the write path sees them,
// whichever client sent them. The settings form fills it from r.FormValue; the
// mobile endpoint decodes JSON into it over the values already stored.
type settingsInput struct {
	TrainerName     string
	TrainerCode     string
	TrainerLevel    int
	Avatar          string
	Pronouns        string
	City            string
	Region          string
	Country         string
	LocationDisplay string
	ProfilePublic   bool
	ShiniesHidden   bool
	FavPokemon      string
	FavPokemonForm  string
}

// validateSettingsInput normalises and checks a settings write, returning the
// coerced input and the i18n key of the first rule that refused it, or "" when it
// passed.
//
// The rules divide in two. Most are silent coercions: an avatar the caller's rank
// cannot use, an unknown location display, a favourite that is not a real species.
// Those blank the field rather than refusing the write, which is what the form has
// always done. The rest are refusals, and the trainer name pair is the reason this
// has to be shared rather than reimplemented: that name is rendered into innerHTML
// and into a data-trainer attribute, and sixteen bytes is enough for
// <svg onload=a()>. A second write path that rewrote these rules could quietly drop
// that check, and nothing would reveal it until someone used it.
//
// classes, locks, rank and dexID are parameters rather than reads off h, so the
// whole rule set is testable with no database and no loaded store. None of it was
// reachable from a test before.
func validateSettingsInput(in settingsInput, classes []pogodata.TrainerClass, locks map[string]int, rank int, dexID func(string) int) (settingsInput, string) {
	in.TrainerName = strings.TrimSpace(in.TrainerName)
	in.TrainerCode = strings.ReplaceAll(strings.TrimSpace(in.TrainerCode), " ", "")
	in.City = strings.TrimSpace(in.City)
	in.Region = strings.TrimSpace(in.Region)
	in.Country = strings.TrimSpace(in.Country)
	in.FavPokemon = strings.TrimSpace(in.FavPokemon)

	// A predefined pronoun passes through untouched. Anything else is a custom one
	// and is capped at 32 characters, counted in runes so a non Latin pronoun is not
	// cut mid character.
	in.Pronouns = strings.TrimSpace(in.Pronouns)
	if !predefinedPronouns[in.Pronouns] {
		in.Pronouns = truncRunes(in.Pronouns, 32)
	}

	if !validLocationDisplays[in.LocationDisplay] {
		in.LocationDisplay = "none"
	}

	// The avatar must be one this rank unlocks. filteredTrainerClasses is the same
	// filter the picker itself is built from, so the two cannot disagree about what
	// is choosable.
	if in.Avatar != "" {
		allowed := false
		for _, tc := range filteredTrainerClasses(classes, locks, rank) {
			if tc.Slug == in.Avatar {
				allowed = true
				break
			}
		}
		if !allowed {
			in.Avatar = ""
		}
	}

	if !validFavPokemonForms[in.FavPokemonForm] {
		in.FavPokemonForm = ""
	}
	if in.FavPokemon != "" && dexID(in.FavPokemon) == 0 {
		in.FavPokemon = ""
		in.FavPokemonForm = ""
	}
	in.FavPokemon = truncRunes(in.FavPokemon, 64)

	switch {
	case len(in.TrainerName) > 16:
		return in, "error.trainer_name_length"
	case !validTrainerName(in.TrainerName):
		return in, "error.trainer_name_chars"
	case in.TrainerCode != "" && !trainerCodeRe.MatchString(in.TrainerCode):
		return in, "error.trainer_code_format"
	case in.TrainerLevel != 0 && (in.TrainerLevel < 1 || in.TrainerLevel > 80):
		return in, "error.trainer_level_range"
	case len(in.City) > 100 || len(in.Region) > 100 || len(in.Country) > 100:
		return in, "error.location_too_long"
	}
	return in, ""
}

// applySettings is the single write path for profile settings. The settings form
// and the mobile JSON endpoint both go through it, so no rule can be enforced by
// one client and skipped by the other.
//
// It returns the coerced input, which callers re-render or echo back, an i18n key
// naming the rule that refused the write, and an error for a server failure.
func (h *Handlers) applySettings(u *auth.User, in settingsInput) (settingsInput, string, error) {
	locks, _ := h.loadSpriteLocks()
	in, key := validateSettingsInput(in, h.store.TrainerClasses(), locks, userAwardGrantRank(u), h.store.PokemonDexID)
	if key != "" {
		return in, key, nil
	}

	// Uniqueness needs the database, so it sits out here rather than in the pure part.
	if in.TrainerName != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE LOWER(trainer_name) = LOWER(?) AND id != ?`, in.TrainerName, u.ID).Scan(&count)
		if count > 0 {
			return in, "error.trainer_name_taken", nil
		}
	}
	if in.TrainerCode != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE trainer_code = ? AND id != ?`, in.TrainerCode, u.ID).Scan(&count)
		if count > 0 {
			return in, "error.trainer_code_taken", nil
		}
	}

	// Derived here, never accepted from the caller.
	favSpriteURL := ""
	if in.FavPokemon != "" {
		if id := h.store.PokemonDexID(in.FavPokemon); id != 0 {
			favSpriteURL = pokemonSpriteURL(id, in.FavPokemonForm)
		}
	}

	_, err := h.db.Exec(
		`UPDATE users SET trainer_name=?, trainer_code=?, trainer_level=?, avatar=?, pronouns=?, city=?, region=?, country=?, location_display=?, profile_public=?, shinies_hidden=?, fav_pokemon=?, fav_pokemon_form=?, fav_sprite_url=? WHERE id=?`,
		in.TrainerName, in.TrainerCode, in.TrainerLevel, in.Avatar, in.Pronouns, in.City, in.Region, in.Country,
		in.LocationDisplay, boolToInt(in.ProfilePublic), boolToInt(in.ShiniesHidden), in.FavPokemon, in.FavPokemonForm, favSpriteURL, u.ID,
	)
	return in, "", err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// blockedUsers lists the accounts this trainer has blocked, most recent first.
// Always non nil, so a JSON caller gets [] rather than null.
func (h *Handlers) blockedUsers(userID uint) []blockedUserEntry {
	out := []blockedUserEntry{}
	rows, err := h.db.Query(`
		SELECT u.username, COALESCE(u.trainer_name,'')
		FROM user_blocks ub
		JOIN users u ON u.id = ub.blocked_id
		WHERE ub.user_id = ?
		ORDER BY ub.created_at DESC`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var be blockedUserEntry
		if rows.Scan(&be.Username, &be.TrainerName) == nil {
			out = append(out, be)
		}
	}
	return out
}

type blockedUserEntry struct {
	Username    string
	TrainerName string
}

type settingsData struct {
	Success         bool
	Error           string
	TrainerName     string
	TrainerCode     string
	TrainerLevel    int
	Avatar          string
	Pronouns        string
	City            string
	Region          string
	Country         string
	LocationDisplay string
	ProfilePublic   bool
	ShiniesHidden   bool
	FavPokemon      string
	FavPokemonForm  string
	TrainerClasses  []pogodata.TrainerClass
	PokemonList     []pogodata.PokemonEntry
	TagRequest      *tagRequestStatus
	BlockedUsers    []blockedUserEntry
}

func (h *Handlers) queryTagRequest(userID uint) *tagRequestStatus {
	var t tagRequestStatus
	err := h.db.QueryRow(
		`SELECT status, COALESCE(reject_reason,''), COALESCE(name,''), COALESCE(color,'#ec4899')
		 FROM custom_tag_requests WHERE user_id = ? ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&t.Status, &t.RejectReason, &t.Name, &t.Color)
	if err != nil {
		return nil
	}
	if t.Status == "rejected" {
		t.NextRequestAt = h.computeTagCooldown(userID)
	}
	return &t
}

func (h *Handlers) SettingsPage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	var d settingsData
	h.db.QueryRow(`
		SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(trainer_level,0), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(city,''), COALESCE(region,''), COALESCE(country,''),
		       COALESCE(location_display,'none'), COALESCE(profile_public,0), COALESCE(shinies_hidden,0),
		       COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,'')
		FROM users WHERE id = ?`, u.ID,
	).Scan(&d.TrainerName, &d.TrainerCode, &d.TrainerLevel, &d.Avatar, &d.Pronouns, &d.City, &d.Region, &d.Country, &d.LocationDisplay, &d.ProfilePublic, &d.ShiniesHidden, &d.FavPokemon, &d.FavPokemonForm)
	locks, _ := h.loadSpriteLocks()
	d.TrainerClasses = filteredTrainerClasses(h.store.TrainerClasses(), locks, userAwardGrantRank(u))
	d.PokemonList = h.store.PokemonList()
	d.TagRequest = h.queryTagRequest(u.ID)
	d.BlockedUsers = h.blockedUsers(u.ID)
	h.render(w, r, "settings", d)
}

// formPronouns resolves the settings form's two pronoun fields into the single
// value the write path stores. An unrecognised choice means none.
func formPronouns(r *http.Request) string {
	choice := r.FormValue("pronouns_choice")
	if choice == "custom" {
		return r.FormValue("pronouns_custom")
	}
	if predefinedPronouns[choice] {
		return choice
	}
	return ""
}

// settingsPageData builds the settings page render struct from a set of values.
// Shared by the success and failure renders so neither can quietly drop a section
// the other shows: the blocked list used to fall off both, and only reappeared if
// you reloaded the page.
func (h *Handlers) settingsPageData(u *auth.User, in settingsInput) settingsData {
	locks, _ := h.loadSpriteLocks()
	return settingsData{
		TrainerName:     in.TrainerName,
		TrainerCode:     in.TrainerCode,
		TrainerLevel:    in.TrainerLevel,
		Avatar:          in.Avatar,
		Pronouns:        in.Pronouns,
		City:            in.City,
		Region:          in.Region,
		Country:         in.Country,
		LocationDisplay: in.LocationDisplay,
		ProfilePublic:   in.ProfilePublic,
		ShiniesHidden:   in.ShiniesHidden,
		FavPokemon:      in.FavPokemon,
		FavPokemonForm:  in.FavPokemonForm,
		TrainerClasses:  filteredTrainerClasses(h.store.TrainerClasses(), locks, userAwardGrantRank(u)),
		PokemonList:     h.store.PokemonList(),
		TagRequest:      h.queryTagRequest(u.ID),
		BlockedUsers:    h.blockedUsers(u.ID),
	}
}

func (h *Handlers) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	// A non numeric level becomes 0, which the write path reads as "not set".
	level, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("trainer_level")))

	in, key, err := h.applySettings(u, settingsInput{
		TrainerName:     r.FormValue("trainer_name"),
		TrainerCode:     r.FormValue("trainer_code"),
		TrainerLevel:    level,
		Avatar:          r.FormValue("avatar"),
		Pronouns:        formPronouns(r),
		City:            r.FormValue("city"),
		Region:          r.FormValue("region"),
		Country:         r.FormValue("country"),
		LocationDisplay: r.FormValue("location_display"),
		ProfilePublic:   r.FormValue("profile_public") == "1",
		ShiniesHidden:   r.FormValue("shinies_hidden") == "1",
		FavPokemon:      r.FormValue("fav_pokemon"),
		FavPokemonForm:  r.FormValue("fav_pokemon_form"),
	})

	// Re-render with the coerced values either way, which is what the form has
	// always done: a blanked avatar or favourite shows the user what was actually
	// stored rather than what they submitted.
	d := h.settingsPageData(u, in)
	switch {
	case key != "":
		d.Error = h.t(r, key)
	case err != nil:
		d.Error = h.t(r, "error.server")
	default:
		d.Success = true
	}
	h.render(w, r, "settings", d)
}

// loadSettings reads a trainer's stored settings. Both the settings page and the
// mobile endpoints go through it, so neither can read a different set of columns
// than the write path writes.
func (h *Handlers) loadSettings(userID uint) settingsInput {
	var in settingsInput
	h.db.QueryRow(`
		SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(trainer_level,0), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(city,''), COALESCE(region,''), COALESCE(country,''),
		       COALESCE(location_display,'none'), COALESCE(profile_public,0), COALESCE(shinies_hidden,0),
		       COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,'')
		FROM users WHERE id = ?`, userID,
	).Scan(&in.TrainerName, &in.TrainerCode, &in.TrainerLevel, &in.Avatar, &in.Pronouns,
		&in.City, &in.Region, &in.Country, &in.LocationDisplay, &in.ProfilePublic,
		&in.ShiniesHidden, &in.FavPokemon, &in.FavPokemonForm)
	return in
}

// absoluteURL turns a site relative path into an absolute one.
//
// Every trainer class sprite is stored relative (/api/trainer-sprite/<slug> or
// /static/sprites/...), which is fine for a page that has an origin to resolve
// against. A phone has none, so a mobile response has to spell the origin out.
// baseURL comes from BASE_URL and never from the request Host, so a forged Host
// cannot point a client at someone else's server.
func absoluteURL(u string) string {
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return baseURL + u
}

// avatarURL resolves a trainer class slug to its absolute sprite URL.
func (h *Handlers) avatarURL(slug string) string {
	if slug == "" {
		return ""
	}
	for _, tc := range h.store.TrainerClasses() {
		if tc.Slug == slug {
			return absoluteURL(tc.SpriteURL)
		}
	}
	return ""
}

type mobileBlockedUser struct {
	Username    string `json:"username"`
	TrainerName string `json:"trainer_name"`
}

type mobileTagRequest struct {
	Status        string  `json:"status"`
	RejectReason  string  `json:"reject_reason"`
	Name          string  `json:"name"`
	Color         string  `json:"color"`
	NextRequestAt *string `json:"next_request_at,omitempty"`
}

type mobileSettingsResponse struct {
	TrainerName     string              `json:"trainer_name"`
	TrainerCode     string              `json:"trainer_code"`
	TrainerLevel    int                 `json:"trainer_level"`
	Avatar          string              `json:"avatar"`
	AvatarURL       string              `json:"avatar_url"`
	Pronouns        string              `json:"pronouns"`
	City            string              `json:"city"`
	Region          string              `json:"region"`
	Country         string              `json:"country"`
	LocationDisplay string              `json:"location_display"`
	ProfilePublic   bool                `json:"profile_public"`
	ShiniesHidden   bool                `json:"shinies_hidden"`
	FavPokemon      string              `json:"fav_pokemon"`
	FavPokemonForm  string              `json:"fav_pokemon_form"`
	FavSpriteURL    string              `json:"fav_sprite_url"`
	TagRequest      *mobileTagRequest   `json:"tag_request"`
	BlockedUsers    []mobileBlockedUser `json:"blocked_users"`
}

func (h *Handlers) mobileSettingsResponse(u *auth.User, in settingsInput) mobileSettingsResponse {
	out := mobileSettingsResponse{
		TrainerName:     in.TrainerName,
		TrainerCode:     in.TrainerCode,
		TrainerLevel:    in.TrainerLevel,
		Avatar:          in.Avatar,
		AvatarURL:       h.avatarURL(in.Avatar),
		Pronouns:        in.Pronouns,
		City:            in.City,
		Region:          in.Region,
		Country:         in.Country,
		LocationDisplay: in.LocationDisplay,
		ProfilePublic:   in.ProfilePublic,
		ShiniesHidden:   in.ShiniesHidden,
		FavPokemon:      in.FavPokemon,
		FavPokemonForm:  in.FavPokemonForm,
		BlockedUsers:    []mobileBlockedUser{},
	}
	if in.FavPokemon != "" {
		if id := h.store.PokemonDexID(in.FavPokemon); id != 0 {
			out.FavSpriteURL = absoluteURL(pokemonSpriteURL(id, in.FavPokemonForm))
		}
	}
	if t := h.queryTagRequest(u.ID); t != nil {
		out.TagRequest = &mobileTagRequest{
			Status: t.Status, RejectReason: t.RejectReason, Name: t.Name, Color: t.Color,
		}
		if t.NextRequestAt != nil {
			s := t.NextRequestAt.UTC().Format(time.RFC3339)
			out.TagRequest.NextRequestAt = &s
		}
	}
	for _, b := range h.blockedUsers(u.ID) {
		out.BlockedUsers = append(out.BlockedUsers, mobileBlockedUser{Username: b.Username, TrainerName: b.TrainerName})
	}
	return out
}

// MobileSettingsGet returns the trainer's own settings.
//
// Deliberately absent: the avatar catalogue and the species list. The catalogue is
// over 1700 entries and gets its own cacheable endpoint; the species list the app
// already holds from GET /api/mobile/v1/pokemon.
func (h *Handlers) MobileSettingsGet(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.mobileSettingsResponse(u, h.loadSettings(u.ID)))
}

// mobileSettingsUpdate is the PUT body. Every field is a pointer so an absent one
// is distinguishable from a zero one: a native screen edits a field at a time, and
// a form POST's "send everything or blank it" shape cannot express that.
type mobileSettingsUpdate struct {
	TrainerName     *string `json:"trainer_name"`
	TrainerCode     *string `json:"trainer_code"`
	TrainerLevel    *int    `json:"trainer_level"`
	Avatar          *string `json:"avatar"`
	Pronouns        *string `json:"pronouns"`
	City            *string `json:"city"`
	Region          *string `json:"region"`
	Country         *string `json:"country"`
	LocationDisplay *string `json:"location_display"`
	ProfilePublic   *bool   `json:"profile_public"`
	ShiniesHidden   *bool   `json:"shinies_hidden"`
	FavPokemon      *string `json:"fav_pokemon"`
	FavPokemonForm  *string `json:"fav_pokemon_form"`
}

// merge lays the present fields over the stored settings.
func (b mobileSettingsUpdate) merge(in settingsInput) settingsInput {
	if b.TrainerName != nil {
		in.TrainerName = *b.TrainerName
	}
	if b.TrainerCode != nil {
		in.TrainerCode = *b.TrainerCode
	}
	if b.TrainerLevel != nil {
		in.TrainerLevel = *b.TrainerLevel
	}
	if b.Avatar != nil {
		in.Avatar = *b.Avatar
	}
	if b.Pronouns != nil {
		in.Pronouns = *b.Pronouns
	}
	if b.City != nil {
		in.City = *b.City
	}
	if b.Region != nil {
		in.Region = *b.Region
	}
	if b.Country != nil {
		in.Country = *b.Country
	}
	if b.LocationDisplay != nil {
		in.LocationDisplay = *b.LocationDisplay
	}
	if b.ProfilePublic != nil {
		in.ProfilePublic = *b.ProfilePublic
	}
	if b.ShiniesHidden != nil {
		in.ShiniesHidden = *b.ShiniesHidden
	}
	if b.FavPokemon != nil {
		in.FavPokemon = *b.FavPokemon
	}
	if b.FavPokemonForm != nil {
		in.FavPokemonForm = *b.FavPokemonForm
	}
	return in
}

// MobileSettingsPut applies a partial settings update.
//
// It goes through applySettings, the same function the web form uses, so the
// trainer name rules and every other check apply identically here. A JSON path
// that reimplemented them could silently drop the character rule, and nothing
// would reveal it until someone put a tag in a trainer name.
func (h *Handlers) MobileSettingsPut(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	var body mobileSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}

	in, key, err := h.applySettings(u, body.merge(h.loadSettings(u.ID)))
	if key != "" {
		// The i18n key travels alongside the message so the app can attach the
		// failure to the right field instead of parsing prose.
		writeJSONErrorCode(w, h.t(r, key), key, http.StatusBadRequest)
		return
	}
	if err != nil {
		writeJSONError(w, "could not save settings", http.StatusInternalServerError)
		return
	}

	// Echo the stored result, not the request: coercions mean what was saved is
	// not always what was sent.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "settings": h.mobileSettingsResponse(u, in)})
}

type mobileAvatar struct {
	Slug      string `json:"slug"`
	Label     string `json:"label"`
	SpriteURL string `json:"sprite_url"`
	Group     string `json:"group,omitempty"`
}

// MobileSettingsAvatars serves the avatar picker's catalogue, filtered to what the
// caller's rank unlocks so a phone never sees a sprite it cannot choose.
//
// Its own endpoint because the catalogue runs to over 1700 entries: sending it
// inside every settings open would dwarf the settings themselves. It only changes
// when the sprite locks change or the caller's rank does, so it carries an ETag
// and answers 304 on a match.
func (h *Handlers) MobileSettingsAvatars(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	locks, _ := h.loadSpriteLocks()
	classes := filteredTrainerClasses(h.store.TrainerClasses(), locks, userAwardGrantRank(u))

	out := make([]mobileAvatar, 0, len(classes))
	for _, tc := range classes {
		out = append(out, mobileAvatar{
			Slug:      tc.Slug,
			Label:     tc.Label,
			SpriteURL: absoluteURL(tc.SpriteURL),
			Group:     tc.Group,
		})
	}

	body, err := json.Marshal(out)
	if err != nil {
		writeJSONError(w, "could not build the avatar list", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
