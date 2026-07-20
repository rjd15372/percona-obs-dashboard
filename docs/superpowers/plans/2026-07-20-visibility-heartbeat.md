# Idle Mode v2: Tab-Visibility Heartbeats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The idle-mode gate is driven by tab-visibility heartbeats from the Builds/Artifacts tabs instead of SSE connection counts, so parked tabs no longer keep OBS polling awake; the metrics panel shows the gate's decision.

**Architecture:** `presence.Gate` loses `Connect`/`Disconnect` (and the hub its `Presence` hook) and gains `Heartbeat()` + `State()`; `Active()` = last heartbeat within `idle.linger`, with the boot-seeded timestamp preserving boot grace. A new `POST /api/presence` receives beats; `/api/metrics` reports `polling: active|idle`. A frontend composable beats every 60s while the browser tab is visible AND the Builds ('board') or Artifacts main tab is selected.

**Tech Stack:** Go, Vue 3 + TS.

**User decisions (already made):**
- Hidden tabs keep their SSE streams; they just stop counting (heartbeat mechanism, not disconnect-on-hide).
- Timeout: reuse `idle.linger` (5m) — a "tighter dedicated timeout" was chosen then explicitly taken back; NO new config key.
- Heartbeats fire only from the Builds and Artifacts tabs; Overview viewers let the backend idle (accepted: their data stays fresh via MQ).
- Expose `polling: active|idle` in `/api/metrics` and the panel header.

Spec: `docs/superpowers/specs/2026-07-20-visibility-heartbeat-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: gate rework — heartbeats replace connection counting

**Goal:** `presence.Gate` is heartbeat-driven (`Heartbeat()`/`State()`, no `Connect`/`Disconnect`); the hub's `Presence` hook is removed; `main.go` no longer wires it.

**Files:**
- Modify: `backend/internal/presence/presence.go` (whole file — complete replacement below)
- Modify: `backend/internal/presence/presence_test.go` (whole file — complete replacement below)
- Modify: `backend/internal/hub/hub.go` (remove Presence interface/field/calls)
- Modify: `backend/internal/hub/hub_test.go` (delete `stubPresence` + `TestPresenceHook`)
- Modify: `backend/cmd/obsboard/main.go` (delete the `h.Presence = gate` line)

**Acceptance Criteria:**
- [ ] `Gate.Heartbeat()` stamps `lastSeen` and signals subscribers exactly once per idle→active transition; `Active()` = `now − lastSeen < linger`; boot-seeded `lastSeen` keeps the boot-grace behavior
- [ ] `State()` returns `"active"`/`"idle"` ("active" always when disabled); disabled gate: `Heartbeat()` no-op, never signals
- [ ] `grep -rn "Connect\|Disconnect" internal/presence/ internal/hub/` → no matches; `hub.Hub` has no `Presence` field
- [ ] `go build ./... && go test ./internal/presence/ ./internal/hub/ -count=1` pass

**Verify:** `cd backend && go test ./internal/presence/ ./internal/hub/ -count=1 -v && go build ./...` → all PASS, build OK

**Steps:**

- [ ] **Step 1: Replace the presence tests (failing first)**

Replace the entire contents of `backend/internal/presence/presence_test.go` with:

```go
package presence

import (
	"testing"
	"time"
)

func newTestGate(enabled bool, linger time.Duration, at *time.Time) *Gate {
	g := New(enabled, linger)
	g.now = func() time.Time { return *at }
	// Re-stamp boot grace with the fake clock.
	g.lastSeen = *at
	return g
}

func TestBootGraceThenIdle(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)

	if !g.Active() {
		t.Fatal("fresh gate must be active (boot grace)")
	}
	cur = cur.Add(5*time.Minute + time.Second)
	if g.Active() {
		t.Fatal("gate must be idle after boot grace expires with no heartbeats")
	}
}

