package handlers

import "net/http"

type trainerEntry struct {
	TrainerName     string
	TrainerCode     string
	Avatar          string
	AvatarURL       string
	Pronouns        string
	Region          string
	Country         string
	LocationDisplay string
	StaffBadge      string
}

func (h *Handlers) TrainersPage(w http.ResponseWriter, r *http.Request) {
	trainerClasses := h.store.TrainerClasses()
	avatarURLBySlug := make(map[string]string, len(trainerClasses))
	for _, tc := range trainerClasses {
		avatarURLBySlug[tc.Slug] = tc.SpriteURL
	}

	rows, err := h.db.Query(`
		SELECT COALESCE(trainer_name,''), trainer_code, COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(region,''), COALESCE(country,''), COALESCE(location_display,'none'),
		       role, username
		FROM users
		WHERE profile_public = 1 AND trainer_code != '' AND directory_hidden = 0
		ORDER BY trainer_name ASC, username ASC`)
	if err != nil {
		h.render(w, r, "trainers", []trainerEntry{})
		return
	}
	defer rows.Close()

	var trainers []trainerEntry
	for rows.Next() {
		var t trainerEntry
		var role, username string
		if err := rows.Scan(&t.TrainerName, &t.TrainerCode, &t.Avatar, &t.Pronouns, &t.Region, &t.Country, &t.LocationDisplay, &role, &username); err != nil {
			continue
		}
		t.AvatarURL = avatarURLBySlug[t.Avatar]
		t.StaffBadge = staffBadge(username, role)
		trainers = append(trainers, t)
	}
	if trainers == nil {
		trainers = []trainerEntry{}
	}
	h.render(w, r, "trainers", trainers)
}
