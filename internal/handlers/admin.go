package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	Token     string
	Link      string
	ExpiresAt time.Time
}

type adminData struct {
	RegistrationOpen bool
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
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
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

	_, err := h.db.Exec(`
		INSERT INTO site_settings (setting_key, setting_value)
		VALUES ('registration_open', ?)
		ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
		regOpen,
	)
	msg := "Settings saved."
	msgOK := true
	if err != nil {
		msg = "Error saving settings: " + err.Error()
		msgOK = false
	}

	h.render(w, r, "admin", adminData{
		RegistrationOpen: regOpen == "1",
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

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "could not generate token", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	_, err := h.db.Exec(
		`INSERT INTO invites (token, created_by, expires_at) VALUES (?, ?, ?)`,
		token, u.ID, expiresAt,
	)
	if err != nil {
		h.render(w, r, "admin", adminData{
			RegistrationOpen: h.registrationOpen(),
			Message:          "Error generating invite: " + err.Error(),
			MessageOK:        false,
			ActiveInvites:    h.loadActiveInvites(r),
			SuperadminUser:   auth.SuperadminUser,
		})
		return
	}

	link := inviteLink(r, token)
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		InviteLink:       link,
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}

func (h *Handlers) loadActiveInvites(r *http.Request) []inviteRow {
	rows, err := h.db.Query(`
		SELECT token, expires_at FROM invites
		WHERE used_at IS NULL AND expires_at > NOW()
		ORDER BY expires_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []inviteRow
	for rows.Next() {
		var token string
		var expiresAt time.Time
		if err := rows.Scan(&token, &expiresAt); err == nil {
			out = append(out, inviteRow{
				Token:     token,
				Link:      inviteLink(r, token),
				ExpiresAt: expiresAt,
			})
		}
	}
	return out
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
	h.db.Exec(`DELETE FROM invites WHERE token = ? AND used_at IS NULL`, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// ── User management API ───────────────────────────────────────

type adminUserRecord struct {
	ID              uint   `json:"id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	Disabled        bool   `json:"disabled"`
	DirectoryHidden bool   `json:"directory_hidden"`
	RaidBanned      bool   `json:"raid_banned"`
	StrikeCount     int    `json:"strike_count"`
	CreatedAt       string `json:"created_at"`
}

func (h *Handlers) AdminUsersAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT u.id, u.username, u.email, u.role, u.disabled, u.directory_hidden, u.raid_banned,
		       COUNT(s.id) AS strike_count, u.created_at
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
	for rows.Next() {
		var u adminUserRecord
		var createdAt time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Disabled, &u.DirectoryHidden, &u.RaidBanned, &u.StrikeCount, &createdAt); err != nil {
			continue
		}
		u.CreatedAt = createdAt.Format("2006-01-02")
		users = append(users, u)
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

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
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

	validRoles := map[string]bool{"user": true, "moderator": true, "admin": true}
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

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
