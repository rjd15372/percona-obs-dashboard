# Automatic Rebuild Trigger for Stuck-Blocked Targets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A background sweeper that automatically triggers an OBS rebuild for build targets stuck in `blocked` state for more than a configurable threshold (default 30m), working around the OBS bug where targets stay blocked after their dependencies finished.

**Architecture:** A new `internal/unblocker.Sweeper` goroutine (modeled on `runPruner`) ticks every 5 minutes, calls a new `store.QueryStaleBlockedTargets` over the existing `target_state_durations` table, tracks attempts per blocked episode in memory (keyed on `entered_at`, so any state change resets the count), and calls the existing `obs.Client.Rebuild` under the background context so the per-minute OBS rate limiter applies.

**Tech Stack:** Go, modernc.org/sqlite (times stored as RFC3339Nano text — scan as string, parse like `attachTargetStartedAt` does), viper config, `log/slog`.

**User decisions (already made):**
- Scope: "Devel + staging + PRs" — implemented as `p.is_release = 0`; releases excluded.
- Retry: "Retry with cap, reset on state change" — max 3 attempts per blocked episode, paced at the threshold interval; a new `entered_at` is a new episode.
- Architecture: "Standalone DB-driven sweeper" (rejected: piggybacking on working-set polls).
- Opt-in: config `unblocker.enabled` defaults to false; `unblocker.threshold` defaults to 30m.

Spec: `docs/superpowers/specs/2026-07-14-auto-unblock-trigger-design.md`

**Conventions:** commands run from `/home/rdias/Work/percona-obs-dashboard/backend`. Commits: `git commit -s`, never a `Co-Authored-By:` trailer. Fixed constants (no config): sweep interval 5m, max 3 attempts/episode, max 10 triggers/sweep.

---

### Task 1: `store.QueryStaleBlockedTargets`

**Goal:** A store query returning targets whose current state is `blocked`, entered before a cutoff, in non-release projects.

**Files:**
- Modify: `internal/store/packages.go` (append after `attachTargetStartedAt`, ~line 403)
- Modify: `internal/store/packages_test.go` (append test)

**Acceptance Criteria:**
- [ ] Returns (project, package, repo, arch, entered_at) for rows with `state='blocked' AND exited_at IS NULL AND entered_at < cutoff` joined to packages with `is_release = 0`
- [ ] Excludes: non-blocked states, closed episodes (`exited_at` set), under-cutoff rows, and release packages
- [ ] `EnteredAt` round-trips as a parseable time (RFC3339Nano text, like `attachTargetStartedAt`)
- [ ] `go test ./internal/store/ -run TestQueryStaleBlockedTargets -v` passes

**Verify:** `go test ./internal/store/ -count=1 ./internal/store/` → all PASS *(command: `go test ./internal/store/ -count=1`)*

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `internal/store/packages_test.go`:

