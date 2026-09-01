package handlers

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	netmail "net/mail"
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
	err := h.db.QueryRow(`SELECT id, password, disabled, COALESCE(disabled_reason,'') FROM users WHERE username = ? AND deleted_at IS NULL`, username).
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
	if !safeNextPath(next) {
		next = "/shinies"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// safeNextPath reports whether a ?next= value is a same-site path worth redirecting
// to after login.
//
// The rooted-path check alone is not enough. A browser resolves "/\evil.com" as a
// scheme-relative URL exactly like "//evil.com" (WHATWG URL treats the backslash as
// a slash), and it strips tab and newline characters before parsing, so "/\t/evil.com"
// collapses to "//evil.com" too. url.Parse does none of that: it reports an empty
// Scheme and Host for all of them, which is how the previous check passed them
// straight through to an off-site redirect.
func safeNextPath(next string) bool {
	if next == "" || next[0] != '/' {
		return false
	}
	if strings.HasPrefix(next, "//") {
		return false
	}
	for _, c := range next {
		if c == '\\' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	u, err := url.Parse(next)
	return err == nil && u.Scheme == "" && u.Host == ""
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

// registerAccount creates an account, applying every rule the registration form
// applies. The web form and the mobile endpoint both go through it, so neither can
// grant something the other would refuse.
//
// The invite handling is the reason this is shared rather than reimplemented: a
// moderator or admin invite must land in pending_role, held until an admin
// confirms it, while a tester invite sets the role outright. A second copy of that
// switch getting it wrong would grant staff on signup.
//
// Returns the new user id, an i18n key naming the rule that refused the
// registration, or an error for a server failure.
func (h *Handlers) registerAccount(username, email, password, inviteToken string) (uint, string, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	inviteToken = strings.TrimSpace(inviteToken)

	// An invite that is expired or used up is ignored rather than rejected: the
	// caller simply falls through to the ordinary open-registration check.
	usingInvite := inviteToken != "" && h.validInvite(inviteToken)
	if !usingInvite && !h.registrationOpen() {
		return 0, "error.reg_closed", nil
	}

	if username == "" || email == "" || password == "" {
		return 0, "error.all_fields", nil
	}
	if len(username) < 2 || len(username) > 32 {
		return 0, "error.username_length", nil
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			return 0, "error.username_chars", nil
		}
	}
	if len(password) < 8 {
		return 0, "error.password_length", nil
	}
	if len(email) > 254 {
		return 0, "error.email_invalid", nil
	}
	// ParseAddress accepts "Name <a@b>" forms; requiring Address == email
	// limits input to a bare address.
	if addr, err := netmail.ParseAddress(email); err != nil || addr.Address != email {
		return 0, "error.email_invalid", nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return 0, "", err
	}

	result, err := h.db.Exec(
		`INSERT INTO users (username, email, password) VALUES (?, ?, ?)`,
		username, email, string(hash),
	)
	if err != nil {
		// A duplicate key has to say which field collided, or the caller cannot
		// tell the user anything useful. The index name is the only signal MySQL
		// gives.
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			if strings.Contains(mysqlErr.Message, "uk_username") {
				return 0, "error.username_taken", nil
			}
			return 0, "error.email_taken", nil
		}
		return 0, "", err
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

	// Soft verify: confirmation email only; nothing is gated on it.
	h.sendVerificationEmail(uint(id), username, email)

	return uint(id), "", nil
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	inviteToken := strings.TrimSpace(r.FormValue("invite"))
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	id, key, err := h.registerAccount(username, email, password, inviteToken)
	if key == "error.reg_closed" {
		http.Error(w, h.t(r, key), http.StatusForbidden)
		return
	}
	if key != "" || err != nil {
		msg := h.t(r, "error.server")
		if key != "" {
			msg = h.t(r, key)
		}
		h.render(w, r, "register", authData{
			Error:       msg,
			Form:        map[string]string{"username": username, "email": email},
			InviteToken: inviteToken,
		})
		return
	}

	token, err := auth.CreateSession(h.db, id)
	if err != nil {
		// The account exists; the visitor just is not logged in yet.
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
