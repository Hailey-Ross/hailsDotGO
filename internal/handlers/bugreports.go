package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
)

// Bug report system ("Report Me Not"): lightweight ticketing with threaded
// messaging between reporters and staff, labels, multi-user invites, and
// private staff-only notes. Anonymous (logged-out) reporting is deferred; this
// file only handles logged-in reporters and participants. Staff management
// lives in bugreports_admin.go.

const maxReporterCollaborators = 4

type bugLabelDTO struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type bugReportListItem struct {
	ID             uint          `json:"id"`
	Type           string        `json:"type"`
	Subject        string        `json:"subject"`
	Status         string        `json:"status"`
	LastActivityAt string        `json:"last_activity_at"`
	Role           string        `json:"role"`
	ReporterName   string        `json:"reporter_username"`
	Unread         bool          `json:"unread"`
	Labels         []bugLabelDTO `json:"labels"`
}

type bugMessageDTO struct {
	ID         uint   `json:"id"`
	AuthorName string `json:"author_username"`
	AuthorID   uint   `json:"author_id"`
	IsStaff    bool   `json:"is_staff"`
	IsMine     bool   `json:"is_mine"`
	IsSystem   bool   `json:"is_system"`
	IsInternal bool   `json:"is_internal"`
	Body       string `json:"body"`
	CreatedAt  string `json:"created_at"`
}

type bugParticipantDTO struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// reportAccess returns the caller's participant role on a report, or "staff"
// when a moderator/admin accesses a report they are not a participant of. The
// boolean is false when the caller may not view the report at all.
func (h *Handlers) reportAccess(reportID, userID uint, u *auth.User) (string, bool) {
	var role string
	err := h.db.QueryRow(
		`SELECT role FROM bug_report_participants WHERE report_id = ? AND user_id = ?`,
		reportID, userID,
	).Scan(&role)
	if err == nil {
		return role, true
	}
	if u != nil && u.IsMod() {
		return "staff", true
	}
	return "", false
}

// staffUserIDs returns the IDs of all active staff (moderators, admins, and the
// configured superadmin) for notification fan-out.
func (h *Handlers) staffUserIDs() []uint {
	rows, err := h.db.Query(
		`SELECT id FROM users WHERE disabled = 0 AND deleted_at IS NULL AND (role IN ('moderator','admin') OR username = ?)`,
		auth.SuperadminUser,
	)
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

// reportParticipantIDs returns the user IDs participating in a report, optionally
// excluding one user (e.g. the author of a new message).
func (h *Handlers) reportParticipantIDs(reportID, exclude uint) []uint {
	rows, err := h.db.Query(`SELECT user_id FROM bug_report_participants WHERE report_id = ?`, reportID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []uint
	for rows.Next() {
		var id uint
		if rows.Scan(&id) == nil && id != exclude {
			ids = append(ids, id)
		}
	}
	return ids
}

// reportCounts returns how many reports the user participates in (any status --
// drives the persistent "Reports" nav link) and how many have unseen activity
// (drives the badge). Cheap enough to run on every page render.
func (h *Handlers) reportCounts(userID uint) (total, unread int) {
	rows, err := h.db.Query(`
		SELECT r.last_activity_at, p.last_seen_at
		FROM bug_report_participants p
		JOIN bug_reports r ON r.id = p.report_id
		WHERE p.user_id = ?`, userID)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	for rows.Next() {
		var lastActivity time.Time
		var lastSeen sql.NullTime
		if rows.Scan(&lastActivity, &lastSeen) != nil {
			continue
		}
		total++
		if !lastSeen.Valid || lastActivity.After(lastSeen.Time) {
			unread++
		}
	}
	return total, unread
}

// ReportsPage renders the dual-frame reports messenger for the logged-in user.
func (h *Handlers) ReportsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "reports", nil)
}

