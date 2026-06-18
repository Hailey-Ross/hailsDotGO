package handlers

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"pogo.hails.cc/internal/auth"
)

const bcryptCost = 13

type authData struct {
	Error              string
	Form               map[string]string
	RegistrationClosed bool
	InviteToken        string
}

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	if h.currentUser(r) != nil {
		http.Redirect(w, r, "/shinies", http.StatusSeeOther)
		return
	}
	next := r.URL.Query().Get("next")
	h.render(w, r, "login", authData{Form: map[string]string{"next": next}})
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	fail := func(msg string) {
		h.render(w, r, "login", authData{
			Error: msg,
			Form:  map[string]string{"username": username},
		})
	}

	if username == "" || password == "" {
		fail(h.t(r, "error.required_fields"))
		return
	}

	var userID uint
	var hash string
	var disabled bool
	var disabledReason string
	err := h.db.QueryRow(`SELECT id, password, disabled, COALESCE(disabled_reason,'') FROM users WHERE username = ?`, username).
		Scan(&userID, &hash, &disabled, &disabledReason)
	if err == sql.ErrNoRows {
		fail(h.t(r, "error.invalid_credentials"))
		return
	}
	if err != nil {
		fail(h.t(r, "error.server"))
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		fail(h.t(r, "error.invalid_credentials"))
		return
	}

	if disabled {
		msg := h.t(r, "error.account_disabled")
		if disabledReason != "" {
			msg += " " + h.t(r, "error.reason_label") + " " + disabledReason
		}
		fail(msg)
		return
	}

	token, err := auth.CreateSession(h.db, userID)
	if err != nil {
		fail(h.t(r, "error.session"))
		return
	}

	http.SetCookie(w, sessionCookie(token, 30*24*time.Hour))

	next := r.FormValue("next")
	if u, err := url.Parse(next); next == "" || err != nil || u.Host != "" || u.Scheme != "" || strings.HasPrefix(next, "//") {
		next = "/shinies"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handlers) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if h.currentUser(r) != nil {
		http.Redirect(w, r, "/shinies", http.StatusSeeOther)
		return
	}

	inviteToken := r.URL.Query().Get("invite")
	if inviteToken != "" {
		if !h.validInvite(inviteToken) {
			h.render(w, r, "register", authData{
				Error:              h.t(r, "error.invite_invalid"),
				RegistrationClosed: true,
			})
			return
		}
		h.render(w, r, "register", authData{InviteToken: inviteToken})
		return
	}

	if !h.registrationOpen() {
		h.render(w, r, "register", authData{
			Error:              h.t(r, "error.reg_closed"),
			RegistrationClosed: true,
		})
		return
	}
	h.render(w, r, "register", authData{})
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	inviteToken := strings.TrimSpace(r.FormValue("invite"))
	usingInvite := inviteToken != "" && h.validInvite(inviteToken)

	if !usingInvite && !h.registrationOpen() {
		http.Error(w, h.t(r, "error.reg_closed"), http.StatusForbidden)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	fail := func(msg string) {
		h.render(w, r, "register", authData{
			Error:       msg,
			Form:        map[string]string{"username": username, "email": email},
			InviteToken: inviteToken,
		})
	}

	if username == "" || email == "" || password == "" {
		fail(h.t(r, "error.all_fields"))
		return
	}
	if len(username) < 2 || len(username) > 32 {
		fail(h.t(r, "error.username_length"))
		return
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			fail(h.t(r, "error.username_chars"))
			return
		}
	}
	if len(password) < 8 {
		fail(h.t(r, "error.password_length"))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		fail(h.t(r, "error.server"))
		return
	}

	result, err := h.db.Exec(
		`INSERT INTO users (username, email, password) VALUES (?, ?, ?)`,
		username, email, string(hash),
	)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			if strings.Contains(mysqlErr.Message, "uk_username") {
				fail(h.t(r, "error.username_taken"))
			} else {
				fail(h.t(r, "error.email_taken"))
			}
			return
		}
		fail(h.t(r, "error.server"))
		return
	}

	id, _ := result.LastInsertId()

	if usingInvite {
		var grantedRole string
		h.db.QueryRow(`SELECT granted_role FROM invites WHERE token = ?`, inviteToken).Scan(&grantedRole)
		h.db.Exec(`UPDATE invites SET use_count = use_count + 1 WHERE token = ?`, inviteToken)

		switch grantedRole {
		case "moderator", "admin":
			// Staff roles are held pending until confirmed by an admin.
			h.db.Exec(`UPDATE users SET pending_role = ? WHERE id = ?`, grantedRole, id)
		case "tester":
			h.db.Exec(`UPDATE users SET role = 'tester' WHERE id = ?`, id)
		}
	}

	token, err := auth.CreateSession(h.db, uint(id))
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	http.SetCookie(w, sessionCookie(token, 30*24*time.Hour))
	http.Redirect(w, r, "/shinies", http.StatusSeeOther)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if err := auth.DeleteSession(h.db, c.Value); err != nil {
			log.Printf("logout: delete session: %v", err)
		}
	}
	http.SetCookie(w, sessionCookie("", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handlers) registrationOpen() bool {
	var val string
	err := h.db.QueryRow(
		`SELECT setting_value FROM site_settings WHERE setting_key = 'registration_open'`,
	).Scan(&val)
	if err != nil {
		return true
	}
	return val == "1"
}

func (h *Handlers) validInvite(token string) bool {
	if token == "" {
		return false
	}
	var count int
	err := h.db.QueryRow(
		`SELECT COUNT(*) FROM invites WHERE token = ? AND use_count < max_uses AND expires_at > NOW()`,
		token,
	).Scan(&count)
	return err == nil && count > 0
}

func sessionCookie(value string, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     auth.CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	if ttl < 0 {
		c.Expires = time.Unix(0, 0)
		c.MaxAge = -1
	} else {
		c.Expires = time.Now().Add(ttl)
		c.MaxAge = int(ttl.Seconds())
	}
	return c
}