func TestHeartbeatKeepsActiveThenExpires(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)
	cur = cur.Add(10 * time.Minute) // past boot grace: idle

	g.Heartbeat()
	if !g.Active() {
		t.Fatal("gate must be active right after a heartbeat")
	}
	cur = cur.Add(4 * time.Minute)
	g.Heartbeat() // refresh within the window
	cur = cur.Add(4 * time.Minute)
	if !g.Active() {
		t.Fatal("gate must stay active while heartbeats keep arriving")
	}
	cur = cur.Add(5 * time.Minute) // 9m since last beat > 5m linger
	if g.Active() {
		t.Fatal("gate must be idle after linger passes with no heartbeat")
	}
}

func TestWakeSignaledOncePerTransition(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(2 * time.Minute) // idle

	g.Heartbeat() // idle → active: signal
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on idle→active")
	}

	g.Heartbeat() // beat while active: no signal
	select {
	case <-wake:
		t.Fatal("unexpected wake signal on heartbeat while active")
	default:
	}

	cur = cur.Add(2 * time.Minute) // idle again
	if g.Active() {
		t.Fatal("expected idle after linger")
	}
	g.Heartbeat() // second transition: signal again
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on second idle→active transition")
	}
}

func TestState(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, time.Minute, &cur)
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active during boot grace", g.State())
	}
	cur = cur.Add(2 * time.Minute)
	if g.State() != "idle" {
		t.Fatalf("State() = %q, want idle after linger", g.State())
	}
	g.Heartbeat()
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active after heartbeat", g.State())
	}
}

