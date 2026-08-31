package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"pogo.hails.cc/internal/auth"
)

// The companion app has no way to tell whether it is out of date, so the site
// publishes the newest build number and the app compares it against its own.
// The number lives in site_settings rather than in the binary or a static file:
// a build ships far more often than the site deploys, and bumping it must not
// require either a deploy or a code change.
const mobileBuildSettingKey = "mobile_build_number"

// buildNumberMax rejects a fat fingered entry. Build numbers climb by one; a
// value in the millions is a typo, and once stored it would tell every install
// it is out of date with no way for the app to tell the difference.
const buildNumberMax = 1000000

// parseBuildNumber accepts the digits a form field or a JSON body carries and
// reports whether they are a usable build number. Zero is rejected on purpose:
// it is the value an unset setting reads as, so accepting it as input would
// make "never configured" and "deliberately set to nothing" indistinguishable.
func parseBuildNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || !validBuildNumber(n) {
		return 0, false
	}
	return n, true
}

// validBuildNumber is the same range check for a value that arrived as a number
// already, so the JSON body and the admin form cannot drift apart on what counts.
func validBuildNumber(n int) bool {
	return n >= 1 && n <= buildNumberMax
}

// buildTokenOK compares an Authorization header against the configured secret in
// constant time. An empty secret never matches, so a server with no
// MOBILE_BUILD_TOKEN set cannot be written to by guessing the empty string.
//
// The "Bearer " prefix is required rather than merely trimmed, matching
// currentUserBearer in middleware.go, so one header shape is accepted here and
// everywhere else in the mobile tree.
func buildTokenOK(header, secret string) bool {
	if secret == "" || !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}

// mobileBuildNumber reads the published build number, answering 0 when the row
// has never been written. Zero is the safe default: the app compares its own
// build against this one, and nothing is ever newer than 0, so a missing row
// tells no one to update rather than telling everyone to.
func (h *Handlers) mobileBuildNumber() int {
	var v string
	h.db.QueryRow(`SELECT setting_value FROM site_settings WHERE setting_key = ?`,
		mobileBuildSettingKey).Scan(&v)
	n, _ := parseBuildNumber(v)
	return n
}

func (h *Handlers) setMobileBuildNumber(n int) error {
	_, err := h.db.Exec(`
		INSERT INTO site_settings (setting_key, setting_value)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
		mobileBuildSettingKey, strconv.Itoa(n),
	)
	return err
}

// MobileBuildGet answers the newest published build number as bare digits, so an
// update check is one request and one Atoi with nothing to unwrap.
//
// Public and unauthenticated: an app that has not logged in still needs to know
// it is stale, and the number is not a secret.
func (h *Handlers) MobileBuildGet(w http.ResponseWriter, r *http.Request) {
	// securityHeaders sets nosniff, so the correct type is mandatory rather than
	// advisory. See the assetLinks comment in internal/server/server.go.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// Never cached: a build going out has to be visible at once, and a stale
	// cached number is exactly the problem this endpoint exists to solve.
	w.Header().Set("Cache-Control", "no-store")
	// No trailing newline, so the body is exactly the number. Clients should
	// still trim before parsing.
	w.Write([]byte(strconv.Itoa(h.mobileBuildNumber())))
}

// MobileBuildSet publishes a new build number, guarded by a shared secret rather
// than a user session: it is called from the mobile repo's release step, which
// has no account and should not need one.
func (h *Handlers) MobileBuildSet(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("MOBILE_BUILD_TOKEN")
	if secret == "" {
		// 404 rather than 401: with no token configured there is nothing to
		// authenticate against, and answering 401 would advertise a write
		// endpoint that cannot be used.
		http.NotFound(w, r)
		return
	}
	if !buildTokenOK(r.Header.Get("Authorization"), secret) {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}

	var body struct {
		Build int `json:"build"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	n := body.Build
	if !validBuildNumber(n) {
		writeJSONError(w, "build must be a whole number between 1 and 1000000", http.StatusBadRequest)
		return
	}

	// A lower number is allowed, because pulling a bad build is legitimate, but
	// it is logged: the other way it happens is a typo, and that is worth being
	// able to find afterwards.
	if prev := h.mobileBuildNumber(); n < prev {
		log.Printf("mobile: published build number lowered from %d to %d", prev, n)
	}

	if err := h.setMobileBuildNumber(n); err != nil {
		log.Printf("mobile: save build number: %v", err)
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}

	// Echoes the stored value so the release step can confirm what landed.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strconv.Itoa(n)))
}

// AdminUpdateMobileBuild is the by hand path, for when a build ships without the
// release step having run.
//
// It is its own handler rather than another key inside AdminUpdateSettings
// because that one rebuilds every key it knows about from the submitted form, so
// a form that omits a field clears it. Sharing it would mean any other settings
// form could wipe the build number.
func (h *Handlers) AdminUpdateMobileBuild(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	msg := h.t(r, "admin.settings.saved")
	msgOK := true
	if n, ok := parseBuildNumber(r.FormValue("mobile_build")); !ok {
		msg = h.t(r, "admin.mobile_build.invalid")
		msgOK = false
	} else if err := h.setMobileBuildNumber(n); err != nil {
		log.Printf("admin: save mobile build number: %v", err)
		msg = h.t(r, "admin.settings.save_error")
		msgOK = false
	}

	h.render(w, r, "admin", adminData{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		Maintenance:      h.maintenanceSettings(),
		MobileBuild:      h.mobileBuildNumber(),
		Message:          msg,
		MessageOK:        msgOK,
		ActiveInvites:    h.loadActiveInvites(r),
		SuperadminUser:   auth.SuperadminUser,
	})
}
