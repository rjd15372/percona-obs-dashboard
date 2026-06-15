# Working Set Architecture Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task.

**Goal:** Replace the current monolithic polling model with a working set of actively-building packages and an async worker pool that enriches each package's state in a modular, extensible way — polling only packages that need attention rather than all packages every tick.

**Architecture:** A `WorkingSet` maintains an in-memory map of packages whose `rollup_state != succeeded`. The existing broad poller retains its discovery role (adding newly-non-succeeded packages to the set) while a fixed worker pool drains a dispatch channel, running a registered list of `Task` implementations per package. Workers remove packages from the set when they reach `succeeded`. The dispatch channel is fed by both an interval scheduler and immediate signals from the MQ consumer and poller.

**Tech Stack:** Go standard library. No new dependencies, no new DB tables (working set is seeded from existing packages table on startup), no new REST or SSE endpoints.

**User decisions (already made):**
- Broad poller keeps its discovery role (Option A); per-package detail polling moves to workers.
- Working set is derived from the existing DB on startup (no separate working set table); an index on `rollup_state` is added for the startup query.
- Worker pool size is configurable (`worker_pool.size`).
- Workers poll on a fixed interval (`worker_pool.interval`) AND are triggered immediately by MQ events (`ws.Signal`) — hybrid A+C.
- Dispatch channel is configurable (`worker_pool.queue_size`), default 512.
- `Task` is the interface name (not `Enricher`).

---

## Scope

| File | Change |
|------|--------|
| `backend/internal/workingset/workingset.go` | New — working set map, dispatch channel, scheduler |
| `backend/internal/worker/worker.go` | New — `Task` interface, `Pool` struct |
| `backend/internal/obs/tasks.go` | New — `BuildStateTask`, `BlockedReasonTask` |
| `backend/internal/store/packages.go` | Add `GetActivePackages()` |
| `backend/internal/config/config.go` | Add `WorkerPool` config section |
| `backend/internal/obs/poller.go` | Call `ws.Add(pkg)` on state change; remove `EnrichBlockedTargets` call |
| `backend/internal/mq/consumer.go` | Call `ws.Signal(pkg)` after upsert; remove `EnrichBlockedTargets` call |
| `backend/cmd/obsboard/main.go` | Wire new components: seed working set, register tasks, start pool |

No other files change. Existing REST endpoints, SSE stream, and data model are untouched.

---

## Components

### WorkingSet (`internal/workingset/workingset.go`)

```go
type WorkingSet struct {
    mu       sync.RWMutex
    packages map[string]*model.Package  // key: "project/name"
    dispatch chan *model.Package
}

func New(queueSize int) *WorkingSet
func (ws *WorkingSet) Seed(pkgs []*model.Package)
func (ws *WorkingSet) Add(pkg *model.Package)
func (ws *WorkingSet) Signal(pkg *model.Package)
func (ws *WorkingSet) Remove(key string)
func (ws *WorkingSet) Dispatch() <-chan *model.Package
func (ws *WorkingSet) StartScheduler(ctx context.Context, interval time.Duration)
```

**Key:** `project + "/" + name`

**`Add(pkg)`:** Acquires write lock. If key not present, inserts into map and attempts a non-blocking send to `dispatch`. If key already present, no-op — the periodic scheduler handles re-queuing for packages already in the set.

**`Signal(pkg)`:** Acquires write lock. Inserts into map if not present. Always attempts a non-blocking send to `dispatch` (immediate trigger regardless of membership).

**`Remove(key)`:** Acquires write lock, deletes from map. Called by worker when `pkg.RollupState == succeeded` after task run.

**`StartScheduler(ctx, interval)`:** Spawns a goroutine with a `time.Ticker`. On each tick: acquires read lock, iterates map, non-blocking send for each package. Exits when `ctx` is cancelled.

**Non-blocking send pattern** (used by Add, Signal, Scheduler):
```go
select {
case ws.dispatch <- pkg:
default:
    // channel full — dropped; scheduler recovers on next tick
}
```

---

### Worker Pool (`internal/worker/worker.go`)

```go
type Task interface {
    Run(ctx context.Context, client *obs.Client, pkg *model.Package) error
}

type Pool struct {
    size   int
    tasks  []Task
    client *obs.Client
    store  *store.Store
    hub    *hub.Hub
    ws     *workingset.WorkingSet
}

func NewPool(size int, tasks []Task, client *obs.Client, store *store.Store, hub *hub.Hub, ws *workingset.WorkingSet) *Pool
func (p *Pool) Start(ctx context.Context)
```

**`Start(ctx)`:** Spawns `size` goroutines. Each runs:
1. Read `pkg` from `ws.Dispatch()` (blocks until item available or ctx cancelled)
2. Run each `Task.Run(ctx, client, pkg)` in sequence
   - On error: log warning with task name, package name, error; continue to next task
3. Call `store.UpsertPackageState(pkg)` + `hub.Notify(hub.PackageUpdate(pkg))`
4. If `pkg.RollupState == model.RollupStateSucceeded` → `ws.Remove(pkg.Project + "/" + pkg.Name)`
5. Loop back to step 1

Goroutines exit when `ws.Dispatch()` is closed or `ctx` is cancelled.

---

### Built-in Tasks (`internal/obs/tasks.go`)

