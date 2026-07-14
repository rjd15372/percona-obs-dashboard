# Persistent Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** OBS request counts persist as 5-minute delta samples in SQLite with 30-day retention; `/api/metrics` windows become DB-backed (surviving restarts) and gain `7d`/`30d`; the in-memory 24h window ring is removed.

**Architecture:** A `metricsampler.Sampler` (telemetry-Reporter pattern, fixed 5-min tick) diffs `MetricsSnapshot()` and inserts non-zero per-op delta rows; `store` gains insert/window-query/prune functions over a new `metrics_samples` table (RFC3339Nano timestamps — the established driver convention); the pruner gains a second retention; the handler swaps `WindowCounts()` for the DB query.

**Tech Stack:** Go + modernc sqlite, Vue 3 (one constant change).

**User decisions (already made):**
- Persist per-endpoint OBS request counts only; live gauges stay in-memory.
- DB-backed windows + `7d`/`30d` added; the in-memory 24h ring removed (one source of truth).
- Retention 30 days, config `store.metrics_retention` default `"30d"` (EVENT_RETENTION convention).

Spec: `docs/superpowers/specs/2026-07-14-persistent-metrics-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. Commits: `git commit -s`, never Co-Authored-By. Timestamps ALWAYS bound as `t.UTC().Format(time.RFC3339Nano)` strings — never raw `time.Time` (known driver trap).

---

### Task 1: `metrics_samples` schema + store functions

**Goal:** The table plus `InsertMetricsSamples`, `QueryMetricsWindows`, `PruneMetricsSamples`, fully tested.

**Files:**
- Modify: `internal/store/db.go` (schema after the `target_state_durations` block)
- Create: `internal/store/metrics.go`
- Create: `internal/store/metrics_test.go`

**Acceptance Criteria:**
- [ ] Insert→query round trip: rows aged 5h59m / 23h59m / 6d23h / 29d land in the correct window sums; rows aged 6h01m / 24h01m / 7d01m / 31d are excluded from the tighter windows (31d from all)
- [ ] Empty table → map with all five keys, all zero
- [ ] Prune deletes only rows older than cutoff and reports the count
- [ ] `go test ./internal/store/ -count=1` passes

**Verify:** `go test ./internal/store/ -run TestMetrics -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/store/metrics_test.go`:

```go
package store

import (
	"testing"
	"time"
)

func TestMetricsSamplesWindowsAndPrune(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	w, err := QueryMetricsWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"6h", "12h", "24h", "7d", "30d"} {
		if v, ok := w[k]; !ok || v != 0 {
			t.Fatalf("empty table: windows[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}

	seed := func(age time.Duration, op string, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{op: count}); err != nil {
			t.Fatal(err)
		}
	}
	// Inside/outside pairs around each window boundary.
	seed(5*time.Hour+59*time.Minute, "build_results", 1)   // in 6h+
	seed(6*time.Hour+time.Minute, "build_results", 2)      // in 12h+, out of 6h
	seed(23*time.Hour+59*time.Minute, "version", 4)        // in 24h+
	seed(24*time.Hour+time.Minute, "version", 8)           // in 7d+, out of 24h
	seed(6*24*time.Hour+23*time.Hour, "rebuild", 16)       // in 7d+
	seed(7*24*time.Hour+time.Hour, "rebuild", 32)          // in 30d only
	seed(29*24*time.Hour, "publish_states", 64)            // in 30d only
	seed(31*24*time.Hour, "publish_states", 128)           // outside all windows

	w, err = QueryMetricsWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string]int64{
		"6h":  1,
		"12h": 1 + 2,
		"24h": 1 + 2 + 4,
		"7d":  1 + 2 + 4 + 8 + 16,
		"30d": 1 + 2 + 4 + 8 + 16 + 32 + 64,
	}
	for k, want := range expect {
		if w[k] != want {
			t.Errorf("windows[%q] = %d, want %d", k, w[k], want)
		}
	}

	// Prune at 30d: only the 31d row goes.
	n, err := PruneMetricsSamples(db, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("prune deleted %d rows, want 1", n)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metrics_samples`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 7 {
		t.Fatalf("remaining rows = %d, want 7", remaining)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestMetrics -v`
Expected: compile error — `undefined: QueryMetricsWindows` etc.

- [ ] **Step 3: Implement**

In `internal/store/db.go`, append to the schema string after the
`idx_tsd_open_pkg` index:

```sql
CREATE TABLE IF NOT EXISTS metrics_samples (
    ts    DATETIME NOT NULL,
    op    TEXT     NOT NULL,
    count INTEGER  NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metrics_samples_ts ON metrics_samples (ts);
```

Create `internal/store/metrics.go`:

```go
package store

import (
	"database/sql"
	"time"
)

// metricsWindows are the trailing windows /api/metrics reports, tightest
// first. The last entry also bounds the table scan in QueryMetricsWindows
// and matches the default retention.
var metricsWindows = []struct {
	key string
	d   time.Duration
}{
	{"6h", 6 * time.Hour},
	{"12h", 12 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

// InsertMetricsSamples writes one row per op with the given counts at ts.
// Zero-count ops must be filtered by the caller. ts is stored as an
// RFC3339Nano UTC string, matching every other datetime column.
func InsertMetricsSamples(db *sql.DB, ts time.Time, deltas map[string]int64) error {
	tsStr := ts.UTC().Format(time.RFC3339Nano)
	for op, count := range deltas {
		if _, err := db.Exec(`INSERT INTO metrics_samples (ts, op, count) VALUES (?, ?, ?)`,
			tsStr, op, count); err != nil {
			return err
		}
	}
	return nil
}

// QueryMetricsWindows returns summed request counts over the trailing
// windows, keyed "6h"/"12h"/"24h"/"7d"/"30d". The scan is bounded by the
// widest window.
func QueryMetricsWindows(db *sql.DB, now time.Time) (map[string]int64, error) {
	cutoff := func(d time.Duration) string {
		return now.Add(-d).UTC().Format(time.RFC3339Nano)
	}
	row := db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(count), 0)
		FROM metrics_samples
		WHERE ts > ?`,
		cutoff(metricsWindows[0].d), cutoff(metricsWindows[1].d),
		cutoff(metricsWindows[2].d), cutoff(metricsWindows[3].d),
		cutoff(metricsWindows[4].d),
	)
	sums := make([]int64, len(metricsWindows))
	if err := row.Scan(&sums[0], &sums[1], &sums[2], &sums[3], &sums[4]); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(metricsWindows))
	for i, wdef := range metricsWindows {
		out[wdef.key] = sums[i]
	}
	return out, nil
}

