# PR Context — One Context Per PR Design

**Date:** 2026-06-30
**Status:** Approved (design)

## Problem

A PR whose packages live entirely outside the product subproject — e.g. only under `isv:percona:PR:pr-NNN:common:deps:build` — shows **no entry** in the dashboard's context selector, so the PR is unreachable.

**Root cause (verified against the live DB).** PR contexts are derived in `App.vue` from package project paths, one per `(PR, subproject)` where `subproject` is the path segment after `pr-N`. The derivation explicitly skips `common`:
- `App.vue:114` (board `contexts`): `if (subproject.toLowerCase() === 'common') continue`
- `App.vue:153` (`artifactsContexts`): same line

A PR with packages only under `common` therefore yields zero contexts. The context selector (`ContextBar`) is the only surface that lists PRs — `PRBoard.vue` exists but has no render site. Note the backend store queries (`QueryPRBuildPackages`/`QueryPRBuildEvents`/`QueryPRDistinctRepos`) already union the PR's `common` packages into every subproject context, so for a mixed PR the `common` packages are visible under its `…:ppg` context; only common-*only* PRs fall through the gap.

## Goal

Represent each PR as a **single context keyed by PR number**, covering all of the PR's packages (every subproject + `common`), so every PR — including common-only ones — is selectable.

## Decision (locked during brainstorming)

- **One context per PR** (not per `(PR, subproject)`). Chosen over "show common only when it's the whole PR" and "always show every subproject".
- **Develop directly on `main`** (explicit user consent). The bug is core board functionality, independent of the statistics feature (which is parked on branch `statistics-dashboard-phase1`).
- **Accepted consequence:** a PR context's version dropdown will show only "All versions" (see below).

## Design

### Frontend — `App.vue`

Both PR-context derivations key by PR number instead of `(PR, subproject)`:

- **`contexts`** (board, ~lines 103–134) and **`artifactsContexts`** (~lines 148–179):
  - Remove the `if (subproject.toLowerCase() === 'common') continue` line.
  - Dedupe by PR segment (`pr-N`) rather than `pr-N:subproject`.
  - Emit one `Context` per PR:
    - `label`: `PR #${prNum}` (drop the `· ${subproject}` suffix)
    - `prefix`: `isv:percona:PR:${prSegment}` (e.g. `isv:percona:PR:pr-117`)
    - `apiBase`: `/api/pr/${prSegment}` (no subproject segment)
  - Keep the existing descending-PR-number sort.

`contextToKey`/`keyToContext` in `useUrlState.ts` need **no change**: `contextToKey` already returns the `pr-N` segment for PR contexts, so URL keys (`?ctx=pr-117`, `?actx=…`) stay valid and — as a bonus — stop colliding (previously two subproject contexts of one PR shared the same key).

### Backend

Change the PR context route group and collapse the subproject dimension:

- **`server.go`:** route group `/api/pr/{pr}/{subproject}/{version}` → **`/api/pr/{pr}/{version}`**, still registering `/packages`, `/events`, `/repos`. (`/api/pr/packages`, the groups endpoint, is unchanged.)
- **Handlers** (`prContextPackagesHandler`, `prContextEventsHandler`, `prReposHandler` in `handlers.go`): drop the `subproject` (and `version` for repos) URL params; resolve only `pr`.
- **Store** (`packages.go`, `events.go`): simplify the three queries to a single whole-PR prefix match. With `P = root + ":PR:" + pr`:
  - `QueryPRBuildPackages(db, root, pr)` → `WHERE is_release = 0 AND (project = ? OR project LIKE ? || ':%')` with `P, P`.
  - `QueryPRBuildEvents(db, root, pr, from, to)` → same prefix predicate (keep the time window + `LIMIT 500`).
  - `QueryPRDistinctRepos(db, root, pr)` → same prefix predicate (drop the `subproject`/`version` arguments; the prefix already spans them).
  - This replaces the existing two-branch `subproject` + `common` union — the whole-PR prefix covers `common` and every subproject in one match.

The old per-subproject routes, handler params, and the two-prefix union are removed (nothing calls them once `apiBase` drops the subproject).

### Consequence: version filtering for PR contexts

Version filtering is client-side (`usePackages.matchesVersion`), keyed on the path segment at `prefixDepth = prefix.split(':').length`. The per-PR prefix `isv:percona:PR:pr-117` has depth 4, so the segment at index 4 is the **subproject** (`ppg`, `common`, …), which is non-numeric. Hence `availableVersions` is empty and the version dropdown shows only **"All versions"** — every package of the PR (all subprojects + common) is shown together. This is the intended behavior for the per-PR model.

## Testing / Verification

- **Backend (store tests):** update the existing `QueryPRBuildPackages` / `QueryPRBuildEvents` / `QueryPRDistinctRepos` tests to the new per-PR signatures and prefix. Add a case seeding a **common-only PR** (`isv:percona:PR:pr-200:common:deps:build`, no `…:ppg` packages) and assert its packages are returned by `QueryPRBuildPackages(db, root, "pr-200")`. Assert a mixed PR returns ppg + common packages from the single query. `go test ./internal/...` green.
- **Frontend:** `npm run build` (type-check + bundle) green. Manual: a common-only PR appears as `PR #N` and is selectable, showing its `common` packages; a mixed PR appears as a single `PR #N` showing all its packages across subprojects; the version dropdown reads "All versions".

## Acceptance Criteria

- [ ] `contexts` and `artifactsContexts` emit one context per PR (`label "PR #N"`, `prefix isv:percona:PR:pr-N`, `apiBase /api/pr/pr-N`); the `common` skip is removed.
- [ ] A PR with only `common:*` packages produces a selectable `PR #N` context whose view lists its packages.
- [ ] A mixed PR (`ppg` + `common`, or multiple subprojects) appears as a single `PR #N` context showing all its packages.
- [ ] Backend PR routes are `/api/pr/{pr}/{version}/{packages,events,repos}`; the three store queries match the whole-PR prefix `isv:percona:PR:{pr}`; the per-subproject params/union are removed.
- [ ] Store tests cover a common-only PR and a mixed PR; `go test ./internal/...` and `npm run build` pass.

## Out of scope

- The statistics tab and its PR stats contexts (on branch `statistics-dashboard-phase1`).
- The unused `PRBoard.vue`.
- Any data-model change.
