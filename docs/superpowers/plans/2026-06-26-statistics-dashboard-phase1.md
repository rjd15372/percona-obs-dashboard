# Statistics Dashboard — Phase 1 (Foundation + Build Health) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the Statistics tab end-to-end for the first area — a context/version/range-scoped Build Health view — establishing the foundation (tab, selectors, ECharts, TTL stats cache, per-context endpoint) that Phases 2–4 build on.

**Architecture:** A new `statistics` tab (context + optional-version + time-range selectors) renders a Build Health section of stat cards + ECharts charts, fed by a new per-context `/stats/build-health` Go endpoint that aggregates `events`/`packages` via SQL and is wrapped in a TTL `statsCache` mirroring the existing `binaryListCache`. Charts use a thin ECharts wrapper themed to the app's CSS variables.

**Tech Stack:** Go (chi, SQLite, `database/sql`), Vue 3 SFC + TypeScript, Tailwind, Vite, ECharts.

**User decisions (already made):**
- Statistics scoped to a selected context + optional version ("All versions" default) + range (7d/30d/90d/All, default 30d); NO cross-context aggregation.
- Within-context breakdowns by repo·arch (and package), not by context.
- ECharts for charts, themed to the app's light/dark CSS variables.
- Per-context backend endpoints with a TTL cache (no recompute within TTL); follow the existing cache pattern.
- Build in phases; this plan is Phase 1 (foundation + Build Health). Phases 2–4 (Performance, CVE, Activity) are separate later plans.

**Build Health response shape (shared backend↔frontend contract):**
```json
{
  "passRate": 0.942,
  "failingNow": 7,
  "packagesTracked": 142,
  "outcomesOverTime": [{ "date": "2026-06-01", "succeeded": 30, "failed": 2, "broken": 0 }],
  "topFailingPackages": [{ "package": "pxc-8.4", "total": 20, "segments": [{ "repoArch": "el9·x86_64", "count": 12 }] }],
  "passRateByRepoArch": [{ "repoArch": "el9·x86_64", "passRate": 0.9, "total": 50 }]
}
```

---

### Task 1: statsCache (TTL cache for stats JSON)

**Goal:** Add a TTL cache storing pre-marshalled stats JSON, mirroring the existing `binaryListCache`, and wire it into the router.

**Files:**
- Create: `backend/internal/api/stats_cache.go`
- Create: `backend/internal/api/stats_cache_test.go`
- Modify: `backend/internal/api/server.go` (construct the cache)

**Acceptance Criteria:**
- [ ] `statsCache.Get(ctx, key, fetch)` returns cached bytes when within TTL and only calls `fetch` on miss/expiry.
- [ ] Concurrent calls for the same key run `fetch` once (single-flight), like `binaryListCache`.
- [ ] A test proves a second call within the TTL does not invoke `fetch` again.

**Verify:** `cd backend && go test ./internal/api/ -run TestStatsCache -v` → PASS.

**Steps:**

- [ ] **Step 1: Write the cache** — `backend/internal/api/stats_cache.go`:

```go
package api

import (
	"context"
	"sync"
	"time"
)

// statsCache caches pre-marshalled stats JSON per key for a TTL.
// Mirrors binaryListCache: mutex-guarded entries + single-flight inflight map.
type statsCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]statsCacheEntry
	inflight map[string]chan struct{}
}

type statsCacheEntry struct {
	data    []byte
	expires time.Time
	err     error
}

func newStatsCache(ttl time.Duration) *statsCache {
	return &statsCache{
		ttl:      ttl,
		entries:  map[string]statsCacheEntry{},
		inflight: map[string]chan struct{}{},
	}
}

func (c *statsCache) Get(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.data, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.data, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	data, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = statsCacheEntry{data: data, expires: expires, err: err}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return data, err
}
```

- [ ] **Step 2: Test** — `backend/internal/api/stats_cache_test.go`:

