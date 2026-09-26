package handlers

// The scheduled half of costume discovery: when a pass runs, and who gets told.
//
// The deciding lives in internal/costumes. This file owns the clock and the notification, because
// those need the database and the push notifier and that package must not.
//
// Why it exists at all: the drift check has been able to SEE new costumes for a long time, but it
// only ever ran when a superadmin pressed a button, and its cache was in memory so a restart
// forgot everything. f:GOGGLES_2026 was visible to it for months and nobody was ever told.

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/costumes"
)

const (
	// After the box has settled. The label GitHub sync already runs at two minutes, and a pass
	// loads the masterfile, so this one waits its turn.
	costumeDiscoverStartup = 5 * time.Minute

	// Hourly. A pass is about five GitHub calls plus the masterfile, which stays well inside even
	// the unauthenticated budget, and the asset cache collapses an admin's button press and a tick
	// that land together into one fetch.
	costumeDiscoverEvery = time.Hour
)

// costumeNamesCache is where suggested names are remembered between passes, beside pogodata's
// blobs. Without it an hourly job would re-ask Dittobase about the same dead pages forever.
func costumeNamesCache() string { return "cache/costume_names.json" }

// StartCostumeDiscovery runs a pass shortly after boot and then hourly, admitting what it can
// judge and raising a hand about what it cannot.
//
// Set COSTUME_DISCOVERY=0 to turn it off. The kill switch is a loud log line rather than a silent
// skip, because a costume nobody hears about is precisely the failure this exists to prevent.
func (h *Handlers) StartCostumeDiscovery() {
	if os.Getenv("COSTUME_DISCOVERY") == "0" {
		log.Printf("costume discovery: disabled by COSTUME_DISCOVERY=0; new costumes will NOT be found automatically")
		return
	}

	go func() {
		startup := time.NewTimer(costumeDiscoverStartup)
		defer startup.Stop()
		tick := time.NewTicker(costumeDiscoverEvery)
		defer tick.Stop()

		for {
			select {
			case <-startup.C:
				h.runCostumeDiscovery("startup")
			case <-tick.C:
				h.runCostumeDiscovery("tick")
			}
		}
	}()
}

// runCostumeDiscovery is one pass plus its alert. Errors are logged and never propagated: this is
// a background goroutine, and a failed pass just means trying again in an hour.
func (h *Handlers) runCostumeDiscovery(reason string) {
	rep, err := costumes.Discover(false, costumeNamesCache())
	if err != nil {
		log.Printf("costume discovery (%s): %v", reason, err)
		return
	}
	for _, note := range rep.Notes {
		log.Printf("costume discovery (%s): %s", reason, note)
	}
	h.alertNewCostumes()
}

// alertNewCostumes tells admins about anything they have not already been told about.
//
// Order matters: the alert is marked as sent only AFTER it is sent. A crash in between re-alerts
// once on the next pass, which is the right way round to fail.
func (h *Handlers) alertNewCostumes() {
	pending := costumes.PendingAlerts()
	if len(pending) == 0 {
		return
	}

	var sent pushResult
	ids := h.adminUserIDs()
	if len(ids) > 0 {
		title, body := costumeAlertText(pending)
		// Already on the discovery goroutine, so no `go` here: sendPushToUsers is one blocking
		// round trip per device token, which for a handful of admins is fine, and marking the
		// alert sent has to happen after it actually went.
		sent = h.sendPushToUsersOnChannel(ids, title, body, map[string]string{
			"type":  "costume_review",
			"code":  pending[0].Code,
			"count": strconv.Itoa(len(pending)),
		}, pushChannelAdmin)
	}

	// An alert nobody received is not an alert. The record exists to stop an hourly job saying
	// the same thing every hour, so it may only record what actually arrived somewhere; stamping
	// a send that reached zero devices retires the notification permanently and leaves an
	// engineer editing a file on the server as the only way to raise it again. That is exactly
	// what happened on 2026-09-21, when every token on the site was a dead reinstall.
	//
	// So a pass that reaches nobody simply tries again next hour, which is also what makes a
	// tester installing the app at some unpredictable moment work without anyone coordinating
	// it: the next pass after their phone registers delivers the backlog.
	if !sent.Reached() {
		log.Printf("costume discovery: %d costume(s) need an admin, but the alert reached no device "+
			"(%d admin(s), %d device(s) tried, %d dropped as unregistered); trying again next pass",
			len(pending), len(ids), sent.Devices, sent.Dropped)
		return
	}

	codes := make([]string, 0, len(pending))
	for _, a := range pending {
		codes = append(codes, a.Code)
	}
	if err := costumes.MarkAlerted(codes); err != nil {
		log.Printf("costume discovery: mark alerted: %v", err)
	}
	log.Printf("costume discovery: %d costume(s) need an admin, told %d admin(s) on %d device(s)",
		len(pending), len(ids), sent.Delivered)
}

// costumeAlertText writes the notification. It names the costume where it can, because "Friede's
// Goggles needs a name" tells an admin what they are about to look at and "1 new code" does not.
func costumeAlertText(pending []costumes.Alert) (string, string) {
	named, asking := 0, 0
	for _, a := range pending {
		if a.Candidate {
			asking++
		} else {
			named++
		}
	}

	if len(pending) == 1 {
		a := pending[0]
		if a.Candidate {
			return "A costume needs a look", a.Label + " may be a costume, check the sprite"
		}
		return "New costume found", a.Label + " is ready to name"
	}

	var parts []string
	if named > 0 {
		parts = append(parts, fmt.Sprintf("%d need%s a name", named, plural(named, "s", "")))
	}
	if asking > 0 {
		parts = append(parts, fmt.Sprintf("%d need%s a look", asking, plural(asking, "s", "")))
	}
	return "New costumes found", strings.Join(parts, " and ")
}

// adminUserIDs is staffUserIDs without the moderators.
//
// The Costumes tab is RequireAdmin, so telling a moderator about a costume would send them to a
// page they cannot open. The superadmin is matched by username rather than role, as everywhere
// else, which also guarantees this is never empty on a live site.
func (h *Handlers) adminUserIDs() []uint {
	rows, err := h.db.Query(
		`SELECT id FROM users WHERE disabled = 0 AND deleted_at IS NULL AND (role = 'admin' OR username = ?)`,
		auth.SuperadminUser,
	)
	if err != nil {
		log.Printf("costumes: list admins: %v", err)
		return nil
	}
	defer rows.Close()

	var ids []uint
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
