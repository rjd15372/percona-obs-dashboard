package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter creates the chi router with all API routes registered.
func NewRouter(db *sql.DB) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db))
		r.Get("/events", eventsHandler(db))
	})

	r.Get("/api/pr/packages", prPackagesHandler(db))

	return r
}
