package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// MobileRaidOverview returns the raid overview shaped for the mobile data model.
// Response: {"bosses":[{"name","tier","queue_depth"}],"lobbies":[{"id","boss","tier","host","member_count","max_members","state"}]}
func (h *Handlers) MobileRaidOverview(w http.ResponseWriter, r *http.Request) {
	type mobileBoss struct {
		Name       string `json:"name"`
		Tier       uint8  `json:"tier"`
		QueueDepth int    `json:"queue_depth"`
	}
	type mobileLobby struct {
		ID          uint64 `json:"id"`
		Boss        string `json:"boss"`
		Tier        uint8  `json:"tier"`
		Host        string `json:"host"`
		MemberCount int    `json:"member_count"`
		MaxMembers  int    `json:"max_members"`
		State       string `json:"state"`
	}

	bossByName := map[string]*mobileBoss{}
	rows, err := h.db.Query(`
		SELECT boss_name, boss_tier, COUNT(*) FROM raid_lobbies
		WHERE state = 'open' AND expires_at > NOW() AND is_custom = 0
		GROUP BY boss_name, boss_tier`)
	if err == nil {
		for rows.Next() {
			var b mobileBoss
			var count int
			if rows.Scan(&b.Name, &b.Tier, &count) == nil {
				bossByName[b.Name] = &b
			}
		}
		rows.Close()
	}
	qrows, err := h.db.Query(`SELECT boss_name, MAX(boss_tier), COUNT(*) FROM raid_queue GROUP BY boss_name`)
	if err == nil {
		for qrows.Next() {
			var name string
			var tier uint8
			var n int
			if qrows.Scan(&name, &tier, &n) == nil {
				if b, ok := bossByName[name]; ok {
					b.QueueDepth = n
				} else {
					bossByName[name] = &mobileBoss{Name: name, Tier: tier, QueueDepth: n}
				}
			}
		}
		qrows.Close()
	}
	bosses := make([]mobileBoss, 0, len(bossByName))
	for _, b := range bossByName {
		bosses = append(bosses, *b)
	}

	lobbies := make([]mobileLobby, 0)
	lrows, err := h.db.Query(`
		SELECT l.id, l.boss_name, l.boss_tier, u.username, l.max_members, l.state,
		       (SELECT COUNT(*) FROM raid_lobby_members lm
		        WHERE lm.lobby_id = l.id AND lm.state IN ('matched','confirmed'))
		FROM raid_lobbies l JOIN users u ON u.id = l.host_id
		WHERE l.state IN ('open','full') AND l.expires_at > NOW() AND l.is_custom = 1
		ORDER BY l.created_at ASC`)
	if err == nil {
		for lrows.Next() {
			var lb mobileLobby
			if lrows.Scan(&lb.ID, &lb.Boss, &lb.Tier, &lb.Host, &lb.MaxMembers, &lb.State, &lb.MemberCount) == nil {
				lobbies = append(lobbies, lb)
			}
		}
		lrows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"bosses": bosses, "lobbies": lobbies})
}