// PruneMetricsSamples deletes samples older than cutoff and returns how
// many rows were removed.
func PruneMetricsSamples(db *sql.DB, cutoff time.Time) (int64, error) {
	res, err := db.Exec(`DELETE FROM metrics_samples WHERE ts < ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/db.go internal/store/metrics.go internal/store/metrics_test.go
git commit -s -m "feat(store): metrics_samples table with window sums and pruning"
```

---

### Task 2: `internal/metricsampler`

**Goal:** The 5-minute delta sampler with restart-safe baseline semantics.

**Files:**
- Create: `internal/metricsampler/sampler.go`
- Create: `internal/metricsampler/sampler_test.go`

**Acceptance Criteria:**
- [ ] First tick writes counts-since-boot; later ticks write per-op deltas only
- [ ] Zero-delta ticks write no rows
- [ ] A failed insert advances the baseline (next tick does not double-count) and warn-logs
- [ ] `Run(ctx)` exits on cancel
- [ ] `go test ./internal/metricsampler/ -count=1 -v` passes

**Verify:** `go test ./internal/metricsampler/ -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/metricsampler/sampler_test.go`:

```go
package metricsampler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

type fakeSnap struct{ m map[string]int64 }

func (f *fakeSnap) MetricsSnapshot() map[string]int64 {
	out := make(map[string]int64, len(f.m))
	for k, v := range f.m {
		out[k] = v
	}
	return out
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func rowsFor(t *testing.T, db *sql.DB, op string) (n int, total int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(count),0) FROM metrics_samples WHERE op = ?`, op).
		Scan(&n, &total); err != nil {
		t.Fatal(err)
	}
	return n, total
}

func TestSamplerDeltasAndZeroSuppression(t *testing.T) {
	db := testDB(t)
	snap := &fakeSnap{m: map[string]int64{"build_results": 10}}
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	s := &Sampler{DB: db, Snap: snap}
	s.now = func() time.Time { return cur }
	s.prev = snap.MetricsSnapshot() // boot baseline, as Run does

	// No change: no rows.
	s.sample()
	if n, _ := rowsFor(t, db, "build_results"); n != 0 {
		t.Fatalf("zero-delta tick wrote %d rows, want 0", n)
	}

	// +5 requests: one row with count 5.
	snap.m["build_results"] = 15
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db, "build_results"); n != 1 || total != 5 {
		t.Fatalf("delta tick: rows=%d total=%d, want 1/5", n, total)
	}

	// New op appears: its full count is the delta.
	snap.m["rebuild"] = 3
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db, "rebuild"); n != 1 || total != 3 {
		t.Fatalf("new-op tick: rows=%d total=%d, want 1/3", n, total)
	}
}

