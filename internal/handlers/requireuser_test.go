package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pogo.hails.cc/internal/auth"
)

// A request carrying no Bearer header and no session cookie never reaches the
// database, so both helpers can be exercised on a bare Handlers value. That is
// exactly the state a handler sees when the session dies between the middleware
// check and the handler's own lookup, which used to panic on the first field
// access.
func TestRequireUserAPIAnswers401(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/player-reports", nil)

	u, ok := h.requireUserAPI(w, r)
	if ok || u != nil {
		t.Fatalf("requireUserAPI accepted a request with no session: ok=%v u=%v", ok, u)
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, w.Body.String())
	}
	if body["error"] == "" {
		t.Errorf("expected an error field, got %s", w.Body.String())
	}
}

func TestRequireUserPageRedirectsToLogin(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/friends", nil)

	u, ok := h.requireUserPage(w, r)
	if ok || u != nil {
		t.Fatalf("requireUserPage accepted a request with no session: ok=%v u=%v", ok, u)
	}
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/login?next=/friends" {
		t.Errorf("Location = %q, want /login?next=/friends", loc)
	}
}

// ── JSON role gates ───────────────────────────────────────────────────────────

// The decision is tested against fabricated users rather than through the
// wrappers, because h.currentUser needs a database: a wrapper driven end to end
// can only reach its no-session path. The wiring of that path, and the JSON shape
// both refusals answer with, are covered separately below.
func TestRoleGateStatus(t *testing.T) {
	auth.SuperadminUser = "hails"
	t.Cleanup(func() { auth.SuperadminUser = "" })

	plain := &auth.User{Username: "ash", Role: "user"}
	tester := &auth.User{Username: "misty", Role: "tester"}
	mod := &auth.User{Username: "brock", Role: "moderator"}
	admin := &auth.User{Username: "oak", Role: "admin"}
	// Role deliberately "user": the superadmin is matched by username, so this is
	// the case that proves the gate does not read the role column for it.
	super := &auth.User{Username: "hails", Role: "user"}

	gates := map[string]func(*auth.User) bool{
		"mod":   (*auth.User).IsMod,
		"admin": (*auth.User).IsAdmin,
		"super": (*auth.User).IsSuperAdmin,
	}

	cases := []struct {
		gate string
		user *auth.User
		want int
	}{
		{"mod", nil, http.StatusUnauthorized},
		{"mod", plain, http.StatusForbidden},
		{"mod", tester, http.StatusForbidden},
		{"mod", mod, 0},
		{"mod", admin, 0},
		{"mod", super, 0},

		{"admin", nil, http.StatusUnauthorized},
		{"admin", plain, http.StatusForbidden},
		{"admin", mod, http.StatusForbidden},
		{"admin", admin, 0},
		{"admin", super, 0},

		{"super", nil, http.StatusUnauthorized},
		{"super", plain, http.StatusForbidden},
		{"super", mod, http.StatusForbidden},
		{"super", admin, http.StatusForbidden},
		{"super", super, 0},
	}

	for _, c := range cases {
		name := "nil"
		if c.user != nil {
			name = c.user.Username
		}
		if got := roleGateStatus(c.user, gates[c.gate]); got != c.want {
			t.Errorf("%s gate, caller %s: status = %d, want %d", c.gate, name, got, c.want)
		}
	}
}

// A caller with no session must get JSON, not the 303 to /login the HTML wrappers
// answer with. A request carrying neither a Bearer header nor a cookie never
// reaches the database, so a bare Handlers is enough to drive this.
func TestRoleGatesAPIAnswerJSONWithoutASession(t *testing.T) {
	h := &Handlers{}
	called := false
	next := func(http.ResponseWriter, *http.Request) { called = true }

	for name, gate := range map[string]func(http.HandlerFunc) http.HandlerFunc{
		"RequireModAPI":        h.RequireModAPI,
		"RequireAdminAPI":      h.RequireAdminAPI,
		"RequireSuperAdminAPI": h.RequireSuperAdminAPI,
	} {
		called = false
		w := httptest.NewRecorder()
		gate(next)(w, httptest.NewRequest(http.MethodGet, "/api/mobile/v1/admin/context", nil))

		if called {
			t.Errorf("%s let a request with no session through", name)
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want %d", name, w.Code, http.StatusUnauthorized)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s Content-Type = %q, want application/json", name, ct)
		}
		if loc := w.Header().Get("Location"); loc != "" {
			t.Errorf("%s answered a redirect to %q; the HTML wrappers do that, these must not", name, loc)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s body is not JSON: %v (%s)", name, err, w.Body.String())
			continue
		}
		if body["error"] == "" {
			t.Errorf("%s expected an error field, got %s", name, w.Body.String())
		}
	}
}
