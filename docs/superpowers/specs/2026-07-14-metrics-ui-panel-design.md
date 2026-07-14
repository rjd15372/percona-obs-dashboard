# Backend-metrics panel in the Overview UI (+ windowed request counts)

**Date:** 2026-07-14
**Status:** Approved

## Problem

The `/api/metrics` endpoint (OBS request counters, trailing-minute rate,
limiter gauges, working-set stats) is only reachable with curl. The operator
wants it visible in the dashboard: a collapsed panel at the bottom of the
Overview tab, refreshed every 30 seconds, including a breakdown of request
counts over the last 6h / 12h / 24h — which requires new backend history.

## Decision summary

- Panel sits at the bottom of the Overview tab, **collapsed by default**.
- **Polls only while expanded**: fetch immediately on expand, then every
  30s; stop on collapse/unmount. (Rejected: polling while collapsed / a
  live summary in the collapsed header — contradicts poll-on-expand.)
- Expanded layout = **mockup option A**: three labeled groups (OBS requests,
  Rate limiter, Working set) plus a per-endpoint table. (Rejected: dense
  key-value-only layout.)
- New windowed counts: **in-memory 24h ring of 5-minute buckets** in the
  backend; `obs.windows` gains `6h`/`12h`/`24h` sums. Accepted caveat:
  after a restart the windows cover only process uptime (same as every
  other gauge from this endpoint). (Rejected: SQLite-backed history —
  restart-spanning accuracy not worth the write/prune machinery.)

## Backend design

### 1. `internal/obs` — 24h window ring

`obsMetrics` gains a second ring beside the existing 60×1s one:

- `winSec [288]int64` — the unix 5-minute-period id each bucket holds
  (`sec / 300`), `winHits [288]int64` — requests counted in that period.
  Index = `periodID % 288`. `inc(op)` (already under the mutex) stamps and
  zeroes a stale bucket on reuse, then increments — identical pattern to
  the seconds ring. ~5KB, no goroutines.
- New method `windowCounts() (h6, h12, h24 int64)`: with
  `cur = now.Unix()/300`, a bucket with period id `p` contributes to a
  window of `n` buckets when `p > cur-n` (trailing window at ±5min
  precision; 6h=72, 12h=144, 24h=288 buckets).
- Exported `Client.WindowCounts() (h6, h12, h24 int64)` delegating to it.

### 2. `internal/api` — response extension

`obsSection` gains:

```go
Windows map[string]int64 `json:"windows"` // keys "6h", "12h", "24h"
```

populated from `Client.WindowCounts()`. Resulting shape:

```json
"obs": {
  "total": 15234,
  "by_endpoint": { "build_results": 9120 },
  "req_per_s": 0.8,
  "windows": { "6h": 1890, "12h": 4102, "24h": 9317 }
}
```

## Frontend design

### 3. Types — `frontend/src/types/metrics.ts`

Mirrors the API exactly:

```ts
export interface MetricsSnapshot {
  obs: {
    total: number
    by_endpoint: Record<string, number>
    req_per_s: number
    windows: Record<string, number> // "6h" | "12h" | "24h"
  }
  limiter: { enabled: boolean; budget: number; remaining: number; waits: number }
  working_set: { packages: number; inflight: number; by_state: Record<string, number> }
}
```

### 4. Composable — `frontend/src/composables/useMetrics.ts`

`useMetrics(active: Ref<boolean>)`:

- While `active`: immediate `fetch('/api/metrics')`, then a 30s
  `setInterval`. Interval cleared when `active` flips false or on unmount.
- Exposes `data: Ref<MetricsSnapshot | null>`, `error: Ref<string | null>`,
  `fetchedAt: Ref<Date | null>`.
- A fetch failure sets `error` and leaves the previous `data` (stale data
  stays visible with its age); the next tick retries and clears `error` on
  success.

### 5. Component — `frontend/src/components/MetricsPanel.vue`

Card in the app's panel style, self-contained (owns its expanded state,
not persisted). Appended below `CveExposureTable` in `OverviewPanel.vue`
(one import + one tag; no other OverviewPanel changes).

**Collapsed (default):** header row only — chevron (▸/▾ rotation), label
"BACKEND METRICS" (muted uppercase, 11.5px, matching card labels), and,
once data exists, "updated Ns ago" right-aligned in muted 11px. The age
ticks via a 1s interval that runs only while expanded; when collapsed the
last text stays frozen. Clicking the header toggles.

**Expanded body** (per mockup A) — three groups in a responsive grid
(3 columns, stacking to 1 on narrow screens):

- **OBS requests**: 22px bold `obs.total` (locale-formatted); subline
  "total since start · `req_per_s.toFixed(1)` req/s last minute"; then
  three compact label→value rows `last 6h` / `last 12h` / `last 24h` with
  mono tabular values from `obs.windows`.
- **Rate limiter**: when `enabled` — 22px bold `remaining` with ` / budget`
  suffix, subline "remaining this minute · N waits total", and a 6px meter
  (brand-accent `var(--brand-purple)` fill on `var(--bg-muted)` track,
  width `remaining/budget`). When disabled — the group shows just
  "disabled" in muted text.
- **Working set**: 22px bold `packages`; subline "packages · N in flight";
  neutral chips (bg-muted, secondary text) per `by_state` entry as
  "`count` `state`", sorted by count descending.

Below the grid: **Requests by endpoint** — group title + a two-CSS-column
(`columns: 2`) mono table of `by_endpoint` sorted by count descending;
"no requests yet" in muted text when empty.

Error state: a muted single-line "failed to refresh: <msg>" under the
header; existing data stays rendered.

All colors/typography via existing theme tokens — dark mode needs no extra
work. No new chart machinery beyond the one meter div.

## Error handling

- Frontend: covered above (retry on next tick, stale-with-age display).
- Backend: none new — the ring is pure in-memory arithmetic under the
  existing mutex; `windowCounts` cannot fail.

## Testing

- **`internal/obs`** (fake clock): window sums correct for hits spread
  across buckets; trailing exclusion (a hit 24h+5min old contributes to no
  window; one 11h old contributes to 12h and 24h but not 6h); bucket reuse
  after a full 24h wrap zeroes stale hits; zero traffic → all zeros.
- **`internal/api`**: `TestMetricsHandler` extended — `windows` object
  present with the three keys (zero values in the no-traffic case).
- **Frontend**: no test framework — `npm run build` (vue-tsc) plus visual
  check in both themes against a running backend: collapsed default,
  expand fetches immediately, values match `curl /api/metrics`, 30s
  refresh observable via the age label, no polling while collapsed
  (network tab), limiter-disabled and empty-endpoint states render.

## Alternatives considered

- **Poll always while on Overview** — rejected; no background chatter for
  an unopened panel.
- **Live summary in the collapsed header** (mockup C) — rejected;
  contradicts poll-on-expand.
- **Dense key-value layout** (mockup B) — rejected by user choice of A.
- **SQLite-persisted request history** — rejected; restart-spanning window
  accuracy isn't worth DB writes and pruning for an ops panel.
- **Hour-granularity ring** — rejected in favor of 5-minute buckets: same
  trivial memory, much smoother trailing windows.