func TestSamplerFailedInsertAdvancesBaseline(t *testing.T) {
	db := testDB(t)
	snap := &fakeSnap{m: map[string]int64{"build_results": 10}}
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	s := &Sampler{DB: db, Snap: snap}
	s.now = func() time.Time { return cur }
	s.prev = snap.MetricsSnapshot()

	snap.m["build_results"] = 20
	db.Close() // force insert failure
	s.sample()

	// Reopen-equivalent: fresh DB, next delta must NOT include the lost 10.
	db2 := testDB(t)
	s.DB = db2
	snap.m["build_results"] = 22
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db2, "build_results"); n != 1 || total != 2 {
		t.Fatalf("after failed insert: rows=%d total=%d, want 1/2 (lost bucket not re-counted)", n, total)
	}
}

func TestSamplerRunStopsOnCancel(t *testing.T) {
	db := testDB(t)
	s := &Sampler{DB: db, Snap: &fakeSnap{m: map[string]int64{}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on cancel")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/metricsampler/ -v`
Expected: compile error — package doesn't exist.

- [ ] **Step 3: Implement**

Create `internal/metricsampler/sampler.go`:

```go
// Package metricsampler persists OBS request counts as periodic per-op
// delta samples so trailing-window metrics survive restarts.
// Design: docs/superpowers/specs/2026-07-14-persistent-metrics-design.md
package metricsampler

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

const sampleInterval = 5 * time.Minute

// Sampler diffs the OBS request counters every sampleInterval and inserts
// one metrics_samples row per op with a non-zero delta. The baseline
// starts at process boot, so at most one unflushed partial bucket is lost
// per restart. Not safe for concurrent use; Run drives it serially.
type Sampler struct {
	DB   *sql.DB
	Snap Snapshotter

	prev map[string]int64 // last snapshot; nil until Run initializes it
	now  func() time.Time // injectable for tests; nil = time.Now
}

// Run ticks every sampleInterval until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	if s.now == nil {
		s.now = time.Now
	}
	if s.prev == nil {
		s.prev = s.Snap.MetricsSnapshot()
	}
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample()
		}
	}
}

func (s *Sampler) sample() {
	if s.now == nil {
		s.now = time.Now
	}
	cur := s.Snap.MetricsSnapshot()
	deltas := make(map[string]int64)
	for op, c := range cur {
		if d := c - s.prev[op]; d > 0 {
			deltas[op] = d
		}
	}
	// Advance the baseline whether or not the insert succeeds: a failed
	// insert loses that bucket instead of double-counting it next tick.
	s.prev = cur
	if len(deltas) == 0 {
		return
	}
	if err := store.InsertMetricsSamples(s.DB, s.now().UTC(), deltas); err != nil {
		slog.Warn("metricsampler: insert samples", "err", err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/metricsampler/ -count=1 -v`
Expected: 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/metricsampler/sampler.go internal/metricsampler/sampler_test.go
git commit -s -m "feat(metricsampler): 5-minute delta sampler for OBS request counts"
```

---

### Task 3: retention config + pruner + sampler wiring

**Goal:** `store.metrics_retention` (default 30d) config; `runPruner` also prunes samples; the sampler runs from `main.go`.

**Files:**
- Modify: `internal/config/config.go` (StoreConfig field, default, env pair, parse, assemble)
- Modify: `internal/config/config_test.go` (append test)
- Modify: `cmd/obsboard/main.go` (import; sampler goroutine; `runPruner` call + signature)

**Acceptance Criteria:**
- [ ] `cfg.Store.MetricsRetention` defaults to 30 days; `METRICS_RETENTION` overrides (e.g. `60d`)
- [ ] `runPruner` prunes both events and metrics samples on its ticker
- [ ] `main.go` starts `go sampler.Run(ctx)` with the OBS client as Snapshotter
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `go test ./internal/config/ -run TestLoadMetricsRetention -count=1 -v && go build ./...` → PASS, build OK

**Steps:**

- [ ] **Step 1: Write the failing config test**

Append to `internal/config/config_test.go`:

```go
func TestLoadMetricsRetention(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 30*24*time.Hour {
		t.Errorf("default metrics retention = %v, want 720h", cfg.Store.MetricsRetention)
	}

	os.Setenv("METRICS_RETENTION", "60d")
	defer os.Unsetenv("METRICS_RETENTION")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 60*24*time.Hour {
		t.Errorf("override metrics retention = %v, want 1440h", cfg.Store.MetricsRetention)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadMetricsRetention -v`
Expected: compile error — `cfg.Store.MetricsRetention undefined`.

- [ ] **Step 3: Implement config**

In `internal/config/config.go`: extend `StoreConfig`:

```go
type StoreConfig struct {
	DBPath           string
	EventRetention   time.Duration
	MetricsRetention time.Duration
}
```

Default (after `store.event_retention`):

```go
	v.SetDefault("store.metrics_retention", "30d")
```

Env pair (after the event_retention pair):

```go
		{"store.metrics_retention", "METRICS_RETENTION"},
```

Parse (after the `retention` parse block):

```go
	metricsRetention, err := parseRetention(v.GetString("store.metrics_retention"))
	if err != nil {
		return nil, fmt.Errorf("invalid METRICS_RETENTION %q: %w", v.GetString("store.metrics_retention"), err)
	}
```

Assemble:

```go
		Store: StoreConfig{
			DBPath:           v.GetString("store.db_path"),
			EventRetention:   retention,
			MetricsRetention: metricsRetention,
		},
```

- [ ] **Step 4: Wire main.go**

Add the import `"github.com/percona/obs-dashboard/internal/metricsampler"`.

Change the pruner call (line 95) to pass both retentions:

```go
	go runPruner(ctx, db, cfg.Poller.Interval, cfg.Store.EventRetention, cfg.Store.MetricsRetention)
```

Next to it, start the sampler:

```go
	sampler := &metricsampler.Sampler{DB: db, Snap: obsClient}
	go sampler.Run(ctx)
```

Extend `runPruner` (line 148) — full replacement:

```go
func runPruner(ctx context.Context, db *sql.DB, interval, eventRetention, metricsRetention time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-eventRetention)
			if err := store.PruneEvents(db, cutoff); err != nil {
				slog.Warn("pruner: prune events", "err", err)
			}
			if _, err := store.PruneMetricsSamples(db, time.Now().UTC().Add(-metricsRetention)); err != nil {
				slog.Warn("pruner: prune metrics samples", "err", err)
			}
		}
	}
}
```

(NOTE: keep whatever error-handling/logging the existing body has for
PruneEvents — read the current function and preserve its exact existing
behavior, only adding the metrics prune. If the existing body differs from
the sketch above, adapt the sketch to it, not vice versa.)

Also append to BOTH `config.yaml.example` files (repo root and `backend/`),
under the `store`/retention area if one exists, otherwise adjacent to
`event_retention` usage in the file:

```yaml
  # How long persisted OBS request-count samples are kept (feeds the
  # 6h/12h/24h/7d/30d windows in /api/metrics).
  metrics_retention: 30d
```

(If the example files carry no `store:` section, add the key where
`event_retention` is documented; match each file's existing structure.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./... -count=1 && go build ./...`
Expected: all PASS, build OK.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/obsboard/main.go ../config.yaml.example config.yaml.example
git commit -s -m "feat(config): metrics retention, sample pruning and sampler wiring"
```

---

### Task 4: DB-backed windows + ring removal + panel keys

**Goal:** `/api/metrics` windows come from `QueryMetricsWindows` with five keys; the in-memory 24h ring is removed; the panel lists all five windows.

**Files:**
- Modify: `internal/api/metrics.go` (db param, query, error path, doc comment)
- Modify: `internal/api/metrics_test.go` (db plumb + seeded-window assertions)
- Modify: `internal/api/server.go` (route passes `db`)
- Modify: `internal/obs/client.go` (remove `winPeriod`/`winHits`, the 5-min bucket code in `inc`, `windowCounts`, `WindowCounts`; fix comments)
- Modify: `internal/obs/metrics_test.go` (remove `TestWindowCounts`, `TestWindowCountsBucketReuse`)
- Modify: `frontend/src/components/MetricsPanel.vue` (WINDOW_KEYS)

**Acceptance Criteria:**
- [ ] `obs.windows` has keys `6h`/`12h`/`24h`/`7d`/`30d`, summed from seeded `metrics_samples` rows
- [ ] Window-query failure → warn log + all-zero windows, still HTTP 200
- [ ] `grep -rn "winPeriod\|windowCounts\|WindowCounts" backend/internal` → no matches
- [ ] Panel shows five window rows; `npm run build` exits 0
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `go test ./internal/api/ ./internal/obs/ -count=1 && go build ./... && cd ../frontend && npm run build` → all PASS

**Steps:**

- [ ] **Step 1: Update the api test (failing first)**

In `internal/api/metrics_test.go`, inside `TestMetricsHandler`:

Add DB setup after the client construction (add imports `"time"` and the
store import if missing):

```go
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Seed: 7 requests 1h ago (in all windows), 9 requests 3 days ago
	// (7d/30d only).
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-time.Hour),
		map[string]int64{"build_results": 7}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-3*24*time.Hour),
		map[string]int64{"build_results": 9}); err != nil {
		t.Fatal(err)
	}
