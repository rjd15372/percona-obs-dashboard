# MQ-Signal Gate Bypass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** MQ-signaled worker jobs dispatch even while the presence gate is idle, so package state transitions are recorded (with correct timestamps) as RabbitMQ events arrive overnight.

**Architecture:** `workingset.sendJob` splits into an ungated `enqueue` core and the existing gate-checked `sendJob` wrapper; `Signal` calls `enqueue` directly while `Add`/`DispatchDue` stay gated. Comment/doc updates in the MQ consumer and both example configs. No consumer, gate, poller, or worker changes.

**Tech Stack:** Go.

**User decisions (already made):**
- Fix approach A: MQ-signaled jobs bypass the gate; discovery poller and scheduler ticks stay gated. (Rejected by user choice: consumer-written events; waking the whole gate on MQ activity.)
- The same-package-signal-during-in-flight-pass edge (follow-up fetch waits for the next MQ event or wake) is explicitly accepted — no code for it.

Spec: `docs/superpowers/specs/2026-07-21-mq-signal-gate-bypass-design.md`

**Conventions:** backend commands from `backend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: Signal bypasses the idle gate

**Goal:** `Signal()` dispatches while the gate is idle; `Add`/`DispatchDue` keep today's gating; comments and example configs describe the new behavior.

**Files:**
- Modify: `backend/internal/workingset/workingset.go` (split sendJob, reroute Signal, doc comments)
- Modify: `backend/internal/workingset/workingset_test.go` (append one test)
- Modify: `backend/internal/mq/consumer.go` (one comment line)
- Modify: `config.yaml.example` + `backend/config.yaml.example` (idle block comment)

**Acceptance Criteria:**
- [ ] With an idle stub gate: `Signal` puts the job on `Dispatch()` immediately; a second `Signal` on the now-in-flight package does NOT double-dispatch
- [ ] Existing `TestGateBlocksDispatchWhileIdle` still passes unchanged (Add + scheduler stay gated, wake drains)
- [ ] `mqStateToRollup`'s comment says "the signaled worker pass"; both example configs say MQ build events still trigger targeted per-package fetches while idle
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `cd backend && go test ./internal/workingset/ -run 'TestSignalBypassesIdleGate|TestGateBlocksDispatchWhileIdle' -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/workingset/workingset_test.go` (reuses the existing `stubGate` and `pkg` helpers already in this file):

```go
func TestSignalBypassesIdleGate(t *testing.T) {
	ws := workingset.New(10, 20*time.Millisecond, 5*time.Minute, 4)
	g := &stubGate{wake: make(chan struct{}, 1)}
	ws.SetGate(g) // active stays false: idle

	// Signal must dispatch immediately even while the gate is idle — MQ
	// events drive targeted fetches overnight.
	ws.Signal(pkg("proj", "pkg-a", model.RollupFailed))
	select {
	case j := <-ws.Dispatch():
		if j.Pkgs[0].Name != "pkg-a" {
			t.Fatalf("unexpected package %s", j.Pkgs[0].Name)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Signal must dispatch while the gate is idle")
	}

	// A second Signal while the first pass is in flight must not
	// double-dispatch — it marks the wake flag exactly as before.
	ws.Signal(pkg("proj", "pkg-a", model.RollupFailed))
	select {
	case <-ws.Dispatch():
		t.Fatal("in-flight Signal must not double-dispatch")
	case <-time.After(80 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/workingset/ -run TestSignalBypassesIdleGate -v`
Expected: FAIL with "Signal must dispatch while the gate is idle" (the idle gate currently drops the job).

- [ ] **Step 3: Implement**

In `backend/internal/workingset/workingset.go`:

Replace the whole `sendJob` function (currently the last function in the file, with its comment block) with:

```go
// enqueue attempts a non-blocking enqueue and marks every package in the job
// as in-flight on success. Drops the job if the channel is full — the
// packages stay due and are retried on the next tick or signal. Must be
// called with ws.mu held. Callers must ensure no package in the job is
// already in-flight.
func (ws *WorkingSet) enqueue(job Job) {
	select {
	case ws.dispatch <- job:
		for _, p := range job.Pkgs {
			ws.inflight[p.Project+"/"+p.Name] = true
		}
	default:
	}
}

// sendJob is enqueue behind the presence gate: while the gate is idle the
// job is dropped (the packages stay due and are drained on wake). Used by
// the scheduler-driven paths (Add, DispatchDue); Signal bypasses it
// deliberately — see Signal.
func (ws *WorkingSet) sendJob(job Job) {
	if ws.gate != nil && !ws.gate.Active() {
		return
	}
	ws.enqueue(job)
}
```

In `Signal`, change the dispatch line from `ws.sendJob(Job{Pkgs: []*model.Package{pkg}})` to `ws.enqueue(Job{Pkgs: []*model.Package{pkg}})`, and replace `Signal`'s doc comment with:

```go
// Signal replaces the stored package, fully resets its schedule (interval
// back to base — MQ events are strong change signals), and dispatches
// immediately (unless in-flight, in which case it is marked to wake as soon
// as the in-flight pass completes). Used by the MQ consumer for real-time
// reactions.
//
// Signaled jobs bypass the presence gate: MQ events are authoritative
// change notifications, so the targeted fetch runs even while the dashboard
// is idle — that is how state transitions get recorded with correct
// timestamps overnight. The cost is proportional to real build activity and
// still passes the background rate limiter.
```

`Add` and `DispatchDue` keep calling `sendJob` — do not touch them.

- [ ] **Step 4: Comment + config updates**

In `backend/internal/mq/consumer.go`, in `mqStateToRollup`, change the `default:` case comment from "the worker's wake pass derives the real terminal state" to "the signaled worker pass derives the real terminal state" (rest of the comment unchanged).

In BOTH `config.yaml.example` (repo root) and `backend/config.yaml.example`, replace the idle-block comment

```yaml
# Idle mode: pause background OBS polling (discovery + working set) while
# no dashboard tab is watching. Visible Builds/Artifacts tabs send a
# heartbeat every minute; RabbitMQ events keep the database current while
# idle, and the first heartbeat triggers an immediate refresh.
# Enabled by default — set enabled: false to poll continuously.
```

with

```yaml
# Idle mode: pause periodic OBS polling (discovery + working-set sweeps)
# while no dashboard tab is watching. Visible Builds/Artifacts tabs send
# a heartbeat every minute; the first heartbeat triggers an immediate
# refresh. While idle, RabbitMQ build events still trigger targeted
# per-package fetches, so state transitions are recorded as they happen.
# Enabled by default — set enabled: false to poll continuously.
```

(the `idle:` keys and the linger comment below stay untouched).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/workingset/ -run 'TestSignalBypassesIdleGate|TestGateBlocksDispatchWhileIdle' -count=1 -v && go test ./... -count=1 && go build ./... && gofmt -l internal/workingset internal/mq`
Expected: all PASS, build OK, gofmt empty.

- [ ] **Step 6: Commit**

```bash
git add internal/workingset/workingset.go internal/workingset/workingset_test.go internal/mq/consumer.go ../config.yaml.example config.yaml.example
git commit -s -m "feat(workingset): MQ-signaled jobs bypass the idle gate"
```
