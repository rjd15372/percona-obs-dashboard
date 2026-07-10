# Three-Tier OBS Structure Adaptation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adapt the dashboard to the migrated three-tier OBS layout (`ppg:devel:<V>` / `ppg:staging:<V>` / `ppg:releases:<V>`): explicit tier API routes, Devel/Staging selector contexts, subprojects as version-selector extensions (`17:extras`, `16:tde`), and correct Overview grouping/sections.

**Architecture:** Four independent changes: (1) backend products routes gain a validated `{tier}` segment and `QueryBuildPackages` a tier param; (2) the Overview's `logicalProject` learns the tier shapes, generic subproject rows, and a legacy→staging mapping; (3) the frontend replaces the PPG/PPG-Extras contexts with Devel/Staging and a shared version-extension model (`lib/versions.ts`) used by board, artifacts, and events filtering; (4) Overview categories gain a Staging section.

**Tech Stack:** Go (chi, database/sql/SQLite) backend; Vue 3 `<script setup>` + TS + Tailwind frontend (no test runner — `npm run build` = vue-tsc + vite build is the verification).

**Spec:** `docs/superpowers/specs/2026-07-10-three-tier-adaptation-design.md`

**User decisions (already made):**
- Approach B: explicit tier route `/api/products/{product}/{tier}/{version}` with `tier ∈ {devel, staging}` validated (else 400); old route removed (migration is complete, frontend+backend deploy together).
- Subprojects are **version extensions**, not contexts: version selector shows `18, 18:extras, 17, 17:extras, 16, 16:extras, 16:tde, …` (numeric descending, plain first, extensions alphabetical); the `PPG Extras` context is deleted; `containers` stays absorbed into the plain version.
- Plain version excludes extras on the board (deliberate fix of today's leak).
- Common packages (`ppg:common`, `common`) appear in **both** devel and staging board views.
- Contexts in pipeline order (Devel · Staging [+ Releases on artifacts] + PRs); **default context Staging**.
- Overview sections: **Devel · Staging · Releases · PRs**; legacy `ppg:<V>` rows map onto `ppg:staging:<V>` (renamed continuation).
- Work directly on `main`; commits always `git commit -s`, never a Co-Authored-By trailer.

---

## File Structure

| File | Change | Task |
|---|---|---|
| `backend/internal/api/server.go` | products route gains `{tier}` | 1 |
| `backend/internal/api/handlers.go` | tier validation helper; packages/events/repos handlers | 1 |
| `backend/internal/store/packages.go` | `QueryBuildPackages` tier param | 1 |
| `backend/internal/store/packages_test.go` | tier-aware test | 1 |
| `backend/internal/api/handlers_test.go` | new-route URLs + tier-validation test | 1 |
| `backend/internal/api/overview.go` | `logicalProject` tier branches + `tierRow` helper | 2 |
| `backend/internal/api/overview_test.go` | new table | 2 |
| `frontend/src/lib/contexts.ts` | Devel/Staging contexts; extras deleted | 3 |
| `frontend/src/types/api.ts` | `Context.subproject` removed; comments | 3 |
| `frontend/src/composables/useUrlState.ts` | `contextToKey` simplification | 3 |
| `frontend/src/lib/versions.ts` | **create** — version-key derivation + matching | 3 |
| `frontend/src/composables/usePackages.ts` | context-aware versions + matching | 3 |
| `frontend/src/composables/useEvents.ts` | extension-aware event filtering | 3 |
| `frontend/src/composables/useArtifacts.ts` | `matchesProject` via shared matcher | 3 |
| `frontend/src/components/ArtifactsPanel.vue` | shared derivation; repos fetch via apiBase + subproject | 3 |
| `frontend/src/App.vue` | context lists, defaults, call-site updates | 3 |
| `frontend/src/lib/overview.ts` | Staging category | 4 |

Dependencies: Tasks 1, 2, 3 are mutually independent (disjoint files). Task 4 is blocked by Task 3 only to serialize `npm run build` runs in the shared checkout.

---

### Task 1: Backend — tier routes and `QueryBuildPackages`

**Goal:** Products endpoints move to `/api/products/{product}/{tier}/{version}/…` with `tier ∈ {devel, staging}` validated (400 otherwise), and `QueryBuildPackages` scopes to `root:product:tier[:version]` while keeping `root:product:common` + `root:common` in versioned views.

**Files:**
- Modify: `backend/internal/api/server.go:25-29` (route)
- Modify: `backend/internal/api/handlers.go` (`packagesHandler`, `eventsHandler`, `reposHandler`, new `productTier` helper)
- Modify: `backend/internal/store/packages.go:520-552` (`QueryBuildPackages`)
- Test: `backend/internal/store/packages_test.go:247-282`, `backend/internal/api/handlers_test.go` (three URL updates + new tier-validation test)

**Acceptance Criteria:**
- [ ] `GET /api/products/ppg/staging/17/packages` returns packages under `isv:percona:ppg:staging:17` (incl. subproject subtrees), `isv:percona:ppg:common`, and `isv:percona:common`; devel and releases packages excluded
- [ ] `GET /api/products/ppg/bogus/17/{packages,events,repos}` → 400 with body containing "tier must be devel or staging"
- [ ] `QueryBuildPackages(db, root, product, tier, version)` — version `_`/empty scopes to `root:product:tier`; versioned adds `root:product:common` and `root:common`
- [ ] `reposHandler` keeps `?subproject=` validation and appends it to the prefix; `eventsHandler` prefix is `isv:percona:<product>:<tier>`
- [ ] Old two-segment route removed; no other route changed

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `backend/internal/store/packages_test.go`, replace `TestQueryBuildPackages` (lines 247-282) with:

```go
func TestQueryBuildPackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name string) {
		db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, '[]', ?)`, project, name, now)
	}
	insert("isv:percona:ppg:staging:17", "pg_tde")
	insert("isv:percona:ppg:staging:17:containers:ubi9", "pg_container")
	insert("isv:percona:ppg:staging:17:extras", "extras_pkg")
	insert("isv:percona:ppg:devel:17", "devel_pkg")
	insert("isv:percona:ppg:common", "common_pkg")
	insert("isv:percona:common", "global_common")
	insert("isv:percona:ppg:releases:17", "release_pkg")

	pkgs, err := QueryBuildPackages(db, "isv:percona", "ppg", "staging", "17")
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, p := range pkgs {
		names[p.Name] = true
	}
	// The staging:17 subtree (incl. subprojects — the client filters extras
	// into its own version entry) plus both shared common projects.
	for _, want := range []string{"pg_tde", "pg_container", "extras_pkg", "common_pkg", "global_common"} {
		if !names[want] {
			t.Errorf("missing expected package %q", want)
		}
	}
	for _, unwanted := range []string{"devel_pkg", "release_pkg"} {
		if names[unwanted] {
			t.Errorf("%s should not appear in staging build packages", unwanted)
		}
	}

	// Version-less fetch scopes to the whole tier.
	all, err := QueryBuildPackages(db, "isv:percona", "ppg", "devel", "_")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 { // devel_pkg + global_common (no ppg:common in the "_" branch, as before)
		t.Fatalf("devel _ fetch: expected 2 packages, got %d", len(all))
	}
}
```

In `backend/internal/api/handlers_test.go`, update the three existing request URLs:
- `TestPackagesHandler_EmptyDB`: `/api/products/ppg/17/packages` → `/api/products/ppg/staging/17/packages`
- `TestEventsHandler_WindowParam`: `/api/products/ppg/17/events?window=1440` → `/api/products/ppg/staging/17/events?window=1440`
- `TestEventsHandler_DateRangeParam`: `/api/products/ppg/17/events?from=2026-01-01&to=2026-12-31` → `/api/products/ppg/staging/17/events?from=2026-01-01&to=2026-12-31`

And append:

```go
func TestProductsTierValidation(t *testing.T) {
	router := setupTestServer(t)
	for _, path := range []string{
		"/api/products/ppg/bogus/17/packages",
		"/api/products/ppg/bogus/17/events",
		"/api/products/ppg/bogus/17/repos",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "tier must be devel or staging") {
			t.Fatalf("%s: unexpected body %q", path, rec.Body.String())
		}
	}
}

