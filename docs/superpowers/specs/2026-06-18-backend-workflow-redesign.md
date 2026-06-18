# Backend Workflow Redesign

**Goal:** Refactor the backend to support a configurable OBS root project, a unified project classification system, per-package tags, state duration tracking, and a clean split between real-time monitoring and artifacts-only (release) packages.

**Approach:** Introduce a central `ProjectClassifier` that replaces all scattered path-segment logic. Everything else — poller scope, working set eligibility, tag assignment, handler queries — delegates to this classifier. Remaining gaps (configurable root, poller startup, state duration table, MQ filter, release packages in DB) are mechanical fixes on top of this foundation.

---

## Requirements

- The OBS root project is configurable (default `isv:percona`). All OBS project paths are relative to this root.
- Real-time build monitoring covers: `ppg:<version>`, `ppg:<version>:containers:<baseimage>`, `PR:pr-<n>:ppg:<version>`, `PR:pr-<n>:ppg:<version>:containers:<baseimage>`, `ppg:common` (and subprojects), `common` (and subprojects).
- Release projects (`ppg:releases:<version>` and their container subprojects) are never built — they are snapshots. They appear only in the artifacts tab and are served from the DB.
- The global poller triggers immediately on startup.
- The poller syncs the full project tree to the DB, then adds eligible packages to the working set.
- Working set packages are enriched by workers running a task pipeline.
- MQ events trigger immediate worker re-runs without waiting for the poll interval.
- State transition history is recorded per build target for duration queries and statistics.
- Package type (RPM/DEB vs container image) is detected once per package and persisted.
- The artifacts tab is served on demand from the DB — no real-time updates.

---

## Section 1: Configuration & ProjectClassifier

### Config

Add one field to `config.go`:

```go
OBSRoot string // default "isv:percona"
```

All hardcoded `"isv:percona"` strings (poller roots, MQ filter, handlers) are replaced with `cfg.OBSRoot`. The hardcoded `"isv:common"` root is removed — confirmed non-existent.

### ProjectClassifier (`internal/obs/classifier.go`)

A pure, stateless function that takes a full OBS project path and returns a `ProjectKind` and a set of project-level tags. No I/O.

```go
type ProjectKind int

const (
    KindUnknown  ProjectKind = iota
    KindDev       // <root>:ppg:<version>  or  <root>:ppg:<version>:containers:<baseimage>
    KindPR        // <root>:PR:pr-<n>:ppg:<version>  or  ...:containers:<baseimage>
    KindPPGCommon // <root>:ppg:common  and any subproject (e.g. :deps)
    KindCommon    // <root>:common  and any subproject (e.g. :containers:<baseimage>)
    KindRelease   // <root>:ppg:releases:<version>  or  ...:containers:<baseimage>
)

func (k ProjectKind) IsRealTime() bool {
    switch k {
    case KindDev, KindPR, KindPPGCommon, KindCommon:
        return true
    }
    return false
}

// ProjectTags returns project-level tags derived from the project path.
// The "container" tag is NOT returned here — it is set per-package by the worker
// when is_container is confirmed.
func ProjectTags(root, project string) []string

func Classify(root, project string) ProjectKind
```

**Tag assignment by kind:**

| Kind | Example project | Project-level tags |
|------|----------------|-------------------|
| `KindDev` | `<root>:ppg:17` | `["ppg"]` |
| `KindDev` | `<root>:ppg:17:containers:ubi9` | `["ppg"]` |
| `KindPR` | `<root>:PR:pr-42:ppg:17` | `["ppg", "pr"]` |
| `KindPR` | `<root>:PR:pr-42:ppg:17:containers:ubi9` | `["ppg", "pr"]` |
| `KindPPGCommon` | `<root>:ppg:common:deps` | `["ppg", "common"]` |
| `KindCommon` | `<root>:common` | `["common"]` |
| `KindCommon` | `<root>:common:containers:ubi9` | `["common"]` |
| `KindRelease` | `<root>:ppg:releases:17` | `["ppg", "release"]` |
| `KindRelease` | `<root>:ppg:releases:17:containers:ubi9` | `["ppg", "release"]` |

The `"container"` tag is appended by the worker (not the classifier) when `is_container` transitions to `1`. All existing scattered `strings.Contains`/`strings.HasPrefix` scope-inference logic in the poller, handlers, and store is replaced with `Classify(cfg.OBSRoot, project)` and `ProjectTags(cfg.OBSRoot, project)`.

