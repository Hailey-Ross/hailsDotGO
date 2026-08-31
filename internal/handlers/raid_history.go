package handlers

// Raid boss history, warehoused.
//
// cache/raids.json is overwritten on every refresh, so the day a rotation ends it is gone. This
// records what was actually served, so the site can answer "what was in tier 5 last week" and
// "when did this boss last run".
//
// A STAR SCHEMA, not a flat log, and that is the whole point of the design. Everything expensive
// to know about a boss (its stats, typing, CP range, sprite) is resolved once, on first sight, and
// stored in the dimension. Every later sighting is one narrow row in the fact table pointing at
// it. A flat table would rewrite the same few hundred bytes every couple of hours forever and
// leave nothing to compare against when asking whether a boss has been rebalanced.
//
// So: FIRST sighting of a boss costs an insert with a dozen columns. The ten thousandth costs one
// indexed UPDATE of two counters and one narrow INSERT that is usually a no-op.

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// StartRaidHistory registers the archiver with the store and records what is live right now.
//
// The hook runs on whichever goroutine rebuilt the served list, so the work is handed to a
// goroutine of its own: the raid rebuild happens on the boundary ticker and must not wait on a
// database round trip.
func (h *Handlers) StartRaidHistory() {
	h.store.SetRaidsAppliedHook(func() { go h.archiveServedRaids() })
	// The hook only fires on a rebuild, and a boot that serves the disk cache
	// unchanged does not have one. Without this, a restart during a quiet rotation
	// would leave the current bosses unrecorded until the next refresh.
	go h.archiveServedRaids()
}

// archiveServedRaids writes the currently served bosses into the warehouse.
//
// Idempotent by construction: the dimension is keyed on the boss's natural key and the fact table
// on (boss, window start). That matters because this runs on every rebuild, which is a short
// ticker plus every refresh, so the same rotation is offered for recording dozens of times over
// its run.
func (h *Handlers) archiveServedRaids() {
	rows := h.store.RaidArchiveRows()
	if len(rows) == 0 {
		return
	}

	now := time.Now().UTC()
	var dims, facts, rebalanced int
	for _, r := range rows {
		bossID, changed, err := h.upsertRaidBoss(r, now)
		if err != nil {
			log.Printf("raid history: %s: %v", r.Species, err)
			continue
		}
		dims++
		if changed {
			rebalanced++
		}
		if !r.HasWindow() {
			// No rotation window, so there is no appearance to key. Tier 1 and 3
			// are the whole of this: no feed anywhere carries timing for them. The
			// boss is still worth having in the dimension, which is why the upsert
			// above ran; inventing a window here would put a row in the fact table
			// recording something that did not happen.
			continue
		}
		inserted, err := h.recordRaidAppearance(bossID, r, now)
		if err != nil {
			log.Printf("raid history: appearance for %s: %v", r.Species, err)
			continue
		}
		if inserted {
			facts++
		}
	}

	// Only when something actually landed. This runs on a short ticker, and a log
	// line per tick saying nothing changed is a log nobody reads.
	if facts > 0 || rebalanced > 0 {
		log.Printf("raid history: %d bosses seen, %d new appearances, %d rebalanced", dims, facts, rebalanced)
	}
}

