package server

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type bwEntry struct {
	bytes int64
	since time.Time
}

type bwLimiter struct {
	mu      sync.Mutex
	windows map[string]*bwEntry
	blocked map[string]time.Time
	limit   int64
	window  time.Duration
	penalty time.Duration
}

func newBWLimiter(limit int64, window, penalty time.Duration) *bwLimiter {
	bl := &bwLimiter{
		windows: make(map[string]*bwEntry),
		blocked: make(map[string]time.Time),
		limit:   limit,
		window:  window,
		penalty: penalty,
	}
	go bl.cleanup()
	return bl
}

// realIP reads the address the realIP middleware resolved, and nothing else.
//
// This used to read X-Real-IP and then the first X-Forwarded-For entry, both
// straight from the request. X-Real-IP is the dangerous one: a capture against the
// live host confirmed Caddy forwards it to the app exactly as the client sent it.
// So one header of the attacker's choosing both bypassed the bandwidth cap entirely
// and added an unbounded entry to the windows and blocked maps, since every distinct
// forged value counted as a new key.
func (bl *bwLimiter) realIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// bwTicketKey carries a *bwTicket from Meter down to Handler.
type bwTicketKey struct{}

// bwTicket is how a metered route tells Meter, which sits outside the compressor, that
// the bytes this request put on the wire count toward its IP's budget.
type bwTicket struct{ metered bool }

// Meter counts what a response actually costs on the wire. It must be registered
// BEFORE middleware.Compress so it sees compressed bytes: counted inside the
// compressor (as Handler alone does), an 862 KB game data bundle that travels as
// 125 KB charged the full 862 KB, so a crowded network ran out of budget about seven
// times sooner than the bandwidth it really used. Only routes wrapped in Handler are
// counted; every other request passes through untouched.
func (bl *bwLimiter) Meter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := &bwTicket{}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), bwTicketKey{}, t)))
		if t.metered {
			bl.add(bl.realIP(r), int64(ww.BytesWritten()), time.Now())
		}
	})
}

// add charges bytes to an IP's window and blocks it once the window is over the limit.
func (bl *bwLimiter) add(ip string, n int64, now time.Time) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	entry := bl.windows[ip]
	if entry == nil || now.Sub(entry.since) > bl.window {
		entry = &bwEntry{since: now}
		bl.windows[ip] = entry
	}
	entry.bytes += n
	if entry.bytes > bl.limit {
		bl.blocked[ip] = now.Add(bl.penalty)
		delete(bl.windows, ip)
	}
}

func (bl *bwLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := bl.realIP(r)
		now := time.Now()

		bl.mu.Lock()
		if until, ok := bl.blocked[ip]; ok && now.Before(until) {
			bl.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"bandwidth limit exceeded, try again later"}`, http.StatusTooManyRequests)
			return
		}
		bl.mu.Unlock()

		// Normal path: Meter, outside the compressor, counts the bytes on the wire.
		if t, ok := r.Context().Value(bwTicketKey{}).(*bwTicket); ok {
			t.metered = true
			next.ServeHTTP(w, r)
			return
		}
		// No Meter in the chain (a test, or a router built without it): count here.
		cw := &countingWriter{ResponseWriter: w}
		next.ServeHTTP(cw, r)
		bl.add(ip, cw.written, now)
	})
}

func (bl *bwLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		now := time.Now()
		bl.mu.Lock()
		for ip, e := range bl.windows {
			if now.Sub(e.since) > bl.window {
				delete(bl.windows, ip)
			}
		}
		for ip, until := range bl.blocked {
			if now.After(until) {
				delete(bl.blocked, ip)
			}
		}
		bl.mu.Unlock()
	}
}

type countingWriter struct {
	http.ResponseWriter
	written int64
}

func (cw *countingWriter) Write(b []byte) (int, error) {
	n, err := cw.ResponseWriter.Write(b)
	cw.written += int64(n)
	return n, err
}
