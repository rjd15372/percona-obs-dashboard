# MQ Build Parking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Park packages that are waiting only on build completions (remove from the working set) and wake them with MQ `build_success`/`build_fail`/`build_unchanged` events, with the poller as loss-tolerant fallback.

**Architecture:** A pure `Parkable(pkg, flags)` helper (sibling to `Settled`) decides parking: every active target is either building-with-reason or inert-succeeded (`Published` or non-publishing repo), with at least one building. The worker removes parked packages next to the settled removal (in-memory only — `settled` stays 0 so restarts re-seed and re-park). The MQ consumer stops dropping `build_unchanged` and maps it to target state `"finished"` so the wake pass observes the `finished→succeeded` transition and build events fire normally.

**Tech Stack:** Go, `httptest`, existing `internal/obs` / `internal/mq` / `internal/worker` patterns. `settled_test.go` (internal package `obs`) already has `publishFlagsForTest()`; `worker_test.go` is external `worker_test`; `consumer_test.go` is internal package `mq`.

**User decisions (already made):**
- Park scope: "building + succeeded/published" — building-with-reason + inert-succeeded targets; **no scheduled parking**.
- Succeeded-unpublished in a *publishing* repo is NOT parkable (missed `repo.published` has no poller fallback — would strand the package).
- Parking is in-memory only (does not set `settled`; restart re-seeds → first pass re-parks).
- `build_unchanged` becomes a wake signal, mapped to `"finished"` (not `succeeded`) so events survive.
- No new working-set machinery — `ws.Remove` to park, existing `Signal`/`Add` to wake.
- Spec approved: `docs/superpowers/specs/2026-07-06-mq-build-parking-design.md` (`3843392`).

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `backend/internal/obs/parkable.go` (new) | `Parkable(pkg, flags)` pure helper | 1 |
| `backend/internal/obs/parkable_test.go` (new) | table tests (reuses `publishFlagsForTest`) | 1 |
| `backend/internal/worker/worker.go` | park removal in `ProcessOnce` | 2 |
| `backend/internal/worker/worker_test.go` | park/retain behaviour tests | 2 |
| `backend/internal/mq/consumer.go` | `build_unchanged` wake + `"finished"` mapping | 3 |
| `backend/internal/mq/consumer_test.go` | wake + mapping tests | 3 |

---

### Task 1: `Parkable` helper

**Goal:** A pure function deciding when a package waits only on MQ-announced build completions.

**Files:**
- Create: `backend/internal/obs/parkable.go`
- Create: `backend/internal/obs/parkable_test.go`

**Acceptance Criteria:**
- [ ] `building` target with `BuildReason != ""` is parkable; building without reason is not.
- [ ] `succeeded` target is inert (parkable alongside building) iff `Published` OR `!flags.Publishes(repo)`; succeeded-unpublished-publishing blocks parking.
- [ ] Any other active state (scheduled/blocked/finished/broken/failed/unresolvable) blocks parking.
- [ ] `skipState` targets (disabled/excluded/locked) are ignored; result requires ≥1 building target (all-inert or empty → false).

**Verify:** `cd backend && go test ./internal/obs/ -run TestParkable -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** — create `backend/internal/obs/parkable_test.go` (internal package `obs`; `publishFlagsForTest()` from `settled_test.go` gives flags where `UBI_8` does not publish and everything else does):

```go
package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestParkable(t *testing.T) {
	flags := publishFlagsForTest() // UBI_8 does not publish; other repos do

	pkg := func(targets ...model.Target) *model.Package {
		return &model.Package{RollupState: model.RollupBuilding, Targets: targets}
	}
	building := func(reason string) model.Target {
		return model.Target{Repo: "images", Arch: "x86_64", State: "building", BuildReason: reason}
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"all building with reasons", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "building", BuildReason: "meta change"}), true},
		{"building without reason", pkg(building("")), false},
		{"building + succeeded published", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "succeeded", Published: true}), true},
		{"building + succeeded non-publishing repo", pkg(building("meta change"), {Repo: "UBI_8", Arch: "x86_64", State: "succeeded"}), true},
		{"building + succeeded unpublished publishing repo", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "succeeded"}), false},
		{"building + scheduled", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "scheduled", BuildReason: "meta change"}), false},
		{"building + blocked", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "blocked"}), false},
		{"building + finished", pkg(building("meta change"), {Repo: "images", Arch: "aarch64", State: "finished"}), false},
		{"all inert no building", pkg(model.Target{Repo: "UBI_8", Arch: "x86_64", State: "succeeded"}), false},
		{"skip-state ignored", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "disabled"}), true},
		{"no targets", pkg(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parkable(tc.pkg, flags); got != tc.want {
				t.Fatalf("Parkable = %v, want %v", got, tc.want)
			}
		})
	}
}
```

NOTE: composite-literal elements inside `pkg(...)` calls need the `model.Target{...}` type prefix (shown inconsistently above for brevity in two rows — write ALL of them as full `model.Target{...}` literals so the file compiles).

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestParkable -v`
Expected: FAIL — `Parkable` undefined.

