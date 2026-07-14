# Backend-Metrics UI Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A collapsed "Backend metrics" panel at the bottom of the Overview tab showing everything `/api/metrics` returns — refreshed every 30 s while expanded — plus new backend request-count windows for the last 6h/12h/24h.

**Architecture:** Backend: `obsMetrics` gains a second ring (288 × 5-minute buckets, 24 h) alongside the 60 × 1 s one; `Client.WindowCounts()` sums trailing 6/12/24 h; the API's `obs` section gains a `windows` map. Frontend: a `useMetrics(active)` polling composable and a self-contained `MetricsPanel.vue` (mockup-A layout) appended to `OverviewPanel.vue` with one import + one tag.

**Tech Stack:** Go (existing ring pattern, fake-clock tests), Vue 3 `<script setup>` + TypeScript + Tailwind tokens (light/dark free), plain `fetch` + `setInterval` polling.

**User decisions (already made):**
- Panel at the bottom of the Overview tab, **collapsed by default**.
- **Poll only while expanded**: immediate fetch on expand, then every 30 s; stop on collapse/unmount.
- Expanded layout: **mockup option A** (three groups + endpoint table). Rejected: dense key-value layout, live collapsed-header summary.
- Windowed counts via **in-memory 5-minute-bucket 24 h ring**; restart resets accepted ("looks good" to the caveat). Rejected: SQLite-backed history.

Spec: `docs/superpowers/specs/2026-07-14-metrics-ui-panel-design.md`

**Conventions:** backend commands run from `/home/rdias/Work/percona-obs-dashboard/backend`, frontend from `frontend/`. Commits: `git commit -s`, never a `Co-Authored-By:` trailer.

---

### Task 1: 24 h window ring + `WindowCounts()`

**Goal:** `obsMetrics` counts requests into 288 five-minute buckets; `Client.WindowCounts()` reports trailing 6 h / 12 h / 24 h totals.

**Files:**
- Modify: `internal/obs/client.go:16-52` (`obsMetrics` struct, `inc`, new method) and `:121-125` (after `RatePerSecond`)
- Modify: `internal/obs/metrics_test.go` (append tests)

**Acceptance Criteria:**
- [ ] Hits 11 h old count toward 12 h and 24 h but not 6 h; hits 23 h old only toward 24 h; hits 24 h+5 m old toward none
- [ ] A bucket reused exactly 24 h later (same `period % 288` slot) is zeroed first
- [ ] Zero traffic → all three windows 0
- [ ] `go test ./internal/obs/ -count=1` passes

**Verify:** `go test ./internal/obs/ -run TestWindowCounts -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/obs/metrics_test.go`:

```go
func TestWindowCounts(t *testing.T) {
	base := time.Unix(3_000_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	h6, h12, h24 := m.windowCounts()
	if h6 != 0 || h12 != 0 || h24 != 0 {
		t.Fatalf("no traffic: got %d/%d/%d, want zeros", h6, h12, h24)
	}

	hitsAt := func(at time.Time, n int) {
		cur = at
		for i := 0; i < n; i++ {
			m.inc("op")
		}
	}
	// 9 hits just outside 24h, 7 at 23h, 5 at 11h, 3 now.
	hitsAt(base.Add(-24*time.Hour-5*time.Minute), 9)
	hitsAt(base.Add(-23*time.Hour), 7)
	hitsAt(base.Add(-11*time.Hour), 5)
	hitsAt(base, 3)
	cur = base

	h6, h12, h24 = m.windowCounts()
	if h6 != 3 {
		t.Errorf("6h = %d, want 3", h6)
	}
	if h12 != 8 {
		t.Errorf("12h = %d, want 8 (3 now + 5 at 11h)", h12)
	}
	if h24 != 15 {
		t.Errorf("24h = %d, want 15 (3 + 5 + 7; the 24h+5m batch excluded)", h24)
	}
}

func TestWindowCountsBucketReuse(t *testing.T) {
	base := time.Unix(3_100_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	m.inc("op")
	cur = base.Add(24 * time.Hour) // same ring slot (period+288), new period
	m.inc("op")

	h6, _, h24 := m.windowCounts()
	if h6 != 1 || h24 != 1 {
		t.Fatalf("stale bucket must be zeroed on reuse: 6h=%d 24h=%d, want 1/1", h6, h24)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/obs/ -run TestWindowCounts -v`
Expected: compile error — `m.windowCounts undefined` / `unknown fields`.

- [ ] **Step 3: Implement**

In `internal/obs/client.go`, extend the `obsMetrics` struct (lines 16-25) to:

