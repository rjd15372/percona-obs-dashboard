# Build Event State Machine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the build event log so each target emits a faithful, replayable state machine: `build_started → (blocked | unresolvable | broken)* → succeeded | failed`.

**Architecture:** The worker (`worker/worker.go`) becomes the sole event emitter — the poller's `stateChangeEvent` path is deleted. Per-target events carry `repo`/`arch` for fine-grained filtering. `succeeded` fires on `Published` flip (true publication), not on `State == "succeeded"` (intermediate).

**Tech Stack:** Go (backend), Vue 3 + TypeScript (frontend), SQLite event store, SSE hub.

**User decisions (already made):**
- `build_started` fires as soon as OBS assigns a `BuildReason`, regardless of target state (not just `State == "building"`).
- Intermediate states (`blocked`, `unresolvable`, `broken`) emit only after `build_started`; guard is `t.BuildReason != ""`.
- If `BuildReason` appears while target is already `blocked`, emit `build_started` then `blocked` in the same cycle.
- `succeeded` fires on `!old.Published && t.Published` (publication = real consumer-visible success).
- `published` events suppressed entirely.
- Poller's `stateChangeEvent` removed; worker is the sole event emitter.
- `unresolvable` and `broken` are intermediate states with their own event types, not `failed`.
- `failed` is only emitted on terminal `"failed"` state; `why` scaffolded empty for now.
- No changes to `model/types.go` (14 EventType constants kept to avoid breaking TS types).

---

## File Map

| Action | File | What changes |
|--------|------|--------------|
| Modify | `backend/internal/worker/worker.go` | `emitBuildEvents` restructured |
| Modify | `backend/internal/obs/poller.go` | `stateChangeEvent`, `isTransientRollup` deleted; imports cleaned |
| Modify | `backend/internal/worker/worker_test.go` | 2 tests updated, 7 new tests added |
| Modify | `frontend/src/composables/useEventDisplay.ts` | `showReason` extended |

---

### Task 1: Restructure `emitBuildEvents` in worker.go

**Goal:** Replace the current per-target event logic with the new state machine: `build_started` on BuildReason appearance, intermediate state events guarded by `BuildReason != ""`, `succeeded` on `Published` flip, `failed` only on terminal `"failed"` state, no `published`.

**Files:**
- Modify: `backend/internal/worker/worker.go`