- [ ] **Step 3: Create `backend/internal/obs/parkable.go`**

```go
package obs

import "github.com/percona/obs-dashboard/internal/model"

// Parkable reports whether pkg is waiting only on build completions that MQ
// will announce (build_success/build_fail/build_unchanged), with the poller as
// loss-tolerant fallback. A parkable package can leave the working set and be
// re-added by the wake signal — nothing about it needs active polling.
//
// A target qualifies when it is:
//   - building with its BuildReason already fetched (enrichment complete), or
//   - succeeded and inert: already Published, or in a repo that never
//     publishes. Succeeded-unpublished in a publishing repo does NOT qualify —
//     publication is only detected by polling (a missed repo.published event
//     has no poller fallback), so those packages keep polling.
//
// At least one building target is required: an all-inert package is Settled's
// territory, and anything else (scheduled, blocked, finished, broken, …)
// still needs the 30s poll.
func Parkable(pkg *model.Package, flags PublishFlags) bool {
	hasBuilding := false
	for _, t := range pkg.Targets {
		if skipState(t.State) {
			continue
		}
		switch {
		case t.State == "building" && t.BuildReason != "":
			hasBuilding = true
		case t.State == "succeeded" && (t.Published || !flags.Publishes(t.Repo)):
			// inert: nothing pending for this target
		default:
			return false
		}
	}
	return hasBuilding
}
```

- [ ] **Step 4: Run test** — `cd backend && go test ./internal/obs/ -run TestParkable -v` → PASS; also `go test ./internal/obs/`.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/parkable.go internal/obs/parkable_test.go
git commit -s -m "feat(obs): Parkable helper for MQ-driven build waiting"
```

```json:metadata
{"files": ["backend/internal/obs/parkable.go", "backend/internal/obs/parkable_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestParkable -v && go test ./internal/obs/", "acceptanceCriteria": ["building-with-reason parkable; without reason not", "succeeded inert iff Published or non-publishing repo; unpublished-publishing blocks", "any other active state blocks", "skipState ignored; requires ≥1 building"], "modelTier": "mechanical"}
```

---

### Task 2: Worker parks alongside settle

**Goal:** `ProcessOnce` removes parkable packages from the working set without touching `settled`.

**Files:**
- Modify: `backend/internal/worker/worker.go` (the removal block, currently `if pkg.Settled { p.ws.Remove(...) }` at ~line 139)
- Test: `backend/internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] Removal becomes `if pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg, flags)) { p.ws.Remove(...) }` — using the `flags` already computed in the pass.
- [ ] An all-building-with-reason package (type known) is removed after a pass; its DB row keeps `settled = 0`.
- [ ] A building package with a succeeded-unpublished target (publishing per zero-value flags) is retained.
- [ ] A building package whose target lacks a BuildReason is retained.
- [ ] Existing worker tests pass unmodified.

**Verify:** `cd backend && go test ./internal/worker/ -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/worker/worker_test.go` (external package; mirror the existing removal-test pattern with `openDB`, `hubpkg.New()`, `workingset.New`, nil-task pools; nil client → zero flags → publishes-all, which is fine because the building branch needs no flags and `Published: true` short-circuits the inert check):

