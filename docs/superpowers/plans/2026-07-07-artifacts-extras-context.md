# Artifacts "PPG Extras" Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "PPG Extras" entry to the Artifacts context selector showing `isv:percona:ppg:<v>:extras` packages/containers, with the plain PPG entry cleanly excluding them via an allowlist.

**Architecture:** The frontend `Context` model gains `subproject?` (view rooted at `prefix:ver:<subproject>`) and `allowedSubprojects?` (PPG = `['containers']` — future subprojects hidden by default). Row matching (`useArtifacts`), version pills, repos URL, and the URL key all read the fields generically. The only backend change: the products repos endpoint accepts a sanitized `?subproject=` param appended to its query prefix.

**Tech Stack:** Vue 3 + TypeScript (no JS test runner — verified; validation = `vue-tsc` via `npm run build`), Go backend (httptest for the repos handler).

**User decisions (already made):**
- Approach A — generalize the frontend `Context` model; no dedicated backend routes.
- Clean separation: extras appear ONLY in "PPG Extras"; PPG excludes them.
- Allowlist (`allowedSubprojects: ['containers']`) not denylist — "more subprojects are coming"; they must hide by default, one new constant exposes each.
- Dev-only scope: releases extras is a follow-up; PR contexts keep their catch-all (extras mixed in).
- Invalid `?subproject=` → HTTP 400 (spec).
- Spec approved: `docs/superpowers/specs/2026-07-07-artifacts-extras-context-design.md` (`114dd99`).

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `backend/internal/api/handlers.go` | `?subproject=` param on `reposHandler` | 1 |
| `backend/internal/api/handlers_test.go` | repos param tests | 1 |
| `frontend/src/types/api.ts` | `Context` fields | 2 |
| `frontend/src/lib/contexts.ts` | `PPG_EXTRAS_CONTEXT`; PPG allowlist | 2 |
| `frontend/src/App.vue` | selector list insert | 2 |
| `frontend/src/composables/useUrlState.ts` | `contextToKey` disambiguation | 2 |
| `frontend/src/composables/useArtifacts.ts` | subproject-aware row matching | 3 |
| `frontend/src/components/ArtifactsPanel.vue` | version pills + repos URL + pass context | 3 |

---

### Task 1: Backend — `?subproject=` on the products repos endpoint

**Goal:** `GET /api/products/ppg/{version}/repos?subproject=extras` returns only the repos that extras packages target; invalid param → 400.

**Files:**
- Modify: `backend/internal/api/handlers.go` (reposHandler, ~line 264)
- Test: `backend/internal/api/handlers_test.go`

**Acceptance Criteria:**
- [ ] `?subproject=extras` appends `:extras` to the query prefix (`isv:percona:ppg:18:extras`), so only extras repos return.
- [ ] Param absent → behaviour byte-identical to today.
- [ ] Param not matching `^[a-z0-9_-]+$` (e.g. `Ex%tras`, `EXTRAS`) → HTTP 400.
- [ ] Existing repos tests pass unmodified.

**Verify:** `cd backend && go test ./internal/api/ -run TestRepos -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** — add to `backend/internal/api/handlers_test.go` (match the existing repos-test setup around lines 233/250 — they build requests like `httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/repos", nil)` against the test router; reuse the same helper/seed pattern; seed one main package with a `Debian_13` target at `isv:percona:ppg:18` and one extras package with a `UBI_9` target at `isv:percona:ppg:18:extras`, both `is_container` false):

```go
func TestReposSubprojectParam(t *testing.T) {
	// Seed: main package (Debian_13) + extras package (UBI_9).
	// Use the same server/seed helper as the existing repos tests in this file.
	// main:   project "isv:percona:ppg:18",        target repo "Debian_13"
	// extras: project "isv:percona:ppg:18:extras", target repo "UBI_9"

	// 1. With ?subproject=extras → only UBI_9.
	//    GET /api/products/ppg/18/repos?subproject=extras
	//    assert: rpm list contains "UBI_9"; neither list contains "Debian_13".

	// 2. Without the param → both repos (unchanged behaviour).
	//    GET /api/products/ppg/18/repos
	//    assert: "Debian_13" present.

	// 3. Invalid param → 400.
	//    GET /api/products/ppg/18/repos?subproject=EX%25TRAS  (encoded "EX%TRAS")
	//    assert: response code 400.
}
```

