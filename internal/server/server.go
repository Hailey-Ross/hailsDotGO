package server

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"pogo.hails.cc/internal/handlers"
	"pogo.hails.cc/internal/pogodata"
)

func New(store *pogodata.Store, db *sql.DB) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	h := handlers.New(store, db)

	// Public pages
	r.Get("/", h.Home)
	r.Get("/raids", h.Raids)
	r.Get("/dps", h.DPS)
	r.Get("/pvp", h.PVP)
	r.Get("/events", h.Events)
	r.Get("/credits", h.Credits)
	r.Get("/changelog", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/credits?tab=changelog", http.StatusMovedPermanently)
	})

	// Auth
	r.Get("/login", h.LoginPage)
	r.Post("/login", h.Login)
	r.Get("/register", h.RegisterPage)
	r.Post("/register", h.Register)
	r.Post("/logout", h.Logout)

	// Public community pages
	r.Get("/trainers", h.TrainersPage)

	// Protected pages
	r.Get("/settings", h.RequireAuth(h.SettingsPage))
	r.Post("/settings", h.RequireAuth(h.SettingsUpdate))
	r.Get("/shinies", h.RequireAuth(h.ShiniesPage))
	r.Get("/admin", h.RequireMod(h.AdminPage))
	r.Post("/admin/settings", h.RequireAdmin(h.AdminUpdateSettings))
	r.Post("/admin/invite", h.RequireAdmin(h.AdminGenerateInvite))
	r.Post("/admin/invite/{token}/cancel", h.RequireAdmin(h.AdminCancelInvite))

	// User management (mod+)
	r.Get("/admin/users", h.RequireMod(h.AdminUsersAPI))
	r.Post("/admin/users/{id}/password", h.RequireMod(h.AdminResetPassword))
	r.Post("/admin/users/{id}/username", h.RequireMod(h.AdminChangeUsername))
	r.Post("/admin/users/{id}/disable", h.RequireMod(h.AdminToggleDisable))
	r.Post("/admin/users/{id}/role", h.RequireAdmin(h.AdminChangeRole))
	r.Post("/admin/users/{id}/directory-hide", h.RequireMod(h.AdminToggleDirectoryHide))
	r.Post("/admin/users/{id}/raid-ban", h.RequireMod(h.AdminToggleRaidBan))
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
	r.Post("/api/raid-posts/{id}/rate", h.RequireAuth(h.APIRaidPostsRate))

	// Trainer sprite proxy (cached, public)
	r.Get("/api/trainer-sprite/{slug}", h.APITrainerSprite)

	// Public data API
	r.Get("/api/data", h.APIData)
	r.Get("/api/raids", h.APIRaids)
	r.Get("/api/pokemon", h.APIPokemon)
	r.Get("/api/moves", h.APIMoves)
	r.Post("/api/refresh", h.APIRefresh)

	// Protected user API
	r.Get("/api/shinies", h.RequireAuth(h.APIShiniesGet))
	r.Post("/api/shinies", h.RequireAuth(h.APIShiniesAdd))
	r.Put("/api/shinies/{id}", h.RequireAuth(h.APIShiniesUpdate))
	r.Delete("/api/shinies/{id}", h.RequireAuth(h.APIShiniesDelete))

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return r
}
