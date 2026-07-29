# Artifacts tab: per-tier PR contexts

**Date:** 2026-07-29
**Status:** Approved

## Problem

The Artifacts tab shows 0 packages and no containers for PR projects.
Root cause (confirmed against prod `/api/pr/packages`): PR projects now
follow the devel/staging-restructured layout
`isv:percona:PR:<pr>:ppg:<tier>:<version>[:<sub>]` — e.g. PR #4 is
`isv:percona:PR:pr-4:ppg:staging:18` (+ `:containers`), and PR #2 spans
two tiers, `…:ppg:staging:17` and `…:ppg:devel:19`.

`App.vue`'s `artifactsContexts` builds one context per PR with
`prefix: isv:percona:PR:pr-N` (4 segments) and no `allowedSubprojects`.
The shared `deriveVersionKeys` reads the version at
`parts[prefix.depth]` = `parts[4]` = `ppg` (not numeric) → returns no
version keys → the version selector is empty → `matchesProject` filters
everything out → 0 packages/containers. The devel/staging main contexts
avoid this because their prefix already includes `ppg:<tier>`, so the
numeric version sits exactly at the prefix depth.

## Decision summary

- **One Artifacts context per (PR, tier)** (user choice) — expand each
  PR into `PR #<n> · Staging` / `PR #<n> · Devel` entries, each mirroring
  a devel/staging context. (Rejected: one PR context with a tier-aware
  version key — invasive to the shared version functions used by board +
  events + artifacts; one PR context staging-only — silently hides
  devel-tier PR projects.)
- Each per-tier context uses `prefix: isv:percona:PR:<pr>:ppg:<tier>`
  and `allowedSubprojects: ['containers']`, so the **unchanged** shared
  `deriveVersionKeys`/`matchesVersionKey` resolve versions correctly.
- The PR-context construction is extracted from the `App.vue` computed
  into a pure, testable helper.

## Design

### 1. `prArtifactsContexts` helper — `frontend/src/lib/contexts.ts` (new export)

A pure function replacing the inline PR-context loop in `App.vue`:

```ts
export function prArtifactsContexts(groups: PRGroup[]): Context[]
```

For every `pkg.project` across `groups[].packages`, match
`isv:percona:PR:<prSeg>:ppg:<tier>:<version>` where `tier` ∈
`{'devel','staging'}` and `<version>` is numeric. For each distinct
`(prSeg, tier)` pair emit one `Context`:

- `label`: `PR #<n> · Staging` / `PR #<n> · Devel` (n = prSeg without the
  `pr-` prefix; tier title-cased)
- `apiBase`: `/api/pr/<prSeg>` (the PR routes already return every
  subproject of the PR; the prefix does the tier filtering client-side)
- `prefix`: `isv:percona:PR:<prSeg>:ppg:<tier>`
- `allowedSubprojects: ['containers']`

Projects that don't match (e.g. `…:ppg:common:deps`,
`…:common:deps:build`) contribute no context — nothing versioned to
show, consistent with the main tabs having no common-deps artifacts
context. Sort: PR number descending, then tier (staging before devel,
matching the main context order).

### 2. `App.vue` wiring

`artifactsContexts` becomes:

```ts
const artifactsContexts = computed<Context[]>(() => [
  PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT,
  ...prArtifactsContexts(prGroups.value),
])
```

The inline PR-parsing loop and its local sort are deleted (moved into
the helper).

### 3. Nothing else changes

- Shared `deriveVersionKeys`/`matchesVersionKey` (`lib/versions.ts`) —
  unchanged; the corrected prefix depth + `allowedSubprojects` is all
  they need.
- `useArtifacts` (`matchesProject`, container fan-out) — unchanged; it
  reads `ctx.prefix`/`ctx.allowedSubprojects` and now filters correctly,
  and the just-merged per-repo container fan-out then splits ubi8/ubi9.
- Backend — unchanged; `/api/pr/<pr>/_/packages`,
  `/api/pr/<pr>/<version>/repos` already cover all subprojects.

## Error handling / caveats

- A PR with no versioned `ppg:<tier>` project produces no Artifacts
  context (won't appear in the selector) — acceptable; it has no
  versioned artifacts.
- `fetchRepos` requests `/api/pr/<pr>/<version>/repos` with the numeric
  version only (tier lives in the prefix, not the version key). Real
  PRs carry distinct version numbers per tier (17 vs 19; PR #4 is
  staging-only), so this is unambiguous today. A PR with the *same*
  numeric version in both tiers could cross-fetch repos — verify during
  planning whether `prReposHandler` needs tier scoping; it does not
  block the packages/containers fix.

## Testing

- Frontend has no runtime test runner (only `.test-d.ts` type checks via
  `vue-tsc`). `prArtifactsContexts` is a pure function; add a
  `.test-d.ts` pinning its `Context[]` return shape. Logic is verified
  by `npm run build` + manual check on the deployed build.
- Manual/live: PR #4 → a `PR #4 · Staging` context whose packages tab
  lists its packages and whose containers tab shows ubi8/ubi9 groups;
  PR #2 → both `PR #2 · Staging` (17) and `PR #2 · Devel` (19) contexts,
  each scoped to its tier.
- `cd frontend && npm run build` → exit 0.

## Alternatives considered

- One PR context with a tier-aware version key (`staging:18`) — bends
  the shared version-key model (tier isn't numeric) used by three
  callers; rejected.
- One PR context, staging-only — hides devel-tier PR projects; rejected.
- Deepening the single PR prefix to one tier — same hiding problem for
  multi-tier PRs; rejected.