```go
// A package waiting only on build completions (all targets building, reasons
// known) is parked: removed from the working set but NOT settled.
func TestPoolParksAllBuildingPackage(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())
	p := worker.NewPool(1, nil, nil, nil, db, h, ws, nil) // no tasks, nil client
	p.Start(ctx)

	isContainer := false
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-building",
		RollupState: model.RollupBuilding, OKTargets: 0, TotalTargets: 2,
		IsContainer: &isContainer,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"},
			{Repo: "repo", Arch: "aarch64", State: "succeeded", Published: true}, // inert
		},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg)
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	// Parked → removed → Add re-dispatches.
	ws.Add(pkg)
	select {
	case <-ws.Dispatch():
		// correct — package was parked (removed)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("all-building package was not parked")
	}

	// Parking must not settle: the DB row stays settled=0 (re-seeded on restart).
	var settled int
	if err := db.QueryRow(`SELECT settled FROM packages WHERE project = ? AND name = ?`,
		pkg.Project, pkg.Name).Scan(&settled); err != nil {
		t.Fatal(err)
	}
	if settled != 0 {
		t.Fatalf("parked package must keep settled=0, got %d", settled)
	}
}

// A succeeded-unpublished target in a publishing repo (zero-value flags →
// publishes) must keep the package polling — publish detection has no
// MQ-with-poller-fallback path.
func TestPoolDoesNotParkWithUnpublishedPublishingTarget(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := worker.NewPool(1, nil, nil, nil, db, h, ws, nil)
	p.Start(ctx)

	isContainer := false
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-mixed",
		RollupState: model.RollupBuilding, OKTargets: 1, TotalTargets: 2,
		IsContainer: &isContainer,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"},
			{Repo: "repo", Arch: "aarch64", State: "succeeded"}, // unpublished, publishing repo
		},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg)
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	ws.Add(pkg) // still present → Add is a no-op → no dispatch
	select {
	case <-ws.Dispatch():
		t.Fatal("package with unpublished publishing target was wrongly parked")
	case <-time.After(100 * time.Millisecond):
		// correct — retained
	}
}

// A building target whose BuildReason has not been fetched yet keeps polling.
func TestPoolDoesNotParkWithoutBuildReason(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := worker.NewPool(1, nil, nil, nil, db, h, ws, nil)
	p.Start(ctx)

	isContainer := false
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-noreason",
		RollupState: model.RollupBuilding, OKTargets: 0, TotalTargets: 1,
		IsContainer: &isContainer,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "building"}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg)
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	ws.Add(pkg)
	select {
	case <-ws.Dispatch():
		t.Fatal("package without build reason was wrongly parked")
	case <-time.After(100 * time.Millisecond):
		// correct — retained
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/worker/ -run TestPoolParks -v`
Expected: `TestPoolParksAllBuildingPackage` FAILS (today the package is retained — building is not settled).

- [ ] **Step 3: Implement** — in `backend/internal/worker/worker.go`, replace:

```go
	if pkg.Settled {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```

with:

```go
	// Remove from the working set when there is nothing left to poll for:
	// settled (terminal) or parked (waiting only on build completions that MQ
	// announces, with the poller as fallback). Parking is in-memory only —
	// settled stays 0, so a restart re-seeds the package and re-parks it.
	if pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg, flags)) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```

(`flags` is already in scope — computed before the upsert; `obs` is already imported.)

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/worker/ -v && go test ./...`
Expected: all PASS (existing removal tests use published/succeeded/failed states — unaffected by the parking branch).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/worker/worker.go internal/worker/worker_test.go
git commit -s -m "feat(worker): park build-waiting packages out of the working set"
```

```json:metadata
{"files": ["backend/internal/worker/worker.go", "backend/internal/worker/worker_test.go"], "verifyCommand": "cd backend && go test ./internal/worker/ -v && go test ./...", "acceptanceCriteria": ["removal gate becomes Settled OR (type-known AND Parkable)", "all-building package parked with settled=0 in DB", "unpublished-publishing mixed package retained", "missing BuildReason retained", "existing tests pass unmodified"], "modelTier": "standard"}
```

---

### Task 3: MQ `build_unchanged` wakes the working set

**Goal:** `build_unchanged` events flow through the same merge+upsert+Signal path as success/fail, with the completed target marked `"finished"`.

**Files:**
- Modify: `backend/internal/mq/consumer.go` (the build-event case + `mqStateToRollup`)
- Test: `backend/internal/mq/consumer_test.go`

**Acceptance Criteria:**
- [ ] The early return `if key == "opensuse.obs.package.build_unchanged" { return }` is deleted.
- [ ] `mqStateToRollup("opensuse.obs.package.build_unchanged")` returns `model.RollupFinished` (the `default` arm changes from `RollupSucceeded` to `RollupFinished` with a comment).
- [ ] A `build_unchanged` delivery for a stored building package upserts the merged package (target `"finished"`) and Signals the working set.
- [ ] Existing consumer test passes unmodified.

