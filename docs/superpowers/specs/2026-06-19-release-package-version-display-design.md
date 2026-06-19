# Release Package Version Display — Design Spec

**Goal:** Show the package version (EVR) in the Artifacts tab package list for release contexts, where it is currently always blank.

**Architecture:** A new OBS client method fetches binary EVR data per `(repo, arch)` in parallel; the release artifacts builder merges the results and sets `Version` on each `ReleasePackageArtifact`; the frontend passes the field through to the existing version badge.

**Tech Stack:** Go (backend OBS client + release artifacts handler), Vue 3 / TypeScript (frontend one-liner)

**User decisions (already made):**
- Approach: per-repo-arch parallel fetch inside `buildReleaseArtifacts` (Option A)
- Version format: full EVR (`version-release`), epoch prefix stripped if present

---

## OBS Client

**File:** `backend/internal/obs/client.go`

New method:

```go
func (c *Client) RepoBinaryVersions(ctx context.Context, project, repo, arch string) (map[string]string, error)
```

- Calls `/build/<project>/<repo>/<arch>/_repository?view=binaryversions&withevr=1`
- Parses `<binaryversionlist><binary name="..." evr="..."/>`
- Returns `map[filename]evr`
- Strips epoch at parse time: if `evr` contains `:`, take the substring after the first `:`
- Returns an empty map (not an error) if the response body is empty or the list is empty

Example OBS response:

```xml
<binaryversionlist>
  <binary name="postgresql16.rpm" sizek="1234" evr="16.4-2.3" arch="x86_64"/>
  <binary name="postgresql16-devel.rpm" sizek="99" evr="16.4-2.3" arch="x86_64"/>
</binaryversionlist>
```

---

## Release Artifacts Builder

**File:** `backend/internal/api/release_artifacts.go`

### `ReleasePackageArtifact` — new field

```go
type ReleasePackageArtifact struct {
    // ... existing fields ...
    Version  string           `json:"version"`
}
```

### `buildReleaseArtifacts` — fan-out after `ProjectBinaryList`

After getting `binaries` from `client.ProjectBinaryList`:

1. Collect distinct `(repo, arch)` pairs from the non-container binaries (i.e. where `obs.IsDistributableBinary(binary.Filename)` is true).
2. Fan out one goroutine per pair, each calling `client.RepoBinaryVersions(ctx, project, repo, arch)`.
3. Merge results into a lookup map keyed by `repo + "\x00" + arch + "\x00" + filename → evr`.
4. Version fetch errors are non-fatal: log and continue with an empty version for that repo/arch.
5. Pass the lookup map to `buildReleasePackageArtifacts`.

### `buildReleasePackageArtifacts` — set Version

When constructing an artifact, iterate the binaries already collected. Set `artifact.Version` to the EVR of the first binary whose filename appears in the lookup map. All distributable binaries of the same package share the same EVR, so first-match is sufficient.

Signature change:

```go
func buildReleasePackageArtifacts(binaries []obs.BinaryArtifact, versions map[string]string) []ReleasePackageArtifact
```

Where `versions` key is `repo + "\x00" + arch + "\x00" + filename`.

---

## Frontend

**File:** `frontend/src/components/ArtifactsPanel.vue`

One-line change in the release row mapper (currently around line 264):

```typescript
// before
version: '',

// after
version: pkg.version ?? '',
```

`PackagesSubTab.vue:289` already renders `<code v-if="row.version" class="pkg-version">{{ row.version }}</code>` — no change needed there.

---

## Error Handling

- `RepoBinaryVersions` returns `(empty map, nil)` on an empty OBS response — no version shown, no crash.
- Per-pair goroutine errors are collected but non-fatal; the affected packages show no version badge rather than breaking the whole response.
- The result is cached by the existing `releaseArtifactsCache` TTL — no additional caching layer needed.

---

## Testing

- `backend/internal/obs/client_test.go` (or a new `client_binaryversions_test.go`): unit test for `RepoBinaryVersions` with a mock HTTP server returning sample XML; verify epoch stripping.
- `backend/internal/api/release_artifacts_test.go`: extend existing tests to assert `Version` is populated correctly and that a version-fetch error does not fail the whole handler.
