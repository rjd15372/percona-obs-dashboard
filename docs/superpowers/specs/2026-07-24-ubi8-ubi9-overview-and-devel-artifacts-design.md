# Distinguish ubi8/ubi9 in the Overview CVE table and devel Artifacts

**Date:** 2026-07-24
**Status:** Approved

## Problem

Two more container surfaces still collapse ubi8 and ubi9 into one entry
under the new `:containers`/`ubiN` OBS layout (the base image is now the
build repo, not a project-name segment):

1. **Overview "CVE exposure by project" table.** `buildOverviewSnapshot`
   aggregates CVE scan rows into images keyed by `(project, package)`,
   taking worst-case Critical/High. ubi8 and ubi9 (same project+package,
   different repo) merge into one row, and the project total is
   max-collapsed rather than summed. The old layout distinguished them
   via a frontend `imageSuffix` (project-suffix), which no longer differs.
2. **devel Artifacts "Container Images" sub-tab.** For a non-release
   context, `ArtifactsPanel` sources images from `useArtifacts.ts`
   (client-side, from the working-set package list), which emits **one
   image per container package** with `baseOs = deriveBaseOs(pkg.project)`
   (project-name only) and a hardcoded `/images/` registry. The base
   image lives in each package's target repos (`pkg.targets[].repo` =
   ubi8/ubi9); the code never looks at it, so a package that builds both
   collapses to one mislabeled row. (The earlier
   `buildReleaseContainerArtifacts` fix corrected only the *release*
   path, which this devel view does not use.)

Both are the same class of bug as the already-fixed release-artifacts and
CVE-scan paths: the base-image **repo** must be a first-class dimension.

## Decision summary

- Both surfaces key on the build **repo**; base OS is derived **repo-first
  with a project-name fallback**, the same rule already used by the
  backend `deriveBaseOS` (api) — old-layout containers (repo `images`,
  base OS in the project) keep working via the fallback.
- Overview rows are labelled with a unified base-OS label (`name ·
  BaseOS`), retiring the `imageSuffix` project-suffix hack; the
  per-row CVE report is filtered to that row's repo.
- devel container images fan out to **one image per distinct target
  repo**, base OS + registry derived from the repo, CVE scans filtered
  to the repo.
- (Combined into one effort at the user's request; the two parts touch
  disjoint files.)

## Design

### Part A — Overview CVE exposure table

**Store — `internal/store/overview.go`.**
`OverviewCveScan` and `OverviewCvePeriod` each gain `Repo string`.
`QueryAllCveScans` selects `repo` (`SELECT project, package, repo, arch,
critical_count, high_count, cve_since`) and scans it; `QueryAllCvePeriods`
likewise adds `repo`.

**API — `internal/api/overview.go`.**
`OverviewImage` gains `Repo string \`json:"repo"\`` and `BaseOS string
\`json:"base_os"\``. In `buildOverviewSnapshot`:
- `imgKey` becomes `{project, pkg, repo}`; the scan loop creates each
  image with `Repo: s.Repo` and `BaseOS: deriveBaseOS(s.Project, s.Repo)`
  (reuses the `api`-package helper added for release artifacts). Max
  Critical/High and oldest-open are tracked per `(project, pkg, repo)`.
- The `fixDays`/periods map keys by `{project, pkg, repo}` too, so
  avg-fix is per base image.
- Side effect (intended): a project's summed image counts become
  accurate for new-layout packages (previously ubi8+ubi9 collapsed to a
  single max-ed row).

**Frontend — `types/overview.ts` + `components/CveExposureTable.vue`.**
- `OverviewImage` gains `repo: string` and `base_os: string`.
- The row label shows `{{ img.name }} · {{ img.base_os }}`; the
  `imageSuffix` function and its usage are removed.
- Report-expansion state key (`imgKey`) becomes
  `img.project + '/' + img.name + '/' + img.repo`, so the two rows expand
  independently; `fetchReport` filters the returned scans to
  `s.repo === img.repo`.

### Part B — devel Artifacts container images

**Frontend — `composables/useArtifacts.ts` (only).**
- `deriveBaseOs` gains a repo parameter: `deriveBaseOs(project, repo)` —
  return the base-OS label for a known base-image repo (ubi8/ubi9/
  noble/bookworm), else fall back to the existing project-name parse
  (which already maps the `:containers:<x>` segment), last resort the
  project string. Mirrors the backend `deriveBaseOS(project, repo)`.
- The `containerImages` computed **fans out per distinct target repo**
  instead of one image per package: for each container package, collect
  the distinct `pkg.targets[].repo` values; emit one `ContainerImage`
  per repo with
  - `id: pkg.project + '/' + pkg.name + '/' + repo`,
  - `baseOs: deriveBaseOs(pkg.project, repo)`,
  - `registry: registry.opensuse.org/<project ':'→'/' lowercased>/<repo>/<name>`
    (repo `images` reproduces the old `.../images/<name>` path),
  - `cveScans` filtered to `s.repo === repo`,
  - `tags`/`pullCmd`/`published`/`rollupState` as today.
  A package whose targets are all one repo (old layout: `images`) still
  yields exactly one image, base OS from the project fallback — no
  behavior change for old-layout devel containers.
- The `ContainersSubTab` grouping (by `baseOs`) is unchanged; distinct
  base-OS values split the fanned-out images into separate groups
  automatically.

## Error handling

None new. A container package with no targets (or targets whose repo is
empty) yields no fan-out entry for that repo / falls back to the project
name — no crash, matching current robustness. Store queries keep their
existing error propagation.

## Testing

- **Backend (Go, fully unit-tested):**
  - `buildOverviewSnapshot`: two scan rows (same project+package, repos
    ubi8/ubi9, different counts) → two `OverviewImage`s with distinct
    `Repo`/`BaseOS` and independent counts; old-layout two-project scans
    → two rows with base OS from the project fallback; period avg-fix
    keyed per repo.
  - `QueryAllCveScans` / `QueryAllCvePeriods` round-trip `Repo` (seeded
    `:memory:` store).
- **Frontend:** no runtime test runner exists (only `.test-d.ts` type
  assertions checked by `vue-tsc`); Part B's logic is verified by
  `npm run build` (vue-tsc typecheck) plus the backend tests and a
  visual check on the deployed build. Optionally add a `.test-d.ts`
  pinning the `ContainerImage`/`OverviewImage` shapes (`repo`/`base_os`
  present). No claim of runtime unit coverage for the fan-out.
- `go test ./... -count=1 && go build ./...`; `cd frontend && npm run build`.
- Live check after deploy: `?tab=artifacts&ctx=devel&aversion=18&sub=containers`
  shows separate UBI 8 / UBI 9 groups; the Overview CVE table shows
  ubi8/ubi9 as separate rows with correct per-repo counts.

## Alternatives considered

- Deriving base OS on the backend for devel too (a new endpoint) —
  rejected; the devel container list is already assembled client-side
  from packages that carry `targets[].repo`, so the fan-out belongs
  there, and no new API surface is needed.
- Keeping `imageSuffix` for old-layout Overview rows and adding base-OS
  only for new — rejected in favor of the unified base-OS label (user
  choice).
- Showing all repos' findings in an expanded Overview row — rejected;
  the report is filtered to the row's repo (user choice).
