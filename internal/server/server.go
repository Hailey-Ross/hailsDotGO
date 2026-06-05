package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"pogo.hails.cc/internal/handlers"
	"pogo.hails.cc/internal/pogodata"
)

func New(store *pogodata.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	h := handlers.New(store)

	r.Get("/", h.Home)
	r.Get("/raids", h.Raids)
	r.Get("/dps", h.DPS)
	r.Get("/pvp", h.PVP)
	r.Get("/events", h.Events)
	r.Get("/changelog", h.Changelog)

	r.Get("/api/data", h.APIData)
	r.Get("/api/raids", h.APIRaids)
	r.Get("/api/pokemon", h.APIPokemon)
	r.Get("/api/moves", h.APIMoves)
	r.Post("/api/refresh", h.APIRefresh)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return r
}