func TestDisabledAlwaysActiveNeverSignals(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(false, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(time.Hour)

	if !g.Active() {
		t.Fatal("disabled gate must always be active")
	}
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active for disabled gate", g.State())
	}
	g.Heartbeat()
	select {
	case <-wake:
		t.Fatal("disabled gate must never signal")
	default:
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/presence/ -v`
Expected: compile failure — `g.lastSeen undefined` / `g.Heartbeat undefined`.

- [ ] **Step 3: Replace the gate**

Replace the entire contents of `backend/internal/presence/presence.go` with:

```go
// Package presence tracks whether any dashboard tab is actually visible
// (via heartbeats from the frontend) so background OBS polling can pause
// while nobody is looking.
// Design: docs/superpowers/specs/2026-07-20-visibility-heartbeat-design.md
package presence

import (
	"log/slog"
	"sync"
	"time"
)

// Gate reports whether background polling should run and signals
// subscribers once per idle→active transition. Safe for concurrent use.
// A disabled Gate is permanently active and never signals.
type Gate struct {
	mu        sync.Mutex
	enabled   bool
	linger    time.Duration
	lastSeen  time.Time
	now       func() time.Time // injectable for tests
	subs      []chan struct{}
	wasActive bool // last observed state, for transition logging
}

// New returns a Gate. lastSeen starts at now (boot grace): a restarted
// backend runs one linger window before idling, so post-redeploy
// reconciliation doesn't wait for the next visitor.
func New(enabled bool, linger time.Duration) *Gate {
	g := &Gate{enabled: enabled, linger: linger, now: time.Now, wasActive: true}
	g.lastSeen = g.now()
	return g
}

// Heartbeat records that a visible dashboard tab is watching and wakes
// subscribers on idle→active.
func (g *Gate) Heartbeat() {
	if !g.enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	wasActive := g.activeLocked()
	g.lastSeen = g.now()
	if !wasActive {
		g.wasActive = true
		slog.Info("presence: active — resuming background polling")
		for _, ch := range g.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// Active reports whether background polling should run right now.
func (g *Gate) Active() bool {
	if !g.enabled {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	active := g.activeLocked()
	if !active && g.wasActive {
		slog.Info("presence: idle — background polling paused")
	}
	g.wasActive = active
	return active
}

// State returns "active" or "idle" for observability endpoints. A
// disabled gate is always "active".
func (g *Gate) State() string {
	if g.Active() {
		return "active"
	}
	return "idle"
}

func (g *Gate) activeLocked() bool {
	return g.now().Sub(g.lastSeen) < g.linger
}

// Subscribe returns a buffered channel signaled once per idle→active
// transition. A disabled gate's channel never fires.
func (g *Gate) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	if !g.enabled {
		return ch
	}
	g.mu.Lock()
	g.subs = append(g.subs, ch)
	g.mu.Unlock()
	return ch
}
```

- [ ] **Step 4: Remove the hub's Presence hook**

In `backend/internal/hub/hub.go`:
- Delete the `Presence` interface (lines 5-10), the `// Presence, when non-nil …` comment + `Presence Presence` field from the `Hub` struct, the `if h.Presence != nil { h.Presence.Connect() }` block in `Register`, and the `if h.Presence != nil { h.Presence.Disconnect() }` block in `Unregister`. `Clients()` and `Notify()` stay untouched. Result:

```go
package hub

import "sync"

// Hub fans out SSE payloads to all registered clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan<- []byte]struct{}
}

func New() *Hub { return &Hub{clients: make(map[chan<- []byte]struct{})} }

func (h *Hub) Register(ch chan<- []byte) {
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) Unregister(ch chan<- []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}
```

(then the unchanged `Clients` and `Notify` functions follow as they are today).

In `backend/internal/hub/hub_test.go`: delete the `stubPresence` type (struct + its `Connect`/`Disconnect` methods, around line 66) and the whole `TestPresenceHook` function. All other tests stay.

In `backend/cmd/obsboard/main.go`: delete the single line `h.Presence = gate` (line 56). The `gate := presence.New(...)` line above it STAYS — Task 2 passes the gate to the router.

Note: after this deletion `gate` is still used later in main.go (`ws.SetGate(gate)`, `obs.NewPoller(..., gate)`), so the build stays green.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/presence/ ./internal/hub/ -count=1 -v && go test ./... -count=1 && go build ./... && gofmt -l internal/presence internal/hub cmd/obsboard`
Expected: all PASS, build OK, gofmt empty. Also `grep -rn "Connect\|Disconnect" internal/presence/ internal/hub/` → no matches.

- [ ] **Step 6: Commit**

```bash
git add internal/presence/presence.go internal/presence/presence_test.go internal/hub/hub.go internal/hub/hub_test.go cmd/obsboard/main.go
git commit -s -m "feat(presence): heartbeat-driven gate; drop SSE connection counting"
```

---

### Task 2: POST /api/presence + polling state in /api/metrics

**Goal:** The router accepts heartbeats and the metrics payload reports the gate's decision.

**Files:**
- Create: `backend/internal/api/presence.go`
- Create: `backend/internal/api/presence_test.go`
- Modify: `backend/internal/api/metrics.go` (PollState interface, response field, handler param, doc comment)
- Modify: `backend/internal/api/metrics_test.go` (stub + assertions)
- Modify: `backend/internal/api/server.go` (PresenceGate param, two routes)
- Modify: `backend/cmd/obsboard/main.go` (NewRouter call passes `gate`)

**Acceptance Criteria:**
- [ ] `POST /api/presence` → 204 and exactly one `Heartbeat()` call on the gate
- [ ] `/api/metrics` JSON has top-level `polling` equal to the gate's `State()` (stub returns "active" in the test)
- [ ] `go test ./internal/api/ -count=1` passes; `go build ./...` OK

**Verify:** `cd backend && go test ./internal/api/ -run 'TestPresenceHandler|TestMetricsHandler' -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/api/presence_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type spyBeater struct{ beats int }

func (s *spyBeater) Heartbeat() { s.beats++ }

func TestPresenceHandler(t *testing.T) {
	spy := &spyBeater{}
	rec := httptest.NewRecorder()
	presenceHandler(spy)(rec, httptest.NewRequest(http.MethodPost, "/api/presence", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if spy.beats != 1 {
		t.Fatalf("beats = %d, want 1", spy.beats)
	}
}
```

In `backend/internal/api/metrics_test.go`, add near `fakeClientCounter`:

```go
type fakePollState struct{ s string }

func (f fakePollState) State() string { return f.s }
```

Change the handler invocation in `TestMetricsHandler` to:

```go
	metricsHandler(c, ws, fakeClientCounter{n: 3}, db, fakePollState{s: "active"})(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
```

and add after the `sse_clients` assertions:

```go
	if got.Polling != "active" {
		t.Fatalf("polling = %q, want active", got.Polling)
	}
	if _, ok := raw["polling"]; !ok {
		t.Fatalf("polling key missing from raw response")
	}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/api/ -run 'TestPresenceHandler|TestMetricsHandler' -v`
Expected: compile errors — `undefined: presenceHandler` / `metricsHandler` argument count.

- [ ] **Step 3: Implement**

Create `backend/internal/api/presence.go`:

```go
package api

import "net/http"

// Beater receives dashboard-tab heartbeats. See internal/presence.
type Beater interface{ Heartbeat() }

// presenceHandler handles POST /api/presence: a visible dashboard tab's
// periodic heartbeat, driving the idle-mode gate. Always responds 204.
func presenceHandler(gate Beater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gate.Heartbeat()
		w.WriteHeader(http.StatusNoContent)
	}
}
```

In `backend/internal/api/metrics.go`, after the `ClientCounter` interface add:

```go
// PollState reports whether background OBS polling is active or idle.
type PollState interface{ State() string }
```

Extend `metricsResponse` (after `SSEClients`):

```go
	Polling       string         `json:"polling"`
```

Change the handler signature to:

```go
func metricsHandler(obsClient *obs.Client, ws Statter, clients ClientCounter, db *sql.DB, gate PollState) http.HandlerFunc {
```

add to the encoded literal (after `SSEClients`):

```go
			Polling:       gate.State(),
```

and extend the handler doc comment's enumeration with "and the current polling state (active/idle)".

In `backend/internal/api/server.go`, after the imports add the router-level composite (both handler interfaces are defined in this package):

```go
// PresenceGate is what the router needs from the idle-mode gate:
// heartbeats in, polling state out.
type PresenceGate interface {
	Beater
	PollState
}
```

Change the `NewRouter` signature to:

```go
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client, root string, ws Statter, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration, gate PresenceGate) http.Handler {
```

register the new route next to the telemetry POST:

```go
	r.Post("/api/presence", presenceHandler(gate))
```

and change the metrics route to:

```go
	r.Get("/api/metrics", metricsHandler(obsClient, ws, h, db, gate))
```

In `backend/cmd/obsboard/main.go`, change the router construction (line ~117) to:

```go
	router := api.NewRouter(db, h, obsClient, cfg.OBSRoot, ws, telemetryEnabled, cfg.Telemetry.Interval, gate)
```

(`*presence.Gate` has both `Heartbeat()` and `State()`, so it satisfies `PresenceGate`.) Check for other `NewRouter` callers with `grep -rn "NewRouter" --include='*.go' .` — main.go should be the only one; if a test constructs it, append the `fakePollState`-style stub there too.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./... -count=1 && go build ./... && gofmt -l internal/api cmd/obsboard`
Expected: all PASS, build OK; gofmt must not list presence.go/presence_test.go/metrics.go/metrics_test.go/server.go/main.go (release_artifacts.go drift is pre-existing — leave it).

- [ ] **Step 5: Commit**

```bash
git add internal/api/presence.go internal/api/presence_test.go internal/api/metrics.go internal/api/metrics_test.go internal/api/server.go cmd/obsboard/main.go
git commit -s -m "feat(api): presence heartbeat endpoint and polling state in /api/metrics"
```

---

### Task 3: frontend heartbeat + panel polling state

**Goal:** Dashboard tabs beat every 60s while visible on Builds/Artifacts; the metrics panel header shows `polling active|idle`.

**Files:**
- Create: `frontend/src/composables/usePresenceHeartbeat.ts`
- Modify: `frontend/src/App.vue` (import + one call)
- Modify: `frontend/src/types/metrics.ts` (one field)
- Modify: `frontend/src/components/MetricsPanel.vue` (header span)

**Acceptance Criteria:**
- [ ] Composable beats on mount, every 60s, on `visibilitychange`→visible, and on eligibility flipping true — always guarded by `document.visibilityState === 'visible' && eligible.value`; interval/listener/watcher cleaned up on unmount
- [ ] `App.vue` wires it with `computed(() => mainTab.value !== 'overview')`
- [ ] `MetricsSnapshot` has `polling: string`; header reads `N connections · polling active · up … · updated …`, with the polling segment absent when the field is empty/missing
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Create the composable**

Create `frontend/src/composables/usePresenceHeartbeat.ts`:

```ts
import { onMounted, onUnmounted, watch, type Ref } from 'vue'

const BEAT_INTERVAL_MS = 60_000

// Sends a presence heartbeat while the browser tab is visible and an
// eligible main tab (Builds/Artifacts) is selected, so the backend can
// pause OBS polling when nobody is watching live build data.
export function usePresenceHeartbeat(eligible: Ref<boolean>) {
  function shouldBeat(): boolean {
    return document.visibilityState === 'visible' && eligible.value
  }

  function beat() {
    fetch('/api/presence', { method: 'POST' }).catch(() => {
      // A missed beat is harmless; the next interval retries.
    })
  }

  function beatIfWatching() {
    if (shouldBeat()) beat()
  }

  let timer: ReturnType<typeof setInterval> | null = null
  const stopWatch = watch(eligible, (on) => {
    if (on) beatIfWatching()
  })

  onMounted(() => {
    beatIfWatching()
    timer = setInterval(beatIfWatching, BEAT_INTERVAL_MS)
    document.addEventListener('visibilitychange', beatIfWatching)
  })
  onUnmounted(() => {
    if (timer) clearInterval(timer)
    document.removeEventListener('visibilitychange', beatIfWatching)
    stopWatch()
  })
}
```

(`visibilitychange` also fires when the tab hides; `beatIfWatching` no-ops then — no separate visible-check needed.)

- [ ] **Step 2: Wire it in App.vue**

In `frontend/src/App.vue`, add to the imports block:

```ts
import { usePresenceHeartbeat } from './composables/usePresenceHeartbeat'
```

and directly below the `mainTab` declaration (`const mainTab = ref<'board' | 'artifacts' | 'overview'>('overview')`):

```ts
// Heartbeats drive the backend's idle-mode gate: only Builds/Artifacts
// viewers need live OBS polling (Overview stays fresh via MQ events).
usePresenceHeartbeat(computed(() => mainTab.value !== 'overview'))
```

(`computed` is already imported in App.vue.)

- [ ] **Step 3: Type + header**

In `frontend/src/types/metrics.ts`, add to `MetricsSnapshot` after `sse_clients`:

```ts
  polling: string
```

In `frontend/src/components/MetricsPanel.vue`, replace the header span:

```html
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">{{ data.sse_clients }} connection{{ data.sse_clients === 1 ? '' : 's' }} · up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
```

with:

```html
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">{{ data.sse_clients }} connection{{ data.sse_clients === 1 ? '' : 's' }}<template v-if="data.polling"> · polling {{ data.polling }}</template> · up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
```

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0.

- [ ] **Step 5: Commit**

```bash
git add src/composables/usePresenceHeartbeat.ts src/App.vue src/types/metrics.ts src/components/MetricsPanel.vue
git commit -s -m "feat(frontend): visibility heartbeats from Builds/Artifacts and polling state in metrics panel"
```