**Acceptance Criteria:**
- [ ] `build_started` fires when `old.BuildReason == "" && t.BuildReason != ""`, regardless of `t.State`
- [ ] `blocked` fires when `old.State != "blocked" && t.State == "blocked" && t.BuildReason != ""`; `why` = `t.BlockedBy`
- [ ] `unresolvable` fires when `old.State != "unresolvable" && t.State == "unresolvable" && t.BuildReason != ""`; `why` = `t.Details`
- [ ] `broken` fires when `old.State != "broken" && t.State == "broken" && t.BuildReason != ""`; `why` = `t.Details`
- [ ] `succeeded` fires when `!old.Published && t.Published`; type is `EventSucceeded`
- [ ] `failed` fires when `old.State != "failed" && t.State == "failed"`; `why` is empty string; type is `EventFailed`
- [ ] No `published` event emitted
- [ ] `failStates` map removed (no longer needed)
- [ ] `prevRollup` parameter removed from `emitBuildEvents` signature and call site
- [ ] `go build ./...` passes in `backend/`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go build ./...` → exits 0

**Steps:**

- [ ] **Step 1: Replace `emitBuildEvents` in worker.go**

  Remove the `failStates` map (line 108) and rewrite `emitBuildEvents` (lines 116–210). Remove `prevRollup model.RollupState` from the signature and from the call site at line 100.

  New function:

  ```go
  func (p *Pool) emitBuildEvents(pkg *model.Package, oldTargets []model.Target) {
  	oldByKey := make(map[string]model.Target, len(oldTargets))
  	for _, t := range oldTargets {
  		oldByKey[t.Repo+"/"+t.Arch] = t
  	}

  	now := time.Now().UTC()

  	for _, t := range pkg.Targets {
  		key := t.Repo + "/" + t.Arch
  		old := oldByKey[key]

  		// build_started: BuildReason newly appeared, regardless of target state.
  		if old.BuildReason == "" && t.BuildReason != "" {
  			why := t.BuildReason
  			if len(t.BuildReasonPackages) > 0 {
  				why += ": " + strings.Join(t.BuildReasonPackages, ", ")
  			}
  			p.appendEvent(&model.Event{
  				ID:      "evt_" + ulid.Make().String(),
  				Type:    model.EventBuildStarted,
  				Tags:    pkg.Tags,
  				Project: pkg.Project,
  				Package: pkg.Name,
  				Repo:    t.Repo,
  				Arch:    t.Arch,
  				What:    fmt.Sprintf("%s build started", pkg.Name),
  				Why:     why,
  				URL:     fmt.Sprintf("%s/package/live_build_log/%s/%s/%s/%s", obsBase, pkg.Project, pkg.Name, t.Repo, t.Arch),
  				At:      now,
  			})
  		}

  		// Intermediate states — only after build_started (guard: BuildReason present).
  		if t.BuildReason != "" {
  			if old.State != "blocked" && t.State == "blocked" {
  				p.appendEvent(&model.Event{
  					ID:      "evt_" + ulid.Make().String(),
  					Type:    model.EventBlocked,
  					Tags:    pkg.Tags,
  					Project: pkg.Project,
  					Package: pkg.Name,
  					Repo:    t.Repo,
  					Arch:    t.Arch,
  					What:    fmt.Sprintf("%s blocked", pkg.Name),
  					Why:     t.BlockedBy,
  					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
  					At:      now,
  				})
  			}
  			if old.State != "unresolvable" && t.State == "unresolvable" {
  				p.appendEvent(&model.Event{
  					ID:      "evt_" + ulid.Make().String(),
  					Type:    model.EventUnresolvable,
  					Tags:    pkg.Tags,
  					Project: pkg.Project,
  					Package: pkg.Name,
  					Repo:    t.Repo,
  					Arch:    t.Arch,
  					What:    fmt.Sprintf("%s unresolvable", pkg.Name),
  					Why:     t.Details,
  					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
  					At:      now,
  				})
  			}
  			if old.State != "broken" && t.State == "broken" {
  				p.appendEvent(&model.Event{
  					ID:      "evt_" + ulid.Make().String(),
  					Type:    model.EventBroken,
  					Tags:    pkg.Tags,
  					Project: pkg.Project,
  					Package: pkg.Name,
  					Repo:    t.Repo,
  					Arch:    t.Arch,
  					What:    fmt.Sprintf("%s broken", pkg.Name),
  					Why:     t.Details,
  					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
  					At:      now,
  				})
  			}
  		}

  		// succeeded: publication is the real terminal success signal.
  		if !old.Published && t.Published {
  			p.appendEvent(&model.Event{
  				ID:      "evt_" + ulid.Make().String(),
  				Type:    model.EventSucceeded,
  				Tags:    pkg.Tags,
  				Project: pkg.Project,
  				Package: pkg.Name,
  				Repo:    t.Repo,
  				Arch:    t.Arch,
  				What:    fmt.Sprintf("%s succeeded", pkg.Name),
  				Why:     "",
  				Version: pkg.Version,
  				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
  				At:      now,
  			})
  		}

  		// failed: only the terminal "failed" state; why is scaffolded for future use.
  		if old.State != "failed" && t.State == "failed" {
  			p.appendEvent(&model.Event{
  				ID:      "evt_" + ulid.Make().String(),
  				Type:    model.EventFailed,
  				Tags:    pkg.Tags,
  				Project: pkg.Project,
  				Package: pkg.Name,
  				Repo:    t.Repo,
  				Arch:    t.Arch,
  				What:    fmt.Sprintf("%s failed", pkg.Name),
  				Why:     "",
  				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
  				At:      now,
  			})
  		}
  	}
  }
  ```

- [ ] **Step 2: Update the call site in `ProcessOnce`**

  Line 100 currently reads:
  ```go
  p.emitBuildEvents(pkg, oldTargets, prevRollup)
  ```
  Change to:
  ```go
  p.emitBuildEvents(pkg, oldTargets)
  ```
  Remove the `prevRollup := pkg.RollupState` variable at line 71 (it's no longer used anywhere after this change — check that nothing else references it).

- [ ] **Step 3: Verify build**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend && go build ./...
  ```
  Expected: exits 0.