---

## Section 2: Database Schema Changes

### Replace `scope` with `tags`

The `scope TEXT` column is replaced by `tags TEXT` (stored as a JSON array, e.g. `["ppg", "pr"]`).

**Migration:** For each existing row, re-run `ProjectTags(root, project)` on its `project` column to produce the new tags array. Rows with `is_container = 1` get `"container"` appended.

### Add `is_release`

```sql
ALTER TABLE packages ADD COLUMN is_release INTEGER NOT NULL DEFAULT 0;
```

Set by the poller at upsert time: `1` for `KindRelease` projects, `0` for all others. Enables simple SQL filtering without JSON parsing. Release packages start with `rollup_state = 'building'` when first inserted by the poller.

**Migration:** Set `is_release = 1` for all existing rows where `scope = 'release'` (before the scope→tags migration runs).

### Add `published` to `RollupState`

`published` is added to the `RollupState` enum in `model/types.go`. It is the single terminal state for all package types:

- **Real-time packages:** `PublishStateTask` promotes rollup to `published` when every target in `targets_json` has `published = true` and `rollup_state` was `succeeded`. If only some targets are published, rollup stays at `succeeded` and the package remains in the working set for re-check.
- **Release packages:** `BinariesCheckTask` promotes rollup to `published` when binaries are confirmed present.

**Migration:** Set `rollup_state = 'published'` for existing rows where `rollup_state = 'succeeded'` and all entries in `targets_json` have `"published": true`.

### New table: `target_state_durations`

Records every state transition per build target for duration queries and statistics:

```sql
CREATE TABLE target_state_durations (
    project    TEXT     NOT NULL,
    package    TEXT     NOT NULL,
    repo       TEXT     NOT NULL,
    arch       TEXT     NOT NULL,
    state      TEXT     NOT NULL,
    entered_at DATETIME NOT NULL,
    exited_at  DATETIME,            -- NULL = currently in this state
    PRIMARY KEY (project, package, repo, arch, entered_at)
);

CREATE INDEX idx_tsd_lookup ON target_state_durations(project, package, repo, arch);
```

**Write path:** `store.UpsertPackageState` reads the current `targets_json` from the DB before overwriting it. On a state change for a target:
1. Close the open row: `UPDATE target_state_durations SET exited_at = now WHERE project=? AND package=? AND repo=? AND arch=? AND exited_at IS NULL`
2. Open a new row: `INSERT INTO target_state_durations (..., state, entered_at) VALUES (..., new.state, now)`

For targets seen for the first time, only step 2 runs. All callers (poller, MQ consumer, worker) automatically record transitions via the shared upsert path.

**Retention:** No pruning. Duration rows are deleted when their parent package is deleted — both `DeletePackagesByProject` and `DeletePackage` (used by the MQ package-delete path) must issue `DELETE FROM target_state_durations WHERE project=? AND package=?` before or alongside the package delete. No SQLite foreign-key cascade is relied upon.

**Example query** — total build time for package X on RockyLinux_9/x86_64:
```sql
SELECT SUM((JULIANDAY(exited_at) - JULIANDAY(entered_at)) * 86400) AS seconds
FROM target_state_durations
WHERE project = ? AND package = ? AND repo = 'RockyLinux_9' AND arch = 'x86_64'
  AND state = 'building' AND exited_at IS NOT NULL
```

---

## Section 3: Poller Changes

### Immediate startup trigger

`Run()` calls `p.tick(ctx)` once before entering the ticker loop:

```go
func (p *Poller) Run(ctx context.Context) {
    p.tick(ctx) // trigger immediately on startup
    ticker := time.NewTicker(p.interval)
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            p.tick(ctx)
        }
    }
}
```

### Single configurable root

`SearchProjects(ctx, cfg.OBSRoot)` replaces the hardcoded two-root discovery. `"isv:common"` is removed.

### Discovery split: real-time vs. release

Within each `tick()`, the classifier drives two code paths:

**Real-time projects** (`kind.IsRealTime() == true`):
1. Call `BuildResults` to get package names and build state.
2. Upsert packages to DB with `is_release = 0` and project-level tags from `ProjectTags`.
3. Call `ws.Add(pkg)` for packages not yet in the working set.

