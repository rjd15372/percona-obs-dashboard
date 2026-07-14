# Persistent metrics: 30-day request-count history in SQLite

**Date:** 2026-07-14
**Status:** Approved

## Problem

All `/api/metrics` data is in-memory and resets on restart. The trailing
6h/12h/24h windows in particular carry a standing "covers at most process
uptime" caveat. The operator wants metrics persisted in the database,
surviving restarts, with 30 days of retention.

## Decision summary

- **Persisted series: OBS request counts per endpoint** as 5-minute delta
  samples. Live gauges (limiter, sse_clients, working set, req/s, uptime)
  stay in-memory — instantaneous by nature. (Rejected: persisting every
  /api/metrics field.)
- **Windows become DB-backed** SUMs over the samples and the panel gains
  `7d` and `30d` rows. The in-memory 24h window ring is **removed** — one
  source of truth. (Rejected: keeping the ring with the DB as archive
  only.)
- **Mechanism: periodic delta sampler** (telemetry-Reporter pattern).
  (Rejected: serializing/restoring the ring — no 30-day history; per-request
  write-through — DB write on the OBS hot path.)
- Retention: config `store.metrics_retention`, default `"30d"`, env
  `METRICS_RETENTION`, `parseRetention` semantics — same convention as
  `EVENT_RETENTION`. Pruned by the existing `runPruner` loop.
- Accepted losses/asymmetries: the final unflushed partial bucket (≤ 5 min)
  is lost on restart; `obs.total`/`by_endpoint` remain process-since-start
  (panel already labels them so); window precision stays ±one bucket, now
  5 min end-to-end.

## Design

### 1. Schema — `internal/store/db.go`

```sql
CREATE TABLE IF NOT EXISTS metrics_samples (
    ts    DATETIME NOT NULL,
    op    TEXT     NOT NULL,
    count INTEGER  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metrics_samples_ts ON metrics_samples (ts);
```

`ts` is the sample time, stored as a pre-formatted RFC3339Nano UTC string
(the established convention; raw `time.Time` binds are a known driver
trap). Volume: ~12 ops × 288 samples/day × 30d ≈ 100k rows.

### 2. Store functions — `internal/store/metrics.go` (new file)

```go
// InsertMetricsSamples writes one row per op with the given counts at ts.
// Zero-count ops must be filtered by the caller.
func InsertMetricsSamples(db *sql.DB, ts time.Time, deltas map[string]int64) error

// QueryMetricsWindows returns summed request counts over the trailing
// 6h, 12h, 24h, 7d and 30d, keyed "6h"/"12h"/"24h"/"7d"/"30d".
func QueryMetricsWindows(db *sql.DB, now time.Time) (map[string]int64, error)

// PruneMetricsSamples deletes samples older than cutoff.
func PruneMetricsSamples(db *sql.DB, cutoff time.Time) (int64, error)
```

`QueryMetricsWindows` runs one statement:

```sql
SELECT
  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),  -- 6h cutoff
  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),  -- 12h cutoff
  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),  -- 24h cutoff
  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),  -- 7d cutoff
  COALESCE(SUM(count), 0)                             -- 30d = all scanned rows
FROM metrics_samples
WHERE ts > ?                                          -- 30d cutoff bounds the scan
```

All five cutoffs bound as RFC3339Nano UTC strings. ~100k-row scan per call
is milliseconds in SQLite; called at most every 30s by one open panel.

### 3. Sampler — new `internal/metricsampler`

```go
// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

type Sampler struct {
    DB   *sql.DB
    Snap Snapshotter
    // internal: prev map[string]int64 baseline; now func() time.Time
}

func (s *Sampler) Run(ctx context.Context) // 5-minute ticker until cancel
```

- `sampleInterval = 5 * time.Minute` (fixed constant).
- Baseline `prev` initialized from `Snap.MetricsSnapshot()` when `Run`
  starts (boot ≈ zeros). Each tick: `cur := snapshot; deltas := cur − prev`
  per op, drop zero/negative entries; if any remain, insert with
  `ts = now`; **on insert success or failure alike, `prev = cur`** — a
  failed insert loses that bucket rather than double-counting it next tick
  (warn-logged).
- Runs regardless of the presence gate: local writes only; idle periods
  produce empty deltas and no rows.
- Wired in `main.go` beside the other background goroutines.

### 4. Retention

`StoreConfig` gains `MetricsRetention time.Duration`; viper default
`store.metrics_retention = "30d"`, env `METRICS_RETENTION`, parsed with
`parseRetention`. `runPruner` calls `PruneMetricsSamples(db,
now−retention)` alongside `PruneEvents` on its existing ticker. Both
example config files document the key next to `event_retention`.

### 5. API — windows from the DB

- `metricsHandler` gains a `db *sql.DB` parameter (`NewRouter` already has
  it; route registration passes it — NewRouter signature unchanged).
- `Windows` is populated from `store.QueryMetricsWindows(db, time.Now())`
  and now carries five keys: `6h`, `12h`, `24h`, `7d`, `30d`. On query
  error: `slog.Warn` and serve all-zero windows (the rest of the payload
  is still useful) — never a 500.
- **Removed from `internal/obs`**: the `winPeriod`/`winHits` arrays, the
  5-minute bucket code in `inc`, `windowCounts`, `Client.WindowCounts`,
  and their tests (`TestWindowCounts`, `TestWindowCountsBucketReuse`). The
  60-second rate ring and `RatePerSecond` stay. Comments referencing
  "covers at most process uptime" for windows are corrected wherever they
  survive.

### 6. Panel

`WINDOW_KEYS` becomes `['6h', '12h', '24h', '7d', '30d'] as const` — two
more label→value rows in the OBS-requests group. No other UI change; the
existing `?? 0` fallback covers a not-yet-populated backend.

## Error handling

- Sampler: insert failure → warn + baseline advance (bucket lost, no
  double count). Context cancel exits cleanly.
- Handler: window query failure → warn + zero windows, 200 response.
- Prune failure → warn (same as events prune), retried next pruner tick.

## Testing

- **store** (`metrics_test.go` additions or new file, RFC3339Nano-seeded):
  insert→query round trip; window boundary pinning (rows at 5h59m/6h01m,
  23h59m/24h01m, 29d/31d ages land in the right sums); prune deletes only
  older-than-cutoff; zero rows → all-zero map with all five keys.
- **metricsampler** (fake clock + fake Snapshotter + `:memory:` store):
  first tick after boot writes counts-since-boot; steady ticks write
  deltas only; zero-delta ticks write no rows; failed insert (closed DB)
  advances the baseline (next tick doesn't double-count); cancel stops.
- **api**: `TestMetricsHandler` updated — handler takes the test's
  `:memory:` db; seeded sample rows appear in the right window keys; all
  five keys present (zeros in the empty case).
- **frontend**: `npm run build`; visual — five window rows.
- `go test ./... -count=1` green.

## Alternatives considered

- Serialize/restore the in-memory ring — survives restarts but provides no
  30-day history; rejected.
- Per-request write-through — DB write on the OBS hot path; rejected.
- Keeping the ring alongside the DB — two sources of truth for the same
  numbers; rejected.
- Fixed 30d constant instead of config — rejected for consistency with
  `EVENT_RETENTION`, which is configurable.
