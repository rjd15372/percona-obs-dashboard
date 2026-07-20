# Idle mode v2: tab-visibility heartbeats

**Date:** 2026-07-20
**Status:** Approved

## Problem

The idle-mode gate counts open SSE connections, but an open connection
only means an open tab — not someone looking. In practice 5-6
connections are always present (each tab holds one `/api/stream`
EventSource, an Overview tab two), so the backend has never gone idle
since the feature shipped. The gate needs to know whether a tab is
actually visible.

## Decision summary

- **Hidden tabs keep streaming, they just stop counting.** SSE stays
  open so a backgrounded tab is instantly current when the user returns;
  the tab simply stops heartbeating while hidden. (Rejected: closing the
  EventSource on `visibilitychange` — no backend change needed, but tabs
  return stale and reconnect churn on every tab switch.)
- **Mechanism: dedicated heartbeat endpoint** (`POST /api/presence`)
  driving a reworked gate. (Rejected: treating `/api/metrics` polls as
  heartbeats — only fires while the metrics panel is expanded;
  per-connection visibility flags in the hub — connection IDs and
  per-connection state for the same outcome.)
- **Timeout: reuse `idle.linger`** (default 5m) — the user took back an
  initial "tighter dedicated timeout" choice. No new config key. The
  gate idles when no heartbeat has arrived for `linger`; seeding the
  timestamp at boot makes the existing boot-grace behavior fall out of
  the same rule.
- **Heartbeats fire only from the Builds and Artifacts tabs** (user
  decision). Overview renders from the DB, which the MQ consumer keeps
  updated while polling is idle — an Overview viewer doesn't need OBS
  polling. Switching to Builds/Artifacts beats immediately and wakes
  polling.
- Heartbeat cadence: 60s while the tab is visible and on an eligible
  main tab, plus an immediate beat on mount, on `visibilitychange` →
  visible, and on switching onto an eligible tab.
- New observability: `/api/metrics` reports `polling: "active"|"idle"`
  and the panel header shows it.

## Design

### 1. Gate rework — `internal/presence/presence.go`

- `Connect()` and `Disconnect()` are removed, along with the `clients`
  counter and `lastDisconnect`. The `Presence` hook field and its calls
  in `internal/hub/hub.go` are removed too — the hub goes back to being
  a pure broadcaster (its `Clients()` accessor stays; `/api/metrics`
  still reports the connection count).
- New state: `lastSeen time.Time`, seeded to `now` in `New` (boot
  grace: a restarted backend polls for one linger window before idling,
  so post-redeploy reconciliation doesn't wait for a visitor).
- New method:

```go
// Heartbeat records that a visible dashboard tab is watching and wakes
// subscribers on idle→active.
func (g *Gate) Heartbeat()
```

  Stamps `lastSeen`; if the gate was idle, logs
  `presence: active — resuming background polling` and does the
  existing non-blocking send to every subscriber channel.
- `Active()` becomes `now.Sub(lastSeen) < linger`, with the existing
  idle-transition logging preserved. Disabled gate: permanently active,
  `Heartbeat()` a no-op, subscriber channels never fire — as today.
- New accessor for the metrics endpoint:

```go
// State returns "active" or "idle" (always "active" when disabled).
func (g *Gate) State() string
```

- `Subscribe()` unchanged. The poller and working-set wiring
  (`PollGate`, `workingset.Gate` interfaces: `Active()`/`Subscribe()`)
  are untouched.

### 2. Endpoint — `internal/api`

- `POST /api/presence` → `gate.Heartbeat()`, responds 204 No Content.
- Handler takes a consumer-local interface (established pattern):

```go
// Beater receives dashboard-tab heartbeats.
type Beater interface{ Heartbeat() }
```

- `NewRouter` gains the gate as a parameter (it does not currently
  receive it); `main.go` passes the existing `gate`.

### 3. Metrics exposure

- `metricsHandler` gains the gate via a second consumer-local
  interface:

```go
// PollState reports whether background polling is active or idle.
type PollState interface{ State() string }
```

- `metricsResponse` gains `Polling string \`json:"polling"\`` (top
  level, after `SSEClients`), populated per request.
- Frontend `types/metrics.ts`: `polling: string`. Panel header span
  becomes
  `6 connections · polling active · up 3d 4h · updated 12s ago` — a
  `polling {{ data.polling }}` segment inserted between the connection
  count and uptime, wrapped in `<template v-if="data.polling">` so an
  old backend payload (field absent) renders the header exactly as
  today.

### 4. Frontend heartbeat — `composables/usePresenceHeartbeat.ts` (new)

- Signature: `usePresenceHeartbeat(eligible: Ref<boolean>)`; `App.vue`
  calls it once from setup with
  `computed(() => mainTab.value !== 'overview')` — i.e. Builds
  ('board') and Artifacts tabs are eligible, Overview is not.
- `shouldBeat()` = `document.visibilityState === 'visible' &&
  eligible.value`.
- Behavior: `beat()` = `fetch('/api/presence', { method: 'POST' })`
  with errors swallowed (a missed beat is harmless; the next one
  retries).
  - On mount: `beat()` if `shouldBeat()`, and start a 60s interval
    that beats only while `shouldBeat()`.
  - On `visibilitychange` → visible: immediate `beat()` if eligible.
  - On `eligible` flipping false→true (watch): immediate `beat()` if
    visible.
  - On unmount: clear the interval, remove the listener, stop the
    watcher.
- Multiple browser tabs beat independently — idempotent by design.
- Consequence (accepted): a viewer parked on Overview for more than
  one linger window sends the backend idle; their Overview keeps
  updating via MQ events, and switching to Builds/Artifacts wakes
  polling immediately.

## Error handling

- Frontend beat failures are swallowed (no user-visible error; the
  backend just sees a gap).
- Backend handler has no failure modes beyond the router's own; always
  204.
- Deployment note: an old frontend (no heartbeats) against a new
  backend idles after one linger window even with viewers — frontend
  and backend deploy together here (single `task redeploy`), so this is
  a non-issue; listed for completeness.

## Testing

- **presence**: rework tests — heartbeat within linger → active; no
  heartbeat for linger (fake clock) → idle with one transition log;
  heartbeat while idle → active + subscriber fires once; boot grace
  (fresh gate active without any heartbeat, idles after linger);
  disabled gate always active, Heartbeat no-op, subscriber silent.
- **hub**: drop the Presence-hook expectations from existing tests;
  `Clients()` tests stay.
- **api**: `POST /api/presence` → 204 and a spy `Beater` records one
  call; `TestMetricsHandler` asserts `polling` present and equal to the
  stub's value.
- **frontend**: `npm run build`; visual — header shows
  `polling active` while on Builds/Artifacts; sitting on Overview (or
  hiding every dashboard tab) flips the server log to `presence: idle`
  after ~5m and the header to `polling idle`; switching back to Builds
  logs the resume line immediately. Tiles/sparkline confirm reduced
  OBS traffic overnight.

## Alternatives considered

- Close SSE on hidden tabs — no backend change but stale-on-return and
  reconnect churn; rejected.
- Metrics-poll piggyback heartbeat — only fires with the panel
  expanded; rejected.
- Per-connection visibility state in the hub — machinery without
  benefit over a global heartbeat; rejected.
- Dedicated tighter timeout config — initially chosen, then taken back
  in favor of reusing `idle.linger`; no new knob.
