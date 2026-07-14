# Idle mode: pause OBS polling when no dashboard clients are connected

**Date:** 2026-07-14
**Status:** Approved

## Problem

The discovery poller and the working-set worker generate continuous OBS
traffic regardless of whether anyone is looking at the dashboard. When no
client is connected, that polling buys nothing: RabbitMQ events alone can
keep the database current enough, and full reconciliation can wait until
someone opens the dashboard again.

## Decision summary

- **Presence = open SSE connections** to `/api/stream` (every dashboard tab
  holds one). Direct JSON-API consumers (curl) do not count and may read
  stale-while-idle data — accepted.
- **Wake = immediate full refresh**: the idle→active transition triggers a
  poller tick and a working-set dispatch right away; a freshly opened
  dashboard converges within seconds (bounded burst, like startup).
- **Unblocker keeps running while idle** — it is autonomous remediation;
  it reads only the local DB (kept fresh by MQ) and its triggers already
  bypass the limiter.
- **Architecture: central presence gate** consulted by the existing loops
  (rejected: stopping/restarting goroutines — lifecycle churn for no gain;
  frontend heartbeat — SSE connect/disconnect already gives exact presence).
- **Enabled by default** (`idle.enabled: true`); the config knob is an
  opt-out. `idle.linger` (default 5m) absorbs refreshes/brief closes.

## Design

### 1. `internal/presence` — the gate

```go
// Gate tracks whether any dashboard client is connected and whether
// background polling should run. Safe for concurrent use.
type Gate struct {
    // constructor: New(enabled bool, linger time.Duration) *Gate
    // internal: mu, clients int, lastDisconnect time.Time, linger,
    // enabled bool, now func() time.Time (injectable), subscribers []chan struct{}
}

func (g *Gate) Connect()                    // hub: SSE client registered
func (g *Gate) Disconnect()                 // hub: SSE client gone
func (g *Gate) Active() bool                // poll allowed right now?
func (g *Gate) Subscribe() <-chan struct{}  // signaled once per idle→active transition
```

Semantics:

- `Active()` is true when: the feature is disabled, OR `clients > 0`, OR
  `now − lastDisconnect < linger`.
- **Boot grace:** `New` stamps `lastDisconnect = now`, so a restarted
  backend runs one normal convergence window (discovery, working-set
  catch-up, stale-project GC) even with no viewers, then idles. Without it,
  post-redeploy reconciliation would wait for the next visitor.
- `Subscribe` returns a buffered(1) channel. On each transition from
  idle (`Active() == false`) to active, every subscriber channel gets a
  non-blocking send. Connects while already active signal nothing.
  Disabled gates never signal (polling never paused).
- Transition logging: `slog.Info("presence: idle — background polling paused")`
  and `slog.Info("presence: active — resuming", "clients", n)`.
- Idle→active is evaluated inside `Connect`; active→idle lazily whenever
  `Active()` is consulted after the linger expires (no timer goroutine —
  the pollers tick anyway and simply observe the gate).

### 2. Hub wiring

`hub.Hub` gains an optional field:

```go
Presence interface {
    Connect()
    Disconnect()
}
```

`Register` calls `h.Presence.Connect()` and `Unregister` calls
`h.Presence.Disconnect()` when the field is non-nil. The hub does not
import the presence package; `main.go` assigns the gate.

### 3. Gated loops

- **Poller** (`internal/obs/poller.go`): `Poller` gains a `gate` field
  (interface `{ Active() bool; Subscribe() <-chan struct{} }`, nil = always
  active for tests/back-compat). `Run` becomes:

  ```go
  wake := p.gate.Subscribe()
  p.tick(ctx)                     // startup tick unchanged (boot grace)
  for {
      select {
      case <-ctx.Done(): return
      case <-ticker.C:
          if p.gate.Active() { p.tick(ctx) }
      case <-wake:
          p.tick(ctx)             // immediate wake refresh
      }
  }
  ```

- **Working set** (`internal/workingset/workingset.go`): the gate is
  enforced in `sendJob` — the single funnel through which every dispatch
  path flows (`Add`'s immediate dispatch for new packages, `Signal`'s
  MQ-driven dispatch, and the scheduler's `DispatchDue`). When the gate is
  idle, `sendJob` drops the job exactly as it does when the queue is full:
  the package stays due and is picked up later. This matters because `Add`
  and `Signal` dispatch immediately — gating only the scheduler tick would
  leave the MQ→worker→OBS path live while idle. `StartScheduler`
  additionally gains the wake case (immediate `DispatchDue` on idle→active)
  and skips ticking `DispatchDue` while idle to avoid pointless scans.
  While idle, MQ keeps accumulating due packages; the wake dispatch drains
  the backlog through the rate limiter at the normal budget.

- **Untouched:** MQ consumer, unblocker sweeper, CVE scanner (no OBS
  traffic), telemetry reporter, all interactive API handlers, and the
  poller's limiter bypass.

### 4. Config

```yaml
idle:
  enabled: true   # opt-out: set false to poll continuously as before
  linger: 5m      # keep polling this long after the last client disconnects
```

Viper keys `idle.enabled` / `idle.linger`; env `IDLE_ENABLED` /
`IDLE_LINGER`; defaults `true` / `"5m"`, with the same
`time.ParseDuration` + wrapped-error handling as the other sections. `main.go` builds `presence.New(cfg.Idle.Enabled, cfg.Idle.Linger)`,
assigns it to the hub, and passes it to `NewPoller` and `StartScheduler`.

### 5. Accepted staleness while idle

Anything MQ does not carry waits for the wake refresh: publish-state
flips, enrichment fetches (is_container, container tags, versions) for new
packages, and stale project/package GC. The unblocker's blocked-episode
detection stays only as fresh as MQ keeps `target_state_durations`. All of
it reconciles within seconds of the first client connecting.

## Testing

- **presence** (fake clock): boot grace → active, then idle after linger;
  Connect → active; Disconnect → active until linger expires, idle after;
  wake signaled exactly once per idle→active transition, not on
  connects-while-active; disabled → always active and never signals;
  Subscribe before/after transitions.
- **poller gating** (httptest OBS + hit counter, in-package): idle gate →
  ticker tick makes no OBS calls; wake signal → immediate tick fetches.
  Nil gate → behaves as before.
- **working-set gating**: due packages + idle gate → no dispatch on tick;
  wake → immediate dispatch.
- **hub wiring**: Register/Unregister propagate to a stub Presence; nil
  Presence safe.
- **config**: defaults (true/5m) + env overrides.
- `go test ./...` green; manual: open/close the dashboard against
  `task dev`, watch the two presence log lines and the metrics panel's
  `req_per_s` fall to ~0 while idle.

## Alternatives considered

- Stop/start goroutines on transitions — rejected (lifecycle churn, respawn
  races; a skipped tick is free).
- Frontend heartbeat endpoint — rejected (SSE already gives exact
  connect/disconnect).
- Counting interactive API requests as presence — rejected by user choice
  (SSE-only).
- Opt-in default — rejected by user choice (enabled by default; opt-out).
