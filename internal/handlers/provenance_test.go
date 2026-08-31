package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func provReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, nil)
}

const (
	webBoxPath    = "/api/iv/pokemon"
	mobileBoxPath = "/api/mobile/v1/iv/pokemon"
)

func TestResolveProvenanceUsesRouteNotClaim(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		claim    string
		attested bool
		want     boxProvenance
	}{
		{"browser scan", webBoxPath, "scan", false, provScanWeb},
		{"browser manual", webBoxPath, "manual", false, provManual},
		{"app scan", mobileBoxPath, "scan", false, provScanApp},
		{"app scan attested", mobileBoxPath, "scan", true, provScanAppAttested},
		{"app manual", mobileBoxPath, "manual", false, provManual},
		{"claim absent", mobileBoxPath, "", false, provUnknown},
		{"claim unrecognised", mobileBoxPath, "somethingelse", false, provUnknown},
		{"claim case and space tolerated", mobileBoxPath, "  SCAN ", false, provScanApp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProvenance(provReq(tc.path), tc.claim, tc.attested)
			if got != tc.want {
				t.Fatalf("resolveProvenance(%q, %q, %v) = %q, want %q",
					tc.path, tc.claim, tc.attested, got, tc.want)
			}
		})
	}
}

// The column exists so a display can tell a verified row from an unverified one.
// That is worth nothing if a client can simply ask to be recorded as verified,
// so the trust-bearing values must be unreachable from the request body.
func TestResolveProvenanceIgnoresForgedTrustValues(t *testing.T) {
	forged := []string{
		"scan_app_attested",
		"scan_app",
		"scan_web",
		"SCAN_APP_ATTESTED",
	}
	for _, claim := range forged {
		// Even from the app's own route, and even though the string names a
		// real value, nothing but "scan" or "manual" is a recognised claim.
		if got := resolveProvenance(provReq(mobileBoxPath), claim, false); got != provUnknown {
			t.Errorf("claim %q resolved to %q, want %q", claim, got, provUnknown)
		}
		// And a browser cannot reach an app value however it asks.
		if got := resolveProvenance(provReq(webBoxPath), claim, false); got != provUnknown {
			t.Errorf("web claim %q resolved to %q, want %q", claim, got, provUnknown)
		}
	}
	// The one value that means "attested" is reachable only with a real
	// attestation, never by claiming it.
	if got := resolveProvenance(provReq(mobileBoxPath), "scan", false); got == provScanAppAttested {
		t.Fatal("an unattested app write resolved to the attested value")
	}
}

func TestIsMobileAPI(t *testing.T) {
	cases := map[string]bool{
		mobileBoxPath:               true,
		"/api/mobile/v1/iv/scan":    true,
		webBoxPath:                  false,
		"/api/iv/ocr":               false,
		"/iv":                       false,
		"/api/mobile/v2/iv/pokemon": false,
		// Prefix matching must not be fooled by a lookalike path segment.
		"/api/mobile/v1x/iv/pokemon": false,
	}
	for path, want := range cases {
		if got := isMobileAPI(provReq(path)); got != want {
			t.Errorf("isMobileAPI(%q) = %v, want %v", path, got, want)
		}
	}
}

// unknown is the zero-trust default and must never read as verified: every row
// written before the column existed carries it.
func TestProvenanceVerified(t *testing.T) {
	cases := map[boxProvenance]bool{
		provScanApp:         true,
		provScanAppAttested: true,
		provScanWeb:         true,
		provManual:          false,
		provUnknown:         false,
	}
	for p, want := range cases {
		if got := p.verified(); got != want {
			t.Errorf("%q.verified() = %v, want %v", p, got, want)
		}
	}
}
