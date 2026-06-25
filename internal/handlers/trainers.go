package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

type tagEntry struct {
	Name  string
	Color string
}

type trainerEntry struct {
	Username             string
	ProfilePublic        bool
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
	SpecialRank     string
	JoinedAt        time.Time
	ShiniesHidden   bool
	Online          bool
	SuperDonator    bool
	Tags            []tagEntry
}

type trainersPageData struct {
	Trainers      []trainerEntry
	UserGrantRank int // -1 = cannot grant (logged out or community grants disabled)
}

func staffSortRank(badge string) int {
	switch badge {
	case "superadmin":
		return 0
	case "admin":
		return 1
	case "moderator":
		return 2
	case "tester":
		return 3
	}
	return 99
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

	// Load tags for all visible trainers in one query
	tagMap := map[int][]tagEntry{}
	if tagRows, err := h.db.Query(`
		SELECT ut.user_id, t.name, t.color
		FROM user_tags ut JOIN tags t ON t.id = ut.tag_id
		JOIN users u ON u.id = ut.user_id
		WHERE u.directory_hidden = 0 AND u.disabled = 0
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

	userGrantRank := -1
	if u := h.currentUser(r); u != nil {
		if u.IsMod() || h.settingBool("awards_community_grants_enabled") {
			userGrantRank = userAwardGrantRank(u)
		}
	}

	rows, err := h.db.Query(`
		SELECT id, username, COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(region,''), COALESCE(country,''), COALESCE(location_display,'none'),
		       role, COALESCE(special_rank,''), COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,''), COALESCE(fav_sprite_url,''),
		       COALESCE(raid_xp,0), created_at, COALESCE(profile_public,0), COALESCE(shinies_hidden,0),
		       CASE WHEN last_seen_at IS NOT NULL AND last_seen_at > DATE_SUB(NOW(), INTERVAL 5 MINUTE) THEN 1 ELSE 0 END
		FROM users
		WHERE directory_hidden = 0 AND disabled = 0
		ORDER BY username ASC`)
	if err != nil {
		h.render(w, r, "trainers", trainersPageData{Trainers: []trainerEntry{}, UserGrantRank: userGrantRank})
		return
	}
	defer rows.Close()

	var trainers []trainerEntry
	for rows.Next() {
		var t trainerEntry
		var userID int
		var role string
		var profilePublicInt, shiniesHiddenInt, onlineInt int
		if err := rows.Scan(&userID, &t.Username, &t.TrainerName, &t.TrainerCode, &t.Avatar, &t.Pronouns, &t.Region, &t.Country, &t.LocationDisplay, &role, &t.SpecialRank, &t.FavPokemon, &t.FavPokemonForm, &t.FavSpriteURL, &t.RaidXP, &t.JoinedAt, &profilePublicInt, &shiniesHiddenInt, &onlineInt); err != nil {
			continue
		}
		t.ProfilePublic = profilePublicInt > 0
		t.ShiniesHidden = shiniesHiddenInt > 0
		if len(t.TrainerCode) == 12 {
			t.TrainerCodeFormatted = t.TrainerCode[:4] + " " + t.TrainerCode[4:8] + " " + t.TrainerCode[8:]
		} else {
			t.TrainerCodeFormatted = t.TrainerCode
		}
		t.AvatarURL = avatarURLBySlug[t.Avatar]
		t.StaffBadge = staffBadge(t.Username, role)
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

	sort.SliceStable(trainers, func(i, j int) bool {
		a, b := trainers[i], trainers[j]
		if a.Online != b.Online {
			return a.Online
		}
		aRank, bRank := staffSortRank(a.StaffBadge), staffSortRank(b.StaffBadge)
		aIsStaff, bIsStaff := aRank < 99, bRank < 99
		if aIsStaff != bIsStaff {
			return aIsStaff
		}
		if aIsStaff {
			return aRank < bRank
		}
		if a.SuperDonator != b.SuperDonator {
			return a.SuperDonator
		}
		if a.RaidXP != b.RaidXP {
			return a.RaidXP > b.RaidXP
		}
		nameA := a.TrainerName
		if nameA == "" {
			nameA = a.Username
		}
		nameB := b.TrainerName
		if nameB == "" {
			nameB = b.Username
		}
		return nameA < nameB
	})

	h.render(w, r, "trainers", trainersPageData{Trainers: trainers, UserGrantRank: userGrantRank})
}

type trainerProfileData struct {
	Trainer         trainerEntry
	ViewerUsername  string
	IsOwnProfile    bool
	IsFollowing     bool
	FollowsMe       bool
	IsBlocked       bool
	TheyBlockedYou  bool
	Feedback        []trainerFeedbackEntry
	MyFeedbackID    uint
	MyFeedbackOpt   uint
	FeedbackOptions []feedbackOptionRow
	RecentFriends   []friendEntry
	FollowerCount   int
	FollowingCount  int
}

func (h *Handlers) TrainerProfilePage(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")

	trainerClasses := h.store.TrainerClasses()
	avatarURLBySlug := make(map[string]string, len(trainerClasses))
	for _, tc := range trainerClasses {
		avatarURLBySlug[tc.Slug] = tc.SpriteURL
	}

	var t trainerEntry
	var userID uint
	var role string
	var profilePublicInt, shiniesHiddenInt, onlineInt int

	err := h.db.QueryRow(`
		SELECT id, username, COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(region,''), COALESCE(country,''), COALESCE(location_display,'none'),
		       role, COALESCE(special_rank,''), COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,''),
		       COALESCE(fav_sprite_url,''), COALESCE(raid_xp,0), created_at,
		       COALESCE(profile_public,0), COALESCE(shinies_hidden,0),
		       CASE WHEN last_seen_at IS NOT NULL AND last_seen_at > DATE_SUB(NOW(), INTERVAL 5 MINUTE) THEN 1 ELSE 0 END
		FROM users
		WHERE username = ? AND directory_hidden = 0 AND disabled = 0`, username).
		Scan(&userID, &t.Username, &t.TrainerName, &t.TrainerCode, &t.Avatar, &t.Pronouns,
			&t.Region, &t.Country, &t.LocationDisplay, &role, &t.SpecialRank,
			&t.FavPokemon, &t.FavPokemonForm, &t.FavSpriteURL, &t.RaidXP, &t.JoinedAt,
			&profilePublicInt, &shiniesHiddenInt, &onlineInt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	t.ProfilePublic = profilePublicInt > 0
	t.ShiniesHidden = shiniesHiddenInt > 0
	t.Online = onlineInt > 0
	if len(t.TrainerCode) == 12 {
		t.TrainerCodeFormatted = t.TrainerCode[:4] + " " + t.TrainerCode[4:8] + " " + t.TrainerCode[8:]
	} else {
		t.TrainerCodeFormatted = t.TrainerCode
	}
	t.AvatarURL = avatarURLBySlug[t.Avatar]
	t.StaffBadge = staffBadge(t.Username, role)
	t.RaidRank = raidRankLabel(t.RaidXP, role)
	t.RaidRankClass = raidRankClass(t.RaidXP, role)
	t.SuperDonator = h.hasActivePurchase(userID, "super_donator")

	var tags []tagEntry
	if tagRows, err := h.db.Query(`
		SELECT t.name, t.color FROM user_tags ut
		JOIN tags t ON t.id = ut.tag_id
		WHERE ut.user_id = ? ORDER BY t.name`, userID); err == nil {
		defer tagRows.Close()
		for tagRows.Next() {
			var te tagEntry
			if tagRows.Scan(&te.Name, &te.Color) == nil {
				tags = append(tags, te)
			}
		}
	}
	if tags == nil {
		tags = []tagEntry{}
	}
	t.Tags = tags

	pd := trainerProfileData{Trainer: t}

	viewer := h.currentUser(r)
	if viewer != nil {
		pd.ViewerUsername = viewer.Username
		pd.IsOwnProfile = viewer.ID == userID

		if pd.IsOwnProfile {
			if fRows, err := h.db.Query(`
				SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
				FROM user_follows uf JOIN users u ON u.id = uf.friend_id
				WHERE uf.user_id = ? ORDER BY uf.created_at DESC LIMIT 5`, viewer.ID); err == nil {
				defer fRows.Close()
				for fRows.Next() {
					var fe friendEntry
					if fRows.Scan(&fe.Username, &fe.TrainerName, &fe.Avatar) == nil {
						fe.AvatarURL = avatarURLBySlug[fe.Avatar]
						pd.RecentFriends = append(pd.RecentFriends, fe)
					}
				}
			}
		}

		if !pd.IsOwnProfile {
			var count int
			h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, viewer.ID, userID).Scan(&count)
			pd.IsFollowing = count > 0

			count = 0
			h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, userID, viewer.ID).Scan(&count)
			pd.FollowsMe = count > 0

			count = 0
			h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, viewer.ID, userID).Scan(&count)
			pd.IsBlocked = count > 0

			count = 0
			h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, userID, viewer.ID).Scan(&count)
			pd.TheyBlockedYou = count > 0

			var fbID, optID uint
			h.db.QueryRow(`SELECT id, option_id FROM user_feedback WHERE author_id = ? AND target_id = ?`,
				viewer.ID, userID).Scan(&fbID, &optID)
			pd.MyFeedbackID = fbID
			pd.MyFeedbackOpt = optID
		}
	}

	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE friend_id = ?`, userID).Scan(&pd.FollowerCount)
	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ?`, userID).Scan(&pd.FollowingCount)

	var feedback []trainerFeedbackEntry
	fbRows, err := h.db.Query(`
		SELECT uf.id, u.username, COALESCE(u.trainer_name,''), fo.label, fo.sentiment, uf.updated_at
		FROM user_feedback uf
		JOIN users u ON u.id = uf.author_id
		JOIN feedback_options fo ON fo.id = uf.option_id
		WHERE uf.target_id = ?
		ORDER BY uf.updated_at DESC`, userID)
	if err == nil {
		defer fbRows.Close()
		for fbRows.Next() {
			var fb trainerFeedbackEntry
			if fbRows.Scan(&fb.ID, &fb.AuthorUsername, &fb.AuthorName, &fb.Label, &fb.Sentiment, &fb.UpdatedAt) == nil {
				feedback = append(feedback, fb)
			}
		}
	}
	if feedback == nil {
		feedback = []trainerFeedbackEntry{}
	}
	pd.Feedback = feedback

	var options []feedbackOptionRow
	optRows, err := h.db.Query(`
		SELECT id, label, sentiment, sort_order, enabled
		FROM feedback_options WHERE enabled = 1
		ORDER BY sort_order, id`)
	if err == nil {
		defer optRows.Close()
		for optRows.Next() {
			var o feedbackOptionRow
			var enabledInt int
			if optRows.Scan(&o.ID, &o.Label, &o.Sentiment, &o.SortOrder, &enabledInt) == nil {
				o.Enabled = enabledInt > 0
				options = append(options, o)
			}
		}
	}
	if options == nil {
		options = []feedbackOptionRow{}
	}
	pd.FeedbackOptions = options

	h.render(w, r, "trainer", pd)
}
