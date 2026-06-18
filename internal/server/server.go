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
		// SAMEORIGIN (not DENY) so the translator preview iframe can embed
		// site pages; foreign-origin framing stays blocked.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
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
	h.StartRaidSweeper()

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

		r.Get("/login", h.LoginPage)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/login", h.Login)
		r.Get("/register", h.RegisterPage)
		r.With(httprate.LimitByIP(5, time.Minute)).Post("/register", h.Register)
		r.Post("/logout", h.Logout)
		r.Post("/lang", h.SetLang)

		r.Get("/trainers", h.TrainersPage)
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
		r.Post("/admin/users/{id}/api-access", h.RequireSuperAdmin(h.AdminToggleAPIAccess))
		r.Post("/admin/users/{id}/translator", h.RequireSuperAdmin(h.AdminToggleTranslator))
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

		r.Get("/api/raid/overview", h.APIRaidOverview)
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

		r.Get("/api/weather", h.RequireAuth(h.APIWeather))

		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/data", h.APIData)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/maxbattles", h.APIMaxBattles)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/events", h.APIEvents)
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/api/events/{id}", h.APIEventDetail)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/moves", h.APIMoves)

		r.Get("/api/app/data", h.RequireAuthAPI(h.APIData))

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
	})

	return r
}