func TestProductsTierRoutes(t *testing.T) {
	router := setupTestServer(t)
	for _, path := range []string{
		"/api/products/ppg/devel/18/packages",
		"/api/products/ppg/staging/17/repos",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/store/ ./internal/api/ -run 'TestQueryBuildPackages|TestProductsTier' -v`
Expected: compile FAIL in store (`too many arguments to QueryBuildPackages`) / 404s in api.

- [ ] **Step 3: Implement**

`backend/internal/store/packages.go` — replace `QueryBuildPackages`:

```go
// QueryBuildPackages returns build packages for one product tier
// (root:product:tier[:version] subtree, subprojects included — the client
// splits them into version-extension entries) plus the shared common
// projects: root:product:common and root:common appear in both devel and
// staging views. The version-less branch scopes to the whole tier and keeps
// only root:common, as before.
func QueryBuildPackages(db *sql.DB, root, product, tier, version string) ([]*model.Package, error) {
	gp := root + ":common"
	var rows *sql.Rows
	var err error
	if version == "_" || version == "" {
		pp := root + ":" + product + ":" + tier
		rows, err = db.Query(`SELECT`+packageSelectCols+`
			FROM packages
			WHERE is_release = 0
			  AND (  (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%') )
			ORDER BY project, name`,
			pp, pp, gp, gp,
		)
	} else {
		vp := root + ":" + product + ":" + tier + ":" + version
		cp := root + ":" + product + ":common"
		rows, err = db.Query(`SELECT`+packageSelectCols+`
			FROM packages
			WHERE is_release = 0
			  AND (  (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%') )
			ORDER BY project, name`,
			vp, vp, cp, cp, gp, gp,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
```

`backend/internal/api/handlers.go` — add the helper (above `packagesHandler`) and update the three handlers:

```go
// productTier extracts and validates the {tier} URL param. The three-tier
// layout has exactly two building tiers; anything else is a client error
// (and must not reach the SQL LIKE prefixes).
func productTier(w http.ResponseWriter, r *http.Request) (string, bool) {
	tier := chi.URLParam(r, "tier")
	if tier != "devel" && tier != "staging" {
		http.Error(w, "tier must be devel or staging", http.StatusBadRequest)
		return "", false
	}
	return tier, true
}

// packagesHandler returns a handler for GET /api/products/{product}/{tier}/{version}/packages.
func packagesHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		tier, ok := productTier(w, r)
		if !ok {
			return
		}
		version := chi.URLParam(r, "version")

		pkgs, err := store.QueryBuildPackages(db, root, product, tier, version)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if err := store.AttachCveScans(db, pkgs); err != nil {
			slog.Warn("api: attach cve scans", "err", err)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			return
		}
	}
}
```

`eventsHandler` — update the doc comment's route shape to `GET /api/products/{product}/{tier}/{version}/events` and the body's opening lines to:

```go
		product := chi.URLParam(r, "product")
		tier, ok := productTier(w, r)
		if !ok {
			return
		}
		prefix := "isv:percona:" + product + ":" + tier
```

(rest unchanged.)

`reposHandler` — update the doc comment's route shape and the function to:

```go
func reposHandler(db *sql.DB) http.HandlerFunc {
	inner := reposHandlerWithPrefix(db, func(r *http.Request) string {
		prefix := "isv:percona:" + chi.URLParam(r, "product") + ":" + chi.URLParam(r, "tier") + ":" + chi.URLParam(r, "version")
		if sub := r.URL.Query().Get("subproject"); sub != "" {
			prefix += ":" + sub
		}
		return prefix
	})
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := productTier(w, r); !ok {
			return
		}
		if sub := r.URL.Query().Get("subproject"); sub != "" && !subprojectRe.MatchString(sub) {
			http.Error(w, "invalid subproject", http.StatusBadRequest)
			return
		}
		inner(w, r)
	}
}
```

`backend/internal/api/server.go:25` — the route becomes:

```go
	r.Route("/api/products/{product}/{tier}/{version}", func(r chi.Router) {
		r.Get("/packages", packagesHandler(db, root))
		r.Get("/events", eventsHandler(db))
		r.Get("/repos", reposHandler(db))
	})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/store/ ./internal/api/ -run 'TestQueryBuildPackages|TestProductsTier|TestPackagesHandler|TestEventsHandler' -v` → PASS. Then `go test ./...` → all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/api/server.go backend/internal/api/handlers.go backend/internal/api/handlers_test.go backend/internal/store/packages.go backend/internal/store/packages_test.go
git commit -s -m "feat(api): explicit tier segment in products routes for three-tier layout"
```

---

### Task 2: Backend — `logicalProject` tier shapes

**Goal:** The Overview groups `ppg:devel:<V>` / `ppg:staging:<V>` as per-tier version rows (containers absorbed, any other direct subproject its own row) and maps the legacy `ppg:<V>` shape onto the staging row.

**Files:**
- Modify: `backend/internal/api/overview.go:16-51` (`logicalProject` + new `tierRow` helper)
- Test: `backend/internal/api/overview_test.go:14-35` (`TestLogicalProject` table)

**Acceptance Criteria:**
- [ ] `ppg:staging:17[:containers:*]` → `root:ppg:staging:17`; `ppg:staging:16:extras[:…]` → `root:ppg:staging:16:extras`; `ppg:staging:16:tde` → its own row (generic — no subproject allowlist)
- [ ] Same for `devel`; bare `ppg:devel`/`ppg:staging` → `""`
- [ ] Legacy `ppg:17[:containers:*]` → `root:ppg:staging:17`; `ppg:16:extras` → `root:ppg:staging:16:extras`
- [ ] `ppg:common`, `common`, `releases`, PR shapes unchanged

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/api/ -run TestLogicalProject -v` → PASS; `go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Rewrite the test table (failing first)**

Replace the `cases` slice in `TestLogicalProject` (`backend/internal/api/overview_test.go:16-29`) with:

```go
	cases := []struct{ project, want string }{
		// three-tier shapes
		{"isv:percona:ppg:staging:17", "isv:percona:ppg:staging:17"},
		{"isv:percona:ppg:staging:17:containers:ubi9", "isv:percona:ppg:staging:17"},
		{"isv:percona:ppg:staging:16:extras", "isv:percona:ppg:staging:16:extras"},
		{"isv:percona:ppg:staging:16:extras:containers:ubi9", "isv:percona:ppg:staging:16:extras"},
		{"isv:percona:ppg:staging:16:tde", "isv:percona:ppg:staging:16:tde"},
		{"isv:percona:ppg:devel:18", "isv:percona:ppg:devel:18"},
		{"isv:percona:ppg:devel:18:containers:ubi9", "isv:percona:ppg:devel:18"},
		{"isv:percona:ppg:devel", ""},
		{"isv:percona:ppg:staging", ""},
		// legacy two-tier shapes map onto staging (renamed continuation):
		// pre-migration duration/event rows inside the stats windows merge
		// into the staging rows instead of rendering ghost sections.
		{"isv:percona:ppg:17", "isv:percona:ppg:staging:17"},
		{"isv:percona:ppg:17:containers:ubi9", "isv:percona:ppg:staging:17"},
		{"isv:percona:ppg:16:extras", "isv:percona:ppg:staging:16:extras"},
		// unchanged shapes
		{"isv:percona:ppg:common", "isv:percona:ppg:common"},
		{"isv:percona:ppg:common:deps", "isv:percona:ppg:common"},
		{"isv:percona:common:containers:ubi8", "isv:percona:common"},
		{"isv:percona:ppg:releases:17:containers:ubi9", "isv:percona:ppg:releases"},
		{"isv:percona:PR:pr-124:ppg:staging:16:extras", "isv:percona:PR:pr-124"},
		{"isv:percona:PR:pr-33:ppg:18:containers:ubi9", "isv:percona:PR:pr-33"},
		{"isv:other:ppg:17", ""},
		{"isv:percona:ppg", ""},
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/api/ -run TestLogicalProject -v`
Expected: FAIL (staging shapes collapse to `isv:percona:ppg:staging`, legacy shapes keep their old names).

- [ ] **Step 3: Implement**

Replace `logicalProject` in `backend/internal/api/overview.go` with:

```go
// logicalProject maps a raw OBS project to the Overview row it belongs to:
// tier version roots (ppg:devel:<V>, ppg:staging:<V>) absorb their
// :containers:* subprojects; any other direct subproject (extras, tde, …)
// is its own row (absorbing its subtree) — matching the version selector's
// <V>:<sub> granularity. The common trees, the releases tree, and each PR
// collapse to one row each. The legacy two-tier shape ppg:<V> maps onto the
// staging row — staging is the renamed continuation of ppg:<V>, so
// pre-migration duration/event rows still inside the stats windows merge
// into the staging rows instead of rendering ghost sections. Unknown shapes
// return "" (excluded).
func logicalProject(root, project string) string {
	prefix := root + ":"
	if !strings.HasPrefix(project, prefix) {
		return ""
	}
	rel := strings.Split(project[len(prefix):], ":")
	switch rel[0] {
	case "PR":
		if len(rel) >= 2 {
			return root + ":PR:" + rel[1]
		}
		return ""
	case "common":
		return root + ":common"
	case "ppg":
		if len(rel) < 2 {
			return ""
		}
		switch rel[1] {
		case "common":
			return root + ":ppg:common"
		case "releases":
			return root + ":ppg:releases"
		case "devel", "staging":
			if len(rel) < 3 {
				return ""
			}
			return tierRow(root, rel[1], rel[2], rel[3:])
		default:
			// Legacy two-tier shape: ppg:<V>[:<sub>…] → the staging row.
			return tierRow(root, "staging", rel[1], rel[2:])
		}
	}
	return ""
}

// tierRow builds the Overview row name for a tier version root and its
// subproject tail: containers are absorbed into the version row; any other
// direct subproject gets its own row.
func tierRow(root, tier, version string, sub []string) string {
	row := root + ":ppg:" + tier + ":" + version
	if len(sub) > 0 && sub[0] != "containers" {
		row += ":" + sub[0]
	}
	return row
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/api/ -run TestLogicalProject -v` → PASS. Then `go test ./...` → all PASS (other overview tests use shapes covered by the legacy mapping; if `TestOverviewSnapshotBuilder` asserts old row names, update its expected row names to the staging-mapped equivalents — the data seeding stays unchanged).

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/api/overview.go backend/internal/api/overview_test.go
git commit -s -m "feat(overview): group three-tier projects per tier version with legacy mapping"
```

---

### Task 3: Frontend — Devel/Staging contexts and the version-extension model

**Goal:** Selectors offer PPG Devel and PPG Staging (default Staging); subprojects appear as version-selector extensions (`17:extras`) with consistent matching across board, artifacts, and events; the PPG Extras context and `Context.subproject` are removed.

**Files:**
- Create: `frontend/src/lib/versions.ts`
- Modify: `frontend/src/lib/contexts.ts`, `frontend/src/types/api.ts:4-13`, `frontend/src/composables/useUrlState.ts:6-14`, `frontend/src/composables/usePackages.ts`, `frontend/src/composables/useEvents.ts`, `frontend/src/composables/useArtifacts.ts:88-99`, `frontend/src/components/ArtifactsPanel.vue` (availableVersions + fetchRepos), `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] Board contexts `[PPG Devel, PPG Staging, …PRs]`, artifacts `[PPG Devel, PPG Staging, Releases, …PRs]`; both defaults are PPG Staging; URL keys `devel`/`staging`
- [ ] `deriveVersionKeys` produces plain versions (numeric desc) each followed by its extensions alphabetically; `containers` absorbed; catch-all contexts (PR/Releases, `allowedSubprojects` undefined) yield plain keys only
- [ ] Selecting `17` on board/artifacts/events matches version root + absorbed subprojects only (extras no longer leaks in); selecting `17:extras` matches that subtree (containers beneath included); common packages/events still always shown on the board
- [ ] Repos fetch uses `${ctx.apiBase}/<plainVersion>/repos` (+ `?subproject=<sub>` for extension keys) — the hardcoded `/api/products/ppg/` is gone
- [ ] `Context.subproject` removed from the type and all usages; `npm run build` exits 0

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npm run build` → exits 0

**Steps:**

- [ ] **Step 1: Create `frontend/src/lib/versions.ts`**

```ts
// Version-key model for the <version>[:<subproject>] selector: subprojects
// of a version (extras, tde, …) are "version extensions" with their own
// selector entries, while absorbed subprojects (containers) fold into the
// plain version entry. Shared by the board (usePackages), the artifacts
// panel, and the events filter so all three scope identically.

/** Split "17:extras" into ["17", "extras"]; "17" into ["17", undefined]. */
export function splitVersionKey(key: string): [string, string | undefined] {
  const idx = key.indexOf(':')
  return idx < 0 ? [key, undefined] : [key.slice(0, idx), key.slice(idx + 1)]
}

/** Derive selector keys from project paths. depth is the context prefix
 *  segment count; absorbed lists the subprojects folded into the plain
 *  version entry (undefined = catch-all context: plain numeric keys only).
 *  Order: numeric descending, plain key first, extensions alphabetical. */
export function deriveVersionKeys(
  projects: Iterable<string>,
  depth: number,
  absorbed: string[] | undefined,
): string[] {
  const plain = new Set<string>()
  const extensions = new Set<string>()
  for (const project of projects) {
    const parts = project.split(':')
    const ver = parts[depth]
    if (!ver || !/^\d+$/.test(ver)) continue
    const sub = parts[depth + 1]
    if (!sub || absorbed === undefined || absorbed.includes(sub)) {
      plain.add(ver)
    } else {
      extensions.add(`${ver}:${sub}`)
    }
  }
  const versions = new Set<string>(plain)
  for (const ext of extensions) versions.add(splitVersionKey(ext)[0])
  const keys: string[] = []
  for (const ver of [...versions].sort((a, b) => parseInt(b) - parseInt(a))) {
    if (plain.has(ver)) keys.push(ver)
    keys.push(...[...extensions].filter(e => e.startsWith(ver + ':')).sort())
  }
  return keys
}

/** Does a project belong under prefix at the selected version key?
 *  Plain key "17": the version root plus absorbed subprojects only (when
 *  absorbed is defined; catch-all contexts match the whole subtree).
 *  Extension key "17:extras": the prefix:17:extras subtree (catch-all,
 *  containers beneath included). */
export function matchesVersionKey(
  project: string,
  prefix: string,
  key: string,
  absorbed: string[] | undefined,
): boolean {
  const [ver, sub] = splitVersionKey(key)
  const base = sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
  if (project === base) return true
  if (!project.startsWith(base + ':')) return false
  if (sub || absorbed === undefined) return true
  const first = project.slice(base.length + 1).split(':')[0]
  return absorbed.includes(first)
}
```

- [ ] **Step 2: Rewrite `frontend/src/lib/contexts.ts`**

```ts
import type { Context } from '../types/api'

export const PPG_DEVEL_CONTEXT: Context = {
  label: 'PPG Devel',
  apiBase: '/api/products/ppg/devel',
  prefix: 'isv:percona:ppg:devel',
  // Subprojects absorbed into the plain version entry; every other
  // subproject (extras, tde, …) surfaces as a <version>:<sub> entry in
  // the version selector.
  allowedSubprojects: ['containers'],
}

export const PPG_STAGING_CONTEXT: Context = {
  label: 'PPG Staging',
  apiBase: '/api/products/ppg/staging',
  prefix: 'isv:percona:ppg:staging',
  allowedSubprojects: ['containers'],
}

export const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}
```

- [ ] **Step 3: Update the `Context` type (`frontend/src/types/api.ts:4-13`)**

```ts
export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg/staging" or "/api/pr/pr-92"
  prefix: string   // e.g. "isv:percona:ppg:staging" or "isv:percona:PR:pr-92"
  /** Direct subprojects of prefix:ver absorbed into the plain version entry
   *  (e.g. "containers"); all others become <ver>:<sub> version-extension
   *  entries. Undefined = catch-all (PR/Releases contexts). */
  allowedSubprojects?: string[]
}
```

- [ ] **Step 4: Simplify `contextToKey` (`frontend/src/composables/useUrlState.ts:6-14`)**

```ts
export function contextToKey(ctx: Context): string {
  const parts = ctx.prefix.split(':')
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0) {
    return parts[prIdx + 1] // e.g. "pr-106"
  }
  return parts[parts.length - 1] // "devel" | "staging" | "releases"
}
```

- [ ] **Step 5: Rework `frontend/src/composables/usePackages.ts`**

Replace the file's version logic (keep `SEVERITY`, fetching, `filterByTags` as-is):

```ts
import { ref, computed, toValue } from 'vue'
import type { MaybeRef, ComputedRef } from 'vue'
import type { Context, Package } from '../types/api'
import { deriveVersionKeys, matchesVersionKey } from '../lib/versions'
```

Signature: `export function usePackages(apiBase: MaybeRef<string>, version: MaybeRef<string>, context: MaybeRef<Context>)` (the `prefixDepth` param is replaced by the context). Then:

```ts
  // availableVersions: version keys (plain + extensions) derived from the
  // fetched corpus at the context's prefix depth.
  const availableVersions: ComputedRef<string[]> = computed(() => {
    const ctx = toValue(context)
    return deriveVersionKeys(
      data.value.map(p => p.project),
      ctx.prefix.split(':').length,
      ctx.allowedSubprojects,
    )
  })

  const sorted = computed(() => {
    const ver = toValue(version)
    const ctx = toValue(context)
    const depth = ctx.prefix.split(':').length
    return [...data.value]
      .filter(pkg => {
        if (pkg.is_release) return false
        if (!ver) return true
        const seg = pkg.project.split(':')[depth]
        // Common packages (non-numeric segment at depth) are always shown.
        if (!seg || !/^\d+$/.test(seg)) return true
        return matchesVersionKey(pkg.project, ctx.prefix, ver, ctx.allowedSubprojects)
      })
      .sort((a, b) => (SEVERITY[b.rollup_state] ?? 0) - (SEVERITY[a.rollup_state] ?? 0))
  })
