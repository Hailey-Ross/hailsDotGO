package handlers

import (
	"net/http"
	"regexp"
	"strings"

	"pogo.hails.cc/internal/pogodata"
)

var trainerCodeRe = regexp.MustCompile(`^\d{12}$`)

var predefinedPronouns = map[string]bool{
	"":         true,
	"he/him":   true,
	"she/her":  true,
	"they/them": true,
	"any/all":  true,
}

type settingsData struct {
	Success         bool
	Error           string
	TrainerName     string
	TrainerCode     string
	Avatar          string
	Pronouns        string
	City            string
	Region          string
	Country         string
	LocationDisplay string
	ProfilePublic   bool
	TrainerClasses  []pogodata.TrainerClass
}

func (h *Handlers) SettingsPage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	var d settingsData
	h.db.QueryRow(`
		SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(city,''), COALESCE(region,''), COALESCE(country,''),
		       COALESCE(location_display,'none'), COALESCE(profile_public,0)
		FROM users WHERE id = ?`, u.ID,
	).Scan(&d.TrainerName, &d.TrainerCode, &d.Avatar, &d.Pronouns, &d.City, &d.Region, &d.Country, &d.LocationDisplay, &d.ProfilePublic)
	d.TrainerClasses = h.store.TrainerClasses()
	h.render(w, r, "settings", d)
}

func (h *Handlers) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	trainerName     := strings.TrimSpace(r.FormValue("trainer_name"))
	trainerCode     := strings.ReplaceAll(strings.TrimSpace(r.FormValue("trainer_code")), " ", "")
	avatar          := r.FormValue("avatar")
	city            := strings.TrimSpace(r.FormValue("city"))
	region          := strings.TrimSpace(r.FormValue("region"))
	country         := strings.TrimSpace(r.FormValue("country"))
	locationDisplay := r.FormValue("location_display")
	profilePublic   := r.FormValue("profile_public") == "1"

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
	validAvatars := map[string]bool{"": true}
	for _, tc := range h.store.TrainerClasses() {
		validAvatars[tc.Slug] = true
	}
	if !validAvatars[avatar] {
		avatar = ""
	}

	fail := func(msg string) {
		h.render(w, r, "settings", settingsData{
			Error: msg, TrainerName: trainerName, TrainerCode: trainerCode, Avatar: avatar,
			Pronouns: pronouns, City: city, Region: region, Country: country,
			LocationDisplay: locationDisplay, ProfilePublic: profilePublic,
			TrainerClasses: h.store.TrainerClasses(),
		})
	}

	if len(trainerName) > 16 {
		fail("Trainer name must be 16 characters or fewer.")
		return
	}
	if trainerCode != "" && !trainerCodeRe.MatchString(trainerCode) {
		fail("Trainer code must be exactly 12 digits (spaces are fine).")
		return
	}
	if len(city) > 100 || len(region) > 100 || len(country) > 100 {
		fail("Location fields are too long.")
		return
	}

	if trainerName != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE LOWER(trainer_name) = LOWER(?) AND id != ?`, trainerName, u.ID).Scan(&count)
		if count > 0 {
			fail("That trainer name is already registered to another account.")
			return
		}
	}
	if trainerCode != "" {
		var count int
		h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE trainer_code = ? AND id != ?`, trainerCode, u.ID).Scan(&count)
		if count > 0 {
			fail("That trainer code is already registered to another account.")
			return
		}
	}

	publicInt := 0
	if profilePublic {
		publicInt = 1
	}
	_, err := h.db.Exec(
		`UPDATE users SET trainer_name=?, trainer_code=?, avatar=?, pronouns=?, city=?, region=?, country=?, location_display=?, profile_public=? WHERE id=?`,
		trainerName, trainerCode, avatar, pronouns, city, region, country, locationDisplay, publicInt, u.ID,
	)
	if err != nil {
		fail("Something went wrong. Please try again.")
		return
	}
	h.render(w, r, "settings", settingsData{
		Success: true, TrainerName: trainerName, TrainerCode: trainerCode, Avatar: avatar,
		Pronouns: pronouns, City: city, Region: region, Country: country,
		LocationDisplay: locationDisplay, ProfilePublic: profilePublic,
		TrainerClasses: h.store.TrainerClasses(),
	})
}
