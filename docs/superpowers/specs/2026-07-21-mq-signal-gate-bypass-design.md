# Idle mode v3: MQ-signaled work bypasses the presence gate

**Date:** 2026-07-21
**Status:** Approved

## Problem

With heartbeat-driven idle mode (v2), the backend went fully idle
overnight — and package state transitions stopped being tracked. The MQ
consumer kept the *raw* package state current (`upsertPackage` on every
build event) but its `ws.Signal(pkg)` jobs were dropped by the idle
gate in `sendJob`, so no worker pass ran. The event log,
`target_state_durations`, real terminal-state resolution (`finished` →
`succeeded`/`failed` with details), publish detection and version
updates are all produced by `worker.ProcessOnce`, which stamps events
with `time.Now()` at pass time. Result: the morning heartbeat woke the
gate, `DispatchDue` flushed every due package at once, and the event
log showed every overnight build as happening the moment the Builds tab
was opened.

## Decision summary

- **Fix: MQ-signaled jobs bypass the gate** (approach A, user choice).
  `Signal()` dispatches to the workers even while idle; idle-time OBS
  traffic becomes event-driven — one targeted per-package fetch per
  real build completion, bounded by the existing background rate
  limiter, zero when nothing builds.
- Rejected: the consumer writing transition events itself (duplicates
  worker event semantics with coarser MQ data — two writers that
  drift, and the wake pass would double-emit); waking the whole gate on
  MQ activity (nightly rebuilds would keep discovery's full project
  sweeps running — reintroduces the traffic idle mode removes).
- The discovery poller, the scheduler's periodic `DispatchDue` ticks,
  and `Add` stay gated exactly as today.

## Design

### 1. Working set — `internal/workingset/workingset.go`

`sendJob` splits into an ungated core and a gate-checked wrapper:

```go
// enqueue attempts a non-blocking enqueue and marks every package in
// the job as in-flight on success. Drops the job if the channel is
// full — the packages stay due and are retried on the next tick or
// signal. Must be called with ws.mu held. Callers must ensure no
// package in the job is already in-flight.
func (ws *WorkingSet) enqueue(job Job)

// sendJob is enqueue behind the presence gate: while the gate is idle
// the job is dropped (drained on wake). Used by the scheduler paths;
// Signal bypasses it deliberately.
func (ws *WorkingSet) sendJob(job Job)
```

- `sendJob` keeps today's body (gate check → `enqueue`); `Add` and
  `DispatchDue` keep calling `sendJob`.
- `Signal` calls `enqueue` directly. Its doc comment gains the
  rationale: MQ events are authoritative change notifications; the
  fetch cost is proportional to real build activity and still passes
  the background rate limiter, so signaled passes run even while the
  presence gate is idle — that is how transitions get recorded with
  correct timestamps overnight.
- The worker pool consumes `Dispatch()` unconditionally (it was never
  gated), so no other component changes. Both consumer call sites —
  build events and the `repo.published` publish chain — benefit
  automatically.

### 2. Accepted edge: same-package signal during an in-flight pass

`Signal` on an in-flight package sets the existing `wake` flag; `Done`
re-marks the entry due-now, but that re-dispatch rides the scheduler
tick, which stays gated. While idle, the follow-up fetch waits for the
next MQ event for that package or the morning wake. The consumer has
already upserted the newer raw state by then; only detail enrichment
defers. Rare (two completions of one package within one ~seconds pass)
and self-healing — explicitly accepted, no code change.

### 3. Comment and docs updates

- `internal/mq/consumer.go`, `mqStateToRollup`: "the worker's wake
  pass derives the real terminal state" → "the signaled worker pass
  derives the real terminal state" (the pass now runs at event time,
  not wake time).
- Both `config.yaml.example` files, idle block: add that MQ build
  events still trigger targeted per-package fetches while idle, so
  state transitions are recorded as they happen; only periodic
  discovery/working-set sweeps pause.

## Error handling

Nothing new. `enqueue` keeps the non-blocking channel send: a full
queue drops the job, the entry stays due, and the next signal or wake
retries. Rate limiting is unchanged — signaled worker fetches are
background traffic under the existing per-minute budget.

## Testing

- **workingset** (stub gate held idle): `Signal` puts a job on
  `Dispatch()` and marks the package in-flight; `Add` of a new package
  does NOT dispatch; `DispatchDue` with a due entry does NOT dispatch;
  `Signal` on an in-flight package sets the wake flag without
  double-dispatching. Existing gate tests (drop-while-idle via
  scheduler paths, drain-on-wake) unchanged and still green.
- **Full gates**: `go test ./... -count=1 && go build ./...` from
  `backend/`.
- **Live check**: with all tabs hidden/on Overview past the linger
  (log shows `presence: idle`), trigger or await an OBS build; the
  event log gains the transition within seconds of the MQ event, with
  `polling` still `idle` in `/api/metrics`.

## Alternatives considered

- Consumer-written transition events (zero OBS traffic) — coarse MQ
  rollups, duplicated event semantics, double-emit on wake; rejected.
- MQ events wake the whole gate — discovery sweeps all night during
  rebuild activity; rejected.
- Also bypassing the gate for `Add` — unnecessary: its only
  dispatching caller is the poller, which is itself gated.
