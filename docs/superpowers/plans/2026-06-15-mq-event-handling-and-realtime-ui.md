# MQ Event Handling and Real-Time UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Handle additional OBS MQ events (package.create, project.update, package.commit, build_success fix, repo.published transition) and propagate them to the frontend in real-time so new PR projects and version tabs appear immediately.

**Architecture:** Four new cases are added to the MQ consumer's switch statement. `package.build_success` is corrected from `RollupSucceeded` to `RollupFinished` so packages stay in the working set until `repo.published` fires, at which point a new store query finds all `finished` packages in the project and signals them to the worker pool. The frontend `useRealtimeStream` composable is extended to detect new PR numbers and call `refreshPR()` only on first appearance; existing PR packages are updated in-place with rollup recomputation.

**Tech Stack:** Go 1.22+, SQLite (via `database/sql`), Vue 3 / TypeScript (no new dependencies for either).

**User decisions (already made):**
- `package.create` → append `EventCreated` + `ws.Signal(stub)` (immediate working-set dispatch)
- `project.update` and `project.update_project_conf` → append `EventUpdated` only
- `package.commit` → append `EventUpdated` only; `package.update` and `package.upload` are ignored
- `package.build_success` → `RollupFinished` (was `RollupSucceeded`); stays in working set until publish
- `package.build_unchanged` → `RollupSucceeded` (unchanged, already published)
- `repo.published` → eager: query `finished` packages in project, signal each via `ws.Signal`
- PR dropdown refresh is gated: `refreshPR()` only when PR number not yet in `prGroups`; in-place update otherwise
- Version tabs already reactive — no extra frontend code needed once `package.create` is handled

---

## File Map

| File | Role in this change |
|------|---------------------|
| `backend/internal/model/types.go` | Add `EventUpdated` constant |
| `backend/internal/mq/consumer.go` | Fix `mqStateToRollup`; extend `repo.published`; add 4 new event cases |
| `backend/internal/store/packages.go` | Add `GetFinishedPackagesByProject` |
| `backend/internal/store/packages_test.go` | Test `GetFinishedPackagesByProject` |
| `frontend/src/types/api.ts` | Add `'updated'` to `EventType` union |
| `frontend/src/composables/useRealtimeStream.ts` | Extend signature; handle PR `package_update`; gate `refreshPR()` |
| `frontend/src/App.vue` | Pass `prGroups` and `refreshPR` to `useRealtimeStream` |

---

## Task 1: Add `EventUpdated` model constant and fix `build_success` rollup state

**Goal:** Add `EventUpdated` to the event type enum and correct `mqStateToRollup` so `build_success` produces `RollupFinished` instead of `RollupSucceeded`.

**Files:**
- Modify: `backend/internal/model/types.go:88-91`
- Modify: `backend/internal/mq/consumer.go:398-408`

**Acceptance Criteria:**
- [ ] `model.EventUpdated` constant exists with value `"updated"`
- [ ] `mqStateToRollup("opensuse.obs.package.build_success")` returns `model.RollupFinished`
- [ ] `mqStateToRollup("opensuse.obs.package.build_fail")` still returns `model.RollupFailed`
- [ ] `mqStateToRollup("opensuse.obs.package.build_unchanged")` still returns `model.RollupSucceeded`
- [ ] `go build ./...` passes with no errors

**Verify:** `cd backend && go build ./...` → exits 0 with no output

**Steps:**

- [ ] **Step 1: Add `EventUpdated` to `backend/internal/model/types.go`**

  Find the block of `EventType` constants (around line 88). Add after `EventVersionChange`:

  ```go
  EventUpdated EventType = "updated"
  ```

  The full block should look like:
  ```go
  EventTriggered     EventType = "triggered"
  EventStarted       EventType = "started"
  EventSucceeded     EventType = "succeeded"
  EventFailed        EventType = "failed"
  EventUnresolvable  EventType = "unresolvable"
  EventBroken        EventType = "broken"
  EventBlocked       EventType = "blocked"
  EventPublished     EventType = "published"
  EventCreated       EventType = "created"
  EventDeleted       EventType = "deleted"
  EventBuildStarted  EventType = "build_started"
  EventBuildFinished EventType = "build_finished"
  EventVersionChange EventType = "version_change"
  EventUpdated       EventType = "updated"
  ```

