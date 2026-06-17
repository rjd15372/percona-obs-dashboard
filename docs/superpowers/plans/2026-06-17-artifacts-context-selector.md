# Artifacts Context Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an independent context selector to the artifacts tab so users can browse PPG builds, release projects (`isv:percona:ppg:releases:<version>`), and PR build projects.

**Architecture:** Four tasks in dependency order: (1) backend adds releases packages + repos routes and a PR repos route; (2) `useArtifacts` composable gains a `contextPrefix` parameter for context-aware filtering; (3) `ArtifactsVersionBar` grows a context selector dropdown; (4) `ArtifactsPanel` becomes self-fetching and wires everything together, with `App.vue` updated to pass `prGroups` instead of `packages`.

**Tech Stack:** Go (chi router), SQLite via `store.QueryPackages`/`store.QueryDistinctRepos`, Vue 3 + TypeScript, existing `Context` interface from `types/api.ts`.

**User decisions (already made):**
- Artifacts tab context is independent from the builds tab — switching in one tab does not affect the other.
- Three project types: PPG builds, Releases (`isv:percona:ppg:releases:<version>`), PR builds.
- Context selector replaces the static OBS badge in `ArtifactsVersionBar`.
- Per-route repos endpoints: `/api/releases/ppg/{version}/repos` and `/api/pr/{pr}/{subproject}/{version}/repos`.
- `ArtifactsPanel` self-fetches its own packages; `App.vue` passes only `prGroups` for PR context discovery.

---

## File Map

| File | Change |
|------|--------|
| `backend/internal/api/handlers.go` | Refactor `reposHandler` to share logic via `reposHandlerWithPrefix`; add `releasesPackagesHandler`, `releasesReposHandler`, `prReposHandler` |
| `backend/internal/api/server.go` | Register `/api/releases/ppg/{version}/packages`, `/api/releases/ppg/{version}/repos`, `/api/pr/.../repos` |
| `backend/internal/api/handlers_test.go` | Tests for all three new routes |
| `frontend/src/composables/useArtifacts.ts` | Add `contextPrefix: MaybeRef<string>` parameter; update `packageRows` and `containerImages` filters |
| `frontend/src/components/ArtifactsVersionBar.vue` | Replace `obsRoot` prop with `contexts`+`selectedContext` props; add context `<select>` |
| `frontend/src/components/ArtifactsPanel.vue` | Self-fetch refactor: remove `packages`/`availableVersions`/`initialVersion` props; add `prGroups` prop; internal context state + fetch logic |
| `frontend/src/App.vue` | Pass `:pr-groups="prGroups"` to `ArtifactsPanel`; remove `packages`/`availableVersions`/`initialVersion` |

---

## Task 1: Backend — releases and PR repos routes

**Goal:** Add `GET /api/releases/ppg/{version}/packages`, `GET /api/releases/ppg/{version}/repos`, and `GET /api/pr/{pr}/{subproject}/{version}/repos` routes with passing tests.

**Files:**
- Modify: `backend/internal/api/handlers.go`
- Modify: `backend/internal/api/server.go`
- Modify: `backend/internal/api/handlers_test.go`

**Acceptance Criteria:**
- [ ] `GET /api/releases/ppg/17/packages` returns HTTP 200 with a JSON array (empty on fresh DB)
- [ ] `GET /api/releases/ppg/17/repos` returns HTTP 200 with `{"rpm":[],"deb":[]}` shape on fresh DB
- [ ] `GET /api/pr/pr-92/ppg/17/repos` returns HTTP 200 with `{"rpm":[],"deb":[]}` shape on fresh DB
- [ ] `go test ./backend/internal/api/...` passes

**Verify:** `cd backend && go test ./internal/api/... -v -run 'TestReleases|TestPRRepos'` → all PASS

**Steps:**

- [ ] **Step 1: Write failing tests first**

Add to `backend/internal/api/handlers_test.go`:

