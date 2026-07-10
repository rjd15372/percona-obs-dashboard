# `GET /api/metrics` Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `GET /api/metrics` endpoint returning OBS request counts, a trailing-minute request rate, limiter gauges, and working-set stats as JSON — plus the telemetry log line fixed to report limiter gauges as top-level fields instead of deltas.

**Architecture:** Split the limiter gauges out of `Client.MetricsSnapshot()` into a new `Client.LimiterStats()` accessor; add a 60-bucket per-second ring to `obsMetrics` for the trailing-minute rate; give `telemetry.Reporter` a `Limiter` field for top-level log fields; add an `api` handler that composes the three sources into structured JSON.

**Tech Stack:** Go, chi router, `log/slog`, `net/http/httptest` for handler tests. All state is in-memory under existing mutexes — no DB, no new goroutines.

**User decisions (already made):**
- Scope: "Full telemetry picture" — endpoint includes working-set stats, not just OBS client metrics.
- "Yes, fix the log line too" — limiter gauges leave `MetricsSnapshot()`; log line gets top-level `limiter_remaining`/`limiter_waits`.
- Approach: "Split accessors + structured JSON" (rejected: flat map dump, shared telemetry snapshot type).
- Endpoint also reports current req/s of OBS requests over the last minute (`obs.req_per_s`).

Spec: `docs/superpowers/specs/2026-07-10-metrics-endpoint-design.md`

**Conventions:** all commands run from `/home/rdias/Work/percona-obs-dashboard/backend`. Commits: `git commit -s`, never a `Co-Authored-By:` trailer.

---

### Task 1: `obs.LimiterStats` accessor + pure `MetricsSnapshot()`

**Goal:** Limiter gauges move out of the metrics snapshot into a typed accessor.

**Files:**
- Create: `internal/obs/metrics_test.go`
- Modify: `internal/obs/client.go:72-82` (`MetricsSnapshot`), plus new type/method after it

**Acceptance Criteria:**
- [ ] `MetricsSnapshot()` never contains `limiter_remaining`/`limiter_waits` keys, even with a budget set
- [ ] `LimiterStats()` with no budget returns the zero value (`Enabled: false`)
- [ ] `LimiterStats()` with budget 5 and one acquired slot returns `{Enabled: true, Budget: 5, Remaining: 4, Waits: 0}`
- [ ] `go test ./internal/obs/ -run 'TestMetricsSnapshot|TestLimiterStats' -v` passes

**Verify:** `go test ./internal/obs/ -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/obs/metrics_test.go`:

```go
package obs

import (
	"context"
	"testing"
)

func TestMetricsSnapshotExcludesLimiterKeys(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(10)
	c.metrics.inc("build_results")

	snap := c.MetricsSnapshot()
	if _, ok := snap["limiter_remaining"]; ok {
		t.Fatalf("limiter_remaining leaked into snapshot: %v", snap)
	}
	if _, ok := snap["limiter_waits"]; ok {
		t.Fatalf("limiter_waits leaked into snapshot: %v", snap)
	}
	if snap["build_results"] != 1 {
		t.Fatalf("build_results = %d, want 1", snap["build_results"])
	}
}

func TestLimiterStatsDisabled(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	ls := c.LimiterStats()
	if ls.Enabled || ls.Budget != 0 || ls.Remaining != 0 || ls.Waits != 0 {
		t.Fatalf("disabled limiter stats = %+v, want zero value", ls)
	}
}

func TestLimiterStatsEnabled(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(5)
	if err := c.limiter.acquire(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ls := c.LimiterStats()
	if !ls.Enabled || ls.Budget != 5 || ls.Remaining != 4 || ls.Waits != 0 {
		t.Fatalf("stats = %+v, want {Enabled:true Budget:5 Remaining:4 Waits:0}", ls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/obs/ -run 'TestMetricsSnapshot|TestLimiterStats' -v`
Expected: compile error — `c.LimiterStats undefined` (and after implementing only the type, `TestMetricsSnapshotExcludesLimiterKeys` FAILS because the keys are still present).