```

Change the invocation to pass the db:

```go
	metricsHandler(c, ws, fakeClientCounter{n: 3}, db)(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
```

Replace the existing Windows assertions (the `for _, k := range []string{"6h", "12h", "24h"}` zero-check block) with:

```go
	if got.OBS.Windows == nil {
		t.Fatalf("obs.windows must be a map, got null")
	}
	wantWindows := map[string]int64{"6h": 7, "12h": 7, "24h": 7, "7d": 16, "30d": 16}
	for k, want := range wantWindows {
		if got.OBS.Windows[k] != want {
			t.Fatalf("obs.windows[%q] = %d, want %d", k, got.OBS.Windows[k], want)
		}
	}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestMetricsHandler -v`
Expected: compile error — handler argument count.

- [ ] **Step 3: Implement the handler change**

In `internal/api/metrics.go`: add imports `"database/sql"`, `"log/slog"`,
`"time"` (keep existing), and the store import. Change the signature and
the windows source:

```go
// metricsHandler handles GET /api/metrics: absolute OBS request counts,
// the trailing-minute request rate, persisted trailing 6h/12h/24h/7d/30d
// window totals, limiter gauges, working-set stats, process uptime, and
// connected SSE clients.
func metricsHandler(obsClient *obs.Client, ws Statter, clients ClientCounter, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		byEndpoint := obsClient.MetricsSnapshot()
		var total int64
		for _, v := range byEndpoint {
			total += v
		}
		ls := obsClient.LimiterStats()
		s := ws.Stats()

		windows, err := store.QueryMetricsWindows(db, time.Now().UTC())
		if err != nil {
			slog.Warn("api: query metrics windows", "err", err)
			windows = map[string]int64{"6h": 0, "12h": 0, "24h": 0, "7d": 0, "30d": 0}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse{
			OBS: obsSection{
				Total:      total,
				ByEndpoint: byEndpoint,
				ReqPerS:    obsClient.RatePerSecond(),
				Windows:    windows,
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
			UptimeSeconds: int64(time.Since(processStart).Seconds()),
			SSEClients:    clients.Clients(),
		})
	}
}
```

The `h6, h12, h24 := obsClient.WindowCounts()` line and its uses are gone
(the method itself is removed in Step 4). In `internal/api/server.go`, the route becomes:

```go
	r.Get("/api/metrics", metricsHandler(obsClient, ws, h, db))
```

- [ ] **Step 4: Remove the obs ring**

In `internal/obs/client.go`:
- Delete `winPeriod [288]int64` and `winHits [288]int64` from `obsMetrics`
  and drop the "and into a ring of 5-minute buckets…" clause from its doc
  comment.
- In `inc`, delete the 5-minute block (`p := sec / 300` through
  `m.winHits[j]++`), keeping the seconds-ring block.
- Delete the `windowCounts` method and the exported `WindowCounts` method
  entirely.

In `internal/obs/metrics_test.go`, delete `TestWindowCounts` and
`TestWindowCountsBucketReuse` entirely.

Confirm removal: `grep -rn "winPeriod\|windowCounts\|WindowCounts" internal/` → no output.

- [ ] **Step 5: Panel keys**

In `frontend/src/components/MetricsPanel.vue`, change:

```ts
const WINDOW_KEYS = ['6h', '12h', '24h'] as const
```

to:

```ts
const WINDOW_KEYS = ['6h', '12h', '24h', '7d', '30d'] as const
```

- [ ] **Step 6: Run everything**

Run: `go test ./... -count=1 && go build ./... && cd ../frontend && npm run build`
Expected: all PASS, build OK, exit 0.

- [ ] **Step 7: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go internal/api/server.go internal/obs/client.go internal/obs/metrics_test.go ../frontend/src/components/MetricsPanel.vue
git commit -s -m "feat(api): DB-backed metrics windows with 7d/30d; remove in-memory ring"
```