```go
func TestReleasesPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestReleasesReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}

func TestPRReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL (routes don't exist yet)**

```bash
cd backend && go test ./internal/api/... -v -run 'TestReleases|TestPRRepos'
```
Expected: FAIL — `404 page not found` or similar.

- [ ] **Step 3: Refactor reposHandler and add new handlers in handlers.go**

Replace the existing `reposHandler` function and add three new functions. The existing `reposHandler` body moves into a shared helper `reposHandlerWithPrefix`:

```go
// reposHandlerWithPrefix is the shared implementation for all /repos endpoints.
// prefixFn extracts the full OBS project prefix from the request URL params.
func reposHandlerWithPrefix(db *sql.DB, prefixFn func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obsRepos, err := store.QueryDistinctRepos(db, prefixFn(r))
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := ReposResponse{RPM: []RepoInfo{}, DEB: []RepoInfo{}}
		for _, obs := range obsRepos {
			info := RepoInfo{OBS: obs, Name: repoDisplayName(obs)}
			if repoType(obs) == "deb" {
				resp.DEB = append(resp.DEB, info)
			} else {
				resp.RPM = append(resp.RPM, info)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
	}
}

// reposHandler returns a handler for GET /api/products/{product}/{version}/repos.
func reposHandler(db *sql.DB) http.HandlerFunc {
	return reposHandlerWithPrefix(db, func(r *http.Request) string {
		return "isv:percona:" + chi.URLParam(r, "product") + ":" + chi.URLParam(r, "version")
	})
}

// releasesPackagesHandler returns a handler for GET /api/releases/ppg/{version}/packages.
// {version} is accepted for URL symmetry but ignored server-side; version filtering is client-side.
func releasesPackagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkgs, err := store.QueryPackages(db, "isv:percona:ppg:releases")
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			return
		}
	}
}

// releasesReposHandler returns a handler for GET /api/releases/ppg/{version}/repos.
func releasesReposHandler(db *sql.DB) http.HandlerFunc {
	return reposHandlerWithPrefix(db, func(r *http.Request) string {
		return "isv:percona:ppg:releases:" + chi.URLParam(r, "version")
	})
}

// prReposHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/repos.
func prReposHandler(db *sql.DB) http.HandlerFunc {
	return reposHandlerWithPrefix(db, func(r *http.Request) string {
		return "isv:percona:PR:" + chi.URLParam(r, "pr") + ":" +
			chi.URLParam(r, "subproject") + ":" + chi.URLParam(r, "version")
	})
}
```

- [ ] **Step 4: Register new routes in server.go**

Add the releases route group and the repos endpoint to the PR route group:

```go
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db))
		r.Get("/events", eventsHandler(db))
		r.Get("/repos", reposHandler(db))
	})

	r.Route("/api/releases/ppg/{version}", func(r chi.Router) {
		r.Get("/packages", releasesPackagesHandler(db))
		r.Get("/repos", releasesReposHandler(db))
	})

	r.Get("/api/pr/packages", prPackagesHandler(db))

	r.Route("/api/pr/{pr}/{subproject}/{version}", func(r chi.Router) {
		r.Get("/packages", prContextPackagesHandler(db))
		r.Get("/events", prContextEventsHandler(db))
		r.Get("/repos", prReposHandler(db))
	})

	r.Get("/api/stream", streamHandler(h))
	r.Get("/api/binaries", binariesHandler(obsClient))

	return r
}
```

- [ ] **Step 5: Run tests — expect PASS**

```bash
cd backend && go test ./internal/api/... -v -run 'TestReleases|TestPRRepos'
```
Expected: all three tests PASS.

- [ ] **Step 6: Run full backend test suite**

```bash
cd backend && go test ./...
```
Expected: all tests PASS, no compilation errors.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/api/handlers.go backend/internal/api/server.go backend/internal/api/handlers_test.go
git commit -s -m "feat(api): add releases packages+repos routes and PR repos route

- Refactor reposHandler to share logic via reposHandlerWithPrefix helper
- GET /api/releases/ppg/{version}/packages — queries isv:percona:ppg:releases prefix
- GET /api/releases/ppg/{version}/repos — repos for a specific releases version
- GET /api/pr/{pr}/{subproject}/{version}/repos — repos for a PR context version"
```

---

## Task 2: useArtifacts — add contextPrefix parameter

**Goal:** `useArtifacts` accepts a `contextPrefix` parameter so `packageRows` and `containerImages` filter against any OBS project prefix, not just the hardcoded PPG one.

