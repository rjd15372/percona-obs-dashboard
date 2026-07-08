# Park Succeeded Packages Awaiting Publish — Design

**Date:** 2026-07-08
**Status:** Approved (design)

## Problem

A package whose builds all `succeeded` in a publishing repo is neither `Settled` (publication pending) nor `Parkable` (that predicate requires a building target), so it stays in the working set and re-runs the task chain every 30s until the repo publishes. OBS publishes a repo only when **every** package in that repo·arch reaches a terminal state, so the entire repo's worth of succeeded packages polls for the whole tail of the slowest build — often hours. This is the largest remaining class of steady-state OBS requests.

## Decision (locked during brainstorming)

Park succeeded-awaiting-publish packages (remove from the working set) and wake them on the MQ `repo.published` event, with the existing ~2-minute poller as the loss-tolerant fallback — **at zero additional OBS requests**, by reading the repo-level publish state the poller's `_result` response already contains (Approach A).

Rejected:
- **Trusting `repo.published` to set state directly (no wake pass):** the event payload has no `arch` (per-arch publish states differ: one arch can be published while another still builds), the event doesn't guarantee a just-succeeded package made it into that publish run, and a direct write would bypass the worker's transition machinery (EventSucceeded, CVE scan enqueue, rollup promotion, `settled` recompute, SSE). Principle: MQ events say *when* to look; OBS is the source of truth for *what* happened.
- **`repo.publish_state` subscription + timed sweep:** more moving parts, real request cost, and worse worst-case staleness than the poller fallback.
- **Per-package poll backoff:** keeps polling, requires per-package cadence the working set doesn't have.

Verified facts (from `BUILD_STARTED_EVENT_FINDINGS.md` and code inspection):
- `repo.published` fires per **project + repo** (payload `project`, `repo`, `buildid` — no arch), and **only when a publish run changed content**; "nothing changed" runs return before the notify.
- The per-repo·arch state attribute in `_result` (`buildResult.State`, already parsed by `client.BuildResults` and discarded) is the same publish state `PublishStateTask` reads via `RepoPublishStates` — it flips to `published` even on "nothing changed" runs.
- The MQ consumer already subscribes to `repo.published` and wakes `rollup_state = 'succeeded'` packages of the project (`consumer.go`, `GetFinishedPackagesByProject` → `ws.Signal`).
- The poller re-adds packages only on build-state changes; publication is invisible in that diff — hence the fallback must come from the repo-state attribute, not the package diff.

## Architecture

### 1. Parking predicate — generalize `Parkable` (`internal/obs/parkable.go`)

A package parks when **every active (non-`skipState`) target** is either:

- `building` with `BuildReason != ""` (unchanged; wake: `package.build_success/fail/unchanged`), or
- `succeeded` — regardless of `Published` (new; wake for unpublished targets: `repo.published` + poller fallback),

and there is **at least one active target**. The `hasBuilding` requirement is removed: all-succeeded-unpublished packages park, and the mixed case (building + succeeded-unpublished) — previously unparkable — parks too. All-inert packages are `Settled`'s territory and are removed by the settled branch first. The worker's removal condition stays `pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg, flags))`; parking remains in-memory (`settled` stays 0 — a restart re-seeds and re-parks after one chain pass).

### 2. MQ wake path — repo filter (`internal/mq/consumer.go`)

The existing `repo.published` handler keeps its shape (release skip, `GetFinishedPackagesByProject`, `ws.Signal`) with one refinement: signal only packages having **at least one target with `State == "succeeded" && !Published && Repo == m.Repo`** (in-memory filter). Packages waiting on a different repo aren't re-run; settled packages in never-publishing repos are naturally excluded (those repos never emit `repo.published`).

### 3. Poller fallback — repo publish states at zero cost (`internal/obs/client.go`, `internal/obs/poller.go`)

- `client.BuildResults` additionally returns `map[string]string` of `"repo/arch" → state`, populated from the `state` attribute it already parses. Same single `_result` request; no new API call.
- In the poller tick, per stored package (`prev`): if any target has `State == "succeeded" && !t.Published` and the fresh map has that target's `"repo/arch"` equal to `"published"`, call `ws.Add(pkg)` (dedup makes this harmless if the package is already queued). No publish-flag lookup: never-publishing repos never reach state `published`, so the condition self-selects.
- The worker's `PublishStateTask` verifies and promotes as usual on the wake pass.

This one check covers both gaps: a lost MQ event (caught within one poller tick) and "nothing changed" publish runs that never emit `repo.published`.

### 4. Unchanged

`Settled`, the release-project path (poller re-add rules, `BinariesCheckTask`, MQ release skip), the publish-flag cache and its eviction, restart/seeding behavior, telemetry counters.

## Races and error handling

- **Package succeeds just after the publisher's snapshot:** the wake pass reads the real repo state, sees not-yet-published, re-parks; the next publish run (event or poller tick) completes it. Never a premature `Published`.
- **One arch published, another still building:** the wake pass marks only the published arch's target; the package re-parks until the rest catch up. Multi-repo packages behave the same per repo.
- **Package starts rebuilding while parked:** `package.build_*` events and the poller's build-state diff re-add it through the existing paths; parking blocks neither.
- **Publish permanently failing on OBS's side:** repo state never reaches `published`; the package stays parked, correctly showing `succeeded`. Polling would learn nothing more; the poller keeps checking for free.
- **MQ down entirely:** worst-case staleness is one poller tick (≤2 min), strictly better than the previous proposal of a 10–30 min sweep.

## Testing

- `Parkable` unit tests: all-succeeded-unpublished parks; mixed building(+reason)+succeeded parks; any scheduled/blocked/failed/finished/broken target blocks parking; building without reason blocks; no active targets blocks.
- `BuildResults` parsing test: repo-state map populated from `_result` XML alongside package states.
- Poller test: stored succeeded-unpublished package + fresh repo state `published` → `ws.Add`; state `building`/`publishing` → no add.
- Consumer test: `repo.published` wakes only packages with a succeeded-unpublished target in the event's repo; packages in other repos and release projects untouched.
- `go test ./...` green.

## Expected effect

Packages awaiting publication drop from ~2+ OBS requests/min each (30s chain passes for hours) to a handful of requests once per actual publish transition, with ≤ one poller tick of staleness if the MQ moment is missed. Observable via the existing telemetry (`working set size`, requests/min).

## Out of scope

- Release-project publish detection (stays on `BinariesCheckTask`).
- Batching the wake pass's publish-state reads per project (noted as a future lever if wake bursts ever matter in telemetry).
- Any change to `repo.publish_state` handling (not subscribed; not needed with the poller fallback).
