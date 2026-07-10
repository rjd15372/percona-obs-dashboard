# OBS API Traffic Reduction — Design

**Date:** 2026-07-10
**Status:** Approved
**Context:** On the night of 2026-07-09→10, an OBS-side rebuild storm (a distro
meta-change cascade across the `ppg:staging` tiers plus a rebuild loop in
`isv:percona:PR:pr-127:ppg:staging:16:tde`) kept ~200–260 packages continuously
active in the dashboard's working set. The dashboard re-dispatches every
working-set package every 30s and each pass costs at least one OBS API call
(`PackageBuildResults`), plus enrichment calls after state changes. The result
was >200k OBS API requests since midnight, which SUSE IT flagged.

## Goal

Cut background OBS API traffic by roughly two orders of magnitude during build
storms, and guarantee a hard hourly ceiling (~86k requests/day budget, enforced
hourly) that no storm or bug can exceed — without degrading the dashboard's
real-time feel beyond an agreed ~5 minute worst-case staleness for
intermediate build states.

## Non-goals

- No changes to the poller (5m project-level poll stays the loss-tolerant
  fallback) or the MQ consumer (stays the real-time wake source).
- No changes to parking (`Parkable`/`Settled`), DB schema, or frontend.
- Breaking the pr-127 rebuild loop is an OBS project-config fix, tracked
  separately — not part of this design.

## Approach

Scheduler-centric: the working-set scheduler becomes the single place that
decides *when* each package is processed and *how* its build results are
fetched. Four coordinated changes:

## 1. Adaptive backoff (workingset)

`WorkingSet` entries become `{pkg, interval, nextDue}`. The scheduler still
ticks every `worker_pool.poll_interval` (30s) but dispatches only packages
whose `nextDue` has passed.

- A pass that observes a state change resets the package's interval to the
  base (30s). An unchanged pass doubles it: 30s → 1m → 2m → 4m → capped at
  `worker_pool.backoff_max` (default 5m).
- `Done(key)` becomes `Done(key, changed bool)`. The worker already computes
  changed-ness for event emission; it passes that in.
- Wake signals reset backoff: `Signal()` (MQ events) and `Add()` (poller
  re-adds) set `nextDue = now` and reset the interval to base, preserving
  real-time reaction to MQ traffic.
- Backoff state is in-memory only. A restart resets all packages to the base
  interval and the ladder re-converges within minutes (same loss-tolerance
  philosophy as parking).
- Blocked packages need no new parking rules: an unchanging blocked package
  converges to one pass per 5m, and `BlockedReasonTask`'s existing 5-minute
  TTL keeps the blocker list refreshing at that cadence. (Parking blocked
  packages instead would freeze `BlockedBy` indefinitely, because the poller
  only re-adds packages on state *changes*.)

Effect: last night's ~200 long-blocked packages drop from 120 passes/hour each
to 12/hour → ~10x fewer passes before batching is considered.

## 2. Threshold-based project-level batching (workingset + worker)

At each tick the scheduler groups due packages by project:

- **Group size ≥ `worker_pool.batch_threshold` (default 4):** enqueue one
  batch job `Job{Project, Pkgs}`. The worker that picks it up makes a single
  project-level `_result` call (`client.BuildResults` — the same call the
  poller uses, returning all packages' target states *and* per-repo publish
  states) and runs the task chain for each package with that data injected.
- **Group size < threshold:** dispatch single-package jobs exactly as today
  (per-package `PackageBuildResults` is cheaper below the threshold).

Plumbing:

- The dispatch channel carries `Job{Pkgs []*model.Package, Prefetched
  *ProjectResults}` instead of a bare `*model.Package`. All packages in a job
  are marked inflight at enqueue time; `Done` is called per package.
