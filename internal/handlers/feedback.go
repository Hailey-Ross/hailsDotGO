package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type feedbackOptionRow struct {
	ID        uint   `json:"id"`
	Label     string `json:"label"`
	Sentiment string `json:"sentiment"`
	SortOrder int    `json:"sort_order"`
	Enabled   bool   `json:"enabled"`
}

type trainerFeedbackEntry struct {
	ID             uint
	AuthorUsername string
	AuthorName     string
	Label          string
	Sentiment      string
	UpdatedAt      time.Time
}

// enabledFeedbackOptions loads the options a trainer is allowed to pick from, in
// display order. Never nil, so a caller can range over it and a client always
// gets an array.
//
// Extracted from TrainerProfilePage so the mobile endpoint runs the same query.
// The admin CRUD at /api/admin/feedback-options is the only other place this list
// existed, and that one is RequireAdmin: without this, an ordinary trainer could
// read feedback but had no way to learn what they were allowed to say.
func (h *Handlers) enabledFeedbackOptions() []feedbackOptionRow {
	options := []feedbackOptionRow{}
	rows, err := h.db.Query(`
		SELECT id, label, sentiment, sort_order, enabled
		FROM feedback_options WHERE enabled = 1
		ORDER BY sort_order, id`)
	if err != nil {
		return options
	}
	defer rows.Close()
	for rows.Next() {
		var o feedbackOptionRow
		var enabledInt int
		if rows.Scan(&o.ID, &o.Label, &o.Sentiment, &o.SortOrder, &enabledInt) == nil {
			o.Enabled = enabledInt > 0
			options = append(options, o)
		}
	}
	return options
}

// mobileFeedbackOption is one option as a client sees it.
//
// A separate type from feedbackOptionRow purely to drop `enabled`. Every row this
// endpoint serves is enabled by construction, and shipping the column invites a
// client to filter on it, which is a filter that can only ever be wrong: an option
// that is switched off is absent, not present and false.
type mobileFeedbackOption struct {
	ID        uint   `json:"id"`
	Label     string `json:"label"`
	Sentiment string `json:"sentiment"`
	SortOrder int    `json:"sort_order"`
}

