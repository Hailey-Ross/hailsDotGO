package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// avatarURLMap maps a trainer class slug to its sprite URL. The catalogue is built
// once at boot and never mutated, so rebuilding this per request is a cheap read
// rather than a query. It was open coded in three places before.
func (h *Handlers) avatarURLMap() map[string]string {
	classes := h.store.TrainerClasses()
	m := make(map[string]string, len(classes))
	for _, tc := range classes {
		m[tc.Slug] = tc.SpriteURL
	}
	return m
}

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
	Avatar               string
	AvatarURL            string
	Pronouns             string
	Region               string
	Country              string
	LocationDisplay      string
	StaffBadge           string
	FavPokemon           string
	FavPokemonForm       string
	FavSpriteURL         string
	RaidXP               int
	RaidRank             string
	RaidRankClass        string
	SpecialRank          string
	JoinedAt             time.Time
	ShiniesHidden        bool
	Online               bool
	SuperDonator         bool
	Tags                 []tagEntry
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
	if form == "primal" {
		switch id {
		case 383:
			return spriteURLSlug("10007", "") // Primal Groudon
		case 382:
			return spriteURLSlug("10008", "") // Primal Kyogre
		}
	}
	return spriteURLSlug(strconv.Itoa(id), form)
}

// spriteURLSlug builds a PokeAPI sprite URL from a slug rather than an id. Nearly every slug
// is just a number, but the Unown letters are pokemon-form records with no id of their own and
// so are filed under 201-b, 201-exclamation and friends (see unownSpriteSlug).
func spriteURLSlug(slug, form string) string {
	if slug == "" {
		return ""
	}
	const base = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/"
	if form == "shiny" {
		return base + "shiny/" + slug + ".png"
	}
	return base + slug + ".png"
}