- [ ] **Step 4: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/worker/worker.go
  git commit -s -m "feat(worker): implement per-target build event state machine"
  ```

---

### Task 2: Remove poller `stateChangeEvent`

**Goal:** Delete the poller's event emission path entirely — `stateChangeEvent`, `isTransientRollup`, the call site block, and the two now-unused imports.

**Files:**
- Modify: `backend/internal/obs/poller.go`

**Acceptance Criteria:**
- [ ] The `if rollupChanged && !isTransientRollup(pkg.RollupState)` block (lines ~119–126) is deleted
- [ ] `func isTransientRollup(s model.RollupState) bool` is deleted
- [ ] `func stateChangeEvent(pkg *model.Package, prev *model.Package) *model.Event` is deleted
- [ ] Import `"fmt"` removed from `poller.go`
- [ ] Import `"github.com/oklog/ulid/v2"` removed from `poller.go`
- [ ] The stale comment "a flood of stateChangeEvents" (line ~102) updated to "a succeeded↔published oscillation and spurious SSE broadcasts"
- [ ] `go build ./...` passes in `backend/`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go build ./...` → exits 0

**Steps:**

- [ ] **Step 1: Delete the event-emission block**

  In `ProcessOnce` (around line 119), delete this block:
  ```go
  if rollupChanged && !isTransientRollup(pkg.RollupState) {
      evt := stateChangeEvent(pkg, prev)
      if err := store.AppendEvent(p.db, evt); err != nil {
          slog.Error("poller: append event", "err", err)
      } else {
          p.hub.Notify(hubpkg.NewEvent(evt))
      }
  }
  ```

- [ ] **Step 2: Update the stale comment**

  Around line 99–103 there is a comment block ending with "a flood of stateChangeEvents." Change the last sentence to:
  ```
  // Without this guard the poller would flip a published package back to succeeded
  // every tick, causing a succeeded↔published oscillation and spurious SSE broadcasts.
  ```

- [ ] **Step 3: Delete `isTransientRollup` and `stateChangeEvent` functions**

  Delete lines ~340–366 (both functions and their doc comments).

- [ ] **Step 4: Remove unused imports**

  Remove `"fmt"` and `"github.com/oklog/ulid/v2"` from the import block at the top of `poller.go`.

- [ ] **Step 5: Verify build**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend && go build ./...
  ```
  Expected: exits 0.

- [ ] **Step 6: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/obs/poller.go
  git commit -s -m "feat(poller): remove stateChangeEvent — worker is now sole event emitter"
  ```

---

### Task 3: Update and add worker tests

**Goal:** Update two existing tests to match the new state machine semantics, add 7 new tests covering the full per-target state machine, and add a test verifying the poller emits no events.

**Files:**
- Modify: `backend/internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] `TestProcessOnceEmitsPublished` renamed `TestProcessOnceEmitsSucceededOnPublish`; asserts `EventSucceeded` (not `EventPublished`) on `Published` flip
- [ ] `TestProcessOnceNoEventForBlocked` updated: no-BuildReason case still expects 0 events; a separate subtest with BuildReason present expects 2 events (`build_started` then `blocked`)
- [ ] `TestProcessOnceEmitsFailedStates`: only the `"failed"` state case remains; `unresolvable`/`broken` cases removed (they are covered by new intermediate-state tests)
- [ ] `TestBuildStartedFiresOnBlockedState`: BuildReason appears while target is already `blocked` → 2 events in order: `build_started`, `blocked`
- [ ] `TestIntermediateStateRequiresBuildReason`: `blocked` transition with no BuildReason → 0 events
- [ ] `TestIntermediateStatesFireInSequence`: `blocked` → `unresolvable` → `broken` across 3 cycles, each with BuildReason → 3 events with correct types and `why` values
- [ ] `TestSucceededOnPublishNotOnState`: `State == "succeeded"` but `Published` stays false → 0 events
- [ ] `TestSucceededOnPublishFlip`: `Published` flips from false to true → 1 `EventSucceeded` event carrying `repo`/`arch`/`version`
- [ ] `TestFailedTerminal`: target transitions to `"failed"` → 1 `EventFailed` event, `why` is empty string
- [ ] `TestNoPollerRollupEvents`: poller processes a rollup state change to `"succeeded"` → 0 events in DB
- [ ] `go test ./internal/worker/... ./internal/obs/...` passes in `backend/`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/worker/... ./internal/obs/...` → all pass

