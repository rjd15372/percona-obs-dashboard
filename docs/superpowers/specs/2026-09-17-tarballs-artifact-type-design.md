# Tarballs artifact type

**Date:** 2026-09-17
**Status:** Approved

## Problem

The `isv:percona` OBS structure gained a new artifact type. Each
`isv:percona:ppg:<tier>:<version>` project now has a `tarballs`
subproject that builds PostgreSQL distribution tarballs against three
OpenSSL-versioned repos — `ssl1.1`, `ssl3`, `ssl3.5`. These are a third
kind of build artifact alongside packages and container images, and the
dashboard does not present them as such today.

**Confirmed against prod (`/api/products/ppg/staging/_/packages`):**

- Tarball subprojects exist for staging 16/17/18:
  `isv:percona:ppg:staging:<version>:tarballs`.
- The subproject holds a **mix** of builds:
  - `percona-postgresql-tarball` → targets repos `ssl1.1`, `ssl3`,
    `ssl3.5` (×2 arches). These are the tarballs.
  - `percona-psql` → targets `RockyLinux_8/9/9.6`. **Not** a tarball for
    our purposes.
- A non-versioned `isv:percona:ppg:common:deps:tarballs` also exists
  (RockyLinux-built dependency tarballs) — out of scope.
- Release tarball subprojects exist too (e.g.
  `isv:percona:ppg:releases:17:tarballs`), but the Releases **artifacts**
  endpoint (`/api/releases/ppg/<v>/artifacts`) currently returns only
  `packages` + `container_images` — no tarballs.

Package objects already carry `is_container` (detected from
Dockerfile/`.kiwi` source files). There is **no** `is_tarball` flag, and
none is needed: tarball detection is purely structural.

## Decision summary

- **A tarball = a build target in a `…:tarballs` subproject whose repo
  starts with `ssl`** (`ssl1.1`, `ssl3`, `ssl3.5`). (User choice: "only
  ssl-repo builds". Rejected: whole-`:tarballs`-subproject — would pull
  in RockyLinux-built `percona-psql`; package-name match on
  `percona-postgresql-tarball` — too narrow/brittle.)
- **RockyLinux-built packages in `:tarballs` stay in the Packages view.**
- **Card content is minimal**: a "Tarball" label + the ssl repo name,
  plus name, version, arch, build state, built time. (User choice.
  Rejected: a download URL/command — needs backend artifact-path work;
  not requested.)
