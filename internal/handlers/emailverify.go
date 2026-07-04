package handlers

import (
	"log"
	"net/http"
	"time"

	"pogo.hails.cc/internal/mail"
)

const (
	purposeVerify  = "verify"
	verifyTokenTTL = 24 * time.Hour
	resendCooldown = 5 * time.Minute
)

type verifyPageData struct {
	Success bool // token consumed, address confirmed
	Sent    bool // resend accepted (or cooldown active; same page either way)
}

// sendVerificationEmail issues a fresh verify token and emails the link.
// Failures only log; signup and resend must never fail because of mail.
func (h *Handlers) sendVerificationEmail(userID uint, username, email string) {
	raw, err := h.createEmailToken(userID, purposeVerify, verifyTokenTTL)
	if err != nil {
		log.Printf("verify email: create token for user %d: %v", userID, err)
		return
	}
	link := baseURL + "/verify-email?token=" + raw
	subject, htmlBody, textBody := mail.VerificationEmail(username, link)
	go func() {
		if err := h.mailer.Send(email, subject, htmlBody, textBody); err != nil {
			log.Printf("verify email: send to user %d: %v", userID, err)
		}
	}()
}

func (h *Handlers) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.consumeEmailToken(r.URL.Query().Get("token"), purposeVerify)
	if !ok {
		// Mail scanners prefetch emailed links and consume the token before
		// the user clicks. If the viewer is already verified the job is done;
		// show success rather than a scary error for a dead link.
		if u := h.currentUser(r); u != nil && u.EmailVerified {
			h.render(w, r, "verify_email", verifyPageData{Success: true})
			return
		}
		h.render(w, r, "verify_email", verifyPageData{})
		return
	}
	if _, err := h.db.Exec(
		`UPDATE users SET email_verified_at = NOW() WHERE id = ? AND email_verified_at IS NULL`,
		userID,
	); err != nil {
		log.Printf("verify email: mark user %d: %v", userID, err)
		h.render(w, r, "verify_email", verifyPageData{})
		return
	}
	h.render(w, r, "verify_email", verifyPageData{Success: true})
}

func (h *Handlers) ResendVerification(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u.EmailVerified {
		h.render(w, r, "verify_email", verifyPageData{Success: true})
		return
	}
	// Per-user cooldown on top of the IP rate limit: if a fresh token already
	// exists, show the same "sent" page without issuing another email.
	var recent int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM email_tokens
		 WHERE user_id = ? AND purpose = ? AND used_at IS NULL AND created_at > ?`,
		u.ID, purposeVerify, time.Now().Add(-resendCooldown),
	).Scan(&recent); err == nil && recent > 0 {
		h.render(w, r, "verify_email", verifyPageData{Sent: true})
		return
	}
	h.sendVerificationEmail(u.ID, u.Username, u.Email)
	h.render(w, r, "verify_email", verifyPageData{Sent: true})
}
