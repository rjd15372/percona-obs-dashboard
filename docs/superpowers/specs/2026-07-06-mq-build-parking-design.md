# MQ Build Parking — Design

**Date:** 2026-07-06
**Status:** Approved (design)

## Problem

After the working-set reduction, task-chain caching, and the A+B+C refinements, the dominant remaining worker traffic is `package_build_results` (~2 req/min per in-set package): packages whose targets are **building** re-poll `BuildStateTask` every 30s for the whole build duration (10–60 min for PG builds), even though there is nothing left to enrich — the build reason is known, and the *completion* of each target is announced by MQ (`opensuse.obs.package.build_success` / `build_fail` / `build_unchanged`, per project·package·repo·arch).

## Goal

Remove build-waiting packages from the working set ("park" them) and wake them with the MQ completion events, with the existing poller re-add as the loss-tolerant fallback. Polling during a build drops to ~zero; each target completion costs one enrichment pass.

## Key decisions (locked during brainstorming)

- **Park scope: building + inert succeeded** (user: "building + succeeded/published"). A target is *inert-succeeded* when it is `succeeded` AND (`Published` already, or its repo does not publish). **`scheduled` targets do NOT park** — the package keeps its 30s poll until all targets are building/inert.
- **Succeeded-unpublished in a publishing repo is NOT parkable.** Publication is only detected by `PublishStateTask` polling: the `repo.published` MQ event re-adds only rollup-`succeeded` packages (mixed packages roll up as building), and a *missed* `repo.published` has no poller fallback (publication changes no target state, so the poller never re-adds). Parking these could strand a package at succeeded-unpublished forever. They keep polling — the publish-detection loop is unchanged.
- **Parking is in-memory only** — it does NOT set `settled`. Parked packages stay `settled=0`, are re-seeded on restart, and the first pass re-parks them (one pass of cost, conservative).
- **`build_unchanged` becomes a wake signal** and maps to target state `"finished"` (not `succeeded`), so the re-added pass still observes the `finished→succeeded` transition and the succeeded build event fires through the normal state machine.
- **No new working-set machinery** — parking is `ws.Remove`; waking is the existing `Signal` (MQ) / `Add` (poller) paths.

## Correctness argument (the two nets)

1. **MQ (primary, instant):** `build_success`/`build_fail` already flow through `mergePackageTarget → ws.Signal`, which (re-)adds the package regardless of current membership. `build_unchanged` joins them. `mergePackageTarget` marks the completed target `"finished"`, so the worker's pre-chain snapshot still sees the `finished→succeeded` transition — build events are not lost to parking.
2. **Poller (fallback, ≤1 tick):** for real-time projects the poller `ws.Add`s on any rollup/target change. A parked package whose MQ event is lost (auto-ack, disconnect) is re-added within one poll interval. Parking therefore never weakens the correctness guarantee — it trades the 30s self-poll for MQ push with poller fallback.

Re-added pointers (from `Signal`'s replacement or the poller's fresh package) have `CacheWarm=false` → the first pass fetches everything fresh, emits events for observed transitions, then re-parks, keeps polling, or settles as the new state dictates.

## Architecture

### 1. `Parkable` helper — `internal/obs/parkable.go`

Pure function, sibling to `Settled`:

