package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

type siteStats struct {
	// System
	Uptime     string  `json:"uptime"`
	Goroutines int     `json:"goroutines"`
	HeapMB     float64 `json:"heap_mb"`
	GCRuns     uint32  `json:"gc_runs"`

	// DB pool
	DBOpen     int    `json:"db_open"`
	DBInUse    int    `json:"db_in_use"`
	DBIdle     int    `json:"db_idle"`
	DBWaits    int64  `json:"db_waits"`
	DBWaitMs   int64  `json:"db_wait_ms"`
	DBPingMs   int64  `json:"db_ping_ms"`

	// App counts
	UserCount      int `json:"user_count"`
	ActiveRaids    int `json:"active_raids"`
	TotalShinies   int `json:"total_shinies"`
	ActiveSessions int `json:"active_sessions"`
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm %ds", mins, secs)
}

func (h *Handlers) AdminStatsAPI(w http.ResponseWriter, r *http.Request) {
	var s siteStats

	// System
	s.Uptime = formatUptime(time.Since(h.startTime))
	s.Goroutines = runtime.NumGoroutine()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.HeapMB = float64(mem.HeapAlloc) / 1024 / 1024
	s.GCRuns = mem.NumGC

	// DB pool
	dbStats := h.db.Stats()
	s.DBOpen = dbStats.OpenConnections
	s.DBInUse = dbStats.InUse
	s.DBIdle = dbStats.Idle
	s.DBWaits = dbStats.WaitCount
	s.DBWaitMs = dbStats.WaitDuration.Milliseconds()

	// DB ping latency
	t0 := time.Now()
	h.db.QueryRow("SELECT 1").Scan(new(int))
	s.DBPingMs = time.Since(t0).Milliseconds()

	// App counts
	h.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&s.UserCount)
	h.db.QueryRow(`SELECT COUNT(*) FROM raid_posts WHERE expires_at > NOW()`).Scan(&s.ActiveRaids)
	h.db.QueryRow(`SELECT COUNT(*) FROM user_shinies`).Scan(&s.TotalShinies)
	h.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE expires_at > NOW()`).Scan(&s.ActiveSessions)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}
