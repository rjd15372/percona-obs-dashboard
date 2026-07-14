// Package metricsampler persists OBS request counts as periodic per-op
// delta samples so trailing-window metrics survive restarts.
// Design: docs/superpowers/specs/2026-07-14-persistent-metrics-design.md
package metricsampler

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

const sampleInterval = 5 * time.Minute

// Sampler diffs the OBS request counters every sampleInterval and inserts
// one metrics_samples row per op with a non-zero delta. The baseline
// starts at process boot, so at most one unflushed partial bucket is lost
// per restart. Not safe for concurrent use; Run drives it serially.
type Sampler struct {
	DB   *sql.DB
	Snap Snapshotter

	prev map[string]int64 // last snapshot; nil until Run initializes it
	now  func() time.Time // injectable for tests; nil = time.Now
}

// Run ticks every sampleInterval until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	if s.now == nil {
		s.now = time.Now
	}
	if s.prev == nil {
		s.prev = s.Snap.MetricsSnapshot()
	}
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample()
		}
	}
}

func (s *Sampler) sample() {
	if s.now == nil {
		s.now = time.Now
	}
	cur := s.Snap.MetricsSnapshot()
	deltas := make(map[string]int64)
	for op, c := range cur {
		if d := c - s.prev[op]; d > 0 {
			deltas[op] = d
		}
	}
	// Advance the baseline whether or not the insert succeeds: a failed
	// insert loses that bucket instead of double-counting it next tick.
	s.prev = cur
	if len(deltas) == 0 {
		return
	}
	if err := store.InsertMetricsSamples(s.DB, s.now().UTC(), deltas); err != nil {
		slog.Warn("metricsampler: insert samples", "err", err)
	}
}
