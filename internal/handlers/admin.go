package handlers

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/costumes"
)

type inviteRow struct {
	Token       string
	Link        string
	ExpiresAt   time.Time
	GrantedRole string
	MaxUses     int
	UseCount    int
}

type adminData struct {
	RegistrationOpen bool
	StoreEnabled     bool
	Maintenance      PageMaintenance
	MobileBuild      int
	Message          string
	MessageOK        bool
	InviteLink       string
	ActiveInvites    []inviteRow
	SuperadminUser   string
}

func (h *Handlers) AdminPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		Maintenance:      h.maintenanceSettings(),
		MobileBuild:      h.mobileBuildNumber(),
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

func (h *Handlers) AdminRefreshData(w http.ResponseWriter, r *http.Request) {
	h.store.Refresh()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminRunScrapers fetches every scraped source fresh, compares it to what is stored,
// applies any changes, and returns a per-source report so an admin can confirm the
// scrapers are reachable, parse cleanly, and match the current upstream data.
func (h *Handlers) AdminRunScrapers(w http.ResponseWriter, r *http.Request) {
	results := h.store.CheckScrapers()
	// Costumes report drift but never auto-apply it: a new code is unusable until a human gives
	// it a label trainers would recognise, so this row only ever says "run `make costumes`".
	results = append(results, costumes.DriftCheck())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "results": results})
}

func (h *Handlers) AdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	// Which keys the submitted form actually carries, named by its hidden
	// _settings inputs.
	//
	// An unchecked checkbox sends nothing, so "this form does not own the key"
	// and "the key is switched off" arrive looking identical. Two separate forms
	// post here, each carrying one toggle, so reading every key from every
	// submission meant saving the registration toggle switched the store off and
	// saving the store toggle closed registration.
	owned := make(map[string]bool, len(r.Form["_settings"]))
	for _, k := range r.Form["_settings"] {
		owned[k] = true
	}

	saveErr := false
	for _, key := range []string{"registration_open", "store_enabled"} {
		if !owned[key] {
			continue
		}
		val := "0"
		if r.FormValue(key) == "1" {
			val = "1"
		}
		if _, err := h.db.Exec(`
			INSERT INTO site_settings (setting_key, setting_value)
			VALUES (?, ?)
			ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
			key, val,
		); err != nil {
			log.Printf("admin: save settings %s: %v", key, err)
			saveErr = true
		}
	}

	msg := h.t(r, "admin.settings.saved")
	msgOK := true
	if saveErr {
		msg = h.t(r, "admin.settings.save_error")
		msgOK = false
	}

	// Read both back rather than echoing the form, so the toggle this submission
	// did not carry renders its real value instead of a default.
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		Maintenance:      h.maintenanceSettings(),
		MobileBuild:      h.mobileBuildNumber(),
		Message:          msg,
		MessageOK:        msgOK,
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

func (h *Handlers) AdminGenerateInvite(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Error(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	maxUses := 1
	if v, err := strconv.Atoi(r.FormValue("max_uses")); err == nil {
		maxUses = v
	}
	// Shared with the JSON path in mobile_admin.go, so the two cannot disagree
	// about who may be invited as what. Both rules exist so an invite link that
	// leaks cannot mint more than one moderator.
	grantedRole, maxUses, refusal := inviteRules(r.FormValue("granted_role"), maxUses)

	fail := func(msg string) {
		h.render(w, r, "admin", adminData{
			RegistrationOpen: h.registrationOpen(),
			StoreEnabled:     h.storeEnabled(),
			Maintenance:      h.maintenanceSettings(),
			Message:          msg,
			MessageOK:        false,
			ActiveInvites:    h.loadActiveInvites(r),
			SuperadminUser:   auth.SuperadminUser,
		})
	}

	if refusal != "" {
		fail(h.t(r, refusal))
		return
	}

	token, err := generateInviteToken()
	if err != nil {
		http.Error(w, h.t(r, "error.invite_generate"), http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	var dbErr error
	_, dbErr = h.db.Exec(
		`INSERT INTO invites (token, created_by, granted_role, expires_at, max_uses) VALUES (?, ?, ?, ?, ?)`,
		token, u.ID, grantedRole, expiresAt, maxUses,
	)
	if dbErr != nil {
		log.Printf("admin: generate invite: %v", dbErr)
		fail(h.t(r, "error.invite_generate"))
		return
	}

	link := inviteLink(r, token)
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		Maintenance:      h.maintenanceSettings(),
		InviteLink:       link,
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

func (h *Handlers) loadActiveInvites(r *http.Request) []inviteRow {
	rows, err := h.db.Query(`
		SELECT token, expires_at, granted_role, max_uses, use_count FROM invites
		WHERE use_count < max_uses AND expires_at > NOW()
		ORDER BY expires_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []inviteRow
	for rows.Next() {
		var inv inviteRow
		var expiresAt time.Time
		if err := rows.Scan(&inv.Token, &expiresAt, &inv.GrantedRole, &inv.MaxUses, &inv.UseCount); err == nil {
			inv.Link = inviteLink(r, inv.Token)
			inv.ExpiresAt = expiresAt
			out = append(out, inv)
		}
	}
	return out
}

// inviteTokenChars excludes ambiguous characters (0/O, 1/I/L) for readability.
const inviteTokenChars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func generateInviteToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = inviteTokenChars[int(v)%len(inviteTokenChars)]
	}
	return string(out), nil
}