```go
// obsMetrics counts OBS requests by operation label, into a ring of
// per-second buckets for the trailing-minute request rate, and into a ring
// of 5-minute buckets for the trailing 6h/12h/24h window totals.
type obsMetrics struct {
	mu     sync.Mutex
	counts map[string]int64
	now    func() time.Time

	ringSec  [60]int64 // unix second each bucket currently holds
	ringHits [60]int64 // request count observed within that second

	winPeriod [288]int64 // 5-minute period id (unixSec/300) each bucket holds
	winHits   [288]int64 // request count observed within that period
}
```

Extend `inc` (before the final `m.mu.Unlock()`):

```go
	p := sec / 300
	j := p % 288
	if m.winPeriod[j] != p {
		m.winPeriod[j] = p
		m.winHits[j] = 0
	}
	m.winHits[j]++
```

so the full function reads:

```go
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
	p := sec / 300
	j := p % 288
	if m.winPeriod[j] != p {
		m.winPeriod[j] = p
		m.winHits[j] = 0
	}
	m.winHits[j]++
	m.mu.Unlock()
}
```

After `ratePerSecond` (line ~52), add:

```go
// windowCounts returns request totals over the trailing 6h, 12h and 24h at
// 5-minute bucket precision. Windows cover at most process uptime.
func (m *obsMetrics) windowCounts() (h6, h12, h24 int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.now().Unix() / 300
	for i := range m.winPeriod {
		p := m.winPeriod[i]
		if p <= cur-288 { // outside 24h (also skips never-used zero buckets)
			continue
		}
		hits := m.winHits[i]
		h24 += hits
		if p > cur-144 {
			h12 += hits
		}
		if p > cur-72 {
			h6 += hits
		}
	}
	return h6, h12, h24
}
```

After `RatePerSecond` (line ~125), add:

```go
// WindowCounts returns OBS request totals over the trailing 6h, 12h and
// 24h (5-minute bucket precision; covers at most process uptime).
func (c *Client) WindowCounts() (h6, h12, h24 int64) {
	return c.metrics.windowCounts()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/obs/ -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/obs/client.go internal/obs/metrics_test.go
git commit -s -m "feat(obs): trailing 6h/12h/24h request counts via 5-minute ring"
```

---

### Task 2: `obs.windows` in the API response

**Goal:** `/api/metrics` reports the three window totals under `obs.windows`.

**Files:**
- Modify: `internal/api/metrics.go:20-24` (obsSection) and `:52-57` (handler)
- Modify: `internal/api/metrics_test.go` (extend `TestMetricsHandler`)

**Acceptance Criteria:**
- [ ] Response contains `obs.windows` with exactly the keys `6h`, `12h`, `24h`
- [ ] No-traffic case: all three values 0 (map present, not null)
- [ ] `go test ./internal/api/ -count=1` passes

**Verify:** `go test ./internal/api/ -run TestMetricsHandler -count=1 -v` → PASS

**Steps:**

- [ ] **Step 1: Extend the test (failing first)**

In `internal/api/metrics_test.go`, inside `TestMetricsHandler` after the existing `got.OBS.ByEndpoint == nil` check, add:

```go
	if got.OBS.Windows == nil {
		t.Fatalf("obs.windows must be a map, got null")
	}
	for _, k := range []string{"6h", "12h", "24h"} {
		if v, ok := got.OBS.Windows[k]; !ok || v != 0 {
			t.Fatalf("obs.windows[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestMetricsHandler -v`
Expected: compile error — `got.OBS.Windows undefined`.

- [ ] **Step 3: Implement**

In `internal/api/metrics.go`, extend `obsSection`:

```go
type obsSection struct {
	Total      int64            `json:"total"`
	ByEndpoint map[string]int64 `json:"by_endpoint"`
	ReqPerS    float64          `json:"req_per_s"`
	Windows    map[string]int64 `json:"windows"` // trailing counts: "6h", "12h", "24h"
}
```

In `metricsHandler`, before the encode add:

```go
		h6, h12, h24 := obsClient.WindowCounts()
```

and extend the `OBS:` literal:

```go
			OBS: obsSection{
				Total:      total,
				ByEndpoint: byEndpoint,
				ReqPerS:    obsClient.RatePerSecond(),
				Windows:    map[string]int64{"6h": h6, "12h": h12, "24h": h24},
			},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -count=1 && go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go
git commit -s -m "feat(api): trailing 6h/12h/24h request windows in /api/metrics"
```

---

### Task 3: frontend — types, `useMetrics`, `MetricsPanel`