```go
func TestQueryStaleBlockedTargets(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	old := now.Add(-45 * time.Minute)
	fresh := now.Add(-5 * time.Minute)

	insertPkg := func(project, name string, isRelease int) {
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, is_release, updated_at)
			VALUES (?, ?, 'blocked', 0, 0, '[]', ?, ?)`, project, name, isRelease, now); err != nil {
			t.Fatal(err)
		}
	}
	insertDur := func(project, pkg, repo, arch, state string, enteredAt time.Time, exited bool) {
		// Seed exactly as the production write path (recordStateTransitions)
		// does: pre-formatted RFC3339Nano strings, NOT raw time.Time (the
		// driver would render those in an incompatible format).
		exitedAt := any(nil)
		if exited {
			exitedAt = now.UTC().Format(time.RFC3339Nano)
		}
		if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at, exited_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, project, pkg, repo, arch, state, enteredAt.UTC().Format(time.RFC3339Nano), exitedAt); err != nil {
			t.Fatal(err)
		}
	}

	insertPkg("isv:percona:ppg:devel:17", "stuck_pkg", 0)
	insertDur("isv:percona:ppg:devel:17", "stuck_pkg", "Fedora_42", "x86_64", "blocked", old, false)

	insertPkg("isv:percona:ppg:staging:17", "fresh_pkg", 0)
	insertDur("isv:percona:ppg:staging:17", "fresh_pkg", "Fedora_42", "x86_64", "blocked", fresh, false)

	insertPkg("isv:percona:ppg:devel:16", "building_pkg", 0)
	insertDur("isv:percona:ppg:devel:16", "building_pkg", "Fedora_42", "x86_64", "building", old, false)

	insertPkg("isv:percona:ppg:devel:15", "ended_pkg", 0)
	insertDur("isv:percona:ppg:devel:15", "ended_pkg", "Fedora_42", "x86_64", "blocked", old, true)

	insertPkg("isv:percona:ppg:releases:17", "release_pkg", 1)
	insertDur("isv:percona:ppg:releases:17", "release_pkg", "Fedora_42", "x86_64", "blocked", old, false)

	got, err := QueryStaleBlockedTargets(db, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly [stuck_pkg], got %d rows: %+v", len(got), got)
	}
	bt := got[0]
	if bt.Project != "isv:percona:ppg:devel:17" || bt.Package != "stuck_pkg" ||
		bt.Repo != "Fedora_42" || bt.Arch != "x86_64" {
		t.Fatalf("unexpected target: %+v", bt)
	}
	if !bt.EnteredAt.Equal(old) {
		t.Fatalf("EnteredAt = %v, want %v", bt.EnteredAt, old)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestQueryStaleBlockedTargets -v`
Expected: compile error — `undefined: QueryStaleBlockedTargets`.

- [ ] **Step 3: Implement**

Append to `internal/store/packages.go` (after `attachTargetStartedAt`):

```go
// BlockedTarget identifies one build target currently stuck in blocked state.
type BlockedTarget struct {
	Project   string
	Package   string
	Repo      string
	Arch      string
	EnteredAt time.Time
}

// QueryStaleBlockedTargets returns targets whose CURRENT state is 'blocked'
// and was entered before cutoff, restricted to non-release projects
// (devel/staging/PRs). Consumed by the unblocker sweeper.
func QueryStaleBlockedTargets(db *sql.DB, cutoff time.Time) ([]BlockedTarget, error) {
	rows, err := db.Query(`
		SELECT d.project, d.package, d.repo, d.arch, d.entered_at
		FROM target_state_durations d
		JOIN packages p ON p.project = d.project AND p.name = d.package
		WHERE d.state = 'blocked' AND d.exited_at IS NULL
		  AND d.entered_at < ?
		  AND p.is_release = 0
		ORDER BY d.entered_at`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedTarget
	for rows.Next() {
		var t BlockedTarget
		var entered string
		if err := rows.Scan(&t.Project, &t.Package, &t.Repo, &t.Arch, &entered); err != nil {
			return nil, err
		}
		ts, err := time.Parse(time.RFC3339Nano, entered)
		if err != nil {
			return nil, err
		}
		t.EnteredAt = ts
		out = append(out, t)
	}
	return out, rows.Err()
}
```

(Time handling: the production write path — `recordStateTransitions` — stores `entered_at` as explicit `.Format(time.RFC3339Nano)` strings, and the modernc driver renders a RAW `time.Time` arg via `t.String()`, a different lexical format. So the cutoff MUST be bound pre-formatted, mirroring `QueryBuildingEntries` in `overview.go`; `entered_at` is scanned as string then parsed. Any test seeding this table must likewise insert pre-formatted RFC3339Nano strings, or it will pass against data unlike production's.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/packages.go internal/store/packages_test.go
git commit -s -m "feat(store): query for stale blocked targets in non-release projects"
```

---

### Task 2: `internal/unblocker` sweeper

**Goal:** The sweeper loop with episode-scoped attempt tracking, threshold pacing, per-sweep cap, and rebuild triggering.

**Files:**
- Create: `internal/unblocker/sweeper.go`
- Create: `internal/unblocker/sweeper_test.go`

**Acceptance Criteria:**
- [ ] Over-threshold blocked targets trigger `Rebuilder.Rebuild(ctx, project, repo, arch, pkg)`; under-threshold ones don't (enforced by the store cutoff)
- [ ] Max 3 triggers per episode; a row with a newer `entered_at` gets a fresh count
- [ ] Retriggers pace at ≥ Threshold since the last trigger, not at sweep cadence
- [ ] Max 10 triggers per sweep; remainder handled on later sweeps
- [ ] A failed Rebuild counts as an attempt (warn-logged)
- [ ] `Run(ctx)` exits on context cancellation
- [ ] `go test ./internal/unblocker/ -count=1 -v` passes

**Verify:** `go test ./internal/unblocker/ -count=1 -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/unblocker/sweeper_test.go`:

```go
package unblocker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

type fakeRebuilder struct {
	calls []string
	err   error
}

func (f *fakeRebuilder) Rebuild(_ context.Context, project, repo, arch, pkg string) error {
	f.calls = append(f.calls, project+"/"+pkg+"/"+repo+"/"+arch)
	return f.err
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

func seedBlocked(t *testing.T, db *sql.DB, project, pkg string, enteredAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, is_release, updated_at)
		VALUES (?, ?, 'blocked', 0, 0, '[]', 0, ?)`, project, pkg, enteredAt); err != nil {
		t.Fatal(err)
	}
	// entered_at seeded as the production write path stores it: a
	// pre-formatted RFC3339Nano string (raw time.Time would be rendered
	// in an incompatible format by the driver).
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES (?, ?, 'Fedora_42', 'x86_64', 'blocked', ?)`, project, pkg, enteredAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

// closeEpisode marks the target's current blocked episode as exited and
// opens a new one entered at newEnteredAt (simulating blocked→…→blocked).
// Timestamps are seeded as pre-formatted RFC3339Nano strings, matching the
// production write path.
func closeEpisode(t *testing.T, db *sql.DB, project, pkg string, newEnteredAt time.Time) {
	t.Helper()
	ts := newEnteredAt.UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE target_state_durations SET exited_at = ? WHERE project = ? AND package = ? AND exited_at IS NULL`,
		ts, project, pkg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES (?, ?, 'Fedora_42', 'x86_64', 'blocked', ?)`, project, pkg, ts); err != nil {
		t.Fatal(err)
	}
}

func newTestSweeper(db *sql.DB, rb Rebuilder, at *time.Time) *Sweeper {
	return &Sweeper{
		DB:        db,
		Rebuilder: rb,
		Threshold: 30 * time.Minute,
		now:       func() time.Time { return *at },
	}
}

func TestSweepTriggersStaleAndPacesRetries(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	s.sweep(context.Background())
	if len(rb.calls) != 1 {
		t.Fatalf("first sweep: %d calls, want 1", len(rb.calls))
	}

	// 5 minutes later (next tick): still blocked, but within pacing window.
	cur = cur.Add(5 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 1 {
		t.Fatalf("paced sweep retriggered: %d calls, want still 1", len(rb.calls))
	}

	// Threshold later: second attempt fires.
	cur = cur.Add(30 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 2 {
		t.Fatalf("after pacing window: %d calls, want 2", len(rb.calls))
	}
}

func TestSweepAttemptCapAndEpisodeReset(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	for i := 0; i < 6; i++ { // far more sweeps than allowed attempts
		s.sweep(context.Background())
		cur = cur.Add(35 * time.Minute) // always past the pacing window
	}
	if len(rb.calls) != 3 {
		t.Fatalf("attempt cap: %d calls, want 3", len(rb.calls))
	}

	// New episode (state changed and re-blocked): count resets.
	closeEpisode(t, db, "isv:percona:ppg:devel:17", "stuck", cur)
	cur = cur.Add(45 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 4 {
		t.Fatalf("episode reset: %d calls, want 4", len(rb.calls))
	}
}

func TestSweepPerSweepCap(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	// Staggered entered_at so ORDER BY entered_at gives a deterministic
	// iteration order (pkg00 oldest … pkg10 newest); all far past cutoff.
	for i := 0; i < 11; i++ {
		seedBlocked(t, db, "isv:percona:ppg:devel:17", fmt.Sprintf("pkg%02d", i),
			base.Add(time.Duration(i)*time.Second))
	}

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	s.sweep(context.Background())
	if len(rb.calls) != 10 {
		t.Fatalf("per-sweep cap: %d calls, want 10", len(rb.calls))
	}
	// The cap admits 10 per sweep in entered_at order, so pkg00-09 keep
	// winning the slots until they cap out at 3 attempts each (sweeps 2-3),
	// then the straggler pkg10 gets its 3 paced attempts (sweeps 4-6).
	// Cumulative: 10, 20, 30, 31, 32, 33.
	for _, want := range []int{20, 30, 31, 32, 33} {
		cur = cur.Add(35 * time.Minute)
		s.sweep(context.Background())
		if len(rb.calls) != want {
			t.Fatalf("cumulative calls = %d, want %d", len(rb.calls), want)
		}
	}
	seen := make(map[string]bool)
	for _, c := range rb.calls {
		seen[c] = true
	}
	if len(seen) != 11 {
		t.Fatalf("distinct targets triggered = %d, want all 11", len(seen))
	}
}

func TestSweepFailedRebuildCountsAsAttempt(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{err: errors.New("obs unavailable")}
	s := newTestSweeper(db, rb, &cur)

	for i := 0; i < 5; i++ {
		s.sweep(context.Background())
		cur = cur.Add(35 * time.Minute)
	}
	if len(rb.calls) != 3 {
		t.Fatalf("failed rebuilds must still cap at 3 attempts, got %d calls", len(rb.calls))
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	db := testDB(t)
	rb := &fakeRebuilder{}
	s := &Sweeper{DB: db, Rebuilder: rb, Threshold: 30 * time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/unblocker/ -v`
Expected: compile error — package doesn't exist / `undefined: Sweeper`.

- [ ] **Step 3: Implement**

Create `internal/unblocker/sweeper.go`:

```go
// Package unblocker works around an OBS bug where build targets stay in
// blocked state after their dependencies have finished: targets blocked
// longer than a threshold get their build re-triggered automatically.
// Design: docs/superpowers/specs/2026-07-14-auto-unblock-trigger-design.md
package unblocker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// Rebuilder triggers an OBS rebuild for one build target. Satisfied by
// *obs.Client.
type Rebuilder interface {
	Rebuild(ctx context.Context, project, repo, arch, pkg string) error
}

const (
	sweepInterval       = 5 * time.Minute
	maxAttempts         = 3  // per blocked episode
	maxTriggersPerSweep = 10 // protects the shared per-minute OBS budget
)

// episodeKey identifies one continuous blocked episode: any state
// transition writes a new duration row with a new entered_at, producing a
// new key — which is what resets the attempt count.
type episodeKey struct {
	project, pkg, repo, arch string
	enteredAt                time.Time
}

type episode struct {
	attempts    int
	lastTrigger time.Time
}

// Sweeper periodically rebuilds targets stuck in blocked state longer than
// Threshold. Detection reads target_state_durations (maintained by the
// poller and MQ consumer) — it adds no OBS read traffic; only the rebuild
// triggers hit OBS, through the client's background rate limiter.
type Sweeper struct {
	DB        *sql.DB
	Rebuilder Rebuilder
	Threshold time.Duration

	now      func() time.Time // injectable for tests; nil = time.Now
	episodes map[episodeKey]*episode
}

// Run ticks every sweepInterval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	if s.now == nil {
		s.now = time.Now
	}
	if s.episodes == nil {
		s.episodes = make(map[episodeKey]*episode)
	}
	now := s.now().UTC()

	stale, err := store.QueryStaleBlockedTargets(s.DB, now.Add(-s.Threshold))
	if err != nil {
		slog.Warn("unblocker: query stale blocked targets", "err", err)
		return
	}

	// Drop episodes that no longer match a current stale row (unblocked,
	// state changed, or aged out) so the map stays bounded.
	current := make(map[episodeKey]bool, len(stale))
	for _, t := range stale {
		current[keyOf(t)] = true
	}
	for k := range s.episodes {
		if !current[k] {
			delete(s.episodes, k)
		}
	}

	triggered := 0
	for _, t := range stale {
		if triggered >= maxTriggersPerSweep {
			break
		}
		k := keyOf(t)
		ep := s.episodes[k]
		if ep == nil {
			ep = &episode{}
			s.episodes[k] = ep
		}
		if ep.attempts >= maxAttempts {
			continue
		}
		// Pace retries at the threshold interval, not the sweep interval.
		if !ep.lastTrigger.IsZero() && now.Sub(ep.lastTrigger) < s.Threshold {
			continue
		}
		// Count the attempt regardless of outcome: a persistently erroring
		// target caps out instead of retrying forever.
		ep.attempts++
		ep.lastTrigger = now
		triggered++

		blockedFor := now.Sub(t.EnteredAt).Round(time.Minute)
		if err := s.Rebuilder.Rebuild(ctx, t.Project, t.Repo, t.Arch, t.Package); err != nil {
			slog.Warn("unblocker: rebuild trigger failed",
				"project", t.Project, "package", t.Package, "repo", t.Repo, "arch", t.Arch,
				"blocked_for", blockedFor, "attempt", ep.attempts, "err", err)
			continue
		}
		slog.Info("unblocker: triggered rebuild",
			"project", t.Project, "package", t.Package, "repo", t.Repo, "arch", t.Arch,
			"blocked_for", blockedFor, "attempt", ep.attempts)
	}
}

func keyOf(t store.BlockedTarget) episodeKey {
	return episodeKey{
		project:   t.Project,
		pkg:       t.Package,
		repo:      t.Repo,
		arch:      t.Arch,
		enteredAt: t.EnteredAt.UTC(),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/unblocker/ -count=1 -v`
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/unblocker/sweeper.go internal/unblocker/sweeper_test.go
git commit -s -m "feat(unblocker): sweeper auto-triggers rebuilds for stuck-blocked targets"
```

---

### Task 3: config + wiring

**Goal:** Opt-in `unblocker` config section and the `main.go` goroutine start.

**Files:**
- Modify: `internal/config/config.go` (struct ~line 20, defaults ~line 81, env bindings ~line 111, parse+assemble ~lines 136-170)
- Modify: `internal/config/config_test.go` (append test)
- Modify: `cmd/obsboard/main.go` (imports; after `runPruner` start at line 90)
- Modify: `config.yaml.example` (append section)

**Acceptance Criteria:**
- [ ] `cfg.Unblocker.Enabled` defaults to false; `cfg.Unblocker.Threshold` defaults to 30m
- [ ] `UNBLOCKER_ENABLED` / `UNBLOCKER_THRESHOLD` env vars override (viper keys `unblocker.enabled` / `unblocker.threshold`)
- [ ] `main.go` starts `go sweeper.Run(ctx)` only when enabled, with `obsClient` as the Rebuilder
- [ ] `config.yaml.example` documents the section
- [ ] `go test ./... && go build ./...` pass

**Verify:** `go test ./... -count=1 && go build ./...` → all PASS, build OK

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestLoadUnblockerDefaultsAndOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Unblocker.Enabled {
		t.Error("unblocker should be disabled by default")
	}
	if cfg.Unblocker.Threshold != 30*time.Minute {
		t.Errorf("default threshold = %v, want 30m", cfg.Unblocker.Threshold)
	}

	os.Setenv("UNBLOCKER_ENABLED", "true")
	os.Setenv("UNBLOCKER_THRESHOLD", "45m")
	defer os.Unsetenv("UNBLOCKER_ENABLED")
	defer os.Unsetenv("UNBLOCKER_THRESHOLD")

	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Unblocker.Enabled || cfg.Unblocker.Threshold != 45*time.Minute {
		t.Errorf("override: enabled=%v threshold=%v, want true/45m", cfg.Unblocker.Enabled, cfg.Unblocker.Threshold)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadUnblocker -v`
Expected: compile error — `cfg.Unblocker undefined`.

- [ ] **Step 3: Implement config**

In `internal/config/config.go`:

Add to the `Config` struct (after `Telemetry`):

```go
	Unblocker  UnblockerConfig
```

Add the type (after `TelemetryConfig`):

```go
type UnblockerConfig struct {
	Enabled   bool
	Threshold time.Duration
}
```

Add defaults (after the `telemetry.enabled` default, line 81):

```go
	v.SetDefault("unblocker.enabled", false)
	v.SetDefault("unblocker.threshold", "30m")
```

Add env bindings to the pairs list (after the telemetry pairs):

```go
		{"unblocker.enabled", "UNBLOCKER_ENABLED"},
		{"unblocker.threshold", "UNBLOCKER_THRESHOLD"},
```

Add parsing (after `telemetryInterval` parsing, ~line 139):

```go
	unblockThreshold, err := time.ParseDuration(v.GetString("unblocker.threshold"))
	if err != nil {
		return nil, fmt.Errorf("invalid UNBLOCKER_THRESHOLD %q: %w", v.GetString("unblocker.threshold"), err)
	}
```

Add to the assembled `cfg` (after the `Telemetry` field):

```go
		Unblocker: UnblockerConfig{
			Enabled:   v.GetBool("unblocker.enabled"),
			Threshold: unblockThreshold,
		},
```

- [ ] **Step 4: Wire main.go**

In `cmd/obsboard/main.go`, add the import:

```go
	"github.com/percona/obs-dashboard/internal/unblocker"
```

After the `go runPruner(...)` line (line 90), add:

```go
	if cfg.Unblocker.Enabled {
		sweeper := &unblocker.Sweeper{DB: db, Rebuilder: obsClient, Threshold: cfg.Unblocker.Threshold}
		go sweeper.Run(ctx)
	}
```

- [ ] **Step 5: Document in config.yaml.example**

Append to `config.yaml.example` (repo root):

```yaml

# Automatic rebuild trigger for targets stuck in blocked state (works
# around an OBS bug where targets stay blocked after their dependencies
# built). Opt-in; triggers go through the background OBS rate limiter.
unblocker:
  enabled: false
  # Trigger a rebuild when a target has been blocked longer than this.
  threshold: 30m
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./... -count=1 && go build ./...`
Expected: all PASS, build OK.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/obsboard/main.go ../config.yaml.example
git commit -s -m "feat(config): opt-in unblocker sweeper wiring with threshold"
```
