# PR Context — One Context Per PR Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every PR (including PRs that touch only `common` packages) appear as a single selectable context keyed by PR number.

**Architecture:** Frontend derives one `Context` per PR (`prefix isv:percona:PR:pr-N`, `apiBase /api/pr/pr-N`); the backend PR routes drop the `{subproject}` segment and the three PR store queries match the whole-PR prefix instead of unioning `subproject + common`.

**Tech Stack:** Go (chi, SQLite), Vue 3 `<script setup>` + TypeScript.

**User decisions (already made):**
- "One context per PR" — context keyed by PR number, prefix `isv:percona:PR:pr-N`, covering all subprojects (chosen over per-subproject options).
- "Directly on main" — commit straight onto `main` (explicit consent; no feature branch).
- Accepted consequence: a PR context's version dropdown shows only "All versions".

Spec: `docs/superpowers/specs/2026-06-30-pr-context-per-pr-design.md`.

---

## File Structure

- `backend/internal/store/packages.go` — `QueryPRBuildPackages`, `QueryPRDistinctRepos`: drop `subproject`/`version`, match whole-PR prefix.
- `backend/internal/store/events.go` — `QueryPRBuildEvents`: same.
- `backend/internal/store/packages_test.go`, `backend/internal/store/events_test.go` — update the three tests; flip the `other`-subproject expectation to *included*, add cross-PR exclusion + a common-only PR.
- `backend/internal/api/handlers.go` — `prContextPackagesHandler`, `prContextEventsHandler`, `prReposHandler`: drop the `subproject` (and `version` for repos) URL params.
- `backend/internal/api/server.go` — PR route group `/api/pr/{pr}/{subproject}/{version}` → `/api/pr/{pr}/{version}`.
- `frontend/src/App.vue` — `contexts` and `artifactsContexts` computeds: one context per PR.

**Single task / single commit:** the frontend `apiBase` and the backend routes form one contract and must change together; splitting would leave a broken intermediate commit on `main`.

---

### Task 1: One context per PR (backend + frontend)

**Goal:** Switch PR contexts from per-`(PR, subproject)` to per-PR across the backend routes/queries and the frontend derivations, so a common-only PR is selectable and shows its packages.

**Files:**
- Modify: `backend/internal/store/packages.go`
- Modify: `backend/internal/store/events.go`
- Modify: `backend/internal/store/packages_test.go`
- Modify: `backend/internal/store/events_test.go`
- Modify: `backend/internal/api/handlers.go`
- Modify: `backend/internal/api/server.go`
- Modify: `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `QueryPRBuildPackages(db, root, pr)`, `QueryPRBuildEvents(db, root, pr, from, to)`, `QueryPRDistinctRepos(db, root, pr)` match the whole-PR prefix `root:PR:pr` (no `subproject`/`version` params; no `common` union branch).
- [ ] Store tests: a query for `pr-104` returns packages/events/repos from *all* its subprojects (incl. `other` and `common`) and excludes other PRs; a query for a common-only PR (`pr-200`) returns its packages.
- [ ] PR routes are `/api/pr/{pr}/{version}/{packages,events,repos}`; handlers resolve only `pr`.
- [ ] `contexts` and `artifactsContexts` emit one context per PR: `label "PR #N"`, `prefix isv:percona:PR:pr-N`, `apiBase /api/pr/pr-N`; the `common` skip is gone.
- [ ] `go test ./internal/...`, `go build ./...`, and `npm run build` all pass.

**Verify:** `cd backend && go test ./internal/... && go build ./... && cd ../frontend && npm run build` → all pass

**Steps:**

- [ ] **Step 1: Rewrite the three store tests (red).** They must reflect the new whole-PR semantics before the code changes.

In `backend/internal/store/packages_test.go`, replace `TestQueryPRBuildPackagesIncludesPRCommon` (currently starting at line 284) entirely with:

```go
func TestQueryPRBuildPackagesCoversWholePR(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, '[]', ?)`, project, name, now); err != nil {
			t.Fatal(err)
		}
	}
	insert("isv:percona:PR:pr-104:ppg:17", "pg_tde")
	insert("isv:percona:PR:pr-104:ppg:17:containers:ubi9", "pg_container")
	insert("isv:percona:PR:pr-104:common", "common_pkg")
	insert("isv:percona:PR:pr-104:common:deps", "common_dep")
	insert("isv:percona:PR:pr-104:other:17", "other_pkg")            // now part of the PR
	insert("isv:percona:PR:pr-105:common", "other_pr_common")        // different PR
	insert("isv:percona:PR:pr-200:common:deps:build", "common_only") // common-only PR

	pkgs, err := QueryPRBuildPackages(db, "isv:percona", "pr-104")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, p := range pkgs {
		names[p.Name] = true
	}
	for _, want := range []string{"pg_tde", "pg_container", "common_pkg", "common_dep", "other_pkg"} {
		if !names[want] {
			t.Errorf("pr-104: missing expected package %q", want)
		}
	}
	for _, unwanted := range []string{"other_pr_common", "common_only"} {
		if names[unwanted] {
			t.Errorf("pr-104: unexpected package %q from another PR", unwanted)
		}
	}

	// Common-only PR must be reachable.
	onlyCommon, err := QueryPRBuildPackages(db, "isv:percona", "pr-200")
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyCommon) != 1 || onlyCommon[0].Name != "common_only" {
		t.Errorf("pr-200 (common-only): got %d packages, want exactly [common_only]", len(onlyCommon))
	}
}
```

