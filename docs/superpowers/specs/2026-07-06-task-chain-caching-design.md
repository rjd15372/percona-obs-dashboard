# Task-Chain Per-Pass Caching — Design

**Date:** 2026-07-06
**Status:** Approved (design)

## Problem

Follow-up #1 from the working-set OBS request reduction (`2026-07-06-working-set-obs-request-reduction-design.md`). The ~10 packages that legitimately remain in the working set are re-enriched every 30s, and three tasks re-fetch OBS data that cannot have changed while the target's state is unchanged:

| Task | Cost per pass today | Why it's waste |
|---|---|---|
| `BuildReasonTask` | 1 req **per non-succeeded target**, ×3 retries on failure | The build reason explains why the current cycle was triggered — constant within a cycle; cycles are delimited by state transitions |
| `BlockedReasonTask` | 1 req while any target is blocked | The blocked state persists for minutes–hours; details evolve slowly |
| `VersionTask` | 1 req per pass (non-containers) | versrel only changes when a new build lands, which always shows up as a target state transition |

A blocked 4-target package costs ~6 requests per 30s pass; steady-state that's ~17K req/day for one package.

`BuildStateTask` and `PublishStateTask` are **not** cacheable — they are the polls that detect change in the first place.

## Goal

Skip the `BuildReason`/`BlockedReason`/`Version` re-fetch when the relevant target state is unchanged since the last fetch, without changing the `Task` interface or adding any external cache structure.

## Key decisions (locked during brainstorming)

- **Approach A**: invalidation lives in `BuildStateTask` (the one task that sees old and new state side by side); downstream tasks trust already-populated fields. No `Task` interface change (rejected Approach B), no stateful tasks with internal maps (rejected Approach C).
- **BlockedBy refresh at reduced cadence**: cached, but re-fetched when older than a **5-minute constant TTL** (user chose "re-fetch at reduced cadence" over cache-until-transition and over no caching; TTL is a constant, not config — YAGNI).
- **BuildReason is cycle-cached**: invalidated only by a state transition. Accepted staleness: if OBS's `_reason` output evolves while the state string is constant (possible for `blocked`/`unresolvable`), the UI shows the reason from the last transition. This matches the follow-up's stated rule ("skip when target state unchanged since the last fetch").
- **Conservative polarity everywhere**: every cold-start path (process restart, MQ package replacement, first sighting) defaults to *fetch*, never to *skip*.

## Architecture

### Where the memory lives

The working set holds the same `*model.Package` pointer across passes, and `BuildStateTask` already copies per-target enrichment from the previous pass's targets onto the fresh ones. The cache **is** the model — two new in-memory-only fields, both tagged `json:"-"` so they are excluded from JSON and therefore from both the `targets_json` DB column and API responses:

```go
// model.Target
BlockedByFetchedAt time.Time `json:"-"` // when BlockedBy was last fetched; zero = never/unknown

// model.Package
TargetsStable bool `json:"-"` // set by BuildStateTask each pass: true only when stability was positively confirmed
```

Lifecycle: fields survive across passes via the shared pointer; after a restart (DB seed) or an MQ `Signal` replacement they are zero → one conservative refetch, then cached again.

### 1. `BuildStateTask` — the single invalidation point

Preservation becomes **state-conditional**, and the task computes the stability signal:

```go
updated := buildPackage(...)
stable := len(pkg.Targets) > 0 && len(pkg.Targets) == len(updated.Targets)
for i := range updated.Targets {
    matched := false
    for _, old := range pkg.Targets {
        if old.Repo == updated.Targets[i].Repo && old.Arch == updated.Targets[i].Arch {
            matched = true
            if old.State == updated.Targets[i].State {
                // unchanged: carry the cache forward
                updated.Targets[i].BlockedBy = old.BlockedBy
                updated.Targets[i].BuildReason = old.BuildReason
                updated.Targets[i].BuildReasonPackages = old.BuildReasonPackages
                updated.Targets[i].BlockedByFetchedAt = old.BlockedByFetchedAt
            } else {
                stable = false // state transition: leave zero values → downstream refetch
            }
            break
        }
    }
    if !matched {
        stable = false // new target appeared
    }
}
pkg.TargetsStable = stable
```

