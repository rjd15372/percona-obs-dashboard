# OBS API Traffic Reduction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut background OBS API traffic ~10-50x during build storms via adaptive backoff, threshold-based project-level `_result` batching, and a per-minute client-side rate limiter with interactive bypass.

**Architecture:** The working-set scheduler becomes the single decision point for *when* each package is processed (per-package backoff ladder 30s→5m) and *how* build results are fetched (one project-level `_result` call when ≥ threshold packages of a project are due). The OBS client enforces a fixed-window per-minute budget on background requests; API-handler requests bypass it via a context tag. Poller, MQ consumer, and parking logic are untouched.

**Tech Stack:** Go 1.x backend (`backend/`), stdlib only (no new dependencies), viper config, chi router, httptest-based unit tests.

**Spec:** `docs/superpowers/specs/2026-07-10-obs-traffic-reduction-design.md`

**User decisions (already made):**
- All four reduction ideas are in scope: batching, backoff, blocked-via-backoff, rate limiter.
- Batching only engages when ≥ `worker_pool.batch_threshold` (default 4) packages of the same project are due — below that, per-package fetches are cheaper.
- Backoff ladder caps at 5 minutes (`worker_pool.backoff_max`).
- Rate limit is enforced per minute (user corrected from hourly): `obs.minute_request_budget`, default 60 (~86k/day).
- Approach A (scheduler-centric) chosen over independent patches and MQ-first redesign.
- Blocked packages are handled by backoff, not by extending parking.

**Verification baseline:** `cd backend && go build ./... && go test ./...` must pass after every task.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/obs/env.go` (new) | `Env` type: optional pre-fetched project-level data for a task pass |
| `backend/internal/obs/interactive.go` (new) | Context tag for limiter bypass |
| `backend/internal/obs/limiter.go` (new) | Fixed-window per-minute request limiter |
| `backend/internal/obs/tasks.go` | Tasks accept `*Env`; BuildState/PublishState/BinariesCheck consume it |
| `backend/internal/obs/client.go` | Limiter hooks in `get`/`getFile`/`post`, `SetMinuteBudget`, limiter metrics |
| `backend/internal/worker/worker.go` | `Task` interface gains `env`; `ProcessOnce` returns `changed`; `ProcessJob` handles batch jobs |
| `backend/internal/workingset/workingset.go` | Per-package schedule state (backoff), `Job` type, due-based dispatch with project grouping |
| `backend/internal/config/config.go` | `backoff_max`, `batch_threshold`, `minute_request_budget` keys |
| `backend/internal/api/server.go` | Middleware tagging all API requests as interactive |
| `backend/cmd/obsboard/main.go` | Wiring: new `workingset.New` signature, `SetMinuteBudget` |
| `backend/config.yaml.example`, `config.yaml.example` | Document new keys |

---

### Task 1: Env type and Task interface thread-through

**Goal:** Introduce `obs.Env` and thread an `env *obs.Env` parameter through the `Task` interface, all task implementations, `ProcessOnce`, and every test call site — behavior unchanged (env ignored everywhere for now).

**Files:**
- Create: `backend/internal/obs/env.go`
- Modify: `backend/internal/worker/worker.go` (Task interface, ProcessOnce, task loop)
- Modify: `backend/internal/obs/tasks.go` (all 8 Run methods)
- Test: `backend/internal/obs/tasks_test.go`, `backend/internal/worker/worker_test.go` (mechanical call-site updates)

**Acceptance Criteria:**
- [ ] `obs.Env` exists with `BuildStates []PackageBuildState` and `RepoStates map[string]string` fields
- [ ] `worker.Task` interface is `Run(ctx context.Context, client *obs.Client, pkg *model.Package, env *obs.Env) error`
- [ ] All existing tests pass unchanged in behavior

**Verify:** `cd backend && go build ./... && go test ./...` → all packages PASS

**Steps:**

- [ ] **Step 1: Create `backend/internal/obs/env.go`**

```go
package obs