// MobileRaidState returns the user's current raid state shaped for the mobile data model.
// Response: {"state":"idle|queued|matched|confirmed|raiding|reported","lobby_id"?,"boss"?,"confirm_deadline"?,"members"?,"role"?}
func (h *Handlers) MobileRaidState(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}

	h.lazyExpireFor(u.ID)

	type mobileStateMember struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
		State    string `json:"state"`
	}

	out := map[string]any{"state": "idle"}

	if lobbyID := h.hostingLobbyID(u.ID); lobbyID > 0 {
		lb := h.buildLobbyState(lobbyID, u, true)
		if lb != nil {
			members := make([]mobileStateMember, 0, len(lb.Members))
			for _, m := range lb.Members {
				members = append(members, mobileStateMember{ID: m.UserID, Username: m.Username, State: m.State})
			}
			out["state"] = "raiding"
			out["role"] = "host"
			out["lobby_id"] = lb.ID
			out["boss"] = lb.BossName
			out["members"] = members
		}
	} else if lobbyID := h.memberLobbyID(u.ID); lobbyID > 0 {
		lb := h.buildLobbyState(lobbyID, u, false)
		if lb != nil {
			members := make([]mobileStateMember, 0, len(lb.Members))
			for _, m := range lb.Members {
				members = append(members, mobileStateMember{ID: m.UserID, Username: m.Username, State: m.State})
			}
			out["role"] = "member"
			out["lobby_id"] = lb.ID
			out["boss"] = lb.BossName
			out["members"] = members
			if lb.MyState == "matched" {
				out["state"] = "matched"
				if lb.MyDeadline != nil {
					out["confirm_deadline"] = lb.MyDeadline.UTC().Format(time.RFC3339)
				}
			} else {
				out["state"] = "confirmed"
			}
		}
	} else if q := h.queueState(u.ID); q != nil {
		out["state"] = "queued"
		out["boss"] = q.BossName
	} else {
		var feedbackCount int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM raid_lobby_members lm
			JOIN raid_lobbies l ON l.id = lm.lobby_id
			WHERE lm.user_id = ? AND lm.state = 'confirmed' AND lm.attended = 1
			  AND lm.host_vote = 'none' AND lm.raid_success IS NULL
			  AND l.state = 'reported' AND l.closed_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)`, u.ID).Scan(&feedbackCount)
		if feedbackCount > 0 {
			out["state"] = "reported"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// MobileRaidLobbyReport accepts the mobile flat-array report format:
// {"attended":[id,...],"left_early":[id,...]}
func (h *Handlers) MobileRaidLobbyReport(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Attended  []uint `json:"attended"`
		LeftEarly []uint `json:"left_early"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}

	res, err := h.db.Exec(
		`UPDATE raid_lobbies SET state = 'reported', closed_at = NOW() WHERE id = ? AND host_id = ? AND state = 'raiding'`,
		id, u.ID)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_no_report_due"), http.StatusConflict)
		return
	}

	leftEarlySet := make(map[uint]bool, len(body.LeftEarly))
	for _, uid := range body.LeftEarly {
		leftEarlySet[uid] = true
	}

	attendedAny := false
	var attendedIDs []uint
	for _, uid := range body.Attended {
		attended := true
		leftEarly := leftEarlySet[uid]
		res, err := h.db.Exec(`
			UPDATE raid_lobby_members SET attended = ?, left_early = ?
			WHERE lobby_id = ? AND user_id = ? AND state = 'confirmed'`,
			attended, leftEarly, id, uid)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		attendedAny = true
		attendedIDs = append(attendedIDs, uid)
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpJoinComplete, uid)
		if leftEarly {
			weight := h.peerReportWeight(u, uid)
			h.insertTrustEvent(uid, u.ID, id, "left_early", trustDeltaLeftEarly, weight, "host marked left early")
		}
	}
	if attendedAny {
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + ?, last_raid_at = NOW() WHERE id = ?`, xpHostComplete, u.ID)
	}
	if len(attendedIDs) > 0 {
		var bossName string
		h.db.QueryRow(`SELECT boss_name FROM raid_lobbies WHERE id = ?`, id).Scan(&bossName)
		lid := fmt.Sprint(id)
		go h.sendPushToUsers(attendedIDs,
			"Rate Your Host",
			"Your "+bossName+" raid is complete -- tap to rate your host",
			map[string]string{"type": "feedback_requested", "lobby_id": lid},
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// MobileRaidLobbyFeedback accepts a star rating from the mobile app:
// {"option_id": 1-5}
// Maps: 4-5 → commend+success, 3 → none+success, 1-2 → dislike+fail
func (h *Handlers) MobileRaidLobbyFeedback(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		OptionID int `json:"option_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.OptionID < 1 || body.OptionID > 5 {
		writeJSONError(w, "option_id must be 1-5", http.StatusBadRequest)
		return
	}

	var hostVote string
	var raidSuccess bool
	switch {
	case body.OptionID >= 4:
		hostVote, raidSuccess = "commend", true
	case body.OptionID == 3:
		hostVote, raidSuccess = "none", true
	default:
		hostVote, raidSuccess = "dislike", false
	}

	var hostID uint
	if err := h.db.QueryRow(`
		SELECT host_id FROM raid_lobbies
		WHERE id = ? AND state = 'reported' AND closed_at > DATE_SUB(NOW(), INTERVAL 24 HOUR)`, id).Scan(&hostID); err != nil {
		writeJSONError(w, h.t(r, "error.lobby_feedback_closed"), http.StatusGone)
		return
	}

	res, err := h.db.Exec(`
		UPDATE raid_lobby_members SET raid_success = ?, host_vote = ?
		WHERE lobby_id = ? AND user_id = ? AND state = 'confirmed' AND attended = 1
		  AND raid_success IS NULL AND host_vote = 'none'`,
		raidSuccess, hostVote, id, u.ID)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.lobby_feedback_done"), http.StatusConflict)
		return
	}

	weight := h.peerReportWeight(u, hostID)
	if raidSuccess {
		h.insertTrustEvent(hostID, u.ID, id, "raid_success", trustDeltaRaidSuccess, weight, "")
	}
	switch hostVote {
	case "commend":
		h.insertTrustEvent(hostID, u.ID, id, "commend", trustDeltaCommend, weight, "")
		h.db.Exec(`UPDATE users SET raid_xp = raid_xp + 5 WHERE id = ?`, hostID)
	case "dislike":
		h.insertTrustEvent(hostID, u.ID, id, "dislike", trustDeltaDislike, weight, "")
	}
	if hostVote != "none" {
		h.recomputeReliability(u)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
