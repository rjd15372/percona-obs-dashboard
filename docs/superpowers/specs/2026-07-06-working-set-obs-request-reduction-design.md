# Working-Set OBS Request Reduction — Design

**Date:** 2026-07-06
**Status:** Approved (design)

## Problem

OBS IT reported that the dashboard was making **~1.5M requests/day** to `api.opensuse.org` (≈17 req/s sustained). We needed to determine whether this was a bug (unnecessary requests) or expected by design, and fix it if the former.

### Investigation (evidence)

Attributing requests from the code paths against a production DB snapshot (`data/obsboard.db`, 735 packages):

| rollup_state | count | in working set? |
|---|---|---|
| published | 565 | no (removed on publish) |
| **succeeded** | **158** | **yes — never leaves** |
| failed | 7 | yes |
| unresolvable | 5 | yes |

The working set (packages re-polled by the worker pool) holds the **170 non-`published`** packages. The scheduler (`workingset.go:72 StartScheduler`) re-enqueues every non-inflight package **every 30s** → up to **2,880 passes/package/day**.

**158 of those 170 are stuck at `succeeded`** — every target built (`ok_targets == total_targets`) but no target ever `published`, some stuck since **2026-06-17 (>2 weeks)**. They are build-dependency / helper packages in non-publishing subprojects (e.g. `isv:percona:common:containers:ubi8` → `python3-tomli`, `obs-service-kiwi_*`, `file-devel`).

Confirmed against the live OBS API:
```xml
<!-- /source/isv:percona:common:containers:ubi8/_meta -->
<publish><disable/></publish>          <!-- publishing DISABLED project-wide -->
```
```xml
<!-- _result?package=python3-tomli&view=status -->
<result repository="UBI_8" arch="x86_64" code="unpublished" state="unpublished">
  <status package="python3-tomli" code="succeeded"/>
```
Because the repo publishes nothing, `RepoPublishStates` returns `state="unpublished"` permanently, and `PublishStateTask` (which only promotes on `state == "published"`, `tasks.go:89`) can **never** promote these to `published` — so they never satisfy the working-set exit condition and are re-polled forever.

### Request budget

Each pass of a stuck non-container `succeeded` package runs 4 OBS calls (`BuildStateTask` + `PublishStateTask` + `VersionTask` + `BlockedReasonTask`; the latter has **no guard** and always calls):

> **156 non-container stuck packages × 4 calls × 2,880 passes/day ≈ 1.80M requests/day.**

This single population accounts for the entire reported volume. By contrast the **poller is negligible**: 36 projects → `1 SearchProjects + 36 BuildResults` per 2-min tick × 720 ticks ≈ **27K/day (~2%)**.

### Verdict

- **By design:** re-polling *genuinely in-flight* packages every 30s to track live builds.
- **The bug:** ~93% of traffic is spent re-polling packages that have reached their true terminal state (`succeeded`, all targets built) but can never be promoted to `published` because their repo does not publish. Two weeks of byte-identical re-polls is wasted load, not design.

## Goal

Stop re-polling packages that have nothing left to observe. A package should leave the working set once it is **settled**: `published`, or `failed`, or `succeeded` where every active target has either published or lives in a non-publishing repo. Detect non-publishing repos deterministically from OBS `_meta`. Do **not** alter the displayed `rollup_state` — only working-set membership changes.

**Target:** working set 170 → ~10; OBS traffic ≈1.8M/day → ~130–200K/day (**−90%**).

## Scope & key decisions (locked during brainstorming)

- **Approach A — publish-aware terminal detection** (chosen over a time-based heuristic and over slow-lane polling): detect `publish disabled` from OBS `_meta`, cache it, and treat `succeeded` on non-publishing repos as terminal.
- **Terminal-failure scope: `failed` only.** `broken` and `unresolvable` keep polling every 30s (they may resolve soon when deps/source change; 30s detection preferred over 2m).
- **`rollup_state` is never faked.** The dashboard keeps showing the truthful `succeeded`/`failed`. A new `settled` flag governs working-set membership independently.
- **Persisted `settled` column** so the 158 packages are not re-seeded into the working set on every restart.
- **Adjacent cheap fix:** guard `BlockedReasonTask` so it only calls OBS when the package has a `blocked` target.
- **Telemetry (bundled):** periodic structured log lines reporting (a) working-set size — total, inflight, and by-rollup-state — and (b) OBS request volume over the window — total, window delta, req/s, and **per-endpoint** breakdown. Configurable interval, default 60s. **Disabled by default**, toggleable at runtime via an HTTP endpoint (`GET /api/telemetry` status, `POST /api/telemetry?enabled=…` to set). This is also how we verify the fix in production (watch `succeeded` collapse and req/s drop).
- **Re-entry** relies on the existing poller (≤2 min) and MQ `build_*`/`repo.published` events. There is **no** per-package build-started event in OBS (see `BUILD_STARTED_EVENT_FINDINGS.md`), so removal does not make re-entry meaningfully worse — the 30s in-set self-poll was the only faster signal, and it only ever mattered for packages we now correctly consider done.