**Verify:** `cd backend && go test ./internal/mq/ -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/mq/consumer_test.go` (internal package `mq`; check its existing imports and add `context`, `encoding/json`, `time`, `amqp "github.com/rabbitmq/amqp091-go"`, `hubpkg "github.com/percona/obs-dashboard/internal/hub"`, `"github.com/percona/obs-dashboard/internal/store"`, `"github.com/percona/obs-dashboard/internal/workingset"`, `"github.com/percona/obs-dashboard/internal/model"` as needed):

```go
func TestMQStateToRollupUnchangedIsFinished(t *testing.T) {
	if got := mqStateToRollup("opensuse.obs.package.build_unchanged"); got != model.RollupFinished {
		t.Fatalf("build_unchanged → %s, want finished", got)
	}
}

// build_unchanged must wake the working set: the build completed (with an
// identical result), which un-parks a package waiting on MQ completions.
func TestBuildUnchangedWakesWorkingSet(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4)
	h := hubpkg.New()
	c := NewConsumer("", db, h, nil, ws, "isv:percona")

	// Seed a stored package with a building target (as if parked).
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-a",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:17", "package": "pkg-a",
		"repository": "repo", "arch": "x86_64",
	})
	c.handle(context.Background(), amqp.Delivery{
		RoutingKey: "opensuse.obs.package.build_unchanged",
		Body:       body,
	})

	select {
	case got := <-ws.Dispatch():
		if got.Name != "pkg-a" {
			t.Fatalf("dispatched %q, want pkg-a", got.Name)
		}
		for _, tgt := range got.Targets {
			if tgt.Repo == "repo" && tgt.Arch == "x86_64" && tgt.State != "finished" {
				t.Fatalf("merged target state = %q, want finished", tgt.State)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("build_unchanged did not signal the working set")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/mq/ -run 'TestMQStateToRollup|TestBuildUnchanged' -v`
Expected: both FAIL (unchanged maps to succeeded; the delivery early-returns without signalling).

- [ ] **Step 3: Implement** — in `backend/internal/mq/consumer.go`:

Delete these two lines from the `case isPackageBuildEvent(key):` block:

```go
		if key == "opensuse.obs.package.build_unchanged" {
			return
		}
```

Change `mqStateToRollup`'s default arm:

```go
func mqStateToRollup(key string) model.RollupState {
	switch key {
	case "opensuse.obs.package.build_success":
		return model.RollupFinished
	case "opensuse.obs.package.build_fail":
		return model.RollupFailed
	default:
		// build_unchanged: the build completed with an identical result. Treat
		// it as finished so the worker's wake pass derives the real terminal
		// state (and its events) from OBS instead of jumping to succeeded.
		return model.RollupFinished
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/mq/ -v && go test ./...`
Expected: all PASS (the existing merge test is state-mapping-agnostic).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/mq/consumer.go internal/mq/consumer_test.go
git commit -s -m "feat(mq): build_unchanged wakes the working set as finished"
```

```json:metadata
{"files": ["backend/internal/mq/consumer.go", "backend/internal/mq/consumer_test.go"], "verifyCommand": "cd backend && go test ./internal/mq/ -v && go test ./...", "acceptanceCriteria": ["build_unchanged early return deleted", "mqStateToRollup maps unchanged to finished", "build_unchanged delivery merges target as finished and Signals the working set", "existing consumer test unmodified"], "modelTier": "standard"}
```

---

## Self-Review

**Spec coverage:** §1 `Parkable` → Task 1; §2 worker parking (settled untouched, `IsContainer` gate, flags reuse) → Task 2; §3 `build_unchanged` wake + `finished` mapping → Task 3; §4 no-other-changes → no task touches poller/workingset/store. Lifecycle/fallback claims rely on existing code (poller Add-on-change, Signal replacement) — unchanged, regression-covered by full-suite runs. ✓
**Placeholders:** none — all steps carry complete code (with an explicit compile note on the Task 1 table literals).
**Type consistency:** `Parkable(pkg *model.Package, flags PublishFlags)` (Tasks 1, 2); `flags` from ProcessOnce (Task 2); `mqStateToRollup` key strings match `isPackageBuildEvent` (Task 3). ✓
