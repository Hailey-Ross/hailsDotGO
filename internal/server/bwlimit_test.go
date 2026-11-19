package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// The bandwidth cap charges what actually travels. A highly compressible 400 KB
// body sent to a gzip client must cost its compressed size, not 400 KB.
func TestBandwidthCapCountsCompressedBytes(t *testing.T) {
	bl := newBWLimiter(1<<30, time.Minute, time.Minute)
	body := strings.Repeat(`{"pokemon":"Pikachu","cp":1234},`, 12500) // ~400 KB

	r := chi.NewRouter()
	r.Use(bl.Meter)
	r.Use(middleware.Compress(5))
	r.With(bl.Handler).Get("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	r.Get("/other", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })

	get := func(path string, gzip bool) int {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "203.0.113.7:1234"
		if gzip {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Body.Len()
	}
	charged := func() int64 {
		bl.mu.Lock()
		defer bl.mu.Unlock()
		if e := bl.windows["203.0.113.7"]; e != nil {
			return e.bytes
		}
		return 0
	}

	wire := get("/api/data", true)
	if wire >= len(body)/10 {
		t.Fatalf("test setup: body did not compress (%d of %d bytes)", wire, len(body))
	}
	if got := charged(); got != int64(wire) {
		t.Fatalf("gzip client charged %d bytes, want the %d that travelled (uncompressed is %d)", got, wire, len(body))
	}

	before := charged()
	plain := get("/api/data", false)
	if got := charged() - before; got != int64(plain) || plain != len(body) {
		t.Fatalf("plain client charged %d bytes for a %d byte answer", got, plain)
	}

	before = charged()
	get("/other", true)
	if got := charged() - before; got != 0 {
		t.Fatalf("an unmetered route was charged %d bytes", got)
	}
}

// Over the limit, the IP is blocked from metered routes with a 429.
func TestBandwidthCapBlocksOverLimit(t *testing.T) {
	bl := newBWLimiter(1000, time.Minute, time.Minute)
	r := chi.NewRouter()
	r.Use(bl.Meter)
	r.Use(middleware.Compress(5))
	r.With(bl.Handler).Get("/api/raids", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(strings.Repeat("x", 600)))
	})
	codes := []int{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/raids", nil)
		req.RemoteAddr = "203.0.113.8:1"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want [200 200 429]", codes)
	}
}
