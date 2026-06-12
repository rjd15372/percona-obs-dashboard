# Context Switcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the separate PR board with a context dropdown in the ContextBar that lets users view any OBS project subtree (PPG or a specific PR) through the same full board UI.

**Architecture:** A `Context` object (`label`, `apiBase`, `prefix`) drives both API URLs and display. PPG uses existing `/api/products/` routes; PR contexts use new `/api/pr/{pr}/{subproject}/` routes. Context discovery runs client-side from the existing `/api/pr/packages` data. Version tabs are derived dynamically from package project paths using a positional approach (no hardcoded version list).

**Tech Stack:** Go 1.22, chi v5 router, Vue 3 + TypeScript, SQLite via existing store layer.

**User decisions (already made):**
- Remove the dedicated PR board (`PRBoard.vue`) from `App.vue`; main board is the single view for all contexts.
- PR `apiBase` is `/api/pr/pr-92/ppg` (no `PR:` in URL); backend reconstructs `isv:percona:PR:pr-92:ppg`.
- Dedicated backend routes for PR contexts (not patching the existing products handler).
- Context selector is a `<select>` when multiple contexts exist; plain `<code>` badge when only one.
- Version tabs derive from packages at `prefixDepth = selectedContext.prefix.split(':').length` — no hardcoded version list.

---

## File Map

| File | Change |
|------|--------|
| `backend/internal/api/handlers.go` | Add `prContextPackagesHandler`, `prContextEventsHandler`, extract `parseTimeWindow` helper |
| `backend/internal/api/server.go` | Register `/api/pr/{pr}/{subproject}/{version}/packages` and `/events` |
| `backend/internal/api/handlers_test.go` | Add tests for new PR context routes |
| `frontend/src/composables/usePackages.ts` | Replace `product` with `apiBase + prefixDepth`; dynamic `availableVersions`; updated `matchesVersion` |
| `frontend/src/composables/useEvents.ts` | Replace `product` with `apiBase`; updated fetch URL |
| `frontend/src/types/api.ts` | Add `Context` interface |
| `frontend/src/App.vue` | Add `selectedContext`, `contexts`, `prefixDepth`; remove `PRBoard`; pass new props to `ContextBar` |
| `frontend/src/components/ContextBar.vue` | Add `contexts`, `selectedContext`, `availableVersions` props; dropdown/badge; dynamic version tabs |

---

### Task 1: Backend — PR context package and event routes

**Goal:** Expose `/api/pr/{pr}/{subproject}/{version}/packages` and `/api/pr/{pr}/{subproject}/{version}/events` so the frontend can fetch packages and events for a specific PR+subproject context.

**Files:**
- Modify: `backend/internal/api/handlers.go`
- Modify: `backend/internal/api/server.go`
- Modify: `backend/internal/api/handlers_test.go`

**Acceptance Criteria:**
- [ ] `GET /api/pr/pr-92/ppg/17/packages` returns HTTP 200 with a JSON array
- [ ] `GET /api/pr/pr-92/ppg/17/events` returns HTTP 200 with a JSON array
- [ ] The handlers build the OBS prefix as `isv:percona:PR:pr-92:ppg` (not `isv:percona:pr-92:ppg`)
- [ ] All existing tests still pass: `cd backend && go test ./...`

**Verify:** `cd backend && go test ./internal/api/ -v -run TestPRContext` → all new tests PASS

**Steps:**

- [ ] **Step 1: Write failing tests for the new routes**

Add to `backend/internal/api/handlers_test.go`:

```go
func TestPRContextPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/packages", nil)
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

func TestPRContextEventsHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events", nil)
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

func TestPRContextEventsHandler_WindowParam(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events?window=60", nil)
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

func TestPRContextEventsHandler_InvalidWindow(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events?window=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd backend && go test ./internal/api/ -v -run TestPRContext
```

Expected: compile error or 404 responses — routes don't exist yet.

- [ ] **Step 3: Extract `parseTimeWindow` helper and add new handlers to `handlers.go`**

The `eventsHandler` and the new `prContextEventsHandler` share identical time-window parsing logic. Extract it first, then add both new handlers.

Replace the entire `backend/internal/api/handlers.go` with:

```go
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

// parseTimeWindow parses window/from/to query params and returns the time range.
// Defaults to the last 24 hours when no params are provided.
func parseTimeWindow(r *http.Request) (from, to time.Time, err error) {
	now := time.Now().UTC()
	if windowStr := r.URL.Query().Get("window"); windowStr != "" {
		windowMinutes, parseErr := strconv.Atoi(windowStr)
		if parseErr != nil || windowMinutes <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid window")
		}
		return now.Add(-time.Duration(windowMinutes) * time.Minute), now, nil
	}
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		toStr := r.URL.Query().Get("to")
		if toStr == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("to required")
		}
		const layout = "2006-01-02"
		parsedFrom, parseErr := time.Parse(layout, fromStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from")
		}
		parsedTo, parseErr := time.Parse(layout, toStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to")
		}
		return parsedFrom.UTC(), parsedTo.UTC().Add(24*time.Hour - time.Nanosecond), nil
	}
	return now.Add(-24 * time.Hour), now, nil
}

// packagesHandler returns a handler for GET /api/products/{product}/{version}/packages.
func packagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		prefix := "isv:percona:" + product

		pkgs, err := store.QueryPackages(db, prefix)
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

// eventsHandler returns a handler for GET /api/products/{product}/{version}/events.
func eventsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		prefix := "isv:percona:" + product

		from, to, err := parseTimeWindow(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		events, err := store.QueryEvents(db, prefix, from, to)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			return
		}
	}
}

// prContextPackagesHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/packages.
// Builds the OBS prefix as isv:percona:PR:{pr}:{subproject}.
func prContextPackagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")
		prefix := "isv:percona:PR:" + pr + ":" + subproject

		pkgs, err := store.QueryPackages(db, prefix)
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

// prContextEventsHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/events.
// Builds the OBS prefix as isv:percona:PR:{pr}:{subproject}.
func prContextEventsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")
		prefix := "isv:percona:PR:" + pr + ":" + subproject

		from, to, err := parseTimeWindow(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		events, err := store.QueryEvents(db, prefix, from, to)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			return
		}
	}
}

// PRGroup groups all packages under a single PR project number.
type PRGroup struct {
	PR          string            `json:"pr"`
	RollupState model.RollupState `json:"rollup_state"`
	Packages    []*model.Package  `json:"packages"`
}

// prPackagesHandler returns a handler for GET /api/pr/packages.
func prPackagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkgs, err := store.QueryPackages(db, "isv:percona:PR")
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		byPR := map[string][]*model.Package{}
		for _, p := range pkgs {
			pr := obs.PRNumber(p.Project)
			if pr == "" {
				continue
			}
			byPR[pr] = append(byPR[pr], p)
		}

		groups := make([]PRGroup, 0, len(byPR))
		for pr, packages := range byPR {
			rollup := worstRollup(packages)
			groups = append(groups, PRGroup{PR: pr, RollupState: rollup, Packages: packages})
		}
		sort.Slice(groups, func(i, j int) bool {
			ni, erri := strconv.Atoi(groups[i].PR)
			nj, errj := strconv.Atoi(groups[j].PR)
			if erri == nil && errj == nil {
				return ni > nj
			}
			return groups[i].PR > groups[j].PR
		})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(groups); err != nil {
			return
		}
	}
}

// worstRollup returns the worst RollupState across a slice of packages.
func worstRollup(pkgs []*model.Package) model.RollupState {
	worst := model.RollupSucceeded
	for _, p := range pkgs {
		if p.RollupState.Severity() > worst.Severity() {
			worst = p.RollupState
		}
	}
	return worst
}
```

Note: `parseTimeWindow` uses `fmt.Errorf` — the imports block in the code above already includes `"fmt"`. If you copy-pasted the handler file, verify `"fmt"` is present in the import list alongside `"database/sql"`, `"encoding/json"`, etc.

- [ ] **Step 4: Register the new routes in `server.go`**

Replace `backend/internal/api/server.go` with:

```go
package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter creates the chi router with all API routes registered.
func NewRouter(db *sql.DB) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db))
		r.Get("/events", eventsHandler(db))
	})

	r.Get("/api/pr/packages", prPackagesHandler(db))

	r.Route("/api/pr/{pr}/{subproject}/{version}", func(r chi.Router) {
		r.Get("/packages", prContextPackagesHandler(db))
		r.Get("/events", prContextEventsHandler(db))
	})

	return r
}
```