- [ ] **Step 2: Fix `mqStateToRollup` in `backend/internal/mq/consumer.go`**

  Find `mqStateToRollup` (around line 398). Change `RollupSucceeded` to `RollupFinished` for `build_success`:

  ```go
  func mqStateToRollup(key string) model.RollupState {
      switch key {
      case "opensuse.obs.package.build_success":
          return model.RollupFinished
      case "opensuse.obs.package.build_fail":
          return model.RollupFailed
      default:
          return model.RollupSucceeded
      }
  }
  ```

- [ ] **Step 3: Build and verify**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend && go build ./...
  ```

  Expected: exits 0, no output.

- [ ] **Step 4: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/model/types.go backend/internal/mq/consumer.go
  git commit -s -m "feat(mq): add EventUpdated type; fix build_success to RollupFinished"
  ```

---

## Task 2: Add `GetFinishedPackagesByProject` to store

**Goal:** Add a store function that returns all packages for a given project with `rollup_state = 'finished'`, used by the `repo.published` handler to find packages ready for the `finished → succeeded` transition.

**Files:**
- Modify: `backend/internal/store/packages.go`
- Modify: `backend/internal/store/packages_test.go`

**Acceptance Criteria:**
- [ ] `GetFinishedPackagesByProject(db, "isv:percona:ppg:17")` returns only packages with `rollup_state = 'finished'` for that exact project
- [ ] Returns empty slice (not error) when no packages match
- [ ] Does not return packages from other projects with the same prefix
- [ ] `go test ./internal/store/... -run TestGetFinishedPackagesByProject -v` passes

**Verify:** `cd backend && go test ./internal/store/... -run TestGetFinishedPackagesByProject -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test in `backend/internal/store/packages_test.go`**

  Find the existing test file. Add this test after the existing tests:

  ```go
  func TestGetFinishedPackagesByProject(t *testing.T) {
      db := openTestDB(t)

      // Seed: two finished packages in target project, one succeeded, one in another project
      pkgFinished1 := &model.Package{
          Project: "isv:percona:ppg:17", Name: "postgres17", Scope: model.ScopeVersion,
          RollupState: model.RollupFinished, OKTargets: 0, TotalTargets: 1,
          Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "finished"}},
          UpdatedAt: time.Now().UTC(),
      }
      pkgFinished2 := &model.Package{
          Project: "isv:percona:ppg:17", Name: "pgaudit17", Scope: model.ScopeVersion,
          RollupState: model.RollupFinished, OKTargets: 0, TotalTargets: 1,
          Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "aarch64", State: "finished"}},
          UpdatedAt: time.Now().UTC(),
      }
      pkgSucceeded := &model.Package{
          Project: "isv:percona:ppg:17", Name: "pg_stat_monitor", Scope: model.ScopeVersion,
          RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
          Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "succeeded"}},
          UpdatedAt: time.Now().UTC(),
      }
      pkgOtherProject := &model.Package{
          Project: "isv:percona:ppg:16", Name: "postgres16", Scope: model.ScopeVersion,
          RollupState: model.RollupFinished, OKTargets: 0, TotalTargets: 1,
          Targets: []model.Target{{Repo: "Percona-PPG-16", Arch: "x86_64", State: "finished"}},
          UpdatedAt: time.Now().UTC(),
      }
      for _, pkg := range []*model.Package{pkgFinished1, pkgFinished2, pkgSucceeded, pkgOtherProject} {
          if err := UpsertPackageState(db, pkg); err != nil {
              t.Fatalf("seed: %v", err)
          }
      }

      got, err := GetFinishedPackagesByProject(db, "isv:percona:ppg:17")
      if err != nil {
          t.Fatalf("unexpected error: %v", err)
      }
      if len(got) != 2 {
          t.Fatalf("want 2 finished packages, got %d", len(got))
      }
      names := map[string]bool{}
      for _, p := range got {
          names[p.Name] = true
          if p.RollupState != model.RollupFinished {
              t.Errorf("package %s: want RollupFinished, got %s", p.Name, p.RollupState)
          }
          if p.Project != "isv:percona:ppg:17" {
              t.Errorf("package %s: wrong project %s", p.Name, p.Project)
          }
      }
      if !names["postgres17"] || !names["pgaudit17"] {
          t.Errorf("wrong packages returned: %v", names)
      }

      // Empty result case
      got2, err := GetFinishedPackagesByProject(db, "isv:percona:ppg:99")
      if err != nil {
          t.Fatalf("unexpected error on empty: %v", err)
      }
      if len(got2) != 0 {
          t.Errorf("want 0 packages for unknown project, got %d", len(got2))
      }
  }
  ```

- [ ] **Step 2: Run the test to confirm it fails**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend
  go test ./internal/store/... -run TestGetFinishedPackagesByProject -v
  ```

  Expected: compilation error — `GetFinishedPackagesByProject undefined`.

