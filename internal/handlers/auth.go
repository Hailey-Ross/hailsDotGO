package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"pogo.hails.cc/internal/auth"
)

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
	h.render(w, r, "login", authData{})
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
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
		fail("Username and password are required.")
		return
	}

	var userID uint
	var hash string
	err := h.db.QueryRow(`SELECT id, password FROM users WHERE username = ?`, username).
		Scan(&userID, &hash)
	if err == sql.ErrNoRows {
		fail("Invalid username or password.")
		return
	}
	if err != nil {
		fail("Something went wrong. Please try again.")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		fail("Invalid username or password.")
		return
	}

	token, err := auth.CreateSession(h.db, userID)
	if err != nil {
		fail("Could not create session. Please try again.")
		return
	}

	http.SetCookie(w, sessionCookie(token, 30*24*time.Hour))

	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
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
				Error:              "This invite link is invalid or has already been used.",
				RegistrationClosed: true,
			})
			return
		}
		h.render(w, r, "register", authData{InviteToken: inviteToken})
		return
	}

	if !h.registrationOpen() {
		h.render(w, r, "register", authData{
			Error:              "Registration is currently closed.",
			RegistrationClosed: true,
		})
		return
	}
	h.render(w, r, "register", authData{})
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	inviteToken := strings.TrimSpace(r.FormValue("invite"))
	usingInvite := inviteToken != "" && h.validInvite(inviteToken)

	if !usingInvite && !h.registrationOpen() {
		http.Error(w, "registration is currently closed", http.StatusForbidden)
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
		fail("All fields are required.")
		return
	}
	if len(username) < 2 || len(username) > 32 {
		fail("Username must be 2-32 characters.")
		return
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			fail("Username may only contain letters, numbers, _ and -.")
			return
		}
	}
	if len(password) < 8 {
		fail("Password must be at least 8 characters.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fail("Something went wrong. Please try again.")
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
				fail("That username is already taken.")
			} else {
				fail("That email address is already registered.")
			}
			return
		}
		fail("Something went wrong. Please try again.")
		return
	}

	id, _ := result.LastInsertId()

	if usingInvite {
		h.db.Exec(
			`UPDATE invites SET used_by = ?, used_at = NOW() WHERE token = ?`,
			id, inviteToken,
		)
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
		auth.DeleteSession(h.db, c.Value)
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
		`SELECT COUNT(*) FROM invites WHERE token = ? AND used_at IS NULL AND expires_at > NOW()`,
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