- [ ] **Step 5: Run all backend tests**

```bash
cd backend && go test ./...
```

Expected: all tests PASS including the four new `TestPRContext*` tests.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/handlers.go backend/internal/api/server.go backend/internal/api/handlers_test.go
git commit -s -m "feat(api): add PR context package and event routes"
```

---

### Task 2: Frontend composables — replace product with apiBase

**Goal:** Refactor `usePackages` and `useEvents` to accept an `apiBase` URL string instead of a product name, and add dynamic version discovery to `usePackages`.

**Files:**
- Modify: `frontend/src/composables/usePackages.ts`
- Modify: `frontend/src/composables/useEvents.ts`

**Acceptance Criteria:**
- [ ] `usePackages` accepts `apiBase: MaybeRef<string>` and `prefixDepth: MaybeRef<number>` instead of `product`
- [ ] `usePackages` exports `availableVersions: ComputedRef<string[]>` derived from package project paths at `prefixDepth`
- [ ] `matchesVersion` uses `availableVersions` (no hardcoded `['16','17','18']`)
- [ ] `useEvents` accepts `apiBase: MaybeRef<string>` instead of `product`
- [ ] `cd frontend && npx tsc --noEmit` produces no new errors beyond the pre-existing Vue SFC resolution error

**Verify:** `cd frontend && npx tsc --noEmit 2>&1 | grep -v "Cannot find module './App.vue'"` → no output (no errors)

**Steps:**

- [ ] **Step 1: Rewrite `usePackages.ts`**

Replace the entire contents of `frontend/src/composables/usePackages.ts`:

```typescript
import { ref, computed, toValue } from 'vue'
import type { MaybeRef, ComputedRef } from 'vue'
import type { Package } from '../types/api'

const SEVERITY: Record<string, number> = {
  broken: 5,
  unresolvable: 4,
  failed: 3,
  blocked: 2,
  building: 1,
  finished: 1,
  scheduled: 1,
  succeeded: 0,
}

// matchesVersion returns true if pkg belongs to the selected version.
// A package is a "common" package (always shown) when the segment at prefixDepth
// in its project path is absent or not a known version number.
function matchesVersion(
  pkg: Package,
  version: string,
  prefixDepth: number,
  knownVersions: Set<string>,
): boolean {
  const seg = pkg.project.split(':')[prefixDepth]
  if (!seg || !knownVersions.has(seg)) return true
  return seg === version
}