`TargetsStable` is `true` **only** when the previous pass had targets, the target set is identical, and every state matched — all cold-start paths yield `false` → fetch.

### 2. `BuildReasonTask` — skip populated targets

In the per-target loop, before fetching:

```go
if target.BuildReason != "" {
    continue // cached for this cycle; a state transition would have wiped it
}
```

Edge case: a target whose OBS `_reason` is legitimately empty keeps fetching every pass — identical to today's behaviour, no regression, no gain.

### 3. `BlockedReasonTask` — populated + fresh

The existing "no blocked target → return" guard stays. The fetch condition becomes: fetch iff **any** blocked target has `BlockedBy == ""` **or** `time.Since(BlockedByFetchedAt) > blockedByTTL`:

```go
const blockedByTTL = 5 * time.Minute
```

After a successful fetch, for each blocked target that receives a non-empty reason: set `BlockedBy` and stamp `BlockedByFetchedAt = time.Now()`. Stamp-on-value-set (not stamp-on-attempt): a newly blocked target whose details OBS hasn't computed yet keeps retrying every 30s until details appear (today's freshness), while a blocked target *with* a known reason refreshes every 5m instead of every 30s.

### 4. `VersionTask` — skip when stable

```go
if pkg.Version != "" && pkg.TargetsStable {
    return nil
}
```

Fetches when the version is unknown, when any target changed, or when stability was not confirmed (cold start). The only theoretical miss — an entire build cycle completing invisibly between two observations with no state delta — is covered by the conservative polarity on restart/replace, the 30s cadence while running, and the fact that a version bump requires a build that transitions states.

### Untouched paths (verified)

- **Event emission** (`emitBuildEvents`): compares the pre-chain `oldTargets` snapshot against final targets; mid-chain wiping doesn't alter the old side, and the new side is refetched on every transition — `build_started`/`blocked`/`failed` event behaviour is unchanged.
- **MQ `mergePackageTarget`**: already preserves enrichment state-conditionally for its own merge; `BlockedByFetchedAt` arrives zero after a replace → one refetch.
- **Poller `preservePackageEnrichment`**: operates on its own structs for DB writes; the working-set pointer is untouched (poller `Add` dedups).
- **Release chain** (`PackageType`, `ContainerTags`, `BinariesCheck`): contains none of the three cached tasks.
- **`Published`/`PublishStateTask`**: not part of the preservation change (`BuildStateTask` never preserved `Published`).

## Expected impact

Steady-state pass for a blocked 4-target package: ~6 requests → **1** (`PackageBuildResults` only; blocked-reason refresh amortized to 1 per 10 passes). Building/unresolvable multi-target packages: 1 + N → 1 per pass between transitions. Verifiable live via the telemetry endpoint (`build_reason`, `blocked_reasons`, `version` counters should flatten while `package_build_results` continues at the polling rate).

## Testing strategy

- **`BuildStateTask` preservation matrix**: state unchanged → all four fields carried; state changed → wiped; target added/removed → `TargetsStable == false`; identical set/states → `true`; empty previous targets → `false`.
- **`BuildReasonTask`**: populated reason → no HTTP call (httptest counter); empty reason → fetches; state transition wipe → refetches.
- **`BlockedReasonTask`**: fresh `BlockedByFetchedAt` (now) → skip; stale (now−6m) → fetch; empty `BlockedBy` → fetch; stamp set only on value receipt. Deterministic via constructing timestamps directly — no clock injection.
- **`VersionTask`**: `Version != "" && TargetsStable` → skip; empty version or unstable → fetch.
- **Serialization**: `json.Marshal` of `Target`/`Package` excludes the two new fields (guards the DB/API invariant).
- **Regression**: full existing suite (worker event emission, poller, MQ) stays green.

## Out of scope

- Caching `PackageTypeTask` (already self-caching via `IsContainer != nil`) and `ContainerTagsTask` (containers only, self-limiting).
- Configurable TTL for BlockedBy (constant 5m until proven insufficient).
- Negative-result caching for targets with legitimately empty `_reason`.