Write the three sub-assertions as real code following the file's existing repos tests (decode `ReposResponse`, check `.RPM`/`.DEB` entries by `OBS` field). The comment skeleton above defines the required assertions; the surrounding mechanics must mirror the neighbouring tests exactly.

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/api/ -run TestReposSubprojectParam -v`
Expected: FAIL — the param is ignored today, so case 1 sees `Debian_13` and case 3 gets 200.

- [ ] **Step 3: Implement** in `backend/internal/api/handlers.go`. Add near the top of the file (with the other package-level declarations):

```go
// subprojectRe validates the optional ?subproject= repos-endpoint param; the
// value is appended to a SQL LIKE prefix, so only plain segment names pass.
var subprojectRe = regexp.MustCompile(`^[a-z0-9_-]+$`)
```

(add `"regexp"` to imports). Replace `reposHandler`:

```go
// reposHandler returns a handler for GET /api/products/{product}/{version}/repos.
// It queries the DB for distinct OBS repository names found in non-container
// packages' targets, and returns them grouped into rpm and deb categories.
// An optional ?subproject=<segment> narrows the query to that subproject
// (e.g. isv:percona:ppg:18:extras); invalid segments are rejected with 400.
func reposHandler(db *sql.DB) http.HandlerFunc {
	inner := reposHandlerWithPrefix(db, func(r *http.Request) string {
		prefix := "isv:percona:" + chi.URLParam(r, "product") + ":" + chi.URLParam(r, "version")
		if sub := r.URL.Query().Get("subproject"); sub != "" {
			prefix += ":" + sub
		}
		return prefix
	})
	return func(w http.ResponseWriter, r *http.Request) {
		if sub := r.URL.Query().Get("subproject"); sub != "" && !subprojectRe.MatchString(sub) {
			http.Error(w, "invalid subproject", http.StatusBadRequest)
			return
		}
		inner(w, r)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/api/ -v && go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/api/handlers.go internal/api/handlers_test.go
git commit -s -m "feat(api): optional subproject param on products repos endpoint"
```

```json:metadata
{"files": ["backend/internal/api/handlers.go", "backend/internal/api/handlers_test.go"], "verifyCommand": "cd backend && go test ./internal/api/ -run TestRepos -v && go test ./...", "acceptanceCriteria": ["?subproject=extras narrows prefix to :extras (only extras repos)", "absent param unchanged", "invalid param -> 400", "existing repos tests pass"], "modelTier": "standard"}
```

---

### Task 2: Frontend — subproject-aware `Context` model + selector + URL key

**Goal:** The `Context` type carries `subproject`/`allowedSubprojects`; "PPG Extras" appears in the selector; URL keys disambiguate (`ppg-extras`).

**Files:**
- Modify: `frontend/src/types/api.ts` (Context interface, ~line 4)
- Modify: `frontend/src/lib/contexts.ts`
- Modify: `frontend/src/App.vue` (artifactsContexts, ~line 139-166)
- Modify: `frontend/src/composables/useUrlState.ts` (contextToKey, ~line 5)

**Acceptance Criteria:**
- [ ] `Context` gains optional `subproject?: string` and `allowedSubprojects?: string[]` (documented).
- [ ] `PPG_CONTEXT` has `allowedSubprojects: ['containers']`; new `PPG_EXTRAS_CONTEXT` has `subproject: 'extras'` (same apiBase/prefix as PPG).
- [ ] `App.vue` artifacts selector list is `[PPG_CONTEXT, PPG_EXTRAS_CONTEXT, RELEASES_CONTEXT, ...prContexts]` (board contexts unchanged).
- [ ] `contextToKey` returns `ppg-extras` for the extras context, and `ppg`/`releases`/`pr-NN` unchanged for the others.
- [ ] `cd frontend && npm run build` passes (vue-tsc type-check).

**Verify:** `cd frontend && npm run build` → success

**Steps:**

- [ ] **Step 1:** In `frontend/src/types/api.ts`, extend the interface:

```ts
export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg" or "/api/pr/pr-92"
  prefix: string   // e.g. "isv:percona:ppg" or "isv:percona:PR:pr-92"
  /** This context views only prefix:ver:<subproject> and beneath (e.g. "extras"). */
  subproject?: string
  /** Without subproject: which direct subprojects of prefix:ver belong to this
   *  context (allowlist). Undefined = catch-all (PR/Releases contexts). */
  allowedSubprojects?: string[]
}
```

- [ ] **Step 2:** In `frontend/src/lib/contexts.ts`:

```ts
export const PPG_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
  // The distribution + its container images; sibling subprojects (extras, …)
  // are hidden by default and get their own selector entries.
  allowedSubprojects: ['containers'],
}