```

Delete the old `matchesVersion` function.

- [ ] **Step 6: Rework the events filter (`frontend/src/composables/useEvents.ts`)**

Add imports: `import type { Context, Event } from '../types/api'` and `import { matchesVersionKey } from '../lib/versions'`. Replace `matchesEventVersion` and `filterEvents`:

```ts
  function matchesEventVersion(event: Event, key: string, ctx: Context): boolean {
    if (!key) return true
    const seg = event.project.split(':')[ctx.prefix.split(':').length]
    // Non-numeric segment (common, project events) always passes.
    if (!seg || !/^\d+$/.test(seg)) return true
    return matchesVersionKey(event.project, ctx.prefix, key, ctx.allowedSubprojects)
  }

  function filterEvents(tags: string[], version: string, ctx: Context): Event[] {
    return data.value.filter(e => {
      if (!matchesContext(e.project, ctx.prefix)) return false
      if (tags.length > 0 && !tags.every(t => (e.tags ?? []).includes(t))) return false
      return matchesEventVersion(e, version, ctx)
    })
  }
```

(`matchesContext` and `refresh` stay unchanged — the events URL's version segment is ignored server-side, so an extension key in the path is harmless.)

- [ ] **Step 7: Rework `matchesProject` (`frontend/src/composables/useArtifacts.ts:88-99`)**

Add `import { matchesVersionKey } from '../lib/versions'` and replace the function (keeping its doc comment updated):

```ts
  // matchesProject: does an OBS project belong to this context at the
  // selected version key? Plain keys own the version root + absorbed
  // subprojects; extension keys ("17:extras") own that subtree; contexts
  // without allowedSubprojects (PR, Releases) keep the historical catch-all.
  const matchesProject = (project: string, ver: string): boolean => {
    const ctx = toValue(context)
    return matchesVersionKey(project, ctx.prefix, ver, ctx.allowedSubprojects)
  }