// upsertRaidBoss writes the dimension row, resolving the expensive columns only on first sight.
//
// Returns the surrogate id and whether the boss's stats CHANGED, which is a rebalance and is worth
// a log line: it is the one thing that happens to a dimension row after it is written.
func (h *Handlers) upsertRaidBoss(r pogodata.RaidArchiveRow, now time.Time) (id uint, statsChanged bool, err error) {
	species, form := splitBossName(r.Species)
	types := strings.Join(r.Types, ",")

	var (
		prevTypes                                        string
		prevCPMin, prevCPMax, prevBoostMin, prevBoostMax int
	)
	err = h.db.QueryRow(`
		SELECT id, types, cp_min, cp_max, cp_boosted_min, cp_boosted_max
		  FROM raid_boss_dim
		 WHERE species = ? AND form = ? AND tier = ? AND shadow = ?`,
		species, form, r.Tier, r.Shadow,
	).Scan(&id, &prevTypes, &prevCPMin, &prevCPMax, &prevBoostMin, &prevBoostMax)

	if err == sql.ErrNoRows {
		// First sight. This is the only path that pays for the full row.
		res, insErr := h.db.Exec(`
			INSERT INTO raid_boss_dim
				(species, form, tier, shadow, is_mega, types,
				 cp_min, cp_max, cp_boosted_min, cp_boosted_max,
				 image_url, can_be_shiny,
				 first_seen_at, last_seen_at, appearance_count, stats_updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
			species, form, r.Tier, r.Shadow, r.IsMega, types,
			r.CP, r.CPMax, r.CPBoostedMin, r.CPBoostedMax,
			r.ImageURL, r.CanBeShiny, now, now, now,
		)
		if insErr != nil {
			return 0, false, insErr
		}
		newID, idErr := res.LastInsertId()
		if idErr != nil {
			return 0, false, idErr
		}
		return uint(newID), false, nil
	}
	if err != nil {
		return 0, false, err
	}

	// Already known. The cheap path: touch last_seen_at and nothing else, unless
	// the game has actually rebalanced the boss.
	statsChanged = prevTypes != types ||
		prevCPMin != r.CP || prevCPMax != r.CPMax ||
		prevBoostMin != r.CPBoostedMin || prevBoostMax != r.CPBoostedMax

	if !statsChanged {
		_, err = h.db.Exec(`UPDATE raid_boss_dim SET last_seen_at = ? WHERE id = ?`, now, id)
		return id, false, err
	}

	log.Printf("raid history: %s (tier %s) stats changed: types %q->%q, cp %d-%d -> %d-%d",
		r.Species, r.Tier, prevTypes, types, prevCPMin, prevCPMax, r.CP, r.CPMax)
	_, err = h.db.Exec(`
		UPDATE raid_boss_dim
		   SET types = ?, cp_min = ?, cp_max = ?, cp_boosted_min = ?, cp_boosted_max = ?,
		       image_url = ?, can_be_shiny = ?, is_mega = ?,
		       last_seen_at = ?, stats_updated_at = ?
		 WHERE id = ?`,
		types, r.CP, r.CPMax, r.CPBoostedMin, r.CPBoostedMax,
		r.ImageURL, r.CanBeShiny, r.IsMega, now, now, id,
	)
	return id, true, err
}

// recordRaidAppearance writes the fact row, and reports whether it was new.
//
// The unique key on (boss_id, window_start) does the deduplication, so a repeat offer is an
// INSERT IGNORE that touches nothing. appearance_count on the dimension is bumped only when a row
// was actually added, which is what keeps it a count of rotations rather than of ticks.
func (h *Handlers) recordRaidAppearance(bossID uint, r pogodata.RaidArchiveRow, now time.Time) (bool, error) {
	res, err := h.db.Exec(`
		INSERT IGNORE INTO raid_appearance_fact
			(boss_id, window_start, window_end, event_id, source, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		bossID, r.WindowStart.UTC(), r.WindowEnd.UTC(), r.EventID, r.Source, now,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}

	// Denormalised on purpose: "how often has this run" and "when did it last run"
	// are the two questions asked most about a boss, and answering either from the
	// fact table alone is a scan.
	if _, err := h.db.Exec(
		`UPDATE raid_boss_dim SET appearance_count = appearance_count + 1, last_seen_at = ? WHERE id = ?`,
		now, bossID,
	); err != nil {
		return true, err
	}
	return true, nil
}

// splitBossName separates a display name into species and form.
//
// The feeds encode the form in the display name and keep nothing else, the same way they encode
// shadow. "Zacian (Crowned Sword)" is one boss and "Zacian (Hero of Many Battles)" is another,
// with different stats, so the parenthetical has to be part of the natural key rather than
// discarded into the species column.
func splitBossName(name string) (species, form string) {
	name = strings.TrimSpace(name)
	open := strings.LastIndex(name, "(")
	if open <= 0 || !strings.HasSuffix(name, ")") {
		return name, ""
	}
	return strings.TrimSpace(name[:open]), strings.TrimSpace(name[open+1 : len(name)-1])
}

// ── Reads ────────────────────────────────────────────────────────────────────

type raidHistoryEntry struct {
	Species     string   `json:"species"`
	Form        string   `json:"form,omitempty"`
	Tier        string   `json:"tier"`
	Shadow      bool     `json:"shadow,omitempty"`
	IsMega      bool     `json:"is_mega,omitempty"`
	Types       []string `json:"types,omitempty"`
	ImageURL    string   `json:"image_url,omitempty"`
	CanBeShiny  bool     `json:"can_be_shiny,omitempty"`
	WindowStart string   `json:"window_start"`
	WindowEnd   string   `json:"window_end"`
	EventID     string   `json:"event_id,omitempty"`
	Source      string   `json:"source"`
}

// raidHistoryDefaultDays bounds the default window, so the endpoint cannot be asked for the whole
// table by omitting a parameter.
const (
	raidHistoryDefaultDays = 90
	raidHistoryMaxDays     = 730
	raidHistoryMaxRows     = 500
)

// APIRaidHistory lists past rotations, newest first.
//
// One join from fact to dimension, which is the shape the star schema exists to make cheap: the
// stats come from the dimension once per boss rather than being repeated on every row of the table.
func (h *Handlers) APIRaidHistory(w http.ResponseWriter, r *http.Request) {
	days := clampQueryInt(r, "days", raidHistoryDefaultDays, 1, raidHistoryMaxDays)
	limit := clampQueryInt(r, "limit", raidHistoryMaxRows, 1, raidHistoryMaxRows)
	since := time.Now().UTC().AddDate(0, 0, -days)

	args := []any{since}
	query := `
		SELECT d.species, d.form, d.tier, d.shadow, d.is_mega, d.types, d.image_url, d.can_be_shiny,
		       f.window_start, f.window_end, f.event_id, f.source
		  FROM raid_appearance_fact f
		  JOIN raid_boss_dim d ON d.id = f.boss_id
		 WHERE f.window_start >= ?`
	if tier := strings.TrimSpace(r.URL.Query().Get("tier")); tier != "" {
		query += ` AND d.tier = ?`
		args = append(args, tier)
	}
	query += ` ORDER BY f.window_start DESC, d.species ASC LIMIT ?`
	args = append(args, limit)

	h.writeRaidHistory(w, r, query, args)
}

// APIRaidHistoryOfBoss answers "when did this boss last run, and how often".
//
// The counters come off the dimension, which is one indexed row read rather than an aggregate over
// the fact table. That is the denormalisation earning its keep.
func (h *Handlers) APIRaidHistoryOfBoss(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "boss"))
	if name == "" {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	species, form := splitBossName(name)

	type bossSummary struct {
		Species         string             `json:"species"`
		Form            string             `json:"form,omitempty"`
		Tier            string             `json:"tier"`
		Shadow          bool               `json:"shadow,omitempty"`
		IsMega          bool               `json:"is_mega,omitempty"`
		Types           []string           `json:"types,omitempty"`
		ImageURL        string             `json:"image_url,omitempty"`
		FirstSeenAt     string             `json:"first_seen_at"`
		LastSeenAt      string             `json:"last_seen_at"`
		AppearanceCount int                `json:"appearance_count"`
		Appearances     []raidHistoryEntry `json:"appearances"`
	}

	rows, err := h.db.Query(`
		SELECT id, species, form, tier, shadow, is_mega, types, image_url,
		       first_seen_at, last_seen_at, appearance_count
		  FROM raid_boss_dim
		 WHERE species = ? AND (? = '' OR form = ?)
		 ORDER BY tier, shadow`, species, form, form)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []bossSummary{}
	ids := []uint{}
	for rows.Next() {
		var b bossSummary
		var id uint
		var types string
		var first, last time.Time
		if rows.Scan(&id, &b.Species, &b.Form, &b.Tier, &b.Shadow, &b.IsMega, &types, &b.ImageURL,
			&first, &last, &b.AppearanceCount) != nil {
			continue
		}
		b.Types = splitTypes(types)
		b.FirstSeenAt = first.UTC().Format(time.RFC3339)
		b.LastSeenAt = last.UTC().Format(time.RFC3339)
		b.Appearances = []raidHistoryEntry{}
		out = append(out, b)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if len(out) == 0 {
		writeJSONError(w, "boss not found", http.StatusNotFound)
		return
	}

	// The appearances themselves, most recent first, capped.
	for i, id := range ids {
		arows, err := h.db.Query(`
			SELECT window_start, window_end, event_id, source
			  FROM raid_appearance_fact
			 WHERE boss_id = ?
			 ORDER BY window_start DESC
			 LIMIT ?`, id, raidHistoryMaxRows)
		if err != nil {
			continue
		}
		for arows.Next() {
			var e raidHistoryEntry
			var start, end time.Time
			if arows.Scan(&start, &end, &e.EventID, &e.Source) != nil {
				continue
			}
			e.WindowStart = start.UTC().Format(time.RFC3339)
			e.WindowEnd = end.UTC().Format(time.RFC3339)
			out[i].Appearances = append(out[i].Appearances, e)
		}
		arows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// writeRaidHistory runs a fact-to-dimension query and writes the result.
func (h *Handlers) writeRaidHistory(w http.ResponseWriter, r *http.Request, query string, args []any) {
	rows, err := h.db.Query(query, args...)
	if err != nil {
		log.Printf("raid history: query: %v", err)
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []raidHistoryEntry{}
	for rows.Next() {
		var e raidHistoryEntry
		var types string
		var start, end time.Time
		if rows.Scan(&e.Species, &e.Form, &e.Tier, &e.Shadow, &e.IsMega, &types, &e.ImageURL,
			&e.CanBeShiny, &start, &end, &e.EventID, &e.Source) != nil {
			continue
		}
		e.Types = splitTypes(types)
		e.WindowStart = start.UTC().Format(time.RFC3339)
		e.WindowEnd = end.UTC().Format(time.RFC3339)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// splitTypes turns the stored comma separated typing back into a list. Empty stays empty rather
// than becoming a one element list containing "", which is what strings.Split alone would give.
func splitTypes(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}
