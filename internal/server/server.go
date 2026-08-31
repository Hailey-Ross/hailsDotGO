package server

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/gorilla/csrf"
	"pogo.hails.cc/internal/costumes"
	"pogo.hails.cc/internal/handlers"
	"pogo.hails.cc/internal/pogodata"
)

// cacheDir mirrors pogodata's: the on-disk cache the costume sprite proxy writes into.
func cacheDir() string {
	if dir := os.Getenv("CACHE_DIR"); dir != "" {
		return dir
	}
	return "cache"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SAMEORIGIN (not DENY) so the translator preview iframe can embed
		// site pages; foreign-origin framing stays blocked.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// The policy was frame-ancestors and nothing else, which only restated the
		// X-Frame-Options line above it. These three directives are the ones that pay
		// their way without a script-src, which cannot be added while the templates
		// carry inline <script> blocks:
		//
		//   base-uri     stops an injected <base href> silently re-pointing every
		//                relative URL on the page, which was one of the ways the
		//                16-byte trainer name could be turned into an attack.
		//   form-action  stops an injected form posting credentials off-site.
		//   object-src   kills <object>/<embed> plugin content outright.
		w.Header().Set("Content-Security-Policy",
			"frame-ancestors 'self'; base-uri 'self'; form-action 'self'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// trustedClientIP resolves the real client address, or "" when no forwarding header
// can be trusted and the socket address should stand as it is.
//
// The rules below were confirmed by capturing what Caddy actually forwards to :8080
// on the live host (Caddy v2.11.4, reverse_proxy localhost:8080), not read off the
// repo Caddyfile, which is stale and does not even name the current hostname.
//
// What the capture showed:
//
//   - X-Forwarded-For arrives with exactly ONE entry, the peer as Caddy resolved it.
//     A request sent with a forged "X-Forwarded-For: 1.2.3.4" reached the app as
//     "X-Forwarded-For: 127.0.0.1": Caddy REPLACES the header rather than appending
//     to it, so client-supplied entries never survive.
//   - Cf-Connecting-Ip and X-Real-Ip are passed through VERBATIM. Both arrived at the
//     app exactly as forged. Neither may ever be trusted, and nothing here reads them.
//
// That second point is why chi's middleware.RealIP is deliberately not used: it reads
// X-Real-IP first of all and trusts it unconditionally, so dropping it in would have
// let any client pick its own rate-limit key, and pin that key to a stranger's
// address to get that person blocked instead.
//
// Two rules make this safe. Forwarding headers are honoured only when the request
// arrived from a loopback peer, which is where Caddy sits, so anything reaching the
// app port directly is keyed on its own socket address with its headers ignored. And
// the LAST X-Forwarded-For entry is taken: today that is the only entry, and it stays
// correct if a proxy hop is ever added in front, where earlier entries would be the
// client-supplied ones.
//
// If Caddy sends no X-Forwarded-For at all, this returns "" and the behaviour falls
// back to the socket address, so the failure mode is the status quo, not a bypass.
func trustedClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if peer == nil || !peer.IsLoopback() {
		return ""
	}
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return ""
	}
	parts := strings.Split(fwd, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(last) == nil {
		return ""
	}
	return last
}

// realIP rewrites RemoteAddr to the resolved client address so that everything
// downstream, httprate, the bandwidth limiter and the request log alike, keys on
// one consistent and trustworthy value.
//
// Without this every httprate.LimitByIP in the app keyed on 127.0.0.1, because
// httprate.KeyByIP reads RemoteAddr and Caddy is the peer. All 27 limiters shared a
// single global bucket, so three requests a minute to /forgot-password denied
// password resets to every user on the site.
func realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := trustedClientIP(r); ip != "" {
			r.RemoteAddr = net.JoinHostPort(ip, "0")
		}
		next.ServeHTTP(w, r)
	})
}

// defaultMaxBody caps a request body when nothing more specific applies.
//
// Almost every JSON handler in the app decoded straight from r.Body with no bound
// at all: 58 of 62 of them, reachable before authentication via
// POST /api/mobile/v1/auth/login. One slow client streaming an endless body held a
// connection and a decoder buffer for as long as it liked, and SavePokemonIV wrote
// an unbounded json.RawMessage into a MySQL JSON column.
//
// 1 MB was picked by measuring rather than guessing. The largest user-authored text
// in production is a 134 byte bug report message; translation edits are capped at
// 2000 characters by the handler; no request struct in the app decodes a large
// array. 1 MB leaves three orders of magnitude of headroom over anything real.
const defaultMaxBody = 1 << 20

