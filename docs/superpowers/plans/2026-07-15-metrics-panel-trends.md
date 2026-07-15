# Metrics Panel Trend Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/api/metrics` gains previous-period window baselines, a 24h 5-minute series and the oldest-sample timestamp; the metrics panel's OBS-requests area becomes a slim card with a sparkline plus a full-width strip of five window tiles showing variation percentages.

**Architecture:** Three read-only store functions over the existing `metrics_samples` table feed three new fields in the `/api/metrics` `obs` section (riding the existing 30s poll); the Vue panel computes percentages client-side and renders layout mockup C. No sampler, retention or schema changes.

**Tech Stack:** Go (modernc sqlite), Vue 3 + TypeScript + Tailwind-style utility classes.

**User decisions (already made):**
- Variation on the time-window rows only (not the endpoint table).
- Baseline: previous adjacent period (last 6h vs the 6h before it, etc.).
- The 30d tile never shows a variation figure ("don't show the 30d period"); retention stays 30d.
- Layout mockup C: slim OBS card + 24h sparkline + full-width tile strip.
- Data delivery choice "1": extend `/api/metrics`; percentage math client-side.

Spec: `docs/superpowers/specs/2026-07-15-metrics-panel-trends-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. All datetime binds to SQLite are pre-formatted `t.UTC().Format(time.RFC3339Nano)` strings — never raw `time.Time` (known driver trap). Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: store read functions — prev windows, 24h series, oldest sample

**Goal:** `QueryMetricsPrevWindows`, `QueryMetricsSeries` and `OldestMetricsSample` in the store package, fully tested.

**Files:**
- Modify: `backend/internal/store/metrics.go` (append three functions + one exported constant)
- Modify: `backend/internal/store/metrics_test.go` (append three tests)

**Acceptance Criteria:**
- [ ] Prev-window sums: seeds just inside/outside each baseline band produce {6h: 6, 12h: 24, 24h: 96, 7d: 768}; empty table → all four keys present, zero
- [ ] Series: length exactly 288; seeded offsets 2min/7min/23h58m land in slots 287/286/0; a row older than 24h is excluded; two rows in one bucket sum; empty table → 288 zeros
- [ ] Oldest sample: empty table → zero time, nil error; seeded table → the earliest ts round-trips
- [ ] `go test ./internal/store/ -count=1` passes

**Verify:** `cd backend && go test ./internal/store/ -run 'TestQueryMetricsPrevWindows|TestQueryMetricsSeries|TestOldestMetricsSample' -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/store/metrics_test.go`:

```go
func TestQueryMetricsPrevWindows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	p, err := QueryMetricsPrevWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"6h", "12h", "24h", "7d"} {
		if v, ok := p[k]; !ok || v != 0 {
			t.Fatalf("empty table: prev[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}
	if _, ok := p["30d"]; ok {
		t.Fatalf("prev must not carry a 30d key")
	}

	seed := func(age time.Duration, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{"build_results": count}); err != nil {
			t.Fatal(err)
		}
	}

	seed(5*time.Hour+59*time.Minute, 1)     // current 6h window — no baseline
	seed(6*time.Hour+time.Minute, 2)        // 6h baseline (6h, 12h]
	seed(11*time.Hour+59*time.Minute, 4)    // 6h baseline, far edge
	seed(12*time.Hour+time.Minute, 8)       // 12h baseline (12h, 24h]
	seed(23*time.Hour+59*time.Minute, 16)   // 12h baseline, far edge
	seed(24*time.Hour+time.Minute, 32)      // 24h baseline (24h, 48h]
	seed(47*time.Hour+59*time.Minute, 64)   // 24h baseline, far edge
	seed(48*time.Hour+time.Minute, 128)     // gap: no baseline covers (48h, 7d]
	seed(7*24*time.Hour+time.Hour, 256)     // 7d baseline (7d, 14d]
	seed(14*24*time.Hour-time.Hour, 512)    // 7d baseline, far edge
	seed(14*24*time.Hour+time.Hour, 1024)   // outside every baseline

	p, err = QueryMetricsPrevWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"6h": 6, "12h": 24, "24h": 96, "7d": 768}
	for k, w := range want {
		if p[k] != w {
			t.Fatalf("prev[%q] = %d, want %d", k, p[k], w)
		}
	}
}

