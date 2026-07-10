package telemetry

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// Statter provides working-set size.
type Statter interface{ Stats() workingset.Stats }

// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

// LimiterStatser provides absolute limiter gauges.
type LimiterStatser interface{ LimiterStats() obs.LimiterStats }

// Reporter periodically logs working-set and OBS-request telemetry.
type Reporter struct {
	Interval time.Duration
	Enabled  *atomic.Bool
	Stats    Statter
	Snap     Snapshotter
	Limiter  LimiterStatser // optional; nil disables limiter fields
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
		args := []any{
			"window", r.Interval.String(),
			"ws_packages", s.Total,
			"ws_inflight", s.Inflight,
			"ws_by_state", s.ByState,
			"obs_window", total,
			"obs_total", cumulative,
			"obs_req_per_s", rate,
			"obs_by_endpoint", perOp,
		}
		if r.Limiter != nil {
			if ls := r.Limiter.LimiterStats(); ls.Enabled {
				args = append(args, "limiter_remaining", ls.Remaining, "limiter_waits", ls.Waits)
			}
		}
		slog.Info("telemetry", args...)
	}
	return cur
}

// Run ticks every Interval until ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	if r.Interval <= 0 {
		slog.Warn("telemetry: non-positive interval, reporter disabled", "interval", r.Interval)
		return
	}
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