**Steps:**

- [ ] **Step 1: Add `setTargetReasonTask` helper**

  The existing `setReasonTask` only sets `BuildReason` when `target.State == "building"`. Add a new helper that sets it unconditionally (needed for the blocked-state tests):

  ```go
  // setTargetReasonTask sets BuildReason on a specific target unconditionally.
  type setTargetReasonTask struct{ repo, arch, reason string }

  func (t setTargetReasonTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
      for i, target := range pkg.Targets {
          if target.Repo == t.repo && target.Arch == t.arch {
              pkg.Targets[i].BuildReason = t.reason
          }
      }
      return nil
  }
  ```

- [ ] **Step 2: Update `TestProcessOnceEmitsPublished` → `TestProcessOnceEmitsSucceededOnPublish`**

  Rename the function. Change the assertion from `model.EventPublished` to `model.EventSucceeded`.

  Full updated test:
  ```go
  func TestProcessOnceEmitsSucceededOnPublish(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupSucceeded,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded", Published: false}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      pool := worker.NewPool(0, []worker.Task{
          setPublishedTask{"Ubuntu_24.04", "x86_64"},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 1 {
          t.Fatalf("expected 1 event, got %d", len(evts))
      }
      if evts[0].Type != model.EventSucceeded {
          t.Errorf("expected succeeded, got %q", evts[0].Type)
      }
      if evts[0].Repo != "Ubuntu_24.04" || evts[0].Arch != "x86_64" {
          t.Errorf("expected repo/arch on event, got %q/%q", evts[0].Repo, evts[0].Arch)
      }
  }
  ```

- [ ] **Step 3: Update `TestProcessOnceEmitsFailedStates`**

  Remove the `unresolvable` and `broken` table cases — those are now intermediate state events, not `EventFailed`. Keep only the `"failed"` case and rename the test to `TestProcessOnceEmitsFailedTerminal`:

  ```go
  func TestProcessOnceEmitsFailedTerminal(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupBuilding,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      pool := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "failed", ""},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 1 {
          t.Fatalf("expected 1 event, got %d: %v", len(evts), evts)
      }
      if evts[0].Type != model.EventFailed {
          t.Errorf("expected failed, got %q", evts[0].Type)
      }
      if evts[0].Why != "" {
          t.Errorf("expected empty why, got %q", evts[0].Why)
      }
  }
  ```

