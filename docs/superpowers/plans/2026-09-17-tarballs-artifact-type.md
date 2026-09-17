# Tarballs Artifact Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Tarballs" artifact type to the dashboard Artifacts tab — a third sub-tab alongside Packages and Container Images — showing PostgreSQL distribution tarballs built against the OpenSSL-versioned repos `ssl1.1`/`ssl3`/`ssl3.5`, with a per-card "Tarball" label + repo name.

**Architecture:** Tarballs are detected structurally (a build target in a `…:tarballs` subproject whose repo starts with `ssl`) — no backend flag, worker task, or CVE coupling. Live contexts (Staging/Devel/PR) derive tarballs client-side in `useArtifacts`, exactly mirroring the container fan-out; the Releases context gets a parallel server-built array in `release_artifacts.go`. Built time/state come straight from package targets, so tarballs need no metadata or CVE enrichment.

**Tech Stack:** Vue 3 + TypeScript (frontend, no runtime test runner — `.test-d.ts` type checks via `vue-tsc` in `npm run build`); Go (backend, `go test`).

**Spec:** `docs/superpowers/specs/2026-09-17-tarballs-artifact-type-design.md`

## Global Constraints

- **Detection rule (binding):** a tarball = a build target in a project whose name ends `:tarballs` AND whose repo matches `/^ssl/i` (`ssl1.1`, `ssl3`, `ssl3.5`, …). RockyLinux-built packages inside `:tarballs` (e.g. `percona-psql`) are NOT tarballs and stay in Packages.
- **Card content (binding):** a "Tarball" label + the ssl repo name, plus name, version, arch(es), build state, built time. **No** download link/pull command, **no** CVE/registry/tags sections.
- **Sub-tab union** `'packages' | 'containers'` must become `'packages' | 'containers' | 'tarballs'` **consistently** across App.vue, useUrlState.ts, ArtifactsVersionBar.vue, and ArtifactsPanel.vue — a partial widening fails `vue-tsc`.
- **Frontend has no runtime test runner** — verify by `npm run build` (vue-tsc) exit 0 + `.test-d.ts` type pins + manual reasoning against real prod project names.
- Commits: `git commit -s` (sign-off). **Never** add a `Co-Authored-By:` trailer.

**User decisions (already made):**
- "only ssl-repo builds" are tarballs (not the whole `:tarballs` subproject).
- Card shows "Label + repo + basics" (no download reference).
- Contexts: "also add Releases" (Staging + Devel + PR + Releases).
- Approved the 8-component design including the Releases backend piece.

---

### Task 1: Tarball detection helpers

**Goal:** A small pure module `lib/tarballs.ts` exports `isTarballRepo`, `isTarballTarget`, and `tarballRepoOrder`, with a `.test-d.ts` pinning their signatures.

**Files:**
- Create: `frontend/src/lib/tarballs.ts`
- Create: `frontend/src/lib/tarballs.test-d.ts`

**Acceptance Criteria:**
- [ ] `isTarballRepo('ssl3')` / `'ssl1.1'` / `'ssl3.5'` → true; `isTarballRepo('RockyLinux_9')` / `'ubi9'` → false
- [ ] `isTarballTarget('isv:percona:ppg:staging:18:tarballs', 'ssl3')` → true; `isTarballTarget('isv:percona:ppg:staging:18', 'ssl3')` → false (not a `:tarballs` project); `isTarballTarget('…:18:tarballs', 'RockyLinux_9')` → false
- [ ] `tarballRepoOrder` sorts `ssl1.1` < `ssl3` < `ssl3.5`
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Create `frontend/src/lib/tarballs.ts`**

```ts
// Detection helpers for tarball build artifacts. A tarball is a build target
// in a `…:tarballs` subproject whose repo is an OpenSSL-versioned repo
// (ssl1.1 / ssl3 / ssl3.5 / …). RockyLinux-built packages inside a :tarballs
// subproject (e.g. percona-psql) are NOT tarballs and stay in the Packages view.

export function isTarballRepo(repo: string): boolean {
  return /^ssl/i.test(repo)
}

export function isTarballTarget(project: string, repo: string): boolean {
  return project.endsWith(':tarballs') && isTarballRepo(repo)
}

// tarballRepoOrder sorts ssl repos naturally: ssl1.1 < ssl3 < ssl3.5.
export function tarballRepoOrder(a: string, b: string): number {
  return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })
}
```

- [ ] **Step 2: Create `frontend/src/lib/tarballs.test-d.ts`**

```ts
import { isTarballRepo, isTarballTarget, tarballRepoOrder } from './tarballs'

// Pins the helper signatures.
const a: boolean = isTarballRepo('ssl3')
const b: boolean = isTarballTarget('isv:percona:ppg:staging:18:tarballs', 'ssl3')
const c: number = tarballRepoOrder('ssl1.1', 'ssl3')
void a
void b
void c
```

- [ ] **Step 3: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0.

Manual reasoning against the AC (no runtime runner): `/^ssl/i` matches `ssl3`, `ssl1.1`, `ssl3.5`; not `RockyLinux_9`/`ubi9`. `isTarballTarget` additionally requires the project to end `:tarballs`. `localeCompare(..., {numeric:true})` orders `ssl1.1` < `ssl3` (1<3) and `ssl3` < `ssl3.5` (shorter prefix first).

