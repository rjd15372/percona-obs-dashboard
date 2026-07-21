# Completion-Based Rebuild Counting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Overview's rebuild count is driven by build completions (`finished`/`failed` duration rows, written by the MQ consumer polling-independently) instead of poll-observed `building` rows, so idle nights report their real rebuild counts.

**Architecture:** Rename `BuildingEntry`/`QueryBuildingEntries` to `BuildCompletion`/`QueryBuildCompletions` in the store and flip the SQL to `state IN ('finished', 'failed')`; the API aggregation and JSON shape are untouched (call-site renames only). No frontend, consumer, or schema changes.

**Tech Stack:** Go (modernc sqlite).

**User decisions (already made):**
- Approach A: count completions from the rows `recordStateTransitions` already writes. (Rejected by user choice: synthesized `building` rows; a new completion counter/event type.)
- Accepted semantics shift: "rebuilds" = builds that completed in the window; the `failed → failed` repeat-while-idle case stays uncounted (documented in the spec).

Spec: `docs/superpowers/specs/2026-07-21-rebuild-count-completions-design.md`

**Conventions:** backend commands from `backend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: count build completions

**Goal:** `QueryBuildCompletions` returns `finished`/`failed` entries; every caller and test renamed/reseeded; a `building` row is the regression guard.

**Files:**
- Modify: `backend/internal/store/overview.go:8-41` (type + function rename, SQL, doc comments)
- Modify: `backend/internal/store/overview_test.go:8-42` (test rewrite)
- Modify: `backend/internal/api/overview.go` (three rename sites: the aggregate signature ~line 116, the two query calls ~lines 326/330)
- Modify: `backend/internal/api/overview_test.go` (three `store.BuildingEntry` literals ~lines 54/60/174; one seeded state ~line 155)

**Acceptance Criteria:**
- [ ] Seeded rows (in-window `finished`, in-window `failed`, in-window `building`, before-window `finished`, in-window `scheduled`) → current window returns exactly the 2 completions, previous window exactly 1; the `building` row never counts
- [ ] `grep -rn "BuildingEntry\|QueryBuildingEntries" internal/` → no matches
- [ ] `TestOverviewHandler` (api) passes with its seed state flipped to `finished` and unchanged expectations
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `cd backend && go test ./internal/store/ -run TestOverviewBuildCompletions -count=1 -v && go test ./internal/api/ -count=1` → all PASS

**Steps:**

- [ ] **Step 1: Rewrite the store test (failing first)**

Replace `TestOverviewBuildingEntries` in `backend/internal/store/overview_test.go` (keep `TestOverviewCveQueries` untouched) with:

```go
func TestOverviewBuildCompletions(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ins := func(project, pkg, repo, state, enteredAt string) {
		if _, err := db.Exec(`INSERT INTO target_state_durations
			(project, package, repo, arch, state, entered_at) VALUES (?,?,?,?,?,?)`,
			project, pkg, repo, "x86_64", state, enteredAt); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	fmtT := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "finished", fmtT(-1*time.Hour))  // in window: counted
	ins("isv:percona:ppg:17", "pkg-b", "UBI_9", "failed", fmtT(-2*time.Hour))    // in window: counted
	ins("isv:percona:ppg:17", "pkg-c", "UBI_9", "building", fmtT(-1*time.Hour))  // build start: must NOT count
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "finished", fmtT(-30*time.Hour)) // before window
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "scheduled", fmtT(-1*time.Hour)) // wrong state

	got, err := QueryBuildCompletions(db, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("QueryBuildCompletions = %+v, want the 2 in-window completions", got)
	}
	pkgs := map[string]bool{}
	for _, e := range got {
		pkgs[e.Package] = true
	}
	if !pkgs["pkg-a"] || !pkgs["pkg-b"] {
		t.Fatalf("QueryBuildCompletions = %+v, want pkg-a (finished) and pkg-b (failed)", got)
	}
	prev, err := QueryBuildCompletions(db, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 1 {
		t.Fatalf("previous window = %+v, want 1", prev)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run TestOverviewBuildCompletions -v`
Expected: compile failure — `undefined: QueryBuildCompletions`.

- [ ] **Step 3: Implement the store change**

In `backend/internal/store/overview.go`, replace the `BuildingEntry` type, its comment, and `QueryBuildingEntries` (lines 8-41) with:

```go
// BuildCompletion is one target entering a build-completion state —
// "finished" (successful or unchanged build) or "failed" — the Overview's
// unit of "one rebuild". The MQ consumer's merge writes exactly one such
// transition per completed build at event time, so the count is
// polling-independent (build starts are only observable by polling and
// vanish while the idle gate pauses it).
type BuildCompletion struct {
	Project string
	Package string
	Repo    string
}

// QueryBuildCompletions returns every target_state_durations row that
// entered "finished" or "failed" within [since, until). Timestamps in the
// table are RFC3339Nano strings (UTC); lexicographic comparison is
// chronologically correct to within sub-second edge cases (a whole-second
// timestamp sorts after the same second with a fractional part), which is
// negligible for window counting.
func QueryBuildCompletions(db *sql.DB, since, until time.Time) ([]BuildCompletion, error) {
	rows, err := db.Query(`
		SELECT project, package, repo FROM target_state_durations
		WHERE state IN ('finished', 'failed') AND entered_at >= ? AND entered_at < ?`,
		since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuildCompletion
	for rows.Next() {
		var e BuildCompletion
		if err := rows.Scan(&e.Project, &e.Package, &e.Repo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rename the API call sites**

In `backend/internal/api/overview.go`:
- The aggregate function signature (~line 116): `cur, prev []store.BuildingEntry` → `cur, prev []store.BuildCompletion`.
- The two query calls (~lines 326 and 330): `store.QueryBuildingEntries(...)` → `store.QueryBuildCompletions(...)` (arguments unchanged).
- Run `grep -n "Building" internal/api/overview.go` — if any comment still describes rebuilds as "entering building", reword it to "build completions"; otherwise nothing else changes.

In `backend/internal/api/overview_test.go`:
- The three literals (~lines 54, 60, 174): `[]store.BuildingEntry{...}` → `[]store.BuildCompletion{...}` (element values unchanged).
- The seeded INSERT (~line 155): change the state literal `'building'` to `'finished'` — the test's expectations stay exactly as they are (one counted rebuild either way).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/store/ -run TestOverviewBuildCompletions -count=1 -v && go test ./... -count=1 && go build ./... && gofmt -l internal/store internal/api` and `grep -rn "BuildingEntry\|QueryBuildingEntries" internal/`
Expected: all PASS, build OK, gofmt must not list the touched files (release_artifacts.go drift is pre-existing — leave it), grep empty.

- [ ] **Step 6: Commit**

```bash
git add internal/store/overview.go internal/store/overview_test.go internal/api/overview.go internal/api/overview_test.go
git commit -s -m "fix(overview): count rebuilds from build completions, not observed starts"
```
