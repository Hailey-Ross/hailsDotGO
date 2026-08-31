package handlers

// The admin panel, for a JSON client.
//
// Of the 91 admin route registrations, 86 already decode JSON and encode JSON, so the mobile tree
// reaches those by aliasing the same handler behind a JSON role gate. This file is the rest:
//
//   - One bootstrap endpoint. The panel's own state (registration, store, page toggles, the build
//     number, live invites) reaches the template through adminData and exists in no endpoint at
//     all, so without this the app cannot draw the Settings tab.
//   - JSON siblings for the five handlers that take a form and answer with a rendered page.
//
// The five are NOT converted in place. The web form posts to them and expects HTML back, so
// changing them would break the panel in the browser. Everything they decide is shared instead,
// which is what the helpers below are for.
//
// SECURITY, and it is the whole reason RequireModAPI exists: almost no admin handler checks its
// own role. AdminChangeRole validates the role string, refuses the superadmin as a target, and
// writes, without ever asking who the caller is. The route wrapper is its entire authority check.
// Nothing in this tree may be registered bare; TestAdminMobileRoutesAreRoleGated holds that line.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
)

// pageSettingKeys maps the name a client uses to the site_settings key behind it.
//
// The client-facing names are exactly the ones GET /maintenance already answers with, so the app
// reads and writes the same vocabulary instead of learning that "iv" is read as "iv" and written
// as "page_iv_enabled". The database keys stay as they are: they are in a live table.
var pageSettingKeys = map[string]string{
	"raids":             "page_raids_enabled",
	"dps":               "page_dps_enabled",
	"pvp":               "page_pvp_enabled",
	"events":            "page_events_enabled",
	"iv":                "page_iv_enabled",
	"trainers":          "page_trainers_enabled",
	"trainer_directory": "section_trainer_directory_enabled",
	"raid_finder":       "section_raid_finder_enabled",
	"shinies":           "page_shinies_enabled",
	"translator_apps":   "section_translator_apps_enabled",
}

// siteSettingKeys is the same idea for the two toggles on the Settings tab.
var siteSettingKeys = map[string]string{
	"registration_open": "registration_open",
	"store_enabled":     "store_enabled",
}

