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

// ── Page handlers ─────────────────────────────────────────────

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
		http.Error(w, "bad request", http.StatusBadRequest)
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
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
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
		http.Error(w, "could not generate token", http.StatusInternalServerError)
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
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	keys := []string{
		"page_raids_enabled",
		"page_dps_enabled",
		"page_pvp_enabled",
		"page_events_enabled",
		"page_trainers_enabled",
		"section_trainer_directory_enabled",
		"section_raid_finder_enabled",
		"page_shinies_enabled",
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

// ── User management API ───────────────────────────────────────

type adminTagEntry struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type adminUserRecord struct {
	ID              uint            `json:"id"`
	Username        string          `json:"username"`
	Email           string          `json:"email"`
	Role            string          `json:"role"`
	PendingRole     string          `json:"pending_role"`
	Disabled        bool            `json:"disabled"`
	DirectoryHidden bool            `json:"directory_hidden"`
	RaidBanned      bool            `json:"raid_banned"`
	StrikeCount     int             `json:"strike_count"`
	CreatedAt       string          `json:"created_at"`
	APIAccess       bool            `json:"api_access"`
	Tags            []adminTagEntry `json:"tags"`
	RaidXP          int             `json:"raid_xp"`
	RaterWeight     float64         `json:"rater_weight"`
}

func (h *Handlers) AdminUsersAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT u.id, u.username, u.email, u.role, COALESCE(u.pending_role, ''),
		       u.disabled, u.directory_hidden, u.raid_banned,
		       COUNT(s.id) AS strike_count, u.created_at, u.api_access,
		       COALESCE(u.raid_xp, 0), COALESCE(u.rater_weight, 1.000)
		FROM users u
		LEFT JOIN user_strikes s ON s.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at ASC`)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	users := []adminUserRecord{}
	idxByID := map[uint]int{}
	for rows.Next() {
		var u adminUserRecord
		var createdAt time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.PendingRole,
			&u.Disabled, &u.DirectoryHidden, &u.RaidBanned, &u.StrikeCount, &createdAt, &u.APIAccess,
			&u.RaidXP, &u.RaterWeight); err != nil {
			continue
		}
		u.CreatedAt = createdAt.Format("2006-01-02")
		if auth.SuperadminUser != "" && u.Username == auth.SuperadminUser {
			u.APIAccess = true
		}
		u.Tags = []adminTagEntry{}
		idxByID[u.ID] = len(users)
		users = append(users, u)
	}

	if tagRows, err := h.db.Query(`SELECT ut.user_id, t.id, t.name, t.color FROM user_tags ut JOIN tags t ON t.id = ut.tag_id ORDER BY ut.user_id, t.name`); err == nil {
		defer tagRows.Close()
		for tagRows.Next() {
			var uid uint
			var tag adminTagEntry
			if tagRows.Scan(&uid, &tag.ID, &tag.Name, &tag.Color) == nil {
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
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var targetUsername, targetRole string
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&targetUsername, &targetRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if (auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser) || targetRole == "moderator" || targetRole == "admin" {
		writeJSONError(w, "cannot hide a staff member from the directory", http.StatusForbidden)
		return
	}
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET directory_hidden = ? WHERE id = ?`, body.Hidden, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(body.NewPassword) < 8 {
		writeJSONError(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcryptCost)
	if err != nil {
		writeJSONError(w, "could not hash password", http.StatusInternalServerError)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot reset superadmin password", http.StatusForbidden)
		return
	}

	result, err := h.db.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), id)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminChangeUsername(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(body.Username)
	if len(username) < 2 || len(username) > 32 {
		writeJSONError(w, "username must be 2-32 characters", http.StatusBadRequest)
		return
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			writeJSONError(w, "username may only contain letters, numbers, _ and -", http.StatusBadRequest)
			return
		}
	}

	var currentUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&currentUsername); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if currentUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot rename the superadmin account", http.StatusForbidden)
		return
	}

	_, err = h.db.Exec(`UPDATE users SET username = ? WHERE id = ?`, username, id)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			writeJSONError(w, "username already taken", http.StatusConflict)
			return
		}
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "username": username})
}

