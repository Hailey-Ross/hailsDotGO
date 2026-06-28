package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"pogo.hails.cc/internal/auth"
)

// Player ("bad actor") reports. These share the bug_reports ticket tables via
// type='player', carry a reported_user_id + reason, and route to a shared
// moderator bucket (all mods see them in the admin Player Reports tab). The
// reporter is a participant and can converse; the reported user never sees it.

// playerReportReasons maps a reason key to a short label used in the report
// subject. The full user-facing labels live in the i18n catalog.
var playerReportReasons = map[string]string{
	"spoofing":               "GPS spoofing",
	"botting":                "Botting / automation",
	"harassment":             "Harassment",
	"hate":                   "Hate speech",
	"threats":                "Threats / safety",
	"scam":                   "Scam / fraud",
	"impersonation":          "Impersonation",
	"inappropriate_username": "Inappropriate username",
	"inappropriate_profile":  "Inappropriate profile",
	"spam":                   "Spam",
	"other":                  "Other",
}

// usersByRole returns the IDs of active users holding exactly the given role.
func (h *Handlers) usersByRole(role string) []uint {
	rows, err := h.db.Query(`SELECT id FROM users WHERE role = ? AND disabled = 0`, role)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []uint
	for rows.Next() {
		var id uint
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// bucketStaffIDs returns the staff who own new player reports, by rank: all
// moderators; if none exist, all admins; if none, the configured superadmin.
func (h *Handlers) bucketStaffIDs() []uint {
	if ids := h.usersByRole("moderator"); len(ids) > 0 {
		return ids
	}
	if ids := h.usersByRole("admin"); len(ids) > 0 {
		return ids
	}
	var id uint
	if h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, auth.SuperadminUser).Scan(&id) == nil && id > 0 {
		return []uint{id}
	}
	return nil
}

// CreatePlayerReport files a report against another player.
func (h *Handlers) CreatePlayerReport(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)

	var body struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
		Details  string `json:"details"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(body.Username)
	reasonLabel, ok := playerReportReasons[body.Reason]
	if !ok {
		writeJSONError(w, "invalid reason", http.StatusBadRequest)
		return
	}
	details := strings.TrimSpace(body.Details)
	if details == "" {
		writeJSONError(w, "please describe the issue", http.StatusBadRequest)
		return
	}
	if len(details) > 4000 {
		details = details[:4000]
	}

	var reportedID uint
	if err := h.db.QueryRow(`SELECT id FROM users WHERE username = ? AND disabled = 0`, username).Scan(&reportedID); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if reportedID == u.ID {
		writeJSONError(w, "you cannot report yourself", http.StatusBadRequest)
		return
	}

	// Dedupe: reuse an existing open/pending report from this reporter on this user.
	var existingID uint
	if err := h.db.QueryRow(
		`SELECT id FROM bug_reports WHERE type = 'player' AND reporter_id = ? AND reported_user_id = ? AND status IN ('open','pending') LIMIT 1`,
		u.ID, reportedID,
	).Scan(&existingID); err == nil && existingID > 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": existingID, "duplicate": true})
		return
	}

	subject := "Player report: @" + username + " (" + reasonLabel + ")"

	tx, err := h.db.Begin()
	if err != nil {
		writeJSONError(w, "could not file report", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO bug_reports (type, reporter_id, reported_user_id, subject, reason) VALUES ('player', ?, ?, ?, ?)`,
		u.ID, reportedID, subject, body.Reason,
	)
	if err != nil {
		writeJSONError(w, "could not file report", http.StatusInternalServerError)
		return
	}
	reportID, _ := res.LastInsertId()

	if _, err := tx.Exec(
		`INSERT INTO bug_report_participants (report_id, user_id, role, last_seen_at) VALUES (?, ?, 'reporter', NOW())`,
		reportID, u.ID,
	); err != nil {
		writeJSONError(w, "could not file report", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility) VALUES (?, ?, ?, 'public')`,
		reportID, u.ID, details,
	); err != nil {
		writeJSONError(w, "could not file report", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSONError(w, "could not file report", http.StatusInternalServerError)
		return
	}

	go h.sendPushToUsers(h.bucketStaffIDs(), "New player report",
		"@"+username+" was reported",
		map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": reportID})
}

// AdminPlayerReportActioned closes a player report as actioned and thanks the
// reporter (push + a public thread message). Enforcement itself is done in the
// Users admin tab; this only records the outcome and notifies the reporter.
func (h *Handlers) AdminPlayerReportActioned(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var reportType string
	var reporterID sql.NullInt64
	if err := h.db.QueryRow(`SELECT type, reporter_id FROM bug_reports WHERE id = ?`, reportID).Scan(&reportType, &reporterID); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	if reportType != "player" {
		writeJSONError(w, "not a player report", http.StatusBadRequest)
		return
	}

	h.db.Exec(`UPDATE bug_reports SET status = 'closed', last_activity_at = NOW() WHERE id = ?`, reportID)
	h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'public', 1)`,
		reportID, u.ID, "Resolved: action was taken. Thank you for helping keep the community safe.",
	)
	if reporterID.Valid {
		go h.sendPushToUsers([]uint{uint(reporterID.Int64)}, "Thank you",
			"Your report helped remove a bad actor from the community. Thank you!",
			map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
