package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// processStart approximates process start (package init happens within
// milliseconds of it); /api/metrics reports uptime relative to this.
var processStart = time.Now()

// Statter provides working-set stats for the metrics endpoint.
type Statter interface{ Stats() workingset.Stats }

// ClientCounter reports the number of connected SSE clients.
type ClientCounter interface{ Clients() int }

type metricsResponse struct {
	OBS           obsSection     `json:"obs"`
	Limiter       limiterSection `json:"limiter"`
	WorkingSet    wsSection      `json:"working_set"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	SSEClients    int            `json:"sse_clients"`
}

type obsSection struct {
	Total        int64            `json:"total"`
	ByEndpoint   map[string]int64 `json:"by_endpoint"`
	ReqPerS      float64          `json:"req_per_s"`
	Windows      map[string]int64 `json:"windows"`       // trailing counts: "6h", "12h", "24h", "7d", "30d"
	WindowsPrev  map[string]int64 `json:"windows_prev"`  // previous adjacent periods: "6h", "12h", "24h", "7d"
	Series       []int64          `json:"series"`        // requests per 5-min bucket, last 24h, oldest first
	OldestSample string           `json:"oldest_sample"` // RFC3339; "" while no samples exist
}

type limiterSection struct {
	Enabled   bool  `json:"enabled"`
	Budget    int   `json:"budget"`
	Remaining int64 `json:"remaining"`
	Waits     int64 `json:"waits"`
}

type wsSection struct {
	Packages int            `json:"packages"`
	Inflight int            `json:"inflight"`
	ByState  map[string]int `json:"by_state"`
}

// metricsHandler handles GET /api/metrics: absolute OBS request counts,
// the trailing-minute request rate, persisted trailing 6h/12h/24h/7d/30d
// window totals, previous-period window baselines, the 24h 5-minute request
// series, and the oldest persisted sample timestamp, limiter gauges,
// working-set stats, process uptime, and connected SSE clients.
func metricsHandler(obsClient *obs.Client, ws Statter, clients ClientCounter, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		byEndpoint := obsClient.MetricsSnapshot()
		var total int64
		for _, v := range byEndpoint {
			total += v
		}
		ls := obsClient.LimiterStats()
		s := ws.Stats()

		now := time.Now().UTC()
		windows, err := store.QueryMetricsWindows(db, now)
		if err != nil {
			slog.Warn("api: query metrics windows", "err", err)
			windows = map[string]int64{"6h": 0, "12h": 0, "24h": 0, "7d": 0, "30d": 0}
		}
		prev, err := store.QueryMetricsPrevWindows(db, now)
		if err != nil {
			slog.Warn("api: query metrics prev windows", "err", err)
			prev = map[string]int64{"6h": 0, "12h": 0, "24h": 0, "7d": 0}
		}
		series, err := store.QueryMetricsSeries(db, now)
		if err != nil {
			slog.Warn("api: query metrics series", "err", err)
			series = make([]int64, store.SeriesBuckets)
		}
		oldest, err := store.OldestMetricsSample(db)
		if err != nil {
			slog.Warn("api: query oldest metrics sample", "err", err)
			oldest = time.Time{}
		}
		var oldestStr string
		if !oldest.IsZero() {
			oldestStr = oldest.UTC().Format(time.RFC3339)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse{
			OBS: obsSection{
				Total:        total,
				ByEndpoint:   byEndpoint,
				ReqPerS:      obsClient.RatePerSecond(),
				Windows:      windows,
				WindowsPrev:  prev,
				Series:       series,
				OldestSample: oldestStr,
			},
			Limiter: limiterSection{
				Enabled:   ls.Enabled,
				Budget:    ls.Budget,
				Remaining: ls.Remaining,
				Waits:     ls.Waits,
			},
			WorkingSet: wsSection{
				Packages: s.Total,
				Inflight: s.Inflight,
				ByState:  s.ByState,
			},
			UptimeSeconds: int64(time.Since(processStart).Seconds()),
			SSEClients:    clients.Clients(),
		})
	}
}
