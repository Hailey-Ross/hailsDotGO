package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"pogo.hails.cc/internal/auth"
)

// MobileAuthMiddleware returns a chi-compatible middleware that enforces auth via
// Bearer token or session cookie, returning JSON 401 on failure (no redirect).
func (h *Handlers) MobileAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bearer only: this whole tree is CSRF-exempt, so accepting the session
			// cookie here would make every route in it cross-site requestable.
			// See currentUserBearer in middleware.go.
			if h.currentUserBearer(r) == nil {
				writeJSONError(w, "authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type mobileUserResponse struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Lang         string `json:"lang"`
	SpecialRank  string `json:"special_rank"`
	TrainerLevel int    `json:"trainer_level"`
}

func userToMobileResponse(u *auth.User) mobileUserResponse {
	return mobileUserResponse{
		ID:          u.ID,
		Username:    u.Username,
		Role:        u.Role,
		Lang:        u.Lang,
		SpecialRank: u.SpecialRank,
	}
}

func (h *Handlers) MobileLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		writeJSONError(w, "username and password required", http.StatusBadRequest)
		return
	}

	var userID uint
	var hash string
	var disabled bool
	err := h.db.QueryRow(
		`SELECT id, password, disabled FROM users WHERE username = ?`, body.Username,
	).Scan(&userID, &hash, &disabled)
	if err == sql.ErrNoRows {
		writeJSONError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		writeJSONError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if disabled {
		writeJSONError(w, "account disabled", http.StatusForbidden)
		return
	}

	token, err := auth.CreateSession(h.db, userID)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}

	u, err := auth.GetSession(h.db, token)
	if err != nil || u == nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}

	userResp := userToMobileResponse(u)
	h.db.QueryRow(`SELECT COALESCE(trainer_level,0) FROM users WHERE id = ?`, u.ID).Scan(&userResp.TrainerLevel)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":      token,
		"expires_at": time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339),
		"user":       userResp,
	})
}

func (h *Handlers) MobileLogout(w http.ResponseWriter, r *http.Request) {
	if token := resolveToken(r); token != "" {
		auth.DeleteSession(h.db, token)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) MobileMe(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	resp := userToMobileResponse(u)
	h.db.QueryRow(`SELECT COALESCE(trainer_level,0) FROM users WHERE id = ?`, u.ID).Scan(&resp.TrainerLevel)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) MobilePutProfile(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var body struct {
		TrainerLevel *int `json:"trainer_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.TrainerLevel != nil {
		lvl := *body.TrainerLevel
		if lvl < 0 || lvl > 80 {
			writeJSONError(w, "trainer_level must be 0-80", http.StatusBadRequest)
			return
		}
		if _, err := h.db.Exec(`UPDATE users SET trainer_level = ? WHERE id = ?`, lvl, u.ID); err != nil {
			writeJSONError(w, "server error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) RegisterPushToken(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var body struct {
		Platform   string `json:"platform"`
		PushToken  string `json:"push_token"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.Platform != "android" && body.Platform != "ios" {
		writeJSONError(w, "platform must be android or ios", http.StatusBadRequest)
		return
	}
	if body.PushToken == "" {
		writeJSONError(w, "push_token required", http.StatusBadRequest)
		return
	}
	_, err := h.db.Exec(`
		INSERT INTO mobile_device_tokens (user_id, platform, push_token, device_name)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE user_id = VALUES(user_id), platform = VALUES(platform),
		                        device_name = VALUES(device_name), updated_at = NOW()`,
		u.ID, body.Platform, body.PushToken, body.DeviceName,
	)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UnregisterPushToken(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var body struct {
		PushToken string `json:"push_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.PushToken == "" {
		writeJSONError(w, "push_token required", http.StatusBadRequest)
		return
	}
	h.db.Exec(`DELETE FROM mobile_device_tokens WHERE push_token = ? AND user_id = ?`, body.PushToken, u.ID)
	w.WriteHeader(http.StatusNoContent)
}

// MobileMaintenance reports which pages and sections are currently enabled, so
// the app can grey out what the site has switched off instead of offering a
// screen that will answer 503 or simply come back empty.
//
// The same flags drive the website's own navigation; they were previously only
// rendered into templates, leaving a mobile client with no way to see them.
// Store sits in its own setting rather than PageMaintenance, so it is folded in
// here to save the app a second call.
func (h *Handlers) MobileMaintenance(w http.ResponseWriter, r *http.Request) {
	m := h.maintenanceSettings()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{
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
		"store":             h.storeEnabled(),
	})
}