```

- [ ] **Step 8: Update `frontend/src/components/ArtifactsPanel.vue`**

`availableVersions` (lines 70-83) becomes:

```ts
import { deriveVersionKeys, splitVersionKey } from '../lib/versions'

const availableVersions = computed<string[]>(() => {
  const ctx = props.artifactsContext
  return deriveVersionKeys(
    artifactsPackages.value.map(p => p.project),
    ctx.prefix.split(':').length,
    ctx.allowedSubprojects,
  )
})
```

`fetchRepos` (lines 112-122) becomes context-driven (the release/PR else-branch folds in — every context's apiBase + plain version forms the repos URL):

```ts
async function fetchRepos(versionKey: string) {
  const ctx = props.artifactsContext
  const [ver, sub] = splitVersionKey(versionKey)
  let url = `${ctx.apiBase}/${ver}/repos`
  if (sub) {
    url += `?subproject=${encodeURIComponent(sub)}`
  }
  pendingFetches.value++
  ...rest unchanged...
```

- [ ] **Step 9: Update `frontend/src/App.vue`**

1. Import line 11 → `import { PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT } from './lib/contexts'`
2. Defaults: `const selectedContext = ref<Context>(PPG_STAGING_CONTEXT)` (line 52) and `const artifactsContext = ref<Context>(PPG_STAGING_CONTEXT)` (line 138).
3. Remove the `prefixDepth` computed (line 54) — its two consumers change below (keep `selectedPrefix`, used by the SSE stream).
4. Line 92: `usePackages(apiBase, version, selectedContext)`.
5. Line 132: `return [PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, ...prContexts]`.
6. Line 171: `return [PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT, ...prContexts]`.
7. Line 188: `const filteredEvents = computed(() => filterEvents(activeTags.value, version.value, selectedContext.value))`.

- [ ] **Step 10: Build to verify**

Run: `cd /home/rdias/Work/percona-obs-dashboard/frontend && npm run build`
Expected: exits 0 (vue-tsc catches any missed `subproject`/`prefixDepth` usage).

- [ ] **Step 11: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/lib/versions.ts frontend/src/lib/contexts.ts frontend/src/types/api.ts frontend/src/composables/useUrlState.ts frontend/src/composables/usePackages.ts frontend/src/composables/useEvents.ts frontend/src/composables/useArtifacts.ts frontend/src/components/ArtifactsPanel.vue frontend/src/App.vue
git commit -s -m "feat(frontend): devel/staging contexts with version-extension selector model"
```

---

### Task 4: Frontend — Overview Staging category

**Goal:** Overview tables section rows into Devel · Staging · Releases · PRs.

**Files:**
- Modify: `frontend/src/lib/overview.ts` (`ProjectCategory`, `CATEGORY_ORDER`, `categoryOf`)

**Acceptance Criteria:**
- [ ] `ProjectCategory` includes `'Staging'`; `CATEGORY_ORDER = ['Devel', 'Staging', 'Releases', 'PRs']`
- [ ] `categoryOf`: `:PR:` → PRs; `:releases` suffix → Releases; `':staging:'` substring → Staging; else Devel (devel rows + common projects)
- [ ] `npm run build` exits 0

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npm run build` → exits 0

**Steps:**

- [ ] **Step 1: Update `frontend/src/lib/overview.ts`**

Change the type and the two definitions (leave `groupByCategory` untouched — it iterates `CATEGORY_ORDER` generically):

```ts
export type ProjectCategory = 'Devel' | 'Staging' | 'Releases' | 'PRs'

export const CATEGORY_ORDER: ProjectCategory[] = ['Devel', 'Staging', 'Releases', 'PRs']

export function categoryOf(project: string): ProjectCategory {
  if (project.includes(':PR:')) return 'PRs'
  if (project.endsWith(':releases')) return 'Releases'
  if (project.includes(':staging:')) return 'Staging'
  return 'Devel'
}
```

- [ ] **Step 2: Build to verify**

Run: `cd /home/rdias/Work/percona-obs-dashboard/frontend && npm run build`
Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add frontend/src/lib/overview.ts
git commit -s -m "feat(overview): staging category section"
```

---

## Self-Review

- **Spec coverage:** §1 routes/handlers/query → Task 1; §2 logicalProject → Task 2; §3 contexts + §4 version-extension model → Task 3; §5 categories → Task 4; §6 unchanged-areas touched by no task; error handling (tier 400, subproject 400, stale-URL fallbacks) covered by Task 1 code and existing fallback paths; testing section maps to each task's tests + `npm run build`. No gaps.
- **Placeholder scan:** Task 2 Step 4 conditionally instructs updating `TestOverviewSnapshotBuilder` expectations if they assert old row names — that's a concrete, bounded instruction (data seeding unchanged, expected names get the staging mapping), not a placeholder. Everything else is complete code.
- **Type consistency:** `QueryBuildPackages(db, root, product, tier, version)` matches handler and both tests; `productTier(w, r) (string, bool)` used identically in three handlers; `deriveVersionKeys(projects, depth, absorbed)` / `matchesVersionKey(project, prefix, key, absorbed)` / `splitVersionKey(key)` consistent across versions.ts, usePackages, useEvents, useArtifacts, ArtifactsPanel; `usePackages(apiBase, version, context)` matches the App.vue call; `filterEvents(tags, version, ctx)` matches its caller; `tierRow(root, tier, version, sub)` consistent between function and callers.
