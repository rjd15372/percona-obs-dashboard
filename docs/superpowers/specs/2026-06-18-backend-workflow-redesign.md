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

All hardcoded `"isv:percona"` strings (poller roots, MQ filter, handlers) are replaced with `cfg.OBSRoot`. The hardcoded `"isv:common"` root is removed — it does not exist.

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

**Write path:** `BuildStateTask` already diffs old vs new targets. On a state change for a target:
1. Close the open row: `UPDATE target_state_durations SET exited_at = now WHERE project=? AND package=? AND repo=? AND arch=? AND exited_at IS NULL`
2. Open a new row: `INSERT INTO target_state_durations (..., state, entered_at) VALUES (..., new.state, now)`

For targets seen for the first time, only step 2 runs.

**Retention:** No pruning. Duration rows are deleted when their parent package is deleted (store layer cascade).

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
1. Use OBS directory traversal (`ProjectRepos` → `ProjectRepoArchs` → `ProjectRepoPackages`) to enumerate package names. `BuildResults` is not called — release packages are never built.
2. Upsert new packages to DB with `is_release = 1`, `rollup_state = 'building'`, and project-level tags.
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

`BuildStateTask` calls `store.RecordStateTransitions(db, pkg, oldTargets, newTargets, now)` after computing new target states. The store function closes open duration rows for changed targets and opens new ones.

### Task pipeline split

The worker runs a different pipeline based on `pkg.IsRelease`:

**Real-time pipeline** (unchanged task set):
1. `PackageTypeTask` — detect `is_container`
2. `BuildStateTask` — refresh target states and rollup; record state transitions
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

- Iterates over the package's targets.
- Calls `obs.PackageBinaries(ctx, project, repo, arch, pkg)` for each target.
- If any target returns a non-empty binary list: sets `rollup_state = 'published'`.
- Otherwise: sets `rollup_state = 'building'` (keeps package in working set for next check).

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

### Releases packages — DB instead of live OBS

`releasesPackagesHandler` and `releasesReposHandler` currently do live OBS enumeration. Since the poller now syncs release packages to the DB, both switch to DB queries:

- `releasesPackagesHandler` → `store.QueryPackages(db, cfg.OBSRoot+":ppg:releases")`
- `releasesReposHandler` → `store.QueryDistinctRepos(db, cfg.OBSRoot+":ppg:releases:"+version)`

Response formats are unchanged.

### `packagesHandler` — remove manual common-package append

Currently `packagesHandler` manually appends `isv:percona:common` packages to version-specific results. Since the poller now syncs all projects under `cfg.OBSRoot` (including `common` subtree), common packages appear in the DB naturally. The handler becomes a single query:

```go
store.QueryPackages(db, cfg.OBSRoot+":ppg:"+version)
```

`QueryPackages` gains an `AND is_release = 0` filter to ensure release packages never appear in builds tab responses.

### SSE stream

No changes. Release packages never reach the worker's broadcast path since their task pipeline has no `BuildStateTask`.

---

## Change Summary

| Area | Change |
|------|--------|
| Config | Add `OBSRoot` (default `isv:percona`) |
| Classifier | New `internal/obs/classifier.go` — `Classify`, `ProjectTags`, `ProjectKind.IsRealTime()` |
| DB schema | `scope` → `tags` (JSON array); add `is_release`; add `published` rollup state; add `target_state_durations` table |
| Model | Add `published` to `RollupState` enum; replace `Scope` field with `Tags []string` and `IsRelease bool` |
| Store | `GetActivePackages`: `rollup_state != 'published' OR is_container IS NULL`; new `RecordStateTransitions`; `QueryPackages` adds `is_release = 0` filter; `DeletePackagesByProject` cascades to `target_state_durations` |
| Poller | Immediate startup trigger; single configurable root; real-time vs. release discovery split; sets `is_release` on upsert; adds release packages to working set when detection incomplete |
| Worker | `container` tag write-back on `is_container=1`; `RecordStateTransitions` in `BuildStateTask`; split task pipeline (real-time vs. release); `PublishStateTask` promotes to `published`; new `BinariesCheckTask` |
| MQ | Filter uses `cfg.OBSRoot` |
| Handlers | Releases served from DB; `packagesHandler` drops manual common-package append |