**Release projects** (`kind.IsRealTime() == false`):
1. Use OBS directory traversal (`ProjectRepos` → `ProjectRepoArchs` → `ProjectRepoPackages`) to enumerate package names and their repo/arch combinations. `BuildResults` is not called — all targets in release projects are disabled/excluded by OBS, so `_results` returns nothing useful.
2. Store the discovered `(repo, arch)` pairs as the package's `targets_json` (without build state — use a placeholder state such as `unknown`). This is the only way to know which targets exist for a release package.
3. Upsert new packages to DB with `is_release = 1`, `rollup_state = 'building'`, and project-level tags.
3. Call `ws.Add(pkg)` for packages where `rollup_state != 'published' OR is_container IS NULL` — these need detection work (type check + binaries check). Newly inserted release packages always qualify since they start with `rollup_state = 'building'` and `is_container = NULL`.

### Garbage collection

After discovery, packages whose `project` is no longer in the discovered set are deleted via `DeletePackagesByProject`. The store layer also deletes their `target_state_durations` rows.

### Working set seed on startup (`GetActivePackages`)

```sql
WHERE rollup_state != 'published' OR is_container IS NULL
```

This condition is now unified — it correctly seeds both real-time packages (not yet done building/publishing) and release packages (not yet detected/binaries confirmed). No `is_release` branching needed.

---

## Section 4: Working Set & Worker Changes

### MQ consumer filter

`"isv:percona:"` replaced with `cfg.OBSRoot + ":"`.

### `container` tag write-back

When `PackageTypeTask` confirms `is_container = 1`, the worker appends `"container"` to the package's `tags` array before upserting. If `is_container` transitions to `0`, `"container"` is never added.

### State duration recording

State transitions must be recorded at the persistence layer, not in `BuildStateTask`. Both the poller and the MQ consumer upsert package state before signalling the working set, so by the time `BuildStateTask` runs the prior target state is already overwritten in the DB.

`store.UpsertPackageState` is extended to record transitions inline:
1. Before writing, read the current `targets_json` from the DB for this `(project, package)`.
2. For each target where `old.state != new.state`, call the duration recording logic: close the open `target_state_durations` row (`SET exited_at = now`) and open a new one (`INSERT ... entered_at = now`).
3. Write the new `targets_json`.

This means every upsert path — poller, MQ consumer, and worker — automatically records transitions with no additional caller changes.

### Task pipeline split

The worker runs a different pipeline based on `pkg.IsRelease`:

**Real-time pipeline** (unchanged task set):
1. `PackageTypeTask` — detect `is_container`
2. `BuildStateTask` — refresh target states and rollup (duration recording happens inside `UpsertPackageState`, not here)
3. `PublishStateTask` — check per-target publish state; promote rollup to `published` when all targets published
4. `BlockedReasonTask` — populate `blocked_by` per target
5. `BuildReasonTask` — fetch build trigger reason per target
6. `VersionTask` — get versrel for RPM/DEB packages
7. `ContainerTagsTask` — get image tags for container packages

**Release pipeline** (detection and verification only):
1. `PackageTypeTask` — detect `is_container`
2. `BinariesCheckTask` — check if binaries are present via `obs.PackageBinaries`; promote rollup to `published` when binaries confirmed
3. `VersionTask` — get version once type is known
4. `ContainerTagsTask` — get container tags if applicable

`BuildStateTask`, `PublishStateTask`, `BlockedReasonTask`, and `BuildReasonTask` do not run for release packages — they have no build state.

### `BinariesCheckTask`

New task in `internal/obs/tasks.go`:

- Iterates over the package's stored targets (populated from directory traversal — `BuildResults` is not used for release packages since all targets are disabled in OBS).
- Calls `obs.PackageBinaries(ctx, project, repo, arch, pkg)` for each target.
- Marks each individual target as having binaries or not.
- Only sets `rollup_state = 'published'` when **all** stored targets have a non-empty binary list — partial availability is not sufficient.
- If any target has no binaries yet: sets `rollup_state = 'building'` (keeps package in working set for next check).

### Auto-remove from working set

A package is removed from the working set when:

```
rollup_state == "published" AND is_container IS NOT NULL
```

This condition is identical for real-time and release packages:
- Real-time: `PublishStateTask` sets `published` only when all targets are published, and `PackageTypeTask` has confirmed `is_container`.
- Release: `BinariesCheckTask` sets `published` only after binaries are confirmed, running after `PackageTypeTask`.

