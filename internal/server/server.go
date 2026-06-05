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
	r.Get("/changelog", h.Changelog)

	// Auth
	r.Get("/login", h.LoginPage)
	r.Post("/login", h.Login)
	r.Get("/register", h.RegisterPage)
	r.Post("/register", h.Register)
	r.Post("/logout", h.Logout)

	// Protected pages
	r.Get("/shinies", h.RequireAuth(h.ShiniesPage))
	r.Get("/admin", h.RequireAdmin(h.AdminPage))
	r.Post("/admin/settings", h.RequireAdmin(h.AdminUpdateSettings))
	r.Post("/admin/invite", h.RequireAdmin(h.AdminGenerateInvite))

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
