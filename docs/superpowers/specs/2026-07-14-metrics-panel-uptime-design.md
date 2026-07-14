# Metrics panel: uptime display + window-row formatting fix

**Date:** 2026-07-14
**Status:** Approved

## Problem

1. The backend-metrics panel shows counters qualified as "since start" and
   trailing 6h/12h/24h windows that reset on restart, but nothing tells the
   operator how long the process has been up.
2. The `last 6h/12h/24h` rows (and the requests-by-endpoint rows) use
   `flex justify-between` across their full grid-column width — on a wide
   Overview panel (~1360px) that puts a ~300px empty gutter between label
   and value. The approved mockup had ~220px-wide groups; the real panel
   does not.

## Decision summary

- Uptime shown in the **panel header**, right side: `up 3d 4h · updated
  12s ago` (chosen over placing it inside the OBS-requests subline).
  Frozen together with the age label while collapsed.
- API gains **top-level `uptime_seconds`** (process-wide — it qualifies
  working-set and limiter counters too, not just OBS counts).
- Start time captured by a **package-level `var processStart = time.Now()`**
  in `internal/api/metrics.go` (init-time capture, within milliseconds of
  process start). Rejected: threading a start time through `NewRouter` —
  the signature is already at seven parameters (flagged in an earlier
  review) and per-millisecond accuracy is irrelevant for a human-readable
  uptime.
- Formatting fix: cap the window-rows block at `max-w-[200px]` and the
  endpoint table at `max-w-[560px]`, restoring the mockup's tight
  label→value pairing. User approved fixing the endpoint table alongside
  the explicitly-reported window rows (same defect, same file).

## Backend design

`internal/api/metrics.go`:

```go
// processStart approximates process start (package init happens within
// milliseconds of it); /api/metrics reports uptime relative to this.
var processStart = time.Now()
```

`metricsResponse` gains:

```go
UptimeSeconds int64 `json:"uptime_seconds"`
```

populated in the handler with `int64(time.Since(processStart).Seconds())`.

Test: `TestMetricsHandler` additionally unmarshals the response body into
`map[string]any` and asserts the `uptime_seconds` key exists and is a
number ≥ 0.

## Frontend design

`frontend/src/types/metrics.ts`: `MetricsSnapshot` gains
`uptime_seconds: number` (top-level).

`frontend/src/components/MetricsPanel.vue`:

- New helper in the script block:

```ts
function fmtUptime(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds))
  const d = Math.floor(s / 86_400)
  const h = Math.floor((s % 86_400) / 3_600)
  const m = Math.floor((s % 3_600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${s}s`
}
```

- Header right side becomes one muted span combining uptime and age, both
  derived from the last fetched data (so both freeze while collapsed):
  `up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}` — the
  uptime part renders only when `data` exists; the age label behavior is
  unchanged.
- Window rows: the container `<div class="mt-2">` becomes
  `<div class="mt-2 max-w-[200px]">`. Nothing else about the rows changes.
- Endpoint table: the container carrying `style="columns: 2; …"` gains
  `max-w-[560px]` (each CSS column ≈264px).

## Error handling

None new — `uptime_seconds` is always present and non-negative; `fmtUptime`
clamps negatives to 0 defensively.

## Testing

- Backend: extended `TestMetricsHandler` (raw-map key presence + ≥ 0);
  `go test ./internal/api/ -count=1`.
- Frontend: `npm run build`; visual check — header shows `up Ns · updated
  0s ago` on a fresh dev backend, window rows and endpoint columns read as
  tight label→value pairs at full Overview width, both themes.

## Alternatives considered

- Uptime inside the OBS-requests subline — rejected by user choice
  (header).
- `started_at` timestamp instead of `uptime_seconds` — rejected: the
  client would need clock-skew-safe math; a duration is directly
  renderable.
- Fixing only the window rows and leaving the endpoint table — rejected:
  same defect, same file, one extra class.