func TestQueryMetricsSeries(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	s, err := QueryMetricsSeries(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != SeriesBuckets {
		t.Fatalf("len(series) = %d, want %d", len(s), SeriesBuckets)
	}
	for i, v := range s {
		if v != 0 {
			t.Fatalf("empty table: series[%d] = %d, want 0", i, v)
		}
	}

	seed := func(age time.Duration, op string, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{op: count}); err != nil {
			t.Fatal(err)
		}
	}
	seed(2*time.Minute, "build_results", 3)              // newest bucket, slot 287
	seed(2*time.Minute, "project_meta", 11)              // same bucket, sums
	seed(7*time.Minute, "build_results", 5)              // slot 286
	seed(23*time.Hour+58*time.Minute, "build_results", 7) // oldest bucket, slot 0
	seed(24*time.Hour+time.Minute, "build_results", 13)  // older than 24h — excluded

	s, err = QueryMetricsSeries(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if s[287] != 14 || s[286] != 5 || s[0] != 7 {
		t.Fatalf("series slots = [287]=%d [286]=%d [0]=%d, want 14/5/7", s[287], s[286], s[0])
	}
	var total int64
	for _, v := range s {
		total += v
	}
	if total != 26 {
		t.Fatalf("series total = %d, want 26 (excluded row must not leak in)", total)
	}
}

func TestOldestMetricsSample(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := OldestMetricsSample(db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("empty table: oldest = %v, want zero time", got)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	earliest := now.Add(-8 * 24 * time.Hour)
	if err := InsertMetricsSamples(db, now.Add(-time.Hour), map[string]int64{"build_results": 1}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMetricsSamples(db, earliest, map[string]int64{"build_results": 2}); err != nil {
		t.Fatal(err)
	}

	got, err = OldestMetricsSample(db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(earliest) {
		t.Fatalf("oldest = %v, want %v", got, earliest)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/store/ -run 'TestQueryMetricsPrevWindows|TestQueryMetricsSeries|TestOldestMetricsSample' -v`
Expected: compile failure — `undefined: QueryMetricsPrevWindows` (and the others).

- [ ] **Step 3: Implement**

Append to `backend/internal/store/metrics.go`:

```go
// SeriesBuckets is the fixed length of the 24h request series: one bucket
// per 5 minutes.
const SeriesBuckets = 288

// QueryMetricsPrevWindows returns summed request counts over the previous
// adjacent periods of the trailing windows — (now-12h, now-6h],
// (now-24h, now-12h], (now-48h, now-24h] and (now-14d, now-7d] — keyed
// "6h"/"12h"/"24h"/"7d". There is no "30d" key: its baseline would need
// 60d of samples, beyond the default retention.
func QueryMetricsPrevWindows(db *sql.DB, now time.Time) (map[string]int64, error) {
	cutoff := func(d time.Duration) string {
		return now.Add(-d).UTC().Format(time.RFC3339Nano)
	}
	row := db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0)
		FROM metrics_samples
		WHERE ts > ?`,
		cutoff(12*time.Hour), cutoff(6*time.Hour),
		cutoff(24*time.Hour), cutoff(12*time.Hour),
		cutoff(48*time.Hour), cutoff(24*time.Hour),
		cutoff(14*24*time.Hour), cutoff(7*24*time.Hour),
		cutoff(14*24*time.Hour),
	)
	var s6, s12, s24, s7d int64
	if err := row.Scan(&s6, &s12, &s24, &s7d); err != nil {
		return nil, err
	}
	return map[string]int64{"6h": s6, "12h": s12, "24h": s24, "7d": s7d}, nil
}

// QueryMetricsSeries returns total requests per 5-minute bucket over the
// trailing 24h: a slice of exactly SeriesBuckets sums, oldest bucket
// first, missing buckets zero.
func QueryMetricsSeries(db *sql.DB, now time.Time) ([]int64, error) {
	rows, err := db.Query(`SELECT ts, count FROM metrics_samples WHERE ts > ?`,
		now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]int64, SeriesBuckets)
	for rows.Next() {
		var tsStr string
		var count int64
		if err := rows.Scan(&tsStr, &count); err != nil {
			return nil, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			return nil, err
		}
		idx := SeriesBuckets - 1 - int(now.Sub(ts)/(5*time.Minute))
		if idx < 0 || idx >= SeriesBuckets {
			continue
		}
		out[idx] += count
	}
	return out, rows.Err()
}

// OldestMetricsSample returns the earliest sample timestamp, or the zero
// time when the table is empty.
func OldestMetricsSample(db *sql.DB) (time.Time, error) {
	var tsStr sql.NullString
	if err := db.QueryRow(`SELECT MIN(ts) FROM metrics_samples`).Scan(&tsStr); err != nil {
		return time.Time{}, err
	}
	if !tsStr.Valid {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, tsStr.String)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/store/ -count=1`
Expected: all store tests PASS. Also `gofmt -l internal/store/` → empty.

- [ ] **Step 5: Commit**

```bash
git add internal/store/metrics.go internal/store/metrics_test.go
git commit -s -m "feat(store): prev-window baselines, 24h series and oldest metrics sample"
```

---

### Task 2: /api/metrics — windows_prev, series, oldest_sample

**Goal:** The `obs` section of `/api/metrics` carries the three new fields with the established warn-and-zero error convention.

**Files:**
- Modify: `backend/internal/api/metrics.go` (obsSection fields + handler population + doc comment)
- Modify: `backend/internal/api/metrics_test.go` (extra seeds + assertions)

**Acceptance Criteria:**
- [ ] Seeded db (7@1h, 9@3d, 5@8h, 20@8d) yields `windows` {6h:7, 12h:12, 24h:12, 7d:21, 30d:41}, `windows_prev` {6h:5, 12h:0, 24h:0, 7d:20}, `series` length 288 with series[275]==7 and series[191]==5, non-empty RFC3339 `oldest_sample`
- [ ] All three new keys present in the raw JSON
- [ ] Handler signature unchanged (`obsClient, ws, clients, db`); store errors → `slog.Warn` + zero values, HTTP 200
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `cd backend && go test ./internal/api/ -run TestMetricsHandler -count=1 -v` → PASS

**Steps:**

- [ ] **Step 1: Extend the test (failing first)**

In `backend/internal/api/metrics_test.go`, after the existing two seeds add:

```go
	// 5 requests 8h ago: inside the 6h baseline (6h, 12h] and the 12h/24h
	// windows. 20 requests 8 days ago: inside the 7d baseline (7d, 14d]
	// and the 30d window only.
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-8*time.Hour),
		map[string]int64{"build_results": 5}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-8*24*time.Hour),
		map[string]int64{"build_results": 20}); err != nil {
		t.Fatal(err)
	}
```

Replace the `wantWindows` line with:

```go
	wantWindows := map[string]int64{"6h": 7, "12h": 12, "24h": 12, "7d": 21, "30d": 41}
```

After the windows assertion loop, add:

```go
	wantPrev := map[string]int64{"6h": 5, "12h": 0, "24h": 0, "7d": 20}
	if len(got.OBS.WindowsPrev) != len(wantPrev) {
		t.Fatalf("obs.windows_prev = %v, want exactly keys of %v", got.OBS.WindowsPrev, wantPrev)
	}
	for k, want := range wantPrev {
		if got.OBS.WindowsPrev[k] != want {
			t.Fatalf("obs.windows_prev[%q] = %d, want %d", k, got.OBS.WindowsPrev[k], want)
		}
	}
	if len(got.OBS.Series) != store.SeriesBuckets {
		t.Fatalf("len(obs.series) = %d, want %d", len(got.OBS.Series), store.SeriesBuckets)
	}
	if got.OBS.Series[275] != 7 || got.OBS.Series[191] != 5 {
		t.Fatalf("obs.series[275]=%d obs.series[191]=%d, want 7 and 5",
			got.OBS.Series[275], got.OBS.Series[191])
	}
	if got.OBS.OldestSample == "" {
		t.Fatalf("obs.oldest_sample empty, want RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, got.OBS.OldestSample); err != nil {
		t.Fatalf("obs.oldest_sample %q not RFC3339: %v", got.OBS.OldestSample, err)
	}
```

And extend the raw-key checks at the end:

```go
	for _, k := range []string{"windows_prev", "series", "oldest_sample"} {
		if _, ok := raw["obs"].(map[string]any)[k]; !ok {
			t.Fatalf("obs.%s key missing from raw response", k)
		}
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestMetricsHandler -v`
Expected: compile failure — `got.OBS.WindowsPrev undefined`.

- [ ] **Step 3: Implement**

In `backend/internal/api/metrics.go`, replace `obsSection` with:

```go
type obsSection struct {
	Total        int64            `json:"total"`
	ByEndpoint   map[string]int64 `json:"by_endpoint"`
	ReqPerS      float64          `json:"req_per_s"`
	Windows      map[string]int64 `json:"windows"`       // trailing counts: "6h", "12h", "24h", "7d", "30d"
	WindowsPrev  map[string]int64 `json:"windows_prev"`  // previous adjacent periods: "6h", "12h", "24h", "7d"
	Series       []int64          `json:"series"`        // requests per 5-min bucket, last 24h, oldest first
	OldestSample string           `json:"oldest_sample"` // RFC3339; "" while no samples exist
}
```

In the handler, replace the windows query block with:

```go
		now := time.Now().UTC()
		windows, err := store.QueryMetricsWindows(db, now)
		if err != nil {
			slog.Warn("api: query metrics windows", "err", err)
			windows = map[string]int64{"6h": 0, "12h": 0, "24h": 0, "7d": 0, "30d": 0}
		}
		prev, err := store.QueryMetricsPrevWindows(db, now)
		if err != nil {
			slog.Warn("api: query metrics prev windows", "err", err)
			prev = map[string]int64{"6h": 0, "12h": 0, "24h": 0, "7d": 0}
		}
		series, err := store.QueryMetricsSeries(db, now)
		if err != nil {
			slog.Warn("api: query metrics series", "err", err)
			series = make([]int64, store.SeriesBuckets)
		}
		oldest, err := store.OldestMetricsSample(db)
		if err != nil {
			slog.Warn("api: query oldest metrics sample", "err", err)
			oldest = time.Time{}
		}
		var oldestStr string
		if !oldest.IsZero() {
			oldestStr = oldest.UTC().Format(time.RFC3339)
		}
```

and extend the encoded `obsSection` literal:

```go
			OBS: obsSection{
				Total:        total,
				ByEndpoint:   byEndpoint,
				ReqPerS:      obsClient.RatePerSecond(),
				Windows:      windows,
				WindowsPrev:  prev,
				Series:       series,
				OldestSample: oldestStr,
			},
```

Extend the handler doc comment's enumeration with "previous-period window baselines, the 24h 5-minute request series, and the oldest persisted sample timestamp".

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./... -count=1 && go build ./... && gofmt -l internal/api`
Expected: all PASS, build OK, gofmt output must not list metrics.go/metrics_test.go (release_artifacts.go drift is pre-existing — leave it).

- [ ] **Step 5: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go
git commit -s -m "feat(api): prev-window baselines, 24h series and oldest sample in /api/metrics"
```

---

### Task 3: panel restructure — sparkline card + variation tile strip

**Goal:** The metrics panel renders mockup C: slim OBS card with a 24h sparkline; a full-width strip of five window tiles with ▲/▼ percentage or muted "—".

**Files:**
- Modify: `frontend/src/types/metrics.ts` (three fields)
- Modify: `frontend/src/components/MetricsPanel.vue` (script + template)

**Acceptance Criteria:**
- [ ] `MetricsSnapshot.obs` has `windows_prev: Record<string, number>`, `series: number[]`, `oldest_sample: string`
- [ ] OBS card: total + req/s line + SVG sparkline (288-point polyline, `var(--brand-purple)` stroke) + caption "requests per 5 min · last 24h"; the old in-card window list is gone
- [ ] Tile strip between the 3-column grid and the endpoint table: five tiles (label, count, delta line); ▲ green `var(--ok)` positive, ▼ red `var(--fail)` negative, neutral "0%", muted "—" for 30d / zero baseline / warm-up (`oldest_sample` empty or newer than now − 2×window)
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Type fields**

In `frontend/src/types/metrics.ts`, extend the `obs` object type after `windows`:

```ts
    windows_prev: Record<string, number>
    series: number[]
    oldest_sample: string
```

- [ ] **Step 2: Script changes in `frontend/src/components/MetricsPanel.vue`**

Replace the `WINDOW_KEYS` constant with:

```ts
const WINDOW_KEYS = ['6h', '12h', '24h', '7d', '30d'] as const

const WINDOW_MS: Record<string, number> = {
  '6h': 6 * 3_600_000,
  '12h': 12 * 3_600_000,
  '24h': 24 * 3_600_000,
  '7d': 7 * 86_400_000,
}

// Delta vs the previous adjacent period; null renders as a muted "—":
// always for 30d (its baseline would need 60d of samples), and whenever
// the baseline is zero or not fully covered by stored history yet.
function windowDelta(k: string): number | null {
  const obs = data.value?.obs
  if (!obs || !(k in WINDOW_MS)) return null
  const prev = obs.windows_prev?.[k] ?? 0
  if (prev === 0) return null
  if (!obs.oldest_sample) return null
  if (Date.parse(obs.oldest_sample) > Date.now() - 2 * WINDOW_MS[k]) return null
  const cur = obs.windows?.[k] ?? 0
  return Math.round(((cur - prev) / prev) * 100)
}

const tiles = computed(() =>
  WINDOW_KEYS.map((k) => ({
    key: k,
    count: data.value?.obs.windows?.[k] ?? 0,
    delta: windowDelta(k),
  })))

const sparkPoints = computed(() => {
  const series = data.value?.obs.series ?? []
  if (series.length < 2) return ''
  const W = 180
  const H = 34
  const pad = 2
  const max = Math.max(...series, 1)
  return series
    .map((v, i) => {
      const x = (i / (series.length - 1)) * W
      const y = H - pad - (Number(v) / max) * (H - 2 * pad)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
})
```

- [ ] **Step 3: Template changes**

In the OBS requests card, replace the window list block

```html
            <div class="mt-2 max-w-[200px]">
              <div v-for="k in WINDOW_KEYS" :key="k" class="flex justify-between text-[12.5px] py-[2px]">
                <span class="text-text-muted">last {{ k }}</span>
                <span class="font-mono tabular-nums font-semibold">{{ fmt(data.obs.windows?.[k] ?? 0) }}</span>
              </div>
            </div>
```

with the sparkline:

```html
            <div class="mt-2">
              <svg v-if="sparkPoints" width="180" height="34" viewBox="0 0 180 34" aria-hidden="true">
                <polyline fill="none" stroke="var(--brand-purple)" stroke-width="1.5" :points="sparkPoints" />
              </svg>
              <div class="text-[9.5px] text-text-muted mt-1">requests per 5 min · last 24h</div>
            </div>
```

Between the closing `</div>` of the 3-column grid and the `<!-- Endpoint table -->` comment, insert the tile strip:

```html
        <!-- Window tiles -->
        <div class="flex gap-2 mt-4 flex-wrap">
          <div
            v-for="tile in tiles"
            :key="tile.key"
            class="flex-1 min-w-[96px] bg-bg-muted border border-border rounded-[8px] px-3 py-2"
          >
            <div class="text-[9.5px] font-bold uppercase tracking-[0.05em] text-text-muted">last {{ tile.key }}</div>
            <div class="text-[15px] font-extrabold tabular-nums mt-[2px]">{{ fmt(tile.count) }}</div>
            <div class="text-[10.5px] font-bold mt-[2px]">
              <span v-if="tile.delta === null" class="text-text-muted font-normal">—</span>
              <span v-else-if="tile.delta > 0" :style="{ color: 'var(--ok)' }">▲ {{ tile.delta }}%</span>
              <span v-else-if="tile.delta < 0" :style="{ color: 'var(--fail)' }">▼ {{ Math.abs(tile.delta) }}%</span>
              <span v-else class="text-text-secondary">0%</span>
            </div>
          </div>
        </div>
```

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types/metrics.ts frontend/src/components/MetricsPanel.vue
git commit -s -m "feat(overview): sparkline card and variation tile strip in metrics panel"
```
