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
		// The trainer profile screen. Without these it renders a header and
		// nothing else: no feedback, no awards, no shiny collection.
		`r.Get("/feedback/{username}", h.APIGetFeedback)`,
		`r.Get("/feedback/options", h.MobileFeedbackOptions)`,
		`r.Get("/awards", h.APIAwardsList)`,
		`r.Get("/awards/of/{username}", h.APIAwardsOf)`,
		`r.Get("/shinies/of/{username}", h.APIShiniesOfUser)`,
		// Bare on purpose, like APIAwardGrant: RequireAuth answers a 303 on the
		// web path, and this handler makes its own 401. See the guard test below.
		`r.Get("/users/search", h.APIUsersSearch)`,
		`r.Get("/notifications", h.APIGetNotifications)`,
		`r.Get("/weather", h.APIWeather)`,
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

// Every admin route inside /api/mobile/v1 must go through one of the JSON role
// gates. This is the most important test in this file.
//
// Almost no admin handler checks its own role: AdminChangeRole parses an id,
// validates the role string, refuses the superadmin as a TARGET, and writes,
// without ever asking who the caller is. The route wrapper is the whole of its
// authority check. The mobile group's middleware proves only that the session is
// valid, so an admin handler registered bare there is reachable by any signed-in
// trainer, and AdminChangeRole registered bare is a self-service promotion.
//
// The HTML wrappers are not an alternative. They answer a 303 to /login with no
// session and a text/plain body on the wrong role, neither of which a JSON client
// can read, which is why the *API variants exist.
//
// A reviewer will not catch a bare registration in a block this long. This will.
func TestAdminMobileRoutesAreRoleGated(t *testing.T) {
	group := mobileGroup(t)

	gates := []string{"h.RequireModAPI(", "h.RequireAdminAPI(", "h.RequireSuperAdminAPI("}
	// Trailing paren on purpose: it is what keeps these from matching their own
	// *API variants, which are the correct thing to find.
	htmlGates := []string{"h.RequireMod(", "h.RequireAdmin(", "h.RequireSuperAdmin(", "h.RequireTranslator("}

	// Two ways in, because neither alone is enough. Matching the handler name
	// misses /raid-lobbies/{id}, which is gated but calls APIRaidLobbyCancel.
	// Matching the route path misses nothing inside the admin subtree but would
	// not catch an admin handler mounted somewhere else. A route caught by either
	// rule has to be gated.
	isAdminRoute := func(line string) bool {
		if strings.Contains(line, "h.Admin") {
			return true
		}
		// Inside r.Route("/admin", ...), so every registration in that block is an
		// admin route whatever it calls.
		return adminSubtree(group, line)
	}

	seen := 0
	for _, line := range joinRouteContinuations(group) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || !strings.Contains(trimmed, "r.") {
			continue
		}
		if !isAdminRoute(line) {
			continue
		}
		seen++

		var gated bool
		for _, g := range gates {
			gated = gated || strings.Contains(line, g)
		}
		if !gated {
			t.Errorf("admin route registered without a JSON role gate, so any signed-in trainer can reach it:\n  %s", trimmed)
		}
		for _, g := range htmlGates {
			if strings.Contains(line, g) {
				t.Errorf("admin route uses the HTML wrapper %s, which redirects and answers text/plain:\n  %s", g, trimmed)
			}
		}
	}

	t.Logf("checked %d admin route registrations in the mobile group", seen)
}