- **Contexts: Staging, Devel, PR, and Releases.** (User choice "also add
  Releases".) Staging/Devel/PR are frontend-only; Releases needs Go.
- **No new DB column, worker task, or CVE coupling.** Tarball packages
  are `is_container: false`, so they settle normally and are not
  scanned. The data is already served by the existing `_/packages` API;
  the live-context feature is client-side derivation, mirroring the
  container fan-out.

## Non-goals

- Download links / pull commands for tarball files.
- CVE scanning of tarballs and any Overview CVE-table presence.
- An `is_tarball` DB column, OBS detection method, or worker task.
- The non-versioned `common:deps:tarballs` subproject.

## Design

The container feature is the template throughout. Containers are a
`Package` with `is_container: true`, fanned out **client-side** per build
repo into `ContainerImage` cards in a dedicated sub-tab; the Releases
context has a parallel server-built array. Tarballs mirror this, except
detection is structural (subproject + ssl repo) rather than a stored
flag, and there is no metadata/CVE enrichment.

### 1. Shared detection helpers — `frontend/src/lib/`

One source of truth, reused by the derivation (component 2) and the
Packages repo exclusion (component 3):

```ts
export function isTarballRepo(repo: string): boolean {
  return /^ssl/i.test(repo)
}

export function isTarballTarget(project: string, repo: string): boolean {
  return project.endsWith(':tarballs') && isTarballRepo(repo)
}
```

Placed in a small dedicated module (e.g. `lib/tarballs.ts`) with a
`.test-d.ts` pinning the signatures. Ordering of ssl repos for display
is natural/numeric: `ssl1.1` < `ssl3` < `ssl3.5`.

### 2. Live derivation — `frontend/src/composables/useArtifacts.ts`

Add a `Tarball` interface and a `tarballs` computed mirroring
`containerImages`:

```ts
export interface Tarball {
  id: string          // `${project}/${name}/${repo}`
  project: string
  name: string
  version: string     // pkg.version (e.g. "18.6-3")
  repo: string        // ssl1.1 | ssl3 | ssl3.5
  arches: string[]    // distinct arches for this (pkg, repo)
  rollupState: string
  published: boolean
  builtAt: string      // latest target started_at for this repo ("" if none)
}
```

- **Filter**: packages where `matchesProject(pkg.project, ver)` (the
  existing shared `matchesVersionKey` call) **and**
  `pkg.project.endsWith(':tarballs')`.
- **Fan-out**: over distinct target repos passing `isTarballRepo`; one
  `Tarball` per (package, ssl-repo). `arches` = distinct arches among
  that repo's targets; `published` = any such target published;
  `builtAt` = max `started_at` among that repo's targets; `rollupState`
  = `pkg.rollup_state ?? ''`.
- **built time and state come from the package targets directly** — no
  call to `/api/artifacts/metadata`, no `useArtifactMetadata`
  involvement, no CVE scans.
- Return `tarballs` alongside `packageRows` and `containerImages`.

`packageRows` is unchanged here; ssl rows are kept out of Packages by
component 3 (repo exclusion), and `percona-postgresql-tarball` has no
non-ssl repos so it disappears from Packages entirely, while
`percona-psql` keeps its RockyLinux repos.

### 3. Packages repo exclusion — `frontend/src/components/ArtifactsPanel.vue`

In the live-context `fetchRepos` path, drop repos matching
`isTarballRepo` from the `RepoInfo[]` used by the Packages sub-tab, so
the repo selector never offers `ssl1.1`/`ssl3`/`ssl3.5`. This keeps
tarball builds out of Packages without touching the backend
`QueryDistinctRepos` (ssl repos carry no server-side flag to filter on;
the exclusion is a display concern local to the Packages sub-tab).
Release-path repo lists (`reposFromReleaseArtifacts`) are unaffected —
release tarballs come from their own array (component 7).

### 4. `TarballsSubTab.vue` — new card component

Cloned from `ContainersSubTab.vue`:

- Props: `tarballs: Tarball[]`, `loading?: boolean`. (No `copiedKey`/
  `copy` — there is no copy affordance.)
- Group by `repo` (ssl repo), sorted `ssl1.1` → `ssl3` → `ssl3.5`;
  group header shows the repo name and an "N tarball(s)" count.
- Each card shows: a tarball icon (distinct from the container box
  glyph), a **"Tarball · `<repo>`"** label, the package `name`, the
  `version`, arch chips (`arches`), a build-state badge (reusing the
  container `STATE_LABELS`/rebuild-badge style from `rollupState`), and
  the built time (`formatArtifactTime(builtAt)`).
- Drops the REGISTRY, AVAILABLE TAGS, DOCKER PULL, and SECURITY/CVE
  sections entirely.
- Empty state: "No tarballs for this version."
- Add `TarballsSubTab.props.test-d.ts` mirroring the container one.

### 5. Third-tab plumbing

Widen the artifacts sub-tab union `'packages' | 'containers'` to
`'packages' | 'containers' | 'tarballs'` in every declaration:

- `frontend/src/App.vue` — the `artifactsTab` ref.
- `frontend/src/composables/useUrlState.ts` — the `UrlStateOptions`
  type, the `?sub=` hydration guard (accept `'tarballs'`), and the
  write-back (already emits any non-`'packages'` value).
- `frontend/src/components/ArtifactsPanel.vue` — `defineProps` and the
  `update:artifactsTab` emit; convert the current
  `<ContainersSubTab v-else>` into
  `v-else-if="props.artifactsTab === 'containers'"` and add
  `<TarballsSubTab v-else :tarballs="tarballs" :loading="isLoading" />`.
- `frontend/src/components/ArtifactsVersionBar.vue` — the `activeTab`
  prop and `update:tab` emit types, plus a third "Tarballs" `<button>`
  in the segmented control emitting `'tarballs'`.

`ArtifactsPanel` wires `tarballs` from `useArtifacts` for live contexts
and from the release array for the Releases context (component 8).

### 6. Fold the subproject — `frontend/src/lib/contexts.ts`

Add `'tarballs'` to `allowedSubprojects` in the three PPG/PR context
builders: `PPG_DEVEL_CONTEXT`, `PPG_STAGING_CONTEXT`, and each context
produced by `prArtifactsContexts` (`['containers', 'tarballs']`). This
folds `:tarballs` into the plain numeric version entry via the unchanged
shared `deriveVersionKeys`/`matchesVersionKey`, removing the current
stray `"<version>:tarballs"` entry from the version selector.
`RELEASES_CONTEXT` is left as the catch-all (undefined
`allowedSubprojects`); release tarballs flow through the artifacts
endpoint, not version-key folding. `lib/versions.ts` needs no change.

### 7. Releases backend — `backend/internal/api/release_artifacts.go`

Mirror the container release path:

- New response type:

  ```go
  type ReleaseTarballArtifact struct {
      Project string `json:"project"`
      Name    string `json:"name"`
      Version string `json:"version"`
      Repo    string `json:"repo"`
      Arch    string `json:"arch"`
      BuiltAt string `json:"built_at"`
  }
  ```

- Add `Tarballs []ReleaseTarballArtifact `json:"tarballs"`` to
  `ReleaseArtifactsResponse`.
- In `buildReleaseArtifacts`, add a `SearchProjects(ctx, project+
  ":tarballs")` branch (parallel to the existing `":containers"`
  search), list each subproject's binaries via `ProjectBinaryList`, and
  build tarball artifacts from binaries whose **repo path segment starts
  with `ssl`**. One artifact per (name, repo, arch).
- **Implementation-time verification (backend has authenticated OBS
  access):** confirm the exact binary shape for a released tarball —
  filename pattern and how the ssl repo appears in the
  `ProjectBinaryList` path — using
  `isv:percona:ppg:releases:17:tarballs`. Filter by the ssl repo
  segment; a filename-suffix filter is a fallback only if repo filtering
  proves insufficient. `Version` from the binary/package version;
  `BuiltAt` from the binary mtime, matching how release container
  `MTime`/built time is sourced.
- Go table test(s) in `release_artifacts_test.go` covering: ssl-repo
  binaries under `:tarballs` produce tarball artifacts; RockyLinux
  binaries under `:tarballs` do not; one artifact per (name, repo, arch).
- No CVE attach for tarballs (the `attachReleaseCveScans` path is
  container-only and unchanged).

### 8. Releases frontend wiring — `frontend/src/components/ArtifactsPanel.vue`

- Extend the inline `ReleaseArtifactsResponse` interface with a
  `tarballs: ReleaseTarballArtifact[]` field and declare
  `ReleaseTarballArtifact` (mirroring the existing
  `ReleaseContainerArtifact` inline interface).
- Add a `tarballs` computed that branches on `isReleaseContext`, exactly
  like `containerImages` does: release path maps
  `releaseArtifacts.value.tarballs` → `Tarball[]`; live path returns the
  `useArtifacts` `tarballs`.
- **Grouping rule (pins the shape both sides agree on):** the backend
  emits one `ReleaseTarballArtifact` per **(name, repo, arch)** row (the
  natural binary granularity). The frontend release-path computed groups
  those rows by **(project, name, repo)** into one `Tarball` with
  `arches` = the collected arches, `id` = `${project}/${name}/${repo}`,
  `published: true`, `rollupState: 'succeeded'`, and `builtAt` = the max
  `built_at` in the group — matching the per-(package, ssl-repo) card
  granularity of the live path so `TarballsSubTab` renders both
  identically.

## Error handling / caveats

- A version with no ssl-repo tarball targets yields an empty Tarballs
  sub-tab (empty state), never an error.
- `builtAt` empty → the card omits the built-time line (as container
  cards do when metadata is absent).
- The ssl-repo prefix rule (`/^ssl/i`) is deliberately loose so a future
  `ssl3.6`/`ssl4` repo is picked up automatically; if a non-tarball ssl
  repo ever appears outside a `:tarballs` subproject, `isTarballTarget`'s
  subproject guard prevents misclassification in the derivation (the
  Packages repo exclusion uses `isTarballRepo` alone, which is safe
  because ssl repos originate only from tarball builds).
- Board tab is untouched — this is an Artifacts-tab feature only.

## Testing

- **Frontend:** no runtime test runner; `.test-d.ts` type pins for the
  helpers and `TarballsSubTab` props; `cd frontend && npm run build`
  (vue-tsc) → exit 0. Manual/live check on prod: Staging 16/17/18 show a
  Tarballs sub-tab with `ssl1.1`/`ssl3`/`ssl3.5` groups each holding
  `percona-postgresql-tarball`; `percona-psql` still appears under
  Packages (RockyLinux repos); no stray `"<version>:tarballs"` version
  entry.
- **Backend:** `cd backend && go test ./internal/api/ -run
  ReleaseArtifacts -count=1` for the tarball builder; `go build ./...`.
- **Releases (manual):** after deploy, a released version with a
  `:tarballs` subproject shows tarball cards in the Releases context.

## Alternatives considered

- **Backend `is_tarball` flag + worker detection** (full container
  mirror) — unnecessary; detection is structural and the data is already
  served. Rejected for scope.
- **Whole-`:tarballs`-subproject = tarball** — would misclassify
  RockyLinux-built `percona-psql`. Rejected per user.
- **Backend `QueryDistinctRepos` exclusion of ssl repos** — cleaner
  parity with container repo exclusion, but ssl repos carry no
  server-side flag and the concern is display-local; the frontend
  `fetchRepos` filter is simpler and lower-risk. Deferred.
- **Ship Staging/Devel/PR first, Releases as a fast follow** — offered;
  user chose to include Releases now.