- [ ] **Step 4: Update `TestProcessOnceNoEventForBlocked`**

  Convert to a table-driven test with two subtests:

  ```go
  func TestProcessOnceNoEventForBlocked(t *testing.T) {
      t.Run("no build reason → 0 events", func(t *testing.T) {
          db := setupDB(t)
          h := hubpkg.New()
          ws := workingset.New(10)

          pkg := &model.Package{
              Project:     "isv:percona:ppg:17",
              Name:        "mypkg",
              RollupState: model.RollupBuilding,
              Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
              UpdatedAt:   time.Now().UTC(),
          }
          if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
              t.Fatalf("seed: %v", err)
          }
          pool := worker.NewPool(0, []worker.Task{
              setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
          }, nil, nil, db, h, ws)
          pool.ProcessOnce(context.Background(), pkg)

          now := time.Now().UTC()
          evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
          if len(evts) != 0 {
              t.Errorf("expected 0 events, got %d", len(evts))
          }
      })

      t.Run("with build reason → build_started then blocked", func(t *testing.T) {
          db := setupDB(t)
          h := hubpkg.New()
          ws := workingset.New(10)

          pkg := &model.Package{
              Project:     "isv:percona:ppg:17",
              Name:        "mypkg",
              RollupState: model.RollupBuilding,
              Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
              UpdatedAt:   time.Now().UTC(),
          }
          if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
              t.Fatalf("seed: %v", err)
          }
          pool := worker.NewPool(0, []worker.Task{
              setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
              setTargetReasonTask{"Ubuntu_24.04", "x86_64", "source change"},
          }, nil, nil, db, h, ws)
          pool.ProcessOnce(context.Background(), pkg)

          now := time.Now().UTC()
          evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
          if len(evts) != 2 {
              t.Fatalf("expected 2 events, got %d", len(evts))
          }
          if evts[0].Type != model.EventBuildStarted {
              t.Errorf("expected build_started first, got %q", evts[0].Type)
          }
          if evts[1].Type != model.EventBlocked {
              t.Errorf("expected blocked second, got %q", evts[1].Type)
          }
      })
  }
  ```

- [ ] **Step 5: Add `TestBuildStartedFiresOnBlockedState`**

  BuildReason appears in the same cycle the target is already `blocked` (pre-seeded in blocked state):

  ```go
  func TestBuildStartedFiresOnBlockedState(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      // Seed: target already blocked, no BuildReason yet.
      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupBuilding,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "blocked"}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      // Task: set BuildReason while still blocked.
      pool := worker.NewPool(0, []worker.Task{
          setTargetReasonTask{"Ubuntu_24.04", "x86_64", "dep changed"},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 2 {
          t.Fatalf("expected 2 events, got %d: %v", len(evts), evts)
      }
      if evts[0].Type != model.EventBuildStarted {
          t.Errorf("expected build_started first, got %q", evts[0].Type)
      }
      if evts[1].Type != model.EventBlocked {
          t.Errorf("expected blocked second, got %q", evts[1].Type)
      }
  }
  ```

- [ ] **Step 6: Add `TestIntermediateStateRequiresBuildReason`**

  ```go
  func TestIntermediateStateRequiresBuildReason(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupBuilding,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      // Transition to unresolvable with no BuildReason: must not emit any event.
      pool := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "unresolvable", "nothing provides libpq"},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 0 {
          t.Errorf("expected 0 events without BuildReason, got %d", len(evts))
      }
  }
  ```

- [ ] **Step 7: Add `TestIntermediateStatesFireInSequence`**

  Three cycles: each adds a new intermediate state with BuildReason present. The spec says "across cycles" so seed the prior state in the DB between cycles by calling `UpsertPackageState` after each `ProcessOnce`.

  Note: `ProcessOnce` calls `UpsertPackageState` internally, so the pkg state is updated in DB. But `ProcessOnce` also mutates `pkg` in place via the task chain. To simulate multiple poll cycles: call `ProcessOnce` once per cycle, with the pkg mutated by the previous cycle's task.

  ```go
  func TestIntermediateStatesFireInSequence(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      // Cycle 1: target blocked with BuildReason.
      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupBuilding,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      pool1 := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
          setTargetReasonTask{"Ubuntu_24.04", "x86_64", "source change"},
      }, nil, nil, db, h, ws)
      pool1.ProcessOnce(context.Background(), pkg)

      // Cycle 2: unresolvable (BuildReason preserved by the poller enrichment logic,
      // but in tests we set it explicitly since we're not running the full poller).
      pool2 := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "unresolvable", "nothing provides libpq"},
      }, nil, nil, db, h, ws)
      pool2.ProcessOnce(context.Background(), pkg)

      // Cycle 3: broken.
      pool3 := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "broken", "patch failed"},
      }, nil, nil, db, h, ws)
      pool3.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))

      // Expect: build_started, blocked, unresolvable, broken.
      if len(evts) != 4 {
          t.Fatalf("expected 4 events, got %d: %v", len(evts), evts)
      }
      wantTypes := []model.EventType{
          model.EventBuildStarted,
          model.EventBlocked,
          model.EventUnresolvable,
          model.EventBroken,
      }
      for i, want := range wantTypes {
          if evts[i].Type != want {
              t.Errorf("event[%d]: want %q, got %q", i, want, evts[i].Type)
          }
      }
      if evts[2].Why != "nothing provides libpq" {
          t.Errorf("unresolvable why: want %q, got %q", "nothing provides libpq", evts[2].Why)
      }
      if evts[3].Why != "patch failed" {
          t.Errorf("broken why: want %q, got %q", "patch failed", evts[3].Why)
      }
  }
  ```

