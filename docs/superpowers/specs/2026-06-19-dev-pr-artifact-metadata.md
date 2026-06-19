# DEV/PR Artifact Metadata and Rebuild Status — Design

**Goal:** Add backend-cached artifact metadata for DEV and PR projects so the Artifacts tab shows `built_at` timestamps for package binaries and container images, plus a "Rebuilding" indication when OBS is actively producing a replacement for an artifact that already exists.

**Architecture:** New `POST /api/artifacts/metadata` endpoint groups requested items by OBS project, fetches `ProjectBinaryList` once per project (5-minute in-process cache), and returns per-item metadata. The frontend composes a new `useArtifactMetadata` composable that eagerly batch-fetches metadata whenever visible rows change, merges it into the existing `PackageRow`/`ContainerImage` types, and derives `isRebuilding` from current target state. Release projects continue using the existing `/api/releases/ppg/{version}/artifacts` endpoint unchanged.

**Tech stack:** Go backend, Vue 3 + TypeScript frontend, no SQLite changes.

**User decisions (already made):**
- Metadata fetch is eager (fires when visible rows are computed, not on row expand).
- Frontend architecture: new `useArtifactMetadata` composable (Option A), not inlined in `ArtifactsPanel` or extended inside `useArtifacts`.
- `isRebuilding` is frontend-derived from current target/rollup state plus existing artifact metadata — no new persisted backend state.

---

## Backend

### New file: `backend/internal/api/artifact_metadata.go`

#### Endpoint

```
POST /api/artifacts/metadata
Content-Type: application/json
```

#### Request

```go
type ArtifactMetadataRequest struct {
    Items []ArtifactMetadataItem `json:"items"`
}

type ArtifactMetadataItem struct {
    Project string `json:"project"`
    Name    string `json:"name"`
    Repo    string `json:"repo"` // empty = any repo (used for containers)
    Arch    string `json:"arch"` // empty = any arch (used for containers)
    Kind    string `json:"kind"` // "package" | "container"
}
```

#### Response

```go
type ArtifactMetadataResponse struct {
    Items []ArtifactMetadataResult `json:"items"`
}

type ArtifactMetadataResult struct {
    Project  string           `json:"project"`
    Name     string           `json:"name"`
    Repo     string           `json:"repo"`
    Arch     string           `json:"arch"`
    Kind     string           `json:"kind"`
    BuiltAt  string           `json:"built_at,omitempty"` // RFC3339
    MTime    int64            `json:"mtime,omitempty"`
    Binaries []ArtifactBinary `json:"binaries,omitempty"` // packages only
}
```

`ArtifactBinary` is the existing type from `release_artifacts.go`. Items with no matching binaries are returned with empty `built_at` so the frontend knows the lookup ran.

#### Cache

A `binaryListCache` struct keyed by OBS project string, TTL 5 minutes. Uses the same concurrent-safe singleflight pattern as `releaseArtifactsCache` in `release_artifacts.go`. The cache lives on the handler (allocated once at server startup) and is not shared with the release artifacts cache.

#### Handler logic

1. Decode and validate request body; reject empty `items` with 400.
2. Group items by `project`.
3. For each unique project, fetch `client.ProjectBinaryList(ctx, project)` — or return the cached result if still within TTL.
4. For each request item, scan the project's binary list:
   - `kind == "package"`: filter binaries matching `name`, `repo`, and `arch`; keep only distributable binaries via `obs.IsDistributableBinary()`; derive `built_at` from max mtime; include the filtered `binaries` slice in the result.
   - `kind == "container"`: find the `.containerinfo` binary matching `name` (any repo/arch); use its mtime for `built_at`; do not include `binaries`.
5. Return `{ items: [...results] }`.

#### Registration

Wire in `backend/internal/api/server.go` (or wherever the mux is configured):

```go
mux.Handle("POST /api/artifacts/metadata", artifactMetadataHandler(obsClient, metadataCache))
```

---

## Frontend

### Type changes — `frontend/src/composables/useArtifacts.ts`

Add a new required field and three optional fields to `PackageRow`:

```typescript
targetState: string    // state of the specific repo/arch target (resolved during row construction)
builtAt?: string       // ISO 8601, populated from metadata API
mtime?: number
isRebuilding?: boolean // true when artifact exists and OBS is actively rebuilding it
```

Add three optional fields to `ContainerImage`:

```typescript
builtAt?: string
mtime?: number
isRebuilding?: boolean
```

`useArtifacts` populates `targetState` from `pkg.targets.find(t => t.repo === repo && t.arch === arch)?.state` — the value is already resolved at row construction time. The composable itself does not call the metadata API; it continues to be a pure derivation of package state.

### New composable — `frontend/src/composables/useArtifactMetadata.ts`

```typescript
export function useArtifactMetadata(
  packageRows: Ref<PackageRow[]>,
  containerImages: Ref<ContainerImage[]>,
  isLiveContext: Ref<boolean>,
): {
  enrichedPackageRows: ComputedRef<PackageRow[]>
  enrichedContainerImages: ComputedRef<ContainerImage[]>
}
```

**Internals:**

- `metadataMap: Ref<Map<string, ArtifactMetadataResult>>` — keyed by `"project/name/repo/arch/kind"`.
- `watch([packageRows, containerImages, isLiveContext], fetchMetadata, { immediate: true })` — re-fetches whenever visible rows change or context switches.
- `fetchMetadata`: skip fetch when `!isLiveContext.value` (release context); build the POST body from current rows + images; update `metadataMap` with results.
- Container request items use `repo: ""` and `arch: ""`.

**`isRebuilding` derivation:**

```
Package row:
  isRebuilding = !!builtAt && row.targetState ∈ { building, scheduled, finished }

Container image:
  isRebuilding = !!builtAt && image.rollupState ∈ { building, scheduled, finished }
```

`row.targetState` is the new field added to `PackageRow` by `useArtifacts`.

**Return values:** two computed refs that merge `metadataMap` into the raw rows/images and populate `builtAt`, `mtime`, and `isRebuilding`.

### `ArtifactsPanel.vue`

Compose `useArtifactMetadata` and pass enriched rows to sub-tabs:

```typescript
const { enrichedPackageRows, enrichedContainerImages } =
  useArtifactMetadata(livePackageRows, liveContainerImages, isLiveContext)
```

Replace `livePackageRows` / `liveContainerImages` passed to `PackagesSubTab` / `ContainersSubTab` with the enriched versions. Release context rows pass through `useArtifactMetadata` unchanged (fetch is skipped when `!isLiveContext`).

### `PackagesSubTab.vue`

- Display `row.builtAt` using the existing `formatBuiltAt` helper (same rendering as releases).
- Show a "Rebuilding" pill next to the timestamp when `row.isRebuilding`.
- In `toggleRow`: when `row.binaries` is present (populated from metadata), use it directly and skip the `GET /api/binaries` fetch.

### `ContainersSubTab.vue`

- Show BUILT timestamp when `image.builtAt` is set.
- Show a "Rebuilding" pill when `image.isRebuilding`.

---

## Assumptions

- Backend cache is sufficient for DEV/PR artifact metadata; no SQLite migration in this iteration.
- Rebuild detection is frontend-derived; no new persisted backend state.
- API field name is `built_at`; UI text reads "BUILT".
- "Rebuilding" means OBS is producing a replacement for an artifact that already exists. A first-time build with no prior `built_at` does not show "Rebuilding".