export const PPG_EXTRAS_CONTEXT: Context = {
  label: 'PPG Extras',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
  subproject: 'extras',
}
```

- [ ] **Step 3:** In `frontend/src/App.vue`, import `PPG_EXTRAS_CONTEXT` from `./lib/contexts` and change the artifacts list return (~line 166):

```ts
  return [PPG_CONTEXT, PPG_EXTRAS_CONTEXT, RELEASES_CONTEXT, ...prContexts]
```

(The board contexts computed at ~line 130 returns `[PPG_CONTEXT, ...prContexts]` — leave it untouched.)

- [ ] **Step 4:** In `frontend/src/composables/useUrlState.ts`, update `contextToKey`:

```ts
export function contextToKey(ctx: Context): string {
  const parts = ctx.prefix.split(':')
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0) {
    return parts[prIdx + 1] // e.g. "pr-106"
  }
  const last = parts[parts.length - 1] // "ppg" or "releases"
  return ctx.subproject ? `${last}-${ctx.subproject}` : last // "ppg-extras"
}
```

- [ ] **Step 5:** Run `cd frontend && npm run build` — must succeed.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/types/api.ts frontend/src/lib/contexts.ts frontend/src/App.vue frontend/src/composables/useUrlState.ts
git commit -s -m "feat(frontend): subproject-aware Context model and PPG Extras selector entry"
```

```json:metadata
{"files": ["frontend/src/types/api.ts", "frontend/src/lib/contexts.ts", "frontend/src/App.vue", "frontend/src/composables/useUrlState.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["Context gains subproject/allowedSubprojects", "PPG allowlist ['containers']; PPG_EXTRAS_CONTEXT subproject 'extras'", "selector list [PPG, PPG Extras, Releases, ...PRs]", "contextToKey returns ppg-extras", "npm run build passes"], "modelTier": "mechanical"}
```

---

### Task 3: Frontend — subproject-aware matching, version pills, repos URL

**Goal:** `useArtifacts` and `ArtifactsPanel` honour `subproject`/`allowedSubprojects`: extras context shows only the extras subtree, PPG shows main + containers only, extras version pills self-limit, extras repos come from the new query param.

**Files:**
- Modify: `frontend/src/composables/useArtifacts.ts` (signature + matching, ~lines 74-157)
- Modify: `frontend/src/components/ArtifactsPanel.vue` (availableVersions ~line 70, fetchRepos ~line 112, useArtifacts call ~line 203)

**Acceptance Criteria:**
- [ ] `useArtifacts` 5th param becomes `context: MaybeRef<Context>`; a shared `matchesProject` implements: exact root match; beneath root → subproject contexts accept all, allowlist contexts accept only listed first segments, no-field contexts accept all (PR/Releases unchanged).
- [ ] Container images use the same `matchesProject` (with `is_container === true`).
- [ ] `availableVersions`: when `ctx.subproject` set, a version counts only if the segment after it equals the subproject.
- [ ] `fetchRepos`: products contexts with `subproject` append `?subproject=<value>` (URL-encoded).
- [ ] `npm run build` passes; backend suite untouched.

**Verify:** `cd frontend && npm run build && cd ../backend && go test ./...` → success

**Steps:**

- [ ] **Step 1:** In `frontend/src/composables/useArtifacts.ts`: import the `Context` type, change the signature and matching. Replace the function head and both computeds' matching logic:

```ts
import type { Package, Target, CveScan, Context } from '../types/api'

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  selectedRepo: MaybeRef<RepoInfo | null>,
  artArch: MaybeRef<string>,
  context: MaybeRef<Context>,
) {
  // matchesProject: does an OBS project belong to this context at this version?
  //  - subproject contexts (e.g. PPG Extras) own prefix:ver:<subproject> and
  //    everything beneath it (including its :containers:* subprojects).
  //  - allowlist contexts (PPG) own prefix:ver plus only the listed direct
  //    subprojects — future subprojects stay hidden until given an entry.
  //  - contexts with neither field (PR, Releases) keep the historical
  //    catch-all: prefix:ver and everything beneath.
  const matchesProject = (project: string, ver: string): boolean => {
    const ctx = toValue(context)
    const root = ctx.subproject
      ? `${ctx.prefix}:${ver}:${ctx.subproject}`
      : `${ctx.prefix}:${ver}`
    if (project === root) return true
    if (!project.startsWith(root + ':')) return false
    if (ctx.subproject) return true
    if (!ctx.allowedSubprojects) return true
    const first = project.slice(root.length + 1).split(':')[0]
    return ctx.allowedSubprojects.includes(first)
  }
```

