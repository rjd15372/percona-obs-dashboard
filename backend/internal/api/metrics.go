package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// processStart approximates process start (package init happens within
// milliseconds of it); /api/metrics reports uptime relative to this.
var processStart = time.Now()

// Statter provides working-set stats for the metrics endpoint.
type Statter interface{ Stats() workingset.Stats }

type metricsResponse struct {
	OBS           obsSection     `json:"obs"`
	Limiter       limiterSection `json:"limiter"`
	WorkingSet    wsSection      `json:"working_set"`
	UptimeSeconds int64          `json:"uptime_seconds"`
}

type obsSection struct {
	Total      int64            `json:"total"`
	ByEndpoint map[string]int64 `json:"by_endpoint"`
	ReqPerS    float64          `json:"req_per_s"`
	Windows    map[string]int64 `json:"windows"` // trailing counts: "6h", "12h", "24h"
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
// the trailing-minute request rate, trailing 6h/12h/24h window totals,
// limiter gauges, working-set stats, and process uptime.
func metricsHandler(obsClient *obs.Client, ws Statter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		byEndpoint := obsClient.MetricsSnapshot()
		var total int64
		for _, v := range byEndpoint {
			total += v
		}
		ls := obsClient.LimiterStats()
		s := ws.Stats()
		h6, h12, h24 := obsClient.WindowCounts()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse{
			OBS: obsSection{
				Total:      total,
				ByEndpoint: byEndpoint,
				ReqPerS:    obsClient.RatePerSecond(),
				Windows:    map[string]int64{"6h": h6, "12h": h12, "24h": h24},
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
		})
	}
}
