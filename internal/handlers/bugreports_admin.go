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

// Admin/staff side of the bug report system. Viewing a thread, replying (public
// or internal), and inviting reuse the participant endpoints in bugreports.go
// (staff bypass the participant check via reportAccess). This file adds the
// staff-only list, status changes, label assignment, and label management.

type adminBugReportItem struct {
	ID             uint          `json:"id"`
	Type           string        `json:"type"`
	Subject        string        `json:"subject"`
	Reason         string        `json:"reason"`
	ReportedUser   string        `json:"reported_user"`
	Status         string        `json:"status"`
	Priority       string        `json:"priority"`
	LastActivityAt string        `json:"last_activity_at"`
	Reporter       string        `json:"reporter"`
	Assignee       string        `json:"assignee"`
	Rating         string        `json:"rating"`
	Awaiting       string        `json:"awaiting"` // "staff" = waiting on staff, "user" = waiting on the reporter
	Messages       int           `json:"messages"`
	Labels         []bugLabelDTO `json:"labels"`
}

// AdminBugReportsList lists all reports with optional filters (?status= ?label=
// ?assignee=me|<id> ?q=<search>) and sorting (?sort=recent|status|opener|awaiting|priority).
func (h *Handlers) AdminBugReportsList(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportType := r.URL.Query().Get("type")
	status := r.URL.Query().Get("status")
	labelID, _ := strconv.ParseUint(r.URL.Query().Get("label"), 10, 64)
	assignee := r.URL.Query().Get("assignee")
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	sortBy := r.URL.Query().Get("sort")

	// The awaiting subquery's ? is the first placeholder in the SQL text, so its
	// arg must lead the slice.
	args := []any{auth.SuperadminUser}
	query := `
		SELECT r.id, r.type, r.subject, r.reason, r.status, r.priority, r.last_activity_at,
		       COALESCE(ru.username, ''), COALESCE(rpu.username, ''), COALESCE(au.username, ''), r.rating,
		       (SELECT COUNT(*) FROM bug_report_messages m WHERE m.report_id = r.id) AS msgs,
		       COALESCE((
		         SELECT CASE WHEN u2.role IN ('moderator','admin') OR u2.username = ? THEN 'user' ELSE 'staff' END
		         FROM bug_report_messages m2 LEFT JOIN users u2 ON u2.id = m2.author_id
		         WHERE m2.report_id = r.id AND m2.visibility = 'public' AND m2.is_system = 0
		         ORDER BY m2.created_at DESC, m2.id DESC LIMIT 1
		       ), 'staff') AS awaiting
		FROM bug_reports r
		LEFT JOIN users ru ON ru.id = r.reporter_id
		LEFT JOIN users rpu ON rpu.id = r.reported_user_id
		LEFT JOIN users au ON au.id = r.assignee_id`

	var where []string
	if reportType == "bug" || reportType == "player" {
		where = append(where, "r.type = ?")
		args = append(args, reportType)
	}
	if status == "open" || status == "pending" || status == "resolved" || status == "closed" {
		where = append(where, "r.status = ?")
		args = append(args, status)
	}
	if labelID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM bug_report_label_map lm WHERE lm.report_id = r.id AND lm.label_id = ?)")
		args = append(args, labelID)
	}
	if assignee == "me" {
		where = append(where, "r.assignee_id = ?")
		args = append(args, u.ID)
	} else if assignee == "unassigned" {
		where = append(where, "r.assignee_id IS NULL")
	} else if aid, err := strconv.ParseUint(assignee, 10, 64); err == nil && aid > 0 {
		where = append(where, "r.assignee_id = ?")
		args = append(args, aid)
	}
	if search != "" {
		like := "%" + search + "%"
		where = append(where, "(r.subject LIKE ? OR EXISTS (SELECT 1 FROM bug_report_messages ms WHERE ms.report_id = r.id AND ms.body LIKE ?))")
		args = append(args, like, like)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	order := " ORDER BY r.last_activity_at DESC"
	switch sortBy {
	case "status":
		order = " ORDER BY FIELD(r.status,'open','pending','resolved','closed'), r.last_activity_at DESC"
	case "opener":
		order = " ORDER BY (ru.username IS NULL), ru.username ASC, r.last_activity_at DESC"
	case "awaiting":
		order = " ORDER BY awaiting ASC, r.last_activity_at DESC"
	case "priority":
		order = " ORDER BY FIELD(r.priority,'urgent','high','normal','low'), r.last_activity_at DESC"
	}
	query += order + " LIMIT 200"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []adminBugReportItem{}
	index := map[uint]int{}
	var ids []uint
	for rows.Next() {
		var it adminBugReportItem
		var lastActivity sql.NullTime
		if rows.Scan(&it.ID, &it.Type, &it.Subject, &it.Reason, &it.Status, &it.Priority, &lastActivity, &it.Reporter, &it.ReportedUser, &it.Assignee, &it.Rating, &it.Messages, &it.Awaiting) != nil {
			continue
		}
		if lastActivity.Valid {
			it.LastActivityAt = lastActivity.Time.UTC().Format(time.RFC3339)
		}
		if it.Reporter == "" {
			it.Reporter = "(anonymous)"
		}
		it.Labels = []bugLabelDTO{}
		index[it.ID] = len(items)
		ids = append(ids, it.ID)
		items = append(items, it)
	}

	for rid, labels := range h.labelsForReports(ids) {
		if i, ok := index[rid]; ok {
			items[i].Labels = labels
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// labelsForReports returns labels grouped by report id for the given ids.
func (h *Handlers) labelsForReports(ids []uint) map[uint][]bugLabelDTO {
	out := map[uint][]bugLabelDTO{}
	if len(ids) == 0 {
		return out
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := h.db.Query(`
		SELECT m.report_id, l.id, l.name, l.color
		FROM bug_report_label_map m JOIN bug_report_labels l ON l.id = m.label_id
		WHERE m.report_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY l.name`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var rid uint
		var lb bugLabelDTO
		if rows.Scan(&rid, &lb.ID, &lb.Name, &lb.Color) == nil {
			out[rid] = append(out[rid], lb)
		}
	}
	return out
}

// AdminBugReportStatus changes a report's status and records a system note.
func (h *Handlers) AdminBugReportStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
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

	if _, err := h.db.Exec(`UPDATE bug_reports SET status = ?, last_activity_at = NOW() WHERE id = ?`, body.Status, reportID); err != nil {
		writeJSONError(w, "could not update status", http.StatusInternalServerError)
		return
	}
	h.db.Exec(
		`INSERT INTO bug_report_messages (report_id, author_id, body, visibility, is_system) VALUES (?, ?, ?, 'public', 1)`,
		reportID, u.ID, u.Username+" set the status to "+body.Status,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBugReportLabelAdd attaches a label to a report.
func (h *Handlers) AdminBugReportLabelAdd(w http.ResponseWriter, r *http.Request) {
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		LabelID uint `json:"label_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LabelID == 0 {
		writeJSONError(w, "label_id required", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(
		`INSERT IGNORE INTO bug_report_label_map (report_id, label_id) VALUES (?, ?)`,
		reportID, body.LabelID,
	); err != nil {
		writeJSONError(w, "could not add label", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBugReportLabelRemove detaches a label from a report.
func (h *Handlers) AdminBugReportLabelRemove(w http.ResponseWriter, r *http.Request) {
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	labelID, err := strconv.ParseUint(chi.URLParam(r, "labelId"), 10, 64)
	if err != nil || labelID == 0 {
		writeJSONError(w, "invalid label id", http.StatusBadRequest)
		return
	}
	h.db.Exec(`DELETE FROM bug_report_label_map WHERE report_id = ? AND label_id = ?`, reportID, labelID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBugLabels handles GET (list all labels) and POST (create a custom label).
func (h *Handlers) AdminBugLabels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := h.db.Query(`SELECT id, name, color, builtin FROM bug_report_labels ORDER BY builtin DESC, name`)
		if err != nil {
			writeJSONError(w, "query error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		type labelRow struct {
			ID      uint   `json:"id"`
			Name    string `json:"name"`
			Color   string `json:"color"`
			Builtin bool   `json:"builtin"`
		}
		out := []labelRow{}
		for rows.Next() {
			var l labelRow
			var builtin int
			if rows.Scan(&l.ID, &l.Name, &l.Color, &builtin) == nil {
				l.Builtin = builtin == 1
				out = append(out, l)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)

	case http.MethodPost:
		name, color, ok := decodeLabelBody(w, r)
		if !ok {
			return
		}
		res, err := h.db.Exec(`INSERT INTO bug_report_labels (name, color, builtin) VALUES (?, ?, 0)`, name, color)
		if err != nil {
			writeJSONError(w, "could not create label (name may already exist)", http.StatusBadRequest)
			return
		}
		id, _ := res.LastInsertId()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// AdminBugLabel handles PUT (update) and DELETE for a single label. Built-in
// labels are protected: they can be neither edited nor deleted.
func (h *Handlers) AdminBugLabel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var builtin int
		if err := h.db.QueryRow(`SELECT builtin FROM bug_report_labels WHERE id = ?`, id).Scan(&builtin); err != nil {
			writeJSONError(w, "label not found", http.StatusNotFound)
			return
		}
		if builtin == 1 {
			writeJSONError(w, "built-in labels cannot be edited", http.StatusBadRequest)
			return
		}
		name, color, ok := decodeLabelBody(w, r)
		if !ok {
			return
		}
		if _, err := h.db.Exec(`UPDATE bug_report_labels SET name = ?, color = ? WHERE id = ?`, name, color, id); err != nil {
			writeJSONError(w, "could not update label", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	case http.MethodDelete:
		var builtin int
		if err := h.db.QueryRow(`SELECT builtin FROM bug_report_labels WHERE id = ?`, id).Scan(&builtin); err != nil {
			writeJSONError(w, "label not found", http.StatusNotFound)
			return
		}
		if builtin == 1 {
			writeJSONError(w, "built-in labels cannot be deleted", http.StatusBadRequest)
			return
		}
		h.db.Exec(`DELETE FROM bug_report_labels WHERE id = ?`, id)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// decodeLabelBody validates a label create/update payload.
func decodeLabelBody(w http.ResponseWriter, r *http.Request) (name, color string, ok bool) {
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return "", "", false
	}
	name = strings.TrimSpace(body.Name)
	color = strings.TrimSpace(body.Color)
	if name == "" || len(name) > 40 {
		writeJSONError(w, "name required (max 40 chars)", http.StatusBadRequest)
		return "", "", false
	}
	if color == "" {
		color = "#cccccc"
	}
	if len(color) != 7 || color[0] != '#' {
		writeJSONError(w, "color must be a hex value like #rrggbb", http.StatusBadRequest)
		return "", "", false
	}
	return name, color, true
}

// AdminStaffList returns active staff (for the assignee dropdown and filter).
func (h *Handlers) AdminStaffList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(
		`SELECT username, role FROM users WHERE disabled = 0 AND deleted_at IS NULL AND (role IN ('moderator','admin') OR username = ?) ORDER BY username`,
		auth.SuperadminUser,
	)
	if err != nil {
		writeJSONError(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type staffRow struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	out := []staffRow{}
	for rows.Next() {
		var s staffRow
		if rows.Scan(&s.Username, &s.Role) == nil {
			out = append(out, s)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// AdminBugReportAssign sets (or clears, with empty username) a report's assignee.
// The assignee must be a staff member; the new assignee is pushed a notification.
func (h *Handlers) AdminBugReportAssign(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(body.Username)

	if target == "" {
		h.db.Exec(`UPDATE bug_reports SET assignee_id = NULL WHERE id = ?`, reportID)
		h.systemNote(reportID, u.ID, u.Username+" unassigned this report")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
		return
	}

	var targetID uint
	var targetRole string
	if err := h.db.QueryRow(`SELECT id, role FROM users WHERE username = ? AND disabled = 0 AND deleted_at IS NULL`, target).Scan(&targetID, &targetRole); err != nil {
		writeJSONError(w, "user not found", http.StatusNotFound)
		return
	}
	if targetRole != "moderator" && targetRole != "admin" && target != auth.SuperadminUser {
		writeJSONError(w, "assignee must be a staff member", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE bug_reports SET assignee_id = ? WHERE id = ?`, targetID, reportID); err != nil {
		writeJSONError(w, "could not assign", http.StatusInternalServerError)
		return
	}
	h.systemNote(reportID, u.ID, u.Username+" assigned this report to "+target)
	go h.sendPushToUsers([]uint{targetID}, "Report assigned to you",
		"You were assigned report #"+strconv.FormatUint(uint64(reportID), 10),
		map[string]string{"report_id": strconv.FormatUint(uint64(reportID), 10)})

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBugReportPriority sets a report's priority.
func (h *Handlers) AdminBugReportPriority(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUserAPI(w, r)
	if !ok {
		return
	}
	reportID, ok := parseBugReportID(r)
	if !ok {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Priority != "low" && body.Priority != "normal" && body.Priority != "high" && body.Priority != "urgent" {
		writeJSONError(w, "invalid priority", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`UPDATE bug_reports SET priority = ? WHERE id = ?`, body.Priority, reportID); err != nil {
		writeJSONError(w, "could not update priority", http.StatusInternalServerError)
		return
	}
	h.systemNote(reportID, u.ID, u.Username+" set priority to "+body.Priority)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// AdminBugMacros handles GET (list) and POST (create) for canned responses.
func (h *Handlers) AdminBugMacros(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := h.db.Query(`SELECT id, title, body FROM bug_report_macros ORDER BY title`)
		if err != nil {
			writeJSONError(w, "query error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		type macroRow struct {
			ID    uint   `json:"id"`
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		out := []macroRow{}
		for rows.Next() {
			var m macroRow
			if rows.Scan(&m.ID, &m.Title, &m.Body) == nil {
				out = append(out, m)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)

	case http.MethodPost:
		u, ok := h.requireUserAPI(w, r)
		if !ok {
			return
		}
		title, mbody, ok := decodeMacroBody(w, r)
		if !ok {
			return
		}
		res, err := h.db.Exec(`INSERT INTO bug_report_macros (title, body, created_by) VALUES (?, ?, ?)`, title, mbody, u.ID)
		if err != nil {
			writeJSONError(w, "could not create canned response", http.StatusInternalServerError)
			return
		}
		id, _ := res.LastInsertId()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// AdminBugMacro handles PUT (update) and DELETE for a single canned response.
func (h *Handlers) AdminBugMacro(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		title, mbody, ok := decodeMacroBody(w, r)
		if !ok {
			return
		}
		if _, err := h.db.Exec(`UPDATE bug_report_macros SET title = ?, body = ? WHERE id = ?`, title, mbody, id); err != nil {
			writeJSONError(w, "could not update canned response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	case http.MethodDelete:
		h.db.Exec(`DELETE FROM bug_report_macros WHERE id = ?`, id)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// decodeMacroBody validates a canned-response create/update payload.
func decodeMacroBody(w http.ResponseWriter, r *http.Request) (title, body string, ok bool) {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return "", "", false
	}
	title = strings.TrimSpace(in.Title)
	body = strings.TrimSpace(in.Body)
	if title == "" || len(title) > 80 {
		writeJSONError(w, "title required (max 80 chars)", http.StatusBadRequest)
		return "", "", false
	}
	if body == "" {
		writeJSONError(w, "body required", http.StatusBadRequest)
		return "", "", false
	}
	return title, body, true
}