**Out of scope (deferred to follow-ups):**
- **Per-pass task caching** (skip re-fetching unchanged `BuildReason`/`Version`/`BlockedReason` when a target's state is unchanged). After Approach A the working set is tiny, so the marginal benefit is small. Revisit if traffic on the remaining ~10 active packages is still a concern.
- Reconsidering `broken`/`unresolvable` as terminal.
- A publish backstop for the case where a `repo.published` event is missed during downtime (pre-existing gap, not worsened here).

## Architecture

### 1. Publish-flag detection — `Client.ProjectPublishFlags`

New method on `internal/obs/client.go`:

```go
// PublishFlags answers "does repository R publish?" for a project.
type PublishFlags struct { /* resolved rules */ }
func (f PublishFlags) Publishes(repo string) bool

func (c *Client) ProjectPublishFlags(ctx context.Context, project string) (PublishFlags, error)
```

Reads `/source/{project}/_meta`, parses the `<publish>` block, and resolves per-repo publish state:

- No `<publish>` block → **all repos publish** (OBS default).
- `<disable/>` (bare) → project-wide default becomes **disabled**; `<enable/>` (bare) → default enabled.
- `<disable repository="X"/>` / `<enable repository="X"/>` → per-repo override.
- **Most specific wins:** a repo-level rule overrides the project-level default. `Publishes(repo)` returns the repo-level rule if present, else the project-level default, else `true`.

### 2. Publish-flag cache

Publish configuration is set at repo/project creation and is effectively immutable for the lifetime of the repo, so the cache is **fetch-once and never expires** — no TTL. Cache inside `Client` (a `sync.Mutex`-guarded `map[string]PublishFlags`): `ProjectPublishFlags` returns the cached entry if present, otherwise fetches `_meta` once and stores it. Both the poller and the worker read through it.

Cost: **one fetch per project, ever** (~36 total, plus one per newly discovered project) — effectively zero ongoing load.

**Eviction on project removal.** Because there is no TTL, the cache must be explicitly cleared when a project is removed — otherwise a project that is deleted and later recreated with different publish config would keep serving stale flags. Add `Client.EvictPublishFlags(project)` (deletes the entry under lock) and call it from **both** project-removal sites:
- MQ consumer `opensuse.obs.project.delete` handler (`consumer.go`, alongside `DeletePackagesByProject`).
- Poller garbage collection of projects no longer in OBS (`poller.go`, alongside `DeletePackagesByProject`).

Package-level deletions do not evict (publish flags are project-scoped). Edge case: a repo added to a project *after* its flags were cached is not in the cached `_meta`; `Publishes(repo)` then falls back to the project-level default, which is acceptable given the stability assumption.

### 3. The `settled` decision

A single pure helper (in `internal/obs`, so both worker and poller can call it):

```go
// Settled reports whether pkg has nothing left for the worker to observe.
func Settled(pkg *model.Package, flags PublishFlags) bool {
    switch pkg.RollupState {
    case model.RollupPublished, model.RollupFailed:
        return true
    case model.RollupSucceeded:
        // Settled iff every active target has published OR its repo does not publish.
        for _, t := range pkg.Targets {
            if skipState(t.State) { // disabled/excluded/locked
                continue
            }
            if flags.Publishes(t.Repo) && !t.Published {
                return false // a publishing repo we're still waiting on
            }
        }
        return true
    default:
        return false // building, scheduled, finished, blocked, broken, unresolvable
    }
}
```

Note: `succeeded` requires per-target evaluation — a package may build into both a publishing repo (`images`) and a non-publishing one (`UBI_8`); it stays in the set until the publishing target actually publishes.

### 4. Working-set removal (worker)

In `worker.ProcessOnce` (`worker.go`), replace the current removal check:

```go
// old:
if pkg.RollupState == model.RollupPublished && pkg.IsContainer != nil {
    p.ws.Remove(pkg.Project + "/" + pkg.Name)
}
```
with a settled-based check that preserves the `IsContainer != nil` guard (type-unknown packages keep polling so `PackageTypeTask` can run — mirrors the existing `is_container IS NULL` seed rule):

```go
flags, _ := p.client.ProjectPublishFlags(ctx, pkg.Project) // cached; empty flags on error → default publishes=true (safe: keeps polling)
settled := obs.Settled(pkg, flags) && pkg.IsContainer != nil
pkg.Settled = settled
// (settled is persisted by the UpsertPackageState call for this pass)
if settled {
    p.ws.Remove(pkg.Project + "/" + pkg.Name)
}
```

Ordering: compute `settled` before the `UpsertPackageState` for this pass so it is persisted, then `Remove`. On `ProjectPublishFlags` error, flags default to "publishes=true", so a transient `_meta` failure keeps the package in the set rather than wrongly dropping it.

### 5. Data model — `settled` column

- Migration: `ALTER TABLE packages ADD COLUMN settled INTEGER NOT NULL DEFAULT 0;` (add to `store.Open` migrations, following the existing column-add pattern).
- `model.Package` gains `Settled bool`.
- `UpsertPackageState` writes `settled = ?` (with `settled=excluded.settled` in the `ON CONFLICT` clause).
- **Writers:**
  - **Worker** sets `pkg.Settled` = the computed decision (§4).
  - **Poller / MQ** upserts fire only on an actual state change, and leave `Settled=false` (default) — automatically un-settling a package that rebuilds. (No code needed beyond defaulting the field to false in those construction paths.)
- **Seeding:** `GetActivePackages` becomes `WHERE settled = 0`. This subsumes the old `rollup_state != 'published' OR is_container IS NULL` condition: published/failed/non-publishing-succeeded packages have `settled=1`; type-unknown packages have `settled=0` (worker only sets settled when `IsContainer != nil`) so they are still seeded.

### 6. Adjacent fix — guard `BlockedReasonTask`

`tasks.go:160 BlockedReasonTask.Run` currently calls `client.PackageBlockedReasons` unconditionally. Add an early return when no target is in `blocked` state, saving one OBS call per pass for every non-blocked package still in the set.

### 7. Observability — working-set & OBS-request telemetry

A single periodic reporter goroutine (in `main.go`, sibling to `runPruner`) logs two structured lines' worth of state every `Telemetry.Interval` (default 60s).

**Working-set stats.** Add to `internal/workingset`:
```go
type Stats struct {
    Total    int
    Inflight int
    ByState  map[string]int // rollup_state → count
}
func (ws *WorkingSet) Stats() Stats // computed under ws.mu
```

**OBS request counters.** Add a small metrics holder to `internal/obs.Client`:
```go
type obsMetrics struct {
    mu     sync.Mutex
    counts map[string]int64 // operation → cumulative count
}
func (m *obsMetrics) inc(op string)
func (c *Client) MetricsSnapshot() map[string]int64 // copy of counts
```
All OBS traffic passes through `Client.get` / `getFile` / `post`. Give each an **operation label** parameter and increment `metrics.inc(op)` there; every public method passes a stable literal op name mapping to its endpoint, e.g.:

| Method | op label |
|---|---|
| `BuildResults` | `build_results` |
| `PackageBuildResults` | `package_build_results` |
| `RepoPublishStates` | `publish_states` |
| `PackageBlockedReasons` | `blocked_reasons` |
| `PackageBuildReason` | `build_reason` |
| `PackageVersionResult` | `version` |
| `ProjectPublishFlags` | `publish_flags` |
| `SearchProjects` | `search_projects` |
| … | (one label per public method) |

This is a broad but mechanical change — every `Client` public method gains one argument to its internal request call.

**Runtime toggle.** A shared `*atomic.Bool` (telemetry enabled), created in `main.go`, seeded from config, and shared between the reporter goroutine and the API handler.

**Reporter.** `runTelemetry(ctx, ws, client, interval, enabled)` keeps the previous `MetricsSnapshot` and ticks every `interval`. **Each tick it refreshes its `prev` baseline unconditionally** (so window deltas never accumulate across disabled periods), but **emits the log line only when `enabled.Load()` is true**:
```
slog.Info("telemetry",
    "window", interval,
    "ws_packages", s.Total, "ws_inflight", s.Inflight, "ws_by_state", s.ByState,
    "obs_window", totalDelta, "obs_total", cumulative, "obs_req_per_s", rate,
    "obs_by_endpoint", perOpDelta)
```
(`ws_by_state` and `obs_by_endpoint` rendered as slog sub-groups or compact `key=n` strings — format is a plan-time detail.) OBS counters in `Client` always accumulate regardless of the toggle.

**HTTP endpoint** (added to `api` router; `NewRouter` gains the `*atomic.Bool` toggle and the configured interval for status):
- `GET /api/telemetry` → `{"enabled": bool, "interval": "60s"}`.
- `POST /api/telemetry?enabled=true|false` → sets the flag, returns the new status. Missing/invalid `enabled` → 400.
- Curl: `curl -X POST 'http://host:4000/api/telemetry?enabled=true'`.

**Config.** New block (`config.go`):
- `Telemetry.Interval`: default `"60s"`, env `TELEMETRY_INTERVAL`, parsed like the other durations.
- `Telemetry.Enabled`: default `false`, env `TELEMETRY_ENABLED` (the startup value of the runtime flag).

## Re-entry (safety net, unchanged)

A removed package returns to the working set via:
- **Poller** (≤2 min): re-reads `BuildResults(project)` for every project and compares to every DB row; a state change (e.g. `settled`→`building` on rebuild) triggers `ws.Add`. The poller upsert writes `settled=0`.
- **MQ** (near-instant, real-time projects): `build_success`/`build_fail` → `Signal`; `repo.published` → signals succeeded packages for a publish re-check.

There is no reliance on a per-package build-started event (none exists in OBS).

## Testing strategy

- **`ProjectPublishFlags` parsing:** unit tests over `_meta` fixtures — no block (default publish), bare `<disable/>`, per-repo `<disable repository=.../>`, `<disable/>` + `<enable repository=.../>` override, most-specific-wins.
- **`Settled` helper:** table tests — published→true; failed→true; broken/unresolvable/building/blocked→false; succeeded with all-non-publishing repos→true; succeeded with a publishing-but-unpublished target→false; succeeded mixed (publishing target published + non-publishing target)→true; `skipState` targets ignored.
- **Worker removal:** extend `worker_test.go` — a `succeeded` non-container in a non-publishing project is removed; a `succeeded` package with a publishing-unpublished target is retained; a `failed` package is removed; type-unknown (`IsContainer == nil`) is retained.
- **Cache:** a second `ProjectPublishFlags` call for the same project makes no HTTP request (fake transport call-count assertion — permanent cache); after `EvictPublishFlags(project)` the next call refetches; poller project-GC and MQ `project.delete` both evict the entry.
- **Seeding:** `GetActivePackages` returns `settled=0` rows only; a `settled=1` row is excluded; a type-unknown row is included.
- **`BlockedReasonTask` guard:** no OBS call when there are no blocked targets; call made when a blocked target exists.
- **Telemetry:** `WorkingSet.Stats()` returns correct total/inflight/by-state for a seeded set; `Client` metrics increment per op and `MetricsSnapshot` returns a stable copy; the reporter computes correct window deltas across two ticks (fake clock/manual invocation, no real sleep) and emits nothing while the toggle is off but keeps `prev` fresh.
- **Telemetry endpoint:** `GET /api/telemetry` reflects the current flag; `POST ?enabled=true`/`false` flips it (and the reporter observes the change); invalid/missing `enabled` → 400.
- **Regression:** existing poller/consumer/worker tests still pass (publish-aware path defaults to "publishes" on error, preserving current behavior for publishing projects).

## Expected impact

| | Before | After |
|---|---|---|
| Working set size | 170 | ~10 (broken/unresolvable/blocked/building/succeeded-publishing) |
| OBS req/day | ~1.8M | ~130–200K |
| Reduction | — | **~90%** |

## Follow-ups

1. **Per-pass task caching** (original issue #2): skip `BuildReason`/`BlockedReason`/`Version` re-fetch when a target's state is unchanged since the last fetch. Marginal after this change; revisit if needed.
2. ~~Reconsider `broken`/`unresolvable` as terminal if they prove to be long-lived in practice.~~ **Resolved for `unresolvable` (2026-07-06):** production telemetry showed 24 packages stuck in `unresolvable` re-running the full task chain every 30s — exactly the long-lived case this follow-up anticipated. `Settled` now treats `unresolvable` as terminal alongside `published`/`failed` (user-approved, superseding the earlier "failed only" decision). Re-entry unchanged: the poller re-adds on any rollup change within one poll interval. `broken` remains non-terminal (no evidence yet).
3. Publish backstop for missed `repo.published` events during downtime (pre-existing gap).
4. **Release-project poller `settled` awareness** (surfaced in final review): the release branch of `Poller.tick` re-adds packages by published/container/tags without consulting `settled`, so a *release* `succeeded`-in-non-publishing package can slowly oscillate (re-added every poll tick, re-removed by the worker). The headline 158 stuck packages are all real-time (`common`) and unaffected; fixing this needs publish flags in the poller path. Low priority.
