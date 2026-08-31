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

// currentUserBearer resolves the session from the Authorization header ONLY, never
// from the cookie.
//
// The /api/mobile/v1 tree is deliberately outside CSRF protection, on the stated
// grounds that Bearer tokens are not CSRF-vulnerable. That was only half true:
// resolveToken falls back to the session cookie, so a browser carrying a live
// session would authenticate against those routes on a cross-site request with no
// token of any kind. The only thing standing in the way was SameSite, and SameSite
// is scoped to the registrable domain, so any sibling host under the same site
// counted as same-site and defeated it.
//
// Requiring the header closes it properly: cross-origin JavaScript cannot set
// Authorization without a CORS preflight, and the app sends no CORS headers at all,
// so the preflight fails. This also matches what the API is documented to do on the
// credits page, where every authenticated mobile endpoint is listed as Bearer.
func (h *Handlers) currentUserBearer(r *http.Request) *auth.User {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return nil
	}
	token := strings.TrimPrefix(hdr, "Bearer ")
	if token == "" {
		return nil
	}
	u, _ := auth.GetSession(h.db, token)
	return u
}

// requireUserAPI re-resolves the caller inside a handler that already sits behind
// auth middleware, and answers 401 rather than panicking when the session is gone.
//
// The middleware check and the handler use are two independent session lookups,
// and on the mobile tree they are not even the same function: the group gates on
// currentUserBearer, then the handler re-resolves through currentUser. A session
// that expires or is deleted between the two handed the handler a nil user, and
// the first field access panicked where a 401 was wanted.
func (h *Handlers) requireUserAPI(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return nil, false
	}
	return u, true
}

// requireUserPage is requireUserAPI for handlers that render HTML or redirect,
// where a JSON 401 would be the wrong answer. It sends the caller to the login
// page exactly as RequireAuth does.
func (h *Handlers) requireUserPage(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return nil, false
	}
	return u, true
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

// ── JSON role gates, for the /api/mobile/v1 tree ──────────────────────────────
//
// The four wrappers above cannot be used from a Bearer client. Both of their
// failure paths answer in a shape a JSON client cannot read: no session is a 303
// redirect to /login, and the wrong role is an http.Error, which is text/plain.
// An app parsing either as JSON fails with something that names neither problem.
//
// That is only the cosmetic half, and the smaller one. The real reason these
// exist is that ALMOST NO ADMIN HANDLER CHECKS ITS OWN ROLE. AdminChangeRole
// (admin.go) parses an id, validates the role string, refuses the superadmin as a
// TARGET, and writes. Nothing in it asks who the caller is; the route wrapper is
// the whole of its authority check. mayActOn protects the target, not the
// caller's rank, and only some handlers call it at all.
//
// So an admin handler registered bare inside the mobile group's authenticated
// subtree is reachable by any signed-in trainer, because that group's middleware
// proves only that the session is valid. Registering AdminChangeRole bare would
// be a self-service promotion to admin. Every admin route inside /api/mobile/v1
// goes through one of the three wrappers below, and
// TestAdminMobileRoutesAreRoleGated in internal/server holds that line.

// roleGateStatus decides what a role gate should answer: 0 to let the request
// through, or the HTTP status to refuse it with.
//
// Split out from the wrappers so the decision can be tested against fabricated
// users. h.currentUser needs a database, so a wrapper tested end to end can only
// ever exercise its no-session path, which is the half that was never in doubt.
func roleGateStatus(u *auth.User, allow func(*auth.User) bool) int {
	if u == nil {
		return http.StatusUnauthorized
	}
	if !allow(u) {
		return http.StatusForbidden
	}
	return 0
}

// roleGateAPI is the shared body of the three wrappers below.
func (h *Handlers) roleGateAPI(allow func(*auth.User) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch roleGateStatus(h.currentUser(r), allow) {
		case http.StatusUnauthorized:
			writeJSONError(w, "authentication required", http.StatusUnauthorized)
		case http.StatusForbidden:
			writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusForbidden)
		default:
			next(w, r)
		}
	}
}

// RequireModAPI is RequireMod for a JSON client: mods, admins and the superadmin.
func (h *Handlers) RequireModAPI(next http.HandlerFunc) http.HandlerFunc {
	return h.roleGateAPI((*auth.User).IsMod, next)
}

// RequireAdminAPI is RequireAdmin for a JSON client: admins and the superadmin.
func (h *Handlers) RequireAdminAPI(next http.HandlerFunc) http.HandlerFunc {
	return h.roleGateAPI((*auth.User).IsAdmin, next)
}

// RequireSuperAdminAPI is RequireSuperAdmin for a JSON client. The superadmin is
// matched by username against SUPERADMIN_USER, not by the role column, so this
// survives anything the panel can do to a role.
func (h *Handlers) RequireSuperAdminAPI(next http.HandlerFunc) http.HandlerFunc {
	return h.roleGateAPI((*auth.User).IsSuperAdmin, next)
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
