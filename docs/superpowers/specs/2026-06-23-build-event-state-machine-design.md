# Build Event State Machine Design

## Goal

Restructure the build event log so it captures a faithful, per-target timeline of every package build. Each target's lifecycle is represented as a state machine that can be replayed in order to reconstruct what happened and why.

## Problem Statement

The current system has two independent event emitters — the worker (per-target diffs) and the poller (package rollup state changes) — that overlap and produce duplicates. A single package build cycle today generates roughly 44 events: `build_started` × 14, `succeeded` × 15, `published` × 14, `blocked` × 1. 60% of all events are `published`, which carry zero diagnostic value. `succeeded` fires both per-target and at the rollup level, often as an intermediate step toward `published` rather than a true terminal state. Intermediate states (`blocked`, `unresolvable`, `broken`) carry no `repo`/`arch` detail and no actionable reason.

## Architecture

**Single emitter:** The worker (`worker/worker.go`) is the sole source of all build events. The poller's `stateChangeEvent` path is removed entirely. The MQ consumer (`created`/`deleted`) is untouched.

**Per-target granularity:** Every event carries `project`, `package`, `repo`, and `arch`. This makes it possible to filter, group, and reconstruct timelines at the individual target level.

## Per-Target State Machine

```
build_started(why=reason)
  → blocked(why=blocked_by)?
  → unresolvable(why=details)?
  → broken(why=details)?
  ... (intermediate states can repeat across poll cycles)
  → succeeded
  OR failed(why=reason)
```

Intermediate states (`blocked`, `unresolvable`, `broken`) may occur zero or more times between `build_started` and the terminal state. The terminal state is either `succeeded` (publication) or `failed`.

## Event Triggers

| Event | Trigger condition | `why` field |
|-------|------------------|-------------|
| `build_started` | `old.BuildReason == ""` AND `t.BuildReason != ""` — fires regardless of target state | OBS build reason + triggering packages |
| `blocked` | State transitions to `"blocked"` AND `t.BuildReason != ""` | `t.BlockedBy` |
| `unresolvable` | State transitions to `"unresolvable"` AND `t.BuildReason != ""` | `t.Details` |
| `broken` | State transitions to `"broken"` AND `t.BuildReason != ""` | `t.Details` |
| `succeeded` | `!old.Published && t.Published` (publication = real success) **OR**, for repos with publishing disabled, state transitions to `"succeeded"` (`!flags.Publishes(t.Repo)` — amended 2026-07-06) | — |
| `failed` | State transitions to `"failed"` (not `unresolvable`/`broken` — those are intermediate) | scaffolded empty; populated when source is available |

### Key invariants

**`build_started` fires as soon as OBS assigns a BuildReason**, regardless of whether the target state is `"building"`, `"blocked"`, or anything else. This fixes the existing gap where targets that jump directly to `blocked` (dependency not ready) before reaching `"building"` would silently drop the event.

**Intermediate states only emit after `build_started`.** The guard `t.BuildReason != ""` enforces this: since `build_started` fires the moment `BuildReason` appears, any subsequent (or same-cycle) intermediate state is guaranteed to follow it. If a target goes `blocked` in the same poll cycle that `BuildReason` first appears, the event log records `build_started` then `blocked` — both ordered correctly within a single worker cycle.

**`succeeded` = publication, not `State == "succeeded"`.** OBS marks a target as `succeeded` when the build binary is ready, and separately flips `Published` when the package is available in repos. Publication is the real consumer-visible success. Emitting on the `Published` flag flip means one clean `succeeded` per target per build, rather than multiple intermediates.

**Amendment (2026-07-06): non-publishing repos emit on the state transition.** For projects with `<publish><disable/></publish>` (e.g. `isv:percona:ppg:common:deps`), `Published` never flips, so the publication gate silently swallowed all success events. `emitBuildEvents` now takes the package's cached `PublishFlags` and additionally fires `succeeded` on `old.State != "succeeded" && t.State == "succeeded" && !flags.Publishes(t.Repo)` — for a repo that never publishes, the build-state transition is the only terminal moment there is. Publishing repos keep publication-gated semantics; both conditions feed a single append (never a duplicate). Known inherited blind spot: a full cycle completing between observations (package settled and re-added already-`succeeded`) shows no transition and emits nothing — same window the publication path always had.