// listTrainers builds the directory: every account that is not hidden or disabled,
// with tags, badges, ranks and sprites resolved, in display order.
//
// Extracted from TrainersPage so the mobile endpoint runs the same query rather
// than a second copy of it that could drift on the visibility clause.
func (h *Handlers) listTrainers() []trainerEntry {
	avatarURLBySlug := h.avatarURLMap()

	superDonators := h.superDonatorSet()

	// Load tags for all visible trainers in one query
	tagMap := map[int][]tagEntry{}
	if tagRows, err := h.db.Query(`
		SELECT ut.user_id, t.name, t.color
		FROM user_tags ut JOIN tags t ON t.id = ut.tag_id
		JOIN users u ON u.id = ut.user_id
		WHERE u.directory_hidden = 0 AND u.disabled = 0 AND u.deleted_at IS NULL
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
		SELECT id, username, COALESCE(trainer_name,''), COALESCE(trainer_code,''), COALESCE(avatar,''),
		       COALESCE(pronouns,''), COALESCE(region,''), COALESCE(country,''), COALESCE(location_display,'none'),
		       role, COALESCE(special_rank,''), COALESCE(fav_pokemon,''), COALESCE(fav_pokemon_form,''), COALESCE(fav_sprite_url,''),
		       COALESCE(raid_xp,0), created_at, COALESCE(profile_public,0), COALESCE(shinies_hidden,0),
		       CASE WHEN last_seen_at IS NOT NULL AND last_seen_at > DATE_SUB(NOW(), INTERVAL 5 MINUTE) THEN 1 ELSE 0 END
		FROM users
		WHERE directory_hidden = 0 AND disabled = 0 AND deleted_at IS NULL
		ORDER BY username ASC`)
	if err != nil {
		return []trainerEntry{}
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

	return trainers
}

func (h *Handlers) TrainersPage(w http.ResponseWriter, r *http.Request) {
	userGrantRank := -1
	if u := h.currentUser(r); u != nil {
		if u.IsMod() || h.settingBool("awards_community_grants_enabled") {
			userGrantRank = userAwardGrantRank(u)
		}
	}
	h.render(w, r, "trainers", trainersPageData{Trainers: h.listTrainers(), UserGrantRank: userGrantRank})
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

// lookupTrainer loads one trainer for a profile view.
//
// It carries the visibility gate the profile page has always applied: a hidden or
// disabled account is not found at all. Extracted so the mobile endpoint cannot
// end up with a second copy of that WHERE clause that forgets half of it.
func (h *Handlers) lookupTrainer(username string) (trainerEntry, uint, bool) {

	avatarURLBySlug := h.avatarURLMap()

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
		WHERE username = ? AND directory_hidden = 0 AND disabled = 0 AND deleted_at IS NULL`, username).
		Scan(&userID, &t.Username, &t.TrainerName, &t.TrainerCode, &t.Avatar, &t.Pronouns,
			&t.Region, &t.Country, &t.LocationDisplay, &role, &t.SpecialRank,
			&t.FavPokemon, &t.FavPokemonForm, &t.FavSpriteURL, &t.RaidXP, &t.JoinedAt,
			&profilePublicInt, &shiniesHiddenInt, &onlineInt)
	if err != nil {
		return trainerEntry{}, 0, false
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
	return t, userID, true
}

func (h *Handlers) TrainerProfilePage(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	avatarURLBySlug := h.avatarURLMap()
	t, userID, found := h.lookupTrainer(username)
	if !found {
		http.NotFound(w, r)
		return
	}

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

	pd.FeedbackOptions = h.enabledFeedbackOptions()

	h.render(w, r, "trainer", pd)
}

type mobileTag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// mobileTrainer is one trainer as a client sees them, with the privacy gates
// already applied.
//
// This is a separate type rather than JSON tags on trainerEntry, deliberately.
// Every gate on a private profile lives in the templates today: the SQL returns
// the trainer name, friend code and pronouns of every account that is not hidden,
// and trainers.html and trainer.html decide what to draw. Marshalling trainerEntry
// straight out would hand a client exactly the fields the website hides, and the
// comment at templates/trainer.html records that this already happened once, with
// friend codes handed to any logged in visitor. Building a DTO makes the gate
// something you have to pass through rather than something you have to remember.
type mobileTrainer struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	TrainerName  string `json:"trainer_name,omitempty"`
	TrainerCode  string `json:"trainer_code,omitempty"`
	Avatar       string `json:"avatar,omitempty"`
	AvatarURL    string `json:"avatar_url,omitempty"`
	Pronouns     string `json:"pronouns,omitempty"`
	Region       string `json:"region,omitempty"`
	Country      string `json:"country,omitempty"`
	StaffBadge   string `json:"staff_badge,omitempty"`
	SpecialRank  string `json:"special_rank,omitempty"`
	FavPokemon   string `json:"fav_pokemon,omitempty"`
	FavSpriteURL string `json:"fav_sprite_url,omitempty"`
	RaidXP       int    `json:"raid_xp"`
	RaidRank     string `json:"raid_rank,omitempty"`
	// RaidRankClass is the rank's colour class ("pkmn-prof", "youngster" and so
	// on). It travels beside the label because the label is translated: without
	// this a client would have to reverse a colour out of localised text, and
	// would get it wrong in every language but English.
	RaidRankClass string      `json:"raid_rank_class,omitempty"`
	JoinedAt      string      `json:"joined_at"`
	Online        bool        `json:"online"`
	SuperDonator  bool        `json:"super_donator"`
	ProfilePublic bool        `json:"profile_public"`
	ShiniesHidden bool        `json:"shinies_hidden"`
	Tags          []mobileTag `json:"tags"`
}

// toMobileTrainer applies the same visibility rules the templates apply, so a JSON
// client sees what the website shows and nothing more.
//
// isSelf lets a trainer see their own friend code on their own profile, which is
// what trainer.html does. Every mobile route that reaches this is behind
// MobileAuthMiddleware, so the "logged in visitor" half of the template condition
// is always satisfied.
func toMobileTrainer(t trainerEntry, isSelf bool) mobileTrainer {
	out := mobileTrainer{
		Username:      t.Username,
		DisplayName:   t.Username,
		Avatar:        t.Avatar,
		AvatarURL:     absoluteURL(t.AvatarURL),
		StaffBadge:    t.StaffBadge,
		SpecialRank:   t.SpecialRank,
		FavPokemon:    t.FavPokemon,
		FavSpriteURL:  absoluteURL(t.FavSpriteURL),
		RaidXP:        t.RaidXP,
		RaidRank:      t.RaidRank,
		RaidRankClass: t.RaidRankClass,
		JoinedAt:      t.JoinedAt.UTC().Format(time.RFC3339),
		Online:        t.Online,
		SuperDonator:  t.SuperDonator,
		ProfilePublic: t.ProfilePublic,
		ShiniesHidden: t.ShiniesHidden,
		Tags:          []mobileTag{},
	}
	for _, tag := range t.Tags {
		out.Tags = append(out.Tags, mobileTag{Name: tag.Name, Color: tag.Color})
	}

	// A private profile shows its username and nothing personal. The owner still
	// sees their own details.
	if !t.ProfilePublic && !isSelf {
		return out
	}

	if t.TrainerName != "" {
		out.TrainerName = t.TrainerName
		out.DisplayName = t.TrainerName
	}
	out.Pronouns = t.Pronouns
	out.TrainerCode = t.TrainerCodeFormatted

	// location_display is the trainer's own choice about how precise to be, and it
	// has to be honoured here rather than at render time: "none" must not put a
	// country on the wire at all.
	switch t.LocationDisplay {
	case "full":
		out.Region = t.Region
		out.Country = t.Country
	case "country":
		out.Country = t.Country
	}
	return out
}

// mobileTrainersResponse is the directory as a client sees it.
//
// An envelope rather than the bare array this used to return, because two of the
// three fields describe the VIEWER rather than a trainer: whether they may grant
// an award, and how much of the directory they are holding. A bare array had
// nowhere to put either, so the app could not draw the grant control at all.
//
// Reshaping it cost nothing. Nothing consumed this endpoint yet: a grep across
// the mobile repo's api/ and data/ on 2026-08-31 found only the maintenance flag
// being read, and no service or repository calling this path. Do not read the
// change as a precedent; the next one will have clients.
type mobileTrainersResponse struct {
	Trainers      []mobileTrainer `json:"trainers"`
	UserGrantRank int             `json:"user_grant_rank"`
	// Total is the number of trainers MATCHING the query, before limit and
	// offset are applied. That is what a client paging through results needs: it
	// answers "is there another page", which the unfiltered count does not.
	Total int `json:"total"`
}

// mobileTrainersMaxLimit caps a page. The directory is a few hundred rows today
// and the whole list is a legitimate request, so the default is everything; this
// is a ceiling on what one call can ask for, not a page size.
const mobileTrainersMaxLimit = 500

// MobileTrainers serves the trainers directory.
//
// Ordering is whatever listTrainers produced (online first, then staff by rank,
// then supporters, then raid XP, then name) and is never re-sorted here. The
// filter below preserves it.
func (h *Handlers) MobileTrainers(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	// Both flags, not just the page one. TrainersEnabled switches off the whole
	// /trainers page; section_trainer_directory_enabled switches off the directory
	// inside it while leaving the raid finder standing (templates/trainers.html:20).
	// This endpoint IS the directory, so either being off means it has nothing to
	// serve. Serving a list anyway would make the toggle look like it had worked
	// when it had not, which is the failure the box entry in pageEnabled warns about.
	m := h.maintenanceSettings()
	if !m.TrainersEnabled || !m.TrainerDirectoryEnabled {
		writeJSONError(w, h.t(r, "error.maintenance"), http.StatusServiceUnavailable)
		return
	}

	list := h.listTrainers()
	out := make([]mobileTrainer, 0, len(list))
	for _, t := range list {
		out = append(out, toMobileTrainer(t, t.Username == u.Username))
	}

	// Same condition as TrainersPage, read from the same two places. -1 means the
	// viewer cannot grant at all and the control should not be drawn.
	grantRank := -1
	if u.IsMod() || h.settingBool("awards_community_grants_enabled") {
		grantRank = userAwardGrantRank(u)
	}

	out = filterMobileTrainers(out, r.URL.Query().Get("q"))
	total := len(out)

	// Defaults are "everything from the start", so a client that sends neither
	// parameter gets the whole directory, which is what the website renders.
	offset := clampQueryInt(r, "offset", 0, 0, total)
	out = out[offset:]
	if limit := clampQueryInt(r, "limit", len(out), 0, mobileTrainersMaxLimit); limit < len(out) {
		out = out[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mobileTrainersResponse{
		Trainers:      out,
		UserGrantRank: grantRank,
		Total:         total,
	})
}

// filterMobileTrainers narrows the directory to trainers matching q, preserving
// the order it was given. An empty query returns the input untouched.
//
// It runs over the DTO, AFTER toMobileTrainer, and that is the whole privacy
// story here: a private profile's mobileTrainer carries no TrainerName, so its
// trainer name cannot be matched. Filtering trainerEntry instead would turn this
// parameter into an oracle for exactly the field the privacy gate exists to hide,
// because a hit confirms a guess: type "Secretive", get a result, and you have
// just read a name the profile refuses to show you. You can only search what you
// can already see, which is why this takes the gated type and not the raw row.
func filterMobileTrainers(in []mobileTrainer, q string) []mobileTrainer {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return in
	}
	out := make([]mobileTrainer, 0, len(in))
	for _, t := range in {
		if strings.Contains(strings.ToLower(t.Username), q) ||
			(t.TrainerName != "" && strings.Contains(strings.ToLower(t.TrainerName), q)) {
			out = append(out, t)
		}
	}
	return out
}

// clampQueryInt reads a non-negative integer query parameter, falling back to def
// when it is absent or unparseable and clamping it into [0, max].
//
// Unparseable falls back rather than erroring on purpose: ?limit=abc from a
// client bug should serve the default page, not fail the screen. A negative or
// oversized value is clamped for the same reason, and because out[offset:] panics
// on either.
func clampQueryInt(r *http.Request, name string, def, min, max int) int {
	v := def
	if raw := r.URL.Query().Get(name); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			v = n
		}
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

type mobileFriend struct {
	Username    string `json:"username"`
	TrainerName string `json:"trainer_name"`
	Avatar      string `json:"avatar,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

type mobileTrainerProfile struct {
	Trainer        mobileTrainer  `json:"trainer"`
	IsOwnProfile   bool           `json:"is_own_profile"`
	IsFollowing    bool           `json:"is_following"`
	FollowsMe      bool           `json:"follows_me"`
	IsFriend       bool           `json:"is_friend"`
	IsBlocked      bool           `json:"is_blocked"`
	TheyBlockedYou bool           `json:"they_blocked_you"`
	RecentFriends  []mobileFriend `json:"recent_friends"`
	FollowerCount  int            `json:"follower_count"`
	FollowingCount int            `json:"following_count"`
	// MyFeedbackID and MyFeedbackOptionID are the viewer's own feedback on this
	// trainer, so the screen can show "you said X" with a way to take it back
	// instead of offering the form again. Both 0 when they have left none, and
	// both always 0 on your own profile, because feedback on yourself is not a
	// thing the page offers. Mirrors MyFeedbackID / MyFeedbackOpt on
	// trainerProfileData.
	MyFeedbackID       uint `json:"my_feedback_id"`
	MyFeedbackOptionID uint `json:"my_feedback_option_id"`
}

// MobileTrainerProfile serves one trainer's profile.
//
// Only the parts that have no endpoint yet. Feedback, awards and the shiny
// collection already have their own GETs that work over Bearer, so they are not
// duplicated here.
func (h *Handlers) MobileTrainerProfile(w http.ResponseWriter, r *http.Request) {
	viewer, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	t, userID, found := h.lookupTrainer(chi.URLParam(r, "username"))
	if !found {
		writeJSONError(w, "trainer not found", http.StatusNotFound)
		return
	}

	isSelf := userID == viewer.ID
	out := mobileTrainerProfile{
		Trainer:       toMobileTrainer(t, isSelf),
		IsOwnProfile:  isSelf,
		RecentFriends: []mobileFriend{},
	}

	if isSelf {
		avatars := h.avatarURLMap()
		if rows, err := h.db.Query(`
			SELECT u.username, COALESCE(u.trainer_name,''), COALESCE(u.avatar,'')
			FROM user_follows uf JOIN users u ON u.id = uf.friend_id
			WHERE uf.user_id = ? ORDER BY uf.created_at DESC LIMIT 5`, viewer.ID); err == nil {
			defer rows.Close()
			for rows.Next() {
				var f mobileFriend
				if rows.Scan(&f.Username, &f.TrainerName, &f.Avatar) == nil {
					f.AvatarURL = absoluteURL(avatars[f.Avatar])
					out.RecentFriends = append(out.RecentFriends, f)
				}
			}
		}
	} else {
		h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, viewer.ID, userID).Scan(&out.IsFollowing)
		h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ? AND friend_id = ?`, userID, viewer.ID).Scan(&out.FollowsMe)
		h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, viewer.ID, userID).Scan(&out.IsBlocked)
		h.db.QueryRow(`SELECT COUNT(*) FROM user_blocks WHERE user_id = ? AND blocked_id = ?`, userID, viewer.ID).Scan(&out.TheyBlockedYou)
		out.IsFriend = out.IsFollowing && out.FollowsMe

		// No rows is the common case, and Scan leaves both at zero for it, which
		// is exactly what the page does with the same query.
		h.db.QueryRow(`SELECT id, option_id FROM user_feedback WHERE author_id = ? AND target_id = ?`,
			viewer.ID, userID).Scan(&out.MyFeedbackID, &out.MyFeedbackOptionID)
	}

	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE friend_id = ?`, userID).Scan(&out.FollowerCount)
	h.db.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id = ?`, userID).Scan(&out.FollowingCount)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
