package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