In `backend/internal/store/packages_test.go`, replace `TestQueryPRDistinctReposIncludesPRCommon` (currently starting at line 327, through its closing brace) entirely with:

```go
func TestQueryPRDistinctReposCoversWholePR(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name, targets string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, ?, ?)`, project, name, targets, now); err != nil {
			t.Fatal(err)
		}
	}
	insert("isv:percona:PR:pr-104:ppg:17", "pg_tde", `[{"repo":"EL_9"}]`)
	insert("isv:percona:PR:pr-104:common", "common_pkg", `[{"repo":"Debian_12"}]`)
	insert("isv:percona:PR:pr-104:ppg:18", "pg_tde18", `[{"repo":"EL_8"}]`) // now included
	insert("isv:percona:PR:pr-105:ppg:17", "other", `[{"repo":"EL_7"}]`)    // different PR

	repos, err := QueryPRDistinctRepos(db, "isv:percona", "pr-104")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, repo := range repos {
		got[repo] = true
	}
	for _, want := range []string{"EL_9", "Debian_12", "EL_8"} {
		if !got[want] {
			t.Errorf("missing expected repo %q", want)
		}
	}
	if got["EL_7"] {
		t.Errorf("unexpected repo EL_7 from another PR")
	}
}
```

In `backend/internal/store/events_test.go`, replace `TestQueryPRBuildEventsIncludesPRCommon` (currently starting at line 132) entirely with:

```go
func TestQueryPRBuildEventsCoversWholePR(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	events := []*model.Event{
		{
			ID: "evt_pr_ppg", Type: model.EventFailed, Tags: []string{"ppg", "pr"},
			Project: "isv:percona:PR:pr-104:ppg:17", Package: "pg_tde",
			What: "build failed", URL: "https://build.opensuse.org/x", At: now,
		},
		{
			ID: "evt_pr_common", Type: model.EventSucceeded, Tags: []string{"common", "pr"},
			Project: "isv:percona:PR:pr-104:common", Package: "common_pkg",
			What: "build succeeded", URL: "https://build.opensuse.org/x", At: now.Add(-time.Second),
		},
		{
			ID: "evt_other_subproject", Type: model.EventFailed,
			Project: "isv:percona:PR:pr-104:other:17", Package: "other_pkg",
			What: "build failed", URL: "https://build.opensuse.org/x", At: now,
		},
		{
			ID: "evt_other_pr", Type: model.EventFailed,
			Project: "isv:percona:PR:pr-105:common", Package: "other_pr_pkg",
			What: "build failed", URL: "https://build.opensuse.org/x", At: now,
		},
	}
	for _, evt := range events {
		if err := AppendEvent(db, evt); err != nil {
			t.Fatal(err)
		}
	}

	got, err := QueryPRBuildEvents(db, "isv:percona", "pr-104", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, evt := range got {
		ids[evt.ID] = true
	}
	for _, want := range []string{"evt_pr_ppg", "evt_pr_common", "evt_other_subproject"} {
		if !ids[want] {
			t.Fatalf("missing expected event %q", want)
		}
	}
	if ids["evt_other_pr"] {
		t.Fatalf("unexpected event from another PR")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `cd backend && go test ./internal/store/ -run 'CoversWholePR' -v`
Expected: compile error / FAIL — `QueryPRBuildPackages`/`QueryPRBuildEvents`/`QueryPRDistinctRepos` still have the old `subproject`/`version` signatures.

- [ ] **Step 3: Update `QueryPRBuildPackages` in `backend/internal/store/packages.go`** (replace the function at lines ~479–497):

```go
// QueryPRBuildPackages returns all packages under a PR (every subproject), matching
// the whole-PR project prefix root:PR:<pr>.
func QueryPRBuildPackages(db *sql.DB, root, pr string) ([]*model.Package, error) {
	p := root + ":PR:" + pr
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages
		WHERE is_release = 0
		  AND (project = ? OR project LIKE ? || ':%')
		ORDER BY project, name`,
		p, p,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
```

- [ ] **Step 4: Update `QueryPRDistinctRepos` in `backend/internal/store/packages.go`** (replace the function at lines ~537–553):

```go
// QueryPRDistinctRepos returns the distinct build repos across all of a PR's
// packages (every subproject), matching the whole-PR project prefix root:PR:<pr>.
func QueryPRDistinctRepos(db *sql.DB, root, pr string) ([]string, error) {
	p := root + ":PR:" + pr
	rows, err := db.Query(
		`SELECT targets_json FROM packages
		 WHERE (is_container IS NULL OR is_container = 0)
		   AND (project = ? OR project LIKE ? || ':%')`,
		p, p,
	)
	if err != nil {
		return nil, err
	}
	return scanDistinctRepos(rows)
}
```

- [ ] **Step 5: Update `QueryPRBuildEvents` in `backend/internal/store/events.go`** (replace the function at lines ~65–103):

```go
// QueryPRBuildEvents returns events for all packages under a PR (every subproject),
// matching the whole-PR project prefix root:PR:<pr>.
func QueryPRBuildEvents(db *sql.DB, root, pr string, from, to time.Time) ([]*model.Event, error) {
	p := root + ":PR:" + pr
	rows, err := db.Query(`
		SELECT id, type, tags, project, package,
		       COALESCE(repo,''), COALESCE(arch,''),
		       what, why, url, at, COALESCE(version,'')
		FROM events
		WHERE at >= ? AND at <= ?
		  AND (project = ? OR project LIKE ? || ':%')
		ORDER BY at DESC
		LIMIT 500`,
		from, to, p, p,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]*model.Event, 0)
	for rows.Next() {
		e := &model.Event{}
		var tagsJSON string
		if err := rows.Scan(
			&e.ID, &e.Type, &tagsJSON, &e.Project, &e.Package,
			&e.Repo, &e.Arch, &e.What, &e.Why, &e.URL, &e.At, &e.Version,
		); err != nil {
			return nil, err
		}
		if tagsJSON != "" && tagsJSON != "[]" {
			_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
```

- [ ] **Step 6: Update the three handlers in `backend/internal/api/handlers.go`.**

`prContextPackagesHandler` (body at ~108–128) — drop the `subproject` line and the doc comment's `{subproject}` reference; the call becomes:

```go
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")

		pkgs, err := store.QueryPRBuildPackages(db, root, pr)
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
```

`prContextEventsHandler` (body at ~133–155) — drop `subproject`; the call becomes:

```go
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")

		from, to, err := parseTimeWindow(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		events, err := store.QueryPRBuildEvents(db, root, pr, from, to)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			return
		}
	}
```

`prReposHandler` (body at ~321–349) — drop the `subproject`/`version` params; the call becomes:

```go
	return func(w http.ResponseWriter, r *http.Request) {
		repos, err := store.QueryPRDistinctRepos(db, root, chi.URLParam(r, "pr"))
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := ReposResponse{RPM: []RepoInfo{}, DEB: []RepoInfo{}}
		for _, obsName := range repos {
			info := RepoInfo{OBS: obsName, Name: repoDisplayName(obsName)}
			if repoType(obsName) == "deb" {
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
```

Also update the three doc comments above these handlers to say `GET /api/pr/{pr}/{version}/…` and prefix `isv:percona:PR:{pr}` (drop `{subproject}`).

- [ ] **Step 7: Update the route group in `backend/internal/api/server.go`** (line ~37). Change:

```go
	r.Route("/api/pr/{pr}/{subproject}/{version}", func(r chi.Router) {
```

to:

```go
	r.Route("/api/pr/{pr}/{version}", func(r chi.Router) {
```

(The three `r.Get("/packages"|"/events"|"/repos", …)` lines inside are unchanged.)

- [ ] **Step 8: Run the backend (green).**

Run: `cd backend && go test ./internal/... && go build ./...`
Expected: all tests `ok`, build succeeds.

- [ ] **Step 9: Update `contexts` in `frontend/src/App.vue`** (replace the computed at lines ~103–134):

```ts
// Derive available contexts from PR groups data — one context per PR (all subprojects).
const contexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 1 >= parts.length) continue
      const prSegment = parts[prIdx + 1] // "pr-92"
      if (seen.has(prSegment)) continue
      seen.add(prSegment)
      const prNum = prSegment.replace(/^pr-/i, '')
      prContexts.push({
        label: `PR #${prNum}`,
        apiBase: `/api/pr/${prSegment}`,
        prefix: `isv:percona:PR:${prSegment}`,
      })
    }
  }

  prContexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [PPG_CONTEXT, ...prContexts]
})
```

- [ ] **Step 10: Update `artifactsContexts` in `frontend/src/App.vue`** (replace the computed at lines ~148–179):

```ts
// Artifacts contexts: PPG + Releases + one context per PR (all subprojects)
const artifactsContexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 1 >= parts.length) continue
      const prSegment = parts[prIdx + 1]
      if (seen.has(prSegment)) continue
      seen.add(prSegment)
      const prNum = prSegment.replace(/^pr-/i, '')
      prContexts.push({
        label: `PR #${prNum}`,
        apiBase: `/api/pr/${prSegment}`,
        prefix: `isv:percona:PR:${prSegment}`,
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
```

- [ ] **Step 11: Build the frontend.**

Run: `cd frontend && npm run build`
Expected: vue-tsc type-check + bundle succeed.

- [ ] **Step 12: Commit** (this repo requires `-s`, NEVER a Co-Authored-By trailer):

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/store/packages.go backend/internal/store/events.go \
        backend/internal/store/packages_test.go backend/internal/store/events_test.go \
        backend/internal/api/handlers.go backend/internal/api/server.go \
        frontend/src/App.vue
git commit -s -m "fix: show every PR as one context (include common-only PRs)"
```

---

## Self-Review

**1. Spec coverage:**
- One context per PR (label/prefix/apiBase, common skip removed) → Steps 9–10. ✓
- Common-only PR selectable + shows packages → backend whole-PR prefix (Steps 3,5) + frontend context (Step 9); test in Step 1 (`pr-200`). ✓
- Mixed PR shows all packages → whole-PR prefix; test asserts `other_pkg` included (Step 1). ✓
- Backend routes `/api/pr/{pr}/{version}` + whole-PR queries, subproject removed → Steps 3–7. ✓
- Tests cover common-only + mixed; `go test`/`npm run build` pass → Steps 1, 8, 11; Verify. ✓

**2. Placeholder scan:** No TBD/TODO; every step has full code and exact anchor lines. ✓

**3. Type/signature consistency:** All call sites use the new signatures — `QueryPRBuildPackages(db, root, pr)` (handler Step 6, tests Step 1), `QueryPRBuildEvents(db, root, pr, from, to)` (Step 6, Step 1), `QueryPRDistinctRepos(db, root, pr)` (Step 6, Step 1). The route `/api/pr/{pr}/{version}` (Step 7) matches the frontend `apiBase /api/pr/pr-N` (Steps 9–10): the fetch `${apiBase}/_/packages` → `/api/pr/pr-N/_/packages` binds `{pr}=pr-N`, `{version}=_`. Prefix sort uses `split(':')[3]` = `pr-N` for `isv:percona:PR:pr-N` ✓. ✓