`packageRows` — replace the `exactProject`/`inProject` block:

```ts
    for (const pkg of pkgs) {
      // Confirmed container images (is_container: true) are excluded — they belong
      // in the Container Images tab, not here.
      if (!matchesProject(pkg.project, ver) || pkg.is_container === true) continue
      ...
```

`containerImages` — replace the filter:

```ts
    return pkgs
      .filter(pkg =>
        pkg.is_container === true &&
        matchesProject(pkg.project, ver)
      )
```

(Remove the now-unused `contextPrefix`/`prefix` variables; `ver` is already computed in each computed body.)

- [ ] **Step 2:** In `frontend/src/components/ArtifactsPanel.vue`:

`availableVersions` (~line 70) — add the subproject filter:

```ts
const availableVersions = computed<string[]>(() => {
  const ctx = props.artifactsContext
  const depth = ctx.prefix.split(':').length
  const versions = new Set<string>()
  for (const pkg of artifactsPackages.value) {
    const parts = pkg.project.split(':')
    const seg = parts[depth]
    if (!seg || !/^\d+$/.test(seg)) continue
    // Subproject contexts only offer versions that actually have the subproject.
    if (ctx.subproject && parts[depth + 1] !== ctx.subproject) continue
    versions.add(seg)
  }
  return [...versions].sort((a, b) => parseInt(b) - parseInt(a))
})
```

`fetchRepos` (~line 112) — append the param in the products branch:

```ts
  if (ctx.apiBase.startsWith('/api/products/')) {
    url = `/api/products/ppg/${version}/repos`
    if (ctx.subproject) {
      url += `?subproject=${encodeURIComponent(ctx.subproject)}`
    }
  } else {
```

`useArtifacts` call (~line 203) — pass the whole context instead of the prefix:

```ts
const { packageRows: livePackageRows, containerImages: liveContainerImages } = useArtifacts(
  artifactsPackages,
  localVersion,
  selectedRepo,
  artArch,
  computed(() => props.artifactsContext),
)
```

If `contextPrefix` (~line 63) has no remaining usages after this, remove it; if other code still uses it, leave it.

- [ ] **Step 3:** Run `cd frontend && npm run build` — must succeed. Run `cd ../backend && go test ./...` — untouched, must stay green.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/composables/useArtifacts.ts frontend/src/components/ArtifactsPanel.vue
git commit -s -m "feat(frontend): subproject-aware artifact matching, version pills, and repos"
```

```json:metadata
{"files": ["frontend/src/composables/useArtifacts.ts", "frontend/src/components/ArtifactsPanel.vue"], "verifyCommand": "cd frontend && npm run build && cd ../backend && go test ./...", "acceptanceCriteria": ["useArtifacts takes Context; matchesProject implements subproject/allowlist/catch-all", "containers use same matching", "version pills self-limit for subproject contexts", "fetchRepos appends ?subproject=", "npm run build passes"], "modelTier": "standard"}
```

---

## Manual verification (after all tasks, user-run)

With docker-compose running: "PPG Extras" entry → only v18 pill → only UBI_9 repo → the 13 extras packages listed; Container Images shows `percona-distribution-postgresql-with-postgis` with pull cmd `registry.opensuse.org/isv/percona/ppg/18/extras/containers/ubi9/images/...`. "PPG" v18 → extras packages gone from UBI_9 list, extras container gone; main containers still present. Deep link `?actx=ppg-extras&aver=18` restores the view.

## Self-Review

**Spec coverage:** §1 model → Task 2; §2 matching → Task 3; §3 pills → Task 3; §4 repos → Tasks 1+3; §5 URL key → Task 2; §6 untouched — no task modifies those files; future-subprojects requirement satisfied by the model (Task 2). ✓
**Placeholders:** Task 1 Step 1 uses an assertion-skeleton with explicit required assertions and an instruction to mirror the neighbouring tests' mechanics (the exact seed helper is file-local knowledge) — the three assertions are fully specified. Everything else carries complete code. ✓
**Type consistency:** `Context.subproject`/`allowedSubprojects` (Tasks 2, 3); `matchesProject(project, ver)` internal to Task 3; `contextToKey` (Task 2) matches useUrlState import of `Context`. ✓