```go
package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatsCacheHitWithinTTL(t *testing.T) {
	c := newStatsCache(time.Minute)
	var calls int32
	fetch := func(context.Context) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"ok":true}`), nil
	}
	for i := 0; i < 3; i++ {
		got, err := c.Get(context.Background(), "k", fetch)
		if err != nil || string(got) != `{"ok":true}` {
			t.Fatalf("unexpected: %s %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("fetch called %d times, want 1", calls)
	}
}

func TestStatsCacheRecomputesAfterExpiry(t *testing.T) {
	c := newStatsCache(0) // expires immediately
	var calls int32
	fetch := func(context.Context) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte("x"), nil
	}
	_, _ = c.Get(context.Background(), "k", fetch)
	_, _ = c.Get(context.Background(), "k", fetch)
	if calls != 2 {
		t.Fatalf("fetch called %d times, want 2", calls)
	}
}
```

- [ ] **Step 3: Wire into server.go** — in `NewRouter` (after the other cache constructions, ~line 21) add:

```go
statsCache := newStatsCache(60 * time.Second)
```

(Matches the existing pattern of constructing caches with a literal TTL here. `statsCache` is passed to the stats handlers added in Task 2.)

- [ ] **Step 4: Run tests & commit**

```bash
cd backend && go test ./internal/api/ -run TestStatsCache -v   # expect PASS
git add backend/internal/api/stats_cache.go backend/internal/api/stats_cache_test.go backend/internal/api/server.go
git commit -s -m "feat(backend): add TTL statsCache for aggregation results"
```

---

### Task 2: Build Health aggregation endpoint

**Goal:** Add per-context `/stats/build-health` endpoints that aggregate `events`/`packages` into the Build Health response shape, scoped by prefix + version + range, served through `statsCache`, with table tests.

**Files:**
- Create: `backend/internal/store/stats.go` (aggregation queries)
- Create: `backend/internal/store/stats_test.go` (table tests on a temp SQLite DB)
- Create: `backend/internal/api/stats.go` (response types, range parsing, handler)
- Modify: `backend/internal/api/server.go` (register routes)

**Acceptance Criteria:**
- [ ] `GET /api/products/{product}/all/stats/build-health?range=30d` returns the documented JSON with correct `passRate`, `failingNow`, `packagesTracked`, `outcomesOverTime`, `topFailingPackages` (top-N packages, each with repo·arch `segments` ordered by count), and `passRateByRepoArch`.
- [ ] `version` path segment `all` skips version filtering; a concrete version filters to it.
- [ ] `range` ∈ `7d|30d|90d|all` controls the time window (`all` = no lower bound).
- [ ] Equivalent PR (`/api/pr/{pr}/{subproject}/{version}/stats/build-health`) and releases (`/api/releases/ppg/{version}/stats/build-health`) routes exist and resolve the right prefix.
- [ ] Store table tests assert each aggregation against a seeded temp DB.

**Verify:** `cd backend && go test ./internal/store/ -run TestBuildHealth -v && go test ./internal/api/ -run TestStats -v` → PASS.

**Steps:**

- [ ] **Step 1: Aggregation queries** — `backend/internal/store/stats.go`. Use the existing `events` and `packages` schema (events: `type,project,package,repo,arch,at,version`; packages: `rollup_state,project,version`). `prefixLike := prefix + "%"`. When `version != "all"`, add `AND version = ?`. `from`/`to` are RFC3339 strings; `from==""` means no lower bound (range=all).

```go
package store

import (
	"database/sql"
	"strings"
)

