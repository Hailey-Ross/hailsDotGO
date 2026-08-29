package server

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// These are source-level assertions rather than a walk of the built router, because
// New needs a live database and a loaded pogodata store: handlers.New calls
// loadTemplates, reloadLangs and reloadShinyOverrides before a single route exists.
//
// The thing worth guarding is the route table itself. Every write below is reachable
// from the app only because it is registered a second time inside /api/mobile/v1;
// on its web path the CSRF group rejects a Bearer request as a text/plain 403 before
// auth is ever consulted. Deleting one of these lines breaks an app screen and
// nothing else, which is exactly the kind of change that gets made by accident.

// mobileGroup returns the source of the /api/mobile/v1 route tree.
func mobileGroup(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test file")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(self), "server.go"))
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")

	start := strings.Index(src, `r.Route("/api/mobile/v1"`)
	if start < 0 {
		t.Fatal("could not find the /api/mobile/v1 group")
	}
	end := strings.Index(src[start:], "\n\tcsrfDebug :=")
	if end < 0 {
		t.Fatal("could not find the end of the /api/mobile/v1 group")
	}
	return src[start : start+end]
}

func TestMobileAliasesAreRegistered(t *testing.T) {
	group := mobileGroup(t)

	// Registered bare on purpose: the group middleware already gates this tree, and
	// four of these are wrapped in RequireAuth on the web, which answers a 303 to
	// /login rather than JSON.
	want := []string{
		// Matched without the leading r. because these two carry an r.With(...) limiter.
		`.Post("/bug-reports", h.CreateBugReport)`,
		`r.Get("/bug-reports", h.APIListBugReports)`,
		`r.Get("/bug-reports/{id}", h.APIGetBugReport)`,
		`r.Post("/bug-reports/{id}/messages", h.APIPostBugMessage)`,
		`r.Post("/bug-reports/{id}/invite", h.APIBugInvite)`,
		`r.Post("/bug-reports/{id}/status", h.APIBugReportStatus)`,
		`r.Post("/bug-reports/{id}/rating", h.APIBugReportRating)`,
		`r.Post("/social/{username}/friend", h.APIFriend)`,
		`r.Delete("/social/{username}/friend", h.APIFriend)`,
		`r.Post("/social/{username}/block", h.APIBlock)`,
		`r.Delete("/social/{username}/block", h.APIBlock)`,
		`r.Post("/feedback/{username}", h.APIPostFeedback)`,
		`r.Delete("/feedback/entry/{id}", h.APIDeleteFeedback)`,
		`r.Post("/awards/{id}/grant", h.APIAwardGrant)`,
		`r.Post("/store/tag-request", h.StoreTagRequestSubmit)`,
		`r.Post("/store/tag-color", h.StoreTagColorUpdate)`,
		`r.Post("/store/purchases/cancel", h.StorePurchaseCancel)`,
		`.Post("/player-reports", h.CreatePlayerReport)`,
		// Event reminders. The app's bells are inert without these three, and the
		// failure is quiet: the calls 404 and it falls back to its disk cache.
		`r.Get("/events/subscriptions", h.APIEventSubscriptions)`,
		`r.Put("/events/subscriptions/{eventId}", h.APIEventSubscribe)`,
		`r.Delete("/events/subscriptions/{eventId}", h.APIEventUnsubscribe)`,
	}
	for _, line := range want {
		if !strings.Contains(group, line) {
			t.Errorf("missing from the mobile group, so the app cannot reach it:\n  %s", line)
		}
	}
}

// APIFriend and APIBlock switch on r.Method inside the handler rather than being two
// handlers. Registering only the Post means a DELETE reaches them and falls out of
// the switch as a plain text 405, which reads to a client like the route is broken.
func TestMethodMultiplexedHandlersRegisterBothMethods(t *testing.T) {
	group := mobileGroup(t)

	for _, h := range []string{"h.APIFriend", "h.APIBlock"} {
		var post, del bool
		for _, line := range strings.Split(group, "\n") {
			if !strings.Contains(line, h+")") {
				continue
			}
			post = post || strings.Contains(line, "r.Post(")
			del = del || strings.Contains(line, "r.Delete(")
		}
		if !post || !del {
			t.Errorf("%s needs both Post and Delete registered (got post=%v delete=%v)", h, post, del)
		}
	}
}

// The group baseline is 120/min. Moderation endpoints carry the web route's tighter
// limit, restated at the registration; without it the alias silently relaxes by 20x.
func TestModerationAliasesKeepTheTighterRateLimit(t *testing.T) {
	group := mobileGroup(t)

	for _, handler := range []string{"h.CreatePlayerReport", "h.CreateBugReport"} {
		re := regexp.MustCompile(`r\.With\(httprate\.LimitByIP\((\d+), time\.Minute\)\)\.Post\([^)]*` + regexp.QuoteMeta(handler) + `\)`)
		m := re.FindStringSubmatch(group)
		if m == nil {
			t.Errorf("%s has no explicit rate limit, so it inherits the group's 120/min baseline", handler)
			continue
		}
		if m[1] != "6" {
			t.Errorf("%s rate limit = %s/min, want 6/min to match its web route", handler, m[1])
		}
	}
}