- [ ] **Step 3: Implement**

In `internal/obs/client.go`, replace the current `MetricsSnapshot` (lines 72-82):

```go
// MetricsSnapshot returns a copy of the per-operation OBS request counts.
func (c *Client) MetricsSnapshot() map[string]int64 {
	return c.metrics.snapshot()
}

// LimiterStats reports the background rate limiter's absolute gauges.
// The zero value (Enabled: false) means rate limiting is disabled.
type LimiterStats struct {
	Enabled   bool
	Budget    int
	Remaining int64
	Waits     int64
}

// LimiterStats returns the current limiter gauges.
func (c *Client) LimiterStats() LimiterStats {
	if c.limiter.budget <= 0 {
		return LimiterStats{}
	}
	waits, remaining := c.limiter.stats()
	return LimiterStats{Enabled: true, Budget: c.limiter.budget, Remaining: remaining, Waits: waits}
}
```

(`minuteLimiter.stats()` in `internal/obs/limiter.go` stays unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/obs/ -v`
Expected: all PASS (including pre-existing tests).

- [ ] **Step 5: Commit**

```bash
git add internal/obs/client.go internal/obs/metrics_test.go
git commit -s -m "feat(obs): LimiterStats accessor, drop limiter gauges from MetricsSnapshot"
```

---

### Task 2: trailing-minute request rate (`RatePerSecond`)

**Goal:** `obsMetrics` counts requests into a 60-bucket per-second ring; `Client.RatePerSecond()` reports requests/second over the trailing minute.

**Files:**
- Modify: `internal/obs/client.go:16-36` (`obsMetrics` struct, `inc`, constructor) plus new methods
- Modify: `internal/obs/metrics_test.go` (append tests)

**Acceptance Criteria:**
- [ ] 120 increments within one second → `ratePerSecond()` returns exactly `2.0`
- [ ] The same 120 increments still count 30s later; 61s later they return `0.0`
- [ ] A bucket reused one minute later (same `sec % 60` slot) is zeroed first — old hits don't survive
- [ ] Zero traffic → `0.0`
- [ ] `go test ./internal/obs/ -v` passes

**Verify:** `go test ./internal/obs/ -run TestRatePerSecond -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/obs/metrics_test.go`:

```go
func TestRatePerSecondWindow(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	if got := m.ratePerSecond(); got != 0 {
		t.Fatalf("rate with no traffic = %v, want 0", got)
	}

	for i := 0; i < 120; i++ {
		m.inc("build_results")
	}
	if got := m.ratePerSecond(); got != 2.0 {
		t.Fatalf("rate = %v, want 2.0 (120 reqs / 60s)", got)
	}

	cur = base.Add(30 * time.Second)
	if got := m.ratePerSecond(); got != 2.0 {
		t.Fatalf("rate at +30s = %v, want 2.0 (still in window)", got)
	}

	cur = base.Add(61 * time.Second)
	if got := m.ratePerSecond(); got != 0 {
		t.Fatalf("rate at +61s = %v, want 0 (window slid past)", got)
	}
}

func TestRatePerSecondBucketReuse(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	m.inc("build_results")
	cur = base.Add(60 * time.Second) // same ring slot, different second
	m.inc("build_results")

	want := float64(1) / 60
	if got := m.ratePerSecond(); got != want {
		t.Fatalf("rate = %v, want %v (stale bucket must be zeroed on reuse)", got, want)
	}
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/obs/ -run TestRatePerSecond -v`
Expected: compile error — `unknown field now in struct literal` / `m.ratePerSecond undefined`.

- [ ] **Step 3: Implement**

In `internal/obs/client.go`, replace the `obsMetrics` struct and `inc` (lines 16-26):

```go
// obsMetrics counts OBS requests by operation label, and into a ring of
// per-second buckets for the trailing-minute request rate.
type obsMetrics struct {
	mu     sync.Mutex
	counts map[string]int64
	now    func() time.Time

	ringSec  [60]int64 // unix second each bucket currently holds
	ringHits [60]int64 // request count observed within that second
}

func (m *obsMetrics) inc(op string) {
	m.mu.Lock()
	m.counts[op]++
	sec := m.now().Unix()
	i := sec % 60
	if m.ringSec[i] != sec {
		m.ringSec[i] = sec
		m.ringHits[i] = 0
	}
	m.ringHits[i]++
	m.mu.Unlock()
}

// ratePerSecond returns requests/second over the trailing 60 seconds.
func (m *obsMetrics) ratePerSecond() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := m.now().Unix() - 60
	var total int64
	for i := range m.ringSec {
		if m.ringSec[i] > cutoff {
			total += m.ringHits[i]
		}
	}
	return float64(total) / 60
}
```

In `NewClient` (line ~57), initialize the clock:

```go
		metrics:  &obsMetrics{counts: make(map[string]int64), now: time.Now},
```

After the `LimiterStats()` method from Task 1, add:

```go
// RatePerSecond returns OBS requests per second over the trailing minute,
// counting both background and interactive requests.
func (c *Client) RatePerSecond() float64 {
	return c.metrics.ratePerSecond()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/obs/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/obs/client.go internal/obs/metrics_test.go
git commit -s -m "feat(obs): trailing-minute request rate via per-second ring"
```

---

### Task 3: telemetry log line — top-level limiter fields

**Goal:** The telemetry log line reports `limiter_remaining`/`limiter_waits` as top-level absolute fields (when the limiter is enabled) instead of deltas inside `obs_by_endpoint`.

**Files:**
- Modify: `internal/telemetry/telemetry.go:15-66`
- Modify: `internal/telemetry/telemetry_test.go` (append tests)
- Modify: `cmd/obsboard/main.go:94-99` (Reporter wiring)

**Acceptance Criteria:**
- [ ] With an enabled limiter, the logged line contains `limiter_remaining=<N>` and `limiter_waits=<M>` as top-level fields
- [ ] With a disabled limiter (or nil `Limiter` field), the line contains neither field and `tick` does not panic
- [ ] `main.go` passes the OBS client as the Reporter's `Limiter`
- [ ] `go test ./internal/telemetry/ -v` and `go build ./...` pass

**Verify:** `go test ./internal/telemetry/ -v && go build ./...` → all PASS, build OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/telemetry/telemetry_test.go`:

```go
type fakeLimiter struct{ ls obs.LimiterStats }

func (f fakeLimiter) LimiterStats() obs.LimiterStats { return f.ls }

func captureTick(t *testing.T, r *Reporter) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	r.tick(map[string]int64{})
	return buf.String()
}

func TestTickLogsLimiterGauges(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
		Limiter:  fakeLimiter{ls: obs.LimiterStats{Enabled: true, Budget: 60, Remaining: 41, Waits: 17}},
	}
	out := captureTick(t, r)
	if !strings.Contains(out, "limiter_remaining=41") || !strings.Contains(out, "limiter_waits=17") {
		t.Fatalf("limiter gauges missing from log line: %q", out)
	}
}

func TestTickOmitsLimiterWhenDisabled(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
		Limiter:  fakeLimiter{}, // zero value: Enabled false
	}
	out := captureTick(t, r)
	if strings.Contains(out, "limiter_") {
		t.Fatalf("limiter fields present despite disabled limiter: %q", out)
	}
}

func TestTickNilLimiterNoPanic(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
	}
	_ = captureTick(t, r) // must not panic
}
```

Extend the test file's imports with `"bytes"`, `"log/slog"`, `"strings"`, and `"github.com/percona/obs-dashboard/internal/obs"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/telemetry/ -v`
Expected: compile error — `unknown field Limiter in struct literal of type Reporter`.

- [ ] **Step 3: Implement**

In `internal/telemetry/telemetry.go`, add to the imports:

```go
	"github.com/percona/obs-dashboard/internal/obs"
```

After the `Snapshotter` interface (line 16), add:

```go
// LimiterStatser provides absolute limiter gauges.
type LimiterStatser interface{ LimiterStats() obs.LimiterStats }
```

Add the field to `Reporter`:

```go
// Reporter periodically logs working-set and OBS-request telemetry.
type Reporter struct {
	Interval time.Duration
	Enabled  *atomic.Bool
	Stats    Statter
	Snap     Snapshotter
	Limiter  LimiterStatser // optional; nil disables limiter fields
}
```

Replace the `slog.Info` call in `tick` (lines 54-63) with:

```go
		args := []any{
			"window", r.Interval.String(),
			"ws_packages", s.Total,
			"ws_inflight", s.Inflight,
			"ws_by_state", s.ByState,
			"obs_window", total,
			"obs_total", cumulative,
			"obs_req_per_s", rate,
			"obs_by_endpoint", perOp,
		}
		if r.Limiter != nil {
			if ls := r.Limiter.LimiterStats(); ls.Enabled {
				args = append(args, "limiter_remaining", ls.Remaining, "limiter_waits", ls.Waits)
			}
		}
		slog.Info("telemetry", args...)
```

In `cmd/obsboard/main.go` (lines 94-99), wire the limiter source:

```go
	reporter := &telemetry.Reporter{
		Interval: cfg.Telemetry.Interval,
		Enabled:  telemetryEnabled,
		Stats:    ws,
		Snap:     obsClient,
		Limiter:  obsClient,
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/telemetry/ -v && go build ./...`
Expected: all PASS, build OK.

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/telemetry.go internal/telemetry/telemetry_test.go cmd/obsboard/main.go
git commit -s -m "fix(telemetry): log limiter gauges as top-level fields, not endpoint deltas"
```

---

### Task 4: `GET /api/metrics` handler + wiring

**Goal:** New endpoint composing OBS counts, rate, limiter gauges, and working-set stats into the agreed JSON shape.

**Files:**
- Create: `internal/api/metrics.go`
- Create: `internal/api/metrics_test.go`
- Modify: `internal/api/server.go:17,57-58` (signature + route)
- Modify: `internal/api/handlers_test.go:41,274,606` (existing `NewRouter` calls)
- Modify: `cmd/obsboard/main.go:102` (NewRouter call)

**Acceptance Criteria:**
- [ ] `GET /api/metrics` returns 200 with `Content-Type: application/json`
- [ ] Response has `obs.total`, `obs.by_endpoint`, `obs.req_per_s`, `limiter.{enabled,budget,remaining,waits}`, `working_set.{packages,inflight,by_state}`
- [ ] With budget 60 and no traffic: `limiter` = `{enabled:true, budget:60, remaining:60, waits:0}`, `obs.total` = 0, `obs.req_per_s` = 0
- [ ] Working-set fields mirror the stub Statter's values
- [ ] `go test ./... ` passes and `go build ./...` succeeds

**Verify:** `go test ./internal/api/ -run TestMetricsHandler -v && go test ./... && go build ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Create `internal/api/metrics_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

type fakeStatter struct{ s workingset.Stats }

func (f fakeStatter) Stats() workingset.Stats { return f.s }

func TestMetricsHandler(t *testing.T) {
	c := obs.NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(60)
	ws := fakeStatter{s: workingset.Stats{
		Total:    214,
		Inflight: 3,
		ByState:  map[string]int{"succeeded": 180, "building": 20},
	}}

	rec := httptest.NewRecorder()
	metricsHandler(c, ws)(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got metricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OBS.Total != 0 || got.OBS.ReqPerS != 0 {
		t.Fatalf("obs = %+v, want zero total and rate with no traffic", got.OBS)
	}
	if got.OBS.ByEndpoint == nil {
		t.Fatalf("obs.by_endpoint must be a map, got null")
	}
	if !got.Limiter.Enabled || got.Limiter.Budget != 60 || got.Limiter.Remaining != 60 || got.Limiter.Waits != 0 {
		t.Fatalf("limiter = %+v, want {enabled:true budget:60 remaining:60 waits:0}", got.Limiter)
	}
	if got.WorkingSet.Packages != 214 || got.WorkingSet.Inflight != 3 || got.WorkingSet.ByState["succeeded"] != 180 {
		t.Fatalf("working_set = %+v", got.WorkingSet)
	}
}
```

(Note: the existing `handlers_test.go` may already define a stub Statter or similar helpers — if a name collides, reuse the existing helper instead of redeclaring.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestMetricsHandler -v`
Expected: compile error — `undefined: metricsHandler` / `undefined: metricsResponse`.

- [ ] **Step 3: Implement the handler**

Create `internal/api/metrics.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// Statter provides working-set stats for the metrics endpoint.
type Statter interface{ Stats() workingset.Stats }

type metricsResponse struct {
	OBS        obsSection     `json:"obs"`
	Limiter    limiterSection `json:"limiter"`
	WorkingSet wsSection      `json:"working_set"`
}

type obsSection struct {
	Total      int64            `json:"total"`
	ByEndpoint map[string]int64 `json:"by_endpoint"`
	ReqPerS    float64          `json:"req_per_s"`
}

type limiterSection struct {
	Enabled   bool  `json:"enabled"`
	Budget    int   `json:"budget"`
	Remaining int64 `json:"remaining"`
	Waits     int64 `json:"waits"`
}

type wsSection struct {
	Packages int            `json:"packages"`
	Inflight int            `json:"inflight"`
	ByState  map[string]int `json:"by_state"`
}

// metricsHandler handles GET /api/metrics: absolute OBS request counts,
// the trailing-minute request rate, limiter gauges, and working-set stats.
func metricsHandler(obsClient *obs.Client, ws Statter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		byEndpoint := obsClient.MetricsSnapshot()
		var total int64
		for _, v := range byEndpoint {
			total += v
		}
		ls := obsClient.LimiterStats()
		s := ws.Stats()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse{
			OBS: obsSection{
				Total:      total,
				ByEndpoint: byEndpoint,
				ReqPerS:    obsClient.RatePerSecond(),
			},
			Limiter: limiterSection{
				Enabled:   ls.Enabled,
				Budget:    ls.Budget,
				Remaining: ls.Remaining,
				Waits:     ls.Waits,
			},
			WorkingSet: wsSection{
				Packages: s.Total,
				Inflight: s.Inflight,
				ByState:  s.ByState,
			},
		})
	}
}
```

- [ ] **Step 4: Wire the route**

In `internal/api/server.go`, change the `NewRouter` signature (line 17):

```go
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client, root string, ws Statter, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration) http.Handler {
```

and register the route next to the telemetry routes (after line 58):

```go
	r.Get("/api/metrics", metricsHandler(obsClient, ws))
```

In `cmd/obsboard/main.go` (line 102), pass the working set:

```go
	router := api.NewRouter(db, h, obsClient, cfg.OBSRoot, ws, telemetryEnabled, cfg.Telemetry.Interval)
```

`internal/api/handlers_test.go` calls `NewRouter` at lines 41, 274, and 606 — update all three to pass the stub (`fakeStatter` from `metrics_test.go` is visible package-wide):

```go
	NewRouter(db, hub.New(), obsClient, "isv:percona", fakeStatter{}, new(atomic.Bool), time.Duration(0))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run TestMetricsHandler -v && go test ./... && go build ./...`
Expected: all PASS, build OK. `go vet ./...`-level compile errors here usually mean a missed `NewRouter` caller — `grep -rn "NewRouter" backend` to find any remaining.

- [ ] **Step 6: Manual spot check (optional, if a stack is running)**

`curl -s localhost:4000/api/metrics | jq` → JSON with the three sections.

- [ ] **Step 7: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go internal/api/server.go cmd/obsboard/main.go
git commit -s -m "feat(api): GET /api/metrics with OBS counts, rate, limiter gauges, working set"
```