type OutcomeDay struct {
	Date      string `json:"date"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Broken    int    `json:"broken"`
}

type RepoArchSegment struct {
	RepoArch string `json:"repoArch"`
	Count    int    `json:"count"`
}

type FailingPackage struct {
	Package  string            `json:"package"`
	Total    int               `json:"total"`
	Segments []RepoArchSegment `json:"segments"`
}

type RepoArchRate struct {
	RepoArch string  `json:"repoArch"`
	PassRate float64 `json:"passRate"`
	Total    int     `json:"total"`
}

// BuildHealthSnapshot returns current pass rate, failing/broken count, and tracked count
// from the packages table (current state, not range-windowed).
func BuildHealthSnapshot(db *sql.DB, prefix, version string) (passRate float64, failingNow, tracked int, err error) {
	q := `SELECT rollup_state, COUNT(*) FROM packages WHERE project LIKE ?`
	args := []any{prefix + "%"}
	if version != "all" {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` GROUP BY rollup_state`
	rows, err := db.Query(q, args...)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	var ok, total int
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return 0, 0, 0, err
		}
		total += n
		switch state {
		case "succeeded", "published", "finished":
			ok += n
		case "failed", "broken", "unresolvable":
			failingNow += n
		}
	}
	tracked = total
	if total > 0 {
		passRate = float64(ok) / float64(total)
	}
	return passRate, failingNow, tracked, rows.Err()
}

// withScope appends the prefix/version/time filters shared by the event aggregations.
func withScope(base, prefix, version, from, to string) (string, []any) {
	q := base + ` WHERE project LIKE ?`
	args := []any{prefix + "%"}
	if version != "all" {
		q += ` AND version = ?`
		args = append(args, version)
	}
	if from != "" {
		q += ` AND at >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND at <= ?`
		args = append(args, to)
	}
	return q, args
}

func BuildOutcomesOverTime(db *sql.DB, prefix, version, from, to string) ([]OutcomeDay, error) {
	q, args := withScope(`SELECT substr(at,1,10) AS d, type, COUNT(*) FROM events`, prefix, version, from, to)
	q += ` AND type IN ('succeeded','failed','broken') GROUP BY d, type ORDER BY d`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]*OutcomeDay{}
	var order []string
	for rows.Next() {
		var d, typ string
		var n int
		if err := rows.Scan(&d, &typ, &n); err != nil {
			return nil, err
		}
		if byDay[d] == nil {
			byDay[d] = &OutcomeDay{Date: d}
			order = append(order, d)
		}
		switch typ {
		case "succeeded":
			byDay[d].Succeeded = n
		case "failed":
			byDay[d].Failed = n
		case "broken":
			byDay[d].Broken = n
		}
	}
	out := make([]OutcomeDay, 0, len(order))
	for _, d := range order {
		out = append(out, *byDay[d])
	}
	return out, rows.Err()
}

func TopFailingByRepoArch(db *sql.DB, prefix, version, from, to string, limit int) ([]FailingPackage, error) {
	q, args := withScope(`SELECT package, COALESCE(repo,'') , COALESCE(arch,''), COUNT(*) FROM events`, prefix, version, from, to)
	q += ` AND type IN ('failed','broken') GROUP BY package, repo, arch`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pkgs := map[string]*FailingPackage{}
	var order []string
	for rows.Next() {
		var pkg, repo, arch string
		var n int
		if err := rows.Scan(&pkg, &repo, &arch, &n); err != nil {
			return nil, err
		}
		if pkgs[pkg] == nil {
			pkgs[pkg] = &FailingPackage{Package: pkg}
			order = append(order, pkg)
		}
		ra := strings.TrimSuffix(repo+"·"+arch, "·")
		pkgs[pkg].Segments = append(pkgs[pkg].Segments, RepoArchSegment{RepoArch: ra, Count: n})
		pkgs[pkg].Total += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	all := make([]FailingPackage, 0, len(order))
	for _, p := range order {
		all = append(all, *pkgs[p])
	}
	// sort packages by Total desc, take top `limit`
	sortFailingDesc(all)
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func PassRateByRepoArch(db *sql.DB, prefix, version, from, to string) ([]RepoArchRate, error) {
	q, args := withScope(`SELECT COALESCE(repo,''), COALESCE(arch,''), type, COUNT(*) FROM events`, prefix, version, from, to)
	q += ` AND type IN ('succeeded','failed','broken') GROUP BY repo, arch, type`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type agg struct{ ok, total int }
	m := map[string]*agg{}
	var order []string
	for rows.Next() {
		var repo, arch, typ string
		var n int
		if err := rows.Scan(&repo, &arch, &typ, &n); err != nil {
			return nil, err
		}
		ra := strings.TrimSuffix(repo+"·"+arch, "·")
		if m[ra] == nil {
			m[ra] = &agg{}
			order = append(order, ra)
		}
		m[ra].total += n
		if typ == "succeeded" {
			m[ra].ok += n
		}
	}
	out := make([]RepoArchRate, 0, len(order))
	for _, ra := range order {
		rate := 0.0
		if m[ra].total > 0 {
			rate = float64(m[ra].ok) / float64(m[ra].total)
		}
		out = append(out, RepoArchRate{RepoArch: ra, PassRate: rate, Total: m[ra].total})
	}
	return out, rows.Err()
}

func sortFailingDesc(s []FailingPackage) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Total > s[j-1].Total; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
```

- [ ] **Step 2: Response type, range parsing, handler** — `backend/internal/api/stats.go`:

```go
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

type buildHealthResponse struct {
	PassRate         float64                 `json:"passRate"`
	FailingNow       int                     `json:"failingNow"`
	PackagesTracked  int                     `json:"packagesTracked"`
	OutcomesOverTime []store.OutcomeDay      `json:"outcomesOverTime"`
	TopFailing       []store.FailingPackage  `json:"topFailingPackages"`
	PassRateByRA     []store.RepoArchRate    `json:"passRateByRepoArch"`
}

// statsRange maps ?range=7d|30d|90d|all to an inclusive [from,to] RFC3339 window.
// from=="" means no lower bound (all).
func statsRange(r *http.Request) (from, to string) {
	to = time.Now().UTC().Format(time.RFC3339)
	switch r.URL.Query().Get("range") {
	case "7d":
		from = time.Now().UTC().AddDate(0, 0, -7).Format(time.RFC3339)
	case "90d":
		from = time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339)
	case "all":
		from = ""
	default: // "30d" and unknown
		from = time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	}
	return from, to
}

// buildHealthStatsHandler resolves prefix via prefixFn, reads the version path param,
// and serves the cached Build Health aggregation.
func buildHealthStatsHandler(db *sql.DB, cache *statsCache, prefixFn func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefix := prefixFn(r)
		version := chiVersion(r)
		from, to := statsRange(r)
		rangeKey := r.URL.Query().Get("range")
		if rangeKey == "" {
			rangeKey = "30d"
		}
		key := prefix + ":" + version + ":" + rangeKey + ":build-health"

		data, err := cache.Get(r.Context(), key, func(ctx context.Context) ([]byte, error) {
			passRate, failingNow, tracked, err := store.BuildHealthSnapshot(db, prefix, version)
			if err != nil {
				return nil, err
			}
			outcomes, err := store.BuildOutcomesOverTime(db, prefix, version, from, to)
			if err != nil {
				return nil, err
			}
			top, err := store.TopFailingByRepoArch(db, prefix, version, from, to, 10)
			if err != nil {
				return nil, err
			}
			byRA, err := store.PassRateByRepoArch(db, prefix, version, from, to)
			if err != nil {
				return nil, err
			}
			return json.Marshal(buildHealthResponse{
				PassRate: passRate, FailingNow: failingNow, PackagesTracked: tracked,
				OutcomesOverTime: outcomes, TopFailing: top, PassRateByRA: byRA,
			})
		})
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}
```

Add the small `chiVersion` helper in the same file (chi is already imported elsewhere in the package; import it here):

```go
import "github.com/go-chi/chi/v5"

func chiVersion(r *http.Request) string { return chi.URLParam(r, "version") }
```

- [ ] **Step 3: Register routes** — in `server.go`, add a `/stats/build-health` route to each context group, reusing the prefix expressions already used by the repos/events handlers:

```go
// inside r.Route("/api/products/{product}/{version}", ...)
r.Get("/stats/build-health", buildHealthStatsHandler(db, statsCache, func(r *http.Request) string {
	return "isv:percona:" + chi.URLParam(r, "product")
}))

// inside r.Route("/api/pr/{pr}/{subproject}/{version}", ...)
r.Get("/stats/build-health", buildHealthStatsHandler(db, statsCache, func(r *http.Request) string {
	return "isv:percona:PR:" + chi.URLParam(r, "pr") + ":" + chi.URLParam(r, "subproject")
}))

// inside r.Route("/api/releases/ppg/{version}", ...)
r.Get("/stats/build-health", buildHealthStatsHandler(db, statsCache, func(r *http.Request) string {
	return root + ":ppg:releases"
}))
```

(Add `"github.com/go-chi/chi/v5"` to server.go imports if not present.)

- [ ] **Step 4: Store table tests** — `backend/internal/store/stats_test.go`: open a temp SQLite DB, create the `events` and `packages` tables (copy the `CREATE TABLE` statements from `internal/store/db.go`), insert known rows (a couple of packages with states; events of types succeeded/failed/broken across two dates and two repo·arch combos), then assert:
  - `BuildHealthSnapshot` returns the expected passRate/failingNow/tracked.
  - `BuildOutcomesOverTime` groups by date with correct per-type counts.
  - `TopFailingByRepoArch` returns packages ordered by total desc with correct segments.
  - `PassRateByRepoArch` returns correct per-repo·arch rates.
  - version filter: a query with a concrete version excludes other-version rows.

Follow the existing `internal/store/*_test.go` setup (look at how other store tests open the DB and create schema).

- [ ] **Step 5: Run & commit**

```bash
cd backend && go test ./internal/store/ -run TestBuildHealth -v && go test ./internal/api/ -v
git add backend/internal/store/stats.go backend/internal/store/stats_test.go backend/internal/api/stats.go backend/internal/api/server.go
git commit -s -m "feat(backend): build-health stats aggregation endpoint (per-context, cached)"
```

---

### Task 3: ECharts wrapper + theme

**Goal:** Add `echarts` and a reusable Vue wrapper component + theme module that maps ECharts colors to the app's CSS variables for light/dark and re-applies on theme change.

**Files:**
- Modify: `frontend/package.json` (+ lockfile via install)
- Create: `frontend/src/components/charts/EChart.vue`
- Create: `frontend/src/components/charts/echartsTheme.ts`

**Acceptance Criteria:**
- [ ] `echarts` is a dependency and `npm run build` succeeds with it imported.
- [ ] `<EChart :option="..." />` renders a chart, resizes with its container, and disposes on unmount.
- [ ] The chart reads colors from CSS variables (via `getComputedStyle`) so it matches the active theme, and re-themes when the `data-theme` attribute changes.

**Verify:** `cd frontend && npm install && npm run build` → `✓ built`, no errors.

**Steps:**

- [ ] **Step 1: Install** — `cd frontend && npm install echarts`.

- [ ] **Step 2: Theme module** — `frontend/src/components/charts/echartsTheme.ts`:

```ts
// Reads the app's CSS variables so ECharts matches the active (light/dark) theme.
function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

export function echartsTheme() {
  const text = cssVar('--text-secondary')
  const muted = cssVar('--text-muted')
  const border = cssVar('--border-strong')
  return {
    color: [
      cssVar('--ok'), cssVar('--fail'), cssVar('--warn'),
      cssVar('--info'), cssVar('--brand-purple'), cssVar('--broken'), cssVar('--blocked'),
    ],
    textStyle: { color: text, fontFamily: 'inherit' },
    grid: { left: 40, right: 16, top: 24, bottom: 28, containLabel: true },
    categoryAxis: { axisLine: { lineStyle: { color: border } }, axisLabel: { color: muted }, splitLine: { show: false } },
    valueAxis: { axisLine: { show: false }, axisLabel: { color: muted }, splitLine: { lineStyle: { color: border } } },
    legend: { textStyle: { color: muted } },
    tooltip: { backgroundColor: cssVar('--bg-card'), borderColor: border, textStyle: { color: cssVar('--text-primary') } },
  }
}
```

- [ ] **Step 3: Wrapper component** — `frontend/src/components/charts/EChart.vue`:

```vue
<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'
import * as echarts from 'echarts'
import { echartsTheme } from './echartsTheme'

const props = defineProps<{ option: echarts.EChartsCoreOption }>()

const el = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let ro: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null

function render() {
  if (!chart) return
  // Re-create with the current theme palette, then apply the option.
  const opt = props.option
  chart.setOption({ ...echartsTheme(), ...opt } as echarts.EChartsCoreOption, true)
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value)
  render()
  ro = new ResizeObserver(() => chart?.resize())
  ro.observe(el.value)
  // Re-theme when the app toggles data-theme on <html>.
  themeObserver = new MutationObserver(render)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
})

watch(() => props.option, render, { deep: true })

onBeforeUnmount(() => {
  ro?.disconnect()
  themeObserver?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div ref="el" class="w-full h-full min-h-[180px]"></div>
</template>
```

- [ ] **Step 4: Build & commit**

```bash
cd frontend && npm install && npm run build   # expect ✓ built
git add frontend/package.json frontend/package-lock.json frontend/src/components/charts/
git commit -s -m "feat(frontend): add ECharts wrapper and theme module"
```

---

### Task 4: Statistics tab scaffolding (tab, selectors, composable, types)

**Goal:** Add the `statistics` tab with context + version ("All versions" default) + range selectors, URL persistence, a `useStatistics` composable, and the shared TS types — rendering an empty `StatisticsPanel` shell (sections added in Task 5+).

**Files:**
- Modify: `frontend/src/App.vue` (mainTab type + render block + pass contexts/versions)
- Modify: `frontend/src/components/AppHeader.vue` (Statistics pill)
- Modify: `frontend/src/composables/useUrlState.ts` (accept `tab=statistics` + stats params)
- Modify: `frontend/src/types/api.ts` (add `BuildHealthStats` and sub-types)
- Create: `frontend/src/components/StatisticsPanel.vue`
- Create: `frontend/src/composables/useStatistics.ts`

**Acceptance Criteria:**
- [ ] A "Statistics" pill appears in the header; clicking it shows the StatisticsPanel; `?tab=statistics` persists across reload.
- [ ] StatisticsPanel shows a context selector, a version selector with an "All versions" default option, and a 7d/30d/90d/All range switcher; changing any updates state (and URL `?statsCtx/statsVersion/statsRange`).
- [ ] `useStatistics('build-health')` fetches `${apiBase}/${version}/stats/build-health?range=${range}` and exposes `data/loading/error`, refetching when context/version/range change.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Types** — append to `frontend/src/types/api.ts`:

```ts
export interface OutcomeDay { date: string; succeeded: number; failed: number; broken: number }
export interface RepoArchSegment { repoArch: string; count: number }
export interface FailingPackage { package: string; total: number; segments: RepoArchSegment[] }
export interface RepoArchRate { repoArch: string; passRate: number; total: number }
export interface BuildHealthStats {
  passRate: number
  failingNow: number
  packagesTracked: number
  outcomesOverTime: OutcomeDay[]
  topFailingPackages: FailingPackage[]
  passRateByRepoArch: RepoArchRate[]
}
```

- [ ] **Step 2: `useStatistics` composable** — `frontend/src/composables/useStatistics.ts`:

```ts
import { ref, watch, type Ref } from 'vue'

export function useStatistics<T>(area: string, apiBase: Ref<string>, version: Ref<string>, range: Ref<string>) {
  const data = ref<T | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      const res = await fetch(`${apiBase.value}/${version.value}/stats/${area}?range=${range.value}`)
      if (!res.ok) throw new Error(res.statusText)
      data.value = await res.json() as T
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'failed to load'
      data.value = null
    } finally {
      loading.value = false
    }
  }

  watch([apiBase, version, range], refresh, { immediate: true })
  return { data, loading, error, refresh }
}
```

- [ ] **Step 3: StatisticsPanel shell** — `frontend/src/components/StatisticsPanel.vue` (selectors + range switcher; the Build Health section is added in Task 5):

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { Context } from '../types/api'

const props = defineProps<{
  contexts: Context[]
  selectedContext: Context
  availableVersions: string[]
  version: string        // 'all' or a specific version
  range: string          // '7d' | '30d' | '90d' | 'all'
}>()
const emit = defineEmits<{
  'update:context': [ctx: Context]
  'update:version': [v: string]
  'update:range': [r: string]
}>()

const ranges = ['7d', '30d', '90d', 'all'] as const
const apiBase = computed(() => props.selectedContext.apiBase)
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- control row -->
    <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
      <select
        class="font-mono text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px] border border-border cursor-pointer [appearance:auto]"
        :value="selectedContext.apiBase"
        @change="emit('update:context', contexts.find(c => c.apiBase === ($event.target as HTMLSelectElement).value)!)"
      >
        <option v-for="c in contexts" :key="c.apiBase" :value="c.apiBase">{{ c.label }}</option>
      </select>

      <select
        class="font-mono text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px] border border-border cursor-pointer [appearance:auto]"
        :value="version"
        @change="emit('update:version', ($event.target as HTMLSelectElement).value)"
      >
        <option value="all">All versions</option>
        <option v-for="v in availableVersions" :key="v" :value="v">{{ v }}</option>
      </select>

      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border ml-auto">
        <button
          v-for="r in ranges"
          :key="r"
          class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
          :class="range === r
            ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
            : 'bg-transparent text-text-muted font-medium border-transparent'"
          @click="emit('update:range', r)"
        >{{ r === 'all' ? 'All' : r }}</button>
      </div>
    </div>

    <!-- Build Health section is inserted here in Task 5 -->
    <BuildHealthSection :api-base="apiBase" :version="version" :range="range" />
  </div>
</template>
```

(Note: the `<BuildHealthSection>` tag is added/imported in Task 5; for this task either leave a placeholder `<div>` and swap it in Task 5, or create an empty `BuildHealthSection.vue` stub now. Use a placeholder `<div class="text-text-muted text-[13px]">Build Health — coming up</div>` for Task 4's commit to keep the build green, replaced in Task 5.)

- [ ] **Step 4: AppHeader pill** — in `frontend/src/components/AppHeader.vue`, add a third pill inside `.tab-switcher` (after Artifacts):

```html
<button class="tab-pill" :class="{ active: mainTab === 'statistics' }" @click="emit('update:main-tab', 'statistics')">Statistics</button>
```

and widen the prop/emit types from `'board' | 'artifacts'` to `'board' | 'artifacts' | 'statistics'`.

- [ ] **Step 5: App.vue wiring** — (a) change `const mainTab = ref<'board' | 'artifacts'>('board')` → `ref<'board' | 'artifacts' | 'statistics'>('board')`; (b) add stats state: `const statsContext = ref<Context>(PPG_CONTEXT)`, `const statsVersion = ref('all')`, `const statsRange = ref('30d')`; (c) add the render block after the artifacts block:

```vue
<StatisticsPanel
  v-else-if="mainTab === 'statistics'"
  :contexts="artifactsContexts"
  :selected-context="statsContext"
  :available-versions="availableVersions"
  :version="statsVersion"
  :range="statsRange"
  @update:context="statsContext = $event"
  @update:version="statsVersion = $event"
  @update:range="statsRange = $event"
/>
```

Import `StatisticsPanel`. (Reuse `artifactsContexts` for the context list and `availableVersions` already computed in App.vue.) Wire these three refs into the `useUrlState({...})` call.

- [ ] **Step 6: useUrlState** — in `frontend/src/composables/useUrlState.ts`: accept `'statistics'` in the `tab` guard (`if (tab === 'board' || tab === 'artifacts' || tab === 'statistics') mainTab.value = tab`); add `statsCtx`/`statsVersion`/`statsRange` to the params it reads and writes, following the existing pattern for the other state refs.

- [ ] **Step 7: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/App.vue frontend/src/components/AppHeader.vue frontend/src/components/StatisticsPanel.vue frontend/src/composables/useUrlState.ts frontend/src/composables/useStatistics.ts frontend/src/types/api.ts
git commit -s -m "feat(frontend): statistics tab scaffolding (selectors, url state, composable)"
```

---

### Task 5: Build Health section

**Goal:** Build the Build Health section — stat cards + three ECharts charts — fed by `useStatistics('build-health')`, and render it in `StatisticsPanel`.

**Files:**
- Create: `frontend/src/components/stats/BuildHealthSection.vue`
- Modify: `frontend/src/components/StatisticsPanel.vue` (import + render the section, replacing the placeholder)

**Acceptance Criteria:**
- [ ] Renders three stat cards (pass rate %, failing/broken now, packages tracked) from `BuildHealthStats`.
- [ ] Renders: build-outcomes-over-time as a stacked area (succeeded/failed/broken), top-failing-packages as a horizontal stacked bar (segments = repo·arch), and pass-rate-by-repo·arch as a bar — all via `<EChart>`.
- [ ] Loading and empty/error states render without breaking the tab.
- [ ] Charts respond to context/version/range changes (re-fetch via the composable) and to light/dark theme.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`; manual: open the Statistics tab, confirm cards + 3 charts render and update on range change, in light + dark.

**Steps:**

- [ ] **Step 1: Section component** — `frontend/src/components/stats/BuildHealthSection.vue`:

```vue
<script setup lang="ts">
import { computed, toRef } from 'vue'
import EChart from '../charts/EChart.vue'
import { useStatistics } from '../../composables/useStatistics'
import type { BuildHealthStats } from '../../types/api'

const props = defineProps<{ apiBase: string; version: string; range: string }>()

const { data, loading, error } = useStatistics<BuildHealthStats>(
  'build-health', toRef(props, 'apiBase'), toRef(props, 'version'), toRef(props, 'range'),
)

const outcomesOption = computed(() => {
  const d = data.value?.outcomesOverTime ?? []
  return {
    tooltip: { trigger: 'axis' },
    legend: { data: ['succeeded', 'failed', 'broken'] },
    xAxis: { type: 'category', data: d.map(x => x.date) },
    yAxis: { type: 'value' },
    series: [
      { name: 'succeeded', type: 'line', stack: 'o', areaStyle: {}, data: d.map(x => x.succeeded) },
      { name: 'failed', type: 'line', stack: 'o', areaStyle: {}, data: d.map(x => x.failed) },
      { name: 'broken', type: 'line', stack: 'o', areaStyle: {}, data: d.map(x => x.broken) },
    ],
  }
})

const topFailingOption = computed(() => {
  const pkgs = data.value?.topFailingPackages ?? []
  const ras = Array.from(new Set(pkgs.flatMap(p => p.segments.map(s => s.repoArch))))
  return {
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    legend: { data: ras },
    grid: { left: 8, right: 16, top: 24, bottom: 8, containLabel: true },
    xAxis: { type: 'value' },
    yAxis: { type: 'category', data: pkgs.map(p => p.package) },
    series: ras.map(ra => ({
      name: ra, type: 'bar', stack: 'f',
      data: pkgs.map(p => p.segments.find(s => s.repoArch === ra)?.count ?? 0),
    })),
  }
})

const passByRAOption = computed(() => {
  const d = data.value?.passRateByRepoArch ?? []
  return {
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    xAxis: { type: 'category', data: d.map(x => x.repoArch) },
    yAxis: { type: 'value', max: 1 },
    series: [{ type: 'bar', data: d.map(x => +(x.passRate).toFixed(3)) }],
  }
})

const passPct = computed(() => data.value ? (data.value.passRate * 100).toFixed(1) + '%' : '—')
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="text-brand-purple text-[12px] font-bold uppercase tracking-[.5px]">Build Health</div>

    <div v-if="error" class="text-fail text-[13px]">Failed to load build health: {{ error }}</div>
    <div v-else-if="loading && !data" class="text-text-muted text-[13px]">Loading…</div>

    <template v-else-if="data">
      <!-- stat cards -->
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-[10px]">
        <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
          <div class="text-text-muted text-[11px] uppercase">Pass rate</div>
          <div class="text-text-primary text-[24px] font-bold">{{ passPct }}</div>
        </div>
        <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
          <div class="text-text-muted text-[11px] uppercase">Failing / broken now</div>
          <div class="text-text-primary text-[24px] font-bold">{{ data.failingNow }}</div>
        </div>
        <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
          <div class="text-text-muted text-[11px] uppercase">Packages tracked</div>
          <div class="text-text-primary text-[24px] font-bold">{{ data.packagesTracked }}</div>
        </div>
      </div>

      <!-- charts -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-[10px]">
        <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
          <div class="text-text-secondary text-[12px] font-semibold mb-2">Build outcomes over time</div>
          <EChart :option="outcomesOption" class="h-[240px]" />
        </div>
        <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
          <div class="text-text-secondary text-[12px] font-semibold mb-2">Pass rate by repo·arch</div>
          <EChart :option="passByRAOption" class="h-[240px]" />
        </div>
      </div>
      <div class="bg-bg-card border border-border rounded-[12px] p-[14px]">
        <div class="text-text-secondary text-[12px] font-semibold mb-2">Top failing packages — stacked by repo·arch</div>
        <EChart :option="topFailingOption" class="h-[280px]" />
      </div>
    </template>
  </div>
</template>
```

- [ ] **Step 2: Render in StatisticsPanel** — import `BuildHealthSection` and replace the Task-4 placeholder with `<BuildHealthSection :api-base="apiBase" :version="version" :range="range" />` (already shown in the Task 4 template).

- [ ] **Step 3: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/stats/BuildHealthSection.vue frontend/src/components/StatisticsPanel.vue
git commit -s -m "feat(frontend): build health statistics section"
```

---

## Self-Review

- **Spec coverage (Phase 1 portion):** new tab + selectors + URL state (Task 4) ✓; ECharts + theme (Task 3) ✓; per-context cached endpoint (Tasks 1+2) ✓; Build Health metrics incl. top-failing stacked-by-repo·arch and pass-rate-by-repo·arch (Tasks 2+5) ✓; context/version("all")/range scoping, no cross-context aggregation ✓; testing (store/api tests + build) ✓. Phases 2–4 are explicitly out of this plan.
- **Placeholder scan:** complete code given for the cache, SQL aggregations, handler, routes, ECharts wrapper/theme, composable, and all components; the one intentional placeholder (BuildHealthSection tag in Task 4) is called out and resolved in Task 5.
- **Type/naming consistency:** the JSON contract (`passRate`, `failingNow`, `packagesTracked`, `outcomesOverTime`, `topFailingPackages`, `passRateByRepoArch`) matches between Go (`buildHealthResponse` + `store` structs' json tags) and TS (`BuildHealthStats`); `useStatistics(area, apiBase, version, range)` signature matches its call in BuildHealthSection; route path `/{version}/stats/build-health` matches the composable's fetch URL.

## Notes for the executor

- Task 2 blockedBy Task 1 (uses `statsCache`/`newStatsCache`). Task 5 blockedBy Task 3 (EChart) and Task 4 (panel + composable + types). Task 4 should leave a placeholder where the section goes, swapped in Task 5.
- TTL is a literal `60 * time.Second` in `server.go` matching the existing caches; promoting it to a config flag is a trivial later change if desired.
- After Phase 1, Phases 2–4 (Performance, CVE, Activity) each add one `store` aggregation + one `/stats/<area>` route (+ tests) + one section component, reusing this foundation.
