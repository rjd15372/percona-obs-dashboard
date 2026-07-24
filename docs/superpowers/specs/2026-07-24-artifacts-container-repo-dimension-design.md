# Artifacts tab: distinguish ubi8/ubi9 container images

**Date:** 2026-07-24
**Status:** Approved

## Problem

The Artifacts tab's container-images sub-tab groups images by base OS.
Under the new OBS layout, containers live in one project
(`isv:percona:ppg:staging:17:containers`) and build against base-image
repos `ubi8`/`ubi9` — the base image is no longer in the project name.
`buildReleaseContainerArtifacts` (`internal/api/release_artifacts.go`):

1. derives base OS with `deriveBaseOS(project)`, which parses
   `containers:<baseos>` out of the *project name*. New projects end at
   `…:containers` with no suffix, so it hits its `return project`
   fallback and every image gets the same base-OS string; and
2. keys artifacts by `project + package` (ignoring `binary.Repo`), so
   the ubi8 and ubi9 `.containerinfo` binaries merge into one artifact;
   and
3. builds the registry pull path with a hardcoded `/images/` segment.

Result: on release versions cut on the new layout, ubi8 and ubi9
collapse into one indistinguishable group with a wrong pull path.
Older releases on the old layout (repo `images`, base OS in the project
name) still render correctly and must keep working.

## Decision summary

- **Approach A: make the base-image repo the distinguishing dimension**
  of a container artifact — mirrors the just-merged CVE repo-dimension
  fix. (Rejected: fixing only the base-OS *label* while still keying by
  project+package — labels the collision, doesn't remove it.)
- Base OS is derived **repo-first, project-fallback** so old and new
  layouts coexist.
- Registry path becomes `registry.opensuse.org/<project-path>/<repo>/<name>`
  — self-adjusting (`images` → old path, `ubiN` → new path).
- Each base image renders as its own card under its own base-OS heading
  (the existing group-by-`base_os` UI needs no change).

## Design

### 1. `buildReleaseContainerArtifacts` — `internal/api/release_artifacts.go`

- **Key by `(project, package, repo)`.** Change the map key from
  `binary.Project + "\x00" + binary.Package` to include
  `"\x00" + binary.Repo`. The ubi8 and ubi9 binaries then build two
  artifacts. Tag accumulation (`seenTags`) keys off the same composite,
  so tags no longer cross-contaminate between repos.
- **Registry path repo-derived:**
  `"registry.opensuse.org/" + strings.ToLower(strings.ReplaceAll(binary.Project, ":", "/")) + "/" + binary.Repo + "/" + binary.Package`.
  Old layout (repo `images`, project already contains the base OS) →
  `…/containers/ubi9/images/<name>` exactly as before; new layout (repo
  `ubi9`, project ends at `containers`) → `…/containers/ubi9/<name>`.
  A small local helper (e.g. `containerRegistryPath(project, repo, name)`)
  holds this; not shared with `cve.ImageBase` to avoid an api→cve import
  for a one-liner (the two are the same formula, documented as such).
- **`ReleaseContainerArtifact` gains `Repo string \`json:"repo"\`**,
  set from `binary.Repo`. Needed for the CVE-attach filter below and
  available to the UI.

### 2. Base-OS derivation

New helper:

```go
// baseOSFromRepo maps a base-image build repo (ubi8/ubi9/noble/…) to a
// display label. Returns "" for the legacy "images" repo or an unknown
// repo, so the caller falls back to parsing the project name.
func baseOSFromRepo(repo string) string
```

mapping `ubi8`→"UBI 8", `ubi9`→"UBI 9", `noble`→"Ubuntu 24.04 Noble",
`bookworm`→"Debian 12 Bookworm" (same labels the current project-name
switch produces). `deriveBaseOS` becomes `deriveBaseOS(project, repo)`:
return `baseOSFromRepo(repo)` when non-empty, else the existing
project-name parse (unchanged, including its `return project` last
resort). Old-layout releases (repo `images`) take the project-name path
and are unaffected; new-layout releases get the label from the repo.

### 3. CVE attach — `attachReleaseCveScans`

`QueryCveScans(db, project, package)` returns scans for every repo of
that package. Now that each artifact is per-repo and `model.CveScan`
carries `Repo` (merged earlier), filter so each artifact keeps only
`scan.Repo == artifact.Repo`. Without this, both base-image cards would
show the union of ubi8+ubi9 CVEs. (Old-layout artifacts have
`Repo == "images"`, matching their `images`-keyed scan rows.)

### 4. Frontend — no change

`ContainersSubTab` groups on `base_os` and renders `registry` +
`CveFindingsTable` per image. Correct distinct `base_os` values split
ubi8/ubi9 into separate groups automatically; the per-image
`CveFindingsTable` already labels blocks `repo · arch`. The new backend
`repo` field is additive (ignored by the existing type unless surfaced
later).

## Error handling

None new. `binary.Repo` is always populated by `ProjectBinaryList`; an
empty/unknown repo falls back to project-name base-OS parsing and the
registry path simply includes the empty/odd segment (no crash), matching
today's robustness.

## Testing

- **`buildReleaseContainerArtifacts`** (table tests, fake/stub binaries):
  - New layout: two `.containerinfo` binaries, same project+package,
    repos `ubi8`/`ubi9` → two artifacts, base OS "UBI 8"/"UBI 9",
    registry paths `…/containers/ubi8/<name>` and `…/ubi9/<name>`, tags
    not cross-contaminated.
  - Old layout: repo `images`, project `…:containers:ubi9` → one
    artifact, base OS "UBI 9", registry `…/containers/ubi9/images/<name>`
    (byte-identical to pre-change output).
- **`baseOSFromRepo`**: ubi8/ubi9/noble/bookworm mapped; `images` and
  unknown → "".
- **`deriveBaseOS(project, repo)`**: repo-first, project-fallback both
  covered.
- **`attachReleaseCveScans`**: a package with ubi8 (clean) and ubi9
  (CVEs) scan rows → the ubi9 artifact gets only the ubi9 scan, ubi8
  only the ubi8 scan.
- `go test ./... -count=1 && go build ./...`; frontend `npm run build`.
- Live check after deploy: a new-layout release version shows separate
  "UBI 8"/"UBI 9" groups with correct `docker pull` paths.

## Alternatives considered

- Base-OS label only (read repo in `deriveBaseOS`, keep project+package
  keying) — fixes the heading but the two repos' binaries still merge
  into one card with one registry path and merged tags; rejected.
- Reuse `cve.ImageBase` for the registry path — same formula, but pulls
  the cve package into api for a one-liner; a local helper is cleaner.
