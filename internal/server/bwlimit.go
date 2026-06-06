package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
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

func (bl *bwLimiter) realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
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

		cw := &countingWriter{ResponseWriter: w}
		next.ServeHTTP(cw, r)

		bl.mu.Lock()
		entry := bl.windows[ip]
		if entry == nil || now.Sub(entry.since) > bl.window {
			entry = &bwEntry{since: now}
			bl.windows[ip] = entry
		}
		entry.bytes += cw.written
		if entry.bytes > bl.limit {
			bl.blocked[ip] = now.Add(bl.penalty)
			delete(bl.windows, ip)
		}
		bl.mu.Unlock()
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