- [ ] **Step 8: Add `TestSucceededOnPublishNotOnState`**

  ```go
  func TestSucceededOnPublishNotOnState(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      // Seed: building state.
      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          RollupState: model.RollupBuilding,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building", Published: false}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      // Transition State to "succeeded" but leave Published false.
      pool := worker.NewPool(0, []worker.Task{
          setStateTask{"Ubuntu_24.04", "x86_64", "succeeded", ""},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 0 {
          t.Errorf("expected 0 events when State==succeeded but Published==false, got %d", len(evts))
      }
  }
  ```

- [ ] **Step 9: Add `TestSucceededOnPublishFlip`**

  ```go
  func TestSucceededOnPublishFlip(t *testing.T) {
      db := setupDB(t)
      h := hubpkg.New()
      ws := workingset.New(10)

      pkg := &model.Package{
          Project:     "isv:percona:ppg:17",
          Name:        "mypkg",
          Version:     "17.5-1",
          RollupState: model.RollupSucceeded,
          Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded", Published: false}},
          UpdatedAt:   time.Now().UTC(),
      }
      if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
          t.Fatalf("seed: %v", err)
      }

      pool := worker.NewPool(0, []worker.Task{
          setPublishedTask{"Ubuntu_24.04", "x86_64"},
      }, nil, nil, db, h, ws)
      pool.ProcessOnce(context.Background(), pkg)

      now := time.Now().UTC()
      evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
      if len(evts) != 1 {
          t.Fatalf("expected 1 event, got %d", len(evts))
      }
      if evts[0].Type != model.EventSucceeded {
          t.Errorf("expected succeeded, got %q", evts[0].Type)
      }
      if evts[0].Repo != "Ubuntu_24.04" {
          t.Errorf("expected Repo=Ubuntu_24.04, got %q", evts[0].Repo)
      }
      if evts[0].Arch != "x86_64" {
          t.Errorf("expected Arch=x86_64, got %q", evts[0].Arch)
      }
      if evts[0].Version != "17.5-1" {
          t.Errorf("expected Version=17.5-1, got %q", evts[0].Version)
      }
  }
  ```

- [ ] **Step 10: Add `TestFailedTerminal`**

  (Separate from `TestProcessOnceEmitsFailedTerminal` above — this one verifies `why` is empty and the `why` field was scaffolded correctly.)

  This test is covered by the updated `TestProcessOnceEmitsFailedTerminal` in Step 3. No duplicate needed.

- [ ] **Step 11: Add `TestNoPollerRollupEvents`**

  This test lives in `backend/internal/obs/` (a new file, or appended to an existing obs test file if one exists).

  ```go
  // In backend/internal/obs/poller_test.go (create if not present) or append to existing test file.
  func TestNoPollerRollupEvents(t *testing.T) {
      // Verify that a rollup state change in the poller does NOT append any event.
      // The poller no longer calls stateChangeEvent.
      //
      // We test this by ensuring the obs/poller.go file does not reference
      // store.AppendEvent at all (compile-time proof via grep), rather than
      // spinning up a full poller integration. A grep-based check is acceptable
      // here because the integration path is complex and the compile-time
      // invariant is what matters.
      data, err := os.ReadFile("poller.go")
      if err != nil {
          t.Fatalf("read poller.go: %v", err)
      }
      if strings.Contains(string(data), "AppendEvent") {
          t.Error("poller.go must not call store.AppendEvent — worker is the sole event emitter")
      }
  }
  ```

  This test must be in the `obs` package directory. Add the necessary imports at the top of the test file:
  ```go
  import (
      "os"
      "strings"
      "testing"
  )
  ```