```go
// BuildStateTask fetches current build results for the specific package from OBS
// and updates pkg.Targets, pkg.RollupState, pkg.OKTargets, pkg.TotalTargets.
type BuildStateTask struct{}
func (t BuildStateTask) Run(ctx context.Context, client *Client, pkg *model.Package) error

// BlockedReasonTask populates BlockedBy on blocked targets.
// Wraps the existing EnrichBlockedTargets function.
type BlockedReasonTask struct{}
func (t BlockedReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
    EnrichBlockedTargets(ctx, client, pkg)
    return nil
}
```

`BuildStateTask.Run` calls the OBS `/build/{project}/_result?package={name}` endpoint (per-package variant of the existing broad result fetch), parses the XML, and updates the package in place — same logic the poller uses today, scoped to one package.

---

### Config (`internal/config/config.go`)

```go
type WorkerPoolConfig struct {
    Size      int           `env:"WORKER_POOL_SIZE"      default:"5"`
    Interval  time.Duration `env:"WORKER_POOL_INTERVAL"  default:"30s"`
    QueueSize int           `env:"WORKER_POOL_QUEUE_SIZE" default:"512"`
}
```

`config.yaml.example` addition:
```yaml
worker_pool:
  size: 5
  interval: 30s
  queue_size: 512
```

---

### Store addition (`internal/store/packages.go`)

```go
// GetActivePackages returns all packages with rollup_state != 'succeeded'.
// Used to seed the working set on startup.
func GetActivePackages(db *sql.DB) ([]*model.Package, error)
```

A DB index on `rollup_state` is added in the schema migration:
```sql
CREATE INDEX IF NOT EXISTS idx_packages_rollup_state ON packages(rollup_state);
```

---

## Changes to Existing Components

### `obs/poller.go`

After `store.UpsertPackageState(pkg)` in `tick()`, add:
```go
ws.Add(pkg)
```

Remove the existing `EnrichBlockedTargets(ctx, p.client, pkg)` call — enrichment is now handled by `BlockedReasonTask` in the worker pool.

The poller's `Poller` struct gains a `ws *workingset.WorkingSet` field; `NewPoller` is updated to accept it.

### `mq/consumer.go`

After `store.UpsertPackageState(pkg)` in the package build event branch, add:
```go
ws.Signal(pkg)
```

Remove the existing `obs.EnrichBlockedTargets(ctx, c.obsClient, pkg)` call. The consumer still calls `store.UpsertPackageState` and `hub.Notify` immediately (for real-time UI update), then signals the working set for the worker follow-up.

The `Consumer` struct gains a `ws *workingset.WorkingSet` field; `NewConsumer` is updated to accept it.

### `cmd/obsboard/main.go`

Startup sequence addition:
```go
// Seed working set from DB
activePkgs, err := store.GetActivePackages(db)
// handle err
ws := workingset.New(cfg.WorkerPool.QueueSize)
ws.Seed(activePkgs)

// Register tasks
tasks := []worker.Task{
    obs.BuildStateTask{},
    obs.BlockedReasonTask{},
}

// Start worker pool
pool := worker.NewPool(cfg.WorkerPool.Size, tasks, obsClient, db, hub, ws)
pool.Start(ctx)

// Start working set scheduler
ws.StartScheduler(ctx, cfg.WorkerPool.Interval)

// Pass ws to poller and consumer
poller := obs.NewPoller(..., ws)
consumer := mq.NewConsumer(..., ws)
```

---

## Data Flow

```
Startup
  store.GetActivePackages() → ws.Seed(pkgs)

Broad Poller (discovery, unchanged interval)
  → detects state change → ws.Add(pkg)
    if new to set: insert into map + dispatch immediately
    if already in set: no-op (scheduler re-queues)

MQ Consumer (real-time events)
  → store.UpsertPackageState() + hub.Notify()  ← immediate UI update
  → ws.Signal(pkg)                              ← always dispatches immediately

WorkingSet Scheduler (every worker_pool.interval)
  → non-blocking send for every package in the map → dispatch channel

Worker Pool (worker_pool.size goroutines)
  ← reads from dispatch channel
  → BuildStateTask.Run()      ← fetch current state from OBS
  → BlockedReasonTask.Run()   ← fetch blocked reason if applicable
  → store.UpsertPackageState() + hub.Notify()
  → if RollupState == succeeded → ws.Remove(key)
```

---

## Error Handling

- **Task error:** Worker logs a warning (task name, package, error) and continues to next task. Package stays in working set and is retried on next scheduler tick or signal.
- **Dispatch channel full:** Non-blocking send drops silently. Package will be picked up on the next scheduler tick — no work is permanently lost.
- **Startup DB error:** `GetActivePackages` failure is fatal — the working set cannot be seeded. Process exits with error.
- **Worker context cancellation:** Goroutines exit cleanly when `ctx` is cancelled. In-flight `Task.Run` calls complete before the goroutine exits.
- **DB inconsistency on restart:** DB is the authoritative recovery point. Working set is seeded from it; workers correct any stale data on first run.

## What Does Not Change

- All existing REST endpoints are untouched.
- The SSE stream and hub are untouched — `PackageUpdate` events continue to flow through unchanged.
- The `Target` and `Package` data models are untouched.
- The broad poller's discovery logic and interval are untouched.
- The MQ consumer's immediate store write + hub notify are untouched.
