# Metrics panel: connected SSE client count

**Date:** 2026-07-14
**Status:** Approved

## Problem

With idle mode now pausing polling based on connected dashboard clients,
the operator has no way to see the very number that drives it. The
backend-metrics panel should show how many SSE connections are open.

## Decision summary

- **Source: the hub** (`len(h.clients)` via a new `Clients()` accessor) —
  NOT the presence gate, whose counter is deliberately not maintained when
  `idle.enabled: false` and would read 0 on an opted-out deployment.
- **Label: "N connections"** (user choice over "clients") — the count is
  SSE connections, not people: an Overview tab holds 2 streams (app +
  overview), other tabs 1. The count includes the viewer themselves, so it
  never reads 0 while being looked at — a built-in liveness check for idle
  mode.
- JSON field: top-level `sse_clients` (int). Panel header shows the count
  first: `3 connections · up 3d 4h · updated 12s ago`, with
  singular/plural (`1 connection`).

## Design

### 1. Hub accessor — `internal/hub/hub.go`

```go
// Clients returns the number of currently registered SSE clients.
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
```

### 2. API — `internal/api/metrics.go`

- Local consumer interface (Statter pattern):

```go
// ClientCounter reports the number of connected SSE clients.
type ClientCounter interface{ Clients() int }
```

- `metricsHandler(obsClient *obs.Client, ws Statter, clients ClientCounter)`;
  the route registration in `server.go` passes the `h *hub.Hub` that
  `NewRouter` already receives — NewRouter's signature is unchanged.
- `metricsResponse` gains `SSEClients int \`json:"sse_clients"\`` (top
  level, after `UptimeSeconds`), populated with `clients.Clients()` per
  request.

### 3. Frontend

- `frontend/src/types/metrics.ts`: `sse_clients: number` (top level).
- `frontend/src/components/MetricsPanel.vue`: the header's right-side span
  becomes

  ```html
  <span v-if="data" class="ml-auto text-[11px] text-text-muted">{{ data.sse_clients }} connection{{ data.sse_clients === 1 ? '' : 's' }} · up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
  ```

  — count first (most dynamic), then uptime, then refresh age; all frozen
  together while collapsed, as today.

## Error handling

None new: the accessor is a locked map length; the field is always
present and ≥ 1 whenever the panel itself is polling (its own stream
counts).

## Testing

- `internal/hub`: `TestClients` — register 2 channels, unregister 1 →
  `Clients() == 1`; empty hub → 0.
- `internal/api`: `TestMetricsHandler` extended — stub `ClientCounter`
  returning 3; assert decoded `SSEClients == 3` and raw-map key
  `sse_clients` present.
- Frontend: `npm run build`; visual — header reads
  `2 connections · up … · updated …` on the Overview tab (its two
  streams), `1 connection` from a board-only tab situation.

## Alternatives considered

- **Presence gate as source** — rejected: counter not maintained when idle
  mode is disabled.
- **Label "clients"** — rejected by user choice (double-take when one
  Overview tab reads 2).
- **Extending NewRouter's signature** — unnecessary; the hub is already a
  NewRouter parameter, only the handler needs the extra argument.
