package handlers

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

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
	"":         true,
	"he/him":   true,
	"she/her":  true,
	"they/them": true,
	"any/all":  true,
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
	bRows, err := h.db.Query(`
		SELECT u.username, COALESCE(u.trainer_name,'')
		FROM user_blocks ub
		JOIN users u ON u.id = ub.blocked_id
		WHERE ub.user_id = ?
		ORDER BY ub.created_at DESC`, u.ID)
	if err == nil {
		defer bRows.Close()
		for bRows.Next() {
			var be blockedUserEntry
			if bRows.Scan(&be.Username, &be.TrainerName) == nil {
				d.BlockedUsers = append(d.BlockedUsers, be)
			}
		}
	}
	if d.BlockedUsers == nil {
		d.BlockedUsers = []blockedUserEntry{}
	}
	h.render(w, r, "settings", d)
}

func (h *Handlers) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	trainerName     := strings.TrimSpace(r.FormValue("trainer_name"))
	trainerCode     := strings.ReplaceAll(strings.TrimSpace(r.FormValue("trainer_code")), " ", "")
	trainerLevelRaw := strings.TrimSpace(r.FormValue("trainer_level"))
	trainerLevel, _ := strconv.Atoi(trainerLevelRaw)
	avatar          := r.FormValue("avatar")
	city            := strings.TrimSpace(r.FormValue("city"))
	region          := strings.TrimSpace(r.FormValue("region"))
	country         := strings.TrimSpace(r.FormValue("country"))
	locationDisplay := r.FormValue("location_display")
	profilePublic   := r.FormValue("profile_public") == "1"
	shiniesHidden   := r.FormValue("shinies_hidden") == "1"
	favPokemon      := strings.TrimSpace(r.FormValue("fav_pokemon"))
	favPokemonForm  := r.FormValue("fav_pokemon_form")

	pronounsChoice := r.FormValue("pronouns_choice")
	var pronouns string
	if pronounsChoice == "custom" {
		pronouns = strings.TrimSpace(r.FormValue("pronouns_custom"))
		if len(pronouns) > 32 {
			pronouns = pronouns[:32]
		}
	} else if predefinedPronouns[pronounsChoice] {
		pronouns = pronounsChoice
	}

	validDisplay := map[string]bool{"none": true, "country": true, "full": true}
	if !validDisplay[locationDisplay] {
		locationDisplay = "none"
	}
	avatarLocks, _ := h.loadSpriteLocks()
	userRank := userAwardGrantRank(u)
	validAvatars := map[string]bool{"": true}
	for _, tc := range h.store.TrainerClasses() {
		if userRank >= spriteMinRank(tc, avatarLocks) {
			validAvatars[tc.Slug] = true
		}
	}
	if !validAvatars[avatar] {
		avatar = ""
	}
	validForms := map[string]bool{"": true, "shadow": true, "shiny": true, "primal": true}
	if !validForms[favPokemonForm] {
		favPokemonForm = ""
	}
	if favPokemon != "" && h.store.PokemonDexID(favPokemon) == 0 {
		favPokemon = ""
		favPokemonForm = ""
	}
	if len(favPokemon) > 64 {
		favPokemon = favPokemon[:64]
	}

	fail := func(msg string) {
		h.render(w, r, "settings", settingsData{
			Error: msg, TrainerName: trainerName, TrainerCode: trainerCode, TrainerLevel: trainerLevel,
			Avatar: avatar, Pronouns: pronouns, City: city, Region: region, Country: country,
			LocationDisplay: locationDisplay, ProfilePublic: profilePublic, ShiniesHidden: shiniesHidden,
			FavPokemon: favPokemon, FavPokemonForm: favPokemonForm,
			TrainerClasses: filteredTrainerClasses(h.store.TrainerClasses(), avatarLocks, userRank),
			PokemonList: h.store.PokemonList(),
			TagRequest: h.queryTagRequest(u.ID),
		})
	}

	if len(trainerName) > 16 {
		fail(h.t(r, "error.trainer_name_length"))
		return
	}
	if !validTrainerName(trainerName) {
		fail(h.t(r, "error.trainer_name_chars"))
		return
	}
	if trainerCode != "" && !trainerCodeRe.MatchString(trainerCode) {
		fail(h.t(r, "error.trainer_code_format"))
		return
	}
	if trainerLevel != 0 && (trainerLevel < 1 || trainerLevel > 80) {
		fail(h.t(r, "error.trainer_level_range"))
		return
	}
	if len(city) > 100 || len(region) > 100 || len(country) > 100 {
		fail(h.t(r, "error.location_too_long"))
		return
	}

	if trainerName != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE LOWER(trainer_name) = LOWER(?) AND id != ?`, trainerName, u.ID).Scan(&count)
		if count > 0 {
			fail(h.t(r, "error.trainer_name_taken"))
			return
		}
	}
	if trainerCode != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE trainer_code = ? AND id != ?`, trainerCode, u.ID).Scan(&count)
		if count > 0 {
			fail(h.t(r, "error.trainer_code_taken"))
			return
		}
	}

	publicInt := 0
	if profilePublic {
		publicInt = 1
	}
	shiniesHiddenInt := 0
	if shiniesHidden {
		shiniesHiddenInt = 1
	}
	favSpriteURL := ""
	if favPokemon != "" {
		if id := h.store.PokemonDexID(favPokemon); id != 0 {
			favSpriteURL = pokemonSpriteURL(id, favPokemonForm)
		}
	}
	_, err := h.db.Exec(
		`UPDATE users SET trainer_name=?, trainer_code=?, trainer_level=?, avatar=?, pronouns=?, city=?, region=?, country=?, location_display=?, profile_public=?, shinies_hidden=?, fav_pokemon=?, fav_pokemon_form=?, fav_sprite_url=? WHERE id=?`,
		trainerName, trainerCode, trainerLevel, avatar, pronouns, city, region, country, locationDisplay, publicInt, shiniesHiddenInt, favPokemon, favPokemonForm, favSpriteURL, u.ID,
	)
	if err != nil {
		fail(h.t(r, "error.server"))
		return
	}
	h.render(w, r, "settings", settingsData{
		Success: true, TrainerName: trainerName, TrainerCode: trainerCode, TrainerLevel: trainerLevel,
		Avatar: avatar, Pronouns: pronouns, City: city, Region: region, Country: country,
		LocationDisplay: locationDisplay, ProfilePublic: profilePublic, ShiniesHidden: shiniesHidden,
		FavPokemon: favPokemon, FavPokemonForm: favPokemonForm,
		TrainerClasses: filteredTrainerClasses(h.store.TrainerClasses(), avatarLocks, userRank),
		PokemonList:    h.store.PokemonList(),
		TagRequest:     h.queryTagRequest(u.ID),
	})
}