// Handlers registered bare inside the mobile group must carry their own auth
// check, because the group's middleware proves only that the session is valid.
//
// These four are bare for a specific reason: their web twins are wrapped in
// RequireAuth or RequireMod, which answer a 303 to /login rather than JSON. That
// makes the wrapper useless to a Bearer client and puts the whole of the check
// inside the handler, so the check has to actually be there. APIUsersSearch had
// none until 2026-08-31 and is a user enumeration oracle without one.
func TestBareMobileHandlersCheckAuthThemselves(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test file")
	}
	handlersDir := filepath.Join(filepath.Dir(self), "..", "handlers")

	for handler, file := range map[string]string{
		"APIUsersSearch": "awards.go",
		"APIAwardGrant":  "awards.go",
	} {
		raw, err := os.ReadFile(filepath.Join(handlersDir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		src := strings.ReplaceAll(string(raw), "\r\n", "\n")

		start := strings.Index(src, "func (h *Handlers) "+handler+"(")
		if start < 0 {
			t.Errorf("%s not found in %s", handler, file)
			continue
		}
		// The check is the first thing the handler does, so a short window is
		// enough and keeps this from passing on an unrelated check further down.
		window := src[start:min(start+400, len(src))]
		if !strings.Contains(window, "requireUserAPI") && !strings.Contains(window, "StatusUnauthorized") {
			t.Errorf("%s is registered bare in the mobile group but has no auth check of its own", handler)
		}
	}
}

// adminSubtree reports whether a registration line sits inside the
// r.Route("/admin", ...) block of the mobile group.
//
// Brace counting rather than a regex on the path, because the paths inside that
// block are relative ("/users/{id}/role") and carry no /admin prefix to match on.
func adminSubtree(group, line string) bool {
	start := strings.Index(group, `r.Route("/admin", func(r chi.Router) {`)
	if start < 0 {
		return false
	}
	idx := strings.Index(group, line)
	if idx < 0 || idx < start {
		return false
	}
	// Walk from the block's opening brace to the line, tracking depth. Reaching
	// the line at depth zero means the block already closed above it.
	depth := 0
	for _, c := range group[start:idx] {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth > 0
}

// The admin subtree must actually be there and actually be large. A refactor that
// dropped the block would leave TestAdminMobileRoutesAreRoleGated passing on zero
// routes, which is the failure mode of every "assert nothing is wrong" test.
func TestAdminMobileSubtreeIsPresent(t *testing.T) {
	group := mobileGroup(t)

	if !strings.Contains(group, `r.Route("/admin", func(r chi.Router) {`) {
		t.Fatal("the mobile group has no /admin subtree; the whole admin panel is unreachable from the app")
	}

	// Spot checks across the tabs, chosen because each is the only route that
	// makes its tab usable at all.
	for _, want := range []string{
		`r.Get("/context", h.RequireModAPI(h.MobileAdminContext))`,
		`r.Put("/settings", h.RequireAdminAPI(h.MobileAdminSettings))`,
		`r.Put("/pages", h.RequireAdminAPI(h.MobileAdminPages))`,
		`r.Put("/mobile-build", h.RequireAdminAPI(h.MobileAdminMobileBuild))`,
		`r.Post("/invites", h.RequireAdminAPI(h.MobileAdminCreateInvite))`,
		`r.Delete("/invites/{token}", h.RequireAdminAPI(h.MobileAdminCancelInvite))`,
		`r.Get("/users", h.RequireModAPI(h.AdminUsersAPI))`,
		`r.Get("/stats", h.RequireAdminAPI(h.AdminStatsAPI))`,
		`r.Get("/shiny-dex", h.RequireAdminAPI(h.AdminGetShinyDex))`,
		`r.Get("/raid-lobbies", h.RequireModAPI(h.AdminRaidLobbiesList))`,
	} {
		if !strings.Contains(group, want) {
			t.Errorf("missing from the admin subtree:\n  %s", want)
		}
	}
}

// The destructive and expensive routes keep the limiter their web twin carries. An
// alias that drops one relaxes it to the group's 120/min baseline, which for
// permanent account deletion is a twelvefold increase on a route that exists to be
// slow.
func TestAdminMobileRoutesKeepTheirLimiters(t *testing.T) {
	group := mobileGroup(t)

	for _, want := range []string{
		`r.With(httprate.LimitByIP(10, 5*time.Minute)).`,
		`Post("/users/{id}/delete", h.RequireSuperAdminAPI(h.AdminDeleteUser))`,
		`r.With(httprate.LimitByIP(5, time.Minute)).`,
		`Post("/check-costumes", h.RequireAdminAPI(h.AdminCheckCostumes))`,
	} {
		if !strings.Contains(group, want) {
			t.Errorf("missing or unlimited:\n  %s", want)
		}
	}
}

// joinRouteContinuations folds a registration split across two lines back into one.
//
// gofmt breaks a long `r.With(limiter).Post(...)` after the `.`, which left the
// gate check looking at a first half carrying a limiter and no handler, and a
// second half it never saw as a registration at all. Both halves have to be one
// string before either question can be asked of it.
func joinRouteContinuations(group string) []string {
	lines := strings.Split(group, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		cur := lines[i]
		for strings.HasSuffix(strings.TrimSpace(cur), ")).") && i+1 < len(lines) {
			i++
			cur = strings.TrimRight(cur, " \t") + strings.TrimSpace(lines[i])
		}
		out = append(out, cur)
	}
	return out
}
