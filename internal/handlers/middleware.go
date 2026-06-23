package handlers

import (
	"net/http"
	"strings"

	"pogo.hails.cc/internal/auth"
)

// resolveToken extracts the session token from the request.
// Checks Authorization: Bearer <token> first (mobile clients), then the hgo_session cookie (web).
func resolveToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handlers) currentUser(r *http.Request) *auth.User {
	token := resolveToken(r)
	if token == "" {
		return nil
	}
	u, _ := auth.GetSession(h.db, token)
	return u
}

func (h *Handlers) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.currentUser(r) == nil {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.currentUser(r) == nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireMod(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := h.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !u.IsMod() {
			http.Error(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := h.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !u.IsAdmin() {
			http.Error(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := h.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !u.IsSuperAdmin() {
			http.Error(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireTranslator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := h.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !u.IsTranslator() {
			http.Error(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) RequireAPIAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := h.currentUser(r)
		if u == nil || !u.HasAPIAccess() {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"API access required — contact a superadmin to request access"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