func (h *Handlers) AdminToggleDisable(w http.ResponseWriter, r *http.Request) {
	actor := h.currentUser(r)

	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	if actor != nil && uint64(actor.ID) == id {
		writeJSONError(w, "cannot disable your own account", http.StatusForbidden)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot disable the superadmin account", http.StatusForbidden)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, body.Disabled, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
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
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var targetUsername, targetRole string
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&targetUsername, &targetRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if (auth.SuperadminUser != "" && targetUsername == auth.SuperadminUser) || targetRole == "moderator" || targetRole == "admin" {
		writeJSONError(w, "cannot raid-ban a staff member", http.StatusForbidden)
		return
	}
	var body struct {
		Banned bool `json:"banned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET raid_banned = ? WHERE id = ?`, body.Banned, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
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
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	rows, err := h.db.Query(
		`SELECT id, reason, issued_by_name, created_at FROM user_strikes WHERE user_id = ? ORDER BY created_at DESC`, id)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
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
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if len(body.Reason) == 0 {
		writeJSONError(w, "reason is required", http.StatusBadRequest)
		return
	}
	if len(body.Reason) > 255 {
		body.Reason = body.Reason[:255]
	}
	var targetExists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&targetExists); err != nil || !targetExists {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	result, err := h.db.Exec(
		`INSERT INTO user_strikes (user_id, reason, issued_by, issued_by_name) VALUES (?, ?, ?, ?)`,
		id, body.Reason, actor.ID, actor.Username,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	newID, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": newID})
}

func (h *Handlers) AdminStrikesDelete(w http.ResponseWriter, r *http.Request) {
	strikeID, err := strconv.ParseUint(chi.URLParam(r, "strikeId"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid strike id", http.StatusBadRequest)
		return
	}
	result, err := h.db.Exec(`DELETE FROM user_strikes WHERE id = ?`, strikeID)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "strike not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminChangeRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"user": true, "tester": true, "moderator": true, "admin": true}
	if !validRoles[body.Role] {
		writeJSONError(w, "invalid role", http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot change role of superadmin", http.StatusForbidden)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, body.Role, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	// Revoke API access if user is demoted away from admin.
	if body.Role != "admin" {
		h.db.Exec(`UPDATE users SET api_access = 0 WHERE id = ?`, id)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── API Access ────────────────────────────────────────────────

func (h *Handlers) AdminToggleAPIAccess(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		APIAccess bool `json:"api_access"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	var targetUsername, targetRole string
	if err := h.db.QueryRow(`SELECT username, role FROM users WHERE id = ?`, id).Scan(&targetUsername, &targetRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "superadmin always has API access", http.StatusBadRequest)
		return
	}
	if targetRole != "admin" {
		writeJSONError(w, "API access can only be granted to admin users", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET api_access = ? WHERE id = ?`, body.APIAccess, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Pending role confirmation ─────────────────────────────────

func (h *Handlers) AdminConfirmRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var targetUsername, pendingRole string
	if err := h.db.QueryRow(`SELECT username, COALESCE(pending_role, '') FROM users WHERE id = ?`, id).
		Scan(&targetUsername, &pendingRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot modify superadmin", http.StatusForbidden)
		return
	}
	if pendingRole == "" {
		writeJSONError(w, "user has no pending role", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET role = ?, pending_role = NULL WHERE id = ?`, pendingRole, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "role": pendingRole})
}

func (h *Handlers) AdminRejectRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var targetUsername string
	if err := h.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&targetUsername); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetUsername == auth.SuperadminUser {
		writeJSONError(w, "cannot modify superadmin", http.StatusForbidden)
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET pending_role = NULL WHERE id = ?`, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ── Tag management ────────────────────────────────────────────

func (h *Handlers) AdminTagsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`SELECT id, name, color FROM tags ORDER BY name`)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
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
		writeJSONError(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeJSONError(w, "name must be 1-32 characters", http.StatusBadRequest)
		return
	}
	if body.Color == "" {
		body.Color = "#888888"
	}
	res, err := h.db.Exec(`INSERT INTO tags (name, color) VALUES (?, ?)`, body.Name, body.Color)
	if err != nil {
		writeJSONError(w, "tag name already exists", http.StatusConflict)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(adminTagEntry{ID: uint(id), Name: body.Name, Color: body.Color})
}

func (h *Handlers) AdminTagDelete(w http.ResponseWriter, r *http.Request) {
	caller := h.currentUser(r)
	if caller == nil || !caller.IsSuperAdmin() {
		writeJSONError(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	h.db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTagUpdate(w http.ResponseWriter, r *http.Request) {
	caller := h.currentUser(r)
	if caller == nil || !caller.IsSuperAdmin() {
		writeJSONError(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeJSONError(w, "name must be 1-32 characters", http.StatusBadRequest)
		return
	}
	if body.Color == "" {
		body.Color = "#888888"
	}
	_, err = h.db.Exec(`UPDATE tags SET name = ?, color = ? WHERE id = ?`, body.Name, body.Color, id)
	if err != nil {
		writeJSONError(w, "tag name already exists", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(adminTagEntry{ID: uint(id), Name: body.Name, Color: body.Color})
}

func (h *Handlers) AdminUserTagAdd(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var exists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, uid).Scan(&exists); err != nil || !exists {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	var body struct {
		TagID uint `json:"tag_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	h.db.Exec(`INSERT IGNORE INTO user_tags (user_id, tag_id) VALUES (?, ?)`, uid, body.TagID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminUserTagRemove(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	tagID, err := strconv.ParseUint(chi.URLParam(r, "tagId"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid tag id", http.StatusBadRequest)
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
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		RaidXP int `json:"raid_xp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.RaidXP < 0 || body.RaidXP > 9999999 {
		writeJSONError(w, "raid_xp must be 0–9999999", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET raid_xp = ? WHERE id = ?`, body.RaidXP, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminSetRaterWeight(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		RaterWeight float64 `json:"rater_weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.RaterWeight < 0.1 || body.RaterWeight > 1.5 {
		writeJSONError(w, "rater_weight must be 0.1–1.5", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET rater_weight = ? WHERE id = ?`, body.RaterWeight, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminClearRatings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM raid_ratings WHERE rated_id = ?`, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminRefreshActivity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET last_raid_at = NOW() WHERE id = ?`, id); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