- [ ] **Step 3: Implement `GetFinishedPackagesByProject` in `backend/internal/store/packages.go`**

  Add after `GetActivePackages`:

  ```go
  // GetFinishedPackagesByProject returns all packages for the given project with
  // rollup_state = 'finished'. Used by the MQ consumer on repo.published to signal
  // packages for the finished → succeeded transition via the worker pool.
  func GetFinishedPackagesByProject(db *sql.DB, project string) ([]*model.Package, error) {
      rows, err := db.Query(`
          SELECT project, name, scope, rollup_state, ok_targets, total_targets,
                 trigger_what, trigger_kind, trigger_at, targets_json, updated_at
          FROM packages WHERE project = ? AND rollup_state = 'finished'`,
          project,
      )
      if err != nil {
          return nil, err
      }
      defer rows.Close()
      return scanPackages(rows)
  }
  ```

- [ ] **Step 4: Run the test to confirm it passes**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend
  go test ./internal/store/... -run TestGetFinishedPackagesByProject -v
  ```

  Expected:
  ```
  --- PASS: TestGetFinishedPackagesByProject (0.00s)
  PASS
  ```

- [ ] **Step 5: Run the full store test suite**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend
  go test ./internal/store/... -v
  ```

  Expected: all tests PASS.

- [ ] **Step 6: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/store/packages.go backend/internal/store/packages_test.go
  git commit -s -m "feat(store): add GetFinishedPackagesByProject"
  ```

---

## Task 3: Wire new MQ event cases and extend `repo.published`

**Goal:** Add four new event cases to the MQ consumer (`package.create`, `project.update`, `project.update_project_conf`, `package.commit`) and extend the `repo.published` handler to signal `finished` packages to the working set.

**Files:**
- Modify: `backend/internal/mq/consumer.go`

**Acceptance Criteria:**
- [ ] `opensuse.obs.package.create` appends `EventCreated` and calls `ws.Signal` with a stub package
- [ ] `opensuse.obs.project.update` appends `EventUpdated` with `What: "project <X> updated"`
- [ ] `opensuse.obs.project.update_project_conf` appends `EventUpdated` with `What: "project <X> configuration updated"`
- [ ] `opensuse.obs.package.commit` appends `EventUpdated` with `What: "<pkg> committed (rev <rev>)"`
- [ ] `repo.published` handler still appends `EventPublished` AND now also calls `ws.Signal` on each `finished` package in the project
- [ ] `go test ./... -race` passes
- [ ] `go build ./...` passes

**Verify:** `cd backend && go test ./... -race` → all packages PASS

**Steps:**

- [ ] **Step 1: Add the four new cases to `handle()` in `backend/internal/mq/consumer.go`**

  Find the `switch` block in `handle()`. After the `case key == "opensuse.obs.package.version_change":` block (around line 262) and before the `case isPackageBuildEvent(key):` block, insert:

  ```go
  case key == "opensuse.obs.package.create":
      evt := &model.Event{
          ID:      "evt_" + ulid.Make().String(),
          Type:    model.EventCreated,
          Scope:   inferScopeFromProject(m.Project),
          Project: m.Project,
          Package: m.Package,
          What:    fmt.Sprintf("package %s created", m.Package),
          Why:     m.Sender,
          URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", m.Project, m.Package),
          At:      time.Now().UTC(),
      }
      c.appendEvent(evt)
      stub := &model.Package{
          Project: m.Project,
          Name:    m.Package,
          Scope:   inferScopeFromProject(m.Project),
      }
      c.ws.Signal(stub)

  case key == "opensuse.obs.project.update":
      evt := &model.Event{
          ID:      "evt_" + ulid.Make().String(),
          Type:    model.EventUpdated,
          Scope:   inferScopeFromProject(m.Project),
          Project: m.Project,
          What:    fmt.Sprintf("project %s updated", m.Project),
          Why:     m.Sender,
          URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
          At:      time.Now().UTC(),
      }
      c.appendEvent(evt)

  case key == "opensuse.obs.project.update_project_conf":
      evt := &model.Event{
          ID:      "evt_" + ulid.Make().String(),
          Type:    model.EventUpdated,
          Scope:   inferScopeFromProject(m.Project),
          Project: m.Project,
          What:    fmt.Sprintf("project %s configuration updated", m.Project),
          Why:     m.Sender,
          URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
          At:      time.Now().UTC(),
      }
      c.appendEvent(evt)

  case key == "opensuse.obs.package.commit":
      evt := &model.Event{
          ID:      "evt_" + ulid.Make().String(),
          Type:    model.EventUpdated,
          Scope:   inferScopeFromProject(m.Project),
          Project: m.Project,
          Package: m.Package,
          What:    fmt.Sprintf("%s committed (rev %s)", m.Package, m.Rev),
          Why:     m.Comment,
          URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", m.Project, m.Package),
          At:      time.Now().UTC(),
      }
      c.appendEvent(evt)
  ```

- [ ] **Step 2: Extend the `repo.published` handler**

  Find the existing `case key == repoRouteKey:` block (around line 187). After `c.appendEvent(evt)`, add:

  ```go
  finished, err := store.GetFinishedPackagesByProject(c.db, m.Project)
  if err != nil {
      slog.Warn("mq: get finished packages for publish signal", "project", m.Project, "err", err)
  } else {
      for _, pkg := range finished {
          c.ws.Signal(pkg)
      }
  }
  ```

  The full case block becomes:
  ```go
  case key == repoRouteKey:
      evt := &model.Event{
          ID:      "evt_" + ulid.Make().String(),
          Type:    model.EventPublished,
          Scope:   model.ScopeRelease,
          Project: m.Project,
          Package: m.Package,
          Repo:    m.Repo,
          What:    fmt.Sprintf("%s published", m.Repo),
          Why:     "repo published",
          URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
          At:      time.Now().UTC(),
      }
      c.appendEvent(evt)
      finished, err := store.GetFinishedPackagesByProject(c.db, m.Project)
      if err != nil {
          slog.Warn("mq: get finished packages for publish signal", "project", m.Project, "err", err)
      } else {
          for _, pkg := range finished {
              c.ws.Signal(pkg)
          }
      }
  ```

- [ ] **Step 3: Run gofmt**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend
  gofmt -w internal/mq/consumer.go
  ```

