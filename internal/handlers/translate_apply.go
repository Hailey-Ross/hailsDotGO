package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"
)

type translatorApplication struct {
	ID           uint
	UserID       uint
	Languages    string // raw JSON: [{"code":"de","level":"fluent"}]
	Motivation   string
	Experience   string
	Country      string
	Status       string // pending | reviewing | accepted | rejected
	RejectReason string
	CreatedAt    time.Time
}

type translatePageData struct {
	State       string                 // "workspace" | "form" | "status" | "landing"
	Application *translatorApplication // non-nil only for "status" state
	Error       string                 // validation error for form re-render
}

func (h *Handlers) loadTranslatorApplication(userID uint) (*translatorApplication, error) {
	var app translatorApplication
	var reviewedBy sql.NullInt64
	var reviewedAt sql.NullTime
	err := h.db.QueryRow(`
		SELECT id, user_id, languages, motivation, experience, country,
		       status, reviewed_by, reviewed_at, COALESCE(reject_reason,''), created_at
		FROM translator_applications WHERE user_id = ?`, userID,
	).Scan(&app.ID, &app.UserID, &app.Languages, &app.Motivation, &app.Experience,
		&app.Country, &app.Status, &reviewedBy, &reviewedAt, &app.RejectReason, &app.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &app, err
}

var supportedApplyLangs = []string{"de", "es", "fr", "ja", "pt", "it", "nl", "ko", "zh", "pl", "ru", "tr", "ar"}

func (h *Handlers) TranslatorApply(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/translate", http.StatusSeeOther)
		return
	}
	if u.IsTranslator() {
		http.Redirect(w, r, "/translate", http.StatusSeeOther)
		return
	}
	if !h.maintenanceSettings().TranslatorAppsEnabled {
		http.Redirect(w, r, "/translate", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	type langEntry struct {
		Code  string `json:"code"`
		Level string `json:"level"`
	}

	validLevels := map[string]bool{"native": true, "fluent": true, "intermediate": true}
	entries := []langEntry{}
	for _, code := range supportedApplyLangs {
		if r.FormValue("lang_"+code) == "1" {
			level := r.FormValue("level_" + code)
			if !validLevels[level] {
				level = "intermediate"
			}
			entries = append(entries, langEntry{code, level})
		}
	}
	if other := strings.TrimSpace(r.FormValue("lang_other_text")); other != "" {
		if len(other) > 50 {
			other = other[:50]
		}
		level := r.FormValue("level_other")
		if !validLevels[level] {
			level = "intermediate"
		}
		entries = append(entries, langEntry{"other:" + other, level})
	}

	motivation := strings.TrimSpace(r.FormValue("motivation"))
	experience := strings.TrimSpace(r.FormValue("experience"))
	country := strings.TrimSpace(r.FormValue("country"))
	if len(experience) > 2000 {
		experience = experience[:2000]
	}
	if len(country) > 100 {
		country = country[:100]
	}

	renderFormError := func(msg string) {
		langsJSON, _ := json.Marshal(entries)
		h.render(w, r, "translate", translatePageData{
			State: "form",
			Application: &translatorApplication{
				Languages:  string(langsJSON),
				Motivation: motivation,
				Experience: experience,
				Country:    country,
			},
			Error: msg,
		})
	}

	if len(entries) == 0 {
		renderFormError(h.t(r, "translate.apply.err_no_langs"))
		return
	}
	if len(motivation) < 20 || len(motivation) > 2000 {
		renderFormError(h.t(r, "translate.apply.err_motivation"))
		return
	}

	langsJSON, _ := json.Marshal(entries)

	_, err := h.db.Exec(`
		INSERT INTO translator_applications (user_id, languages, motivation, experience, country)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, string(langsJSON), motivation, experience, country)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			http.Redirect(w, r, "/translate", http.StatusSeeOther)
			return
		}
		log.Printf("TranslatorApply: %v", err)
		http.Error(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/translate", http.StatusSeeOther)
}

type translatorAppRecord struct {
	ID           uint   `json:"id"`
	UserID       uint   `json:"user_id"`
	Username     string `json:"username"`
	Languages    string `json:"languages"`
	Motivation   string `json:"motivation"`
	Experience   string `json:"experience"`
	Country      string `json:"country"`
	Status       string `json:"status"`
	RejectReason string `json:"reject_reason"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handlers) AdminTranslatorAppsList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	validStatuses := map[string]bool{"pending": true, "reviewing": true, "accepted": true, "rejected": true, "all": true}
	if !validStatuses[status] {
		writeJSONError(w, h.t(r, "error.tl_invalid_status"), http.StatusBadRequest)
		return
	}

	var rows *sql.Rows
	var err error
	if status == "all" {
		rows, err = h.db.Query(`
			SELECT ta.id, ta.user_id, u.username, ta.languages, ta.motivation,
			       ta.experience, ta.country, ta.status, COALESCE(ta.reject_reason,''), ta.created_at
			FROM translator_applications ta JOIN users u ON u.id = ta.user_id
			ORDER BY FIELD(ta.status,'pending','reviewing','rejected','accepted'), ta.created_at ASC`)
	} else {
		rows, err = h.db.Query(`
			SELECT ta.id, ta.user_id, u.username, ta.languages, ta.motivation,
			       ta.experience, ta.country, ta.status, COALESCE(ta.reject_reason,''), ta.created_at
			FROM translator_applications ta JOIN users u ON u.id = ta.user_id
			WHERE ta.status = ?
			ORDER BY ta.created_at ASC`, status)
	}
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []translatorAppRecord{}
	for rows.Next() {
		var rec translatorAppRecord
		var createdAt time.Time
		if rows.Scan(&rec.ID, &rec.UserID, &rec.Username, &rec.Languages, &rec.Motivation,
			&rec.Experience, &rec.Country, &rec.Status, &rec.RejectReason, &createdAt) != nil {
			continue
		}
		rec.CreatedAt = createdAt.Format("2006-01-02")
		out = append(out, rec)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handlers) AdminTranslatorAppSetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, h.t(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	actor := h.currentUser(r)
	if actor == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	validStatuses := map[string]bool{"reviewing": true, "accepted": true, "rejected": true}
	if !validStatuses[body.Status] {
		writeJSONError(w, h.t(r, "error.tl_invalid_status"), http.StatusBadRequest)
		return
	}

	switch body.Status {
	case "rejected":
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			writeJSONError(w, h.t(r, "error.adm_reason_required"), http.StatusBadRequest)
			return
		}
		if len(reason) > 255 {
			reason = reason[:255]
		}
		res, err := h.db.Exec(`
			UPDATE translator_applications
			SET status = 'rejected', reject_reason = ?, reviewed_by = ?, reviewed_at = NOW()
			WHERE id = ? AND status IN ('pending','reviewing')`,
			reason, actor.ID, id)
		if err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
			return
		}

	case "accepted":
		tx, err := h.db.Begin()
		if err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		defer tx.Rollback() //nolint:errcheck

		var targetUserID uint
		if err := tx.QueryRow(`SELECT user_id FROM translator_applications WHERE id = ?`, id).Scan(&targetUserID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
			} else {
				writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			}
			return
		}
		if _, err := tx.Exec(`
			UPDATE translator_applications
			SET status = 'accepted', reviewed_by = ?, reviewed_at = NOW()
			WHERE id = ?`, actor.ID, id); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		if _, err := tx.Exec(`UPDATE users SET translator = 1 WHERE id = ?`, targetUserID); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}

	case "reviewing":
		res, err := h.db.Exec(`
			UPDATE translator_applications
			SET status = 'reviewing', reviewed_by = ?, reviewed_at = NOW()
			WHERE id = ? AND status = 'pending'`,
			actor.ID, id)
		if err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
