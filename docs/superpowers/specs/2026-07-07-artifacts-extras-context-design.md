# Artifacts "PPG Extras" Context — Design

**Date:** 2026-07-07
**Status:** Approved (design)

## Problem

PPG version projects gained an `extras` subproject (`isv:percona:ppg:<version>:extras` + `…:extras:containers:ubi9`) carrying extra extensions (timescaledb, pgrouting, …) and a `with-postgis` container image. The Artifacts tab has no way to browse them deliberately: today the extras rows leak *into* the plain PPG context via the subproject `startsWith` filter — but only when repo `UBI_9` happens to be selected (the only repo extras build for), so in practice they are invisible. More subprojects inside `<product>:<version>` are planned, so the fix must generalize.

## Goal

A new "PPG Extras" entry in the Artifacts context selector showing the extras packages and container images for the selected version — and a context model where the *next* subproject costs one new constant.

## Key decisions (locked during brainstorming)

- **Approach A — generalize the frontend `Context` model** (over dedicated backend routes or ad-hoc special-casing). The package corpus (`/api/products/ppg/_/packages`) already contains extras (they classify as `KindDev`), and the metadata endpoint takes full project strings — both untouched.
- **Clean separation**: extras artifacts appear ONLY in "PPG Extras"; the plain PPG entry stops showing them.
- **Allowlist, not denylist** (user: more subprojects are coming): PPG declares `allowedSubprojects: ['containers']` — "the distribution + its container images". Future subprojects are hidden from PPG by default instead of leaking until someone extends an exclusion list.
- **Dev-only scope**: covers `isv:percona:ppg:<v>:extras`. Releases extras are a follow-up when `ppg:releases:<v>:extras` projects exist. PR contexts keep today's catch-all (a PR entry means "everything in that PR", extras included).
- Accepted limitations: the "PPG Extras" entry is always in the selector (empty version bar if no extras exist anywhere); PPG's repos list is not extras-filtered (extras repos ⊆ main repos today — a future extras-only repo would show as an empty pill in PPG).

## Architecture

### 1. Context model — `frontend/src/types/api.ts` + `frontend/src/lib/contexts.ts`

```ts
export interface Context {
  label: string
  apiBase: string
  prefix: string
  /** This context views only prefix:ver:<subproject> (and beneath). */
  subproject?: string
  /** Without subproject: which direct subprojects of prefix:ver belong to
   *  this context (allowlist). Undefined = catch-all (PR/Releases). */
  allowedSubprojects?: string[]
}

export const PPG_CONTEXT = { label: 'PPG', apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg', allowedSubprojects: ['containers'] }
export const PPG_EXTRAS_CONTEXT = { label: 'PPG Extras', apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg', subproject: 'extras' }
```

`App.vue` selector list: `[PPG_CONTEXT, PPG_EXTRAS_CONTEXT, RELEASES_CONTEXT, ...prContexts]`.

### 2. Row matching — `useArtifacts`

The composable gains the context's `subproject`/`allowedSubprojects` (as refs or by passing the whole context). Matching root and filter:

```
root = subproject ? `${prefix}:${ver}:${subproject}` : `${prefix}:${ver}`

matches(pkg) =
  pkg.project === root
  || (pkg.project starts with root+':'
      && (subproject set                       → true            // whole extras subtree, incl. its :containers:*
          || allowedSubprojects undefined      → true            // PR/Releases catch-all (unchanged)
          || first segment after root ∈ allowedSubprojects))     // PPG: only :containers:*
```

Packages sub-tab keeps its `is_container !== true` + target(repo,arch) filters; containers sub-tab keeps `is_container === true` with the same project matching. Registry path, pull command, and `deriveBaseOs` are all derived from `pkg.project` and work unchanged for `…:extras:containers:ubi9`.

### 3. Version pills — `ArtifactsPanel.availableVersions`

When `ctx.subproject` is set, a version counts only if some package's project is `prefix:ver:subproject` or beneath it — so "PPG Extras" offers only versions that actually have extras (18 today) and grows automatically. Other contexts: unchanged.

### 4. Repos — `ArtifactsPanel.fetchRepos` + backend `reposHandler`

Extras context fetches `/api/products/ppg/{version}/repos?subproject=extras`. Backend (`handlers.go reposHandler`): read the optional `subproject` query param, sanitize to `^[a-z0-9_-]+$` (reject otherwise — the value lands in a SQL LIKE prefix), and append `":" + subproject` to the existing `isv:percona:{product}:{version}` prefix. Returns only the repos extras actually target (UBI_9 today). No new route; PPG/Releases/PR repos calls unchanged.

### 5. URL state — `contextToKey`

```ts
// prefix last-segment key, disambiguated by subproject:
return ctx.subproject ? `${last}-${ctx.subproject}` : last   // "ppg-extras"
```

`keyToContext` is find-by-computed-key and needs no change. Deep links (`?actx=ppg-extras&aversion=18`) resolve; old `ppg` links keep resolving to PPG.

### 6. Untouched

`/api/releases/ppg/{version}/artifacts` (release flow), `POST /api/artifacts/metadata` (items carry full project strings), `GET /api/binaries`, backend ingestion (extras already classify as `KindDev` and are polled/cached like any dev project), telemetry.

## Future subprojects (explicit requirement)

Adding e.g. `<product>:<version>:tools` to the UI = one constant plus one entry in `App.vue`'s artifacts selector list:
```ts
{ label: 'PPG Tools', apiBase: '/api/products/ppg', prefix: 'isv:percona:ppg', subproject: 'tools' }
```
Version pills, row matching, repos param, and URL key (`ppg-tools`) follow generically. Until that constant is added, `tools` artifacts appear nowhere in PPG (allowlist) — hidden, not leaked.

## Testing strategy

- **Backend** (`handlers_test.go` or equivalent): repos endpoint with `?subproject=extras` returns only extras repos (seed DB with main UBI_8+UBI_9 and extras UBI_9-only packages); invalid subproject (`%`, empty, uppercase) → 400 or ignored-as-absent (pick 400); without the param, behaviour unchanged.
- **Frontend**: the project has no JS test runner (no vitest/jest in package.json — verified), and adding one is out of scope. The matching logic (`useArtifacts`, `contextToKey`) is therefore verified by `npm run build` (type-checks the new `Context` fields) plus the manual checklist below; the matching table in §2 is the review reference.
- **Manual verification** (docker-compose): PPG Extras entry shows v18 pill only, UBI_9 repo only, the 13 extras packages, and the `with-postgis` container with correct pull command; PPG v18 no longer lists extras packages under UBI_9 nor the extras container.

## Out of scope

- Releases extras (`ppg:releases:<v>:extras`) — follow-up when the projects exist.
- Per-PR extras entries or PR extras exclusion (PR = whole PR).
- Hiding the "PPG Extras" selector entry when no extras exist.
- Extras-aware filtering of the plain PPG repos list.
