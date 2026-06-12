package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/percona/obs-dashboard/internal/hub"
)

// NewRouter creates the chi router with all API routes registered.
func NewRouter(db *sql.DB, h *hub.Hub) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db))
		r.Get("/events", eventsHandler(db))
	})

	r.Get("/api/pr/packages", prPackagesHandler(db))

	r.Route("/api/pr/{pr}/{subproject}/{version}", func(r chi.Router) {
		r.Get("/packages", prContextPackagesHandler(db))
		r.Get("/events", prContextEventsHandler(db))
	})

	r.Get("/api/stream", streamHandler(h))

	return r
}

func streamHandler(h *hub.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch := make(chan []byte, 16)
		h.Register((chan<- []byte)(ch))
		defer h.Unregister((chan<- []byte)(ch))

		for {
			select {
			case <-r.Context().Done():
				return
			case payload := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}
