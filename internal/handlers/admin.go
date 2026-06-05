package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type inviteRow struct {
	Link      string
	ExpiresAt time.Time
}

type adminData struct {
	RegistrationOpen bool
	Message          string
	MessageOK        bool
	InviteLink       string
	ActiveInvites    []inviteRow
}

func (h *Handlers) AdminPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		ActiveInvites:    h.loadActiveInvites(r),
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
		})
		return
	}

	link := inviteLink(r, token)
	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		InviteLink:       link,
		ActiveInvites:    h.loadActiveInvites(r),
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
