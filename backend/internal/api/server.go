package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
)

// NewRouter creates the chi router with all API routes registered.
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client, root string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	releaseArtifacts := newReleaseArtifactsCache(10 * time.Minute)
	metadataCache := newBinaryListCache(5 * time.Minute)

	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db, root))
		r.Get("/events", eventsHandler(db))
		r.Get("/repos", reposHandler(db))
	})

	r.Route("/api/releases/ppg/{version}", func(r chi.Router) {
		r.Get("/packages", releasesPackagesHandler(db, root))
		r.Get("/repos", releasesReposHandler(db, root))
		r.Get("/artifacts", releaseArtifactsHandler(db, obsClient, root, releaseArtifacts))
	})

	r.Get("/api/pr/packages", prPackagesHandler(db))

	r.Route("/api/pr/{pr}/{version}", func(r chi.Router) {
		r.Get("/packages", prContextPackagesHandler(db, root))
		r.Get("/events", prContextEventsHandler(db, root))
		r.Get("/repos", prReposHandler(db, root))
	})

	r.Get("/api/stream", streamHandler(h))
	r.Get("/api/binaries", binariesHandler(obsClient))
	r.Post("/api/rebuild", rebuildHandler(obsClient))
	r.Post("/api/artifacts/metadata", artifactMetadataHandler(obsClient, metadataCache))

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

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case payload := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}