// CreateBugReport opens a new report for the logged-in caller. (Anonymous
// reporting is deferred; see the plan's "Deferred: anonymous reporting".)
func (h *Handlers) CreateBugReport(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	var body struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	subject := strings.TrimSpace(body.Subject)
	message := strings.TrimSpace(body.Body)
	if subject == "" || len(subject) > 160 {
		writeJSONErrorCode(w, "subject required (max 160 chars)", "subject_required", http.StatusBadRequest)
		return
	}
	if message == "" {
		writeJSONErrorCode(w, "description required", "description_required", http.StatusBadRequest)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		writeJSONError(w, "could not open report", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Reports from testers come in at a higher default priority so staff triage them sooner.
	priority := "normal"
	if u.IsTester() {
		priority = "high"
	}
	res, err := tx.Exec(`INSERT INTO bug_reports (reporter_id, subject, priority) VALUES (?, ?, ?)`, u.ID, subject, priority)
	if err != nil {
		writeJSONError(w, "could not open report", http.StatusInternalServerError)
		return
	}
	reportID, _ := res.LastInsertId()

	if _, err := tx.Exec(
		`INSERT INTO bug_report_participants (report_id, user_id, role, last_seen_at) VALUES (?, ?, 'reporter', NOW())`,
		reportID, u.ID,
	); err != nil {
		writeJSONError(w, "could not open report", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility) VALUES (?, ?, ?, 'public')`,
		reportID, u.ID, message,
	); err != nil {
		writeJSONError(w, "could not open report", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSONError(w, "could not open report", http.StatusInternalServerError)
		return
	}

	go h.notifyStaffNewReport(uint(reportID), subject, u.Username)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": reportID})
}

// APIListBugReports lists the reports the caller participates in.
func (h *Handlers) APIListBugReports(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}

	rows, err := h.db.Query(`
		SELECT r.id, r.type, r.subject, r.status, r.last_activity_at, p.role, p.last_seen_at, COALESCE(ru.username, '')
		FROM bug_report_participants p
		JOIN bug_reports r ON r.id = p.report_id
		LEFT JOIN users ru ON ru.id = r.reporter_id
		WHERE p.user_id = ?
		ORDER BY r.last_activity_at DESC`, u.ID)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []bugReportListItem{}
	index := map[uint]int{}
	for rows.Next() {
		var it bugReportListItem
		var lastActivity time.Time
		var lastSeen sql.NullTime
		if rows.Scan(&it.ID, &it.Type, &it.Subject, &it.Status, &lastActivity, &it.Role, &lastSeen, &it.ReporterName) != nil {
			continue
		}
		it.LastActivityAt = lastActivity.UTC().Format(time.RFC3339)
		it.Unread = !lastSeen.Valid || lastActivity.After(lastSeen.Time)
		it.Labels = []bugLabelDTO{}
		index[it.ID] = len(items)
		items = append(items, it)
	}

	// Attach labels for all of the caller's reports in one query.
	lrows, lerr := h.db.Query(`
		SELECT m.report_id, l.id, l.name, l.color
		FROM bug_report_label_map m
		JOIN bug_report_labels l ON l.id = m.label_id
		WHERE m.report_id IN (SELECT report_id FROM bug_report_participants WHERE user_id = ?)`, u.ID)
	if lerr == nil {
		defer lrows.Close()
		for lrows.Next() {
			var rid uint
			var lb bugLabelDTO
			if lrows.Scan(&rid, &lb.ID, &lb.Name, &lb.Color) == nil {
				if i, ok := index[rid]; ok {
					items[i].Labels = append(items[i].Labels, lb)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// APIGetBugReport returns the full thread for a report the caller can access.
// Internal (staff-only) messages are included only for staff viewers.
func (h *Handlers) APIGetBugReport(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	role, allowed := h.reportAccess(reportID, u.ID, u)
	if !allowed {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	isStaff := role == "staff" || u.IsMod()

	var subject, status, reporterName, priority, assignee, rating, ratingComment string
	var reportType, reason, reportedName string
	var ratedAt sql.NullTime
	if err := h.db.QueryRow(`
		SELECT r.type, r.reason, COALESCE(rpu.username, ''), r.subject, r.status, COALESCE(ru.username, ''), r.priority, COALESCE(au.username, ''), r.rating, r.rating_comment, r.rated_at
		FROM bug_reports r
		LEFT JOIN users ru ON ru.id = r.reporter_id
		LEFT JOIN users rpu ON rpu.id = r.reported_user_id
		LEFT JOIN users au ON au.id = r.assignee_id
		WHERE r.id = ?`, reportID).Scan(&reportType, &reason, &reportedName, &subject, &status, &reporterName, &priority, &assignee, &rating, &ratingComment, &ratedAt); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	ratedAtStr := ""
	if ratedAt.Valid {
		ratedAtStr = ratedAt.Time.UTC().Format(time.RFC3339)
	}

	messages := h.loadBugMessages(reportID, u.ID, isStaff)

	// Mark this report as seen for the caller.
	h.db.Exec(`UPDATE bug_report_participants SET last_seen_at = NOW() WHERE report_id = ? AND user_id = ?`, reportID, u.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":                reportID,
		"type":              reportType,
		"reason":            reason,
		"reported_username": reportedName,
		"subject":           subject,
		"status":            status,
		"priority":          priority,
		"assignee":          assignee,
		"rating":            rating,
		"rating_comment":    ratingComment,
		"rated_at":          ratedAtStr,
		"reporter_username": reporterName,
		"role":              role,
		"is_staff":          isStaff,
		"messages":          messages,
		"participants":      h.loadBugParticipants(reportID),
		"labels":            h.loadBugLabels(reportID),
	})
}

// loadBugMessages returns the thread messages, hiding internal notes from non-staff.
func (h *Handlers) loadBugMessages(reportID, viewerID uint, isStaff bool) []bugMessageDTO {
	q := `
		SELECT m.id, m.author_id, COALESCE(u.username, ''), COALESCE(u.role, ''), COALESCE(u.username, ''),
		       m.body, m.visibility, m.is_system, m.created_at
		FROM bug_report_messages m
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.report_id = ?`
	if !isStaff {
		q += ` AND m.visibility = 'public'`
	}
	q += ` ORDER BY m.created_at ASC, m.id ASC`

	rows, err := h.db.Query(q, reportID)
	if err != nil {
		return []bugMessageDTO{}
	}
	defer rows.Close()

	out := []bugMessageDTO{}
	for rows.Next() {
		var m bugMessageDTO
		var authorID sql.NullInt64
		var username, role, uname2, visibility string
		var isSystem int
		var createdAt time.Time
		if rows.Scan(&m.ID, &authorID, &username, &role, &uname2, &m.Body, &visibility, &isSystem, &createdAt) != nil {
			continue
		}
		if authorID.Valid {
			m.AuthorID = uint(authorID.Int64)
		}
		m.AuthorName = username
		if m.AuthorName == "" {
			m.AuthorName = "System"
		}
		m.IsStaff = role == "moderator" || role == "admin" || username == auth.SuperadminUser
		m.IsMine = authorID.Valid && uint(authorID.Int64) == viewerID
		m.IsSystem = isSystem == 1
		m.IsInternal = visibility == "internal"
		m.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		out = append(out, m)
	}
	return out
}

func (h *Handlers) loadBugParticipants(reportID uint) []bugParticipantDTO {
	rows, err := h.db.Query(`
		SELECT u.username, p.role
		FROM bug_report_participants p JOIN users u ON u.id = p.user_id
		WHERE p.report_id = ?
		ORDER BY FIELD(p.role,'reporter','collaborator','staff'), u.username`, reportID)
	if err != nil {
		return []bugParticipantDTO{}
	}
	defer rows.Close()
	out := []bugParticipantDTO{}
	for rows.Next() {
		var p bugParticipantDTO
		if rows.Scan(&p.Username, &p.Role) == nil {
			out = append(out, p)
		}
	}
	return out
}

func (h *Handlers) loadBugLabels(reportID uint) []bugLabelDTO {
	rows, err := h.db.Query(`
		SELECT l.id, l.name, l.color
		FROM bug_report_label_map m JOIN bug_report_labels l ON l.id = m.label_id
		WHERE m.report_id = ?
		ORDER BY l.name`, reportID)
	if err != nil {
		return []bugLabelDTO{}
	}
	defer rows.Close()
	out := []bugLabelDTO{}
	for rows.Next() {
		var lb bugLabelDTO
		if rows.Scan(&lb.ID, &lb.Name, &lb.Color) == nil {
			out = append(out, lb)
		}
	}
	return out
}

// APIPostBugMessage appends a reply to a report. Non-staff posts are always
// public; staff may post an internal (private) note via visibility=internal.
func (h *Handlers) APIPostBugMessage(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	role, allowed := h.reportAccess(reportID, u.ID, u)
	if !allowed {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	isStaff := role == "staff" || u.IsMod()

	var body struct {
		Body       string `json:"body"`
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(body.Body)
	if message == "" {
		writeJSONErrorCode(w, "message required", "message_required", http.StatusBadRequest)
		return
	}
	visibility := "public"
	if isStaff && body.Visibility == "internal" {
		visibility = "internal"
	}

	if _, err := h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility) VALUES (?, ?, ?, ?)`,
		reportID, u.ID, message, visibility,
	); err != nil {
		writeJSONError(w, "could not post message", http.StatusInternalServerError)
		return
	}

	// Public replies bump activity and reopen a resolved/closed report.
	if visibility == "public" {
		h.db.Exec(`UPDATE bug_reports
			SET last_activity_at = NOW(),
			    status = CASE WHEN status IN ('resolved','closed') THEN 'open' ELSE status END
			WHERE id = ?`, reportID)
		go h.notifyReportReply(reportID, u.Username, u.ID, isStaff)
	} else {
		h.db.Exec(`UPDATE bug_reports SET last_activity_at = NOW() WHERE id = ?`, reportID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// APIBugInvite adds another user to a report. The reporter and collaborators may
// invite up to maxReporterCollaborators collaborators total; staff may invite anyone.
func (h *Handlers) APIBugInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	role, allowed := h.reportAccess(reportID, u.ID, u)
	if !allowed {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	isStaff := role == "staff" || u.IsMod()

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(body.Username)
	if target == "" {
		writeJSONErrorCode(w, "username required", "username_required", http.StatusBadRequest)
		return
	}

	var targetID uint
	var targetRole string
	if err := h.db.QueryRow(`SELECT id, role FROM users WHERE username = ? AND disabled = 0 AND deleted_at IS NULL`, target).Scan(&targetID, &targetRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}

	// Already a participant?
	var existing int
	h.db.QueryRow(`SELECT COUNT(*) FROM bug_report_participants WHERE report_id = ? AND user_id = ?`, reportID, targetID).Scan(&existing)
	if existing > 0 {
		writeJSONErrorCode(w, "user is already on this report", "already_participant", http.StatusBadRequest)
		return
	}

	// A reporter must not be able to pull the person they reported into the thread.
	// Nothing stopped it before, and the consequences are worse than they first look:
	// it hands the accused the full contents of the report against them, and it turns
	// the ticket into a channel the reporter can use to reach someone who may well
	// have blocked them. Staff keep the ability, since a moderator may have a genuine
	// reason to bring both sides together.
	if !isStaff {
		var reportedUserID sql.NullInt64
		h.db.QueryRow(`SELECT reported_user_id FROM bug_reports WHERE id = ?`, reportID).Scan(&reportedUserID)
		if reportedUserID.Valid && uint(reportedUserID.Int64) == targetID {
			writeJSONError(w, "you cannot invite the person this report is about", http.StatusForbidden)
			return
		}
	}

	// Determine the invited user's role and enforce the collaborator cap for non-staff inviters.
	invitedRole := "collaborator"
	targetIsStaff := targetRole == "moderator" || targetRole == "admin" || target == auth.SuperadminUser
	if isStaff && targetIsStaff {
		invitedRole = "staff"
	}
	if !isStaff {
		var collabCount int
		h.db.QueryRow(`SELECT COUNT(*) FROM bug_report_participants WHERE report_id = ? AND role = 'collaborator'`, reportID).Scan(&collabCount)
		if collabCount >= maxReporterCollaborators {
			writeJSONErrorCode(w, "invite limit reached (max 4)", "invite_limit", http.StatusBadRequest)
			return
		}
	}

	if _, err := h.db.Exec(
		`INSERT INTO bug_report_participants (report_id, user_id, role, added_by) VALUES (?, ?, ?, ?)`,
		reportID, targetID, invitedRole, u.ID,
	); err != nil {
		writeJSONError(w, "could not invite user", http.StatusInternalServerError)
		return
	}

	h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'public', 1)`,
		reportID, u.ID, u.Username+" invited "+target+" to the report",
	)
	h.db.Exec(`UPDATE bug_reports SET last_activity_at = NOW() WHERE id = ?`, reportID)
	go h.sendPushToUsers([]uint{targetID}, "Added to a bug report", "You were added to a bug report", map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// systemNote records a staff-internal generated event (assignment, priority).
// Internal visibility keeps it off the reporter's view and it does not bump
// activity, so it never marks the report unread for the reporter.
func (h *Handlers) systemNote(reportID, authorID uint, text string) {
	h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'internal', 1)`,
		reportID, authorID, text,
	)
}

// APIBugReportStatus lets a participant change status. Non-staff (reporter or
// collaborator) may only resolve or reopen; staff get the full set via the admin
// endpoint, but this also accepts staff for convenience.
func (h *Handlers) APIBugReportStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	role, allowed := h.reportAccess(reportID, u.ID, u)
	if !allowed {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	isStaff := role == "staff" || u.IsMod()

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Status != "open" && body.Status != "pending" && body.Status != "resolved" && body.Status != "closed" {
		writeJSONError(w, "invalid status", http.StatusBadRequest)
		return
	}
	if !isStaff && body.Status != "resolved" && body.Status != "open" {
		writeJSONError(w, "you can only resolve or reopen a report", http.StatusForbidden)
		return
	}

	h.db.Exec(`UPDATE bug_reports SET status = ?, last_activity_at = NOW() WHERE id = ?`, body.Status, reportID)
	h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'public', 1)`,
		reportID, u.ID, u.Username+" marked this report "+body.Status,
	)
	if body.Status == "open" && !isStaff {
		go h.sendPushToUsers(h.staffUserIDs(), "Report reopened",
			u.Username+" reopened report #"+strconv.FormatUint(uint64(reportID), 10),
			map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// APIBugReportRating records the reporter's satisfaction rating on a resolved or
// closed report. Only the reporter, only once.
func (h *Handlers) APIBugReportRating(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var reporterID sql.NullInt64
	var status, rating string
	var ratedAt sql.NullTime
	if err := h.db.QueryRow(`SELECT reporter_id, status, rating, rated_at FROM bug_reports WHERE id = ?`, reportID).Scan(&reporterID, &status, &rating, &ratedAt); err != nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	if !reporterID.Valid || uint(reporterID.Int64) != u.ID {
		writeJSONError(w, "only the reporter can rate this report", http.StatusForbidden)
		return
	}

	firstTime := rating == ""
	if firstTime {
		// A first rating requires the report to be resolved or closed.
		if status != "resolved" && status != "closed" {
			writeJSONError(w, "you can only rate a resolved report", http.StatusBadRequest)
			return
		}
	} else {
		// Changes are allowed for one week after the initial rating.
		if !ratedAt.Valid || time.Since(ratedAt.Time) > 7*24*time.Hour {
			writeJSONError(w, "the rating period has closed", http.StatusBadRequest)
			return
		}
	}

	var body struct {
		Rating  string `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Rating != "good" && body.Rating != "bad" {
		writeJSONError(w, "rating must be good or bad", http.StatusBadRequest)
		return
	}
	comment := strings.TrimSpace(body.Comment)
	comment = truncRunes(comment, 500)

	if firstTime {
		// rated_at anchors the one-week change window; set it once.
		h.db.Exec(`UPDATE bug_reports SET rating = ?, rating_comment = ?, rated_at = NOW() WHERE id = ?`, body.Rating, comment, reportID)
		h.db.Exec(
			`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'internal', 1)`,
			reportID, u.ID, "Reporter rated this resolution: "+body.Rating,
		)
	} else {
		h.db.Exec(`UPDATE bug_reports SET rating = ?, rating_comment = ? WHERE id = ?`, body.Rating, comment, reportID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// parseBugReportID extracts and validates the {id} URL param.
func parseBugReportID(r *http.Request) (uint, bool) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

// notifyStaffNewReport pushes a new-report alert to all staff.
func (h *Handlers) notifyStaffNewReport(reportID uint, subject, reporter string) {
	h.sendPushToUsers(h.staffUserIDs(), "New bug report",
		reporter+": "+subject,
		map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})
}

// notifyReportReply pushes a reply alert to the other participants, plus staff
// when the author is not staff (so reports stay on staff radar).
func (h *Handlers) notifyReportReply(reportID uint, author string, authorID uint, authorIsStaff bool) {
	recipients := h.reportParticipantIDs(reportID, authorID)
	if !authorIsStaff {
		seen := map[uint]bool{}
		for _, id := range recipients {
			seen[id] = true
		}
		for _, id := range h.staffUserIDs() {
			if id != authorID && !seen[id] {
				recipients = append(recipients, id)
			}
		}
	}
	if len(recipients) == 0 {
		return
	}
	h.sendPushToUsers(recipients, "New reply on a bug report",
		author+" replied",
		map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})
}