**Goal:** The collapsed panel at the bottom of the Overview tab, polling `/api/metrics` every 30 s while expanded, rendering the mockup-A layout.

**Files:**
- Create: `frontend/src/types/metrics.ts`
- Create: `frontend/src/composables/useMetrics.ts`
- Create: `frontend/src/components/MetricsPanel.vue`
- Modify: `frontend/src/components/OverviewPanel.vue` (one import; one tag after `<CveExposureTable …/>`)

**Acceptance Criteria:**
- [ ] Panel renders collapsed by default at the bottom of the Overview tab; clicking the header expands/collapses
- [ ] Expanding fetches immediately, then every 30 s; collapsing or unmounting clears the interval (no polling while collapsed)
- [ ] Expanded body shows the three groups (OBS requests with total/rate/window rows; limiter with meter or "disabled"; working set with chips) and the sorted two-column endpoint table ("no requests yet" when empty)
- [ ] "updated Ns ago" appears in the header once data exists; error line shows on fetch failure while stale data stays rendered
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → vue-tsc no errors, exit 0

**Steps:**

- [ ] **Step 1: Create the types**

Create `frontend/src/types/metrics.ts`:

```ts
export interface MetricsSnapshot {
  obs: {
    total: number
    by_endpoint: Record<string, number>
    req_per_s: number
    windows: Record<string, number> // keys "6h" | "12h" | "24h"
  }
  limiter: { enabled: boolean; budget: number; remaining: number; waits: number }
  working_set: { packages: number; inflight: number; by_state: Record<string, number> }
}
```

- [ ] **Step 2: Create the composable**

Create `frontend/src/composables/useMetrics.ts`:

```ts
import { ref, watch, onUnmounted, type Ref } from 'vue'
import type { MetricsSnapshot } from '../types/metrics'

const REFRESH_MS = 30_000

// Polls /api/metrics while `active` is true: immediate fetch on activation,
// then every 30s. A failed fetch keeps the previous data (shown stale with
// its age) and retries on the next tick.
export function useMetrics(active: Ref<boolean>) {
  const data = ref<MetricsSnapshot | null>(null)
  const error = ref<string | null>(null)
  const fetchedAt = ref<Date | null>(null)

  async function refresh() {
    try {
      const res = await fetch('/api/metrics')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json() as MetricsSnapshot
      fetchedAt.value = new Date()
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'failed to fetch metrics'
    }
  }

  let timer: ReturnType<typeof setInterval> | null = null
  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }
  watch(active, (on) => {
    stop()
    if (on) {
      refresh()
      timer = setInterval(refresh, REFRESH_MS)
    }
  }, { immediate: true })
  onUnmounted(stop)

  return { data, error, fetchedAt }
}
```

- [ ] **Step 3: Create the panel component**

Create `frontend/src/components/MetricsPanel.vue`:

