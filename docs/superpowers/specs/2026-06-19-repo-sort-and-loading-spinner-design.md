# Repo Distro Sorting and Packages Loading Spinner — Design

**Goal:** Two independent UX improvements to the Artifacts tab Packages view: (1) replace the RPM/DEB sidebar section headers with distro-brand sections sorted by family and version, and (2) show a centred spinner in the package list while the packages and metadata fetches are in flight.

**Architecture:** Pure frontend changes — no backend or API changes. Distro detection lives as a utility function alongside the `RepoInfo` type. Loading state is threaded from the two existing fetch sites down to `PackagesSubTab` as a single `loading` prop.

**Tech stack:** Vue 3 + TypeScript.

**User decisions (already made):**
- Sidebar uses distro-brand sections (RHEL, openSUSE, Ubuntu, Debian) instead of RPM/DEB headers.
- RHEL family includes RHEL, Rocky Linux, Oracle Linux, CentOS, UBI.
- Loading style: centred spinner with label.
- Spinner covers both the packages fetch and the metadata fetch — disappears only when both resolve.
- Containers sub-tab: no spinner (fast enough, metadata not blocking).

---

## Feature 1: Repo Distro Sorting

### `frontend/src/composables/useArtifacts.ts`

Export a pure utility function `distroGroup(repo: RepoInfo): string` that detects the distro family from `repo.name` (case-insensitive substring match):

| Pattern matches (any) | Group label |
|---|---|
| `rhel`, `centos`, `rocky`, `oracle`, `ubi` | `RHEL` |
| `opensuse`, `suse` | `openSUSE` |
| `ubuntu` | `Ubuntu` |
| `debian` | `Debian` |
| *(no match)* | `Other` |

The function is pure and easily unit-tested.

### `frontend/src/components/PackagesSubTab.vue`

Replace the `rpmRepos` / `debRepos` computed properties with four group computeds:

```
rhelRepos     = repos filtered by distroGroup === 'RHEL',   sorted by repo.name natural order
opensuseRepos = repos filtered by distroGroup === 'openSUSE', sorted by repo.name natural order
ubuntuRepos   = repos filtered by distroGroup === 'Ubuntu',  sorted by repo.name natural order
debianRepos   = repos filtered by distroGroup === 'Debian',  sorted by repo.name natural order
otherRepos    = repos not matched by any of the above,       sorted by repo.name natural order
```

The sidebar template renders the four sections in fixed order: RHEL → openSUSE → Ubuntu → Debian → Other (Other section only rendered if `otherRepos.length > 0`). Each section uses the same clickable-button pattern as the current RPM/DEB groups.

Within each group, repos are sorted by `repo.name` using `localeCompare` with `{ numeric: true }` so version numbers sort correctly (e.g., "RHEL 8" before "RHEL 9", "Ubuntu 20.04" before "Ubuntu 22.04").

The RPM/DEB filter chips at the top of the panel and the repo-auto-selection logic in `ArtifactsPanel` are unchanged.

---

## Feature 2: Packages Loading Spinner

### `frontend/src/composables/useArtifactMetadata.ts`

Add `isLoading: Ref<boolean>` to the return value. Set to `true` at the start of `fetchMetadata()` (after the abort/new-controller setup), `false` in a `finally` block so it always clears on success, error, or abort.

### `frontend/src/components/ArtifactsPanel.vue`

`artifactsLoading` already exists as an unused `ref<boolean>` (set to `true` before `fetchPackages()`, `false` after). Wire it up:

```typescript
const { enrichedPackageRows, enrichedContainerImages, isLoading: metadataLoading } =
  useArtifactMetadata(livePackageRows, liveContainerImages, computed(() => !isReleaseContext.value))

const isLoading = computed(() => artifactsLoading.value || metadataLoading.value)
```

Pass `isLoading` to `PackagesSubTab`:

```html
<PackagesSubTab :loading="isLoading" ... />
```

### `frontend/src/components/PackagesSubTab.vue`

Add `loading: boolean` prop (default `false`). In the template, wrap the package list area:

```html
<div v-if="loading" class="packages-loading">
  <div class="spinner"></div>
  <span class="loading-label">Fetching packages…</span>
</div>
<div v-else>
  <!-- existing package rows -->
</div>
```

The spinner replaces only the package list area — the repo sidebar remains visible throughout (repos and packages arrive together, so the sidebar populates at the same time the spinner would disappear anyway; showing the sidebar during load gives the user something to see).

CSS for the spinner and loading state is scoped to `PackagesSubTab`. The `spinner` animation (`border-top` rotation) is consistent with the style shown in the brainstorming mockup.

---

## Scope

- No backend changes.
- No changes to the Containers sub-tab.
- No changes to the release-context artifact view (release artifacts are pre-computed and fast).
- The RPM/DEB filter chips remain — they filter the package list, not the sidebar grouping.
