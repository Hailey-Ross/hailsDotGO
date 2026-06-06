package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type tagRequestRecord struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Color        string `json:"color"`
	Status       string `json:"status"`
	RejectReason string `json:"reject_reason"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handlers) AdminTagRequestsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT ctr.id, u.username, ctr.name, ctr.color, ctr.status,
		       COALESCE(ctr.reject_reason,''), ctr.created_at
		FROM custom_tag_requests ctr JOIN users u ON u.id = ctr.user_id
		ORDER BY FIELD(ctr.status,'pending','approved','rejected'), ctr.created_at ASC`)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []tagRequestRecord{}
	for rows.Next() {
		var rec tagRequestRecord
		var createdAt time.Time
		if rows.Scan(&rec.ID, &rec.Username, &rec.Name, &rec.Color, &rec.Status, &rec.RejectReason, &createdAt) == nil {
			rec.CreatedAt = createdAt.Format("2006-01-02")
			out = append(out, rec)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) AdminTagRequestApprove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var userID uint
	var name, color, status string
	if err := h.db.QueryRow(
		`SELECT user_id, name, color, status FROM custom_tag_requests WHERE id = ?`, id,
	).Scan(&userID, &name, &color, &status); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	if status != "pending" {
		writeJSONError(w, "request is not pending", http.StatusBadRequest)
		return
	}
	if !h.hasActivePurchase(userID, "supporter") {
		writeJSONError(w, "user no longer has an active Supporter Pack", http.StatusForbidden)
		return
	}

	if _, err = h.db.Exec(`INSERT INTO tags (name, color) VALUES (?, ?) ON DUPLICATE KEY UPDATE color = VALUES(color)`, name, color); err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	var tagID int64
	h.db.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&tagID)

	h.db.Exec(`INSERT IGNORE INTO user_tags (user_id, tag_id) VALUES (?, ?)`, userID, tagID)
	h.db.Exec(
		`UPDATE custom_tag_requests SET status = 'approved', reviewed_by = ?, reviewed_at = NOW() WHERE id = ?`,
		actor.ID, id,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTagRequestReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > 255 {
		reason = reason[:255]
	}

	var userID uint
	var tagName, currentStatus string
	if err := h.db.QueryRow(
		`SELECT user_id, COALESCE(name,''), status FROM custom_tag_requests WHERE id = ?`, id,
	).Scan(&userID, &tagName, &currentStatus); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	if currentStatus != "pending" && currentStatus != "approved" {
		writeJSONError(w, "request is not pending or approved", http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(
		`UPDATE custom_tag_requests SET status = 'rejected', reject_reason = ?, reviewed_by = ?, reviewed_at = NOW()
		 WHERE id = ? AND status IN ('pending','approved')`,
		reason, actor.ID, id,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "not found or already processed", http.StatusNotFound)
		return
	}

	if currentStatus == "approved" && tagName != "" {
		var tagID uint
		if h.db.QueryRow(`SELECT id FROM tags WHERE name = ?`, tagName).Scan(&tagID) == nil && tagID != 0 {
			h.db.Exec(`DELETE FROM user_tags WHERE user_id = ? AND tag_id = ?`, userID, tagID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AdminTagRequestRevision(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	notes := strings.TrimSpace(body.Notes)
	if len(notes) > 255 {
		notes = notes[:255]
	}

	var userID uint
	var tagName, currentStatus string
	if err := h.db.QueryRow(
		`SELECT user_id, COALESCE(name,''), status FROM custom_tag_requests WHERE id = ?`, id,
	).Scan(&userID, &tagName, &currentStatus); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	if currentStatus != "pending" && currentStatus != "approved" {
		writeJSONError(w, "request is not pending or approved", http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(
		`UPDATE custom_tag_requests SET status = 'revision', reject_reason = ?, reviewed_by = ?, reviewed_at = NOW()
		 WHERE id = ? AND status IN ('pending','approved')`,
		notes, actor.ID, id,
	)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, "not found or already processed", http.StatusNotFound)
		return
	}

	if currentStatus == "approved" && tagName != "" {
		var tagID uint
		if h.db.QueryRow(`SELECT id FROM tags WHERE name = ?`, tagName).Scan(&tagID) == nil && tagID != 0 {
			h.db.Exec(`DELETE FROM user_tags WHERE user_id = ? AND tag_id = ?`, userID, tagID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type storeItemAdmin struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	PriceCents int    `json:"price_cents"`
	Type       string `json:"type"`
	Active     bool   `json:"active"`
	SortOrder  int    `json:"sort_order"`
}

func (h *Handlers) AdminStoreItemsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`SELECT id, name, slug, price_cents, type, active, sort_order FROM store_items ORDER BY sort_order`)
	if err != nil {
		writeJSONError(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []storeItemAdmin{}
	for rows.Next() {
		var it storeItemAdmin
		if rows.Scan(&it.ID, &it.Name, &it.Slug, &it.PriceCents, &it.Type, &it.Active, &it.SortOrder) == nil {
			out = append(out, it)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) AdminToggleStoreItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var active bool
	if err := h.db.QueryRow(`SELECT active FROM store_items WHERE id = ?`, id).Scan(&active); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	h.db.Exec(`UPDATE store_items SET active = ? WHERE id = ?`, !active, id)
	w.Header().Set("Content-Type", "application/json")
	if active {
		w.Write([]byte(`{"active":false}`))
	} else {
		w.Write([]byte(`{"active":true}`))
	}
}