---

## Section 5: API Handler Changes

### Store query split: build packages vs. release packages

`QueryPackages` is split into two functions with distinct filters:

- `QueryBuildPackages(db, root, product, version string) []Package` — returns non-release packages for the builds tab. Executes a union using exact-plus-subproject matching to avoid version prefix collisions (e.g. version `1` matching `10`, `11`): `(project = '<root>:<product>:<version>' OR project LIKE '<root>:<product>:<version>:%')` for dev packages, same pattern for `<root>:<product>:common` and `<root>:common`. Adds `AND is_release = 0`. The `product` parameter preserves the generic `/api/products/{product}/{version}` route contract.
- `QueryReleasePackages(db, prefix string) []Package` — returns only release packages (`AND is_release = 1`) for the artifacts tab. Used by `releasesPackagesHandler`.

The original `QueryPackages(db, projectPrefix)` (prefix LIKE query, no release filter) is retained for internal uses such as GC that need to operate across all package types.

### `packagesHandler` — replace manual common-package append

Currently `packagesHandler` manually appends `isv:percona:common` packages. It is replaced with a call to `store.QueryBuildPackages(db, cfg.OBSRoot, product, version)`, where `product` comes from the `{product}` URL parameter. This preserves the existing generic route and covers dev, product-common, and global common packages in a single store call.

### Releases packages — DB instead of live OBS

`releasesPackagesHandler` and `releasesReposHandler` currently do live OBS enumeration. Since the poller now syncs release packages to the DB, both switch to DB queries:

- `releasesPackagesHandler` → `store.QueryReleasePackages(db, cfg.OBSRoot+":ppg:releases")`
- `releasesReposHandler` → `store.QueryDistinctRepos(db, cfg.OBSRoot+":ppg:releases:"+version)`

Response formats are unchanged.

### Constructor signature changes

`cfg.OBSRoot` must be threaded explicitly — it must not become a package-level constant or global. Two constructors need updating:

- `api.NewRouter(db, hub, obsClient, cfg)` — passes `cfg.OBSRoot` to handlers that build project-path prefixes.
- `mq.NewConsumer(url, cfg.OBSRoot, ...)` — uses root to filter incoming AMQP messages.

`obs.NewPoller(client, db, interval, hub, ws)` also needs a `root string` parameter — today it does not receive config or root. Updated signature: `obs.NewPoller(client, db, interval, hub, ws, root string)`.

### SSE stream

The worker suppresses hub notifications for release packages. After the task pipeline completes, `hub.Notify` is only called when `!pkg.IsRelease`. Release packages go through the detection pipeline (type check, binaries check) but their state changes are not broadcast — the artifacts tab fetches on demand from the DB.

---

## Change Summary

| Area | Change |
|------|--------|
| Config | Add `OBSRoot` (default `isv:percona`) |
| Classifier | New `internal/obs/classifier.go` — `Classify`, `ProjectTags`, `ProjectKind.IsRealTime()` |
| DB schema | `scope` → `tags` (JSON array); add `is_release`; add `published` rollup state; add `target_state_durations` table |
| Model | Add `published` to `RollupState` enum; replace `Package.Scope` with `Tags []string` and `IsRelease bool`; `Event.Scope` and `events.scope` column are **unchanged** (events keep legacy scope) |
| Store | `GetActivePackages`: `rollup_state != 'published' OR is_container IS NULL`; `UpsertPackageState` records state transitions inline; split `QueryPackages` into `QueryBuildPackages(root, product, version)` + `QueryReleasePackages`; both `DeletePackagesByProject` and `DeletePackage` delete from `target_state_durations` |
| Poller | Immediate startup trigger; single configurable root; real-time vs. release discovery split; sets `is_release` on upsert; adds release packages to working set when detection incomplete |
| Worker | `container` tag write-back on `is_container=1`; split task pipeline (real-time vs. release); `PublishStateTask` promotes to `published`; new `BinariesCheckTask`; suppress `hub.Notify` for release packages |
| MQ | Filter uses `cfg.OBSRoot` |
| Handlers | Releases served from DB via `QueryReleasePackages`; `packagesHandler` uses `QueryBuildPackages(root, product, version)` (union of dev + product-common + common); `api.NewRouter` and `mq.NewConsumer` receive `cfg.OBSRoot` explicitly; worker suppresses SSE for release packages |
