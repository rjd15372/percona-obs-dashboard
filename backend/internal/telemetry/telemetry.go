package telemetry

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/percona/obs-dashboard/internal/workingset"
)

// Statter provides working-set size.
type Statter interface{ Stats() workingset.Stats }

// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

// Reporter periodically logs working-set and OBS-request telemetry.
type Reporter struct {
	Interval time.Duration
	Enabled  *atomic.Bool
	Stats    Statter
	Snap     Snapshotter
}

// Diff returns per-op deltas (omitting zero-delta ops) and the total delta.
func Diff(prev, cur map[string]int64) (map[string]int64, int64) {
	perOp := make(map[string]int64)
	var total int64
	for op, c := range cur {
		d := c - prev[op]
		if d != 0 {
			perOp[op] = d
		}
		total += d
	}
	return perOp, total
}

// tick emits a telemetry line (when enabled) and returns the refreshed baseline.
func (r *Reporter) tick(prev map[string]int64) map[string]int64 {
	cur := r.Snap.MetricsSnapshot()
	perOp, total := Diff(prev, cur)
	if r.Enabled.Load() {
		s := r.Stats.Stats()
		var cumulative int64
		for _, v := range cur {
			cumulative += v
		}
		rate := 0.0
		if r.Interval > 0 {
			rate = float64(total) / r.Interval.Seconds()
		}
		slog.Info("telemetry",
			"window", r.Interval.String(),
			"ws_packages", s.Total,
			"ws_inflight", s.Inflight,
			"ws_by_state", s.ByState,
			"obs_window", total,
			"obs_total", cumulative,
			"obs_req_per_s", rate,
			"obs_by_endpoint", perOp,
		)
	}
	return cur
}

// Run ticks every Interval until ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	prev := r.Snap.MetricsSnapshot()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prev = r.tick(prev)
		}
	}
}