**Files:**
- Modify: `frontend/src/composables/useArtifacts.ts`

**Acceptance Criteria:**
- [ ] `useArtifacts` signature includes `contextPrefix: MaybeRef<string>` as 5th parameter
- [ ] `packageRows` uses `exactProject = toValue(contextPrefix) + ':' + ver` instead of `'isv:percona:ppg:' + ver`
- [ ] `containerImages` uses `pkg.project.startsWith(toValue(contextPrefix) + ':' + ver + ':')` instead of `pkg.project.includes(':ppg:' + ver + ':')`
- [ ] `npx vue-tsc --noEmit` passes with no errors

**Verify:** `cd frontend && npx vue-tsc --noEmit` → exits 0, no output

**Steps:**

- [ ] **Step 1: Update useArtifacts.ts**

Replace the entire file content with:

```typescript
import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target } from '../types/api'

export interface RepoInfo {
  obs: string
  name: string
  type: 'rpm' | 'deb'
}

export interface PackageRow {
  project: string
  name: string
  version: string
  scope: 'common' | 'ppgcommon' | 'version'
  state: string
  published: boolean
  repo: RepoInfo
  arch: string
}

export interface ContainerImage {
  id: string
  imageName: string
  baseOs: string
  registry: string
  tags: string[]
  pullCmd: string
  rollupState: string
  published: boolean
}

export function deriveBaseOs(project: string): string {
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    const osMap: Record<string, string> = {
      'ubi8': 'UBI 8',
      'ubi9': 'UBI 9',
      'noble': 'Ubuntu 24.04 Noble',
      'bookworm': 'Debian 12 Bookworm',
    }
    return osMap[suffix] ?? suffix
  }
  return project
}

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  selectedRepo: MaybeRef<RepoInfo | null>,
  artArch: MaybeRef<string>,
  contextPrefix: MaybeRef<string>,
) {
  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repo = toValue(selectedRepo)
    const arch = toValue(artArch)
    const prefix = toValue(contextPrefix)

    if (!repo) return []

    const exactProject = `${prefix}:${ver}`
    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      if (pkg.project !== exactProject) continue

      const target = pkg.targets?.find(
        (t: Target) => t.repo === repo.obs && t.arch === arch,
      )
      if (!target) continue

      rows.push({
        project: pkg.project,
        name: pkg.name,
        version: pkg.version ?? '',
        scope: pkg.scope as 'common' | 'ppgcommon' | 'version',
        state: target.state ?? '',
        published: target.published === true,
        repo,
        arch,
      })
    }
    return rows
  })

  const containerImages = computed<ContainerImage[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const prefix = toValue(contextPrefix)

    return pkgs
      .filter(pkg =>
        pkg.scope === 'container' &&
        pkg.is_container !== false &&
        pkg.project.startsWith(`${prefix}:${ver}:`)
      )
      .map(pkg => {
        const tags = pkg.container_tags ?? []
        const baseOs = deriveBaseOs(pkg.project)
        const published = pkg.targets?.some((t: Target) => t.published === true) ?? false

        const registryPath = pkg.project.split(':').join('/')
        const registry = `registry.opensuse.org/${registryPath}/images/${pkg.name}`

        const pullTag = tags[tags.length - 1] ?? ''
        const pullCmd = pullTag
          ? `docker pull ${registry}:${pullTag}`
          : `docker pull ${registry}`

        return {
          id: pkg.project + '/' + pkg.name,
          imageName: pkg.name,
          baseOs,
          registry,
          tags,
          pullCmd,
          rollupState: pkg.rollup_state ?? '',
          published,
        }
      })
  })

  return { packageRows, containerImages }
}
```

- [ ] **Step 2: Run type check**

