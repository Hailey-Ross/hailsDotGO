// Package mail sends transactional email through the Resend HTTP API.
// When RESEND_API_KEY is unset the mailer is disabled: Send logs the skip and
// returns nil, so local dev and tests never attempt a network call.
package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type Mailer struct {
	apiKey string
	from   string
	client *http.Client
	api    string
}

func New() *Mailer {
	from := os.Getenv("MAIL_FROM")
	if from == "" {
		from = "hailsDotGO <noreply@hails.cc>"
	}
	return &Mailer{
		apiKey: os.Getenv("RESEND_API_KEY"),
		from:   from,
		client: &http.Client{Timeout: 10 * time.Second},
		api:    "https://api.resend.com/emails",
	}
}

func (m *Mailer) Enabled() bool { return m.apiKey != "" }

// Send delivers one message. Callers on the request path should invoke it in a
// goroutine and log the error; a slow or down Resend must never block a request.
func (m *Mailer) Send(to, subject, htmlBody, textBody string) error {
	if !m.Enabled() {
		log.Printf("mail: disabled (RESEND_API_KEY unset), skipping %q to %s", subject, to)
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"from":    m.from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
		"text":    textBody,
	})
	if err != nil {
		return fmt.Errorf("mail: marshal: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, m.api, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mail: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("mail: resend returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// VerificationEmail builds the address confirmation message sent at signup
// and on resend. link must be an absolute https URL. The 24 hour wording
// matches the verify token TTL.
func VerificationEmail(username, link string) (subject, htmlBody, textBody string) {
	subject = "Confirm your hailsDotGO email address"
	u := html.EscapeString(username)
	l := html.EscapeString(link)
	htmlBody = fmt.Sprintf(`<div style="font-family:sans-serif;max-width:480px;margin:0 auto">
<h2 style="color:#333">Confirm your email</h2>
<p>Hi %s,</p>
<p>Welcome to hailsDotGO! Click the button below within 24 hours to confirm this email address belongs to you:</p>
<p style="text-align:center;margin:24px 0"><a href="%s" style="background:#5b9cf6;color:#fff;padding:10px 22px;border-radius:6px;text-decoration:none;display:inline-block">Confirm email</a></p>
<p style="font-size:13px;color:#666">Or copy this link into your browser:<br>%s</p>
<p style="font-size:13px;color:#666">If you did not create a hailsDotGO account, you can ignore this email.</p>
</div>`, u, l, l)
	textBody = fmt.Sprintf("Hi %s,\n\nWelcome to hailsDotGO! Open this link within 24 hours to confirm this email address belongs to you:\n\n%s\n\nIf you did not create a hailsDotGO account, you can ignore this email.\n", username, link)
	return subject, htmlBody, textBody
}

// PasswordResetEmail builds the password reset message. link must be an
// absolute https URL. The one hour wording matches the reset token TTL.
func PasswordResetEmail(username, link string) (subject, htmlBody, textBody string) {
	subject = "Reset your hailsDotGO password"
	u := html.EscapeString(username)
	l := html.EscapeString(link)
	htmlBody = fmt.Sprintf(`<div style="font-family:sans-serif;max-width:480px;margin:0 auto">
<h2 style="color:#333">Password reset</h2>
<p>Hi %s,</p>
<p>Someone requested a password reset for your hailsDotGO account. If this was you, click the button below within one hour:</p>
<p style="text-align:center;margin:24px 0"><a href="%s" style="background:#5b9cf6;color:#fff;padding:10px 22px;border-radius:6px;text-decoration:none;display:inline-block">Reset password</a></p>
<p style="font-size:13px;color:#666">Or copy this link into your browser:<br>%s</p>
<p style="font-size:13px;color:#666">If you did not request this, you can ignore this email. Your password is unchanged.</p>
</div>`, u, l, l)
	textBody = fmt.Sprintf("Hi %s,\n\nSomeone requested a password reset for your hailsDotGO account. If this was you, open this link within one hour:\n\n%s\n\nIf you did not request this, you can ignore this email. Your password is unchanged.\n", username, link)
	return subject, htmlBody, textBody
}