// MobileFeedbackOptions serves the feedback dropdown for the profile screen.
func (h *Handlers) MobileFeedbackOptions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUserAPI(w, r); !ok {
		return
	}
	rows := h.enabledFeedbackOptions()
	out := make([]mobileFeedbackOption, 0, len(rows))
	for _, o := range rows {
		out = append(out, mobileFeedbackOption{ID: o.ID, Label: o.Label, Sentiment: o.Sentiment, SortOrder: o.SortOrder})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// APIGetFeedback returns the feedback list for a given username (public endpoint).
func (h *Handlers) APIGetFeedback(w http.ResponseWriter, r *http.Request) {
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0 AND deleted_at IS NULL`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	type apiEntry struct {
		ID             uint   `json:"id"`
		AuthorUsername string `json:"author_username"`
		AuthorName     string `json:"author_name"`
		Label          string `json:"label"`
		Sentiment      string `json:"sentiment"`
		UpdatedAt      string `json:"updated_at"`
	}

	rows, err := h.db.Query(`
		SELECT uf.id, u.username, COALESCE(u.trainer_name,''), fo.label, fo.sentiment, uf.updated_at
		FROM user_feedback uf
		JOIN users u ON u.id = uf.author_id
		JOIN feedback_options fo ON fo.id = uf.option_id
		WHERE uf.target_id = ?
		ORDER BY uf.updated_at DESC`, targetID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	entries := []apiEntry{}
	for rows.Next() {
		var e apiEntry
		var updatedAt time.Time
		if rows.Scan(&e.ID, &e.AuthorUsername, &e.AuthorName, &e.Label, &e.Sentiment, &updatedAt) == nil {
			e.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
			entries = append(entries, e)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// APIPostFeedback upserts the caller's feedback on a target user (one per pair).
func (h *Handlers) APIPostFeedback(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	targetUsername := chi.URLParam(r, "username")

	var targetID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0 AND deleted_at IS NULL`, targetUsername).Scan(&targetID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	if targetID == u.ID {
		writeJSONError(w, "cannot leave feedback on yourself", http.StatusBadRequest)
		return
	}

	var body struct {
		OptionID uint `json:"option_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OptionID == 0 {
		writeJSONError(w, "option_id required", http.StatusBadRequest)
		return
	}

	// Verify option exists and is enabled
	var optEnabled int
	if err := h.db.QueryRow(`SELECT enabled FROM feedback_options WHERE id = ?`, body.OptionID).Scan(&optEnabled); err != nil || optEnabled != 1 {
		writeJSONError(w, "invalid or disabled option", http.StatusBadRequest)
		return
	}

	_, err := h.db.Exec(`
		INSERT INTO user_feedback (author_id, target_id, option_id)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE option_id = VALUES(option_id), updated_at = NOW()`,
		u.ID, targetID, body.OptionID)
	if err != nil {
		writeJSONError(w, "could not save feedback", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// APIDeleteFeedback removes a feedback entry. Authors can delete their own; mods can delete any.
func (h *Handlers) APIDeleteFeedback(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var authorID uint
	if err := h.db.QueryRow(`SELECT author_id FROM user_feedback WHERE id = ?`, id).Scan(&authorID); err != nil {
		writeJSONError(w, "feedback not found", http.StatusNotFound)
		return
	}

	if authorID != u.ID && !u.IsMod() {
		writeJSONError(w, "unauthorized", http.StatusForbidden)
		return
	}

	h.db.Exec(`DELETE FROM user_feedback WHERE id = ?`, id)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// APIAdminFeedbackOptions handles GET (list all) and POST (create) for admin feedback option management.
func (h *Handlers) APIAdminFeedbackOptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := h.db.Query(`
			SELECT id, label, sentiment, sort_order, enabled
			FROM feedback_options
			ORDER BY sort_order, id`)
		if err != nil {
			writeJSONError(w, "query error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		opts := []feedbackOptionRow{}
		for rows.Next() {
			var o feedbackOptionRow
			var enabledInt int
			if rows.Scan(&o.ID, &o.Label, &o.Sentiment, &o.SortOrder, &enabledInt) == nil {
				o.Enabled = enabledInt > 0
				opts = append(opts, o)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opts)

	case http.MethodPost:
		var body struct {
			Label     string `json:"label"`
			Sentiment string `json:"sentiment"`
			SortOrder int    `json:"sort_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		label := strings.TrimSpace(body.Label)
		if label == "" || len(label) > 120 {
			writeJSONError(w, "label required (max 120 chars)", http.StatusBadRequest)
			return
		}
		sentiment := body.Sentiment
		if sentiment != "positive" && sentiment != "neutral" && sentiment != "negative" {
			writeJSONError(w, "sentiment must be positive, neutral, or negative", http.StatusBadRequest)
			return
		}
		res, err := h.db.Exec(`INSERT INTO feedback_options (label, sentiment, sort_order) VALUES (?, ?, ?)`,
			label, sentiment, body.SortOrder)
		if err != nil {
			writeJSONError(w, "could not create option", http.StatusInternalServerError)
			return
		}
		newID, _ := res.LastInsertId()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": newID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// APIAdminFeedbackOption handles PUT (update) and DELETE for a single feedback option.
func (h *Handlers) APIAdminFeedbackOption(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		// POINTERS, for the same reason AdminAwardUpdate uses them: a body carrying
		// only {"enabled": false} used to blank the label as well, because Go decodes
		// an absent key as the zero value and this wrote every column unconditionally.
		var body struct {
			Label     *string `json:"label"`
			Sentiment *string `json:"sentiment"`
			SortOrder *int    `json:"sort_order"`
			Enabled   *bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}

		sets := make([]string, 0, 4)
		args := make([]any, 0, 5)
		set := func(col string, v any) {
			sets = append(sets, col+" = ?")
			args = append(args, v)
		}
		if body.Label != nil {
			label := strings.TrimSpace(*body.Label)
			if label == "" || len(label) > 120 {
				writeJSONError(w, "label required (max 120 chars)", http.StatusBadRequest)
				return
			}
			set("label", label)
		}
		if body.Sentiment != nil {
			if *body.Sentiment != "positive" && *body.Sentiment != "neutral" && *body.Sentiment != "negative" {
				writeJSONError(w, "sentiment must be positive, neutral, or negative", http.StatusBadRequest)
				return
			}
			set("sentiment", *body.Sentiment)
		}
		if body.SortOrder != nil {
			set("sort_order", *body.SortOrder)
		}
		if body.Enabled != nil {
			enabledInt := 0
			if *body.Enabled {
				enabledInt = 1
			}
			set("enabled", enabledInt)
		}
		if len(sets) == 0 {
			writeJSONError(w, "no fields to update", http.StatusBadRequest)
			return
		}

		args = append(args, id)
		if _, err := h.db.Exec(`UPDATE feedback_options SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			writeJSONError(w, "could not update option", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	case http.MethodDelete:
		h.db.Exec(`DELETE FROM feedback_options WHERE id = ?`, id)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