```bash
cd frontend && npx vue-tsc --noEmit
```
Expected: exits 0, no output. (ArtifactsPanel.vue will temporarily have a type error since it still calls useArtifacts without the new param — that's OK; it will be fixed in Task 4.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/composables/useArtifacts.ts
git commit -s -m "feat(ui): add contextPrefix parameter to useArtifacts

Package and container image filters now use the caller-supplied context
prefix instead of the hardcoded isv:percona:ppg path, enabling Releases
and PR project contexts."
```

---

## Task 3: ArtifactsVersionBar — context selector

**Goal:** `ArtifactsVersionBar` replaces the static OBS badge with a context-aware `<select>` dropdown (when multiple contexts exist) or a plain badge (when only one context exists).

**Files:**
- Modify: `frontend/src/components/ArtifactsVersionBar.vue`

**Acceptance Criteria:**
- [ ] `obsRoot` prop is removed; replaced by `contexts: Context[]` and `selectedContext: Context`
- [ ] `update:context` emit is declared with payload type `Context`
- [ ] When `contexts.length <= 1`, renders `<code class="obs-badge">{{ selectedContext.prefix }}:{{ version }}</code>`
- [ ] When `contexts.length > 1`, renders a `<select class="context-select">` with one `<option>` per context
- [ ] `npx vue-tsc --noEmit` passes (ArtifactsPanel.vue passes the new props in Task 4)

**Verify:** `cd frontend && npx vue-tsc --noEmit` → exits 0 (after Task 4 wires the props)

**Steps:**

- [ ] **Step 1: Rewrite ArtifactsVersionBar.vue**

Replace the entire file content:

```vue
<script setup lang="ts">
import type { Context } from '../types/api'

defineProps<{
  version: string
  availableVersions: string[]
  activeTab: 'packages' | 'containers'
  contexts: Context[]
  selectedContext: Context
}>()

const emit = defineEmits<{
  'update:version': [v: string]
  'update:tab': [tab: 'packages' | 'containers']
  'update:context': [ctx: Context]
}>()
</script>

<template>
  <div class="version-bar">
    <div class="top-row">
      <!-- PostgreSQL badge -->
      <span class="pg-badge">PostgreSQL</span>

      <!-- Context: plain badge when only PPG, dropdown when multiple contexts exist -->
      <code v-if="contexts.length <= 1" class="obs-badge">
        {{ selectedContext.prefix }}:{{ version }}
      </code>
      <select
        v-else
        class="context-select"
        :value="selectedContext.apiBase"
        @change="emit('update:context', contexts.find(c => c.apiBase === ($event.target as HTMLSelectElement).value)!)"
      >
        <option
          v-for="ctx in contexts"
          :key="ctx.apiBase"
          :value="ctx.apiBase"
        >{{ ctx.label }}</option>
      </select>

      <!-- Version segment control -->
      <div v-if="availableVersions.length > 0" class="inline-group">
        <span class="row-label">Version</span>
        <div class="segment">
          <button
            v-for="v in availableVersions"
            :key="v"
            class="seg-btn"
            :class="{ active: v === version }"
            @click="emit('update:version', v)"
          >{{ v }}</button>
        </div>
      </div>

      <!-- Tab switcher -->
      <div class="inline-group" style="margin-left: auto;">
        <div class="segment">
          <button
            class="seg-btn"
            :class="{ active: activeTab === 'packages' }"
            @click="emit('update:tab', 'packages')"
          >Packages</button>
          <button
            class="seg-btn"
            :class="{ active: activeTab === 'containers' }"
            @click="emit('update:tab', 'containers')"
          >Container Images</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.version-bar {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 14px 18px;
  margin: 12px 16px 0;
  flex-shrink: 0;
}

.top-row {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.pg-badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 5px 12px;
  border-radius: 8px;
  background: var(--tint-postgres);
  color: var(--tech-postgres);
  font-size: 12px;
  font-weight: 700;
  border: 1px solid rgba(0, 94, 214, 0.15);
}

.obs-badge {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  background: var(--bg-muted);
  padding: 5px 10px;
  border-radius: 7px;
}

.context-select {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  background: var(--bg-muted);
  padding: 5px 10px;
  border-radius: 7px;
  border: none;
  cursor: pointer;
  appearance: auto;
}

.inline-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.row-label {
  font-size: 11px;
  color: var(--text-muted);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-right: 2px;
}

.segment {
  display: flex;
  gap: 3px;
  background: var(--bg-muted);
  padding: 3px;
  border-radius: 9px;
}

.seg-btn {
  background: transparent;
  color: var(--text-muted);
  font-weight: 500;
  padding: 4px 12px;
  border-radius: 7px;
  border: none;
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
}

.seg-btn.active {
  background: var(--bg-card);
  color: var(--text-primary);
  font-weight: 700;
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/ArtifactsVersionBar.vue
git commit -s -m "feat(ui): add context selector to ArtifactsVersionBar

Replaces the static obs-badge with a <select> dropdown when multiple
contexts are available (PPG, Releases, PR builds), or a plain code badge
when only the default context exists."
```

---

## Task 4: ArtifactsPanel self-fetch refactor + App.vue wiring

**Goal:** `ArtifactsPanel` manages its own context state and fetches packages independently from `App.vue`. `App.vue` passes only `prGroups` for PR context discovery. The full context selector pipeline is wired end-to-end.

**Files:**
- Modify: `frontend/src/components/ArtifactsPanel.vue`
- Modify: `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `ArtifactsPanel` prop is `{ prGroups: PRGroup[] }` — no `packages`, `availableVersions`, or `initialVersion`
- [ ] `artifactsContexts` computed includes PPG, Releases, and PR contexts derived from `prGroups`
- [ ] Selecting a different context triggers a new packages + repos fetch
- [ ] `availableVersions` is derived from fetched packages using prefix depth
- [ ] Selecting a version in the bar re-fetches repos for the new version
- [ ] `npx vue-tsc --noEmit` passes with no errors
- [ ] `go build ./...` in `backend/` still passes

**Verify:** `cd frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Rewrite ArtifactsPanel.vue**

Replace the entire `<script setup>` section (keep the template and style sections, updating only the binding attributes as shown below):

```vue
<template>
  <div class="artifacts-panel">
    <ArtifactsVersionBar
      :version="localVersion"
      :available-versions="availableVersions"
      :active-tab="artifactsTab"
      :contexts="artifactsContexts"
      :selected-context="selectedContext"
      @update:version="onVersionChange"
      @update:tab="artifactsTab = $event"
      @update:context="onContextChange"
    />

    <PackagesSubTab
      v-if="artifactsTab === 'packages'"
      :package-rows="packageRows"
      :repos="repos"
      :selected-repo="selectedRepo"
      :version="localVersion"
      :art-arch="artArch"
      :copied-key="copiedKey"
      @update:art-repo="artRepoObs = $event"
      @update:art-arch="artArch = $event as 'x86_64' | 'aarch64'"
      @copy="onCopy"
    />

    <ContainersSubTab
      v-else
      :container-images="containerImages"
      :copied-key="copiedKey"
      @copy="onCopy"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { Context, Package, PRGroup } from '../types/api'
import type { RepoInfo } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'

const props = defineProps<{
  prGroups: PRGroup[]
}>()

// ── Context constants ─────────────────────────────────────────────────────────

const PPG_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
}

const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}

