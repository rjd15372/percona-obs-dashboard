// Package unblocker works around an OBS bug where build targets stay in
// blocked state after their dependencies have finished: targets blocked
// longer than a threshold get their build re-triggered automatically.
// Design: docs/superpowers/specs/2026-07-14-auto-unblock-trigger-design.md
package unblocker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// Rebuilder triggers an OBS rebuild for one build target. Satisfied by
// *obs.Client.
type Rebuilder interface {
	Rebuild(ctx context.Context, project, repo, arch, pkg string) error
}

const (
	sweepInterval       = 5 * time.Minute
	maxAttempts         = 3  // per blocked episode
	maxTriggersPerSweep = 10 // protects the shared per-minute OBS budget
)

// episodeKey identifies one continuous blocked episode: any state
// transition writes a new duration row with a new entered_at, producing a
// new key — which is what resets the attempt count.
type episodeKey struct {
	project, pkg, repo, arch string
	enteredAt                time.Time
}

type episode struct {
	attempts    int
	lastTrigger time.Time
}

// Sweeper periodically rebuilds targets stuck in blocked state longer than
// Threshold. Detection reads target_state_durations (maintained by the
// poller and MQ consumer) — it adds no OBS read traffic; only the rebuild
// triggers hit OBS, through the client's background rate limiter.
type Sweeper struct {
	DB        *sql.DB
	Rebuilder Rebuilder
	Threshold time.Duration

	now      func() time.Time // injectable for tests; nil = time.Now
	episodes map[episodeKey]*episode
}

// Run ticks every sweepInterval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	if s.now == nil {
		s.now = time.Now
	}
	if s.episodes == nil {
		s.episodes = make(map[episodeKey]*episode)
	}
	now := s.now().UTC()

	stale, err := store.QueryStaleBlockedTargets(s.DB, now.Add(-s.Threshold))
	if err != nil {
		slog.Warn("unblocker: query stale blocked targets", "err", err)
		return
	}

	// Drop episodes that no longer match a current stale row (unblocked,
	// state changed, or aged out) so the map stays bounded.
	current := make(map[episodeKey]bool, len(stale))
	for _, t := range stale {
		current[keyOf(t)] = true
	}
	for k := range s.episodes {
		if !current[k] {
			delete(s.episodes, k)
		}
	}

	triggered := 0
	for _, t := range stale {
		if triggered >= maxTriggersPerSweep {
			break
		}
		k := keyOf(t)
		ep := s.episodes[k]
		if ep == nil {
			ep = &episode{}
			s.episodes[k] = ep
		}
		if ep.attempts >= maxAttempts {
			continue
		}
		// Pace retries at the threshold interval, not the sweep interval.
		if !ep.lastTrigger.IsZero() && now.Sub(ep.lastTrigger) < s.Threshold {
			continue
		}
		// Count the attempt regardless of outcome: a persistently erroring
		// target caps out instead of retrying forever.
		ep.attempts++
		ep.lastTrigger = now
		triggered++

		blockedFor := now.Sub(t.EnteredAt).Round(time.Minute)
		if err := s.Rebuilder.Rebuild(ctx, t.Project, t.Repo, t.Arch, t.Package); err != nil {
			slog.Warn("unblocker: rebuild trigger failed",
				"project", t.Project, "package", t.Package, "repo", t.Repo, "arch", t.Arch,
				"blocked_for", blockedFor, "attempt", ep.attempts, "err", err)
			continue
		}
		slog.Info("unblocker: triggered rebuild",
			"project", t.Project, "package", t.Package, "repo", t.Repo, "arch", t.Arch,
			"blocked_for", blockedFor, "attempt", ep.attempts)
	}
}

func keyOf(t store.BlockedTarget) episodeKey {
	return episodeKey{
		project:   t.Project,
		pkg:       t.Package,
		repo:      t.Repo,
		arch:      t.Arch,
		enteredAt: t.EnteredAt.UTC(),
	}
}
