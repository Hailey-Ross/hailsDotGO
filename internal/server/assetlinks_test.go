package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The Digital Asset Links statement fails silently when it is wrong: Android reports
// nothing to the user, links simply keep opening in the browser. So the shape of the
// response and the contents of the committed file are both worth asserting.

func TestAssetLinksServesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "assetlinks.json")
	body := `[{"relation":["delegate_permission/common.handle_all_urls"]}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		assetLinks(path)(rec, httptest.NewRequest(method, "/.well-known/assetlinks.json", nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", method, rec.Code)
		}
		// securityHeaders sends nosniff, so an inexact type is fatal, not cosmetic.
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: Content-Type = %q, want application/json", method, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
			t.Errorf("%s: Cache-Control = %q", method, got)
		}
		// The verifier does not follow redirects, so any Location here is a failure.
		if got := rec.Header().Get("Location"); got != "" {
			t.Errorf("%s: unexpected Location %q", method, got)
		}
	}

	rec := httptest.NewRecorder()
	assetLinks(path)(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q", rec.Body.String(), body)
	}
}

func TestAssetLinksMissingFileIsNotCached(t *testing.T) {
	rec := httptest.NewRecorder()
	assetLinks(filepath.Join(t.TempDir(), "nope.json"))(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	// A missing file means a deploy went wrong. Caching that answer for an hour would
	// keep serving the 404 after the file was put back.
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("404 was cached: Cache-Control = %q", got)
	}
}

// The committed file is what actually ships. A typo in the package name or a malformed
// fingerprint costs an install-time verification with no error message anywhere.
func TestCommittedAssetLinksFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", assetLinksPath))
	if err != nil {
		t.Fatalf("%s is missing; the route would 404 in production: %v", assetLinksPath, err)
	}

	var statements []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			PackageName  string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(raw, &statements); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(statements))
	}

	s := statements[0]
	if len(s.Relation) != 1 || s.Relation[0] != "delegate_permission/common.handle_all_urls" {
		t.Errorf("relation = %v", s.Relation)
	}
	if s.Target.Namespace != "android_app" {
		t.Errorf("namespace = %q, want android_app", s.Target.Namespace)
	}
	// Fixed by the app's applicationId in hailsDotGO-mobile. Not a guess.
	if s.Target.PackageName != "live.hails.hailsdotgo" {
		t.Errorf("package_name = %q, want live.hails.hailsdotgo", s.Target.PackageName)
	}
	if len(s.Target.Fingerprints) == 0 {
		t.Error("no fingerprints listed; verification cannot succeed")
	}
	// Uppercase hex, colon separated, 32 bytes. Anything else is rejected outright.
	for _, fp := range s.Target.Fingerprints {
		if len(fp) != 95 {
			t.Errorf("fingerprint %q is %d chars, want 95 (32 colon-separated bytes)", fp, len(fp))
			continue
		}
		for i, c := range fp {
			ok := false
			switch {
			case i%3 == 2:
				ok = c == ':'
			default:
				ok = (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')
			}
			if !ok {
				t.Errorf("fingerprint %q: bad character %q at %d", fp, c, i)
				break
			}
		}
	}
}