// bodyLimits overrides the cap for the endpoints that need a different one,
// in either direction. Keyed on the exact request path.
var bodyLimits = map[string]int64{
	// Screenshot upload. The handler applies the same 8 MB itself; this keeps the
	// outer wrapper from being the tighter of the two.
	"/api/iv/ocr":           8 << 20,
	"/api/mobile/v1/iv/ocr": 8 << 20,

	// A submitted reading, which replaces that upload: about 400 bytes of JSON
	// for the same scan. Held far BELOW the 1 MB default rather than above it,
	// because there is no legitimate large body here and the endpoint's whole
	// purpose is that the frame stays on the device. 16 KB is forty times the
	// real thing.
	"/api/mobile/v1/iv/scan": 16 << 10,

	// A saved Pokemon carries its iv_candidates list. An ambiguous appraisal (no
	// dust, wide level range) can enumerate several thousand candidates at roughly
	// 85 bytes each, so this one is legitimately larger than the rest.
	"/api/iv/pokemon":           2 << 20,
	"/api/mobile/v1/iv/pokemon": 2 << 20,
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		limit := int64(defaultMaxBody)
		if n, ok := bodyLimits[r.URL.Path]; ok {
			limit = n
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// sensitiveQueryKeys are query parameters that are credentials in their own right.
// Anyone holding one can complete the action it belongs to.
var sensitiveQueryKeys = []string{"token", "invite"}

// redactingLogFormatter is chi's request logger with those parameters masked.
//
// GET /reset-password, GET /verify-email and the invite links all carry their
// single-use token in the query string, and the default formatter writes
// r.RequestURI verbatim. That put every live password reset token into the systemd
// journal, which is readable by more people than the mailbox the link was sent to
// and is retained long after the token itself expires.
type redactingLogFormatter struct {
	inner middleware.LogFormatter
}

func (f *redactingLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	if r.URL == nil || r.URL.RawQuery == "" {
		return f.inner.NewLogEntry(r)
	}
	q := r.URL.Query()
	found := false
	for _, key := range sensitiveQueryKeys {
		if q.Has(key) {
			q.Set(key, "REDACTED")
			found = true
		}
	}
	if !found {
		return f.inner.NewLogEntry(r)
	}
	// Shallow copy with its own URL: the formatter only reads, and this copy is
	// never passed downstream, so handlers still see the real token.
	scrubbed := *r
	u := *r.URL
	u.RawQuery = q.Encode()
	scrubbed.URL = &u
	scrubbed.RequestURI = u.RequestURI()
	return f.inner.NewLogEntry(&scrubbed)
}

// assetLinksPath is where the Digital Asset Links statement lives on disk. It stays a
// real file rather than an embedded literal so a signing fingerprint can be added on
// the VPS without a rebuild. Relative to the working directory, like http.Dir("static")
// below; the unit runs with WorkingDirectory=/opt/hailsdotgo.
const assetLinksPath = "static/.well-known/assetlinks.json"

// assetLinks serves the Digital Asset Links statement that lets the Android app
// (live.hails.hailsdotgo) claim pogo.hails.app links as its own, so tapping one opens
// the app instead of the browser. See .claude/assetlinks-for-mobile.md.
//
// Google's verifier is strict and fails silently: it wants this exact URL over HTTPS,
// answering 200 with a JSON content type, with no redirect and no authentication. It is
// fetched by Google's servers rather than the device, so an IP rate limiter would refuse
// a request we never see. Hence a bare registration outside every group.
func assetLinks(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(path)
		if err != nil {
			// Deliberately no Cache-Control here: a missing file means a deploy went
			// wrong, and caching that 404 for an hour would outlive the fix.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// securityHeaders sets nosniff, so the correct type is mandatory, not advisory.
		w.Header().Set("Content-Type", "application/json")
		// Short, so a fingerprint can be added without waiting out a long cache.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(b)
	}
}

func New(store *pogodata.Store, db *sql.DB, csrfKey []byte) http.Handler {
	r := chi.NewRouter()
	// First in the stack: every limiter and the request log below read RemoteAddr,
	// so it has to be resolved before any of them see it.
	r.Use(realIP)
	r.Use(limitBody)
	r.Use(middleware.RequestLogger(&redactingLogFormatter{
		inner: &middleware.DefaultLogFormatter{Logger: log.New(os.Stdout, "", log.LstdFlags)},
	}))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(securityHeaders)

	h := handlers.New(store, db)
	h.StartRaidSweeper()
	h.StartEventReminderSweeper()
	h.StartTranslationAutoSync()
	h.StartCostumeAutoSync()
	// Warehouses the served raid list on every rebuild. Registered before
	// store.Start so the boot rebuild is recorded too.
	h.StartRaidHistory()

	// Event reminders are pinned to a start time the feed can move under them, so
	// they are re-resolved every time a new feed lands rather than only at
	// subscribe time. The store calls this; it knows nothing about the database.
	// Registered before Start, so the startup fetch is covered too.
	store.SetEventsAppliedHook(h.ReconcileEventSubscriptions)

	// Bandwidth limiter: 15 MB per IP per 5-minute window; 30-minute block on breach.
	// Counts aggregate bytes across all seven public API endpoints for the same IP.
	apiBW := newBWLimiter(15*1024*1024, 5*time.Minute, 30*time.Minute)

	// CSRF-exempt: PayPal sends server-to-server POSTs without browser CSRF tokens.
	r.Post("/api/store/webhook", h.StoreWebhook)

	// Static files are GET-only; no CSRF needed. Asset URLs carry a content-derived ?v=
	// query (see handlers.computeAssetVersion), so a long cache is safe: changed files get
	// a new URL and are refetched, unchanged files stay cached.
	staticFS := http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		staticFS.ServeHTTP(w, req)
	}))

	// Digital Asset Links for the Android app. Registered here, directly on r, so the
	// CSRF group, the per-route rate limiters and RequireAuth never touch it.
	//
	// HEAD is registered alongside GET on purpose: chi answers 405 to an unregistered
	// method, which would make a `curl -I` health check on this path look like a failure.
	//
	// Note this file is ALSO reachable at /static/.well-known/assetlinks.json, because
	// http.FileServer does not hide dot-directories. Harmless: the platform reads only
	// the canonical path below, and nothing links to the other one.
	r.Method(http.MethodGet, "/.well-known/assetlinks.json", assetLinks(assetLinksPath))
	r.Method(http.MethodHead, "/.well-known/assetlinks.json", assetLinks(assetLinksPath))

	// Mobile API -- outside CSRF group (Bearer tokens are not CSRF-vulnerable).
	// Login is public and rate-limited; everything else requires Bearer or cookie auth.
	r.Route("/api/mobile/v1", func(r chi.Router) {
		r.With(httprate.LimitByIP(15, time.Minute)).Post("/auth/login", h.MobileLogin)
		// Public, so these sit outside MobileAuthMiddleware and the authenticated
		// group's 120/min baseline does not reach them. Each carries its web route's
		// own limit instead: account creation and reset requests are exactly the
		// endpoints a loose ceiling would hurt.
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/auth/register", h.MobileRegister)
		r.With(httprate.LimitByIP(8, time.Minute)).Post("/auth/forgot-password", h.MobileForgotPassword)

		// Public game data aliases with stable versioned URLs for mobile clients.
		// Rate-limited to match the legacy web API (server.go ~line 305).
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/events", h.APIEvents)
		// The detail route existed only on the CSRF-protected web path and the
		// private path, so the app reached it at /api/events/{id} and got away with
		// it because it is a GET. That made it the one events call outside the
		// versioned tree; this is its alias.
		//
		// The static /events/subscriptions routes registered in the authenticated
		// group below still win over this parameter: chi matches a static segment
		// before a wildcard, so an unauthenticated GET there is a 401 and not an
		// "unknown event" 404.
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/events/{id}", h.APIEventDetail)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/data", h.APIData)
		// Dynamic and polled by the app, so a looser standalone limit.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/raid/overview", h.MobileRaidOverview)

		// Public for the same reason the website's nav is: an anonymous visitor already
		// sees a disabled section missing from the nav and gets the 503 maintenance page
		// if they ask for it anyway. Requiring a token here would only stop a logged-out
		// app from greying out a section the site has switched off.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/maintenance", h.MobileMaintenance)

		// The site's own string bundles, so a native screen renders in the language
		// the trainer picked and an approved translation reaches devices without an
		// app release.
		//
		// Public, and for the same reason the maintenance flags are: the website
		// serves these strings to anonymous visitors on every page, and a logged out
		// app needs its login screen translated too.
		//
		// Bundles run 50 to 80 KB, so they carry the bandwidth limiter and the same
		// 10 per 2 minutes the other public reads use. Each answers an ETag and a
		// 304, so a launch that has not missed a translation costs a few hundred bytes.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/i18n", h.MobileLangs)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/i18n/{lang}", h.MobileI18nBundle)

		// Newest published app build, as bare digits, so an install can tell it is
		// stale. Public for the same reason /maintenance is: a logged out app needs
		// it too, and the number is not a secret.
		//
		// HEAD alongside GET for the reason given above assetlinks: chi answers 405
		// to an unregistered method, which would make a `curl -I` check on this path
		// look like a failure.
		//
		// The POST publishes a new number and is guarded by MOBILE_BUILD_TOKEN
		// inside the handler rather than by MobileAuthMiddleware: it is called by the
		// mobile repo's release step, which has no user account.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/build", h.MobileBuildGet)
		r.With(httprate.LimitByIP(20, time.Minute)).Method(http.MethodHead, "/build", http.HandlerFunc(h.MobileBuildGet))
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/build", h.MobileBuildSet)

		// All remaining endpoints require authentication.
		r.Group(func(r chi.Router) {
			r.Use(h.MobileAuthMiddleware())
			r.Use(httprate.LimitByIP(120, time.Minute)) // baseline abuse ceiling
			r.Delete("/auth/session", h.MobileLogout)
			r.Get("/auth/me", h.MobileMe)
			r.Put("/profile", h.MobilePutProfile)

			// Trainer settings. The write goes through applySettings, the same function
			// the settings form uses, so the trainer name rules cannot be enforced on the
			// web and skipped here. The avatar catalogue is separate because it runs to
			// over 1700 entries and would dwarf the settings it belongs to.
			r.Get("/settings", h.MobileSettingsGet)
			r.Put("/settings", h.MobileSettingsPut)
			r.Get("/settings/avatars", h.MobileSettingsAvatars)

			// Trainers directory, one profile, and the social lists. All three go through
			// toMobileTrainer, which applies the privacy gates the templates apply: a
			// private profile must not put its friend code or pronouns on the wire.
			r.Get("/trainers", h.MobileTrainers)
			r.Get("/trainer/{username}", h.MobileTrainerProfile)
			r.Get("/social/{username}/lists", h.MobileSocialLists)

			// The rest of the profile screen. All four already answered JSON over
			// Bearer on their web paths, so these are aliases and not new handlers;
			// they are here so the screen has one base path instead of straddling
			// /api/... and /api/mobile/v1/..., which is the same reason the bug
			// report GETs were repeated below. A client that hardcodes a web path
			// is a client that breaks the day that path moves inside a stricter gate.
			//
			// APIUsersSearch is the one to look at twice. On the web it is wrapped
			// in RequireAuth, which answers a 303 to /login; it is registered bare
			// here and makes its own 401, exactly as APIAwardGrant does further down.
			//
			// /feedback/options sits under the same prefix as the {username}
			// route and wins, because chi matches a static segment before a
			// wildcard. That is the behaviour wanted, and the same arrangement
			// /events/subscriptions has above. The cost is that a trainer named
			// "options" cannot have their feedback read at this path; usernames
			// are not reserved, so if that ever matters, move the options list
			// rather than renaming the account.
			r.Get("/feedback/{username}", h.APIGetFeedback)
			r.Get("/feedback/options", h.MobileFeedbackOptions)
			r.Get("/awards", h.APIAwardsList)
			r.Get("/awards/of/{username}", h.APIAwardsOf)
			r.Get("/shinies/of/{username}", h.APIShiniesOfUser)
			r.Get("/users/search", h.APIUsersSearch)

			// Two reads the app takes off their web paths today. Same alias
			// argument as above; nothing about either handler changes.
			r.Get("/notifications", h.APIGetNotifications)
			r.Get("/weather", h.APIWeather)
			r.Post("/push/token", h.RegisterPushToken)
			r.Delete("/push/token", h.UnregisterPushToken)

			// Event reminders. The app has had the UI for a release (a bell per
			// upcoming event card, a per-event lead time, a default in Settings)
			// and these three are what make it do anything: without them the calls
			// 404 and it falls back to its disk cache.
			//
			// PUT is an upsert, so subscribing and changing a lead time are one
			// call and a retry after a dropped connection is safe to repeat. It is
			// also what the app re-sends for every subscription when the device's
			// timezone changes, which is the one burst these see, so the group's
			// 120/min baseline is the limit that matters.
			//
			// The event id is a path parameter and reaches a LIKE-free
			// parameterized query; the handlers validate it against the single
			// eventIDPattern in events_ics.go.
			r.Get("/events/subscriptions", h.APIEventSubscriptions)
			r.Put("/events/subscriptions/{eventId}", h.APIEventSubscribe)
			r.Delete("/events/subscriptions/{eventId}", h.APIEventUnsubscribe)

			r.Post("/iv/calculate", h.IVCalculate)
			r.With(httprate.LimitByIP(10, time.Minute)).Post("/iv/ocr", h.IVFromOCR)
			// The app submits its own reading and the server solves it.
			//
			// Limited per ACCOUNT, not per IP, which is the difference that
			// matters here. The image path is self limiting: an 8 MB upload
			// costs the sender real bandwidth to make the server work. A few
			// hundred bytes that trigger a full IV enumeration cost the sender
			// nothing and cost the server the same, so the bound has to be on
			// the thing that cannot be changed with a VPN. Measured worst case
			// for one solve is 17 ms of CPU (no dust, no appraisal, full level
			// sweep); a typical scan is under 1 ms. See LimitByAccount and
			// BenchmarkSolveWorstCase.
			r.With(h.LimitByAccount(30, time.Minute)).Post("/iv/scan", h.IVFromScan)
			r.Post("/iv/pokemon", h.SavePokemonIV)
			r.Get("/iv/pokemon", h.ListPokemonIV)
			r.Delete("/iv/pokemon/{id}", h.DeletePokemonIV)

			// Shiny collection: GET/POST need mobile response shapes (sprite_url); the rest
			// reuse the web handlers verbatim since they already work over Bearer auth.
			r.Get("/shinies", h.MobileShiniesGet)
			r.Post("/shinies", h.MobileShiniesAdd)
			r.Get("/shinies/reference", h.MobileShiniesReference)
			// The checklist itself, expanded server side: one card per species plus
			// one per regional or alternate forme, sprites resolved, release state
			// and announced dates already worked out. The website derives this in
			// the browser across three TypeScript modules; serving it means the app
			// renders and filters rather than growing a second copy of that logic.
			//
			// Carries an ETag and answers 304, so the group's 120/min baseline is
			// limit enough even though the payload is a few hundred KB.
			r.Get("/shiny-dex", h.MobileShinyDex)
			// Pokedex species text: the genus, the flavour text and the legendary
			// and mythical flags, for every species at once and in the caller's
			// language. It takes the one PokeAPI call the app makes straight from
			// the device off users' phones, and it is authenticated rather than
			// public for the same reason /shiny-dex is: it sits behind a screen
			// that already requires login, and the upstream data being public is
			// no reason to make our copy of it so.
			//
			// Same ETag and 304 arrangement as /shiny-dex, so the group's 120/min
			// baseline is limit enough for a payload of a few hundred KB.
			r.Get("/pokedex", h.MobilePokedex)
			r.Get("/pokedex/{dex}", h.MobilePokedexSpecies)
			// The costume catalog, precomputed per species. Not user-specific, but it sits in
			// the authenticated group beside the rest of the shiny collection because that is
			// the only screen that asks for it, and the group baseline is limit enough.
			r.Get("/costumes", h.MobileCostumes)
			r.Put("/shinies/{id}", h.APIShiniesUpdate)
			r.Delete("/shinies/{id}", h.APIShiniesDelete)
			r.Post("/shinies/{id}/evolve", h.APIShiniesEvolve)

			r.Get("/raid/state", h.MobileRaidState)
			// Past rotations. Aliases of the web reads; same handlers, same shapes.
			r.With(httprate.LimitByIP(20, time.Minute)).Get("/raid/history", h.APIRaidHistory)
			r.With(httprate.LimitByIP(20, time.Minute)).Get("/raid/history/{boss}", h.APIRaidHistoryOfBoss)
			r.Post("/raid/queue", h.APIRaidQueueJoin)
			r.Delete("/raid/queue", h.APIRaidQueueLeave)
			r.Post("/raid/lobbies", h.APIRaidLobbyCreate)
			r.Delete("/raid/lobbies/{id}", h.APIRaidLobbyCancel)
			r.Post("/raid/lobbies/{id}/confirm", h.APIRaidLobbyConfirm)
			r.Post("/raid/lobbies/{id}/leave", h.APIRaidLobbyLeave)
			r.Post("/raid/lobbies/{id}/kick", h.APIRaidLobbyKick)
			r.Post("/raid/lobbies/{id}/invited", h.APIRaidLobbyInvited)
			r.Post("/raid/lobbies/{id}/report", h.MobileRaidLobbyReport)
			r.Post("/raid/lobbies/{id}/feedback", h.MobileRaidLobbyFeedback)

			// Player ("bad actor") reports. The web route is unreachable from the app: it sits
			// inside the CSRF group, which rejects a Bearer request before auth is ever
			// consulted, and does so as text/plain. CreatePlayerReport itself needs nothing
			// session-specific, so it is reused verbatim. The tighter limit is the web route's:
			// the group's 120/min baseline is far too loose for a moderation endpoint.
			r.With(httprate.LimitByIP(6, time.Minute)).Post("/player-reports", h.CreatePlayerReport)

			// Bug report threads. Same story as /player-reports above: the writes sit inside
			// the CSRF group, so the app can file a player report today but cannot read the
			// thread it just created. Every one of these handlers is session-generic, so they
			// are reused verbatim. The two GETs already worked over Bearer on their web paths;
			// they are repeated here so the screen has one base path instead of straddling
			// /api/bug-reports and /api/mobile/v1/bug-reports.
			//
			// The create limit is the web route's 6/min, restated for the same reason as above.
			r.With(httprate.LimitByIP(6, time.Minute)).Post("/bug-reports", h.CreateBugReport)
			r.Get("/bug-reports", h.APIListBugReports)
			r.Get("/bug-reports/{id}", h.APIGetBugReport)
			r.Post("/bug-reports/{id}/messages", h.APIPostBugMessage)
			r.Post("/bug-reports/{id}/invite", h.APIBugInvite)
			r.Post("/bug-reports/{id}/status", h.APIBugReportStatus)
			r.Post("/bug-reports/{id}/rating", h.APIBugReportRating)

			// Profile actions: follow, block, feedback and community award grants.
			//
			// APIFriend and APIBlock switch on r.Method internally rather than being separate
			// handlers, so each needs both registrations. With only the Post, a DELETE reaches
			// the handler and falls out of its method switch as a plain text 405.
			r.Post("/social/{username}/friend", h.APIFriend)
			r.Delete("/social/{username}/friend", h.APIFriend)
			r.Post("/social/{username}/block", h.APIBlock)
			r.Delete("/social/{username}/block", h.APIBlock)
			r.Post("/feedback/{username}", h.APIPostFeedback)
			r.Delete("/feedback/entry/{id}", h.APIDeleteFeedback)
			// Registered bare, unlike its web twin: APIAwardGrant is wrapped in RequireAuth
			// there, which answers a 303 redirect to /login instead of JSON. The group
			// middleware already gates this tree, and the handler makes its own 401 check.
			r.Post("/awards/{id}/grant", h.APIAwardGrant)

			// Supporter tag controls. Checkout itself stays in the WebView, since it is a real
			// money PayPal redirect out to /store/return, but these three are ordinary JSON
			// writes that only CSRF was blocking. Bare for the same RequireAuth reason as the
			// award grant above; each already answers its own 401.
			r.Post("/store/tag-request", h.StoreTagRequestSubmit)
			r.Post("/store/tag-color", h.StoreTagColorUpdate)
			r.Post("/store/purchases/cancel", h.StorePurchaseCancel)

			// ── Admin panel ──────────────────────────────────────────────────
			//
			// All sixteen tabs. 90 of these are ALIASES: the same handler as the web
			// route, registered a second time here because /api/mobile/v1 is the only
			// tree outside csrf.Protect and every write on a web path is rejected as a
			// text/plain 403 before auth is consulted.
			//
			// EVERY ONE GOES THROUGH A JSON ROLE GATE, and that is not cosmetic.
			// Almost no admin handler checks its own role: AdminChangeRole validates
			// the role string, refuses the superadmin as a TARGET, and writes, without
			// ever asking who the caller is. The route wrapper is its entire authority
			// check, and the group middleware above proves only that the session is
			// valid. A bare registration here would be a self-service promotion to
			// admin. TestAdminMobileRoutesAreRoleGated fails the build if one appears.
			//
			// The gate is the JSON sibling of the web wrapper, one for one:
			// RequireMod becomes RequireModAPI, and so on. The HTML wrappers answer a
			// 303 to /login with no session and a text/plain body on the wrong role,
			// neither of which a client can read.
			//
			// Rate limiters are carried across wherever the web route has one. An
			// alias that silently relaxes a limit is worse than no alias.
			r.Route("/admin", func(r chi.Router) {
				// The panel's own state. adminData reaches the template and no
				// endpoint, so without this the app cannot draw the Settings tab or
				// decide which of the sixteen tabs the caller may see.
				r.Get("/context", h.RequireModAPI(h.MobileAdminContext))

				// The five form-encoded handlers' JSON siblings. Their web twins take
				// a form and answer with a rendered page, so they are re-implemented
				// rather than aliased; see mobile_admin.go for what is shared.
				//
				// Both PUTs take a PARTIAL body: an omitted key is left alone. That is
				// the bug the form path needed hidden _settings inputs to work around.
				r.Put("/settings", h.RequireAdminAPI(h.MobileAdminSettings))
				r.Put("/pages", h.RequireAdminAPI(h.MobileAdminPages))
				r.Put("/mobile-build", h.RequireAdminAPI(h.MobileAdminMobileBuild))
				r.Post("/invites", h.RequireAdminAPI(h.MobileAdminCreateInvite))
				r.Delete("/invites/{token}", h.RequireAdminAPI(h.MobileAdminCancelInvite))

				// H1: users. The whole moderation surface.
				r.Get("/users", h.RequireModAPI(h.AdminUsersAPI))
				r.Post("/users/{id}/password", h.RequireModAPI(h.AdminResetPassword))
				r.Post("/users/{id}/username", h.RequireModAPI(h.AdminChangeUsername))
				r.Post("/users/{id}/disable", h.RequireModAPI(h.AdminToggleDisable))
				r.Post("/users/{id}/role", h.RequireAdminAPI(h.AdminChangeRole))
				// Keeps the web route's limiter. Permanent deletion is irreversible,
				// and 10 per 5 minutes is a brake on a scripted rampage with a stolen
				// session; a phone is not a reason to relax it.
				r.With(httprate.LimitByIP(10, 5*time.Minute)).
					Post("/users/{id}/delete", h.RequireSuperAdminAPI(h.AdminDeleteUser))
				r.Post("/users/{id}/api-access", h.RequireSuperAdminAPI(h.AdminToggleAPIAccess))
				r.Post("/users/{id}/translator", h.RequireSuperAdminAPI(h.AdminToggleTranslator))
				r.Post("/users/{id}/confirm-role", h.RequireAdminAPI(h.AdminConfirmRole))
				r.Post("/users/{id}/reject-role", h.RequireAdminAPI(h.AdminRejectRole))
				r.Post("/users/{id}/directory-hide", h.RequireModAPI(h.AdminToggleDirectoryHide))
				r.Post("/users/{id}/raid-ban", h.RequireModAPI(h.AdminToggleRaidBan))
				r.Post("/users/{id}/raid-xp", h.RequireAdminAPI(h.AdminSetRaidXP))
				r.Post("/users/{id}/rater-weight", h.RequireAdminAPI(h.AdminSetRaterWeight))
				r.Post("/users/{id}/clear-ratings", h.RequireAdminAPI(h.AdminClearRatings))
				r.Post("/users/{id}/refresh-activity", h.RequireAdminAPI(h.AdminRefreshActivity))
				r.Post("/users/{id}/special-rank", h.RequireAdminAPI(h.AdminSetSpecialRank))
				r.Get("/users/{id}/strikes", h.RequireModAPI(h.AdminStrikesGet))
				r.Post("/users/{id}/strikes", h.RequireModAPI(h.AdminStrikesAdd))
				r.Delete("/users/{id}/strikes/{strikeId}", h.RequireModAPI(h.AdminStrikesDelete))
				r.Post("/users/{id}/tags", h.RequireModAPI(h.AdminUserTagAdd))
				r.Delete("/users/{id}/tags/{tagId}", h.RequireModAPI(h.AdminUserTagRemove))
				r.Post("/users/{id}/trust-adjust", h.RequireAdminAPI(h.AdminTrustAdjust))
				r.Post("/users/{id}/trust-recompute", h.RequireAdminAPI(h.AdminTrustRecompute))

				// H3: bug reports and player reports.
				r.Get("/bug-reports", h.RequireModAPI(h.AdminBugReportsList))
				r.Get("/staff", h.RequireModAPI(h.AdminStaffList))
				r.Post("/bug-reports/{id}/status", h.RequireModAPI(h.AdminBugReportStatus))
				r.Post("/bug-reports/{id}/priority", h.RequireModAPI(h.AdminBugReportPriority))
				r.Post("/bug-reports/{id}/assign", h.RequireModAPI(h.AdminBugReportAssign))
				r.Post("/bug-reports/{id}/actioned", h.RequireModAPI(h.AdminPlayerReportActioned))
				r.Post("/bug-reports/{id}/labels", h.RequireModAPI(h.AdminBugReportLabelAdd))
				r.Delete("/bug-reports/{id}/labels/{labelId}", h.RequireModAPI(h.AdminBugReportLabelRemove))
				r.Get("/bug-report-labels", h.RequireModAPI(h.AdminBugLabels))
				r.Post("/bug-report-labels", h.RequireAdminAPI(h.AdminBugLabels))
				r.Put("/bug-report-labels/{id}", h.RequireAdminAPI(h.AdminBugLabel))
				r.Delete("/bug-report-labels/{id}", h.RequireAdminAPI(h.AdminBugLabel))
				r.Get("/bug-report-macros", h.RequireModAPI(h.AdminBugMacros))
				r.Post("/bug-report-macros", h.RequireAdminAPI(h.AdminBugMacros))
				r.Put("/bug-report-macros/{id}", h.RequireAdminAPI(h.AdminBugMacro))
				r.Delete("/bug-report-macros/{id}", h.RequireAdminAPI(h.AdminBugMacro))

				// H4: awards, tags, tag requests, feedback options, store items.
				r.Get("/awards", h.RequireModAPI(h.AdminAwardsList))
				r.Post("/awards", h.RequireAdminAPI(h.AdminAwardCreate))
				r.Patch("/awards/{id}", h.RequireAdminAPI(h.AdminAwardUpdate))
				r.Delete("/awards/{id}", h.RequireAdminAPI(h.AdminAwardDelete))
				r.Delete("/award-grants/{id}", h.RequireModAPI(h.AdminAwardGrantDelete))
				r.Get("/tags", h.RequireModAPI(h.AdminTagsList))
				r.Post("/tags", h.RequireSuperAdminAPI(h.AdminTagCreate))
				r.Patch("/tags/{id}", h.RequireSuperAdminAPI(h.AdminTagUpdate))
				r.Delete("/tags/{id}", h.RequireSuperAdminAPI(h.AdminTagDelete))
				r.Get("/tag-requests", h.RequireModAPI(h.AdminTagRequestsList))
				r.Post("/tag-requests/{id}/approve", h.RequireModAPI(h.AdminTagRequestApprove))
				r.Post("/tag-requests/{id}/reject", h.RequireModAPI(h.AdminTagRequestReject))
				r.Post("/tag-requests/{id}/revision", h.RequireModAPI(h.AdminTagRequestRevision))
				r.Get("/feedback-options", h.RequireAdminAPI(h.APIAdminFeedbackOptions))
				r.Post("/feedback-options", h.RequireAdminAPI(h.APIAdminFeedbackOptions))
				r.Put("/feedback-options/{id}", h.RequireAdminAPI(h.APIAdminFeedbackOption))
				r.Delete("/feedback-options/{id}", h.RequireAdminAPI(h.APIAdminFeedbackOption))
				r.Get("/store-items", h.RequireAdminAPI(h.AdminStoreItemsList))
				r.Post("/store-items/{id}/toggle", h.RequireAdminAPI(h.AdminToggleStoreItem))

				// H5: costumes, sprite locks, shiny dex flags.
				r.Get("/costumes", h.RequireAdminAPI(h.AdminCostumes))
				r.Post("/costumes/name", h.RequireAdminAPI(h.AdminNameCostume))
				r.Post("/costumes/hide", h.RequireAdminAPI(h.AdminHideCostume))
				r.Delete("/costumes/name", h.RequireAdminAPI(h.AdminUnnameCostume))
				r.With(httprate.LimitByIP(5, time.Minute)).
					Post("/check-costumes", h.RequireAdminAPI(h.AdminCheckCostumes))
				r.Get("/sprite-locks", h.RequireAdminAPI(h.AdminGetSpriteLocks))
				r.Post("/sprite-lock/{slug}", h.RequireAdminAPI(h.AdminSetSpriteLock))
				r.Delete("/sprite-lock/{slug}", h.RequireAdminAPI(h.AdminDeleteSpriteLock))
				r.Get("/shiny-dex", h.RequireAdminAPI(h.AdminGetShinyDex))
				r.Put("/shiny-dex/{dex}", h.RequireAdminAPI(h.AdminSetShinyDexFlags))
				r.Delete("/shiny-dex/{dex}", h.RequireAdminAPI(h.AdminResetShinyDexFlags))
				r.Post("/shiny-dex/bulk", h.RequireAdminAPI(h.AdminBulkSetShinyDexFlags))

				// H6: raids, trust, and the two global refresh tools.
				r.Get("/raid-lobbies", h.RequireModAPI(h.AdminRaidLobbiesList))
				// Not an h.Admin* name, so the role-gate test cannot spot it by the
				// handler. It is caught by the path rule instead: everything under
				// /admin in this tree must be gated.
				r.Delete("/raid-lobbies/{id}", h.RequireModAPI(h.APIRaidLobbyCancel))
				r.Get("/trust/{id}", h.RequireModAPI(h.AdminTrustEvents))
				r.Post("/refresh-data", h.RequireSuperAdminAPI(h.AdminRefreshData))
				r.Post("/check-scrapers", h.RequireSuperAdminAPI(h.AdminRunScrapers))

				// H7: translation review. The AUTHOR facing workspace at /translate is
				// exempt from the native conversion; this is the review half, which
				// lives in the admin panel and comes with it. If the exemption is
				// meant to cover both halves, this group is what to delete.
				r.Get("/translator-applications", h.RequireAdminAPI(h.AdminTranslatorAppsList))
				r.Post("/translator-applications/{id}/status", h.RequireAdminAPI(h.AdminTranslatorAppSetStatus))
				r.Get("/translations", h.RequireAdminAPI(h.AdminTranslationsList))
				r.Post("/translations/{id}/approve", h.RequireAdminAPI(h.AdminTranslationApprove))
				r.Post("/translations/{id}/reject", h.RequireAdminAPI(h.AdminTranslationReject))
				r.Get("/translations/export/{lang}", h.RequireAdminAPI(h.AdminTranslationsExport))
				r.Post("/translations/sync", h.RequireAdminAPI(h.AdminTranslationsSync))
				r.Get("/locales", h.RequireAdminAPI(h.APITranslateLocales))
				r.Post("/locales/{code}/enable", h.RequireAdminAPI(h.AdminLocaleEnable))
				r.Delete("/locales/{code}", h.RequireAdminAPI(h.AdminLocaleDelete))

				// H8: stats.
				r.Get("/stats", h.RequireAdminAPI(h.AdminStatsAPI))
			})
		})
	})

	csrfDebug := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, cookieErr := r.Cookie("_pogo_csrf")
		log.Printf("CSRF FAIL: method=%s path=%s reason=%v token=%q cookie_present=%v",
			r.Method, r.URL.Path,
			csrf.FailureReason(r),
			r.Header.Get("X-CSRF-Token"),
			cookieErr == nil,
		)
		http.Error(w, "Forbidden", http.StatusForbidden)
	})

	r.Group(func(r chi.Router) {
		r.Use(csrf.Protect(csrfKey, csrf.Secure(true), csrf.SameSite(csrf.SameSiteStrictMode), csrf.Path("/"), csrf.ErrorHandler(csrfDebug), csrf.CookieName("_pogo_csrf")))

		r.Get("/", h.Home)
		r.Get("/raids", h.Raids)
		r.Get("/dps", h.DPS)
		r.Get("/pvp", h.PVP)
		r.Get("/events", h.Events)
		r.With(httprate.LimitByIP(30, time.Minute)).Get("/events/calendar.ics", h.EventsICS)
		r.With(httprate.LimitByIP(30, time.Minute)).Get("/events/event.ics", h.EventICS)
		r.Get("/iv", h.GetIVPage)
		r.Get("/box", h.GetBoxPage)
		r.Get("/credits", h.Credits)
		r.Get("/privacy", h.Privacy)
		r.Get("/changelog", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/credits?tab=changelog", http.StatusMovedPermanently)
		})

		r.Get("/login", h.LoginPage)
		// These numbers only started meaning anything once realIP made the key the
		// actual client. Until then every one of them shared a single 127.0.0.1 bucket,
		// so they were tuned for a world where they effectively never fired. They are
		// raised here to absorb shared addresses (a household, a campus, or carrier
		// grade NAT can put many genuine users behind one IP) while still being far
		// below what a brute force run needs.
		r.With(httprate.LimitByIP(15, time.Minute)).Post("/login", h.Login)
		r.Get("/register", h.RegisterPage)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/register", h.Register)
		r.Get("/forgot-password", h.ForgotPasswordPage)
		r.With(httprate.LimitByIP(8, time.Minute)).Post("/forgot-password", h.ForgotPassword)
		r.Get("/reset-password", h.ResetPasswordPage)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/reset-password", h.ResetPassword)
		r.Get("/verify-email", h.VerifyEmail)
		r.With(httprate.LimitByIP(5, time.Minute)).Post("/resend-verification", h.RequireAuth(h.ResendVerification))
		r.Post("/logout", h.Logout)
		r.Post("/lang", h.SetLang)

		r.Get("/trainers", h.TrainersPage)
		r.Get("/trainer/{username}", h.TrainerProfilePage)
		r.Get("/social/{username}", h.SocialPage)
		r.Get("/friends", h.RequireAuth(h.FriendsRedirect))
		r.Get("/notifications", h.RequireAuth(h.NotificationsPage))
		r.Get("/raidfinder", h.RaidFinderPage)

		r.Get("/store", h.StorePage)
		r.Post("/store/checkout", h.RequireAuth(h.StoreCheckout))
		r.Get("/store/return", h.StoreReturn)
		r.Get("/store/cancel", h.StoreCancel)
		r.Post("/api/store/tag-request", h.RequireAuth(h.StoreTagRequestSubmit))
		r.Post("/api/store/tag-color", h.RequireAuth(h.StoreTagColorUpdate))
		r.Post("/api/store/purchases/cancel", h.RequireAuth(h.StorePurchaseCancel))

		r.Get("/settings", h.RequireAuth(h.SettingsPage))
		r.Post("/settings", h.RequireAuth(h.SettingsUpdate))
		r.Get("/shinies", h.RequireAuth(h.ShiniesPage))
		r.Get("/admin", h.RequireMod(h.AdminPage))
		r.Post("/admin/settings", h.RequireAdmin(h.AdminUpdateSettings))
		r.Post("/admin/pages", h.RequireAdmin(h.AdminUpdatePageSettings))
		r.Post("/admin/mobile-build", h.RequireAdmin(h.AdminUpdateMobileBuild))
		r.Post("/admin/invite", h.RequireAdmin(h.AdminGenerateInvite))
		r.Post("/admin/invite/{token}/cancel", h.RequireAdmin(h.AdminCancelInvite))

		r.Get("/api/admin/stats", h.RequireAdmin(h.AdminStatsAPI))

		r.Get("/translate", h.TranslatePage)
		r.With(httprate.LimitByIP(3, 5*time.Minute)).Post("/translate/apply", h.RequireAuth(h.TranslatorApply))
		r.Get("/api/translate/keys", h.RequireTranslator(h.APITranslateKeys))
		r.Post("/api/translate/edits", h.RequireTranslator(h.APITranslateSubmit))
		r.Delete("/api/translate/edits/{id}", h.RequireTranslator(h.APITranslateWithdraw))
		r.Get("/api/translate/locales", h.RequireTranslator(h.APITranslateLocales))
		r.Post("/api/translate/locales", h.RequireTranslator(h.APITranslateLocaleCreate))

		r.Get("/api/admin/translator-applications", h.RequireAdmin(h.AdminTranslatorAppsList))
		r.Post("/api/admin/translator-applications/{id}/status", h.RequireAdmin(h.AdminTranslatorAppSetStatus))

		r.Get("/api/admin/translations", h.RequireAdmin(h.AdminTranslationsList))
		r.Post("/api/admin/translations/{id}/approve", h.RequireAdmin(h.AdminTranslationApprove))
		r.Post("/api/admin/translations/{id}/reject", h.RequireAdmin(h.AdminTranslationReject))
		r.Get("/api/admin/translations/export/{lang}", h.RequireAdmin(h.AdminTranslationsExport))
		r.Post("/api/admin/translations/sync", h.RequireAdmin(h.AdminTranslationsSync))
		r.Get("/api/admin/locales", h.RequireAdmin(h.APITranslateLocales))
		r.Post("/api/admin/locales/{code}/enable", h.RequireAdmin(h.AdminLocaleEnable))
		r.Delete("/api/admin/locales/{code}", h.RequireAdmin(h.AdminLocaleDelete))

		r.Get("/api/admin/tag-requests", h.RequireMod(h.AdminTagRequestsList))
		r.Post("/api/admin/tag-requests/{id}/approve", h.RequireMod(h.AdminTagRequestApprove))
		r.Post("/api/admin/tag-requests/{id}/reject", h.RequireMod(h.AdminTagRequestReject))
		r.Post("/api/admin/tag-requests/{id}/revision", h.RequireMod(h.AdminTagRequestRevision))

		r.Get("/api/admin/store-items", h.RequireAdmin(h.AdminStoreItemsList))
		r.Post("/api/admin/store-items/{id}/toggle", h.RequireAdmin(h.AdminToggleStoreItem))

		r.Get("/api/admin/tags", h.RequireMod(h.AdminTagsList))
		r.Post("/api/admin/tags", h.RequireSuperAdmin(h.AdminTagCreate))
		r.Patch("/api/admin/tags/{id}", h.RequireSuperAdmin(h.AdminTagUpdate))
		r.Delete("/api/admin/tags/{id}", h.RequireSuperAdmin(h.AdminTagDelete))
		r.Post("/api/admin/users/{id}/tags", h.RequireMod(h.AdminUserTagAdd))
		r.Delete("/api/admin/users/{id}/tags/{tagId}", h.RequireMod(h.AdminUserTagRemove))

		r.Get("/admin/users", h.RequireMod(h.AdminUsersAPI))
		r.Post("/admin/users/{id}/password", h.RequireMod(h.AdminResetPassword))
		r.Post("/admin/users/{id}/username", h.RequireMod(h.AdminChangeUsername))
		r.Post("/admin/users/{id}/disable", h.RequireMod(h.AdminToggleDisable))
		r.Post("/admin/users/{id}/role", h.RequireAdmin(h.AdminChangeRole))
		// Permanent account deletion. Superadmin only and irreversible, so it is
		// rate limited as a brake on a scripted rampage with a stolen session.
		// 10 per 5 minutes leaves room for a real spam cleanup, which is the
		// reason this route exists, while still making mass deletion slow.
		r.With(httprate.LimitByIP(10, 5*time.Minute)).
			Post("/admin/users/{id}/delete", h.RequireSuperAdmin(h.AdminDeleteUser))
		r.Post("/admin/users/{id}/api-access", h.RequireSuperAdmin(h.AdminToggleAPIAccess))
		r.Post("/admin/users/{id}/translator", h.RequireSuperAdmin(h.AdminToggleTranslator))
		r.Post("/admin/refresh-data", h.RequireSuperAdmin(h.AdminRefreshData))
		r.Post("/admin/check-scrapers", h.RequireSuperAdmin(h.AdminRunScrapers))
		r.Post("/admin/users/{id}/confirm-role", h.RequireAdmin(h.AdminConfirmRole))
		r.Post("/admin/users/{id}/reject-role", h.RequireAdmin(h.AdminRejectRole))
		r.Post("/admin/users/{id}/directory-hide", h.RequireMod(h.AdminToggleDirectoryHide))
		r.Post("/admin/users/{id}/raid-ban", h.RequireMod(h.AdminToggleRaidBan))
		r.Post("/admin/users/{id}/raid-xp", h.RequireAdmin(h.AdminSetRaidXP))
		r.Post("/admin/users/{id}/rater-weight", h.RequireAdmin(h.AdminSetRaterWeight))
		r.Post("/admin/users/{id}/clear-ratings", h.RequireAdmin(h.AdminClearRatings))
		r.Post("/admin/users/{id}/refresh-activity", h.RequireAdmin(h.AdminRefreshActivity))
		r.Get("/admin/users/{id}/strikes", h.RequireMod(h.AdminStrikesGet))
		r.Post("/admin/users/{id}/strikes", h.RequireMod(h.AdminStrikesAdd))
		r.Delete("/admin/users/{id}/strikes/{strikeId}", h.RequireMod(h.AdminStrikesDelete))

		// Social: friends and blocks
		r.Get("/api/social/{username}", h.RequireAuthAPI(h.APIGetSocialState))
		r.Post("/api/social/{username}/friend", h.RequireAuthAPI(h.APIFriend))
		r.Delete("/api/social/{username}/friend", h.RequireAuthAPI(h.APIFriend))
		r.Post("/api/social/{username}/block", h.RequireAuthAPI(h.APIBlock))
		r.Delete("/api/social/{username}/block", h.RequireAuthAPI(h.APIBlock))

		// Notifications: active raids from friends
		r.Get("/api/notifications", h.RequireAuthAPI(h.APIGetNotifications))

		// Feedback: public read, auth write, mod/author delete
		r.Get("/api/feedback/{username}", h.APIGetFeedback)
		r.Post("/api/feedback/{username}", h.RequireAuthAPI(h.APIPostFeedback))
		r.Delete("/api/feedback/entry/{id}", h.RequireAuthAPI(h.APIDeleteFeedback))

		// Admin: feedback option management (admin+ only)
		r.Get("/api/admin/feedback-options", h.RequireAdmin(h.APIAdminFeedbackOptions))
		r.Post("/api/admin/feedback-options", h.RequireAdmin(h.APIAdminFeedbackOptions))
		r.Put("/api/admin/feedback-options/{id}", h.RequireAdmin(h.APIAdminFeedbackOption))
		r.Delete("/api/admin/feedback-options/{id}", h.RequireAdmin(h.APIAdminFeedbackOption))

		// Bug reports ("Report Me Not"): reporter/participant-facing.
		r.Get("/reports", h.RequireAuth(h.ReportsPage))
		r.With(httprate.LimitByIP(6, time.Minute)).Post("/api/bug-reports", h.RequireAuthAPI(h.CreateBugReport))
		r.Get("/api/bug-reports", h.RequireAuthAPI(h.APIListBugReports))
		r.Get("/api/bug-reports/{id}", h.RequireAuthAPI(h.APIGetBugReport))
		r.Post("/api/bug-reports/{id}/messages", h.RequireAuthAPI(h.APIPostBugMessage))
		r.Post("/api/bug-reports/{id}/invite", h.RequireAuthAPI(h.APIBugInvite))
		r.Post("/api/bug-reports/{id}/status", h.RequireAuthAPI(h.APIBugReportStatus))
		r.Post("/api/bug-reports/{id}/rating", h.RequireAuthAPI(h.APIBugReportRating))

		// Player ("bad actor") reports: share the ticket tables via type='player'.
		r.With(httprate.LimitByIP(6, time.Minute)).Post("/api/player-reports", h.RequireAuthAPI(h.CreatePlayerReport))
		r.Post("/api/admin/bug-reports/{id}/actioned", h.RequireMod(h.AdminPlayerReportActioned))

		// Bug reports: staff/admin management (thread view + reply + invite reuse the endpoints above).
		r.Get("/api/admin/bug-reports", h.RequireMod(h.AdminBugReportsList))
		r.Get("/api/admin/staff", h.RequireMod(h.AdminStaffList))
		r.Post("/api/admin/bug-reports/{id}/status", h.RequireMod(h.AdminBugReportStatus))
		r.Post("/api/admin/bug-reports/{id}/priority", h.RequireMod(h.AdminBugReportPriority))
		r.Post("/api/admin/bug-reports/{id}/assign", h.RequireMod(h.AdminBugReportAssign))
		r.Post("/api/admin/bug-reports/{id}/labels", h.RequireMod(h.AdminBugReportLabelAdd))
		r.Delete("/api/admin/bug-reports/{id}/labels/{labelId}", h.RequireMod(h.AdminBugReportLabelRemove))
		r.Get("/api/admin/bug-report-labels", h.RequireMod(h.AdminBugLabels))
		r.Post("/api/admin/bug-report-labels", h.RequireAdmin(h.AdminBugLabels))
		r.Put("/api/admin/bug-report-labels/{id}", h.RequireAdmin(h.AdminBugLabel))
		r.Delete("/api/admin/bug-report-labels/{id}", h.RequireAdmin(h.AdminBugLabel))
		r.Get("/api/admin/bug-report-macros", h.RequireMod(h.AdminBugMacros))
		r.Post("/api/admin/bug-report-macros", h.RequireAdmin(h.AdminBugMacros))
		r.Put("/api/admin/bug-report-macros/{id}", h.RequireAdmin(h.AdminBugMacro))
		r.Delete("/api/admin/bug-report-macros/{id}", h.RequireAdmin(h.AdminBugMacro))

		r.Get("/api/raid/overview", h.APIRaidOverview)
		// Past rotations, joined from the appearance fact table to the boss
		// dimension. Public and read only, like the overview above.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/api/raid/history", h.APIRaidHistory)
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/api/raid/history/{boss}", h.APIRaidHistoryOfBoss)
		r.Get("/api/raid/state", h.RequireAuth(h.APIRaidState))
		r.Post("/api/raid/queue", h.RequireAuth(h.APIRaidQueueJoin))
		r.Delete("/api/raid/queue", h.RequireAuth(h.APIRaidQueueLeave))
		r.Post("/api/raid/lobbies", h.RequireAuth(h.APIRaidLobbyCreate))
		r.Delete("/api/raid/lobbies/{id}", h.RequireAuth(h.APIRaidLobbyCancel))
		r.Post("/api/raid/lobbies/{id}/confirm", h.RequireAuth(h.APIRaidLobbyConfirm))
		r.Post("/api/raid/lobbies/{id}/leave", h.RequireAuth(h.APIRaidLobbyLeave))
		r.Post("/api/raid/lobbies/{id}/kick", h.RequireAuth(h.APIRaidLobbyKick))
		r.Post("/api/raid/lobbies/{id}/invited", h.RequireAuth(h.APIRaidLobbyInvited))
		r.Post("/api/raid/lobbies/{id}/report", h.RequireAuth(h.APIRaidLobbyReport))
		r.Post("/api/raid/lobbies/{id}/feedback", h.RequireAuth(h.APIRaidLobbyFeedback))

		r.Post("/admin/users/{id}/special-rank", h.RequireAdmin(h.AdminSetSpecialRank))

		r.Get("/api/awards", h.APIAwardsList)
		r.Get("/api/awards/of/{username}", h.APIAwardsOf)
		r.Get("/api/shinies/of/{username}", h.APIShiniesOfUser)
		r.Get("/api/users/search", h.RequireAuth(h.APIUsersSearch))
		r.Post("/api/awards/{id}/grant", h.RequireAuth(h.APIAwardGrant))

		r.Get("/api/admin/awards", h.RequireMod(h.AdminAwardsList))
		r.Post("/api/admin/awards", h.RequireAdmin(h.AdminAwardCreate))
		r.Patch("/api/admin/awards/{id}", h.RequireAdmin(h.AdminAwardUpdate))
		r.Delete("/api/admin/awards/{id}", h.RequireAdmin(h.AdminAwardDelete))
		r.Delete("/api/admin/award-grants/{id}", h.RequireMod(h.AdminAwardGrantDelete))

		r.Get("/api/admin/trust/{id}", h.RequireMod(h.AdminTrustEvents))
		r.Post("/admin/users/{id}/trust-adjust", h.RequireAdmin(h.AdminTrustAdjust))
		r.Post("/admin/users/{id}/trust-recompute", h.RequireAdmin(h.AdminTrustRecompute))

		r.Get("/api/admin/raid-lobbies", h.RequireMod(h.AdminRaidLobbiesList))
		r.Delete("/api/admin/raid-lobbies/{id}", h.RequireMod(h.APIRaidLobbyCancel))

		r.Get("/api/trainer-sprite/{slug}", h.APITrainerSprite)

		// Costume sprites are proxied and cached by us, never hotlinked: the shiny checklist
		// would otherwise push a request per costumed row onto the mined-asset host.
		r.Get("/api/costume-sprite/{file}", costumes.SpriteProxy(cacheDir()))

		// The costume review queue, and naming from it. A name lands in an overlay file merged over
		// the embedded labels, so it is live in the picker at once (same trick as approved
		// translations). Naming can only ever ADD: costumes.Name refuses a code that already has a
		// label, because a label is free text trainers have typed and renaming one orphans their art.
		r.Get("/api/admin/costumes", h.RequireAdmin(h.AdminCostumes))
		r.Post("/api/admin/costumes/name", h.RequireAdmin(h.AdminNameCostume))
		r.Post("/api/admin/costumes/hide", h.RequireAdmin(h.AdminHideCostume))
		r.Delete("/api/admin/costumes/name", h.RequireAdmin(h.AdminUnnameCostume))
		r.With(httprate.LimitByIP(5, time.Minute)).Post("/admin/check-costumes", h.RequireAdmin(h.AdminCheckCostumes))

		r.Get("/api/admin/sprite-locks", h.RequireAdmin(h.AdminGetSpriteLocks))
		r.Post("/api/admin/sprite-lock/{slug}", h.RequireAdmin(h.AdminSetSpriteLock))
		r.Delete("/api/admin/sprite-lock/{slug}", h.RequireAdmin(h.AdminDeleteSpriteLock))

		// Shiny dex availability: which species are in Pokemon GO and which have a shiny released.
		// Overrides the embedded baseline so a shiny release is a checkbox, not a deploy.
		r.Get("/api/admin/shiny-dex", h.RequireAdmin(h.AdminGetShinyDex))
		r.Put("/api/admin/shiny-dex/{dex}", h.RequireAdmin(h.AdminSetShinyDexFlags))
		r.Delete("/api/admin/shiny-dex/{dex}", h.RequireAdmin(h.AdminResetShinyDexFlags))
		r.Post("/api/admin/shiny-dex/bulk", h.RequireAdmin(h.AdminBulkSetShinyDexFlags))

		r.Get("/api/weather", h.RequireAuth(h.APIWeather))

		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/data", h.APIData)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/maxbattles", h.APIMaxBattles)
		// Raised from 10 to match the detail endpoint below. This is an
		// unauthenticated read that every visitor to /events performs, and the
		// reasoning recorded further down (a household, a campus or carrier grade
		// NAT can put many genuine trainers behind one address) applies here just
		// as much as it does to login. apiBW, the bandwidth cap, is the real abuse
		// ceiling and is untouched.
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/api/events", h.APIEvents)
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/api/events/{id}", h.APIEventDetail)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/moves", h.APIMoves)

		r.Get("/api/app/data", h.RequireAuthAPI(h.APIData))

		// Unauthenticated and the most CPU-expensive endpoint in the app: it
		// re-unmarshals the master species blob per request and the solver runs up to
		// roughly 400k iterations. It had no limit at all. 30/min leaves ordinary
		// interactive use untouched (a user checks one Pokemon at a time) while
		// bounding what a single address can burn.
		r.With(httprate.LimitByIP(30, time.Minute)).Post("/api/iv/calculate", h.IVCalculate)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/api/iv/ocr", h.RequireAuthAPI(h.IVFromOCR))
		// The box endpoints had no limiter, which was survivable while the only
		// caller was the calculator's own save button. There is a form in front
		// of them now, and the box holds up to 9000 rows, so a list call is the
		// most expensive authenticated read on the site. Generous enough that no
		// real trainer notices, tight enough that nobody loops it.
		r.With(httprate.LimitByIP(60, time.Minute)).Get("/api/iv/pokemon", h.RequireAuthAPI(h.ListPokemonIV))
		r.With(httprate.LimitByIP(60, time.Minute)).Post("/api/iv/pokemon", h.RequireAuthAPI(h.SavePokemonIV))
		r.With(httprate.LimitByIP(60, time.Minute)).Delete("/api/iv/pokemon/{id}", h.RequireAuthAPI(h.DeletePokemonIV))

		r.With(httprate.LimitAll(2, 10*time.Minute)).Post("/api/refresh", h.RequireAPIAccess(h.APIRefresh))

		r.Get("/api/private/data", h.RequireAPIAccess(h.APIData))
		r.Get("/api/private/raids", h.RequireAPIAccess(h.APIRaids))
		r.Get("/api/private/maxbattles", h.RequireAPIAccess(h.APIMaxBattles))
		r.Get("/api/private/events", h.RequireAPIAccess(h.APIEvents))
		r.Get("/api/private/events/{id}", h.RequireAPIAccess(h.APIEventDetail))
		r.Get("/api/private/pokemon", h.RequireAPIAccess(h.APIPokemon))
		r.Get("/api/private/moves", h.RequireAPIAccess(h.APIMoves))

		r.Get("/api/shinies", h.RequireAuth(h.APIShiniesGet))
		r.Post("/api/shinies", h.RequireAuth(h.APIShiniesAdd))
		r.Put("/api/shinies/{id}", h.RequireAuth(h.APIShiniesUpdate))
		r.Delete("/api/shinies/{id}", h.RequireAuth(h.APIShiniesDelete))
		r.Post("/api/shinies/{id}/evolve", h.RequireAuth(h.APIShiniesEvolve))
	})

	return r
}