- [ ] **Step 4: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/lib/tarballs.ts frontend/src/lib/tarballs.test-d.ts
git commit -s -m "feat(artifacts): tarball detection helpers"
```

```json:metadata
{"files": ["frontend/src/lib/tarballs.ts", "frontend/src/lib/tarballs.test-d.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["isTarballRepo true for ssl*, false for RockyLinux/ubi", "isTarballTarget requires :tarballs project AND ssl repo", "tarballRepoOrder ssl1.1<ssl3<ssl3.5", "build exit 0"], "modelTier": "mechanical"}
```

---

### Task 2: Live tarball derivation in `useArtifacts`

**Goal:** `useArtifacts` exports a `Tarball` interface and a `tarballs` computed that fans tarball packages out per ssl repo (one card per (package, ssl-repo)), returned alongside `packageRows`/`containerImages`.

**Files:**
- Modify: `frontend/src/composables/useArtifacts.ts`

**Acceptance Criteria:**
- [ ] New exported `Tarball` interface with `{ id, project, name, version, repo, arches, rollupState, published, builtAt }`
- [ ] `tarballs` filters packages where the project ends `:tarballs` and the version matches, then emits one `Tarball` per distinct ssl-repo target (via `isTarballTarget`); `arches` = distinct arches for that repo; `builtAt` = latest target `started_at`; `published` = any such target published
- [ ] RockyLinux targets inside a `:tarballs` package produce **no** tarball (filtered by `isTarballTarget`)
- [ ] `useArtifacts` return object includes `tarballs`
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Add the import** — at the top of `frontend/src/composables/useArtifacts.ts`, after the existing `import { matchesVersionKey } from '../lib/versions'` line (line 4), add:

```ts
import { isTarballTarget } from '../lib/tarballs'
```

- [ ] **Step 2: Add the `Tarball` interface** — after the `ContainerImage` interface (ends at line 48), add:

```ts
export interface Tarball {
  id: string          // `${project}/${name}/${repo}`
  project: string
  name: string
  version: string     // pkg.version, e.g. "18.6-3"
  repo: string        // ssl1.1 | ssl3 | ssl3.5
  arches: string[]    // distinct arches for this (pkg, repo)
  rollupState: string
  published: boolean
  builtAt: string     // latest target started_at for this repo ("" if none)
}
```

- [ ] **Step 3: Add the `tarballs` computed** — inside `useArtifacts`, immediately after the `containerImages` computed (ends at line 170) and before `return { packageRows, containerImages }`, add:

```ts
  // Tarballs: PostgreSQL distribution tarballs built against ssl* repos inside
  // a :tarballs subproject. Detection is structural (isTarballTarget) — no
  // backend flag. One card per (package, ssl-repo); built time/state come from
  // the package targets directly, so no metadata/CVE enrichment is involved.
  const tarballs = computed<Tarball[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)

    return pkgs
      .filter(pkg =>
        matchesProject(pkg.project, ver) &&
        pkg.project.endsWith(':tarballs')
      )
      .flatMap(pkg => {
        const targets = pkg.targets ?? []
        const repos = [...new Set(
          targets
            .filter((t: Target) => isTarballTarget(pkg.project, t.repo))
            .map((t: Target) => t.repo),
        )]
        return repos.map(repo => {
          const repoTargets = targets.filter((t: Target) => t.repo === repo)
          const arches = [...new Set(repoTargets.map((t: Target) => t.arch))]
          const builtAt = repoTargets.reduce(
            (latest: string, t: Target) => {
              const at = t.started_at ?? ''
              return at > latest ? at : latest
            },
            '',
          )
          return {
            id: pkg.project + '/' + pkg.name + '/' + repo,
            project: pkg.project,
            name: pkg.name,
            version: pkg.version ?? '',
            repo,
            arches,
            rollupState: pkg.rollup_state ?? '',
            published: repoTargets.some((t: Target) => t.published === true),
            builtAt,
          }
        })
      })
  })
```

- [ ] **Step 4: Return `tarballs`** — change the final return (line 172) from:

```ts
  return { packageRows, containerImages }
```

to:

```ts
  return { packageRows, containerImages, tarballs }
```

- [ ] **Step 5: Build check**

Run: `cd frontend && npm run build`
Expected: exit 0.

Manual reasoning: for `percona-postgresql-tarball` in `…:18:tarballs` with targets `ssl3.5`/`ssl3`/`ssl1.1` (×2 arches), `tarballs` at version "18" (once Task 4 folds `:tarballs`) emits 3 tarballs, each with `arches: ['x86_64','aarch64']`. For `percona-psql` (RockyLinux targets), `isTarballTarget` rejects every target → 0 tarballs. `started_at` values are RFC3339 strings, so lexical `>` picks the latest.

- [ ] **Step 6: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/composables/useArtifacts.ts
git commit -s -m "feat(artifacts): derive tarballs per ssl repo in useArtifacts"
```

```json:metadata
{"files": ["frontend/src/composables/useArtifacts.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["Tarball interface exported", "tarballs fans out per ssl repo via isTarballTarget", "RockyLinux :tarballs targets yield no tarball", "useArtifacts returns tarballs", "build exit 0"], "modelTier": "mechanical"}
```

