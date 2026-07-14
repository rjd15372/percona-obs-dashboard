# SSE Connection Count in Metrics Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/api/metrics` reports the number of open SSE connections and the panel header shows it as `N connections · up … · updated …`.

**Architecture:** A `Hub.Clients()` accessor (locked map length); `metricsHandler` gains a `ClientCounter` consumer interface param fed with the hub `NewRouter` already receives; one JSON field, one type field, one header-span change.

**Tech Stack:** Go, Vue 3 + TS.

**User decisions (already made):**
- Source: the **hub**, not the presence gate (whose counter isn't maintained when `idle.enabled: false`).
- Label: **"N connections"** (over "clients") — the count is SSE connections; an Overview tab holds 2.
- JSON field `sse_clients`, top level; count shown first in the header span.

Spec: `docs/superpowers/specs/2026-07-14-metrics-panel-clients-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: hub accessor + `sse_clients` in /api/metrics

**Goal:** `Hub.Clients()` exists and the metrics response carries the connection count.

**Files:**
- Modify: `internal/hub/hub.go` (accessor after `Unregister`)
- Modify: `internal/hub/hub_test.go` (append test)
- Modify: `internal/api/metrics.go` (interface, handler signature, response field)
- Modify: `internal/api/metrics_test.go` (stub + assertions)
- Modify: `internal/api/server.go` (route registration passes `h`)

**Acceptance Criteria:**
- [ ] `Hub.Clients()`: empty hub → 0; register 2 → 2; unregister 1 → 1
- [ ] Response JSON has top-level `sse_clients` equal to the counter's value (stub returns 3 in the test)
- [ ] `go test ./internal/hub/ ./internal/api/ -count=1` passes; `go build ./...` OK

**Verify:** `go test ./internal/hub/ ./internal/api/ -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing hub test**

Append to `internal/hub/hub_test.go`:

```go
func TestClients(t *testing.T) {
	h := New()
	if h.Clients() != 0 {
		t.Fatalf("empty hub: Clients() = %d, want 0", h.Clients())
	}
	a := make(chan []byte, 1)
	b := make(chan []byte, 1)
	h.Register(a)
	h.Register(b)
	if h.Clients() != 2 {
		t.Fatalf("after 2 registers: Clients() = %d, want 2", h.Clients())
	}
	h.Unregister(a)
	if h.Clients() != 1 {
		t.Fatalf("after 1 unregister: Clients() = %d, want 1", h.Clients())
	}
}
```

(The file is external package `hub_test`; use `hub.New()` if the existing tests do — match the file's convention.)

- [ ] **Step 2: Write the failing api test changes**

In `internal/api/metrics_test.go`, add the stub near `fakeStatter`:

```go
type fakeClientCounter struct{ n int }

func (f fakeClientCounter) Clients() int { return f.n }
```

Change the handler invocation in `TestMetricsHandler` from:

```go
	metricsHandler(c, ws)(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
```

to:

```go
	metricsHandler(c, ws, fakeClientCounter{n: 3})(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
```

and add at the end of the test (after the uptime assertions):

```go
	if got.SSEClients != 3 {
		t.Fatalf("sse_clients = %d, want 3", got.SSEClients)
	}
	if _, ok := raw["sse_clients"]; !ok {
		t.Fatalf("sse_clients key missing from raw response")
	}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/hub/ -run TestClients -v ; go test ./internal/api/ -run TestMetricsHandler -v`
Expected: compile errors — `h.Clients undefined` / `metricsHandler` argument count.

- [ ] **Step 4: Implement**

In `internal/hub/hub.go`, after `Unregister`:

```go
// Clients returns the number of currently registered SSE clients.
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
```

In `internal/api/metrics.go`, after the `Statter` interface:

```go
// ClientCounter reports the number of connected SSE clients.
type ClientCounter interface{ Clients() int }
```

Extend `metricsResponse`:

```go
type metricsResponse struct {
	OBS           obsSection     `json:"obs"`
	Limiter       limiterSection `json:"limiter"`
	WorkingSet    wsSection      `json:"working_set"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	SSEClients    int            `json:"sse_clients"`
}
```

Change the handler signature and add the field to the encoded literal:

```go
func metricsHandler(obsClient *obs.Client, ws Statter, clients ClientCounter) http.HandlerFunc {
```

```go
			UptimeSeconds: int64(time.Since(processStart).Seconds()),
			SSEClients:    clients.Clients(),
```

Extend the handler doc comment's enumeration with "connected SSE clients".

In `internal/api/server.go`, change the route registration:

```go
	r.Get("/api/metrics", metricsHandler(obsClient, ws, h))
```

(`h *hub.Hub` is already a `NewRouter` parameter; no signature change.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/hub/ ./internal/api/ -count=1 && go test ./... -count=1 && go build ./...`
Expected: all PASS, build OK.

- [ ] **Step 6: Commit**

```bash
git add internal/hub/hub.go internal/hub/hub_test.go internal/api/metrics.go internal/api/metrics_test.go internal/api/server.go
git commit -s -m "feat(api): report connected SSE client count in /api/metrics"
```

---

### Task 2: connection count in the panel header

**Goal:** Header reads `3 connections · up 3d 4h · updated 12s ago` (singular `1 connection`).

**Files:**
- Modify: `frontend/src/types/metrics.ts` (one field)
- Modify: `frontend/src/components/MetricsPanel.vue` (header span)

**Acceptance Criteria:**
- [ ] `MetricsSnapshot` has `sse_clients: number` (top level)
- [ ] Header span shows count first with singular/plural handling, then uptime, then refresh age
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Type field**

In `frontend/src/types/metrics.ts`, add to `MetricsSnapshot` after `uptime_seconds`:

```ts
  sse_clients: number
```

- [ ] **Step 2: Header span**

In `frontend/src/components/MetricsPanel.vue`, replace:

```html
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
```

with:

```html
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">{{ data.sse_clients }} connection{{ data.sse_clients === 1 ? '' : 's' }} · up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
```

- [ ] **Step 3: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/types/metrics.ts frontend/src/components/MetricsPanel.vue
git commit -s -m "feat(overview): SSE connection count in metrics-panel header"
```