- The `Task` interface becomes `Run(ctx, client, pkg, env *Env)`. `Env`
  optionally carries the pre-fetched per-package build states and the
  project's repo publish states. Tasks that don't care ignore it. `Env` lives
  in the `obs` package (like the task implementations): `worker` already
  imports `obs`, so placing it in `worker` would create an import cycle.
- `BuildStateTask` uses `env`'s pre-fetched states when present instead of
  calling `PackageBuildResults`. `PublishStateTask` uses `env`'s repo publish
  states instead of calling `RepoPublishStates` — repo publish state is a
  property of the repo, not the package, so one fetch serves the whole batch.
- **Fallback:** if the project-level fetch fails, the worker processes every
  package in the job with `env = nil` (per-package fetches). Batching is an
  optimization, never a correctness dependency.
- A batch is processed serially by one worker. Pre-fetched results are at most
  ~a minute stale for the last package of a large batch — within the 5m
  freshness budget — and serial processing acts as natural throttling during
  storms.

Effect: during a storm, the up-to-two per-pass calls per package (`_result`,
plus `_result?view=status` when the package has succeeded-but-unpublished
targets) collapse into one call per project per tick.

## 3. Hourly rate limiter (obs.Client)

A fixed-window hourly counter in `Client`, enforced in the existing `get` /
`getFile` / `post` choke points (every OBS call funnels through these):

- Budget: `obs.hourly_request_budget` (default 3600 ≈ 86k/day). Within a
  window requests flow at full speed; once exhausted, background requests
  block until the next hour window opens. Blocking is context-aware (respects
  cancellation); requests are delayed, never dropped.
- **Interactive bypass:** API handlers serving real users (rebuild, build log,
  binaries, artifact metadata, release artifacts) tag their context via
  `obs.Interactive(ctx)`; tagged requests skip the limiter. Their volume is
  negligible and users must never wait behind background churn.
- **Observability:** the limiter exports `limited_waits` and
  `remaining_budget` through the existing `MetricsSnapshot()` so telemetry
  shows when the cap is being approached.

With backoff + batching, normal load is expected around 200–500 req/hour; the
limiter is the contractual guarantee to SUSE that no future storm exceeds the
budget.

## 4. Configuration

New keys, all defaulted so existing prod config keeps working:

| Key | Default | Meaning |
|---|---|---|
| `worker_pool.backoff_max` | `5m` | Cap of the per-package backoff ladder |
| `worker_pool.batch_threshold` | `4` | Min due packages per project to use one project-level `_result` call |
| `obs.hourly_request_budget` | `3600` | Hourly OBS request cap for background traffic |

## Error handling

- Batch fetch failure → per-package fallback (`env = nil`).
- The worker always calls `Done` (even when tasks error) so no package is
  stuck inflight.
- Limiter blocking respects context cancellation; shutdown stays clean.
- Backoff/limiter state is in-memory; restarts are safe and self-correcting.

## Testing

- **Backoff ladder:** reset on change, doubling, cap at `backoff_max`,
  `Signal`/`Add` reset, restart behavior.
- **Grouping/threshold:** groups ≥ threshold become batch jobs, smaller groups
  stay single-package; fetch-failure fallback path.
- **Limiter:** exhaustion blocks background requests, interactive bypass
  works, window rollover releases waiters, context cancellation unblocks.
- **Env injection:** `BuildStateTask`/`PublishStateTask` consume pre-fetched
  data when present and fetch when absent; other tasks unaffected.
- The `Task` interface change updates all existing task tests mechanically.
- **Prod verification:** compare per-op `MetricsSnapshot()` counts before and
  after over a full day; expect build-state/publish-state ops to drop by
  ~10–50x during active periods.

## Expected impact (replaying the 2026-07-10 storm)

| Source | Before | After |
|---|---|---|
| Scheduled passes | ~89k | ~9k (backoff) |
| Build-state + publish-state calls | ~110k+ | ~2–4k (batching folds waves into per-project calls) |
| Hard ceiling | none | 3,600/hour |
