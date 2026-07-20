package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
)

// PresenceGate is what the router needs from the idle-mode gate:
// heartbeats in, polling state out.
type PresenceGate interface {
	Beater
	PollState
}

// NewRouter creates the chi router with all API routes registered.
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client, root string, ws Statter, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration, gate PresenceGate) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// All API requests are user-initiated: they bypass the OBS client's
	// background rate limiter so users never queue behind polling traffic.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(obs.Interactive(req.Context())))
		})
	})
	releaseArtifacts := newReleaseArtifactsCache(10 * time.Minute)
	metadataCache := newBinaryListCache(5 * time.Minute)
	overview := newOverviewCache(60 * time.Second)

	r.Route("/api/products/{product}/{tier}/{version}", func(r chi.Router) {
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

	r.Get("/api/telemetry", telemetryStatusHandler(telemetryEnabled, telemetryInterval))
	r.Post("/api/telemetry", telemetrySetHandler(telemetryEnabled))
	r.Post("/api/presence", presenceHandler(gate))

	r.Get("/api/metrics", metricsHandler(obsClient, ws, h, db, gate))

	r.Get("/api/overview", overviewHandler(db, root, overview))
	r.Get("/api/cve/scans", cveScansHandler(db))

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
