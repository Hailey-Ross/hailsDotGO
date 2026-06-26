package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/pogodata"
)

// loadSpriteLocks returns slug -> min_rank for all rows in sprite_locks.
// Slugs absent from the table are unlocked (effective min_rank = 0).
func (h *Handlers) loadSpriteLocks() (map[string]int, error) {
	rows, err := h.db.Query(`SELECT slug, min_rank FROM sprite_locks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var slug string
		var rank int
		if err := rows.Scan(&slug, &rank); err == nil {
			m[slug] = rank
		}
	}
	return m, nil
}

// isProfessor returns true for trainer sprites whose label begins with "Prof."
// These are auto-locked at min_rank=1 (Trusted+) regardless of the sprite_locks table.
func isProfessor(tc pogodata.TrainerClass) bool {
	return strings.HasPrefix(tc.Label, "Prof.")
}

// spriteMinRank returns the effective minimum rank a user must have to use a sprite.
func spriteMinRank(tc pogodata.TrainerClass, locks map[string]int) int {
	if isProfessor(tc) {
		return 1
	}
	return locks[tc.Slug]
}

// filteredTrainerClasses returns only the sprites the given user rank can access.
func filteredTrainerClasses(all []pogodata.TrainerClass, locks map[string]int, userRank int) []pogodata.TrainerClass {
	out := make([]pogodata.TrainerClass, 0, len(all))
	for _, tc := range all {
		if userRank >= spriteMinRank(tc, locks) {
			out = append(out, tc)
		}
	}
	return out
}

// --- Admin API -----------------------------------------------------------

type spriteAdminEntry struct {
	Slug        string `json:"slug"`
	Label       string `json:"label"`
	SpriteURL   string `json:"sprite_url"`
	Group       string `json:"group"`
	MinRank     int    `json:"min_rank"`
	IsProfessor bool   `json:"is_professor"`
}

// AdminGetSpriteLocks returns all trainer sprites with their lock configuration.
func (h *Handlers) AdminGetSpriteLocks(w http.ResponseWriter, r *http.Request) {
	locks, err := h.loadSpriteLocks()
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	all := h.store.TrainerClasses()
	entries := make([]spriteAdminEntry, 0, len(all))
	for _, tc := range all {
		entries = append(entries, spriteAdminEntry{
			Slug:        tc.Slug,
			Label:       tc.Label,
			SpriteURL:   tc.SpriteURL,
			Group:       tc.Group,
			MinRank:     locks[tc.Slug],
			IsProfessor: isProfessor(tc),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

var validMinRanks = map[int]bool{0: true, 1: true, 2: true, 4: true, 5: true, 100: true}

// AdminSetSpriteLock sets or updates the min_rank for a single sprite slug.
func (h *Handlers) AdminSetSpriteLock(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	// Verify slug exists
	found := false
	for _, tc := range h.store.TrainerClasses() {
		if tc.Slug == slug {
			found = true
			if isProfessor(tc) {
				writeJSONError(w, "professor locks are automatic and cannot be overridden", http.StatusBadRequest)
				return
			}
			break
		}
	}
	if !found {
		writeJSONError(w, "sprite not found", http.StatusNotFound)
		return
	}

	var body struct {
		MinRank int `json:"min_rank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !validMinRanks[body.MinRank] {
		writeJSONError(w, "invalid min_rank value", http.StatusBadRequest)
		return
	}

	if body.MinRank == 0 {
		h.db.Exec(`DELETE FROM sprite_locks WHERE slug = ?`, slug)
	} else {
		h.db.Exec(`INSERT INTO sprite_locks (slug, min_rank) VALUES (?, ?)
		           ON DUPLICATE KEY UPDATE min_rank = VALUES(min_rank)`, slug, body.MinRank)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminDeleteSpriteLock removes a lock from a sprite (sets it back to unlocked).
func (h *Handlers) AdminDeleteSpriteLock(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	h.db.Exec(`DELETE FROM sprite_locks WHERE slug = ?`, slug)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
