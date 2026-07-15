# Metrics panel: OBS-requests redesign with trend variation and sparkline

**Date:** 2026-07-15
**Status:** Approved

## Problem

The OBS-requests card in the backend-metrics panel is a plain number
list. It hides whether traffic is rising or falling, and it squeezes
five window counts into a narrow 200px column while the panel is wide.
The operator wants a different layout and a percentage of variation on
the window breakdown.

## Decision summary

- **Layout: mockup C** — the OBS card slims to total + req/s + a 24h
  sparkline; the five windows move to a full-width tile strip between
  the three cards and the endpoint table. (Rejected: in-card list with
  bars — doesn't use the panel's width; tile strip without sparkline —
  chosen C over A explicitly.)
- **Variation: previous adjacent period** — last 6h vs the 6h before
  it, same for 12h/24h/7d. (Rejected: day-over-day baseline; share of
  total.)
- **The 30d tile never shows a variation figure** (its baseline would
  need 60d of samples; retention stays 30d). Muted "—" instead.
- **Data delivery: extend `/api/metrics`** (user choice "1") — three
  new fields under `obs`, riding the existing 30s poll. Percentage math
  happens client-side on raw sums. (Rejected: separate series endpoint;
  server-computed percentages.)

## Design

### 1. Store — `internal/store/metrics.go` (three new functions)

```go
// QueryMetricsPrevWindows returns summed request counts over the
// previous adjacent periods of the trailing windows: (now-12h, now-6h],
// (now-24h, now-12h], (now-48h, now-24h] and (now-14d, now-7d],
// keyed "6h"/"12h"/"24h"/"7d". No "30d" key: its baseline would need
// 60d of samples, beyond the 30d retention.
func QueryMetricsPrevWindows(db *sql.DB, now time.Time) (map[string]int64, error)

// QueryMetricsSeries returns total requests per 5-minute bucket over
// the trailing 24h: a fixed [288]-length slice, oldest bucket first,
// missing buckets zero.
func QueryMetricsSeries(db *sql.DB, now time.Time) ([]int64, error)

// OldestMetricsSample returns the earliest sample timestamp, or the
// zero time when the table is empty.
func OldestMetricsSample(db *sql.DB) (time.Time, error)
```

- `QueryMetricsPrevWindows` is one statement in the existing style:
  four `COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0)`
  columns, scan bounded by `WHERE ts > ?` with the 14d cutoff (the
  widest baseline band). All cutoffs bound as RFC3339Nano UTC strings
  (established convention; raw `time.Time` binds are a known driver
  trap).
- `QueryMetricsSeries` selects `ts, count` rows `WHERE ts > ?` (24h
  cutoff) and buckets in Go: index `287 - int(now.Sub(t) / 5min)`,
  clamped rows on the boundary dropped; ts parsed with
  `time.Parse(time.RFC3339Nano, …)`.
- `OldestMetricsSample` is `SELECT MIN(ts)`; NULL → zero time, nil
  error.

### 2. API — `/api/metrics` `obs` section (three new fields)

```go
type obsSection struct {
    // … existing fields unchanged …
    WindowsPrev  map[string]int64 `json:"windows_prev"`  // 6h/12h/24h/7d baselines
    Series       []int64          `json:"series"`        // 288 5-min buckets, oldest first
    OldestSample string           `json:"oldest_sample"` // RFC3339, "" when no samples
}
```

- Populated per request from the three store functions with
  `time.Now().UTC()`.
- Error convention matches the windows query: on any store error,
  `slog.Warn` and serve zero values (four-key zero map / 288 zeros /
  `""`) — never a 500.
- `oldest_sample` is formatted `time.RFC3339`; the zero time serializes
  as `""`.
- Handler signature, sampler, pruner and retention are untouched.

### 3. Frontend — `MetricsPanel.vue` restructure (mockup C)

- **OBS card** (first grid column): total, `req/s last minute` line,
  then an inline-SVG sparkline — 288-point polyline, width-normalized,
  y normalized to the series max (all-zero series renders a flat
  baseline), brand-purple stroke, caption
  `requests per 5 min · last 24h`. Text stays in text tokens; only the
  line carries color.
- **Tile strip**: full-width flex row of five tiles between the
  3-column grid and the endpoint table. Each tile: uppercase label
  (`last 6h` … `last 30d`), the window count, and a delta line.
- **Delta rules** (client-side, per tile):
  - `pct = (cur − prev) / prev × 100`, rounded to a whole percent.
  - Render `▲ N%` (green) when positive, `▼ N%` (red) when negative,
    neutral `0%` when zero.
  - Muted `—` when: the window is `30d` (always); `prev == 0`; or
    `oldest_sample` is empty or newer than `now − 2×window` (baseline
    incomplete — warm-up).
- Rate-limiter and working-set cards, the endpoint table, header line,
  polling cadence (30s while expanded) all unchanged.

### 4. Types — `frontend/src/types/metrics.ts`

`obs` gains `windows_prev: Record<string, number>`, `series: number[]`,
`oldest_sample: string`.

## Error handling

- Store query failure → warn + zero values, HTTP 200 (existing
  convention, extended to the three new fields).
- Frontend guards: `?? 0` / `?? []` fallbacks on the new fields so an
  older backend payload renders as warm-up ("—", flat sparkline) rather
  than breaking.

## Testing

- **store**: prev-window boundary pinning — rows just inside/outside
  each baseline band land in the right sums, e.g. ages 7h/11h for the
  6h baseline (in) vs 5h59m/12h01m (out); series bucketing — known
  offsets (2min, 7min, 23h58m) land in slots 287/286/0, row at 24h01m
  dropped, empty table → 288 zeros; oldest-sample: empty table → zero
  time, seeded table → earliest ts. All seeds pre-formatted
  RFC3339Nano strings.
- **api**: `TestMetricsHandler` extended — seeded rows produce expected
  `windows_prev` sums, a `series` of length 288 with the seeded buckets
  non-zero, and a non-empty `oldest_sample`; all three keys present in
  the raw JSON.
- **frontend**: `npm run build`; visual — five tiles with arrow/—
  states, sparkline shape, warm-up rendering against an empty dev DB.

## Alternatives considered

- Separate `/api/metrics/series` endpoint — two polls, two error
  paths, single consumer; rejected.
- Server-computed percentages — bakes rounding and warm-up display
  rules into the API; rejected.
- Day-over-day or share-of-total variation semantics — rejected in
  favor of previous adjacent period.
- Raising retention to 60d to give the 30d row a baseline — rejected;
  the 30d tile simply shows no variation.