- [ ] **Step 4: Build and run all tests**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/backend
  go build ./... && go test ./... -race
  ```

  Expected: all packages build and all tests PASS.

- [ ] **Step 5: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add backend/internal/mq/consumer.go
  git commit -s -m "feat(mq): handle package.create, project.update, package.commit; signal finished on repo.published"
  ```

---

## Task 4: Frontend real-time updates for PR dropdown and version tabs

**Goal:** Extend `useRealtimeStream` to update `prGroups` in real-time — calling `refreshPR()` for new PRs and updating in-place for known ones — and add `'updated'` to the `EventType` union.

**Files:**
- Modify: `frontend/src/types/api.ts`
- Modify: `frontend/src/composables/useRealtimeStream.ts`
- Modify: `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `EventType` in `api.ts` includes `'updated'`
- [ ] `useRealtimeStream` accepts `prGroups: Ref<PRGroup[]>` and `refreshPR: () => void` as parameters 3 and 4 (shifting `refresh` to position 5... wait — let me re-check the spec signature)

  The spec signature is:
  ```ts
  export function useRealtimeStream(
    packages: Ref<Package[]>,
    events: Ref<Event[]>,
    prGroups: Ref<PRGroup[]>,
    refresh: () => void,
    refreshPR: () => void,
  ): void
  ```

- [ ] When a `package_update` arrives for a project matching `isv:percona:PR:*` and the PR number is NOT in `prGroups`, `refreshPR()` is called
- [ ] When a `package_update` arrives for a known PR project, the package is updated in-place within the matching group and the group's `rollup_state` is recomputed
- [ ] Non-PR `package_update` messages still update `packages` as before
- [ ] `App.vue` calls `useRealtimeStream(rawPackages, events, prGroups, refresh, refreshPR)` with the correct argument order
- [ ] `cd frontend && npm run build` (or `npx tsc --noEmit`) exits 0 with no type errors

**Verify:** `cd frontend && npx tsc --noEmit` → exits 0 with no output

**Steps:**

- [ ] **Step 1: Add `'updated'` to `EventType` in `frontend/src/types/api.ts`**

  Find the `EventType` line (line 3). Replace it with:

  ```ts
  export type EventType = 'triggered' | 'started' | 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'published' | 'created' | 'deleted' | 'build_started' | 'build_finished' | 'version_change' | 'updated'
  ```

- [ ] **Step 2: Rewrite `frontend/src/composables/useRealtimeStream.ts`**

  Replace the entire file with:

  ```ts
  import { onMounted, onUnmounted, type Ref } from 'vue'
  import type { Package, PRGroup, Event } from '../types/api'

  const SEVERITY: Record<string, number> = {
    broken: 5, unresolvable: 4, failed: 3, blocked: 2,
    building: 1, finished: 1, scheduled: 1, succeeded: 0,
  }

  function prNumberFromProject(project: string): string {
    const parts = project.split(':')
    const idx = parts.findIndex(p => p.toLowerCase() === 'pr')
    if (idx >= 0 && idx + 1 < parts.length) {
      return parts[idx + 1].toLowerCase().replace(/^pr-/, '')
    }
    return ''
  }

  export function useRealtimeStream(
    packages: Ref<Package[]>,
    events: Ref<Event[]>,
    prGroups: Ref<PRGroup[]>,
    refresh: () => void,
    refreshPR: () => void,
  ): void {
    let es: EventSource | null = null
    let wasError = false

    function connect(): void {
      es = new EventSource('/api/stream')

      es.onopen = (): void => {
        if (wasError) {
          wasError = false
          refresh()
        }
      }

      es.onmessage = (e: MessageEvent): void => {
        const msg = JSON.parse(e.data as string) as { type: string; data: unknown }

        if (msg.type === 'package_update') {
          const pkg = msg.data as Package
          const prNum = prNumberFromProject(pkg.project)

          if (prNum) {
            const group = prGroups.value.find(g => g.pr === prNum)
            if (!group) {
              refreshPR()
            } else {
              const pkgIdx = group.packages.findIndex(
                p => p.project === pkg.project && p.name === pkg.name,
              )
              if (pkgIdx >= 0) {
                group.packages[pkgIdx] = pkg
              } else {
                group.packages.push(pkg)
              }
              const worst = group.packages.reduce((acc, p) => {
                return (SEVERITY[p.rollup_state] ?? 0) > (SEVERITY[acc] ?? 0)
                  ? p.rollup_state
                  : acc
              }, 'succeeded' as string)
              group.rollup_state = worst as PRGroup['rollup_state']
            }
          } else {
            const idx = packages.value.findIndex(
              p => p.project === pkg.project && p.name === pkg.name,
            )
            if (idx >= 0) {
              packages.value[idx] = pkg
            } else {
              packages.value.push(pkg)
            }
          }
        } else if (msg.type === 'new_event') {
          events.value.unshift(msg.data as Event)
          if (events.value.length > 200) {
            events.value.length = 200
          }
        }
      }

      es.onerror = (): void => {
        wasError = true
      }
    }

    onMounted(connect)
    onUnmounted((): void => {
      es?.close()
    })
  }
  ```

- [ ] **Step 3: Update `useRealtimeStream` call in `frontend/src/App.vue`**

  Find the existing call (near the bottom of `<script setup>`):
  ```ts
  useRealtimeStream(rawPackages, events, refresh)
  ```

  Replace with:
  ```ts
  useRealtimeStream(rawPackages, events, prGroups, refresh, refreshPR)
  ```

  `prGroups` is already declared as `const { data: prGroups, refresh: refreshPR } = usePRPackages()` earlier in the same file.

- [ ] **Step 4: Type-check**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard/frontend
  npx tsc --noEmit
  ```

  Expected: exits 0 with no output.

- [ ] **Step 5: Commit**

  ```bash
  cd /home/rdias/Work/percona-obs-dashboard
  git add frontend/src/types/api.ts \
           frontend/src/composables/useRealtimeStream.ts \
           frontend/src/App.vue
  git commit -s -m "feat(frontend): real-time PR dropdown and version tab updates via SSE"
  ```