// saveSiteSettings writes the given site_settings keys. Returns the first error, having attempted
// every key: a half saved form is better reported than abandoned midway.
func (h *Handlers) saveSiteSettings(values map[string]bool) error {
	var firstErr error
	for key, val := range values {
		v := "0"
		if val {
			v = "1"
		}
		if _, err := h.db.Exec(`
			INSERT INTO site_settings (setting_key, setting_value)
			VALUES (?, ?)
			ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`,
			key, v,
		); err != nil {
			log.Printf("admin: save setting %s: %v", key, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ── Bootstrap ────────────────────────────────────────────────────────────────

type mobileAdminViewer struct {
	IsMod        bool `json:"is_mod"`
	IsAdmin      bool `json:"is_admin"`
	IsSuperAdmin bool `json:"is_superadmin"`
}

type mobileAdminInvite struct {
	Token       string `json:"token"`
	Link        string `json:"link"`
	ExpiresAt   string `json:"expires_at"`
	GrantedRole string `json:"granted_role"`
	MaxUses     int    `json:"max_uses"`
	UseCount    int    `json:"use_count"`
}

type mobileAdminContext struct {
	RegistrationOpen bool                `json:"registration_open"`
	StoreEnabled     bool                `json:"store_enabled"`
	Maintenance      map[string]bool     `json:"maintenance"`
	MobileBuild      int                 `json:"mobile_build"`
	ActiveInvites    []mobileAdminInvite `json:"active_invites"`
	SuperadminUser   string              `json:"superadmin_user"`
	Viewer           mobileAdminViewer   `json:"viewer"`
}

// MobileAdminContext is everything the panel needs before it can draw anything.
//
// Viewer is the field that earns this endpoint its keep. Eight of the sixteen tabs are
// {{if .User.IsAdmin}} gated in admin.html, and the app has to make the same decision. It must not
// infer that from `role`, because the superadmin is matched by username against SUPERADMIN_USER
// and is not in the role column at all: reading `role` would show a superadmin whose row says
// "user" a moderator's panel.
//
// superadmin_user is already rendered into the page for every mod, so naming it here leaks nothing
// that a mod could not already read.
func (h *Handlers) MobileAdminContext(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	m := h.maintenanceSettings()
	out := mobileAdminContext{
		RegistrationOpen: h.registrationOpen(),
		StoreEnabled:     h.storeEnabled(),
		MobileBuild:      h.mobileBuildNumber(),
		SuperadminUser:   auth.SuperadminUser,
		ActiveInvites:    []mobileAdminInvite{},
		Maintenance: map[string]bool{
			"raids":             m.RaidsEnabled,
			"dps":               m.DPSEnabled,
			"pvp":               m.PVPEnabled,
			"events":            m.EventsEnabled,
			"iv":                m.IVEnabled,
			"trainers":          m.TrainersEnabled,
			"trainer_directory": m.TrainerDirectoryEnabled,
			"raid_finder":       m.RaidFinderEnabled,
			"shinies":           m.ShiniesEnabled,
			"translator_apps":   m.TranslatorAppsEnabled,
		},
		Viewer: mobileAdminViewer{
			IsMod:        u.IsMod(),
			IsAdmin:      u.IsAdmin(),
			IsSuperAdmin: u.IsSuperAdmin(),
		},
	}

	for _, inv := range h.loadActiveInvites(r) {
		out.ActiveInvites = append(out.ActiveInvites, mobileAdminInvite{
			Token:       inv.Token,
			Link:        inv.Link,
			ExpiresAt:   inv.ExpiresAt.UTC().Format(time.RFC3339),
			GrantedRole: inv.GrantedRole,
			MaxUses:     inv.MaxUses,
			UseCount:    inv.UseCount,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// ── Settings and page toggles ────────────────────────────────────────────────

// MobileAdminSettings writes the Settings tab's toggles.
//
// EVERY FIELD IS OPTIONAL AND AN OMITTED FIELD IS LEFT ALONE. That is not a style choice, it is
// the bug the form path had to grow a workaround for: AdminUpdateSettings rebuilt every key it
// knew about from the submitted form, and an unchecked checkbox sends nothing, so "this form does
// not own the key" and "the key is off" arrived identical. Two separate forms post there, so
// saving the registration toggle switched the store off. The fix was hidden _settings inputs
// naming the keys a form owns.
//
// A JSON body does not need that hack, because an absent key is genuinely absent. Hence *bool.
// Do not copy the _settings pattern across for symmetry: it would reintroduce the very shape it
// exists to work around.
func (h *Handlers) MobileAdminSettings(w http.ResponseWriter, r *http.Request) {
	h.mobileAdminToggleWrite(w, r, siteSettingKeys)
}

// MobileAdminPages writes the Pages tab's per-section toggles. Same partial-write contract as
// MobileAdminSettings above, and for the same reason.
func (h *Handlers) MobileAdminPages(w http.ResponseWriter, r *http.Request) {
	h.mobileAdminToggleWrite(w, r, pageSettingKeys)
}

// mobileAdminToggleWrite decodes a partial map of toggles and writes only what was sent.
//
// Decoding into map[string]*bool rather than a struct is what makes "absent" expressible: a struct
// of bools cannot tell an omitted field from a false one, which is the entire distinction here.
func (h *Handlers) mobileAdminToggleWrite(w http.ResponseWriter, r *http.Request, allowed map[string]string) {
	var body map[string]*bool
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	values, err := collectToggleWrites(body, allowed)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.saveSiteSettings(values); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	// Answer with the panel's whole state rather than {"ok":true}: the caller has
	// just changed what the tabs render, and this saves it a follow-up GET whose
	// result it would have to reconcile with what it thinks it wrote.
	h.MobileAdminContext(w, r)
}

// MobileAdminMobileBuild publishes the newest app build number by hand.
//
// Its own endpoint rather than a key inside admin settings, for the reason already written above
// AdminUpdateMobileBuild: sharing one would let any other settings form wipe the build number.
func (h *Handlers) MobileAdminMobileBuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Build *int `json:"build"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Build == nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if !validBuildNumber(*body.Build) {
		writeJSONError(w, h.t(r, "admin.mobile_build.invalid"), http.StatusBadRequest)
		return
	}
	if prev := h.mobileBuildNumber(); *body.Build < prev {
		// Lower is allowed, because pulling a bad build is legitimate, but it is
		// logged: the other way it happens is a typo.
		log.Printf("admin: published build number lowered from %d to %d", prev, *body.Build)
	}
	if err := h.setMobileBuildNumber(*body.Build); err != nil {
		log.Printf("admin: save mobile build number: %v", err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	h.MobileAdminContext(w, r)
}

// collectToggleWrites turns a partial client body into the site_settings keys to write.
//
// An absent name and an explicit null both mean "no opinion" and are skipped, which is the
// distinction the whole partial-write contract rests on. An unrecognised name is refused rather
// than ignored: silently dropping it would have a client believe it had switched something off.
//
// An empty result is an error too. A body of {} is far more likely to be a client bug than a
// deliberate request to change nothing, and answering 200 to it would hide that bug behind a
// response that looks like a successful save.
func collectToggleWrites(body map[string]*bool, allowed map[string]string) (map[string]bool, error) {
	values := make(map[string]bool, len(body))
	for name, val := range body {
		if val == nil {
			continue
		}
		key, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("unknown setting: %s", name)
		}
		values[key] = *val
	}
	if len(values) == 0 {
		return nil, errors.New("no settings to write")
	}
	return values, nil
}

// ── Invites ──────────────────────────────────────────────────────────────────

// inviteRules validates a requested invite and normalises it.
//
// Extracted from AdminGenerateInvite so the form path and the JSON path cannot disagree about who
// may be invited as what. Returns an i18n key when the request is refused.
//
// The two rules: a multi-use invite may only grant "user", and a staff invite must be single use.
// Both exist so an invite link that leaks cannot mint more than one moderator.
func inviteRules(grantedRole string, maxUses int) (role string, uses int, msgKey string) {
	validRoles := map[string]bool{"user": true, "tester": true, "moderator": true, "admin": true}
	role = grantedRole
	if !validRoles[role] {
		role = "user"
	}
	uses = maxUses
	if uses < 1 || uses > 50 {
		uses = 1
	}

	if uses > 1 && role != "user" {
		return role, uses, "error.invite_multiuse_role"
	}
	if (role == "moderator" || role == "admin") && uses != 1 {
		return role, uses, "error.invite_staff_single"
	}
	return role, uses, ""
}

// MobileAdminCreateInvite mints an invite link.
func (h *Handlers) MobileAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	var body struct {
		GrantedRole string `json:"granted_role"`
		MaxUses     int    `json:"max_uses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if body.MaxUses == 0 {
		body.MaxUses = 1
	}

	role, uses, msgKey := inviteRules(body.GrantedRole, body.MaxUses)
	if msgKey != "" {
		writeJSONError(w, h.t(r, msgKey), http.StatusBadRequest)
		return
	}

	token, err := generateInviteToken()
	if err != nil {
		writeJSONError(w, h.t(r, "error.invite_generate"), http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	if _, err := h.db.Exec(
		`INSERT INTO invites (token, created_by, granted_role, expires_at, max_uses) VALUES (?, ?, ?, ?, ?)`,
		token, u.ID, role, expiresAt, uses,
	); err != nil {
		log.Printf("admin: generate invite: %v", err)
		writeJSONError(w, h.t(r, "error.invite_generate"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mobileAdminInvite{
		Token:       token,
		Link:        inviteLink(r, token),
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
		GrantedRole: role,
		MaxUses:     uses,
		UseCount:    0,
	})
}

// MobileAdminCancelInvite revokes one. The web twin answers a 303 to /admin, which a JSON client
// cannot read.
func (h *Handlers) MobileAdminCancelInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM invites WHERE token = ?`, token); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