func inviteLink(r *http.Request, token string) string {
	scheme := "https"
	if strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/register?invite=%s", scheme, r.Host, token)
}

func (h *Handlers) AdminCancelInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	h.db.Exec(`DELETE FROM invites WHERE token = ?`, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *Handlers) AdminUpdatePageSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	// The key list lives in mobile_admin.go, keyed by the client-facing name the
	// JSON path and GET /maintenance both use. One list, so a new toggle added for
	// one path cannot be missing from the other.
	//
	// Iteration order does not matter: each key is written independently, and this
	// form owns all of them, which is why it can safely rebuild every one from the
	// submission. That is NOT true of AdminUpdateSettings above; see the comment
	// there about _settings.
	saveErr := false
	for _, key := range pageSettingKeys {
		val := "0"
		if r.FormValue(key) == "1" {
			val = "1"
		}
		if _, err := h.db.Exec(`
			INSERT INTO site_settings (setting_key, setting_value)
			VALUES (?, ?)
			ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
			key, val,
		); err != nil {
			log.Printf("admin: save page setting %s: %v", key, err)
			saveErr = true
		}
	}

	msg := h.t(r, "admin.settings.saved")
	msgOK := true
	if saveErr {
		msg = h.t(r, "admin.settings.save_error")
		msgOK = false
	}

	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		Maintenance:      h.maintenanceSettings(),
		MobileBuild:      h.mobileBuildNumber(),
		Message:          msg,
		MessageOK:        msgOK,
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

type adminTagEntry struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Custom bool   `json:"custom"`
}

type adminUserRecord struct {
	ID              uint            `json:"id"`
	Username        string          `json:"username"`
	Email           string          `json:"email"`
	Role            string          `json:"role"`
	PendingRole     string          `json:"pending_role"`
	Disabled        bool            `json:"disabled"`
	DisabledReason  string          `json:"disabled_reason"`
	DirectoryHidden bool            `json:"directory_hidden"`
	RaidBanned      bool            `json:"raid_banned"`
	StrikeCount     int             `json:"strike_count"`
	CreatedAt       string          `json:"created_at"`
	APIAccess       bool            `json:"api_access"`
	Translator      bool            `json:"translator"`
	Tags            []adminTagEntry `json:"tags"`
	RaidXP          int             `json:"raid_xp"`
	RaterWeight     float64         `json:"rater_weight"`
	TrustScore      float64         `json:"trust_score"`
	SpecialRank     string          `json:"special_rank"`
}

func (h *Handlers) AdminUsersAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT u.id, u.username, u.email, u.role, COALESCE(u.pending_role, ''),
		       u.disabled, COALESCE(u.disabled_reason, ''), u.directory_hidden, u.raid_banned,
		       COUNT(s.id) AS strike_count, u.created_at, u.api_access, u.translator,
		       COALESCE(u.raid_xp, 0), COALESCE(u.rater_weight, 1.000),
		       COALESCE(u.trust_score, 0), COALESCE(u.special_rank, '')
		FROM users u
		LEFT JOIN user_strikes s ON s.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at ASC`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	users := []adminUserRecord{}
	idxByID := map[uint]int{}
	for rows.Next() {
		var u adminUserRecord
		var createdAt time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.PendingRole,
			&u.Disabled, &u.DisabledReason, &u.DirectoryHidden, &u.RaidBanned, &u.StrikeCount, &createdAt, &u.APIAccess,
			&u.Translator, &u.RaidXP, &u.RaterWeight, &u.TrustScore, &u.SpecialRank); err != nil {
			continue
		}
		u.CreatedAt = createdAt.Format("2006-01-02")
		if auth.SuperadminUser != "" && u.Username == auth.SuperadminUser {
			u.APIAccess = true
			u.Translator = true
		}
		u.Tags = []adminTagEntry{}
		idxByID[u.ID] = len(users)
		users = append(users, u)
	}

	if tagRows, err := h.db.Query(`
		SELECT ut.user_id, t.id, t.name, t.color,
		       CASE WHEN ctr.id IS NOT NULL THEN 1 ELSE 0 END AS custom
		FROM user_tags ut
		JOIN tags t ON t.id = ut.tag_id
		LEFT JOIN custom_tag_requests ctr
		       ON ctr.user_id = ut.user_id AND ctr.name = t.name AND ctr.status = 'approved'
		ORDER BY ut.user_id, t.name`); err == nil {
		defer tagRows.Close()
		for tagRows.Next() {
			var uid uint
			var tag adminTagEntry
			var customInt int
			if tagRows.Scan(&uid, &tag.ID, &tag.Name, &tag.Color, &customInt) == nil {
				tag.Custom = customInt == 1
				if idx, ok := idxByID[uid]; ok {
					users[idx].Tags = append(users[idx].Tags, tag)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func (h *Handlers) AdminToggleDirectoryHide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var targetUsername, targetRole string
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&targetUsername, &targetRole); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if (auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser) || targetRole == "moderator" || targetRole == "admin" {
		writeJSONError(w, h.t(r, "error.adm_hide_staff"), http.StatusForbidden)
		return
	}
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET directory_hidden = ? WHERE id = ?`, body.Hidden, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// staffRank orders privilege levels so that an account-altering action can require
// the caller to strictly outrank their target. The superadmin sits above every
// stored role, because that privilege comes from the SUPERADMIN_USER env var and
// not from the users.role column.
func staffRank(username, role string) int {
	if auth.SuperadminUser != "" && username == auth.SuperadminUser {
		return 3
	}
	switch role {
	case "admin":
		return 2
	case "moderator":
		return 1
	}
	return 0
}

// confirmMatchesUsername reports whether a typed confirmation names the target
// account exactly. The match is deliberately not case insensitive and not
// fuzzy: the panel prints the username directly above the box, so a caller who
// cannot reproduce it is a caller who has the wrong account in front of them.
func confirmMatchesUsername(typed, targetUsername string) bool {
	typed = strings.TrimSpace(typed)
	return typed != "" && typed == targetUsername
}

// mayActOn reports whether the caller may take an account-altering action against
// the given target, writing the refusal itself when they may not.
//
// Every such handler must funnel through this. Before it existed, RequireMod was
// the only gate on password reset, disable, rename and strikes, so a moderator
// could reset an administrator's password and simply log in as them, or disable
// every admin at once (disabling also drops the target's sessions). Only the
// superadmin was ever checked for, and only by username.
//
// The rule is strict: equal rank is refused, so staff cannot act on their peers
// laterally either. The superadmin outranks everyone and is never locked out.
func (h *Handlers) mayActOn(w http.ResponseWriter, r *http.Request, targetUsername, targetRole string) bool {
	caller := h.currentUser(r)
	if caller == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return false
	}
	if staffRank(caller.Username, caller.Role) <= staffRank(targetUsername, targetRole) {
		writeJSONError(w, h.t(r, "error.adm_staff_target"), http.StatusForbidden)
		return false
	}
	return true
}

// targetUser loads the username and role a staff check needs, writing the 404
// itself when the id does not resolve.
func (h *Handlers) targetUser(w http.ResponseWriter, r *http.Request, id uint64) (username, role string, ok bool) {
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&username, &role); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return "", "", false
	}
	return username, role, true
}

func (h *Handlers) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if len(body.NewPassword) < 8 {
		writeJSONError(w, h.t(r, "error.password_length"), http.StatusBadRequest)
		return
	}

	// Authorize before hashing: bcrypt at cost 13 is deliberately expensive, so a
	// refused caller should not get to spend it.
	targetUsername, targetRole, ok := h.targetUser(w, r, id)
	if !ok {
		return
	}
	if !h.mayActOn(w, r, targetUsername, targetRole) {
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcryptCost)
	if err != nil {
		writeJSONError(w, h.t(r, "error.server"), http.StatusInternalServerError)
		return
	}

	result, err := h.db.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), id)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}

	// An admin reset is the "this account is compromised" lever, so it has to evict
	// the intruder too. The self-serve reset and the disable path both already do
	// this; this one did not, and left every existing session alive.
	if _, err := h.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		log.Printf("admin reset password: clear sessions for user %d: %v", id, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminChangeUsername(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(body.Username)
	if len(username) < 2 || len(username) > 32 {
		writeJSONError(w, h.t(r, "error.username_length"), http.StatusBadRequest)
		return
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			writeJSONError(w, h.t(r, "error.username_chars"), http.StatusBadRequest)
			return
		}
	}

	currentUsername, currentRole, ok := h.targetUser(w, r, id)
	if !ok {
		return
	}
	if !h.mayActOn(w, r, currentUsername, currentRole) {
		return
	}

	_, err = h.db.Exec(`UPDATE users SET username = ? WHERE id = ?`, username, id)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			writeJSONError(w, h.t(r, "error.username_taken"), http.StatusConflict)
			return
		}
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "username": username})
}

func (h *Handlers) AdminToggleDisable(w http.ResponseWriter, r *http.Request) {
	actor := h.currentUser(r)

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	if actor != nil && uint64(actor.ID) == id {
		writeJSONError(w, h.t(r, "error.adm_disable_self"), http.StatusForbidden)
		return
	}

	targetUsername, targetRole, ok := h.targetUser(w, r, id)
	if !ok {
		return
	}
	if !h.mayActOn(w, r, targetUsername, targetRole) {
		return
	}

	reason := strings.TrimSpace(body.Reason)
	if !body.Disabled {
		reason = ""
	}
	if _, err := h.db.Exec(`UPDATE users SET disabled = ?, disabled_reason = ? WHERE id = ?`, body.Disabled, reason, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	if body.Disabled {
		h.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminToggleRaidBan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var targetUsername, targetRole string
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&targetUsername, &targetRole); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if (auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser) || targetRole == "moderator" || targetRole == "admin" {
		writeJSONError(w, h.t(r, "error.adm_raidban_staff"), http.StatusForbidden)
		return
	}
	var body struct {
		Banned bool `json:"banned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET raid_banned = ? WHERE id = ?`, body.Banned, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if body.Banned {
		// Pull the user out of any live matchmaking immediately.
		h.db.Exec(`DELETE FROM raid_queue WHERE user_id = ?`, id)
		h.db.Exec(`UPDATE raid_lobby_members SET state = 'removed' WHERE user_id = ? AND state IN ('matched','confirmed')`, id)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminSetSpecialRank grants or clears the Trusted / Content Creator special
// rank (admin+ only). Holders can create custom raid lobbies and get a badge.
func (h *Handlers) AdminSetSpecialRank(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	caller, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	if auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser && !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.adm_modify_superadmin"), http.StatusForbidden)
		return
	}
	var body struct {
		Rank string `json:"rank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.Rank != "" && body.Rank != "trusted" && body.Rank != "content_creator" {
		writeJSONError(w, h.t(r, "error.adm_invalid_rank"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET special_rank = ? WHERE id = ?`, body.Rank, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Strikes ───────────────────────────────────────────────────

type strikeRecord struct {
	ID           uint   `json:"id"`
	Reason       string `json:"reason"`
	IssuedByName string `json:"issued_by_name"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handlers) AdminStrikesGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	rows, err := h.db.Query(
		`SELECT id, reason, issued_by_name, created_at FROM user_strikes WHERE user_id = ? ORDER BY created_at DESC`, id)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	strikes := []strikeRecord{}
	for rows.Next() {
		var s strikeRecord
		var t time.Time
		if err := rows.Scan(&s.ID, &s.Reason, &s.IssuedByName, &t); err != nil {
			continue
		}
		s.CreatedAt = t.Format("2006-01-02")
		strikes = append(strikes, s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(strikes)
}

func (h *Handlers) AdminStrikesAdd(w http.ResponseWriter, r *http.Request) {
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if len(body.Reason) == 0 {
		writeJSONError(w, h.t(r, "error.adm_reason_required"), http.StatusBadRequest)
		return
	}
	body.Reason = truncRunes(body.Reason, 255)
	targetUsername, targetRole, ok := h.targetUser(w, r, id)
	if !ok {
		return
	}
	if !h.mayActOn(w, r, targetUsername, targetRole) {
		return
	}
	result, err := h.db.Exec(
		`INSERT INTO user_strikes (user_id, reason, issued_by, issued_by_name) VALUES (?, ?, ?, ?)`,
		id, body.Reason, actor.ID, actor.Username,
	)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	newID, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": newID})
}

func (h *Handlers) AdminStrikesDelete(w http.ResponseWriter, r *http.Request) {
	strikeID, err := strconv.ParseUint(chi.URLParam(r, "strikeId"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	// The route only carries the strike id, so resolve the user it belongs to before
	// deciding: clearing a strike is as much a staff action as issuing one.
	var strikeUsername, strikeRole string
	err = h.db.QueryRow(
		`SELECT u.username, u.role FROM user_strikes s JOIN users u ON u.id = s.user_id WHERE s.id = ?`,
		strikeID,
	).Scan(&strikeUsername, &strikeRole)
	if err != nil {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	if !h.mayActOn(w, r, strikeUsername, strikeRole) {
		return
	}

	result, err := h.db.Exec(`DELETE FROM user_strikes WHERE id = ?`, strikeID)
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

func (h *Handlers) AdminChangeRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"user": true, "tester": true, "moderator": true, "admin": true}
	if !validRoles[body.Role] {
		writeJSONError(w, h.t(r, "error.adm_invalid_role"), http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.adm_role_superadmin"), http.StatusForbidden)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, body.Role, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── API Access ────────────────────────────────────────────────

func (h *Handlers) AdminToggleAPIAccess(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		APIAccess bool `json:"api_access"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.adm_superadmin_api"), http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET api_access = ? WHERE id = ?`, body.APIAccess, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Pending role confirmation ─────────────────────────────────

func (h *Handlers) AdminConfirmRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var targetUsername, pendingRole string
	if err := h.db.QueryRow(`SELECT username, COALESCE(pending_role, '') FROM users WHERE id = ?`, id).
		Scan(&targetUsername, &pendingRole); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.adm_modify_superadmin"), http.StatusForbidden)
		return
	}
	if pendingRole == "" {
		writeJSONError(w, h.t(r, "error.adm_no_pending_role"), http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET role = ?, pending_role = NULL WHERE id = ?`, pendingRole, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "role": pendingRole})
}

func (h *Handlers) AdminRejectRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.adm_modify_superadmin"), http.StatusForbidden)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET pending_role = NULL WHERE id = ?`, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Tag management ────────────────────────────────────────────

func (h *Handlers) AdminTagsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`SELECT id, name, color FROM tags ORDER BY name`)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	tags := []adminTagEntry{}
	for rows.Next() {
		var t adminTagEntry
		if rows.Scan(&t.ID, &t.Name, &t.Color) == nil {
			tags = append(tags, t)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func (h *Handlers) AdminTagCreate(w http.ResponseWriter, r *http.Request) {
	caller := h.currentUser(r)
	if caller == nil || !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeJSONError(w, h.t(r, "error.adm_tag_name_length"), http.StatusBadRequest)
		return
	}
	if body.Color == "" {
		body.Color = "#888888"
	}
	res, err := h.db.Exec(`INSERT INTO tags (name, color) VALUES (?, ?)`, body.Name, body.Color)
	if err != nil {
		writeJSONError(w, h.t(r, "error.adm_tag_exists"), http.StatusConflict)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(adminTagEntry{ID: uint(id), Name: body.Name, Color: body.Color})
}

func (h *Handlers) AdminTagDelete(w http.ResponseWriter, r *http.Request) {
	caller := h.currentUser(r)
	if caller == nil || !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	h.db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTagUpdate(w http.ResponseWriter, r *http.Request) {
	caller := h.currentUser(r)
	if caller == nil || !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeJSONError(w, h.t(r, "error.adm_tag_name_length"), http.StatusBadRequest)
		return
	}
	if body.Color == "" {
		body.Color = "#888888"
	}
	_, err = h.db.Exec(`UPDATE tags SET name = ?, color = ? WHERE id = ?`, body.Name, body.Color, id)
	if err != nil {
		writeJSONError(w, h.t(r, "error.adm_tag_exists"), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(adminTagEntry{ID: uint(id), Name: body.Name, Color: body.Color})
}

func (h *Handlers) AdminUserTagAdd(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var exists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, uid).Scan(&exists); err != nil || !exists {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	var body struct {
		TagID uint `json:"tag_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	h.db.Exec(`INSERT IGNORE INTO user_tags (user_id, tag_id) VALUES (?, ?)`, uid, body.TagID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminUserTagRemove(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	tagID, err := strconv.ParseUint(chi.URLParam(r, "tagId"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var isCustom int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM custom_tag_requests ctr
		JOIN tags t ON t.name = ctr.name
		WHERE t.id = ? AND ctr.user_id = ? AND ctr.status = 'approved'`,
		tagID, uid).Scan(&isCustom)
	if isCustom > 0 {
		writeJSONError(w, h.t(r, "error.adm_custom_tag_managed"), http.StatusForbidden)
		return
	}
	h.db.Exec(`DELETE FROM user_tags WHERE user_id = ? AND tag_id = ?`, uid, tagID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Raid ranking & rating adjustment ─────────────────────────

func (h *Handlers) AdminSetRaidXP(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		RaidXP int `json:"raid_xp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.RaidXP < 0 || body.RaidXP > 9999999 {
		writeJSONError(w, h.t(r, "error.adm_raid_xp_range"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET raid_xp = ? WHERE id = ?`, body.RaidXP, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminSetRaterWeight(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	var body struct {
		RaterWeight float64 `json:"rater_weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.RaterWeight < 0.1 || body.RaterWeight > 1.5 {
		writeJSONError(w, h.t(r, "error.adm_rater_weight_range"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET rater_weight = ? WHERE id = ?`, body.RaterWeight, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminClearRatings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM raid_ratings WHERE rated_id = ?`, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminRefreshActivity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET last_raid_at = NOW() WHERE id = ?`, id); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminDeleteUser permanently removes an account and everything tied to it.
// Superadmin only, and irreversible: there is no tombstone row and nothing is
// archived, which is why the caller has to retype the target's username.
//
// Most of the work is already done by the schema. Of the columns pointing at
// users.id, all but two carry a foreign key that either cascades or nulls. The
// statements below cover what the database cannot clean up on its own, and every
// one of them must run before the DELETE, while the rows they join against still
// exist. Sessions need no handling: fk_session_user cascades, so the account is
// logged out everywhere by the delete itself.
func (h *Handlers) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	// The route is already wrapped in RequireSuperAdmin. Repeated here so the
	// handler cannot be re-mounted behind a weaker gate by accident.
	actor := h.currentUser(r)
	if actor == nil || !actor.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
		return
	}

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}

	var body struct {
		ConfirmUsername string `json:"confirm_username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	if uint64(actor.ID) == id {
		writeJSONError(w, h.t(r, "error.adm_delete_self"), http.StatusForbidden)
		return
	}

	targetUsername, targetRole, ok := h.targetUser(w, r, id)
	if !ok {
		return
	}
	if !h.mayActOn(w, r, targetUsername, targetRole) {
		return
	}
	// Compared against the username just loaded from the database, never against
	// anything the browser supplied, so a stale panel cannot delete a recycled id.
	if !confirmMatchesUsername(body.ConfirmUsername, targetUsername) {
		writeJSONError(w, h.t(r, "error.adm_delete_confirm_mismatch"), http.StatusBadRequest)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		log.Printf("admin delete user %d: begin: %v", id, err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	cleanup := []struct {
		what string
		stmt string
	}{
		// Neither column has a foreign key behind it, so nothing nulls them for us
		// and they would be left pointing at an id that no longer exists.
		{"custom_tag_requests.reviewed_by",
			`UPDATE custom_tag_requests SET reviewed_by = NULL WHERE reviewed_by = ?`},
		{"bug_report_participants.added_by",
			`UPDATE bug_report_participants SET added_by = NULL WHERE added_by = ?`},

		// reporter_id is ON DELETE SET NULL, but reporter_email is a plain string
		// and would outlive the account whose address it is.
		{"bug_reports.reporter_email",
			`UPDATE bug_reports SET reporter_email = NULL WHERE reporter_id = ?`},

		// trust_events.lobby_id has no foreign key to raid_lobbies, so deleting a
		// host strands other trainers' events. Null the dead reference rather than
		// delete the row: users.trust_score is a stored running sum of
		// applied_delta, so removing those rows would silently desync scores that
		// belong to trainers who are still here. uk_te_vote tolerates the NULL.
		{"trust_events.lobby_id",
			`UPDATE trust_events te JOIN raid_lobbies l ON l.id = te.lobby_id
			    SET te.lobby_id = NULL
			  WHERE l.host_id = ?`},

		// raid_ratings.post_id has no foreign key to raid_posts and is NOT NULL, so
		// unlike the events above these rows cannot be kept once the host's posts
		// cascade away. Nothing caches them: AdminClearRatings deletes the same
		// rows with no recompute afterwards.
		{"raid_ratings orphaned by raid_posts",
			`DELETE r FROM raid_ratings r
			   JOIN raid_posts p ON p.id = r.post_id
			  WHERE p.user_id = ?`},
	}
	for _, c := range cleanup {
		if _, err := tx.Exec(c.stmt, id); err != nil {
			log.Printf("admin delete user %d: cleanup %s: %v", id, c.what, err)
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
	}

	// Matched on the username as well as the id so the confirmation is atomic with
	// the delete. Without it there is a window, however small, in which the account
	// is renamed or deleted and recreated between the lookup above and this line,
	// and the typed confirmation would then have approved a different account.
	res, err := tx.Exec(`DELETE FROM users WHERE id = ? AND username = ?`, id, targetUsername)
	if err != nil {
		log.Printf("admin delete user %d: %v", id, err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The account changed underneath us. The deferred rollback undoes the
		// cleanup statements above, so nothing is left half done.
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("admin delete user %d: commit: %v", id, err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	// There is no audit table in this app, so the service log is the only lasting
	// record that this happened.
	log.Printf("admin: superadmin %q permanently deleted user %d (%q, role %q)",
		actor.Username, id, targetUsername, targetRole)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