---

### Task 3: `TarballsSubTab.vue` card component

**Goal:** A `TarballsSubTab.vue` component renders tarballs grouped by ssl repo, each card showing a "Tarball · `<repo>`" label, name, version, arch chips, build-state badge, and built time; with a `.props.test-d.ts`.

**Files:**
- Create: `frontend/src/components/TarballsSubTab.vue`
- Create: `frontend/src/components/TarballsSubTab.props.test-d.ts`

**Acceptance Criteria:**
- [ ] Props: `tarballs: Tarball[]`, `loading?: boolean` (no `copiedKey`/`copy` — no copy affordance)
- [ ] Tarballs grouped by `repo`, groups sorted `ssl1.1` → `ssl3` → `ssl3.5` via `tarballRepoOrder`; group header shows the repo + "N tarball(s)"
- [ ] Each card shows a tarball icon, a "Tarball" pill + `<repo>` code, the `name`, `version` (omitted when empty), arch chips, a rebuild badge (from `rollupState`), and built time (omitted when empty)
- [ ] No REGISTRY / TAGS / DOCKER PULL / SECURITY sections
- [ ] Empty state: "No tarballs for this version."
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Create `frontend/src/components/TarballsSubTab.vue`**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { Tarball } from '../composables/useArtifacts'
import { tarballRepoOrder } from '../lib/tarballs'
import { formatArtifactTime } from '../lib/cve'

const props = defineProps<{
  tarballs: Tarball[]
  loading?: boolean
}>()

// Group tarballs by ssl repo, sorted ssl1.1 < ssl3 < ssl3.5.
const groups = computed(() => {
  const map = new Map<string, Tarball[]>()
  for (const t of props.tarballs) {
    const list = map.get(t.repo) ?? []
    list.push(t)
    map.set(t.repo, list)
  }
  return Array.from(map.entries())
    .sort((a, b) => tarballRepoOrder(a[0], b[0]))
    .map(([repo, tarballs]) => ({ repo, tarballs }))
})

const STATE_LABELS: Record<string, string> = {
  building: 'Rebuilding',
  finished: 'Rebuilding',
  scheduled: 'Waiting to rebuild',
}

function rebuildBadge(t: Tarball): { label: string; cls: string } | null {
  const label = STATE_LABELS[t.rollupState]
  if (!label) return null
  return {
    label,
    cls: t.rollupState === 'scheduled' ? 'waiting' : 'building',
  }
}
</script>

<template>
  <div class="flex flex-col gap-6 p-4">
    <div
      v-if="loading"
      class="tarballs-loading flex flex-col items-center justify-center py-12 gap-3 text-text-muted"
      :class="{ compact: groups.length > 0 }"
    >
      <div class="spinner"></div>
      <span class="text-[13px] text-text-muted">Fetching tarballs…</span>
    </div>

    <template v-if="groups.length > 0">
      <div v-for="group in groups" :key="group.repo" class="flex flex-col gap-[14px]">
        <div class="flex items-center justify-between gap-4 px-[14px] py-[11px] bg-bg-card-2 border border-border border-l-4 border-l-brand-purple rounded-lg">
          <div class="flex flex-col gap-0.5 min-w-0">
            <h3 class="m-0 text-[15px] [font-weight:750] leading-[1.2] text-text-primary">{{ group.repo }}</h3>
            <span class="text-[11.5px] text-text-muted">OpenSSL {{ group.repo.replace(/^ssl/i, '') }} build</span>
          </div>
          <span class="shrink-0 px-2 py-[3px] rounded-[6px] bg-bg-card border border-border text-text-secondary text-[11px] font-bold whitespace-nowrap">
            {{ group.tarballs.length }} tarball{{ group.tarballs.length !== 1 ? 's' : '' }}
          </span>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4">
          <div
            v-for="tarball in group.tarballs"
            :key="tarball.id"
            class="bg-bg-card rounded-[12px] overflow-hidden flex flex-col [transition:opacity_0.15s_ease,filter_0.15s_ease]"
            :class="{ 'opacity-[0.48] grayscale-[0.85]': loading }"
          >
            <!-- Card header -->
            <div class="flex items-center justify-between px-[18px] py-[14px] border-b border-border">
              <div class="flex items-center gap-[10px]">
                <div class="flex items-center justify-center w-9 h-9 rounded-lg bg-info-tint text-info shrink-0">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none"
                       stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <path d="M21 8v13H3V8"/>
                    <path d="M1 3h22v5H1z"/>
                    <path d="M10 12h4"/>
                  </svg>
                </div>
                <span class="text-[14px] font-bold">{{ tarball.name }}</span>
              </div>
              <template v-for="badge in [rebuildBadge(tarball)]" :key="'rebuild-badge'">
                <span v-if="badge" class="status-badge" :class="badge.cls">{{ badge.label }}</span>
              </template>
            </div>

            <!-- Tarball label + repo -->
            <div class="bg-bg-card-2 px-[18px] py-[10px] border-b border-border">
              <div class="section-label">ARTIFACT</div>
              <span class="inline-flex items-center gap-[6px] mt-1 text-[12px] text-text-secondary font-semibold">
                <span class="inline-flex items-center px-2 py-[2px] rounded-[6px] bg-brand-purple-tint text-brand-purple text-[11px] font-bold">Tarball</span>
                <code class="font-mono">{{ tarball.repo }}</code>
              </span>
            </div>

            <!-- Version -->
            <div v-if="tarball.version" class="px-[18px] py-[10px] border-b border-border">
              <div class="section-label">VERSION</div>
              <span class="block mt-1 text-[12px] text-text-secondary">{{ tarball.version }}</span>
            </div>

            <!-- Architectures -->
            <div class="px-[18px] py-3 border-b border-border">
              <div class="section-label">ARCHITECTURES</div>
              <div class="flex flex-wrap gap-[6px] mt-2" v-if="tarball.arches.length > 0">
                <code
                  v-for="arch in tarball.arches"
                  :key="arch"
                  class="inline-flex items-center font-mono text-[11px] px-2 py-[3px] rounded-[6px] bg-bg-muted text-text-secondary font-semibold"
                >{{ arch }}</code>
              </div>
              <span v-else class="block mt-[6px] text-[12px] text-text-muted">No architectures yet</span>
            </div>

            <!-- Built -->
            <div v-if="tarball.builtAt" class="px-[18px] py-[10px] flex-1">
              <div class="section-label">BUILT</div>
              <span class="block mt-1 text-[12px] text-text-secondary">{{ formatArtifactTime(tarball.builtAt) }}</span>
            </div>
          </div>
        </div>
      </div>
    </template>

    <div v-else-if="!loading" class="text-center p-12 text-text-muted text-[14px]">
      No tarballs for this version.
    </div>
  </div>
