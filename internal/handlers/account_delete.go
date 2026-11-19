package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Self service account deletion (2026-11-18, for Google Play and for the site). A trainer
// deletes their own account from Settings, from the app, or by email (flexdex@hails.app, for
// someone who can no longer sign in); /delete-account explains all three without a sign in.
//
// Owner decisions: it uses the 90 day mark-then-purge hold, and signing in again within the
// hold cancels it; open trades and connect requests end as a block would end them; moderation
// records survive the purge without the name (reports already do, through ON DELETE SET NULL).

// passwordMatches checks a trainer's password against the stored hash.
func (h *Handlers) passwordMatches(userID uint, password string) bool {
	var hash string
	if err := h.db.QueryRow(`SELECT password FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// MobileAccountDelete is POST /api/mobile/v1/account/delete, Bearer auth, body {"password"}.
// 200 {"ok":true,"purge_after":"<RFC 3339>"}; 403 code wrong_password or not_allowed; 429 code
// slow_down (the route's limiter). Every session and push token of the account ends at once,
// including the token that made this call.
func (h *Handlers) MobileAccountDelete(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		writeJSONError(w, h.t(r, "account.delete.password_required"), http.StatusBadRequest)
		return
	}
	if !h.passwordMatches(u.ID, body.Password) {
		writeJSONErrorCode(w, h.t(r, "account.delete.wrong_password"), "wrong_password", http.StatusForbidden)
		return
	}
	purgeAfter, err := h.selfDeleteAccount(u.ID, u.Username, time.Now())
	if errors.Is(err, errSelfDeleteNotAllowed) {
		writeJSONErrorCode(w, h.t(r, "account.delete.not_allowed"), "not_allowed", http.StatusForbidden)
		return
	}
	if err != nil {
		log.Printf("mobile account delete %d: %v", u.ID, err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "purge_after": purgeAfter.UTC().Format(time.RFC3339)})
}

// SettingsDeleteAccount is the Settings form (POST /settings/delete-account): the password and a
// confirmation tick. On success the trainer is signed out and lands on /delete-account?done=1.
func (h *Handlers) SettingsDeleteAccount(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/settings?deletion=error#delete-account", http.StatusSeeOther)
		return
	}
	if r.FormValue("confirm") != "1" {
		http.Redirect(w, r, "/settings?deletion=confirm#delete-account", http.StatusSeeOther)
		return
	}
	if !h.passwordMatches(u.ID, r.FormValue("password")) {
		http.Redirect(w, r, "/settings?deletion=wrong_password#delete-account", http.StatusSeeOther)
		return
	}
	if _, err := h.selfDeleteAccount(u.ID, u.Username, time.Now()); err != nil {
		if !errors.Is(err, errSelfDeleteNotAllowed) {
			log.Printf("settings account delete %d: %v", u.ID, err)
		}
		http.Redirect(w, r, "/settings?deletion=error#delete-account", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, sessionCookie("", -1))
	http.Redirect(w, r, "/delete-account?done=1", http.StatusSeeOther)
}

// DeleteAccountPage is the public page Google Play lists: how to delete an account from the
// site, from the app, or by email, what is deleted, what is kept, and the 90 day hold.
func (h *Handlers) DeleteAccountPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "delete_account", struct{ Done bool }{r.URL.Query().Get("done") == "1"})
}
