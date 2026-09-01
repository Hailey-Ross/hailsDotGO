package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"pogo.hails.cc/internal/mail"
)

const (
	purposeReset  = "reset"
	resetTokenTTL = time.Hour
)

// baseURL is the absolute origin used in emailed links. It is never derived
// from the request Host header, so a forged Host cannot poison an email link.
var baseURL = func() string {
	if v := strings.TrimRight(os.Getenv("BASE_URL"), "/"); v != "" {
		return v
	}
	return "https://pogo.hails.app"
}()

type resetPageData struct {
	Error      string
	Info       string
	Email      string
	Token      string
	TokenValid bool
	Success    bool
}

func newEmailToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	return raw, hashEmailToken(raw), nil
}

func hashEmailToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// createEmailToken issues a fresh token and supersedes any unused older
// tokens of the same purpose. Only the SHA-256 hash is stored.
func (h *Handlers) createEmailToken(userID uint, purpose string, ttl time.Duration) (string, error) {
	raw, hash, err := newEmailToken()
	if err != nil {
		return "", err
	}
	h.db.Exec(`UPDATE email_tokens SET used_at = NOW() WHERE user_id = ? AND purpose = ? AND used_at IS NULL`, userID, purpose)
	if _, err := h.db.Exec(
		`INSERT INTO email_tokens (user_id, token_hash, purpose, expires_at) VALUES (?, ?, ?, ?)`,
		userID, hash, purpose, time.Now().Add(ttl),
	); err != nil {
		return "", err
	}
	return raw, nil
}

func (h *Handlers) validEmailToken(raw, purpose string) bool {
	if len(raw) != 64 {
		return false
	}
	var n int
	err := h.db.QueryRow(
		`SELECT COUNT(*) FROM email_tokens WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > NOW()`,
		hashEmailToken(raw), purpose,
	).Scan(&n)
	return err == nil && n > 0
}

// consumeEmailToken marks the token used and returns its user. The used_at IS
// NULL guard on the UPDATE keeps consumption single-use even under a race.
func (h *Handlers) consumeEmailToken(raw, purpose string) (uint, bool) {
	if len(raw) != 64 {
		return 0, false
	}
	var id, userID uint
	err := h.db.QueryRow(
		`SELECT id, user_id FROM email_tokens WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > NOW()`,
		hashEmailToken(raw), purpose,
	).Scan(&id, &userID)
	if err != nil {
		return 0, false
	}
	res, err := h.db.Exec(`UPDATE email_tokens SET used_at = NOW() WHERE id = ? AND used_at IS NULL`, id)
	if err != nil {
		return 0, false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, false
	}
	return userID, true
}

func (h *Handlers) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "forgot_password", resetPageData{})
}

// issuePasswordReset emails a reset link if the address belongs to an enabled
// account, and reports nothing about whether it did.
//
// That silence is the point. The caller must answer identically for a hit and a
// miss, or the endpoint becomes a way to test whether somebody has an account
// here. The send happens in a goroutine so the extra work on a hit cannot be
// timed either.
func (h *Handlers) issuePasswordReset(email string) {
	// Opportunistic cleanup of long-expired tokens.
	h.db.Exec(`DELETE FROM email_tokens WHERE expires_at < NOW() - INTERVAL 7 DAY`)

	var userID uint
	var username string
	var disabled bool
	err := h.db.QueryRow(`SELECT id, username, disabled FROM users WHERE email = ? AND deleted_at IS NULL`, email).
		Scan(&userID, &username, &disabled)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("forgot password: lookup: %v", err)
		}
		return
	}
	if disabled {
		return
	}

	raw, terr := h.createEmailToken(userID, purposeReset, resetTokenTTL)
	if terr != nil {
		log.Printf("forgot password: create token for user %d: %v", userID, terr)
		return
	}
	link := baseURL + "/reset-password?token=" + raw
	subject, htmlBody, textBody := mail.PasswordResetEmail(username, link)
	go func() {
		if serr := h.mailer.Send(email, subject, htmlBody, textBody); serr != nil {
			log.Printf("forgot password: send to user %d: %v", userID, serr)
		}
	}()
}

func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		h.render(w, r, "forgot_password", resetPageData{Error: h.t(r, "error.required_fields")})
		return
	}

	h.issuePasswordReset(email)

	// Always the same response, whether or not the email matched an account.
	h.render(w, r, "forgot_password", resetPageData{Info: h.t(r, "forgot.sent")})
}

func (h *Handlers) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !h.validEmailToken(token, purposeReset) {
		h.render(w, r, "reset_password", resetPageData{})
		return
	}
	h.render(w, r, "reset_password", resetPageData{TokenValid: true, Token: token})
}

func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	token := r.FormValue("token")
	password := r.FormValue("password")

	if len(password) < 8 {
		if !h.validEmailToken(token, purposeReset) {
			h.render(w, r, "reset_password", resetPageData{})
			return
		}
		h.render(w, r, "reset_password", resetPageData{
			TokenValid: true, Token: token, Error: h.t(r, "error.password_length"),
		})
		return
	}

	userID, ok := h.consumeEmailToken(token, purposeReset)
	if !ok {
		h.render(w, r, "reset_password", resetPageData{})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		log.Printf("reset password: bcrypt for user %d: %v", userID, err)
		h.render(w, r, "reset_password", resetPageData{Error: h.t(r, "error.server")})
		return
	}
	if _, err := h.db.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), userID); err != nil {
		log.Printf("reset password: update for user %d: %v", userID, err)
		h.render(w, r, "reset_password", resetPageData{Error: h.t(r, "error.server")})
		return
	}

	// Log out every existing session for the account.
	if _, err := h.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		log.Printf("reset password: clear sessions for user %d: %v", userID, err)
	}
	// Retire any other outstanding reset tokens; a completed reset must leave
	// no live recovery path behind.
	if _, err := h.db.Exec(`UPDATE email_tokens SET used_at = NOW() WHERE user_id = ? AND purpose = ? AND used_at IS NULL`, userID, purposeReset); err != nil {
		log.Printf("reset password: retire tokens for user %d: %v", userID, err)
	}

	h.render(w, r, "reset_password", resetPageData{Success: true})
}