</template>

<style scoped>
@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Loading states */
.tarballs-loading.compact {
  flex-direction: row;
  justify-content: flex-start;
  padding: 12px 16px;
  gap: 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.tarballs-loading.compact .spinner {
  width: 18px;
  height: 18px;
  border-width: 2px;
}

/* Section label shared utility */
.section-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

/* Status badges */
.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-badge.building {
  background: #fef9c3;
  color: #a16207;
}

.status-badge.waiting {
  background: var(--info-tint);
  color: var(--info);
}
</style>
```

- [ ] **Step 2: Create `frontend/src/components/TarballsSubTab.props.test-d.ts`**

```ts
import TarballsSubTab from './TarballsSubTab.vue'

type TarballsSubTabProps = InstanceType<typeof TarballsSubTab>['$props']

const propsWithLoading: TarballsSubTabProps = {
  tarballs: [],
  loading: true,
}

void propsWithLoading
```

- [ ] **Step 3: Build check**

Run: `cd frontend && npm run build`
Expected: exit 0 (the component compiles; the `.props.test-d.ts` pins the prop shape).

- [ ] **Step 4: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/components/TarballsSubTab.vue frontend/src/components/TarballsSubTab.props.test-d.ts
git commit -s -m "feat(artifacts): TarballsSubTab card component"
```

```json:metadata
{"files": ["frontend/src/components/TarballsSubTab.vue", "frontend/src/components/TarballsSubTab.props.test-d.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["props tarballs + loading only", "grouped by repo sorted ssl1.1<ssl3<ssl3.5", "card shows Tarball pill+repo, name, version, arches, state, built", "no registry/tags/pull/cve sections", "build exit 0"], "modelTier": "mechanical"}
```

---

### Task 4: Fold `:tarballs` into the version entry

**Goal:** Adding `'tarballs'` to `allowedSubprojects` in the PPG/PR context builders folds the `:tarballs` subproject into the plain numeric version, removing the stray `"<version>:tarballs"` selector entry.

**Files:**
- Modify: `frontend/src/lib/contexts.ts`

**Acceptance Criteria:**
- [ ] `PPG_DEVEL_CONTEXT`, `PPG_STAGING_CONTEXT`, and each `prArtifactsContexts` output have `allowedSubprojects: ['containers', 'tarballs']`
- [ ] `RELEASES_CONTEXT` is unchanged (no `allowedSubprojects`)
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Update the two static contexts** — in `frontend/src/lib/contexts.ts`, change the `allowedSubprojects` on `PPG_DEVEL_CONTEXT` (line 10) and `PPG_STAGING_CONTEXT` (line 17) from:

```ts
  allowedSubprojects: ['containers'],
```

to:

```ts
  allowedSubprojects: ['containers', 'tarballs'],
```

(Both occurrences.)

- [ ] **Step 2: Update the PR context builder** — in the `prArtifactsContexts` function, change the pushed context's `allowedSubprojects` (line 60) from:

```ts
        allowedSubprojects: ['containers'],
```

to:

```ts
        allowedSubprojects: ['containers', 'tarballs'],
```

- [ ] **Step 3: Build check**

Run: `cd frontend && npm run build`
Expected: exit 0.

Manual reasoning: `deriveVersionKeys` reads `sub = parts[depth+1]`; for `…:18:tarballs`, `sub='tarballs'` now ∈ absorbed → folds into plain `"18"` (no `"18:tarballs"` extension entry). `matchesVersionKey` for plain `"18"` matches `…:18:tarballs` because its first extra segment `tarballs` ∈ absorbed. `…:18:extras` (sub `extras` ∉ absorbed) is unaffected. `RELEASES_CONTEXT` keeps `allowedSubprojects: undefined` (catch-all), so its version selector already folds every subproject.

- [ ] **Step 4: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/lib/contexts.ts
git commit -s -m "feat(artifacts): fold :tarballs subproject into version entry"
```

```json:metadata
{"files": ["frontend/src/lib/contexts.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["devel/staging/PR contexts absorb containers+tarballs", "releases context unchanged", "build exit 0"], "modelTier": "mechanical"}
```

---

### Task 5: Three-way tab plumbing + ArtifactsPanel wiring

**Goal:** The Artifacts tab shows a third "Tarballs" sub-tab: the `'packages' | 'containers' | 'tarballs'` union is widened everywhere, the tab button is added, ssl repos are excluded from the Packages repo selector, and `ArtifactsPanel` renders `TarballsSubTab` fed by a `tarballs` computed covering both live and Releases contexts.

**Files:**
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/composables/useUrlState.ts`
- Modify: `frontend/src/components/ArtifactsVersionBar.vue`
- Modify: `frontend/src/components/ArtifactsPanel.vue`

**Acceptance Criteria:**
- [ ] The sub-tab union includes `'tarballs'` in App.vue (`artifactsTab` ref), useUrlState (`UrlStateOptions.artifactsTab` type + `?sub=` hydration guard), ArtifactsVersionBar (`activeTab` prop + `update:tab` emit), and ArtifactsPanel (`defineProps` + `update:artifactsTab` emit)
- [ ] ArtifactsVersionBar has a third "Tarballs" button emitting `'tarballs'`, styled like the other two
- [ ] `ArtifactsPanel.fetchRepos` filters ssl repos (via `isTarballRepo`) out of the live Packages repo list; `reposFromReleaseArtifacts` is unchanged
- [ ] `ArtifactsPanel` has a `tarballs` computed: live context → `useArtifacts` tarballs; Releases → `releaseArtifacts.value.tarballs` grouped by (project, name, repo) into `Tarball[]`
- [ ] The template renders `<ContainersSubTab v-else-if="props.artifactsTab === 'containers'">` and `<TarballsSubTab v-else :tarballs="tarballs" :loading="isLoading" />`
- [ ] `?sub=tarballs` round-trips (hydrates the tab; written to the URL when active)
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Widen the ref in `App.vue`** — change line 141 from:

```ts
const artifactsTab = ref<'packages' | 'containers'>('packages')
```

to:

```ts
const artifactsTab = ref<'packages' | 'containers' | 'tarballs'>('packages')
```

- [ ] **Step 2: Widen `useUrlState.ts`** — change the interface field (line 26) from:

```ts
  artifactsTab: Ref<'packages' | 'containers'>
```

to:

```ts
  artifactsTab: Ref<'packages' | 'containers' | 'tarballs'>
```

and the hydration guard (line 61) from:

```ts
    if (sub === 'packages' || sub === 'containers') artifactsTab.value = sub
```

to:

```ts
    if (sub === 'packages' || sub === 'containers' || sub === 'tarballs') artifactsTab.value = sub
```

(The write-back at line 124, `if (artifactsTab.value !== 'packages') params.set('sub', artifactsTab.value)`, already handles any non-default value — no change.)

- [ ] **Step 3: Widen `ArtifactsVersionBar.vue` types + add the button** — change the `activeTab` prop (line 8) from `activeTab: 'packages' | 'containers'` to `activeTab: 'packages' | 'containers' | 'tarballs'`, and the `update:tab` emit (line 15) from `'update:tab': [tab: 'packages' | 'containers']` to `'update:tab': [tab: 'packages' | 'containers' | 'tarballs']`. Then, in the tab switcher, add a third button immediately after the "Container Images" button (after line 77, inside the same `<div class="flex gap-[3px] …">`):

```vue
          <button
            class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
            :class="activeTab === 'tarballs'
              ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
              : 'bg-transparent text-text-muted font-medium border-transparent'"
            @click="emit('update:tab', 'tarballs')"
          >Tarballs</button>
```

- [ ] **Step 4: Wire `ArtifactsPanel.vue` — imports & types.** Add the tarball imports. Change the component/type imports near the top (lines 40-47) so the type import from `useArtifacts` also brings `Tarball`, add the `isTarballRepo` import, and import `TarballsSubTab`. Concretely:

Change line 41 from:

```ts
import type { ArtifactBinary, ContainerImage, PackageRow, RepoInfo } from '../composables/useArtifacts'
```

to:

```ts
import type { ArtifactBinary, ContainerImage, PackageRow, RepoInfo, Tarball } from '../composables/useArtifacts'
```

Add after line 47 (`import ContainersSubTab from './ContainersSubTab.vue'`):

```ts
import TarballsSubTab from './TarballsSubTab.vue'
import { isTarballRepo } from '../lib/tarballs'
```

Widen the `defineProps` `artifactsTab` (line 53) from `artifactsTab: 'packages' | 'containers'` to `artifactsTab: 'packages' | 'containers' | 'tarballs'`, and the `update:artifactsTab` emit (line 59) from `'update:artifactsTab': [tab: 'packages' | 'containers']` to `'update:artifactsTab': [tab: 'packages' | 'containers' | 'tarballs']`.

- [ ] **Step 5: Exclude ssl repos from the live Packages repo list.** In `fetchRepos` (lines 107-131), change the `next` construction (lines 118-121) from:

```ts
      const next: RepoInfo[] = [
        ...data.rpm.map(r => ({ ...r, type: 'rpm' as const })),
        ...data.deb.map(r => ({ ...r, type: 'deb' as const })),
      ]
```

to:

```ts
      const next: RepoInfo[] = [
        ...data.rpm.map(r => ({ ...r, type: 'rpm' as const })),
        ...data.deb.map(r => ({ ...r, type: 'deb' as const })),
      ].filter(r => !isTarballRepo(r.obs))
```

(This removes `ssl1.1`/`ssl3`/`ssl3.5` from the Packages repo selector for live contexts. `percona-postgresql-tarball` — ssl-only — thus leaves Packages entirely, while `percona-psql` keeps its RockyLinux repos. The release path's `reposFromReleaseArtifacts` is not touched.)

- [ ] **Step 6: Destructure live tarballs from `useArtifacts`.** Change the `useArtifacts` call (lines 198-204) from:

```ts
const { packageRows: livePackageRows, containerImages: liveContainerImages } = useArtifacts(
```

to:

```ts
const { packageRows: livePackageRows, containerImages: liveContainerImages, tarballs: liveTarballs } = useArtifacts(
```

(The rest of the call arguments are unchanged.)

- [ ] **Step 7: Add the release-tarball interfaces.** After the `ReleaseContainerArtifact` interface (ends at line 292), add:

```ts
interface ReleaseTarballArtifact {
  project: string
  name: string
  version: string
  repo: string
  arch: string
  built_at: string
}
```

and add a `tarballs` field to `ReleaseArtifactsResponse` (lines 294-299) so it reads:

```ts
interface ReleaseArtifactsResponse {
  version: string
  refreshed_at: string
  packages: ReleasePackageArtifact[]
  container_images: ReleaseContainerArtifact[]
  tarballs?: ReleaseTarballArtifact[]
}
```

(`tarballs` is optional so an older backend that doesn't yet emit it degrades to an empty list.)

- [ ] **Step 8: Add the `tarballs` computed.** After the `containerImages` computed (ends at line 251), add:

```ts
const tarballs = computed<Tarball[]>(() => {
  if (!isReleaseContext.value) return liveTarballs.value
  if (!releaseArtifacts.value) return []
  // Backend emits one ReleaseTarballArtifact per (name, repo, arch); group by
  // (project, name, repo) into one card with collected arches + latest built_at,
  // matching the live path's per-(package, ssl-repo) granularity.
  const byKey = new Map<string, Tarball>()
  for (const t of releaseArtifacts.value.tarballs ?? []) {
    const id = `${t.project}/${t.name}/${t.repo}`
    const existing = byKey.get(id)
    if (existing) {
      if (!existing.arches.includes(t.arch)) existing.arches.push(t.arch)
      if (t.built_at > existing.builtAt) existing.builtAt = t.built_at
    } else {
      byKey.set(id, {
        id,
        project: t.project,
        name: t.name,
        version: t.version,
        repo: t.repo,
        arches: [t.arch],
        rollupState: 'succeeded',
        published: true,
        builtAt: t.built_at,
      })
    }
  }
  return [...byKey.values()]
})
```

- [ ] **Step 9: Render the branch.** In the template, replace the `<ContainersSubTab v-else … />` block (lines 28-34) with:

```vue
    <ContainersSubTab
      v-else-if="props.artifactsTab === 'containers'"
      :container-images="containerImages"
      :copied-key="copiedKey"
      :loading="isLoading"
      @copy="onCopy"
    />

    <TarballsSubTab
      v-else
      :tarballs="tarballs"
      :loading="isLoading"
    />
```

- [ ] **Step 10: Build check**

Run: `cd frontend && npm run build`
Expected: exit 0. vue-tsc must show no errors — the union is consistent across all four files, and `TarballsSubTab`'s props match.

Manual reasoning: selecting the "Tarballs" button sets `artifactsTab='tarballs'` → `?sub=tarballs` written; on reload the hydration guard restores it. For a live Staging version 18, `tarballs` = `liveTarballs` = 3 cards (ssl1.1/ssl3/ssl3.5). The Packages repo selector no longer lists ssl repos, so `percona-postgresql-tarball` disappears from Packages while `percona-psql` (RockyLinux) remains. For the Releases context, `tarballs` maps `releaseArtifacts.value.tarballs` (empty until Task 6 ships, then populated).

- [ ] **Step 11: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/App.vue frontend/src/composables/useUrlState.ts frontend/src/components/ArtifactsVersionBar.vue frontend/src/components/ArtifactsPanel.vue
git commit -s -m "feat(artifacts): Tarballs sub-tab wired across artifacts panel"
```

```json:metadata
{"files": ["frontend/src/App.vue", "frontend/src/composables/useUrlState.ts", "frontend/src/components/ArtifactsVersionBar.vue", "frontend/src/components/ArtifactsPanel.vue"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["sub-tab union widened in all 4 files", "Tarballs button emits tarballs", "fetchRepos excludes ssl repos from Packages", "tarballs computed covers live + release", "v-else-if containers + v-else TarballsSubTab", "?sub=tarballs round-trips", "build exit 0"], "modelTier": "standard"}
```

---

### Task 6: Releases backend — tarball artifacts

**Goal:** The Releases artifacts endpoint returns a `tarballs` array built from ssl-repo binaries under `…:releases:<version>:tarballs` subprojects, covered by a Go table test.

**Files:**
- Modify: `backend/internal/api/release_artifacts.go`
- Modify: `backend/internal/api/release_artifacts_test.go`

**Acceptance Criteria:**
- [ ] `ReleaseTarballArtifact {Project, Name, Version, Repo, Arch, BuiltAt}` type added; `ReleaseArtifactsResponse` gains `Tarballs []ReleaseTarballArtifact` (`json:"tarballs"`)
- [ ] `buildReleaseArtifacts` searches `project+":tarballs"` subprojects, lists their binaries, and populates `Tarballs` via `buildReleaseTarballArtifacts`
- [ ] `buildReleaseTarballArtifacts` keeps only ssl-repo binaries (`isTarballRepo`), keys one artifact per (project, name, repo, arch), sets `BuiltAt` from the latest binary mtime, and sorts by (repo, name, arch); RockyLinux binaries under `:tarballs` are excluded
- [ ] `go test ./internal/api/ -run ReleaseTarball -count=1` passes; `go build ./...` succeeds
- [ ] **Verification against live OBS** (backend has authenticated access): confirm `ProjectBinaryList` on `isv:percona:ppg:releases:17:tarballs` returns binaries whose `Repo` is the ssl name (`ssl1.1`/`ssl3`/`ssl3.5`); if the repo field differs, adjust `isTarballRepo`/the filter accordingly. `Version` is intentionally left empty in this iteration (the card omits it) pending confirmation of the tarball binary/version naming — a small documented follow-up.

**Verify:** `cd backend && go test ./internal/api/ -run ReleaseTarball -count=1 -v && go build ./...` → PASS, build OK

**Steps:**

- [ ] **Step 1: Write the failing test** — append to `backend/internal/api/release_artifacts_test.go`:

```go
func TestBuildReleaseTarballArtifacts(t *testing.T) {
	base := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	proj := "isv:percona:ppg:releases:17:tarballs"
	binaries := []obs.BinaryArtifact{
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl3", Arch: "x86_64", Filename: "percona-postgresql-tarball.tar.gz", MTime: base.Unix(), BuiltAt: base},
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl3", Arch: "aarch64", Filename: "percona-postgresql-tarball.tar.gz", MTime: base.Unix(), BuiltAt: base},
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl1.1", Arch: "x86_64", Filename: "percona-postgresql-tarball.tar.gz", MTime: base.Unix(), BuiltAt: base},
		// RockyLinux build inside :tarballs — NOT a tarball, must be excluded.
		{Project: proj, Package: "percona-psql", Repo: "RockyLinux_9", Arch: "x86_64", Filename: "percona-psql.rpm", MTime: base.Unix(), BuiltAt: base},
	}

	out := buildReleaseTarballArtifacts(binaries)

	if len(out) != 3 {
		t.Fatalf("want 3 tarball artifacts, got %d: %+v", len(out), out)
	}
	for _, a := range out {
		if !isTarballRepo(a.Repo) {
			t.Errorf("non-ssl repo leaked: %q", a.Repo)
		}
		if a.Name != "percona-postgresql-tarball" {
			t.Errorf("unexpected name %q", a.Name)
		}
		if a.BuiltAt == "" {
			t.Errorf("built_at not set for %s/%s", a.Repo, a.Arch)
		}
	}
	// Sorted by repo then name then arch → ssl1.1 first.
	if out[0].Repo != "ssl1.1" {
		t.Errorf("want ssl1.1 first, got %q", out[0].Repo)
	}
}
```

If `obs` / `time` are not already imported in the test file, add them to its import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestBuildReleaseTarballArtifacts -count=1 -v`
Expected: FAIL — `undefined: buildReleaseTarballArtifacts` / `undefined: isTarballRepo` (compile error).

- [ ] **Step 3: Add the response type + response field** — in `backend/internal/api/release_artifacts.go`, add after the `ReleaseContainerArtifact` struct (ends at line 51):

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

and add the `Tarballs` field to `ReleaseArtifactsResponse` (lines 53-58) so it reads:

```go
type ReleaseArtifactsResponse struct {
	Version         string                     `json:"version"`
	RefreshedAt     string                     `json:"refreshed_at"`
	Packages        []ReleasePackageArtifact   `json:"packages"`
	ContainerImages []ReleaseContainerArtifact `json:"container_images"`
	Tarballs        []ReleaseTarballArtifact   `json:"tarballs"`
}
```

- [ ] **Step 4: Search & list tarball subprojects in `buildReleaseArtifacts`** — after the container-projects block (ends at line 188, the `containerBinaries` loop) and before the version-fetch block (line 190 `// Fetch binary EVR versions`), insert:

```go
	tarballProjects, err := client.SearchProjects(ctx, project+":tarballs")
	if err != nil {
		return ReleaseArtifactsResponse{}, err
	}
	var tarballBinaries []obs.BinaryArtifact
	for _, tarballProject := range tarballProjects {
		items, err := client.ProjectBinaryList(ctx, tarballProject)
		if err != nil {
			return ReleaseArtifactsResponse{}, err
		}
		tarballBinaries = append(tarballBinaries, items...)
	}
```

- [ ] **Step 5: Populate `Tarballs` in the response** — change the `response :=` assignment (lines 221-226) so it includes the tarballs:

```go
	response := ReleaseArtifactsResponse{
		Version:         version,
		RefreshedAt:     time.Now().UTC().Format(time.RFC3339),
		Packages:        buildReleasePackageArtifacts(binaries, versions),
		ContainerImages: buildReleaseContainerArtifacts(ctx, client, containerBinaries),
		Tarballs:        buildReleaseTarballArtifacts(tarballBinaries),
	}
```

- [ ] **Step 6: Add the builder + `isTarballRepo`** — add after `buildReleaseContainerArtifacts` (ends at line 339):

```go
// buildReleaseTarballArtifacts groups tarball binaries into per-(name,repo,arch)
// artifacts. A tarball is a binary in a :tarballs subproject built against an
// OpenSSL-versioned repo (ssl1.1 / ssl3 / ssl3.5 / …); binaries built against
// other repos (e.g. RockyLinux_*) are not tarballs and are skipped. Version is
// left empty in this iteration (the UI omits it) pending confirmation of the
// released-tarball binary/version naming.
func buildReleaseTarballArtifacts(binaries []obs.BinaryArtifact) []ReleaseTarballArtifact {
	byKey := map[string]*ReleaseTarballArtifact{}
	latestMTime := map[string]int64{}
	for _, binary := range binaries {
		if !isTarballRepo(binary.Repo) {
			continue
		}
		key := binary.Project + "\x00" + binary.Package + "\x00" + binary.Repo + "\x00" + binary.Arch
		artifact := byKey[key]
		if artifact == nil {
			artifact = &ReleaseTarballArtifact{
				Project: binary.Project,
				Name:    binary.Package,
				Repo:    binary.Repo,
				Arch:    binary.Arch,
			}
			byKey[key] = artifact
		}
		if binary.MTime > latestMTime[key] {
			latestMTime[key] = binary.MTime
			artifact.BuiltAt = binary.BuiltAt.Format(time.RFC3339)
		}
	}

	out := make([]ReleaseTarballArtifact, 0, len(byKey))
	for _, artifact := range byKey {
		out = append(out, *artifact)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Arch < out[j].Arch
	})
	return out
}

// isTarballRepo reports whether an OBS build repo is an OpenSSL-versioned
// tarball repo (ssl1.1 / ssl3 / ssl3.5 / …).
func isTarballRepo(repo string) bool {
	return strings.HasPrefix(strings.ToLower(repo), "ssl")
}
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestBuildReleaseTarballArtifacts -count=1 -v`
Expected: PASS.

- [ ] **Step 8: Full build + package suite**

Run: `cd backend && go build ./... && go test ./internal/api/ -count=1`
Expected: build OK, all api tests pass.

- [ ] **Step 9: Live OBS verification** (backend has authenticated access; this closes the one design unknown). Using the running backend or an ad-hoc client, confirm `ProjectBinaryList(ctx, "isv:percona:ppg:releases:17:tarballs")` returns binaries whose `Repo` field is `ssl1.1`/`ssl3`/`ssl3.5`. If the ssl repo is expressed differently (e.g. a path segment rather than `Repo`), adjust the `isTarballRepo` filter or the subproject binary handling so the ssl-repo binaries are kept and RockyLinux ones excluded. Re-run Step 8.

- [ ] **Step 10: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/api/release_artifacts.go backend/internal/api/release_artifacts_test.go
git commit -s -m "feat(artifacts): release tarball artifacts from ssl-repo builds"
```

```json:metadata
{"files": ["backend/internal/api/release_artifacts.go", "backend/internal/api/release_artifacts_test.go"], "verifyCommand": "cd backend && go test ./internal/api/ -run ReleaseTarball -count=1 -v && go build ./...", "acceptanceCriteria": ["ReleaseTarballArtifact type + Tarballs response field", "buildReleaseArtifacts searches :tarballs subprojects", "builder keeps ssl repos only, keys (project,name,repo,arch), sorts repo/name/arch", "RockyLinux :tarballs binaries excluded", "test + build green", "live OBS binary shape verified"], "modelTier": "standard"}
```

---

## Notes for the executor

- **Build sequencing:** Tasks 1→2→3 are a chain (helpers → derivation → component). Task 4 is independent. Task 5 depends on 1, 2, 3, and 4 (it imports the helper/type/component and needs the fold for tarballs to surface at a plain version). Task 6 (backend) is independent of the frontend; the Releases frontend mapping in Task 5 reads the optional `tarballs` field and degrades gracefully until Task 6 ships.
- **Post-merge manual check (all tasks done, deployed):** Staging 16/17/18 → a Tarballs sub-tab with `ssl1.1`/`ssl3`/`ssl3.5` groups each holding `percona-postgresql-tarball`; `percona-psql` still under Packages (RockyLinux repos); no stray `"<version>:tarballs"` version entry; the Releases context shows tarball cards for a released version once the backend is live.
