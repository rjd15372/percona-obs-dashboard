# Metrics Panel Uptime + Formatting Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/api/metrics` reports `uptime_seconds`; the metrics panel header shows `up 3d 4h · updated 12s ago`; the window-breakdown rows and endpoint table get width caps that restore the approved mockup's tight label→value pairing.

**Architecture:** Backend: a package-level `processStart` in `internal/api/metrics.go` plus one response field. Frontend: one type field, a `fmtUptime` helper, a header tweak, and two width-cap classes in `MetricsPanel.vue`.

**Tech Stack:** Go, Vue 3 + TypeScript + Tailwind arbitrary values.

**User decisions (already made):**
- Uptime in the **panel header** (over the OBS-group subline).
- Fix the endpoint table's identical width defect alongside the reported window rows.
- `uptime_seconds` top-level (process-wide); start captured at package init, not threaded through `NewRouter`.

Spec: `docs/superpowers/specs/2026-07-14-metrics-panel-uptime-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: `uptime_seconds` in /api/metrics

**Goal:** The metrics response carries process uptime in whole seconds.

**Files:**
- Modify: `internal/api/metrics.go` (package var; `metricsResponse`; handler)
- Modify: `internal/api/metrics_test.go` (extend `TestMetricsHandler`)

**Acceptance Criteria:**
- [ ] Response JSON has top-level `uptime_seconds` as a number ≥ 0
- [ ] `go test ./internal/api/ -count=1` passes

**Verify:** `go test ./internal/api/ -run TestMetricsHandler -count=1 -v` → PASS

**Steps:**

- [ ] **Step 1: Extend the test (failing first)**

In `internal/api/metrics_test.go`, at the end of `TestMetricsHandler`, add:

```go
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	up, ok := raw["uptime_seconds"].(float64)
	if !ok || up < 0 {
		t.Fatalf("uptime_seconds = %v (present=%v), want number >= 0", raw["uptime_seconds"], ok)
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestMetricsHandler -v`
Expected: FAIL — `uptime_seconds = <nil> (present=false)`.

- [ ] **Step 3: Implement**

In `internal/api/metrics.go`, add below the imports:

```go
// processStart approximates process start (package init happens within
// milliseconds of it); /api/metrics reports uptime relative to this.
var processStart = time.Now()
```

Add `"time"` to the imports. Extend `metricsResponse`:

```go
type metricsResponse struct {
	OBS           obsSection     `json:"obs"`
	Limiter       limiterSection `json:"limiter"`
	WorkingSet    wsSection      `json:"working_set"`
	UptimeSeconds int64          `json:"uptime_seconds"`
}
```

In `metricsHandler`, add to the encoded literal (after `WorkingSet:`):

```go
			UptimeSeconds: int64(time.Since(processStart).Seconds()),
```

and extend the handler doc comment's enumeration with "process uptime".

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -count=1 && go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go
git commit -s -m "feat(api): report process uptime_seconds in /api/metrics"
```

---

### Task 2: header uptime + width caps in MetricsPanel

**Goal:** Header shows `up <uptime> · updated Ns ago`; window rows and endpoint table stop stretching across wide columns.

**Files:**
- Modify: `frontend/src/types/metrics.ts` (one field)
- Modify: `frontend/src/components/MetricsPanel.vue` (helper, header span, two class additions)

**Acceptance Criteria:**
- [ ] `MetricsSnapshot` has `uptime_seconds: number`
- [ ] Header right side renders `up 3d 4h · updated 12s ago` once data exists (uptime frozen with the age label while collapsed)
- [ ] `fmtUptime`: 274680s → `3d 4h`; 18720s → `5h 12m`; 720s → `12m`; 45s → `45s`; negative → `0s`
- [ ] Window-rows container has `max-w-[200px]`; endpoint-table container has `max-w-[560px]`
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Type field**

In `frontend/src/types/metrics.ts`, add to `MetricsSnapshot` (top level, after `working_set`):

```ts
  uptime_seconds: number
```

- [ ] **Step 2: Helper**

In `frontend/src/components/MetricsPanel.vue` `<script setup>`, after the `fmt` helper, add:

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

- [ ] **Step 3: Header span**

Replace:

```html
      <span v-if="updatedLabel" class="ml-auto text-[11px] text-text-muted">{{ updatedLabel }}</span>
```

with:

```html
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
```

(`data` and `fetchedAt` are set together on every successful fetch, so `updatedLabel` is always non-empty when `data` exists.)

- [ ] **Step 4: Width caps**

Change the window-rows container from:

```html
            <div class="mt-2">
```

to:

```html
            <div class="mt-2 max-w-[200px]">
```

and the endpoint-table container from:

```html
          <div v-if="endpoints.length" style="columns: 2; column-gap: 32px;">
```

to:

```html
          <div v-if="endpoints.length" class="max-w-[560px]" style="columns: 2; column-gap: 32px;">
```

- [ ] **Step 5: Build check**

Run: `cd frontend && npm run build`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/types/metrics.ts frontend/src/components/MetricsPanel.vue
git commit -s -m "feat(overview): uptime in metrics-panel header; cap breakdown row widths"
```
