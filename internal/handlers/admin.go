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
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

func (h *Handlers) AdminRefreshData(w http.ResponseWriter, r *http.Request) {
	h.store.Refresh()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	regOpen := "0"
	if r.FormValue("registration_open") == "1" {
		regOpen = "1"
	}
	storeOn := "0"
	if r.FormValue("store_enabled") == "1" {
		storeOn = "1"
	}

	saveErr := false
	for _, kv := range [][2]string{
		{"registration_open", regOpen},
		{"store_enabled", storeOn},
	} {
		if _, err := h.db.Exec(`
			INSERT INTO site_settings (setting_key, setting_value)
			VALUES (?, ?)
			ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
			kv[0], kv[1],
		); err != nil {
			log.Printf("admin: save settings %s: %v", kv[0], err)
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
		RegistrationOpen: regOpen == "1",
		StoreEnabled:     storeOn == "1",
		Maintenance:      h.maintenanceSettings(),
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

	grantedRole := r.FormValue("granted_role")
	validRoles := map[string]bool{"user": true, "tester": true, "moderator": true, "admin": true}
	if !validRoles[grantedRole] {
		grantedRole = "user"
	}

	maxUses := 1
	if v, err := strconv.Atoi(r.FormValue("max_uses")); err == nil && v >= 1 && v <= 50 {
		maxUses = v
	}

	isStaff := grantedRole == "moderator" || grantedRole == "admin"
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

	if maxUses > 1 && grantedRole != "user" {
		fail(h.t(r, "error.invite_multiuse_role"))
		return
	}
	if isStaff && maxUses != 1 {
		fail(h.t(r, "error.invite_staff_single"))
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

	keys := []string{
		"page_raids_enabled",
		"page_dps_enabled",
		"page_pvp_enabled",
		"page_events_enabled",
		"page_iv_enabled",
		"page_trainers_enabled",
		"section_trainer_directory_enabled",
		"section_raid_finder_enabled",
		"page_shinies_enabled",
		"section_translator_apps_enabled",
	}

	saveErr := false
	for _, key := range keys {
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

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcryptCost)
	if err != nil {
		writeJSONError(w, h.t(r, "error.server"), http.StatusInternalServerError)
		return
	}

	caller := h.currentUser(r)

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser && !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.adm_reset_superadmin"), http.StatusForbidden)
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

	caller := h.currentUser(r)

	var currentUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&currentUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if currentUsername == auth.SuperadminUser && !caller.IsSuperAdmin() {
		writeJSONError(w, h.t(r, "error.adm_rename_superadmin"), http.StatusForbidden)
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

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, h.t(r, "error.adm_disable_superadmin"), http.StatusForbidden)
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
	caller := h.currentUser(r)
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
	if len(body.Reason) > 255 {
		body.Reason = body.Reason[:255]
	}
	var targetExists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&targetExists); err != nil || !targetExists {
		writeJSONError(w, h.t(r, "error.user_not_found"), http.StatusNotFound)
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
