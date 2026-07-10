# Three-Tier OBS Structure Adaptation — Design

**Date:** 2026-07-10
**Status:** Approved (design)
**Upstream spec:** `percona-obs-packaging/docs/superpowers/specs/2026-07-08-three-tier-obs-structure-design.md` (PG-2518)

## Problem

The OBS project layout migrated from two tiers to three: `ppg:<V>` became `ppg:staging:<V>`, a new `ppg:devel:<V>` tier builds from development branches, and `ppg:releases:<V>` is unchanged. The migration is **complete** — old `ppg:<V>` projects are deleted from OBS. The dashboard still assumes the old shape in its URL/prefix layer, its hardcoded frontend contexts, and the Overview's project grouping (`ppg:staging:18` currently collapses into a single bogus `ppg:staging` row for all versions).

## Decisions (locked during brainstorming)

- **Approach B — explicit tier route:** `/api/products/{product}/{tier}/{version}/…` with `tier ∈ {devel, staging}` validated (else 400). Rejected: tier-qualified product segment (`product=ppg:staging` through the old route — less explicit); generic prefix-driven API (YAGNI).
- **Migration state:** fully migrated; the dashboard targets only the new shape. The old `PPG` selector entry disappears; old-shape DB package rows are garbage-collected by the existing poller GC.
- **Common packages:** `ppg:common` + `common` appear in **both** devel and staging board views (preserves today's behavior in each).
- **Overview categories:** four sections — **Devel · Staging · Releases · PRs** (pipeline order). Common projects stay in the Devel section.
- **Subprojects are version extensions, not contexts** (user decision, revised design): `extras`, `tde`, and any future subproject of `ppg:<tier>:<V>` appear as entries in the version selector (`18, 18:extras, 17, 17:extras, 16, 16:extras, 16:tde, …`) instead of separate context entries. The `PPG Extras` context is removed. `containers` subprojects remain absorbed into the plain version entry.
- **Selector order/default:** contexts in pipeline order (Devel · Staging, artifacts adds Releases; PRs appended); **default context is Staging** (the renamed continuation of today's default `PPG`).
- **Version-key ordering:** numeric descending (newest first); each plain version followed by its extensions alphabetically.
- **Plain version excludes extras on the board:** today the board leaks extras packages into the plain version view (only Artifacts had the allowlist); with version extensions, plain `17` = version root + absorbed subprojects only. Deliberate behavior fix.

## Architecture

### 1. Backend — routes and handlers

- `server.go`: the products route becomes `r.Route("/api/products/{product}/{tier}/{version}", …)` with the same three endpoints (`/packages`, `/events`, `/repos`). The old two-segment route is removed (frontend and backend deploy together).
- Shared tier validation in each products handler: `tier := chi.URLParam(r, "tier")`; not `devel`/`staging` → 400.
- `packagesHandler` → `store.QueryBuildPackages(db, root, product, tier, version)`. New signature; prefixes: `vp = root:product:tier:version`, `cp = root:product:common` (expression unchanged — correct again since product stays `ppg`), `gp = root:common`. The version-less (`_`) branch uses `pp = root:product:tier` and keeps its current common handling (only `gp`).
- `eventsHandler` prefix → `root:product:tier`.
- `reposHandler` prefix → `root:product:tier:version[:subproject]`; the existing `?subproject=` param and its validation are **kept** — it becomes the mechanism for version-extension repo queries.
- PR routes (`/api/pr/…`), releases routes (`/api/releases/ppg/…`), and all other endpoints are unchanged.

### 2. Backend — overview `logicalProject`

New tier branches ahead of the current default, plus a generic subproject rule:

- `rel = [ppg, devel|staging, V, …]` → row `root:ppg:<tier>:<V>`; a direct subproject at `rel[3]` **other than `containers`** gets its own row `root:ppg:<tier>:<V>:<sub>` (absorbing its subtree); `containers:*` stays absorbed into the version row. This keeps Overview row granularity identical to the version selector's and needs no change when new subprojects (e.g. `tde`) appear.
- Legacy shape `ppg:<V>[:<sub>]` maps to `root:ppg:staging:<V>[:<sub>]` — staging is the renamed continuation of `ppg:<V>` per the upstream spec, so pre-migration rows still inside the stats windows (`target_state_durations`, events) merge into the staging rows instead of rendering ghost sections until they age out.
- `ppg:common`, `common`, `releases`, and PR branches unchanged.

### 3. Frontend — contexts

`frontend/src/lib/contexts.ts`:

```ts
PPG_DEVEL_CONTEXT:   label "PPG Devel",   apiBase "/api/products/ppg/devel",   prefix "isv:percona:ppg:devel",   allowedSubprojects ["containers"]
PPG_STAGING_CONTEXT: label "PPG Staging", apiBase "/api/products/ppg/staging", prefix "isv:percona:ppg:staging", allowedSubprojects ["containers"]
```

`PPG_EXTRAS_CONTEXT` is deleted. `RELEASES_CONTEXT` unchanged. Board contexts: `[PPG_DEVEL, PPG_STAGING, …prContexts]`; artifacts contexts: `[PPG_DEVEL, PPG_STAGING, RELEASES, …prContexts]`. Default `selectedContext`/`artifactsContext`: `PPG_STAGING_CONTEXT`.

The `Context.subproject` field is removed from the type and from `contextToKey` (URL keys become `devel`, `staging`, `releases`, `pr-N`; stale bookmark keys fall back to the default context — existing behavior). `allowedSubprojects` stays and now means "subprojects absorbed into the plain version entry"; contexts without it (PR, Releases) keep the historical catch-all.

### 4. Frontend — version-extension model

- New shared helper `frontend/src/lib/versions.ts` (replaces the duplicated derivation in `usePackages.availableVersions` and `ArtifactsPanel.availableVersions`): scan package projects at `depth = ctx.prefix.split(':').length`; a numeric segment `V` yields the plain key `V` when the package sits at the version root or in an absorbed subproject, and the key `V:<sub>` for any other direct subproject. Sort: numeric descending, plain key first, extensions after it alphabetically.
- Version matching (board `matchesVersion`, artifacts `matchesProject`, events filter): selected key `V` → matches version root + absorbed subprojects only; selected key `V:<sub>` → matches the `prefix:V:<sub>` subtree (catch-all beneath, containers included — same semantics as the old extras context). Non-version segments (common packages) remain always-shown on the board, as today.
- Repos fetch: when the selected version key has an extension, the artifacts panel requests `/repos?subproject=<sub>` with the plain version in the path (existing backend param). Packages/events fetching is unchanged (fetch-all + client-side filtering).
- The URL `v`/artifacts-version params carry the full key (e.g. `v=17:extras`).
- PR contexts: unchanged (no numeric segment at their depth → no version pills, catch-all matching, as today).

### 5. Frontend — overview categories

`ProjectCategory` gains `'Staging'`; `CATEGORY_ORDER = ['Devel', 'Staging', 'Releases', 'PRs']`; `categoryOf`: `:PR:` → PRs, `:releases` suffix → Releases, `':staging:'` substring → Staging, else Devel (devel rows + common projects).

### 6. Unchanged (verified during brainstorming)

- Classifier: `ppg:staging:18` / `ppg:devel:18` already classify as `KindDev` with tag `ppg`; poller discovery, MQ consumer, working set, parking, and telemetry all operate prefix-relative.
- Releases path (`BinariesCheckTask`, release artifacts), PR mechanics (marker-based parsing), container registry paths and CVE image bases (derived from the full project string, which now correctly includes the tier).
- Old-shape cleanup: poller GC deletes packages of OBS-deleted projects; the CVE orphan sweep handles their scan rows.

## Error handling

- Invalid `{tier}` → 400 `"tier must be devel or staging"`; malformed `?subproject=` keeps its existing 400.
- Stale URLs (`bctx=ppg`, `actx=ppg-extras`, `v=17` under a context where it no longer exists) resolve to defaults via the existing fallback paths; a version key that disappears (e.g. subproject deleted) falls back like any vanished version today (the existing availableVersions watcher).

## Testing

- **Backend:** route tests — `/api/products/ppg/staging/17/packages` returns seeded `ppg:staging:17` + `ppg:common` + `common` packages; `/api/products/ppg/bogus/17/packages` → 400; events/repos analogous incl. `?subproject=extras`. `QueryBuildPackages` tier test. `logicalProject` table test: staging/devel version rows, containers absorption, generic subproject rows (`extras`, `tde`), legacy `ppg:17[:extras]` → staging mapping, PR/common/releases unchanged.
- **Frontend:** `npm run build`; unit-less (no test runner, established). Manual post-deploy: both selectors, version extensions appear and scope board/artifacts/events correctly, extras repos populate, Overview shows four sections with per-version tier rows.

## Out of scope

- Dashboard support for old-shape projects beyond the legacy `logicalProject` mapping (OBS migration is complete).
- Devel-specific UI treatment beyond the selector entry (e.g. branch names, nightly indicators — future work in the upstream spec too).
- Route aliases for the removed `/api/products/{product}/{version}` shape.
- Statistics dashboard plans (separate effort).
