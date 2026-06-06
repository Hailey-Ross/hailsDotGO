package handlers

import (
	"net/http"
	"strconv"
	"time"
)

type tagEntry struct {
	Name  string
	Color string
}

type trainerEntry struct {
	TrainerName          string
	TrainerCode          string
	TrainerCodeFormatted string
	Avatar          string
	AvatarURL       string
	Pronouns        string
	Region          string
	Country         string
	LocationDisplay string
	StaffBadge      string
	FavPokemon      string
	FavPokemonForm  string
	FavSpriteURL    string
	RaidXP          int
	RaidRank        string
	RaidRankClass   string
	JoinedAt        time.Time
	Online          bool
	SuperDonator    bool
	Tags            []tagEntry
}

func pokemonSpriteURL(id int, form string) string {
	if id == 0 {
		return ""
	}
	const base = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/"
	if form == "shiny" {
		return base + "shiny/" + strconv.Itoa(id) + ".png"
	}
	if form == "primal" {
		switch id {
		case 383:
			return base + "10007.png" // Primal Groudon
		case 382:
			return base + "10008.png" // Primal Kyogre
		}
	}
	return base + strconv.Itoa(id) + ".png"
}

func (h *Handlers) TrainersPage(w http.ResponseWriter, r *http.Request) {
	trainerClasses := h.store.TrainerClasses()
	avatarURLBySlug := make(map[string]string, len(trainerClasses))
	for _, tc := range trainerClasses {
		avatarURLBySlug[tc.Slug] = tc.SpriteURL
	}

	superDonators := h.superDonatorSet()

	// Load tags for all public trainers in one query
	tagMap := map[int][]tagEntry{}
	if tagRows, err := h.db.Query(`
		SELECT ut.user_id, t.name, t.color
		FROM user_tags ut JOIN tags t ON t.id = ut.tag_id
		JOIN users u ON u.id = ut.user_id
		WHERE u.profile_public = 1 AND u.trainer_code != '' AND u.directory_hidden = 0
		ORDER BY ut.user_id, t.name`); err == nil {
		defer tagRows.Close()
		for tagRows.Next() {
			var uid int
			var te tagEntry
			if tagRows.Scan(&uid, &te.Name, &te.Color) == nil {
				tagMap[uid] = append(tagMap[uid], te)
			}
		}
	}

	rows, err := h.db.Query(`
		SELECT id, COALESCE(trainer_name,''), trainer_code, COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(region,''), COALESCE(country,''), COALESCE(location_display,'none'),
		       role, username, COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,''), COALESCE(fav_sprite_url,''),
		       COALESCE(raid_xp,0), created_at,
		       CASE WHEN last_seen_at IS NOT NULL AND last_seen_at > DATE_SUB(NOW(), INTERVAL 5 MINUTE) THEN 1 ELSE 0 END
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
		var userID int
		var role, username string
		var onlineInt int
		if err := rows.Scan(&userID, &t.TrainerName, &t.TrainerCode, &t.Avatar, &t.Pronouns, &t.Region, &t.Country, &t.LocationDisplay, &role, &username, &t.FavPokemon, &t.FavPokemonForm, &t.FavSpriteURL, &t.RaidXP, &t.JoinedAt, &onlineInt); err != nil {
			continue
		}
		if len(t.TrainerCode) == 12 {
			t.TrainerCodeFormatted = t.TrainerCode[:4] + " " + t.TrainerCode[4:8] + " " + t.TrainerCode[8:]
		} else {
			t.TrainerCodeFormatted = t.TrainerCode
		}
		t.AvatarURL = avatarURLBySlug[t.Avatar]
		t.StaffBadge = staffBadge(username, role)
		t.RaidRank = raidRankLabel(t.RaidXP, role)
		t.RaidRankClass = raidRankClass(t.RaidXP, role)
		t.Online = onlineInt > 0
		t.SuperDonator = superDonators[uint(userID)]
		t.Tags = tagMap[userID]
		if t.Tags == nil {
			t.Tags = []tagEntry{}
		}
		if t.FavPokemon != "" && t.FavSpriteURL == "" {
			if id := h.store.PokemonDexID(t.FavPokemon); id != 0 {
				t.FavSpriteURL = pokemonSpriteURL(id, t.FavPokemonForm)
				uid, url := userID, t.FavSpriteURL
				go h.db.Exec(`UPDATE users SET fav_sprite_url = ? WHERE id = ?`, url, uid)
			}
		}
		trainers = append(trainers, t)
	}
	if trainers == nil {
		trainers = []trainerEntry{}
	}
	h.render(w, r, "trainers", trainers)
}