// Env carries optional pre-fetched project-level OBS data into a task pass.
// A nil *Env (or nil field) means the task fetches its own data per package.
// The worker populates it when a batch job pre-fetched a project-level
// _result response that serves every package in the job.
type Env struct {
	// BuildStates are this package's target build states from a project-level
	// _result call. Nil means BuildStateTask must fetch per-package.
	BuildStates []PackageBuildState
	// RepoStates maps "repo/arch" to the repo publish state ("published",
	// "publishing", …) from the same _result response. Nil means
	// PublishStateTask/BinariesCheckTask must fetch per-package.
	RepoStates map[string]string
}
```

- [ ] **Step 2: Change the Task interface and ProcessOnce in `backend/internal/worker/worker.go`**

```go
// Task is implemented by types that enrich a package's state from OBS.
// Implementations live in obs/tasks.go to avoid circular imports.
type Task interface {
	Run(ctx context.Context, client *obs.Client, pkg *model.Package, env *obs.Env) error
}
```

`ProcessOnce` gains the parameter and forwards it (signature only — return value comes in Task 5):

```go
func (p *Pool) ProcessOnce(ctx context.Context, pkg *model.Package, env *obs.Env) {
```

and inside the task loop:

```go
	for _, t := range tasks {
		if err := t.Run(ctx, p.client, pkg, env); err != nil {
```

The `run()` dispatch loop passes `nil`:

```go
			p.ProcessOnce(ctx, pkg, nil)
```

- [ ] **Step 3: Add `env *Env` to all 8 Run methods in `backend/internal/obs/tasks.go`**

Change each signature; bodies unchanged. The parameter is intentionally unused in this task:

```go
func (t BuildStateTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t PublishStateTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t BinariesCheckTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t BlockedReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t BuildReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t PackageTypeTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t VersionTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
func (t ContainerTagsTask) Run(ctx context.Context, client *Client, pkg *model.Package, env *Env) error {
```

- [ ] **Step 4: Fix all test call sites (mechanical)**

In `backend/internal/obs/tasks_test.go`: every `task.Run(context.Background(), c, pkg)` and `(obs.SomeTask{}).Run(context.Background(), c, pkg)` gains a trailing `, nil` argument.

In `backend/internal/worker/worker_test.go`: the fake task types (`captureTask`, `errorTask`, `succeedingTask`, `publishedTask`, and any other type implementing `worker.Task`) gain the parameter:

```go
func (t *captureTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package, _ *obs.Env) error {
```

Every direct `p.ProcessOnce(ctx, pkg)` call gains `, nil`.

- [ ] **Step 5: Verify and commit**

Run: `cd backend && go build ./... && go test ./...`
Expected: all packages PASS (no behavior change).

```bash
git add backend/internal/obs/env.go backend/internal/obs/tasks.go backend/internal/obs/tasks_test.go backend/internal/worker/worker.go backend/internal/worker/worker_test.go
git commit -s -m "refactor(worker): thread Env parameter through Task interface

Preparation for project-level batch fetching: tasks can receive
pre-fetched project data instead of fetching per package. No behavior
change; env is nil everywhere."
```

```json:metadata
{"files": ["backend/internal/obs/env.go", "backend/internal/obs/tasks.go", "backend/internal/worker/worker.go", "backend/internal/obs/tasks_test.go", "backend/internal/worker/worker_test.go"], "verifyCommand": "cd backend && go build ./... && go test ./...", "acceptanceCriteria": ["obs.Env exists with BuildStates and RepoStates fields", "Task interface carries env *obs.Env", "all existing tests pass"], "modelTier": "mechanical"}
```

---

### Task 2: BuildStateTask, PublishStateTask, BinariesCheckTask consume Env

**Goal:** The three tasks that fetch `_result` data use pre-fetched Env data when present, skipping their own OBS calls.

**Files:**
- Modify: `backend/internal/obs/tasks.go:45-50` (BuildStateTask), `:119-123` (PublishStateTask), `:162-167` (BinariesCheckTask)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] With `env.BuildStates` set, BuildStateTask makes zero HTTP requests and applies the given states
- [ ] With `env.RepoStates` set, PublishStateTask and BinariesCheckTask make zero `_result?view=status` requests
- [ ] With `env == nil`, all three behave exactly as before (existing tests still pass)

**Verify:** `cd backend && go test ./internal/obs/ -run 'TestBuildStateTask|TestPublishStateTask|TestBinariesCheck' -v` → PASS including new `...UsesPrefetchedEnv` tests

**Steps:**

- [ ] **Step 1: Write the failing tests in `backend/internal/obs/tasks_test.go`**

```go
func TestBuildStateTaskUsesPrefetchedEnv(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupFailed,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
	}
	env := &obs.Env{BuildStates: []obs.PackageBuildState{
		{Project: "isv:percona", Repo: "repo", Arch: "x86_64", Package: "mypkg", State: "succeeded"},
	}}

	if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Errorf("expected no HTTP requests, got %d", hits.Load())
	}
	if pkg.RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded rollup, got %s", pkg.RollupState)
	}
}

func TestPublishStateTaskUsesPrefetchedEnv(t *testing.T) {
	// Serve only the _meta publish-flags request; _result must not be hit.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_meta") {
			fmt.Fprint(w, `<project name="isv:percona"/>`)
			return
		}
		http.Error(w, "must not be called: "+r.URL.Path, http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
	}
	env := &obs.Env{RepoStates: map[string]string{"repo/x86_64": "published"}}

	if err := (obs.PublishStateTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if !pkg.Targets[0].Published {
		t.Error("expected target published from prefetched repo states")
	}
	if pkg.RollupState != model.RollupPublished {
		t.Errorf("expected published rollup, got %s", pkg.RollupState)
	}
}

func TestBinariesCheckTaskUsesPrefetchedEnv(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:releases:17",
		Name:        "mypkg",
		IsRelease:   true,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
	}
	env := &obs.Env{RepoStates: map[string]string{"repo/x86_64": "published"}}

	if err := (obs.BinariesCheckTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if pkg.RollupState != model.RollupPublished {
		t.Errorf("expected published rollup, got %s", pkg.RollupState)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/obs/ -run 'UsesPrefetchedEnv' -v`
Expected: FAIL (tasks still fetch via HTTP and hit the 500 handler).

- [ ] **Step 3: Implement Env consumption in `backend/internal/obs/tasks.go`**

BuildStateTask — replace the fetch at the top of `Run`:

```go
	var results []PackageBuildState
	if env != nil && env.BuildStates != nil {
		results = env.BuildStates
	} else {
		var err error
		results, err = client.PackageBuildResults(ctx, pkg.Project, pkg.Name)
		if err != nil {
			return err
		}
	}
```

PublishStateTask — replace the `RepoPublishStates` fetch (keep the hasCandidate/needsCheck guards above it unchanged):

```go
	var states map[string]string
	if env != nil && env.RepoStates != nil {
		states = env.RepoStates
	} else {
		var err error
		states, err = client.RepoPublishStates(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: repo publish states", "pkg", pkg.Name, "err", err)
			return nil
		}
	}
```

BinariesCheckTask — same substitution for its `RepoPublishStates` call:

```go
	var states map[string]string
	if env != nil && env.RepoStates != nil {
		states = env.RepoStates
	} else {
		var err error
		states, err = client.RepoPublishStates(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: binaries check repo states", "pkg", pkg.Name, "err", err)
			return nil
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/obs/ -v`
Expected: PASS (new and existing tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/obs/tasks.go backend/internal/obs/tasks_test.go
git commit -s -m "feat(obs): tasks consume pre-fetched project-level results via Env

BuildStateTask, PublishStateTask, and BinariesCheckTask skip their own
OBS calls when a batch pass supplies the data."
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -v", "acceptanceCriteria": ["BuildStateTask uses env.BuildStates without HTTP", "PublishStateTask/BinariesCheckTask use env.RepoStates without HTTP", "nil env preserves existing behavior"], "modelTier": "mechanical"}
```

---

### Task 3: Config keys for backoff, batching, and rate budget

**Goal:** Add `worker_pool.backoff_max`, `worker_pool.batch_threshold`, and `obs.minute_request_budget` config keys with defaults and env bindings; document them in both example files.

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/config.yaml.example`, `config.yaml.example`
- Test: `backend/internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] Defaults: `BackoffMax=5m`, `BatchThreshold=4`, `MinuteRequestBudget=60`
- [ ] Env overrides work: `WORKER_POOL_BACKOFF_MAX`, `WORKER_POOL_BATCH_THRESHOLD`, `OBS_MINUTE_REQUEST_BUDGET`
- [ ] Both example YAML files document the new keys

**Verify:** `cd backend && go test ./internal/config/ -v` → PASS including `TestTrafficReductionDefaults`

**Steps:**

- [ ] **Step 1: Write the failing test in `backend/internal/config/config_test.go`**

```go
func TestTrafficReductionDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 5*time.Minute {
		t.Errorf("BackoffMax = %v, want 5m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 4 {
		t.Errorf("BatchThreshold = %d, want 4", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.OBS.MinuteRequestBudget != 60 {
		t.Errorf("MinuteRequestBudget = %d, want 60", cfg.OBS.MinuteRequestBudget)
	}
}

func TestTrafficReductionEnvOverride(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("WORKER_POOL_BACKOFF_MAX", "2m")
	t.Setenv("WORKER_POOL_BATCH_THRESHOLD", "8")
	t.Setenv("OBS_MINUTE_REQUEST_BUDGET", "30")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 2*time.Minute {
		t.Errorf("BackoffMax = %v, want 2m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 8 {
		t.Errorf("BatchThreshold = %d, want 8", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.OBS.MinuteRequestBudget != 30 {
		t.Errorf("MinuteRequestBudget = %d, want 30", cfg.OBS.MinuteRequestBudget)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/config/ -run TestTrafficReduction -v`
Expected: FAIL (fields don't exist — compile error is the failure mode here).

- [ ] **Step 3: Implement in `backend/internal/config/config.go`**

Struct fields:

```go
type OBSConfig struct {
	Username            string
	Password            string
	BaseURL             string
	MinuteRequestBudget int
}
```

```go
type WorkerPoolConfig struct {
	Size           int
	PollInterval   time.Duration
	QueueSize      int
	BackoffMax     time.Duration
	BatchThreshold int
}
```

Defaults (next to the existing `v.SetDefault` block):

```go
	v.SetDefault("obs.minute_request_budget", 60)
	v.SetDefault("worker_pool.backoff_max", "5m")
	v.SetDefault("worker_pool.batch_threshold", 4)
```

Env bindings (append to the existing pair list):

```go
		{"obs.minute_request_budget", "OBS_MINUTE_REQUEST_BUDGET"},
		{"worker_pool.backoff_max", "WORKER_POOL_BACKOFF_MAX"},
		{"worker_pool.batch_threshold", "WORKER_POOL_BATCH_THRESHOLD"},
```

Parsing (after the `pollIntervalWP` block):

```go
	backoffMax, err := time.ParseDuration(v.GetString("worker_pool.backoff_max"))
	if err != nil {
		return nil, fmt.Errorf("invalid WORKER_POOL_BACKOFF_MAX %q: %w", v.GetString("worker_pool.backoff_max"), err)
	}
```

Struct wiring:

```go
		OBS: OBSConfig{
			Username:            v.GetString("obs.username"),
			Password:            v.GetString("obs.password"),
			BaseURL:             strings.TrimRight(v.GetString("obs.base_url"), "/"),
			MinuteRequestBudget: v.GetInt("obs.minute_request_budget"),
		},
```

```go
		WorkerPool: WorkerPoolConfig{
			Size:           v.GetInt("worker_pool.size"),
			PollInterval:   pollIntervalWP,
			QueueSize:      v.GetInt("worker_pool.queue_size"),
			BackoffMax:     backoffMax,
			BatchThreshold: v.GetInt("worker_pool.batch_threshold"),
		},
```

- [ ] **Step 4: Document in both example files**

`backend/config.yaml.example` — extend the `obs:` and `worker_pool:` sections:

```yaml
obs:
  base_url: https://api.opensuse.org
  username: ""  # Set via OBS_USERNAME env var
  password: ""  # Set via OBS_PASSWORD env var
  # Per-minute cap on background OBS requests (poller + workers).
  # Interactive API requests bypass it. 0 disables limiting.
  minute_request_budget: 60
```

```yaml
worker_pool:
  size: 5
  poll_interval: 30s
  queue_size: 512
  # Cap of the per-package backoff ladder: unchanged packages are polled
  # less and less often, up to this interval.
  backoff_max: 5m
  # Minimum number of due packages of the same project to fetch build
  # results with one project-level call instead of per-package calls.
  batch_threshold: 4
```

`config.yaml.example` (repo root) — same additions to its `obs:` section and a new `worker_pool:` section with the same content (the root example currently has none, so add the whole block including `size: 5`, `poll_interval: 30s`, `queue_size: 512`).

- [ ] **Step 5: Run tests and commit**

Run: `cd backend && go test ./internal/config/ -v`
Expected: PASS.

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go backend/config.yaml.example config.yaml.example
git commit -s -m "feat(config): backoff_max, batch_threshold, minute_request_budget keys"
```

```json:metadata
{"files": ["backend/internal/config/config.go", "backend/internal/config/config_test.go", "backend/config.yaml.example", "config.yaml.example"], "verifyCommand": "cd backend && go test ./internal/config/ -v", "acceptanceCriteria": ["defaults 5m/4/60", "env overrides bound", "example YAMLs document keys"], "modelTier": "mechanical"}
```

---

### Task 4: Per-minute rate limiter with interactive bypass

**Goal:** Background OBS requests pass through a fixed-window per-minute budget in the client; API-handler requests bypass it via a context tag; limiter stats surface in `MetricsSnapshot()`.

**Files:**
- Create: `backend/internal/obs/interactive.go`
- Create: `backend/internal/obs/limiter.go`
- Create: `backend/internal/obs/limiter_test.go`
- Modify: `backend/internal/obs/client.go` (limiter field, `SetMinuteBudget`, hooks in `get`/`getFile`/`post`, `MetricsSnapshot`)
- Modify: `backend/internal/api/server.go` (interactive middleware)
- Modify: `backend/cmd/obsboard/main.go` (wire budget from config)

**Acceptance Criteria:**
- [ ] Budget exhausted → background `acquire` blocks; cancelled context unblocks with `ctx.Err()`
- [ ] Window rollover resets the budget
- [ ] `obs.Interactive(ctx)` requests skip the limiter entirely
- [ ] Budget ≤ 0 disables limiting (all existing tests, which use `NewClient` without a budget, are unaffected)
- [ ] `MetricsSnapshot()` includes `limiter_waits` and `limiter_remaining` when limiting is enabled

**Verify:** `cd backend && go test ./internal/obs/ -run 'Limiter|Interactive' -v` → PASS; `go build ./...` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests in `backend/internal/obs/limiter_test.go`**

```go
package obs

import (
	"context"
	"testing"
	"time"
)

func TestLimiterAllowsWithinBudget(t *testing.T) {
	l := newMinuteLimiter(2)
	base := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l.now = func() time.Time { return base }

	for i := 0; i < 2; i++ {
		if err := l.acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
}

func TestLimiterBlocksWhenExhausted(t *testing.T) {
	l := newMinuteLimiter(1)
	base := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l.now = func() time.Time { return base }

	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Exhausted: a cancelled context must return immediately with an error
	// instead of sleeping until the next window.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.acquire(ctx); err == nil {
		t.Fatal("expected context error when budget exhausted")
	}
	waits, remaining := l.stats()
	if waits != 1 {
		t.Errorf("waits = %d, want 1", waits)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
}

func TestLimiterWindowRolloverResetsBudget(t *testing.T) {
	current := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l := newMinuteLimiter(1)
	l.now = func() time.Time { return current }

	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	current = current.Add(time.Minute) // next window
	if err := l.acquire(context.Background()); err != nil {
		t.Fatalf("acquire after rollover: %v", err)
	}
}

func TestLimiterDisabledByZeroBudget(t *testing.T) {
	l := newMinuteLimiter(0)
	for i := 0; i < 100; i++ {
		if err := l.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInteractiveContextBypassesLimiter(t *testing.T) {
	ctx := Interactive(context.Background())
	if !isInteractive(ctx) {
		t.Fatal("expected interactive context to be detected")
	}
	if isInteractive(context.Background()) {
		t.Fatal("plain context must not be interactive")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/obs/ -run 'Limiter|Interactive' -v`
Expected: FAIL (compile error — types don't exist).

- [ ] **Step 3: Create `backend/internal/obs/interactive.go`**

```go
package obs

import "context"

type interactiveKey struct{}

// Interactive marks ctx so OBS requests made with it bypass the background
// rate limiter. Used for user-facing API requests, which must not queue
// behind background polling.
func Interactive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

func isInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveKey{}).(bool)
	return v
}
```

- [ ] **Step 4: Create `backend/internal/obs/limiter.go`**

```go
package obs

import (
	"context"
	"sync"
	"time"
)

// minuteLimiter is a fixed-window per-minute request budget. Background OBS
// requests acquire a slot before dispatch; once the window's budget is
// exhausted they block until the next clock minute opens. Requests are
// delayed, never dropped. A budget <= 0 disables limiting.
type minuteLimiter struct {
	mu     sync.Mutex
	budget int
	now    func() time.Time

	window time.Time // start of the current minute window
	used   int
	waits  int64 // cumulative count of acquisitions that had to block
}

func newMinuteLimiter(budget int) *minuteLimiter {
	return &minuteLimiter{budget: budget, now: time.Now}
}

// acquire blocks until a slot is available in the current minute window or
// ctx is cancelled.
func (l *minuteLimiter) acquire(ctx context.Context) error {
	if l.budget <= 0 {
		return nil
	}
	for {
		l.mu.Lock()
		now := l.now()
		windowStart := now.Truncate(time.Minute)
		if !windowStart.Equal(l.window) {
			l.window = windowStart
			l.used = 0
		}
		if l.used < l.budget {
			l.used++
			l.mu.Unlock()
			return nil
		}
		l.waits++
		wait := windowStart.Add(time.Minute).Sub(now)
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// stats returns the cumulative number of blocked acquisitions and the
// remaining budget in the current window.
func (l *minuteLimiter) stats() (waits, remaining int64) {
	if l.budget <= 0 {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.now().Truncate(time.Minute).Equal(l.window) {
		return l.waits, int64(l.budget)
	}
	return l.waits, int64(l.budget - l.used)
}
```

- [ ] **Step 5: Hook the limiter into `backend/internal/obs/client.go`**

Add the field and initialize it disabled (existing tests unaffected):

```go
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
	metrics  *obsMetrics
	limiter  *minuteLimiter

	pubMu    sync.Mutex
	pubCache map[string]PublishFlags
}
```

In `NewClient`, add `limiter: newMinuteLimiter(0),` to the struct literal.

Add the setter (called once at startup, before any goroutine uses the client):

```go
// SetMinuteBudget enables the background request limiter: at most n requests
// per clock minute; further background requests block until the next window.
// Interactive-tagged contexts (see Interactive) bypass the limiter.
// n <= 0 disables limiting. Not safe to call concurrently with requests —
// wire it at startup.
func (c *Client) SetMinuteBudget(n int) {
	c.limiter = newMinuteLimiter(n)
}
```

In `get`, `getFile`, and `post`, immediately before `c.metrics.inc(op)`:

```go
	if !isInteractive(ctx) {
		if err := c.limiter.acquire(ctx); err != nil {
			return nil, err
		}
	}
```

(In `post`, which returns only `error`, use `return err` instead of `return nil, err`.)

Extend `MetricsSnapshot`:

```go
// MetricsSnapshot returns a copy of the per-operation OBS request counts,
// plus limiter gauges when rate limiting is enabled.
func (c *Client) MetricsSnapshot() map[string]int64 {
	out := c.metrics.snapshot()
	if c.limiter.budget > 0 {
		waits, remaining := c.limiter.stats()
		out["limiter_waits"] = waits
		out["limiter_remaining"] = remaining
	}
	return out
}
```

- [ ] **Step 6: Tag API requests as interactive in `backend/internal/api/server.go`**

After `r.Use(middleware.Recoverer)`:

```go
	// All API requests are user-initiated: they bypass the OBS client's
	// background rate limiter so users never queue behind polling traffic.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(obs.Interactive(req.Context())))
		})
	})
```

- [ ] **Step 7: Wire the budget in `backend/cmd/obsboard/main.go`**

After `obsClient := obs.NewClient(...)`:

```go
	obsClient.SetMinuteBudget(cfg.OBS.MinuteRequestBudget)
```

- [ ] **Step 8: Run tests and commit**

Run: `cd backend && go build ./... && go test ./...`
Expected: all PASS.

```bash
git add backend/internal/obs/interactive.go backend/internal/obs/limiter.go backend/internal/obs/limiter_test.go backend/internal/obs/client.go backend/internal/api/server.go backend/cmd/obsboard/main.go
git commit -s -m "feat(obs): per-minute rate limiter for background requests

Fixed-window budget (obs.minute_request_budget, default 60/min ~ 86k/day).
Background requests block until the next window when exhausted; API
handler requests bypass via context tag. Limiter stats exposed through
MetricsSnapshot."
```

```json:metadata
{"files": ["backend/internal/obs/interactive.go", "backend/internal/obs/limiter.go", "backend/internal/obs/limiter_test.go", "backend/internal/obs/client.go", "backend/internal/api/server.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go build ./... && go test ./internal/obs/ -v", "acceptanceCriteria": ["exhausted budget blocks background requests, ctx cancel unblocks", "window rollover resets budget", "Interactive ctx bypasses limiter", "budget<=0 disables limiting", "MetricsSnapshot exposes limiter_waits/limiter_remaining"], "modelTier": "standard"}
```

---

### Task 5: Adaptive backoff in the working set

**Goal:** Working-set entries carry `{interval, nextDue}`; the scheduler dispatches only due packages; unchanged passes double the interval (30s → 5m cap), changed passes and wake signals reset it.

**Files:**
- Modify: `backend/internal/workingset/workingset.go` (full rework)
- Modify: `backend/internal/worker/worker.go` (`ProcessOnce` returns `changed`; `run()` passes it to `Done`)
- Modify: `backend/cmd/obsboard/main.go` (new `workingset.New` signature, `StartScheduler(ctx)`)
- Test: `backend/internal/workingset/workingset_test.go`, `backend/internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] `Done(key, changed=false)` doubles the interval up to `backoffMax`; `changed=true` resets to base
- [ ] `DispatchDue()` dispatches only entries with `nextDue <= now` and not inflight
- [ ] `Signal` resets the schedule and dispatches immediately; `Add` on an existing package resets its schedule without immediate dispatch (preserving today's dedup semantics)
- [ ] `ProcessOnce` returns true iff rollup state or any target's build state changed during the pass

**Verify:** `cd backend && go test ./internal/workingset/ ./internal/worker/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing backoff tests in `backend/internal/workingset/workingset_test.go`**

Add a fake clock helper and ladder tests (existing tests get the new constructor signature in Step 3):

```go
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func drain(t *testing.T, ws *workingset.WorkingSet) *model.Package {
	t.Helper()
	select {
	case p := <-ws.Dispatch():
		return p
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dispatch but nothing received")
		return nil
	}
}

func expectNoDispatch(t *testing.T, ws *workingset.WorkingSet) {
	t.Helper()
	select {
	case p := <-ws.Dispatch():
		t.Fatalf("unexpected dispatch of %s", p.Name)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBackoffDoublesOnUnchangedPass(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)                 // immediate dispatch on Add
	ws.Done("proj/pkg-a", false) // unchanged → interval doubles to 60s

	clock.Advance(31 * time.Second)
	ws.DispatchDue()
	expectNoDispatch(t, ws) // only 31s elapsed, due in 60s

	clock.Advance(30 * time.Second)
	ws.DispatchDue()
	drain(t, ws) // 61s elapsed ≥ 60s
}

func TestBackoffResetsOnChangedPass(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // 60s
	clock.Advance(61 * time.Second)
	ws.DispatchDue()
	drain(t, ws)
	ws.Done("proj/pkg-a", true) // changed → back to 30s

	clock.Advance(31 * time.Second)
	ws.DispatchDue()
	drain(t, ws)
}

func TestBackoffCapsAtMax(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	// 10 unchanged passes: 30s→1m→2m→4m→5m (capped)
	for i := 0; i < 10; i++ {
		ws.Done("proj/pkg-a", false)
		clock.Advance(5*time.Minute + time.Second)
		ws.DispatchDue()
		drain(t, ws)
	}
	ws.Done("proj/pkg-a", false)
	clock.Advance(4 * time.Minute)
	ws.DispatchDue()
	expectNoDispatch(t, ws) // capped at 5m, 4m is not enough
	clock.Advance(1*time.Minute + time.Second)
	ws.DispatchDue()
	drain(t, ws)
}

func TestSignalResetsBackoffAndDispatchesImmediately(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // backed off to 60s
	ws.Signal(pkg("proj", "pkg-a", model.RollupFinished))
	drain(t, ws) // immediate, no clock advance needed
}

func TestAddExistingResetsScheduleWithoutDispatch(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // 60s
	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	expectNoDispatch(t, ws) // Add on existing: no immediate dispatch...
	ws.DispatchDue()
	drain(t, ws) // ...but the schedule was reset to due-now
}
```

Note: these tests need `"sync"` added to the test file imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/workingset/ -v`
Expected: FAIL (compile error — `NewWithClock`, `DispatchDue`, two-arg `Done` don't exist).

- [ ] **Step 3: Rework `backend/internal/workingset/workingset.go`**

```go
package workingset

import (
	"context"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// Stats is a snapshot of working-set size.
type Stats struct {
	Total    int
	Inflight int
	ByState  map[string]int // rollup_state → count
}

// entry is a working-set member with its poll schedule. interval follows a
// backoff ladder: reset to base when a pass observes a change, doubled (up
// to max) when a pass observes nothing new.
type entry struct {
	pkg      *model.Package
	interval time.Duration
	nextDue  time.Time
}

type WorkingSet struct {
	mu       sync.Mutex
	entries  map[string]*entry
	inflight map[string]bool
	dispatch chan *model.Package

	base           time.Duration
	max            time.Duration
	batchThreshold int
	now            func() time.Time
}

// New creates a working set. base is the initial per-package poll interval
// (and the scheduler tick), max caps the backoff ladder. batchThreshold is
// the minimum number of due packages of one project that triggers a
// project-level batch fetch (used by the dispatcher; see Job).
func New(queueSize int, base, max time.Duration, batchThreshold int) *WorkingSet {
	return &WorkingSet{
		entries:        make(map[string]*entry),
		inflight:       make(map[string]bool),
		dispatch:       make(chan *model.Package, queueSize),
		base:           base,
		max:            max,
		batchThreshold: batchThreshold,
		now:            time.Now,
	}
}

// NewWithClock is New with an injectable clock, for tests.
func NewWithClock(queueSize int, base, max time.Duration, batchThreshold int, clock func() time.Time) *WorkingSet {
	ws := New(queueSize, base, max, batchThreshold)
	ws.now = clock
	return ws
}

// Seed inserts packages without dispatching; they become due immediately and
// the first scheduler tick picks them up.
func (ws *WorkingSet) Seed(pkgs []*model.Package) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	for _, p := range pkgs {
		key := p.Project + "/" + p.Name
		ws.entries[key] = &entry{pkg: p, interval: ws.base, nextDue: now}
	}
}

// Add inserts a package and dispatches it immediately. If the package is
// already present this is a wake signal: its schedule resets to due-now at
// the base interval (the stored, possibly enriched, package object is kept)
// but nothing is dispatched — the next scheduler tick handles it.
func (ws *WorkingSet) Add(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if e, exists := ws.entries[key]; exists {
		e.interval = ws.base
		e.nextDue = ws.now()
		return
	}
	ws.entries[key] = &entry{pkg: pkg, interval: ws.base, nextDue: ws.now()}
	ws.send(key, pkg)
}

// Signal replaces the stored package, resets its schedule, and dispatches
// immediately (unless in-flight). Used by the MQ consumer for real-time
// reactions.
func (ws *WorkingSet) Signal(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.entries[key] = &entry{pkg: pkg, interval: ws.base, nextDue: ws.now()}
	ws.send(key, pkg)
}

func (ws *WorkingSet) Remove(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.entries, key)
	delete(ws.inflight, key)
}

// Done marks a package as no longer in-flight and advances its schedule:
// a changed pass resets the interval to base, an unchanged pass doubles it
// up to max. The next pass is due interval from now.
func (ws *WorkingSet) Done(key string, changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.inflight, key)
	e, ok := ws.entries[key]
	if !ok {
		return // removed while in-flight
	}
	if changed {
		e.interval = ws.base
	} else {
		e.interval *= 2
		if e.interval > ws.max {
			e.interval = ws.max
		}
	}
	e.nextDue = ws.now().Add(e.interval)
}

func (ws *WorkingSet) Dispatch() <-chan *model.Package {
	return ws.dispatch
}

// Stats returns a snapshot of the current working set under the lock.
func (ws *WorkingSet) Stats() Stats {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	s := Stats{Total: len(ws.entries), Inflight: len(ws.inflight), ByState: make(map[string]int)}
	for _, e := range ws.entries {
		s.ByState[string(e.pkg.RollupState)]++
	}
	return s
}

// DispatchDue enqueues every entry whose nextDue has passed and that is not
// already in-flight. Called by the scheduler tick; exported for tests.
func (ws *WorkingSet) DispatchDue() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	for key, e := range ws.entries {
		if e.nextDue.After(now) {
			continue
		}
		ws.send(key, e.pkg)
	}
}

// StartScheduler ticks at the base interval and dispatches due packages.
func (ws *WorkingSet) StartScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(ws.base)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws.DispatchDue()
			}
		}
	}()
}

// send attempts a non-blocking enqueue. Drops the send if the package is
// already in-flight (being processed by a worker) or if the channel is full
// (retried on the next tick). Must be called with ws.mu held.
func (ws *WorkingSet) send(key string, pkg *model.Package) {
	if ws.inflight[key] {
		return
	}
	select {
	case ws.dispatch <- pkg:
		ws.inflight[key] = true
	default:
	}
}
```

- [ ] **Step 4: Update `backend/internal/worker/worker.go`**

`ProcessOnce` returns whether the pass observed a change; the run loop feeds it to `Done`:

```go
		case pkg, ok := <-p.ws.Dispatch():
			if !ok {
				return
			}
			key := pkg.Project + "/" + pkg.Name
			changed := p.ProcessOnce(ctx, pkg, nil)
			p.ws.Done(key, changed)
```

```go
// ProcessOnce runs the task chain for pkg (devTasks or releaseTasks based on
// pkg.IsRelease), upserts to DB, emits build events and SSE for real-time
// packages only, and removes pkg from the working set when rollup reaches
// published. Returns true when the pass observed a rollup or target build
// state change — the scheduler uses this to reset or grow the poll backoff.
// Exported for testing.
func (p *Pool) ProcessOnce(ctx context.Context, pkg *model.Package, env *obs.Env) bool {
```

After the task loop (right before the publish-flags block), compute the result; add the return at the end of the function:

```go
	changed := prevRollupState != pkg.RollupState ||
		targetStatesChanged(oldTargets, pkg.Targets)
```

…(existing body unchanged)…

```go
	if pkg.Settled || (flagsKnown && pkg.IsContainer != nil && obs.Parkable(pkg)) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
	return changed
}

// targetStatesChanged reports whether any target's build state differs
// between two snapshots, or the target set itself changed. Details and
// enrichment fields are deliberately ignored: backoff cares about build
// progress, not metadata refinement.
func targetStatesChanged(old, new []model.Target) bool {
	if len(old) != len(new) {
		return true
	}
	prev := make(map[string]string, len(old))
	for _, t := range old {
		prev[t.Repo+"/"+t.Arch] = t.State
	}
	for _, t := range new {
		s, ok := prev[t.Repo+"/"+t.Arch]
		if !ok || s != t.State {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Update `backend/cmd/obsboard/main.go`**

```go
	ws := workingset.New(cfg.WorkerPool.QueueSize, cfg.WorkerPool.PollInterval,
		cfg.WorkerPool.BackoffMax, cfg.WorkerPool.BatchThreshold)
	ws.Seed(activePkgs)
```

and after `pool.Start(ctx)`:

```go
	ws.StartScheduler(ctx)
```

- [ ] **Step 6: Fix remaining call sites (mechanical)**

- `backend/internal/workingset/workingset_test.go`: every `workingset.New(10)` → `workingset.New(10, 30*time.Second, 5*time.Minute, 4)`; every `ws.Done("…")` → `ws.Done("…", false)`.
- `backend/internal/worker/worker_test.go`: same `workingset.New` update; `ProcessOnce` call sites now return a bool (assign or ignore).

- [ ] **Step 7: Run tests and commit**

Run: `cd backend && go build ./... && go test ./...`
Expected: all PASS.

```bash
git add backend/internal/workingset/workingset.go backend/internal/workingset/workingset_test.go backend/internal/worker/worker.go backend/internal/worker/worker_test.go backend/cmd/obsboard/main.go
git commit -s -m "feat(workingset): adaptive per-package poll backoff

Unchanged passes double the poll interval (30s up to backoff_max 5m);
changed passes, MQ signals, and poller re-adds reset it. Cuts steady
storm polling ~10x while keeping real-time reaction to MQ events."
```

```json:metadata
{"files": ["backend/internal/workingset/workingset.go", "backend/internal/workingset/workingset_test.go", "backend/internal/worker/worker.go", "backend/internal/worker/worker_test.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go test ./internal/workingset/ ./internal/worker/ -v", "acceptanceCriteria": ["unchanged Done doubles interval up to cap", "changed Done resets to base", "DispatchDue only dispatches due entries", "Signal resets and dispatches immediately", "ProcessOnce returns changed flag"], "modelTier": "standard"}
```

---

### Task 6: Threshold-based project-level batch dispatch

**Goal:** The dispatch channel carries `Job`s; at each tick, due packages of the same project ≥ threshold become one batch job whose worker makes a single project-level `_result` call and injects it into every package's pass.

**Files:**
- Modify: `backend/internal/workingset/workingset.go` (Job type, channel, grouping in DispatchDue)
- Modify: `backend/internal/worker/worker.go` (run loop → ProcessJob)
- Modify: `backend/internal/obs/client.go:358-385` (`BuildResults` carries `Details`)
- Test: `backend/internal/workingset/workingset_test.go`, `backend/internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] `DispatchDue` groups due packages by project; groups ≥ threshold become one `Job{ProjectFetch: true}`; smaller groups become single-package jobs
- [ ] Batch worker makes exactly one `_result` request for the whole job and every package's states are applied
- [ ] Project fetch failure → every package in the job falls back to per-package fetches
- [ ] `Add`/`Signal` immediate dispatches are single-package jobs

**Verify:** `cd backend && go test ./internal/workingset/ ./internal/worker/ -v` → PASS including `TestDispatchDueBatchesByProject` and `TestProcessJobBatchFetch*`

**Steps:**

- [ ] **Step 1: Write the failing grouping test in `backend/internal/workingset/workingset_test.go`**

The channel type changes in this task, so `drain`/`expectNoDispatch` are updated here too:

```go
func drain(t *testing.T, ws *workingset.WorkingSet) workingset.Job {
	t.Helper()
	select {
	case j := <-ws.Dispatch():
		return j
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dispatch but nothing received")
		return workingset.Job{}
	}
}

func expectNoDispatch(t *testing.T, ws *workingset.WorkingSet) {
	t.Helper()
	select {
	case j := <-ws.Dispatch():
		t.Fatalf("unexpected dispatch of job with %d pkgs", len(j.Pkgs))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDispatchDueBatchesByProject(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 3, clock.Now)

	ws.Seed([]*model.Package{
		pkg("proj-a", "p1", model.RollupBuilding),
		pkg("proj-a", "p2", model.RollupBuilding),
		pkg("proj-a", "p3", model.RollupBuilding),
		pkg("proj-b", "q1", model.RollupBuilding),
	})
	ws.DispatchDue()

	var batch *workingset.Job
	var singles []workingset.Job
	for i := 0; i < 2; i++ {
		j := drain(t, ws)
		if j.ProjectFetch {
			batch = &j
		} else {
			singles = append(singles, j)
		}
	}
	if batch == nil {
		t.Fatal("expected one batch job for proj-a")
	}
	if batch.Project != "proj-a" || len(batch.Pkgs) != 3 {
		t.Errorf("batch = %s/%d pkgs, want proj-a/3", batch.Project, len(batch.Pkgs))
	}
	if len(singles) != 1 || len(singles[0].Pkgs) != 1 || singles[0].Pkgs[0].Project != "proj-b" {
		t.Errorf("expected one single-package job for proj-b, got %+v", singles)
	}
	expectNoDispatch(t, ws) // everything dispatched exactly once
}
```

Existing tests asserting on dispatched packages unwrap the job: `p := <-ws.Dispatch()` becomes `j := <-ws.Dispatch()` with assertions on `j.Pkgs[0]` (or use the updated `drain` helper: `drain(t, ws).Pkgs[0]`).

- [ ] **Step 2: Write the failing worker batch tests in `backend/internal/worker/worker_test.go`**

```go
func TestProcessJobBatchFetchesProjectOnce(t *testing.T) {
	var resultHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_meta"):
			fmt.Fprint(w, `<project name="proj"/>`)
		case strings.HasSuffix(r.URL.Path, "/_result"):
			resultHits.Add(1)
			fmt.Fprint(w, `<resultlist>
              <result project="proj" repository="repo" arch="x86_64" state="published">
                <status package="pkg-a" code="succeeded"/>
                <status package="pkg-b" code="succeeded"/>
              </result>
            </resultlist>`)
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 2)
	c := obs.NewClient(ts.URL, "u", "p")

	pkgA := &model.Package{Project: "proj", Name: "pkg-a", RollupState: model.RollupBuilding,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "building"}}}
	pkgB := &model.Package{Project: "proj", Name: "pkg-b", RollupState: model.RollupBuilding,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "building"}}}

	p := worker.NewPool(1, []worker.Task{obs.BuildStateTask{}}, nil, c, db, h, ws, nil)
	p.ProcessJob(context.Background(), workingset.Job{
		Project: "proj", ProjectFetch: true, Pkgs: []*model.Package{pkgA, pkgB},
	})

	if got := resultHits.Load(); got != 1 {
		t.Errorf("_result requests = %d, want exactly 1 for the whole batch", got)
	}
	if pkgA.RollupState != model.RollupSucceeded || pkgB.RollupState != model.RollupSucceeded {
		t.Errorf("rollups = %s/%s, want succeeded/succeeded", pkgA.RollupState, pkgB.RollupState)
	}
}

func TestProcessJobBatchFallsBackPerPackageOnFetchError(t *testing.T) {
	var projectResultHits, packageResultHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_meta"):
			fmt.Fprint(w, `<project name="proj"/>`)
		case strings.HasSuffix(r.URL.Path, "/_result") && r.URL.Query().Get("package") == "":
			projectResultHits.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/_result"):
			packageResultHits.Add(1)
			fmt.Fprintf(w, `<resultlist>
              <result project="proj" repository="repo" arch="x86_64" state="published">
                <status package="%s" code="succeeded"/>
              </result>
            </resultlist>`, r.URL.Query().Get("package"))
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 2)
	c := obs.NewClient(ts.URL, "u", "p")

	pkgA := &model.Package{Project: "proj", Name: "pkg-a", RollupState: model.RollupBuilding,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "building"}}}
	pkgB := &model.Package{Project: "proj", Name: "pkg-b", RollupState: model.RollupBuilding,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "building"}}}

	p := worker.NewPool(1, []worker.Task{obs.BuildStateTask{}}, nil, c, db, h, ws, nil)
	p.ProcessJob(context.Background(), workingset.Job{
		Project: "proj", ProjectFetch: true, Pkgs: []*model.Package{pkgA, pkgB},
	})

	if projectResultHits.Load() != 1 {
		t.Errorf("project _result hits = %d, want 1", projectResultHits.Load())
	}
	if packageResultHits.Load() != 2 {
		t.Errorf("per-package _result hits = %d, want 2 (fallback)", packageResultHits.Load())
	}
	if pkgA.RollupState != model.RollupSucceeded || pkgB.RollupState != model.RollupSucceeded {
		t.Errorf("rollups = %s/%s, want succeeded/succeeded", pkgA.RollupState, pkgB.RollupState)
	}
}
```

Note: `client.PackageBuildResults` requests `_result?package=<name>` while the project-level `BuildResults` requests bare `_result` — the query-param check above distinguishes them.

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd backend && go test ./internal/workingset/ ./internal/worker/ -run 'Batch|ProcessJob' -v`
Expected: FAIL (compile error — `Job`, `ProcessJob` don't exist).

- [ ] **Step 4: Add Job and grouping to `backend/internal/workingset/workingset.go`**

Add the type:

```go
// Job is a unit of work for the worker pool: one or more packages of the
// same project. ProjectFetch marks that the worker should make a single
// project-level _result call and process every package with the result.
type Job struct {
	Project      string
	ProjectFetch bool
	Pkgs         []*model.Package
}
```

Change the channel: `dispatch chan Job` (in the struct and both constructors: `dispatch: make(chan Job, queueSize)`), and:

```go
func (ws *WorkingSet) Dispatch() <-chan Job {
	return ws.dispatch
}
```

Replace `send` with job-aware sending and group in `DispatchDue`:

```go
// DispatchDue enqueues every entry whose nextDue has passed and that is not
// already in-flight. Due packages of the same project are batched into one
// project-fetch job when the group reaches batchThreshold; below that,
// per-package fetches are cheaper than a project-level _result. Called by
// the scheduler tick; exported for tests.
func (ws *WorkingSet) DispatchDue() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	byProject := make(map[string][]*model.Package)
	for key, e := range ws.entries {
		if e.nextDue.After(now) || ws.inflight[key] {
			continue
		}
		byProject[e.pkg.Project] = append(byProject[e.pkg.Project], e.pkg)
	}
	for project, pkgs := range byProject {
		if ws.batchThreshold > 0 && len(pkgs) >= ws.batchThreshold {
			ws.sendJob(Job{Project: project, ProjectFetch: true, Pkgs: pkgs})
			continue
		}
		for _, p := range pkgs {
			ws.sendJob(Job{Pkgs: []*model.Package{p}})
		}
	}
}

// sendJob attempts a non-blocking enqueue and marks every package in the job
// as in-flight on success. Drops the job if the channel is full — the
// packages stay due and are retried on the next tick. Must be called with
// ws.mu held. Callers must ensure no package in the job is already in-flight.
func (ws *WorkingSet) sendJob(job Job) {
	select {
	case ws.dispatch <- job:
		for _, p := range job.Pkgs {
			ws.inflight[p.Project+"/"+p.Name] = true
		}
	default:
	}
}
```

`Add` and `Signal` immediate sends become (keeping their in-flight guard):

```go
	if !ws.inflight[key] {
		ws.sendJob(Job{Pkgs: []*model.Package{pkg}})
	}
```

Delete the old `send` method.

- [ ] **Step 5: Rework the worker run loop into ProcessJob in `backend/internal/worker/worker.go`**

```go
func (p *Pool) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.ws.Dispatch():
			if !ok {
				return
			}
			p.ProcessJob(ctx, job)
		}
	}
}

// ProcessJob runs the task chain for every package in job. For project-fetch
// jobs it makes one project-level _result call and injects the response into
// each package's pass; if that fetch fails, every package falls back to
// per-package fetches. Exported for testing.
func (p *Pool) ProcessJob(ctx context.Context, job workingset.Job) {
	var envs map[string]*obs.Env
	if job.ProjectFetch && p.client != nil {
		results, repoStates, err := p.client.BuildResults(ctx, job.Project)
		if err != nil {
			slog.Warn("worker: project batch fetch, falling back to per-package",
				"project", job.Project, "err", err)
		} else {
			byPkg := make(map[string][]obs.PackageBuildState)
			for _, r := range results {
				byPkg[r.Package] = append(byPkg[r.Package], r)
			}
			envs = make(map[string]*obs.Env, len(job.Pkgs))
			for _, pkg := range job.Pkgs {
				if states, ok := byPkg[pkg.Name]; ok {
					envs[pkg.Name] = &obs.Env{BuildStates: states, RepoStates: repoStates}
				}
			}
		}
	}
	for _, pkg := range job.Pkgs {
		key := pkg.Project + "/" + pkg.Name
		if ctx.Err() != nil {
			p.ws.Done(key, false) // shutdown: release in-flight without processing
			continue
		}
		changed := p.ProcessOnce(ctx, pkg, envs[pkg.Name])
		p.ws.Done(key, changed)
	}
}
```

(A package missing from the project response gets a nil env — `envs[pkg.Name]` on a nil or missing key returns nil — so BuildStateTask falls back to its per-package fetch, which also handles packages deleted from OBS between scheduling and processing.)

Also fix a parity gap in `backend/internal/obs/client.go` `BuildResults` (line ~375): the per-package fetch carries each status's `Details` but the project-level fetch drops it, which would wipe failure details on every batch pass. Add the field:

```go
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
			})
```

- [ ] **Step 6: Fix remaining call sites (mechanical)**

`backend/internal/workingset/workingset_test.go` and `backend/internal/worker/worker_test.go`: receives from `ws.Dispatch()` now yield `workingset.Job` — unwrap with `.Pkgs[0]` where a single package is asserted.

- [ ] **Step 7: Run the full suite and commit**

Run: `cd backend && go build ./... && go test ./...`
Expected: all PASS.

```bash
git add backend/internal/workingset/workingset.go backend/internal/workingset/workingset_test.go backend/internal/worker/worker.go backend/internal/worker/worker_test.go backend/internal/obs/client.go
git commit -s -m "feat(worker): project-level batch fetch for dense working sets

When >= batch_threshold packages of one project are due, one project
_result call serves the whole group (build states + repo publish
states), replacing per-package fetches. Falls back per package when the
project fetch fails."
```

```json:metadata
{"files": ["backend/internal/workingset/workingset.go", "backend/internal/workingset/workingset_test.go", "backend/internal/worker/worker.go", "backend/internal/worker/worker_test.go", "backend/internal/obs/client.go"], "verifyCommand": "cd backend && go test ./internal/workingset/ ./internal/worker/ -v", "acceptanceCriteria": ["due packages grouped by project with threshold", "batch job makes exactly one _result call", "fetch failure falls back per package", "Add/Signal dispatch single-package jobs", "BuildResults carries status Details"], "modelTier": "standard"}
```

---

## Execution order and dependencies

```
Task 1 (Env thread-through)
  ├─→ Task 2 (tasks consume Env)
  └─→ Task 5 (backoff; needs ProcessOnce signature)   ←─ Task 3 (config)
Task 3 (config)
  └─→ Task 4 (limiter; needs MinuteRequestBudget)
Task 5 + Task 2
  └─→ Task 6 (batching; needs Job scheduling and Env consumption)
```

Suggested serial order: 1 → 2 → 3 → 4 → 5 → 6.

## Post-deploy verification (manual, after prod rollout)

Compare per-op counts from `MetricsSnapshot()` (telemetry endpoint) over a day against the pre-change baseline: `package_build_results` and `repo_publish_states` should drop ~10–50x during active periods, `build_results` (project-level) grows modestly, `limiter_waits` should stay 0 outside storms.