**`published` events are removed.** Publishing is now represented by `succeeded`. No separate `published` event is emitted.

**Poller emits no events.** The poller's `stateChangeEvent` function, `isTransientRollup` helper, and associated `fmt`/`ulid` imports are deleted. The poller continues to manage package discovery, working set population, and DB upserts — unchanged.

## Backend Changes

### `backend/internal/worker/worker.go`

`emitBuildEvents` is restructured. The per-target loop processes checks in this fixed order:

1. **`build_started`** — `old.BuildReason == "" && t.BuildReason != ""`; sets a `buildStartedThisCycle` flag
2. **Intermediate states** — guarded by `t.BuildReason != ""`; state transition check per type; `why` from `t.BlockedBy` or `t.Details`
3. **`succeeded`** — `!old.Published && t.Published`
4. **`failed`** — `old.State != "failed" && t.State == "failed"`; `why` empty, scaffolded

The old per-target `succeeded` (on `State == "succeeded"`) and `published` blocks are removed. There are no package-level events after the loop.

### `backend/internal/obs/poller.go`

The following are deleted:
- The `if rollupChanged && !isTransientRollup(...)` block that called `stateChangeEvent`
- `func isTransientRollup(s model.RollupState) bool`
- `func stateChangeEvent(pkg *model.Package, prev *model.Package) *model.Event`
- Unused imports: `"fmt"`, `"github.com/oklog/ulid/v2"`

The stale comment referencing "flood of stateChangeEvents" is updated.

### `backend/internal/model/types.go`

No type constants are deleted. `EventPublished`, `EventTriggered`, `EventBuildFinished`, `EventVersionChange`, `EventUpdated` remain defined — they are simply no longer emitted. This avoids breaking the frontend TypeScript `EventType` union.

## Frontend Changes

### `frontend/src/composables/useEventDisplay.ts`

`showReason` is extended to surface the `why` field for intermediate states:

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

### No other frontend changes required

- `EventLog.vue` — `TYPE_META` already covers all types. `availableTypes` is computed dynamically from live events; `published` vanishes from the filter automatically.
- `EventRow.vue` and `PackageEventGroup.vue` — already render `repo`/`arch` when present; intermediate states now carry these fields and display correctly.
- `GLYPH`, `GLYPH_COLOR`, `GLYPH_BG` in `useEventDisplay.ts` — all 14 types already defined.

## Testing

### Worker tests to update (`backend/internal/worker/worker_test.go`)

- `TestProcessOnceEmitsPublished` → renamed `TestProcessOnceEmitsSucceededOnPublish`; assertion changes from `EventPublished` to `EventSucceeded`; trigger is `Published` flag flip
- `TestProcessOnceNoEventForBlocked` → updated: when `BuildReason` is present on a `blocked` target, expect `build_started` + `blocked`; when `BuildReason` is absent, expect 0 events

### New worker tests to add

| Test | Scenario | Expected |
|------|----------|----------|
| `TestBuildStartedFiresOnBlockedState` | BuildReason appears while target is already `blocked` | `build_started` then `blocked`, in order |
| `TestIntermediateStateRequiresBuildReason` | Target goes `blocked` with no `BuildReason` | 0 events |
| `TestIntermediateStatesFireInSequence` | `blocked` → `unresolvable` → `broken` across cycles | 3 events, all with `why`, correct order |
| `TestSucceededOnPublishNotOnState` | `State == "succeeded"` but `Published` stays false | 0 events |
| `TestSucceededOnPublishFlip` | `Published` flips from false to true | 1 `succeeded` event with `repo`/`arch` |
| `TestFailedTerminal` | Target transitions to `"failed"` | 1 `failed` event, `why` empty |
| `TestNoPollerRollupEvents` | Poller processes a rollup state change | 0 events appended to DB |