// ── State ─────────────────────────────────────────────────────────────────────

const selectedContext = ref<Context>(PPG_CONTEXT)
const artifactsPackages = ref<Package[]>([])
const localVersion = ref('')
const artifactsTab = ref<'packages' | 'containers'>('packages')
const artRepoObs = ref<string>('')
const artArch = ref<'x86_64' | 'aarch64'>('x86_64')
const copiedKey = ref<string | null>(null)
const repos = ref<RepoInfo[]>([])

// ── Contexts ──────────────────────────────────────────────────────────────────

const artifactsContexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of props.prGroups) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 2 >= parts.length) continue
      const prSegment = parts[prIdx + 1]   // "pr-92"
      const subproject = parts[prIdx + 2]  // "ppg"
      const key = `${prSegment}:${subproject}`
      if (seen.has(key)) continue
      seen.add(key)
      const prNum = prSegment.replace(/^pr-/i, '')
      prContexts.push({
        label: `PR #${prNum} · ${subproject}`,
        apiBase: `/api/pr/${prSegment}/${subproject}`,
        prefix: `isv:percona:PR:${prSegment}:${subproject}`,
      })
    }
  }

  prContexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [PPG_CONTEXT, RELEASES_CONTEXT, ...prContexts]
})

// ── Available versions (derived from fetched packages) ────────────────────────

