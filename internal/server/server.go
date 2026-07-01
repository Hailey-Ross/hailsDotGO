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
	h.StartTranslationAutoSync()

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

	// Mobile API -- outside CSRF group (Bearer tokens are not CSRF-vulnerable).
	// Login is public and rate-limited; everything else requires Bearer or cookie auth.
	r.Route("/api/mobile/v1", func(r chi.Router) {
		r.With(httprate.LimitByIP(5, time.Minute)).Post("/auth/login", h.MobileLogin)

		// Public game data aliases with stable versioned URLs for mobile clients.
		// Rate-limited to match the legacy web API (server.go ~line 305).
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/events", h.APIEvents)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/data", h.APIData)
		// Dynamic and polled by the app, so a looser standalone limit.
		r.With(httprate.LimitByIP(20, time.Minute)).Get("/raid/overview", h.MobileRaidOverview)

		// All remaining endpoints require authentication.
		r.Group(func(r chi.Router) {
			r.Use(h.MobileAuthMiddleware())
			r.Use(httprate.LimitByIP(120, time.Minute)) // baseline abuse ceiling
			r.Delete("/auth/session", h.MobileLogout)
			r.Get("/auth/me", h.MobileMe)
			r.Put("/profile", h.MobilePutProfile)
			r.Post("/push/token", h.RegisterPushToken)
			r.Delete("/push/token", h.UnregisterPushToken)

			r.Post("/iv/calculate", h.IVCalculate)
			r.With(httprate.LimitByIP(10, time.Minute)).Post("/iv/ocr", h.IVFromOCR)
			r.Post("/iv/pokemon", h.SavePokemonIV)
			r.Get("/iv/pokemon", h.ListPokemonIV)
			r.Delete("/iv/pokemon/{id}", h.DeletePokemonIV)

			r.Get("/raid/state", h.MobileRaidState)
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
		r.Get("/iv", h.GetIVPage)
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

		r.Get("/api/admin/sprite-locks",          h.RequireAdmin(h.AdminGetSpriteLocks))
		r.Post("/api/admin/sprite-lock/{slug}",   h.RequireAdmin(h.AdminSetSpriteLock))
		r.Delete("/api/admin/sprite-lock/{slug}", h.RequireAdmin(h.AdminDeleteSpriteLock))

		r.Get("/api/weather", h.RequireAuth(h.APIWeather))

		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/data", h.APIData)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/raids", h.APIRaids)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/maxbattles", h.APIMaxBattles)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/events", h.APIEvents)
		r.With(apiBW.Handler, httprate.LimitByIP(30, 2*time.Minute)).Get("/api/events/{id}", h.APIEventDetail)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/pokemon", h.APIPokemon)
		r.With(apiBW.Handler, httprate.LimitByIP(10, 2*time.Minute)).Get("/api/moves", h.APIMoves)

		r.Get("/api/app/data", h.RequireAuthAPI(h.APIData))

		r.Post("/api/iv/calculate", h.IVCalculate)
		r.With(httprate.LimitByIP(10, time.Minute)).Post("/api/iv/ocr", h.RequireAuthAPI(h.IVFromOCR))
		r.Get("/api/iv/pokemon", h.RequireAuthAPI(h.ListPokemonIV))
		r.Post("/api/iv/pokemon", h.RequireAuthAPI(h.SavePokemonIV))
		r.Delete("/api/iv/pokemon/{id}", h.RequireAuthAPI(h.DeletePokemonIV))

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