```vue
<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useMetrics } from '../composables/useMetrics'

const expanded = ref(false)
const { data, error, fetchedAt } = useMetrics(expanded)

// "updated Ns ago" ticks only while expanded; the last text stays frozen
// when collapsed.
const nowTick = ref(Date.now())
let ageTimer: ReturnType<typeof setInterval> | null = null
watch(expanded, (on) => {
  if (ageTimer) {
    clearInterval(ageTimer)
    ageTimer = null
  }
  if (on) ageTimer = setInterval(() => { nowTick.value = Date.now() }, 1000)
}, { immediate: true })
onUnmounted(() => { if (ageTimer) clearInterval(ageTimer) })

const updatedLabel = computed(() => {
  if (!fetchedAt.value) return ''
  const s = Math.max(0, Math.round((nowTick.value - fetchedAt.value.getTime()) / 1000))
  return `updated ${s}s ago`
})

const endpoints = computed(() =>
  Object.entries(data.value?.obs.by_endpoint ?? {}).sort((a, b) => b[1] - a[1]))

const states = computed(() =>
  Object.entries(data.value?.working_set.by_state ?? {}).sort((a, b) => b[1] - a[1]))

const WINDOW_KEYS = ['6h', '12h', '24h'] as const

const fmt = (n: number) => n.toLocaleString('en-US')
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
    <button
      type="button"
      class="w-full flex items-center gap-2 p-[12px_20px] text-left"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <span class="text-text-muted text-[11px] transition-transform inline-block" :class="expanded ? 'rotate-90' : ''">▸</span>
      <span class="text-[11.5px] font-bold uppercase tracking-[0.05em] text-text-muted">Backend metrics</span>
      <span v-if="updatedLabel" class="ml-auto text-[11px] text-text-muted">{{ updatedLabel }}</span>
    </button>

    <div v-if="expanded" class="border-t border-border p-[14px_20px_16px]">
      <div v-if="error" class="text-[12px] text-text-muted pb-2">failed to refresh: {{ error }}</div>
      <template v-if="data">
        <div class="grid grid-cols-1 min-[760px]:grid-cols-3 gap-6">
          <!-- OBS requests -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">OBS requests</div>
            <div class="text-[22px] font-extrabold tabular-nums leading-none">{{ fmt(data.obs.total) }}</div>
            <div class="text-[11px] text-text-muted mt-1">
              total since start · <b class="text-text-secondary">{{ data.obs.req_per_s.toFixed(1) }}</b> req/s last minute
            </div>
            <div class="mt-2">
              <div v-for="k in WINDOW_KEYS" :key="k" class="flex justify-between text-[12.5px] py-[2px]">
                <span class="text-text-muted">last {{ k }}</span>
                <span class="font-mono tabular-nums font-semibold">{{ fmt(data.obs.windows?.[k] ?? 0) }}</span>
              </div>
            </div>
          </div>
          <!-- Rate limiter -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Rate limiter</div>
            <template v-if="data.limiter.enabled">
              <div class="text-[22px] font-extrabold tabular-nums leading-none">
                {{ data.limiter.remaining }}<span class="text-[13px] text-text-muted font-semibold"> / {{ data.limiter.budget }}</span>
              </div>
              <div class="text-[11px] text-text-muted mt-1">
                remaining this minute · <b class="text-text-secondary">{{ fmt(data.limiter.waits) }}</b> waits total
              </div>
              <div class="h-[6px] bg-bg-muted rounded-[4px] overflow-hidden mt-2">
                <div
                  class="h-full rounded-[4px]"
                  :style="{ width: `${(data.limiter.remaining / Math.max(1, data.limiter.budget)) * 100}%`, background: 'var(--brand-purple)' }"
                />
              </div>
            </template>
            <div v-else class="text-[12.5px] text-text-muted">disabled</div>
          </div>
          <!-- Working set -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Working set</div>
            <div class="text-[22px] font-extrabold tabular-nums leading-none">{{ fmt(data.working_set.packages) }}</div>
            <div class="text-[11px] text-text-muted mt-1">
              packages · <b class="text-text-secondary">{{ data.working_set.inflight }}</b> in flight
            </div>
            <div class="flex gap-[5px] flex-wrap mt-2">
              <span
                v-for="[state, n] in states"
                :key="state"
                class="text-[10.5px] font-bold px-2 py-[2px] rounded-[6px] bg-bg-muted text-text-secondary tabular-nums"
              >{{ fmt(n) }} {{ state }}</span>
            </div>
          </div>
        </div>
        <!-- Endpoint table -->
        <div class="mt-4">
          <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Requests by endpoint</div>
          <div v-if="endpoints.length" style="columns: 2; column-gap: 32px;">
            <div v-for="[op, n] in endpoints" :key="op" class="flex justify-between font-mono text-[12px] py-[1px] break-inside-avoid">
              <span class="text-text-muted">{{ op }}</span>
              <span class="tabular-nums">{{ fmt(n) }}</span>
            </div>
          </div>
          <div v-else class="text-[12px] text-text-muted">no requests yet</div>
        </div>
      </template>
      <div v-else-if="!error" class="text-[12px] text-text-muted">loading…</div>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Wire into OverviewPanel**

In `frontend/src/components/OverviewPanel.vue`, add to the component imports:

```ts
import MetricsPanel from './MetricsPanel.vue'
```

and in the template, immediately after the `<CveExposureTable :projects="projects" :accent-of="accentOf" />` line, add:

```html
      <MetricsPanel />
```

No other OverviewPanel changes.

- [ ] **Step 5: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, vite build exit 0.

- [ ] **Step 6: Visual check (needs a running stack; skip if port 4000 is held by prod)**

`task dev` → Overview tab: panel collapsed at the bottom; expand → values match `curl -s localhost:4000/api/metrics | jq`; age label ticks; network tab shows a request on expand + every 30 s, none while collapsed; dark theme renders; limiter-disabled shows "disabled" when `minute_request_budget: 0`.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/types/metrics.ts frontend/src/composables/useMetrics.ts frontend/src/components/MetricsPanel.vue frontend/src/components/OverviewPanel.vue
git commit -s -m "feat(overview): collapsible backend-metrics panel polling /api/metrics"
```
