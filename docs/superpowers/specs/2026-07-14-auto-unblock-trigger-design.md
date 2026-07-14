# Automatic rebuild trigger for stuck-blocked targets

**Date:** 2026-07-14
**Status:** Approved

## Problem

An OBS bug leaves package build targets in `blocked` state even after the
packages they wait for have finished building. Today the operator notices a
stuck target on the board and manually triggers a rebuild to unblock it. The
dashboard backend should do this automatically: when a target has been
blocked for more than a threshold (30 minutes), trigger its rebuild.

## Decision summary

- **Scope:** devel + staging + PR trees (`is_release = 0`). Release projects
  are excluded — release builds are deliberate.
- **Retry policy:** re-trigger while still blocked, paced at the threshold
  interval, **at most 3 attempts per blocked episode**; any state change
  (including blocked→scheduled→blocked) starts a new episode and resets the
  count.
- **Architecture:** standalone DB-driven sweeper goroutine (like the event
  pruner), reading `target_state_durations` — detection is independent of
  the working-set poll backoff and adds zero OBS read traffic. (Rejected:
  piggybacking on working-set polls — stuck-blocked packages are exactly the
  ones whose unchanged state drives their poll interval toward maximum
  backoff, slowing detection for the packages that matter most.)
- Triggers use the existing `obs.Client.Rebuild` under a background context,
  so the per-minute rate limiter applies as it does to all background
  traffic.
- Opt-in via config (`enabled`, default false in the example file);
  threshold configurable, default 30m.

## Design

### 1. Detection query — `internal/store`

New function:

```go
// BlockedTarget identifies one target currently stuck in blocked state.
type BlockedTarget struct {
    Project   string
    Package   string
    Repo      string
    Arch      string
    EnteredAt time.Time
}

func QueryStaleBlockedTargets(db *sql.DB, cutoff time.Time) ([]BlockedTarget, error)
```

backed by:

```sql
SELECT d.project, d.package, d.repo, d.arch, d.entered_at
FROM target_state_durations d
JOIN packages p ON p.project = d.project AND p.name = d.package
WHERE d.state = 'blocked' AND d.exited_at IS NULL
  AND d.entered_at < :cutoff
  AND p.is_release = 0
```

`target_state_durations` is already maintained by the poller and MQ
consumer; `exited_at IS NULL` selects each target's current state and
`entered_at` is when it entered it. The `is_release = 0` join implements the
scope decision (devel/staging/PRs in, releases out).

### 2. Sweeper — new package `internal/unblocker`

```go
// Rebuilder triggers an OBS rebuild for one target.
type Rebuilder interface {
    Rebuild(ctx context.Context, project, repo, arch, pkg string) error
}

type Sweeper struct {
    DB        *sql.DB
    Rebuilder Rebuilder
    Threshold time.Duration // blocked-for threshold (config, default 30m)
    // internal: now func() time.Time for tests; attempts map
}

func (s *Sweeper) Run(ctx context.Context) // ticks every sweepInterval until ctx cancels
```

Fixed constants (no config knobs without demonstrated need):
`sweepInterval = 5 * time.Minute`, `maxAttempts = 3`,
`maxTriggersPerSweep = 10`.

Per tick:

1. `cutoff = now - Threshold`; fetch stale blocked targets.
2. **Episode/attempt tracking:** in-memory map keyed by
   `(project, package, repo, arch, entered_at)` → `{attempts int, lastTrigger time.Time}`.
   Because `entered_at` is part of the key, any state transition — including
   blocked→scheduled→blocked — produces a new key and thus a fresh episode
   with a zero count. Entries whose key doesn't match any current row are
   pruned each tick, bounding the map.
3. Skip a target when `attempts >= maxAttempts`, or when
   `now - lastTrigger < Threshold` (retries pace at threshold intervals, not
   sweep intervals).
4. Trigger at most `maxTriggersPerSweep` rebuilds per tick (protects the
   shared per-minute OBS budget from a first-run backlog; the remainder is
   handled on later sweeps). Each trigger calls
   `Rebuilder.Rebuild(ctx, ...)` with the sweeper's background context —
   the OBS client's minute limiter applies automatically.
5. Record the attempt (increment + stamp `lastTrigger`) **whether or not the
   call succeeded** — a persistently erroring target caps out at 3 instead
   of retrying forever. Success logs
   `slog.Info("unblocker: triggered rebuild", "project", …, "package", …,
   "repo", …, "arch", …, "blocked_for", …, "attempt", …)`; failure logs the
   same fields via `slog.Warn` plus the error.

### 3. Config and wiring

`config.yaml` gains:

```yaml
unblocker:
  enabled: false   # opt-in
  threshold: 30m
```

Env overrides follow the existing config conventions. `cmd/obsboard/main.go`
starts `go sweeper.Run(ctx)` beside `runPruner` only when
`cfg.Unblocker.Enabled` is true, wiring `obsClient` as the `Rebuilder`.

### 4. Error handling

- Failed `Rebuild` → warn log + attempt still counted (see above).
- Query error → warn log, skip the tick (transient DB issues self-heal).
- Context cancellation exits `Run` cleanly.
- The sweeper performs no DB writes.

### 5. Observability

Log lines per trigger (info/warn as above). The rebuild op also increments
the existing OBS client per-op counter, so triggers are visible in the
`/api/metrics` `obs.by_endpoint` map and the telemetry log line. The
resulting OBS build events flow back through MQ into the event log like any
manual trigger — no synthetic events needed.

## Testing

`internal/unblocker` unit tests with a fake `Rebuilder`, in-memory SQLite,
and an injected clock:

- under-threshold blocked targets are not triggered
- over-threshold targets are triggered once per threshold interval
- attempt cap: no 4th trigger within one episode
- episode reset: same target with a newer `entered_at` gets a fresh count
- per-sweep cap: 11 stale targets → 10 triggers on the first tick
- failed rebuild counts as an attempt
- releases (`is_release = 1`) never trigger (store-level test)
- cancelled context stops `Run`

Store-level test for `QueryStaleBlockedTargets` beside the existing
duration-table tests. Verify: `cd backend && go test ./...` green.

End-to-end expectation: a target stuck in blocked gains an automatic rebuild
within threshold + one sweep interval (~35 minutes worst case), up to 3
times per episode.

## Alternatives considered

- **Piggyback on working-set polls:** rejected — detection latency inherits
  the poll backoff ladder, which is slowest exactly for stuck packages.
- **Unlimited retries / single attempt:** rejected in favor of capped
  retries with episode reset (user decision).
- **Synthetic "auto-rebuild" event in the event log:** rejected — OBS's own
  build events already surface the outcome; a log line + metrics counter
  suffice.