const availableVersions = computed<string[]>(() => {
  const depth = selectedContext.value.prefix.split(':').length
  const found = new Set<string>()
  for (const pkg of artifactsPackages.value) {
    const seg = pkg.project.split(':')[depth]
    if (seg && /^\d+$/.test(seg)) found.add(seg)
  }
  return [...found].sort((a, b) => parseInt(b) - parseInt(a))
})

// Reset localVersion to the highest available when context changes and current
// version is no longer present in the new context's package set.
watch(availableVersions, (vers) => {
  if (vers.length > 0 && !vers.includes(localVersion.value)) {
    localVersion.value = vers[0]
  }
})

// ── Derived ───────────────────────────────────────────────────────────────────

const contextPrefix = computed(() => selectedContext.value.prefix)

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

const { packageRows, containerImages } = useArtifacts(
  artifactsPackages,
  localVersion,
  selectedRepo,
  artArch,
  contextPrefix,
)

// ── Fetching ──────────────────────────────────────────────────────────────────

async function fetchPackages(ctx: Context) {
  try {
    // {version} in URL is accepted but ignored server-side; all versions returned.
    const res = await fetch(`${ctx.apiBase}/17/packages`)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json()
    artifactsPackages.value = Array.isArray(data) ? data : []
  } catch {
    artifactsPackages.value = []
  }
}

async function fetchRepos(version: string) {
  const ctx = selectedContext.value
  let url: string
  if (ctx.apiBase.startsWith('/api/products/')) {
    url = `/api/products/ppg/${version}/repos`
  } else if (ctx.apiBase.startsWith('/api/releases/')) {
    url = `/api/releases/ppg/${version}/repos`
  } else {
    // /api/pr/{pr}/{subproject}
    url = `${ctx.apiBase}/${version}/repos`
  }
  try {
    const res = await fetch(url)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json() as { rpm: { obs: string; name: string }[]; deb: { obs: string; name: string }[] }
    const next: RepoInfo[] = [
      ...data.rpm.map(r => ({ ...r, type: 'rpm' as const })),
      ...data.deb.map(r => ({ ...r, type: 'deb' as const })),
    ]
    repos.value = next
    if (next.length > 0 && !next.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = next.find(r => r.type === 'rpm')?.obs ?? next[0].obs
    }
  } catch {
    repos.value = []
  }
}

// Re-fetch repos whenever localVersion settles (after availableVersions watcher sets it).
watch(localVersion, (v) => {
  if (v) fetchRepos(v)
})

function onVersionChange(v: string) {
  localVersion.value = v
}

function onContextChange(ctx: Context) {
  selectedContext.value = ctx
  repos.value = []
  artRepoObs.value = ''
  fetchPackages(ctx)
}

// Initial fetch on mount
fetchPackages(PPG_CONTEXT)

// ── Copy ──────────────────────────────────────────────────────────────────────

let copyTimer: ReturnType<typeof setTimeout> | null = null
function onCopy(key: string, text: string) {
  navigator.clipboard.writeText(text).catch(() => {})
  copiedKey.value = key
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copiedKey.value = null
    copyTimer = null
  }, 2000)
}
</script>

<style scoped>
.artifacts-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
</style>
```

- [ ] **Step 2: Update App.vue — replace ArtifactsPanel props**

In `App.vue`, find the `<ArtifactsPanel>` element and replace its attributes:

```html
<!-- BEFORE -->
<ArtifactsPanel
  v-else
  :packages="rawPackages"
  :available-versions="availableVersions"
  :initial-version="version || availableVersions[0] || '17'"
/>

<!-- AFTER -->
<ArtifactsPanel
  v-else
  :pr-groups="prGroups"
/>
```

- [ ] **Step 3: Run type check**

```bash
cd frontend && npx vue-tsc --noEmit
```
Expected: exits 0, no errors.

- [ ] **Step 4: Run backend build check**

```bash
cd backend && go build ./...
```
Expected: exits 0, no output.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ArtifactsPanel.vue frontend/src/App.vue
git commit -s -m "feat(ui): ArtifactsPanel self-fetch with context selector

ArtifactsPanel now manages its own context state and fetches packages
independently. Contexts: PPG (default), Releases, and any PR builds
derived from the prGroups prop. Version list is derived from fetched
packages using prefix depth. Selecting a context or version triggers
the appropriate packages/repos re-fetch."
```