export function usePackages(
  apiBase: MaybeRef<string>,
  version: MaybeRef<string>,
  prefixDepth: MaybeRef<number>,
) {
  const data = ref<Package[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh() {
    const base = toValue(apiBase)
    const v = toValue(version)
    loading.value = true
    error.value = null
    try {
      const res = await fetch(`${base}/${v}/packages`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  // availableVersions: unique version segments found at prefixDepth in project paths,
  // sorted descending (newest first). Purely numeric segments are versions; anything
  // else (e.g. "common", "containers") is not.
  const availableVersions: ComputedRef<string[]> = computed(() => {
    const depth = toValue(prefixDepth)
    const found = new Set<string>()
    for (const pkg of data.value) {
      const seg = pkg.project.split(':')[depth]
      if (seg && /^\d+$/.test(seg)) found.add(seg)
    }
    return [...found].sort((a, b) => parseInt(b) - parseInt(a))
  })

  const sorted = computed(() => {
    const ver = toValue(version)
    const depth = toValue(prefixDepth)
    const knownVersions = new Set(availableVersions.value)
    return [...data.value]
      .filter(pkg => matchesVersion(pkg, ver, depth, knownVersions))
      .sort((a, b) => (SEVERITY[b.rollup_state] ?? 0) - (SEVERITY[a.rollup_state] ?? 0))
  })

  function filterByScope(scopes: string[]) {
    if (scopes.length === 0) return sorted.value
    return sorted.value.filter(p => scopes.includes(p.scope))
  }

  return { data: sorted, availableVersions, loading, error, refresh, filterByScope }
}
```

- [ ] **Step 2: Rewrite `useEvents.ts`**

Replace the entire contents of `frontend/src/composables/useEvents.ts`:

```typescript
import { ref, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Event } from '../types/api'

export function useEvents(apiBase: MaybeRef<string>, version: MaybeRef<string>) {
  const data = ref<Event[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh(opts: { window?: number; from?: string; to?: string } = {}) {
    const base = toValue(apiBase)
    const v = toValue(version)
    loading.value = true
    error.value = null
    try {
      let qs = ''
      if (opts.from && opts.to) {
        qs = `?from=${encodeURIComponent(opts.from)}&to=${encodeURIComponent(opts.to)}`
      } else {
        qs = `?window=${opts.window ?? 1440}`
      }
      const res = await fetch(`${base}/${v}/events${qs}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  return { data, loading, error, refresh }
}
```

- [ ] **Step 3: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | grep -v "Cannot find module './App.vue'"
```

Expected: no output (App.vue will be fixed in Task 3).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/composables/usePackages.ts frontend/src/composables/useEvents.ts
git commit -s -m "refactor(composables): replace product param with apiBase + dynamic version discovery"
```

---

### Task 3: Context switcher — types, App.vue, ContextBar

**Goal:** Wire up the context selector end-to-end: add the `Context` type, update `App.vue` to manage `selectedContext` and `contexts`, and update `ContextBar` to render a dropdown or badge and dynamic version tabs.

**Files:**
- Modify: `frontend/src/types/api.ts`
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/components/ContextBar.vue`

**Acceptance Criteria:**
- [ ] Default view shows PPG context — identical to current UI (version tabs, scope chips, health header, failure board, event log)
- [ ] When PR contexts exist in `/api/pr/packages`, a `<select>` appears in place of the `isv:percona:ppg` badge
- [ ] Selecting a PR context updates the board to show that PR's packages and events
- [ ] Version tabs show only versions present in the current context's packages; hidden when none
- [ ] Switching context resets `activeScopes` and updates `version` to the highest available
- [ ] `PRBoard` is no longer rendered
- [ ] `cd frontend && npx tsc --noEmit` produces no new errors beyond the pre-existing Vue SFC resolution error

**Verify:** `cd frontend && npx tsc --noEmit 2>&1 | grep -v "Cannot find module './App.vue'"` → no output

**Steps:**

- [ ] **Step 1: Add `Context` interface to `types/api.ts`**

Add at the top of `frontend/src/types/api.ts`, after the existing type aliases:

```typescript
export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg" or "/api/pr/pr-92/ppg"
  prefix: string   // e.g. "isv:percona:ppg" or "isv:percona:PR:pr-92:ppg"
}
```

- [ ] **Step 2: Rewrite `App.vue`**

Replace the entire `frontend/src/App.vue` with:

```vue
<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import ContextBar from './components/ContextBar.vue'
import HealthHeader from './components/HealthHeader.vue'
import MainGrid from './components/MainGrid.vue'
import type { Context } from './types/api'
import { usePackages } from './composables/usePackages'
import { useEvents } from './composables/useEvents'
import { usePRPackages } from './composables/usePRPackages'

// Theme
const theme = ref<'light' | 'dark'>('light')
watch(theme, (val) => {
  document.documentElement.setAttribute('data-theme', val === 'dark' ? 'dark' : '')
}, { immediate: true })

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
}

// Context
const DEFAULT_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
}
const selectedContext = ref<Context>(DEFAULT_CONTEXT)
const prefixDepth = computed(() => selectedContext.value.prefix.split(':').length)

// Navigation state
const version = ref('17')
const activeScopes = ref<string[]>([])

function toggleScope(scope: string) {
  if (scope === 'all') {
    activeScopes.value = []
    return
  }
  const idx = activeScopes.value.indexOf(scope)
  if (idx >= 0) {
    activeScopes.value = activeScopes.value.filter(s => s !== scope)
  } else {
    activeScopes.value = [...activeScopes.value, scope]
  }
}

function selectContext(ctx: Context) {
  selectedContext.value = ctx
  activeScopes.value = []
  // version is reset by the availableVersions watcher below
  refresh()
}

// Event window state
const windowMin = ref(1440)
const customFrom = ref<string | null>(null)
const customTo = ref<string | null>(null)

// Data fetching
const apiBase = computed(() => selectedContext.value.apiBase)
const { data: allPackages, availableVersions, refresh: refreshPackages, filterByScope } = usePackages(apiBase, version, prefixDepth)
const { data: events, refresh: refreshEvents } = useEvents(apiBase, version)
const { data: prGroups, refresh: refreshPR } = usePRPackages()

// Reset version to highest available when context changes or versions change
watch(availableVersions, (vers) => {
  if (vers.length > 0 && !vers.includes(version.value)) {
    version.value = vers[0]
  }
})

// Derive available contexts from PR groups data
const contexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
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
    const na = parseInt(a.prefix.split(':')[4]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[4]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [DEFAULT_CONTEXT, ...prContexts]
})

const filteredPackages = computed(() => filterByScope(activeScopes.value))
const updatedAt = ref<string | null>(null)

async function refresh() {
  await Promise.all([
    refreshPackages(),
    refreshEvents(
      windowMin.value === -1 && customFrom.value && customTo.value
        ? { from: customFrom.value, to: customTo.value }
        : { window: windowMin.value }
    ),
    refreshPR(),
  ])
  updatedAt.value = new Date().toISOString()
}

// Initial fetch + 5-min auto-refresh
onMounted(() => { refresh() })
const timer = setInterval(refresh, 5 * 60 * 1000)
onUnmounted(() => clearInterval(timer))

// Re-fetch on version change
watch(version, () => refresh())

// Re-fetch on window change
watch([windowMin, customFrom, customTo], () => refresh())
</script>

<template>
  <div class="min-h-screen bg-bg-app" style="padding: 24px 28px 60px;">
    <div style="max-width: 1360px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px;">
      <AppHeader :theme="theme" @toggle-theme="toggleTheme" />
      <ContextBar
        :version="version"
        :updated-at="updatedAt"
        :active-scopes="activeScopes"
        :contexts="contexts"
        :selected-context="selectedContext"
        :available-versions="availableVersions"
        @update:version="version = $event"
        @toggle-scope="toggleScope"
        @update:context="selectContext"
      />
      <HealthHeader :packages="allPackages" />
      <MainGrid
        :packages="filteredPackages"
        :events="events"
        :window-min="windowMin"
        :custom-from="customFrom"
        :custom-to="customTo"
        @update:window-min="windowMin = $event"
        @update:custom-from="customFrom = $event"
        @update:custom-to="customTo = $event"
      />
    </div>
  </div>
</template>
```

- [ ] **Step 3: Rewrite `ContextBar.vue`**

Replace the entire `frontend/src/components/ContextBar.vue` with:

```vue
<script setup lang="ts">
import type { Context } from '../types/api'

defineProps<{
  version: string
  updatedAt: string | null
  activeScopes: string[]
  contexts: Context[]
  selectedContext: Context
  availableVersions: string[]
}>()

const emit = defineEmits<{
  'update:version': [version: string]
  'toggle-scope': [scope: string]
  'update:context': [ctx: Context]
}>()

const SCOPES = [
  { id: 'all', label: 'All' },
  { id: 'common', label: 'Common' },
  { id: 'ppgcommon', label: 'PPG Common' },
  { id: 'version', label: 'Version' },
  { id: 'container', label: 'Container' },
  { id: 'release', label: 'Release' },
]

function formatTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return `${time} · ${isToday ? 'today' : d.toLocaleDateString()}`
}

function tabStyle(v: string, selected: string): string {
  const active = v === selected
  return active
    ? 'background: var(--bg-card); color: var(--text-primary); font-weight: 700; padding: 4px 12px; border-radius: 7px; border: none; font-size: 13px; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-muted); font-weight: 500; padding: 4px 12px; border-radius: 7px; border: none; font-size: 13px; cursor: pointer; font-family: inherit;'
}

function scopeStyle(id: string, active: boolean): string {
  return active
    ? 'background: var(--brand-purple); color: #fff; padding: 4px 11px; border-radius: 8px; border: none; font-size: 11.5px; font-weight: 600; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-secondary); padding: 4px 11px; border-radius: 8px; border: 1px solid var(--border); font-size: 11.5px; font-weight: 500; cursor: pointer; font-family: inherit;'
}

function onContextChange(e: Event) {
  // intentional cast: target is a <select> element
  const apiBase = (e.target as HTMLSelectElement).value
  const ctx = (e as any).contexts?.find((c: Context) => c.apiBase === apiBase)
  emit('update:context', ctx)
}
</script>

<template>
  <div style="background: var(--bg-card); border: 1px solid var(--border); border-radius: 14px; padding: 14px 18px; display: flex; flex-direction: column; gap: 13px;">
    <!-- Top row: tech badge + context selector + version tabs + updated -->
    <div style="display: flex; align-items: center; gap: 16px; flex-wrap: wrap;">
      <span style="display: inline-flex; align-items: center; gap: 7px; padding: 5px 12px; border-radius: 8px; background: var(--tint-postgres); color: var(--tech-postgres); font-size: 12px; font-weight: 700; border: 1px solid rgba(0,94,214,0.15);">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style="flex-shrink:0;">
          <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z" fill="currentColor" opacity="0.15"/>
          <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8z" fill="currentColor"/>
          <text x="7" y="16" font-size="10" font-weight="800" fill="currentColor" font-family="monospace">pg</text>
        </svg>
        PostgreSQL
      </span>

      <!-- Context selector: dropdown when multiple contexts exist, plain badge otherwise -->
      <select
        v-if="contexts.length > 1"
        :value="selectedContext.apiBase"
        @change="e => { const apiBase = (e.target as HTMLSelectElement).value; const ctx = contexts.find(c => c.apiBase === apiBase); if (ctx) emit('update:context', ctx) }"
        style="font-family: var(--font-mono); font-size: 12.5px; color: var(--text-secondary); background: var(--bg-muted); padding: 5px 10px; border-radius: 7px; border: 1px solid var(--border); cursor: pointer;"
      >
        <option v-for="ctx in contexts" :key="ctx.apiBase" :value="ctx.apiBase">{{ ctx.prefix }}</option>
      </select>
      <code
        v-else
        style="font-family: var(--font-mono); font-size: 12.5px; color: var(--text-secondary); background: var(--bg-muted); padding: 5px 10px; border-radius: 7px;"
      >{{ selectedContext.prefix }}</code>

      <!-- Version tabs: hidden when no versioned packages exist in the context -->
      <div v-if="availableVersions.length > 0" style="display: flex; align-items: center; gap: 6px;">
        <span style="font-size: 11px; color: var(--text-muted); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; margin-right: 2px;">Version</span>
        <div style="display: flex; gap: 3px; background: var(--bg-muted); padding: 3px; border-radius: 9px;">
          <button
            v-for="v in availableVersions"
            :key="v"
            @click="emit('update:version', v)"
            :style="tabStyle(v, version)"
          >{{ v }}</button>
        </div>
      </div>

      <div style="margin-left: auto; display: flex; align-items: center; gap: 16px; font-size: 12px; color: var(--text-muted);">
        <span>Updated <strong style="color: var(--text-secondary); font-weight: 600;">{{ formatTime(updatedAt) }}</strong></span>
        <span style="display: inline-flex; align-items: center; gap: 6px;">
          <span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Auto-refresh 5 min
        </span>
      </div>
    </div>

    <!-- Scope chips -->
    <div style="display: flex; align-items: center; gap: 9px; flex-wrap: wrap; border-top: 1px solid var(--border); padding-top: 12px;">
      <span style="font-size: 11px; color: var(--text-muted); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; margin-right: 2px;">Scope</span>
      <button
        v-for="s in SCOPES"
        :key="s.id"
        @click="s.id === 'all' ? emit('toggle-scope', 'all') : emit('toggle-scope', s.id)"
        :style="scopeStyle(s.id, s.id === 'all' ? activeScopes.length === 0 : activeScopes.includes(s.id))"
      >{{ s.label }}</button>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | grep -v "Cannot find module './App.vue'"
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types/api.ts frontend/src/App.vue frontend/src/components/ContextBar.vue
git commit -s -m "feat(frontend): context switcher — dropdown, dynamic versions, remove PR board"
```
