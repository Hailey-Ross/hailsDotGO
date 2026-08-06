package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// trustedClientIP is what every rate limiter in the app now keys on, so getting it
// wrong in either direction is serious: too trusting and any client picks its own
// key (and can pin it to a stranger's address to get them blocked), too strict and
// every request collapses onto one shared bucket again.
func TestTrustedClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		xRealIP    string
		want       string
	}{
		{
			name:       "behind the proxy, single hop",
			remoteAddr: "127.0.0.1:54321",
			xff:        "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "client forged an earlier XFF entry, Caddy appended the truth",
			remoteAddr: "127.0.0.1:54321",
			xff:        "1.2.3.4, 203.0.113.7",
			want:       "203.0.113.7", // the LAST entry, not the first
		},
		{
			name:       "client forged X-Real-IP, which is never consulted",
			remoteAddr: "127.0.0.1:54321",
			xff:        "203.0.113.7",
			xRealIP:    "9.9.9.9",
			want:       "203.0.113.7",
		},
		{
			name:       "direct hit on the app port: headers are ignored entirely",
			remoteAddr: "198.51.100.9:41000",
			xff:        "203.0.113.7",
			xRealIP:    "9.9.9.9",
			want:       "", // fall back to the socket address
		},
		{
			name:       "proxy sent no XFF: status quo, not a bypass",
			remoteAddr: "127.0.0.1:54321",
			want:       "",
		},
		{
			name:       "garbage in XFF is not accepted as a key",
			remoteAddr: "127.0.0.1:54321",
			xff:        "not-an-ip",
			want:       "",
		},
		{
			name:       "IPv6 loopback counts as the proxy",
			remoteAddr: "[::1]:54321",
			xff:        "2001:db8::1",
			want:       "2001:db8::1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				r.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if got := trustedClientIP(r); got != tc.want {
				t.Errorf("trustedClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A forged header must not be able to change the rate-limit key.
func TestRealIPMiddlewareIgnoresForgedHeadersOnDirectConnections(t *testing.T) {
	var seen string
	h := realIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.9:41000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.Header.Set("X-Real-IP", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if seen != "198.51.100.9:41000" {
		t.Errorf("RemoteAddr = %q, want the untouched socket address: a direct client "+
			"must not be able to choose its own rate-limit key", seen)
	}
}

// The body cap has to apply by default and be overridable upward for the few
// endpoints that need it.
func TestBodyLimitLookup(t *testing.T) {
	if got := bodyLimits["/api/iv/ocr"]; got != 8<<20 {
		t.Errorf("OCR upload cap = %d, want %d", got, 8<<20)
	}
	if got := bodyLimits["/api/iv/pokemon"]; got != 2<<20 {
		t.Errorf("IV save cap = %d, want %d", got, 2<<20)
	}
	if _, ok := bodyLimits["/login"]; ok {
		t.Error("/login should take the default cap, not an override")
	}
	if defaultMaxBody != 1<<20 {
		t.Errorf("defaultMaxBody = %d, want %d", defaultMaxBody, 1<<20)
	}
}
