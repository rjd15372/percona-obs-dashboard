# `GET /api/metrics` — JSON metrics endpoint + telemetry log-line fix

**Date:** 2026-07-10
**Status:** Approved

## Problem

The OBS client's metrics (`MetricsSnapshot()`) are observable only through the
periodic telemetry log line, which reports them as per-window deltas. The
limiter gauges (`limiter_remaining`, `limiter_waits`) ride along inside the
`obs_by_endpoint` delta map, which is misleading for gauges and skews the
`obs_window`/`obs_total` sums. There is no way to read the absolute values on
demand, and no way to see the current OBS request rate outside the log.

## Decision summary

- New `GET /api/metrics` endpoint returning the full telemetry picture as
  JSON: OBS per-op counts, a trailing-minute request rate, limiter gauges,
  and working-set stats.
- Fix the telemetry log line at the same time: limiter gauges move out of
  `MetricsSnapshot()` into a dedicated accessor and are logged as top-level
  fields; `obs_window`/`obs_total`/`obs_by_endpoint` become pure request
  counts.
- Approach: split accessors + structured JSON (rejected: flat map dump;
  shared telemetry snapshot type — see Alternatives).

## API surface

`GET /api/metrics`, registered next to the `/api/telemetry` routes in
`internal/api/server.go`. Always returns 200 with
`Content-Type: application/json`:

```json
{
  "obs": {
    "total": 15234,
    "by_endpoint": { "build_result": 9120, "published": 3021 },
    "req_per_s": 2.4
  },
  "limiter": { "enabled": true, "budget": 60, "remaining": 41, "waits": 17 },
  "working_set": {
    "packages": 214,
    "inflight": 3,
    "by_state": { "succeeded": 180, "building": 20 }
  }
}
```

Field semantics:

- `obs.total` — cumulative request count since process start (sum of
  `by_endpoint` values).
- `obs.by_endpoint` — cumulative per-operation counts, keyed by the `op`
  names already used by `metrics.inc(op)`.
- `obs.req_per_s` — requests observed in the trailing 60 seconds ÷ 60.
  Counted at the same point as `by_endpoint`, so it includes both background
  and interactive requests (unlike the limiter's window counter, which only
  sees background traffic). Differs deliberately from the telemetry log's
  `obs_req_per_s`, which averages over the reporter interval; the endpoint's
  figure is always the trailing minute, fresh at request time.
- `limiter` — absolute gauges: `enabled` (budget > 0), configured `budget`,
  `remaining` slots in the current minute window, cumulative `waits`
  (acquisitions that had to block). When disabled:
  `{"enabled": false, "budget": 0, "remaining": 0, "waits": 0}`.
- `working_set` — `packages` (= `workingset.Stats.Total`), `inflight`,
  `by_state` (rollup_state → count).

Like the rest of the API: no auth. The existing Interactive middleware
applies but is irrelevant — the handler makes no OBS calls; everything is
in-memory reads.

## Backend changes

### `internal/obs/client.go` — split the accessors, add the rate ring

- New exported type:

  ```go
  type LimiterStats struct {
      Enabled   bool
      Budget    int
      Remaining int64
      Waits     int64
  }
  ```

  and method `Client.LimiterStats() LimiterStats` wrapping
  `limiter.stats()` (plus the budget). The internal `minuteLimiter.stats()`
  stays as is.
- `MetricsSnapshot()` drops the two `limiter_*` keys and returns pure per-op
  counts.
- `obsMetrics` gains a fixed ring of 60 per-second buckets for the
  trailing-minute rate: each `inc(op)` also increments
  `ring[now.Unix()%60]`, first zeroing the bucket if its stored unix second
  doesn't match the current one (each slot remembers which second it holds).
  A new method `ratePerSecond(now)` sums the still-fresh buckets and divides
  by 60, exposed as `Client.RatePerSecond() float64`. No goroutines, no
  timers — everything under the existing mutex on paths that already take
  it. Injectable `now func() time.Time` for tests (same pattern as
  `minuteLimiter`).

### `internal/telemetry/telemetry.go` — log-line fix

- `Reporter` gains a `Limiter interface{ LimiterStats() obs.LimiterStats }`
  field (telemetry already imports workingset; importing obs introduces no
  cycle).
- The log line gains top-level `limiter_remaining` and `limiter_waits`
  fields, emitted only when the limiter is enabled. `obs_window`,
  `obs_total`, `obs_by_endpoint`, and `obs_req_per_s` become pure
  request-count figures automatically since the gauges left the snapshot
  map.

### `internal/api` — handler and wiring

- New file `internal/api/metrics.go` with
  `metricsHandler(obsClient *obs.Client, ws Statter) http.HandlerFunc`.
- `api` defines its own `type Statter interface{ Stats() workingset.Stats }`
  (same shape as telemetry's) to avoid depending on the telemetry package.
- `NewRouter` gains a `ws Statter` parameter; `cmd/obsboard/main.go` passes
  the working set it already hands to the telemetry Reporter.
- Route: `r.Get("/api/metrics", metricsHandler(obsClient, ws))`.

## Error handling

The only failure mode is JSON encoding, which cannot realistically fail for
this shape — the handler writes 200 unconditionally. No OBS traffic, no DB
access, no locks held across I/O (snapshot first, encode after).

## Testing

Backend has a Go test suite (`go test ./...`); new behavior gets tests:

- **`internal/obs`**: `MetricsSnapshot()` no longer contains `limiter_*`
  keys; `LimiterStats()` returns correct budget/remaining/waits including
  the disabled case; ring tests with a fake clock — rate reflects only the
  last 60 seconds (increments 61+ seconds old fall out), zero traffic →
  0.0, burst then silence decays as the window slides.
- **`internal/api`**: `httptest` request to `/api/metrics` asserting the
  full JSON shape with a stub `Statter` and a real `obs.Client` (no network
  — counters incremented via exported behavior or test hooks as available).
- **`internal/telemetry`**: update any existing tests that assume limiter
  gauges inside the snapshot map; assert the new top-level limiter fields
  are logged when enabled.

Verify: `cd backend && go test ./...` all green; manual spot check
`curl :4000/api/metrics | jq` once running.

## Alternatives considered

- **Flat map dump** (return `MetricsSnapshot()` as-is plus ws fields):
  minimal code but keeps gauges and counters in one bag; the log-line fix
  would still need the accessor split, so it saves little. Rejected.
- **Shared telemetry snapshot type** (one struct consumed by log line and
  endpoint): the log reports per-window deltas while the endpoint reports
  absolutes — a shared type serves neither well and couples `api` to
  telemetry internals. Rejected.
- **Deriving req/s from the limiter window**: undercounts (interactive
  requests bypass the limiter; disabled limiter counts nothing). Rejected
  in favor of the per-second ring at the `metrics.inc` counting point.