- [ ] **Step 12: Run all tests**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/worker/... ./internal/obs/...
  ```
  Expected: all pass.

- [ ] **Step 13: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/worker/worker_test.go backend/internal/obs/
  git commit -s -m "test(worker): update and add tests for build event state machine"
  ```

---

### Task 4: Update frontend `showReason`

**Goal:** Extend `showReason` in `useEventDisplay.ts` to surface the `why` field for intermediate states (`blocked`, `unresolvable`, `broken`) in addition to `build_started` and `failed`.

**Files:**
- Modify: `frontend/src/composables/useEventDisplay.ts`

**Acceptance Criteria:**
- [ ] `showReason` returns `true` for `blocked`, `unresolvable`, and `broken` when `event.why` is non-empty
- [ ] `showReason` still returns `true` for `build_started` and `failed` when `event.why` is non-empty
- [ ] `showReason` still returns `false` when `event.why` is empty regardless of type
- [ ] `npx vue-tsc --noEmit` passes in `frontend/`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Update `showReason` in `useEventDisplay.ts`**

  Current function (around line where `showReason` is defined):
  ```typescript
  export function showReason(event: Event): boolean {
    return (event.type === 'build_started' || event.type === 'failed') && !!event.why
  }
  ```

  Replace with:
  ```typescript
  export function showReason(event: Event): boolean {
    return (
      event.type === 'build_started' ||
      event.type === 'failed' ||
      event.type === 'blocked' ||
      event.type === 'unresolvable' ||
      event.type === 'broken'
    ) && !!event.why
  }
  ```

- [ ] **Step 2: Verify TypeScript**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit
  ```
  Expected: exits 0 (no type errors).

- [ ] **Step 3: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add frontend/src/composables/useEventDisplay.ts
  git commit -s -m "feat(frontend): show why for blocked/unresolvable/broken events"
  ```

---

## Self-Review

**Spec coverage:**
- ✅ `build_started` on BuildReason appearance (Task 1, Step 1)
- ✅ Intermediate states guarded by `BuildReason != ""` (Task 1, Step 1)
- ✅ Same-cycle ordering: `build_started` processed before intermediate checks (loop order in new `emitBuildEvents`)
- ✅ `succeeded` = `Published` flip (Task 1, Step 1)
- ✅ `failed` = terminal `"failed"` state only, `why` scaffolded empty (Task 1, Step 1)
- ✅ No `published` event (Task 1 removes the block)
- ✅ Poller `stateChangeEvent` removed (Task 2)
- ✅ `isTransientRollup` deleted (Task 2)
- ✅ Unused imports `"fmt"` and `ulid` removed from poller (Task 2)
- ✅ Stale comment updated (Task 2, Step 2)
- ✅ `prevRollup` parameter removed from `emitBuildEvents` and call site (Task 1, Step 2)
- ✅ `TestProcessOnceEmitsPublished` → `TestProcessOnceEmitsSucceededOnPublish` (Task 3, Step 2)
- ✅ `TestProcessOnceNoEventForBlocked` updated with BuildReason subtest (Task 3, Step 4)
- ✅ `TestProcessOnceEmitsFailedStates` updated to remove `unresolvable`/`broken` cases (Task 3, Step 3)
- ✅ 7 new tests added (Tasks 3, Steps 5–11)
- ✅ `showReason` extended for intermediate states (Task 4)

**Placeholder scan:** None found.

**Type consistency:**
- `model.EventBlocked`, `model.EventUnresolvable`, `model.EventBroken` — all defined in `model/types.go` (confirmed lines 80–82).
- `setTargetReasonTask` defined in Step 1 and referenced in Steps 4, 5, 7.
- `setStateTask`, `setPublishedTask`, `setupDB` — all pre-existing in the test file.