```go
// Parkable reports whether pkg is waiting only on build completions that MQ
// will announce (with the poller as fallback) — nothing left to poll for.
// It requires at least one building target: an all-inert package is Settled's
// territory, and anything else (scheduled, blocked, finished, broken,
// succeeded-unpublished-in-a-publishing-repo, …) still needs polling.
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

The `BuildReason != ""` requirement ensures the enrichment for the current cycle completed before parking (a building target whose reason fetch failed keeps polling until it succeeds).

### 2. Worker — park alongside settle

In `ProcessOnce`, the removal check becomes:

```go
	if pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg, flags)) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```

(`flags` is already computed earlier in the pass; the `IsContainer != nil` gate mirrors the settled guard so type detection completes before parking. `pkg.Settled` already embeds its own `IsContainer` gate.)

### 3. MQ consumer — `build_unchanged` wakes

In `internal/mq/consumer.go`:
- Delete the early return `if key == "opensuse.obs.package.build_unchanged" { return }` — unchanged events now flow through the same `mergePackageTarget` + upsert + `Signal` path as success/fail.
- In `mqStateToRollup`, map `build_unchanged` (the current `default` arm) to `model.RollupFinished` instead of `RollupSucceeded`, so the merged target state is `"finished"` and the worker derives the real terminal state (and its events) from OBS on the wake pass.
- The existing `if pkg.RollupState != model.RollupSucceeded { c.ws.Signal(pkg) }` gate stays; with unchanged→finished the merged rollup is never prematurely `succeeded`, so the signal fires.

### 4. No other changes

Poller, working set, store, Settled, telemetry: untouched. Parked packages simply vanish from `ws_by_state` (they are not in the set); their DB rows and UI presentation are unaffected.

## Life-cycle walkthrough (multi-target package)

1. Triggered → scheduled: polls (not parkable), reason fetched.
2. All targets building (reasons known) → **parked**.
3. Target 1 completes → MQ `build_success` → `Signal` (target 1 = `finished`, rest building) → wake pass: BuildStateTask observes `finished→succeeded`, succeeded event fires (non-publishing) or publish-wait begins; if the remaining targets are building and target 1 is inert → **re-parked**.
4. Repeat per completion. Final wake pass: all inert → `Settled` → removed permanently (or publish-wait polling if a publishing repo is still unpublished).
5. Restart mid-build: re-seeded (`settled=0`), one cold pass (fetch + re-park).
6. MQ outage: poller re-adds on the state change within one tick; everything proceeds at poller latency.

## Expected impact

Each building package stops costing ~2 req/min for the build duration; a target completion costs one enrichment pass (~1–3 requests). With ~10 in-flight packages, `package_build_results` drops from ~20/min to transition passes only; steady-state traffic ≈ the poller alone (~47/min in the user's environment).

## Testing strategy

- **`Parkable` table tests:** all-building-with-reasons → true; building without reason → false; building + inert succeeded (Published) → true; building + inert succeeded (non-publishing repo) → true; building + succeeded-unpublished-publishing → false; any scheduled/blocked/finished/broken target → false; all-inert no building → false; skipState targets ignored; empty targets → false.
- **Worker:** a parkable package is removed from the working set after a pass (mirror the settled-removal tests, `_meta` fixture for non-publishing); a package with a scheduled target is retained; parking does not set `settled` (DB row remains `settled=0`).
- **MQ consumer:** `build_unchanged` no longer early-returns — it upserts a merged package whose completed target is `"finished"` and Signals the working set; `mqStateToRollup("...build_unchanged")` → `RollupFinished`.
- **Regression:** full suite; existing MQ success/fail tests unchanged.

## Follow-ups (from final review, 2026-07-06 — accepted, not blocking)

1. **Working-set `Remove`/`Done` race**: `ws.Remove` (in `ProcessOnce`) clears `inflight` before the worker loop's trailing `ws.Done`; a `Signal` landing in that microsecond window can double-dispatch a package. Pre-existing for settled removals; parking makes `Remove` more frequent. Consequence: one duplicate (idempotent) pass. Fix idea: generation-count the inflight flag or skip `Done` after a worker-initiated `Remove`.
2. **MQ-ahead-of-`_result`**: if the wake event outruns OBS's `_result` view, the wake pass re-parks with the completion unobserved; the poller re-adds at the next tick (designed fallback), but the wake event is spent.
3. **Missed second build cycle while parked**: a rebuild cycling building→scheduled→building entirely between poller ticks keeps the stale reason and delays the cycle-2 `build_started` event until completion — pre-existing preservation gap, widened from the 30s worker poll to the poller interval.
4. **Composed wake-loop integration test**: merge-preservation → BuildStateTask preservation → zero reason refetches → re-park is verified piece-wise but not as one integration test with a fake client.

## Out of scope

- Parking `scheduled` targets (user decision — keeps the 30s scheduled→building freshness).
- Parking succeeded-unpublished-publishing targets (correctness: no fallback for missed `repo.published`).
- A `repo.published` fallback in the poller (pre-existing gap, unchanged).
- Any new "dormant" state in the working set (plain Remove + existing re-add paths suffice).
