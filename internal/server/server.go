package server

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/gorilla/csrf"
	"pogo.hails.cc/internal/handlers"
	"pogo.hails.cc/internal/pogodata"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

func New(store *pogodata.Store, db *sql.DB, csrfKey []byte) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(securityHeaders)

	h := handlers.New(store, db)

	// Bandwidth limiter: 15 MB per IP per 5-minute window; 30-minute block on breach.
	// Counts aggregate bytes across all four public API endpoints for the same IP.
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

	// All remaining routes get CSRF protection via a group.
	csrfDebug := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, cookieErr := r.Cookie("_gorilla_csrf")
		log.Printf("CSRF FAIL: method=%s path=%s reason=%v token=%q cookie_present=%v",
			r.Method, r.URL.Path,
			csrf.FailureReason(r),
			r.Header.Get("X-CSRF-Token"),
			cookieErr == nil,
		)
		http.Error(w, "Forbidden", http.StatusForbidden)
	})

	r.Group(func(r chi.Router) {
		r.Use(csrf.Protect(csrfKey, csrf.Secure(true), csrf.SameSite(csrf.SameSiteStrictMode), csrf.ErrorHandler(csrfDebug)))

		// Public pages
		r.Get("/", h.Home)
		r.Get("/raids", h.Raids)
		r.Get("/dps", h.DPS)
		r.Get("/pvp", h.PVP)
		r.Get("/events", h.Events)
		r.Get("/shinydex", h.ShinyDex)
		r.Get("/credits", h.Credits)
		r.Get("/changelog", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/credits?tab=changelog", http.StatusMovedPermanently)
		})

		// Auth (rate-limited)
		r.Get("/login", h.LoginPage)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/login", h.Login)
		r.Get("/register", h.RegisterPage)
		r.With(httprate.LimitByIP(5, time.Minute)).Post("/register", h.Register)
		r.Post("/logout", h.Logout)
		r.Post("/lang", h.SetLang)

		// Public community pages
		r.Get("/trainers", h.TrainersPage)

		// Store / Donations
		r.Get("/store", h.StorePage)
		r.Post("/store/checkout", h.RequireAuth(h.StoreCheckout))
		r.Get("/store/return", h.StoreReturn)
		r.Get("/store/cancel", h.StoreCancel)
		r.Post("/api/store/tag-request", h.RequireAuth(h.StoreTagRequestSubmit))
		r.Post("/api/store/tag-color", h.RequireAuth(h.StoreTagColorUpdate))
		r.Post("/api/store/purchases/cancel", h.RequireAuth(h.StorePurchaseCancel))

		// Protected pages
		r.Get("/settings", h.RequireAuth(h.SettingsPage))
		r.Post("/settings", h.RequireAuth(h.SettingsUpdate))
		r.Get("/shinies", h.RequireAuth(h.ShiniesPage))
		r.Get("/admin", h.RequireMod(h.AdminPage))
		r.Post("/admin/settings", h.RequireAdmin(h.AdminUpdateSettings))
		r.Post("/admin/pages", h.RequireAdmin(h.AdminUpdatePageSettings))
		r.Post("/admin/invite", h.RequireAdmin(h.AdminGenerateInvite))
		r.Post("/admin/invite/{token}/cancel", h.RequireAdmin(h.AdminCancelInvite))

		// Admin stats (admin only)
		r.Get("/api/admin/stats", h.RequireAdmin(h.AdminStatsAPI))

		// Custom tag requests (mod+)
		r.Get("/api/admin/tag-requests", h.RequireMod(h.AdminTagRequestsList))
		r.Post("/api/admin/tag-requests/{id}/approve", h.RequireMod(h.AdminTagRequestApprove))
		r.Post("/api/admin/tag-requests/{id}/reject", h.RequireMod(h.AdminTagRequestReject))
		r.Post("/api/admin/tag-requests/{id}/revision", h.RequireMod(h.AdminTagRequestRevision))

		// Store item management (admin+)
		r.Get("/api/admin/store-items", h.RequireAdmin(h.AdminStoreItemsList))
		r.Post("/api/admin/store-items/{id}/toggle", h.RequireAdmin(h.AdminToggleStoreItem))

		// Tag management (list/assign/remove: mod+; create/delete: superadmin)
		r.Get("/api/admin/tags", h.RequireMod(h.AdminTagsList))
		r.Post("/api/admin/tags", h.RequireSuperAdmin(h.AdminTagCreate))
		r.Patch("/api/admin/tags/{id}", h.RequireSuperAdmin(h.AdminTagUpdate))
		r.Delete("/api/admin/tags/{id}", h.RequireSuperAdmin(h.AdminTagDelete))
		r.Post("/api/admin/users/{id}/tags", h.RequireMod(h.AdminUserTagAdd))
		r.Delete("/api/admin/users/{id}/tags/{tagId}", h.RequireMod(h.AdminUserTagRemove))

		// User management (mod+)
		r.Get("/admin/users", h.RequireMod(h.AdminUsersAPI))
		r.Post("/admin/users/{id}/password", h.RequireMod(h.AdminResetPassword))
		r.Post("/admin/users/{id}/username", h.RequireMod(h.AdminChangeUsername))
		r.Post("/admin/users/{id}/disable", h.RequireMod(h.AdminToggleDisable))
		r.Post("/admin/users/{id}/role", h.RequireAdmin(h.AdminChangeRole))
		r.Post("/admin/users/{id}/api-access", h.RequireSuperAdmin(h.AdminToggleAPIAccess))
		r.Post("/admin/refresh-data", h.RequireSuperAdmin(h.AdminRefreshData))
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

		// Raid finder API (list is public; actions require auth)
		r.Get("/api/raid-posts", h.APIRaidPostsList)
		r.Post("/api/raid-posts", h.RequireAuth(h.APIRaidPostsCreate))
		r.Delete("/api/raid-posts/{id}", h.RequireAuth(h.APIRaidPostsDelete))
		r.Post("/api/raid-posts/{id}/join", h.RequireAuth(h.APIRaidPostsJoin))
		r.Delete("/api/raid-posts/{id}/join", h.RequireAuth(h.APIRaidPostsLeave))
		r.Post("/api/raid-posts/{id}/confirm", h.RequireAuth(h.APIRaidPostsConfirm))
		r.Post("/api/raid-posts/{id}/invite", h.RequireAuth(h.APIRaidPostsMarkInvited))
		r.Post("/api/raid-posts/{id}/accept", h.RequireAuth(h.APIRaidPostsAccept))
		r.Post("/api/raid-posts/{id}/decline", h.RequireAuth(h.APIRaidPostsDecline))
		r.Post("/api/raid-posts/{id}/rate", h.RequireAuth(h.APIRaidPostsRate))

		// Trainer sprite proxy (cached, public)
		r.Get("/api/trainer-sprite/{slug}", h.APITrainerSprite)

		// Weather (auth required; uses stored city, cached per city)
		r.Get("/api/weather", h.RequireAuth(h.APIWeather))

		// Public game data API
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/data", h.APIData)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/maxbattles", h.APIMaxBattles)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/events", h.APIEvents)
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/api/events/{id}", h.APIEventDetail)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/moves", h.APIMoves)

		// Session-auth game data (site frontend — no rate limit for logged-in users)
		r.Get("/api/app/data", h.RequireAuthAPI(h.APIData))

		// Refresh — requires API access; globally rate-limited
		r.With(httprate.LimitAll(2, 10*time.Minute)).Post("/api/refresh", h.RequireAPIAccess(h.APIRefresh))

		// Private game data API — requires API access, no rate limit
		r.Get("/api/private/data", h.RequireAPIAccess(h.APIData))
		r.Get("/api/private/raids", h.RequireAPIAccess(h.APIRaids))
		r.Get("/api/private/maxbattles", h.RequireAPIAccess(h.APIMaxBattles))
		r.Get("/api/private/events", h.RequireAPIAccess(h.APIEvents))
		r.Get("/api/private/events/{id}", h.RequireAPIAccess(h.APIEventDetail))
		r.Get("/api/private/pokemon", h.RequireAPIAccess(h.APIPokemon))
		r.Get("/api/private/moves", h.RequireAPIAccess(h.APIMoves))

		// Protected user API
		r.Get("/api/shinies", h.RequireAuth(h.APIShiniesGet))
		r.Post("/api/shinies", h.RequireAuth(h.APIShiniesAdd))
		r.Put("/api/shinies/{id}", h.RequireAuth(h.APIShiniesUpdate))
		r.Delete("/api/shinies/{id}", h.RequireAuth(h.APIShiniesDelete))
	})

	return r
}
