# Overview Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A third dashboard tab summarizing rebuild activity (windowed, delta'd, per logical project) and CVE exposure (per project, expandable to per image), backed by `GET /api/overview`.

**Architecture:** Backend: a pure logical-project grouping function; thin store queries over `target_state_durations`, `cve_scans`, `cve_periods`; a snapshot builder + 60s TTL/singleflight cache + handler in `internal/api/overview.go`. Frontend: new tokens (`--crit`/`--high`), snake_case types, a `useOverviewData` composable (fetch + own EventSource on the existing `/api/stream` for debounced refetch), four components, and `mainTab: 'overview'` integration with URL state.

**Tech Stack:** Go + SQLite (existing patterns: `releaseArtifactsCache` shape), Vue 3 + TS + Tailwind over `theme.css` tokens, `vue-tsc` build as frontend verification.

**User decisions (already made):**
- Rebuild unit = `target_state_durations` rows entering `building` (per target); prev-window delta from the same table (works for 7d).
- Scope = everything, grouped into logical projects (dev versions, extras separate, ppg:common, common, releases, per-PR).
- Realtime = reuse global `/api/stream` endpoint, debounce-refetch; no dedicated stream.
- 5 stat cards including Most Rebuilt Repo (`top_repo` snapshot field).
- CVE per image = max across archs; `oldest_open_days` from `cve_since`; `avg_fix_days` from `cve_periods` (0 rows today → renders `—`).
- Reuse existing theme tokens (Overview inherits the app's **blue** brand, not the mockup's purple); add only `--crit`/`--high` + tints. App mono stack; no JetBrains Mono bundling; no in-page theme toggle.
- Snake_case JSON + frontend types (app convention, deviation from UI-spec camelCase).
- **Branch `feature/overview-tab` created from `origin/main`** (== local main after the user's push); plain branch, no worktree.
- Pixel authority: `~/Downloads/obs-dashboard-overview/overview-panel-spec.md` + `spec-assets/*.png` + approved mockup (scratchpad file `obs-overview-mockup.html`, artifact 5f79d92b). Spec doc: `docs/superpowers/specs/2026-07-07-overview-tab-design.md`.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `backend/internal/api/overview.go` (new) | grouping fn, snapshot types, builder, cache, handler | 1, 3 |
| `backend/internal/api/overview_test.go` (new) | grouping table test; snapshot/handler/cache tests | 1, 3 |
| `backend/internal/store/overview.go` (new) | thin queries: building entries, all scans, all periods | 2 |
| `backend/internal/store/overview_test.go` (new) | query tests | 2 |
| `backend/internal/api/server.go` | route registration | 3 |
| `frontend/src/assets/theme.css` + `frontend/tailwind.config.ts` | `--crit`/`--high` tokens | 4 |
| `frontend/src/types/overview.ts` (new) | snapshot types (snake_case) | 4 |
| `frontend/src/composables/useOverviewData.ts` (new) | fetch + SSE refetch + derived aggregates | 5 |
| `frontend/src/components/StatCard.vue` (new) | reusable stat card leaf | 6 |
| `frontend/src/components/RebuildBarChart.vue` (new) | normalized bar rows | 6 |
| `frontend/src/components/CveExposureTable.vue` (new) | expandable project/image table | 6 |
| `frontend/src/components/OverviewPanel.vue` (new) | page: header, window selector, composition | 6 |
| `frontend/src/App.vue`, `AppHeader.vue`, `useUrlState.ts` | third tab + `?tab=overview&owin=` | 7 |

Execution note: **Task 1 Step 0 creates the branch**; all commits land on `feature/overview-tab`.

---

### Task 1: Branch + logical-project grouping

**Goal:** The `feature/overview-tab` branch exists off `origin/main`, carrying `logicalProject` — the single grouping rule both overview sections use.

**Files:**
- Create: `backend/internal/api/overview.go`
- Create: `backend/internal/api/overview_test.go`

**Acceptance Criteria:**
- [ ] Branch `feature/overview-tab` created from `origin/main`; baseline `go test ./...` + `npm run build` green on it.
- [ ] `logicalProject(root, project)` implements the spec table: version roots absorb `:containers:*`; `:extras` separate (absorbing its own subtree); `ppg:common*`→`ppg:common`; `common*`→`common`; `ppg:releases*`→`ppg:releases`; `PR:pr-N*`→`PR:pr-N`; non-root or malformed → `""`.

**Verify:** `cd backend && go test ./internal/api/ -run TestLogicalProject -v` → PASS

**Steps:**

- [ ] **Step 0: Create the branch**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git fetch origin && git switch -c feature/overview-tab origin/main
cd backend && go test ./... && cd ../frontend && npm run build
```

- [ ] **Step 1: Write the failing test** — `backend/internal/api/overview_test.go`:

```go
package api

import "testing"

func TestLogicalProject(t *testing.T) {
	const root = "isv:percona"
	cases := []struct{ project, want string }{
		{"isv:percona:ppg:17", "isv:percona:ppg:17"},
		{"isv:percona:ppg:17:containers:ubi9", "isv:percona:ppg:17"},
		{"isv:percona:ppg:16:extras", "isv:percona:ppg:16:extras"},
		{"isv:percona:ppg:16:extras:containers:ubi9", "isv:percona:ppg:16:extras"},
		{"isv:percona:ppg:common", "isv:percona:ppg:common"},
		{"isv:percona:ppg:common:deps", "isv:percona:ppg:common"},
		{"isv:percona:common:containers:ubi8", "isv:percona:common"},
		{"isv:percona:ppg:releases:17:containers:ubi9", "isv:percona:ppg:releases"},
		{"isv:percona:PR:pr-124:ppg:16:extras", "isv:percona:PR:pr-124"},
		{"isv:percona:PR:pr-33:ppg:18:containers:ubi9", "isv:percona:PR:pr-33"},
		{"isv:other:ppg:17", ""},
		{"isv:percona:ppg", ""},
	}
	for _, c := range cases {
		if got := logicalProject(root, c.project); got != c.want {
			t.Errorf("logicalProject(%q) = %q, want %q", c.project, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/api/ -run TestLogicalProject -v` → compile FAIL (undefined).

- [ ] **Step 3: Create `backend/internal/api/overview.go`**:

```go
package api

import "strings"

// logicalProject maps a raw OBS project to the Overview row it belongs to:
// dev version roots absorb their :containers:* subprojects; :extras is its own
// row (absorbing its subtree); the common trees, the releases tree, and each PR
// collapse to one row each. Unknown shapes return "" (excluded).
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
		default:
			if len(rel) >= 3 && rel[2] == "extras" {
				return root + ":ppg:" + rel[1] + ":extras"
			}
			return root + ":ppg:" + rel[1]
		}
	}
	return ""
}
```

- [ ] **Step 4: Run test** — PASS. Also `go vet ./internal/api/`.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/api/overview.go internal/api/overview_test.go
git commit -s -m "feat(api): logical-project grouping for the overview tab"
```

```json:metadata
{"files": ["backend/internal/api/overview.go", "backend/internal/api/overview_test.go"], "verifyCommand": "cd backend && go test ./internal/api/ -run TestLogicalProject -v", "acceptanceCriteria": ["branch feature/overview-tab created from origin/main, baseline green", "logicalProject implements the spec grouping table incl. extras/PR/common/releases and exclusion cases"], "modelTier": "mechanical"}
```

---

### Task 2: Store queries

**Goal:** Three thin read queries feeding the snapshot builder.

**Files:**
- Create: `backend/internal/store/overview.go`
- Create: `backend/internal/store/overview_test.go`

**Acceptance Criteria:**
- [ ] `QueryBuildingEntries(db, since, until)` returns (project, package, repo) for `target_state_durations` rows with `state='building'` and `entered_at` in `[since, until)`.
- [ ] `QueryAllCveScans(db)` returns (project, package, arch, critical, high, cve_since-nullable) for every scan row.
- [ ] `QueryAllCvePeriods(db)` returns (project, package, cve_since, clean_since) for every period row.
- [ ] Tests seed via raw SQL (matching the tables' RFC3339Nano string timestamps) and assert window boundaries and null handling.

**Verify:** `cd backend && go test ./internal/store/ -run TestOverview -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing tests** — `backend/internal/store/overview_test.go` (package `store`, `Open(":memory:")` like siblings):

```go
package store

import (
	"testing"
	"time"
)

func TestOverviewBuildingEntries(t *testing.T) {
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
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "building", fmtT(-1*time.Hour))   // in window
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "building", fmtT(-30*time.Hour))  // before window
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "scheduled", fmtT(-1*time.Hour))  // wrong state

	got, err := QueryBuildingEntries(db, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Repo != "UBI_9" || got[0].Package != "pkg-a" {
		t.Fatalf("QueryBuildingEntries = %+v, want 1 in-window building row", got)
	}
	// The -30h row falls in the previous 24h window.
	prev, err := QueryBuildingEntries(db, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 1 {
		t.Fatalf("previous window = %+v, want 1", prev)
	}
}

func TestOverviewCveQueries(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO cve_scans
		(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "x86_64", "ref", now.Format(time.RFC3339),
		2, 6, "[]", now.Add(-34*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cve_scans
		(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json)
		VALUES (?,?,?,?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "aarch64", "ref", now.Format(time.RFC3339),
		1, 6, "[]"); err != nil { // NULL cve_since (pre-age-tracking row)
		t.Fatal(err)
	}
	scans, err := QueryAllCveScans(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != 2 {
		t.Fatalf("scans = %d, want 2", len(scans))
	}
	var withSince, withoutSince int
	for _, s := range scans {
		if s.CveSince != nil {
			withSince++
		} else {
			withoutSince++
		}
	}
	if withSince != 1 || withoutSince != 1 {
		t.Fatalf("cve_since nullability mishandled: %+v", scans)
	}

	if _, err := db.Exec(`INSERT INTO cve_periods (project, package, arch, cve_since, clean_since)
		VALUES (?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "x86_64",
		now.Add(-20*24*time.Hour).Format(time.RFC3339Nano), now.Add(-11*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	periods, err := QueryAllCvePeriods(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 || int(periods[0].CleanSince.Sub(periods[0].CveSince).Hours()/24) != 9 {
		t.Fatalf("periods = %+v, want one 9-day episode", periods)
	}
}
```

- [ ] **Step 2: Run to verify failure** — undefined symbols.

- [ ] **Step 3: Create `backend/internal/store/overview.go`**:

```go
package store

import (
	"database/sql"
	"time"
)

// BuildingEntry is one target entering the "building" state (the Overview's
// unit of "one rebuild").
type BuildingEntry struct {
	Project string
	Package string
	Repo    string
}

// QueryBuildingEntries returns every target_state_durations row that entered
// "building" within [since, until). Timestamps in the table are RFC3339Nano
// strings (UTC), so lexicographic comparison is chronologically correct.
func QueryBuildingEntries(db *sql.DB, since, until time.Time) ([]BuildingEntry, error) {
	rows, err := db.Query(`
		SELECT project, package, repo FROM target_state_durations
		WHERE state = 'building' AND entered_at >= ? AND entered_at < ?`,
		since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuildingEntry
	for rows.Next() {
		var e BuildingEntry
		if err := rows.Scan(&e.Project, &e.Package, &e.Repo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OverviewCveScan is one arch scan row for the Overview aggregation.
type OverviewCveScan struct {
	Project  string
	Package  string
	Arch     string
	Critical int
	High     int
	CveSince *time.Time // nil for clean images or pre-age-tracking rows
}

// QueryAllCveScans returns every cve_scans row (counts + open-since).
func QueryAllCveScans(db *sql.DB) ([]OverviewCveScan, error) {
	rows, err := db.Query(`
		SELECT project, package, arch, critical_count, high_count, cve_since FROM cve_scans`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverviewCveScan
	for rows.Next() {
		var s OverviewCveScan
		var since sql.NullString
		if err := rows.Scan(&s.Project, &s.Package, &s.Arch, &s.Critical, &s.High, &since); err != nil {
			return nil, err
		}
		if since.Valid {
			if t, err := parseRFC3339(since.String); err == nil {
				s.CveSince = &t
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OverviewCvePeriod is one closed CVE episode.
type OverviewCvePeriod struct {
	Project    string
	Package    string
	CveSince   time.Time
	CleanSince time.Time
}

// QueryAllCvePeriods returns every closed CVE episode.
func QueryAllCvePeriods(db *sql.DB) ([]OverviewCvePeriod, error) {
	rows, err := db.Query(`SELECT project, package, cve_since, clean_since FROM cve_periods`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverviewCvePeriod
	for rows.Next() {
		var p OverviewCvePeriod
		var cs, cl string
		if err := rows.Scan(&p.Project, &p.Package, &cs, &cl); err != nil {
			return nil, err
		}
		p.CveSince, _ = parseRFC3339(cs)
		p.CleanSince, _ = parseRFC3339(cl)
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run** — `go test ./internal/store/ -run TestOverview -v && go test ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/store/overview.go internal/store/overview_test.go
git commit -s -m "feat(store): overview queries for building entries and CVE data"
```

```json:metadata
{"files": ["backend/internal/store/overview.go", "backend/internal/store/overview_test.go"], "verifyCommand": "cd backend && go test ./internal/store/ -run TestOverview -v && go test ./...", "acceptanceCriteria": ["QueryBuildingEntries respects [since,until) and state filter", "QueryAllCveScans preserves cve_since nullability", "QueryAllCvePeriods returns closed episodes"], "modelTier": "standard"}
```

---

### Task 3: Snapshot builder, cache, handler, route

**Goal:** `GET /api/overview?window=24h|48h|7d` serves a cached `OverviewSnapshot`.

**Files:**
- Modify: `backend/internal/api/overview.go` (append)
- Modify: `backend/internal/api/overview_test.go` (append)
- Modify: `backend/internal/api/server.go` (route + cache instance)

**Acceptance Criteria:**
- [ ] JSON shape exactly as the design spec (snake_case; `top_repo`/`top_package` omitted when empty).
- [ ] Aggregations: rebuilds & top_package per logical project; top_repo global; prev-window total; per-image max-across-archs; `oldest_open_days` = days since the *oldest* non-nil `CveSince` among vulnerable archs (0 otherwise); `avg_fix_days` = rounded mean of that image's period durations (0 when none); projects included iff rebuilds>0 or images present; projects sorted rebuilds desc, then name.
- [ ] `window` defaults to `24h`; values other than 24h/48h/7d → 400.
- [ ] 60s TTL + singleflight cache keyed by window (handler test proves the second request within TTL does not re-query — count via a fetch counter or DB mutation between calls).

**Verify:** `cd backend && go test ./internal/api/ -run TestOverview -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Append failing tests** to `backend/internal/api/overview_test.go`:

```go
func TestOverviewSnapshotBuilder(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }

	cur := []store.BuildingEntry{
		{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "UBI_9"},
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Repo: "images"},
		{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "Debian_12"},
		{Project: "isv:percona:PR:pr-9:ppg:18", Package: "pkg-b", Repo: "UBI_9"},
	}
	prev := []store.BuildingEntry{{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "UBI_9"}}
	scans := []store.OverviewCveScan{
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Arch: "x86_64", Critical: 2, High: 6, CveSince: ptrTime(day(34))},
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Arch: "aarch64", Critical: 1, High: 7, CveSince: ptrTime(day(10))},
		{Project: "isv:percona:ppg:releases:17:containers:ubi9", Package: "img-r", Arch: "x86_64", Critical: 0, High: 48, CveSince: nil},
		{Project: "isv:percona:common:containers:ubi9", Package: "img-clean", Arch: "x86_64", Critical: 0, High: 0, CveSince: nil},
	}
	periods := []store.OverviewCvePeriod{
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", CveSince: day(30), CleanSince: day(21)}, // 9d
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", CveSince: day(60), CleanSince: day(49)}, // 11d
	}

	s := buildOverviewSnapshot("isv:percona", "24h", now, cur, prev, scans, periods)

	if s.PreviousWindowRebuildTotal != 1 {
		t.Fatalf("prev total = %d", s.PreviousWindowRebuildTotal)
	}
	if s.TopRepo == nil || s.TopRepo.Name != "UBI_9" || s.TopRepo.Count != 2 {
		t.Fatalf("top_repo = %+v", s.TopRepo)
	}
	// ppg:17 row: 3 rebuilds (root ×2 + containers ×1), top pkg pkg-a×2, img-x with max-across-archs.
	p17 := findProject(t, s, "isv:percona:ppg:17")
	if p17.Rebuilds != 3 || p17.TopPackage.Name != "pkg-a" || p17.TopPackage.Count != 2 {
		t.Fatalf("ppg:17 = %+v", p17)
	}
	if len(p17.Images) != 1 || p17.Images[0].Critical != 2 || p17.Images[0].High != 7 {
		t.Fatalf("img-x max-across-archs failed: %+v", p17.Images)
	}
	if p17.Images[0].OldestOpenDays != 34 || p17.Images[0].AvgFixDays != 10 { // mean(9,11)=10
		t.Fatalf("img-x ages = %+v", p17.Images[0])
	}
	// releases row: 0 rebuilds but has an image → included; NULL cve_since → 0 days.
	rel := findProject(t, s, "isv:percona:ppg:releases")
	if rel.Rebuilds != 0 || rel.Images[0].OldestOpenDays != 0 || rel.Images[0].AvgFixDays != 0 {
		t.Fatalf("releases = %+v", rel)
	}
	// clean image with no rebuilds still lists its project (has a scanned image).
	findProject(t, s, "isv:percona:common")
	// PR row present with 1 rebuild.
	pr := findProject(t, s, "isv:percona:PR:pr-9")
	if pr.Rebuilds != 1 {
		t.Fatalf("pr = %+v", pr)
	}
	// Sorted by rebuilds desc: ppg:17 first.
	if s.Projects[0].Project != "isv:percona:ppg:17" {
		t.Fatalf("sort order: %v", s.Projects[0].Project)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func findProject(t *testing.T, s OverviewSnapshot, name string) OverviewProject {
	t.Helper()
	for _, p := range s.Projects {
		if p.Project == name {
			return p
		}
	}
	t.Fatalf("project %s missing from snapshot: %+v", name, s.Projects)
	return OverviewProject{}
}

func TestOverviewHandlerWindowValidation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "isv:percona", newOverviewCache(time.Minute))

	for _, tc := range []struct {
		q    string
		code int
	}{{"", 200}, {"?window=24h", 200}, {"?window=48h", 200}, {"?window=7d", 200}, {"?window=1h", 400}} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, "/api/overview"+tc.q, nil))
		if w.Code != tc.code {
			t.Fatalf("window %q → %d, want %d", tc.q, w.Code, tc.code)
		}
	}
}

func TestOverviewHandlerCaches(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "isv:percona", newOverviewCache(time.Minute))

	// First request: empty snapshot.
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s1 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s1); err != nil {
		t.Fatal(err)
	}
	// Mutate the DB; a cached second request must NOT see it.
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES ('isv:percona:ppg:17','p','r','x86_64','building',?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s2 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s2); err != nil {
		t.Fatal(err)
	}
	if len(s2.Projects) != len(s1.Projects) {
		t.Fatalf("second request bypassed the cache: %d vs %d projects", len(s2.Projects), len(s1.Projects))
	}
}
```

(Add imports as needed: `encoding/json`, `net/http`, `net/http/httptest`, `time`, `github.com/percona/obs-dashboard/internal/store`.)

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Append to `backend/internal/api/overview.go`** (imports: `database/sql`, `context`, `encoding/json`, `net/http`, `sort`, `sync`, `time`, `github.com/percona/obs-dashboard/internal/store`):

```go
// ── snapshot types (snake_case JSON, app convention) ──

type OverviewCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type OverviewImage struct {
	Name           string `json:"name"`
	Critical       int    `json:"critical"`
	High           int    `json:"high"`
	OldestOpenDays int    `json:"oldest_open_days"` // 0 = none open / unknown
	AvgFixDays     int    `json:"avg_fix_days"`     // 0 = no closed episodes yet
}

type OverviewProject struct {
	Project    string          `json:"project"`
	Rebuilds   int             `json:"rebuilds"`
	TopPackage *OverviewCount  `json:"top_package,omitempty"`
	Images     []OverviewImage `json:"images"`
}

type OverviewSnapshot struct {
	Window                     string            `json:"window"`
	GeneratedAt                string            `json:"generated_at"`
	PreviousWindowRebuildTotal int               `json:"previous_window_rebuild_total"`
	TopRepo                    *OverviewCount    `json:"top_repo,omitempty"`
	Projects                   []OverviewProject `json:"projects"`
}

var overviewWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"48h": 48 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// buildOverviewSnapshot assembles the snapshot from raw store rows.
// Aggregation rules are the design spec's: rebuilds/top_package per logical
// project, top_repo global, per-image CVE counts as max across archs,
// oldest_open_days from the oldest non-nil CveSince among vulnerable archs,
// avg_fix_days as the rounded mean of the image's closed episodes.
func buildOverviewSnapshot(root, window string, now time.Time,
	cur, prev []store.BuildingEntry, scans []store.OverviewCveScan, periods []store.OverviewCvePeriod,
) OverviewSnapshot {
	type projAgg struct {
		rebuilds int
		pkgCount map[string]int
		images   map[string]*OverviewImage
	}
	agg := map[string]*projAgg{}
	getAgg := func(logical string) *projAgg {
		a, ok := agg[logical]
		if !ok {
			a = &projAgg{pkgCount: map[string]int{}, images: map[string]*OverviewImage{}}
			agg[logical] = a
		}
		return a
	}

	repoCount := map[string]int{}
	for _, e := range cur {
		logical := logicalProject(root, e.Project)
		if logical == "" {
			continue
		}
		a := getAgg(logical)
		a.rebuilds++
		a.pkgCount[e.Package]++
		repoCount[e.Repo]++
	}

	prevTotal := 0
	for _, e := range prev {
		if logicalProject(root, e.Project) != "" {
			prevTotal++
		}
	}

	// Per-image CVE aggregation: key = raw project + package.
	type imgKey struct{ project, pkg string }
	imgSince := map[imgKey]*time.Time{}
	imgAt := map[imgKey]*OverviewImage{}
	imgLogical := map[imgKey]string{}
	for _, s := range scans {
		logical := logicalProject(root, s.Project)
		if logical == "" {
			continue
		}
		k := imgKey{s.Project, s.Package}
		img, ok := imgAt[k]
		if !ok {
			img = &OverviewImage{Name: s.Package}
			imgAt[k] = img
			imgLogical[k] = logical
		}
		if s.Critical > img.Critical {
			img.Critical = s.Critical
		}
		if s.High > img.High {
			img.High = s.High
		}
		if (s.Critical > 0 || s.High > 0) && s.CveSince != nil {
			if cur := imgSince[k]; cur == nil || s.CveSince.Before(*cur) {
				imgSince[k] = s.CveSince
			}
		}
	}
	for k, since := range imgSince {
		imgAt[k].OldestOpenDays = int(now.Sub(*since).Hours() / 24)
	}

	// avg_fix_days per image from closed episodes.
	fixDays := map[imgKey][]float64{}
	for _, p := range periods {
		k := imgKey{p.Project, p.Package}
		fixDays[k] = append(fixDays[k], p.CleanSince.Sub(p.CveSince).Hours()/24)
	}
	for k, days := range fixDays {
		img, ok := imgAt[k]
		if !ok {
			continue // period for an image with no current scan row
		}
		sum := 0.0
		for _, d := range days {
			sum += d
		}
		img.AvgFixDays = int(sum/float64(len(days)) + 0.5)
	}

	for k, img := range imgAt {
		getAgg(imgLogical[k]).images[k.project+"/"+k.pkg] = img
	}

	// Assemble, sort, and derive per-project top packages.
	var projects []OverviewProject
	for logical, a := range agg {
		if a.rebuilds == 0 && len(a.images) == 0 {
			continue
		}
		p := OverviewProject{Project: logical, Rebuilds: a.rebuilds, Images: []OverviewImage{}}
		var topPkg string
		for name, n := range a.pkgCount {
			if n > a.pkgCount[topPkg] || topPkg == "" {
				topPkg = name
			}
		}
		if topPkg != "" {
			p.TopPackage = &OverviewCount{Name: topPkg, Count: a.pkgCount[topPkg]}
		}
		var names []string
		for k := range a.images {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p.Images = append(p.Images, *a.images[k])
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Rebuilds != projects[j].Rebuilds {
			return projects[i].Rebuilds > projects[j].Rebuilds
		}
		return projects[i].Project < projects[j].Project
	})

	snap := OverviewSnapshot{
		Window:                     window,
		GeneratedAt:                now.Format(time.RFC3339),
		PreviousWindowRebuildTotal: prevTotal,
		Projects:                   projects,
	}
	var topRepo string
	for name, n := range repoCount {
		if topRepo == "" || n > repoCount[topRepo] {
			topRepo = name
		}
	}
	if topRepo != "" {
		snap.TopRepo = &OverviewCount{Name: topRepo, Count: repoCount[topRepo]}
	}
	return snap
}

// ── cache (mirrors releaseArtifactsCache: TTL + singleflight) ──

type overviewCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]overviewCacheEntry
	inflight map[string]chan struct{}
}

type overviewCacheEntry struct {
	snapshot OverviewSnapshot
	expires  time.Time
	err      error
}

func newOverviewCache(ttl time.Duration) *overviewCache {
	return &overviewCache{ttl: ttl, entries: map[string]overviewCacheEntry{}, inflight: map[string]chan struct{}{}}
}

func (c *overviewCache) Get(ctx context.Context, key string, fetch func(context.Context) (OverviewSnapshot, error)) (OverviewSnapshot, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.snapshot, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return OverviewSnapshot{}, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.snapshot, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	snapshot, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = overviewCacheEntry{snapshot: snapshot, expires: expires, err: err}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return snapshot, err
}

// ── handler ──

// overviewHandler serves GET /api/overview?window=24h|48h|7d.
func overviewHandler(db *sql.DB, root string, cache *overviewCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		dur, ok := overviewWindows[window]
		if !ok {
			http.Error(w, "invalid window (24h|48h|7d)", http.StatusBadRequest)
			return
		}
		snap, err := cache.Get(r.Context(), window, func(ctx context.Context) (OverviewSnapshot, error) {
			now := time.Now().UTC()
			cur, err := store.QueryBuildingEntries(db, now.Add(-dur), now)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			prev, err := store.QueryBuildingEntries(db, now.Add(-2*dur), now.Add(-dur))
			if err != nil {
				return OverviewSnapshot{}, err
			}
			scans, err := store.QueryAllCveScans(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			periods, err := store.QueryAllCvePeriods(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			return buildOverviewSnapshot(root, window, now, cur, prev, scans, periods), nil
		})
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}
}
```

- [ ] **Step 4: Register the route** in `backend/internal/api/server.go` — add with the other cache instances:

```go
	overview := newOverviewCache(60 * time.Second)
```
and with the other top-level routes:
```go
	r.Get("/api/overview", overviewHandler(db, root, overview))
```

- [ ] **Step 5: Run** — `go test ./internal/api/ -run TestOverview -v && go test ./... && go vet ./...` → all PASS.

- [ ] **Step 6: Commit**

```bash
cd backend && git add internal/api/overview.go internal/api/overview_test.go internal/api/server.go
git commit -s -m "feat(api): overview snapshot endpoint with TTL cache"
```

```json:metadata
{"files": ["backend/internal/api/overview.go", "backend/internal/api/overview_test.go", "backend/internal/api/server.go"], "verifyCommand": "cd backend && go test ./internal/api/ -run TestOverview -v && go test ./...", "acceptanceCriteria": ["snapshot builder matches all aggregation rules incl. max-across-archs, oldest-open, avg-fix, inclusion + sorting", "window validation 400", "60s TTL+singleflight cache proven by DB-mutation test", "route registered"], "modelTier": "standard"}
```

---

### Task 4: Frontend tokens + types

**Goal:** `--crit`/`--high` (+tints) exist in both themes; `types/overview.ts` mirrors the endpoint.

**Files:**
- Modify: `frontend/src/assets/theme.css`
- Modify: `frontend/tailwind.config.ts` (only if it maps named colors — check; components may use `var()` arbitrary values instead)
- Create: `frontend/src/types/overview.ts`

**Acceptance Criteria:**
- [ ] `:root` (light) gains `--crit: #B0203A; --crit-tint: rgba(176,32,58,0.12); --high: #E5731C; --high-tint: rgba(229,115,28,0.14);` and `[data-theme="dark"]` gains `--crit: #FF5E7A; --crit-tint: rgba(255,94,122,0.16); --high: #FF9147; --high-tint: rgba(255,145,71,0.16);` (values from the UI spec §1).
- [ ] `types/overview.ts` (snake_case) compiles:

```ts
export type WindowKey = '24h' | '48h' | '7d'

export interface OverviewCount { name: string; count: number }

export interface OverviewImage {
  name: string
  critical: number
  high: number
  oldest_open_days: number   // 0 = none open / unknown
  avg_fix_days: number       // 0 = no closed episodes yet
}

export interface OverviewProject {
  project: string
  rebuilds: number
  top_package?: OverviewCount
  images: OverviewImage[]
}

export interface OverviewSnapshot {
  window: WindowKey
  generated_at: string
  previous_window_rebuild_total: number
  top_repo?: OverviewCount
  projects: OverviewProject[]
}
```

- [ ] `cd frontend && npm run build` passes.

**Verify:** `cd frontend && npm run build` → success

**Steps:** apply the two token blocks to `theme.css` (append inside the existing `:root` and `[data-theme="dark"]` blocks); create the types file with the code above; check `tailwind.config.ts` — if it maps token names to Tailwind colors, add `crit`/`high` mappings for consistency, otherwise skip (components will use `text-[var(--crit)]`-style arbitrary values like the rest of the app); build; commit:

```bash
git add frontend/src/assets/theme.css frontend/src/types/overview.ts frontend/tailwind.config.ts
git commit -s -m "feat(frontend): crit/high severity tokens and overview types"
```

```json:metadata
{"files": ["frontend/src/assets/theme.css", "frontend/src/types/overview.ts", "frontend/tailwind.config.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["crit/high + tints in both theme blocks with UI-spec values", "snake_case overview types compile", "npm run build passes"], "modelTier": "mechanical"}
```

---

### Task 5: `useOverviewData` composable

**Goal:** Fetch + realtime-refetch + all derived aggregates.

**Files:**
- Create: `frontend/src/composables/useOverviewData.ts`

**Acceptance Criteria:**
- [ ] Fetches `/api/overview?window=...` on mount and window change; exposes `snapshot`, `loading`, `error`.
- [ ] Opens its own `EventSource('/api/stream')` (the existing global stream) and debounce-refetches (2s) on any message; closes it on unmount.
- [ ] Derived computeds per the UI spec §4/§11: `totalRebuilds`, `rebuildDeltaPct` (0 when previous total is 0), `topPackage` (global max across projects), `totalCritical`, `totalHigh`, `affectedImageCount`, `avgFixDays` (mean over images with CVEs and non-zero avg), `oldestOpenDays` (max), `rebuildBars` (sorted desc with pct of max), plus `topRepo` passthrough.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → success

**Steps:** create the file with exactly:

```ts
import { ref, computed, watch, onMounted, onUnmounted, type Ref } from 'vue'
import type { OverviewSnapshot, OverviewCount, WindowKey } from '../types/overview'

export interface RebuildBar {
  project: string
  count: number
  pct: number // 0-100, normalized to the max bar
}

export function useOverviewData(window: Ref<WindowKey>) {
  const snapshot = ref<OverviewSnapshot | null>(null)
  const loading = ref(true)
  const error = ref<string | null>(null)

  async function fetchSnapshot() {
    try {
      const res = await fetch(`/api/overview?window=${window.value}`)
      if (!res.ok) throw new Error(res.statusText)
      snapshot.value = await res.json() as OverviewSnapshot
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'failed to load overview'
    } finally {
      loading.value = false
    }
  }

  // Realtime: any activity on the app's global stream schedules a debounced
  // refetch. We open our own EventSource on the shared endpoint (decision:
  // no dedicated overview stream).
  let es: EventSource | null = null
  let debounce: ReturnType<typeof setTimeout> | null = null
  function onStreamMessage() {
    if (debounce) clearTimeout(debounce)
    debounce = setTimeout(fetchSnapshot, 2000)
  }
  onMounted(() => {
    fetchSnapshot()
    es = new EventSource('/api/stream')
    es.onmessage = onStreamMessage
  })
  onUnmounted(() => {
    es?.close()
    if (debounce) clearTimeout(debounce)
  })
  watch(window, () => { loading.value = true; fetchSnapshot() })

  const projects = computed(() => snapshot.value?.projects ?? [])
  const allImages = computed(() => projects.value.flatMap(p => p.images))

  const totalRebuilds = computed(() => projects.value.reduce((s, p) => s + p.rebuilds, 0))
  const rebuildDeltaPct = computed(() => {
    const prev = snapshot.value?.previous_window_rebuild_total ?? 0
    if (prev === 0) return 0
    return Math.round((totalRebuilds.value - prev) / prev * 100)
  })
  const topPackage = computed<{ name: string; count: number; project: string } | null>(() => {
    let best: { name: string; count: number; project: string } | null = null
    for (const p of projects.value) {
      if (p.top_package && (!best || p.top_package.count > best.count)) {
        best = { ...p.top_package, project: p.project }
      }
    }
    return best
  })
  const topRepo = computed<OverviewCount | null>(() => snapshot.value?.top_repo ?? null)
  const totalCritical = computed(() => allImages.value.reduce((s, i) => s + i.critical, 0))
  const totalHigh = computed(() => allImages.value.reduce((s, i) => s + i.high, 0))
  const affectedImageCount = computed(() =>
    allImages.value.filter(i => i.critical + i.high > 0).length)
  const avgFixDays = computed(() => {
    const days = allImages.value
      .filter(i => i.critical + i.high > 0 && i.avg_fix_days > 0)
      .map(i => i.avg_fix_days)
    if (days.length === 0) return 0
    return Math.round(days.reduce((a, b) => a + b) / days.length)
  })
  const oldestOpenDays = computed(() =>
    allImages.value.reduce((m, i) => Math.max(m, i.oldest_open_days), 0))
  const rebuildBars = computed<RebuildBar[]>(() => {
    const withRebuilds = projects.value.filter(p => p.rebuilds > 0)
    const max = Math.max(1, ...withRebuilds.map(p => p.rebuilds))
    return withRebuilds
      .slice()
      .sort((a, b) => b.rebuilds - a.rebuilds)
      .map(p => ({ project: p.project, count: p.rebuilds, pct: Math.round(p.rebuilds / max * 100) }))
  })

  return {
    snapshot, loading, error,
    totalRebuilds, rebuildDeltaPct, topPackage, topRepo,
    totalCritical, totalHigh, affectedImageCount, avgFixDays, oldestOpenDays,
    rebuildBars, projects,
  }
}
```

Build, then commit:

```bash
git add frontend/src/composables/useOverviewData.ts
git commit -s -m "feat(frontend): useOverviewData composable"
```

```json:metadata
{"files": ["frontend/src/composables/useOverviewData.ts"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["fetch on mount/window change with loading/error", "own EventSource on /api/stream with 2s debounce refetch, closed on unmount", "all derived aggregates per UI spec §11", "npm run build passes"], "modelTier": "standard"}
```

---

### Task 6: Overview components

**Goal:** The four components rendering the approved design with app tokens.

**Files:**
- Create: `frontend/src/components/StatCard.vue`
- Create: `frontend/src/components/RebuildBarChart.vue`
- Create: `frontend/src/components/CveExposureTable.vue`
- Create: `frontend/src/components/OverviewPanel.vue`

**Pixel authority (the implementer MUST read these):**
- UI spec: `/home/rdias/Downloads/obs-dashboard-overview/overview-panel-spec.md` (§§6-13 — exact paddings, sizes, weights, a11y)
- Approved mockup HTML/CSS/JS: `docs/superpowers/specs/2026-07-07-overview-mockup.html (repo-relative)` (translate its structure/classes to Vue + Tailwind arbitrary values)
- Reference images: `/home/rdias/Downloads/obs-dashboard-overview/spec-assets/*.png`

**Token mapping (mockup → app):** `--bg-app→--bg-app`, `--bg-card→--bg-card`, `--bg-card-2→--bg-card-2`, `--bg-muted→--bg-muted`, `--text-*→--text-*`, `--border→--border`, `--brand→--brand-purple`, `--brand-tint→--brand-purple-tint`, `--ok/--fail/--info` (+tints) → same names, `--crit/--high` (+tints) → added in Task 4. Mono = the app's `font-mono` utility (`--font-mono`). Use Tailwind arbitrary values (`bg-[var(--crit-tint)]`) matching the app's existing component style.

**Component contracts:**

```
StatCard.vue      props: label: string   — slots: #icon (chip content), #value, #footnote
                  chip tint via prop: tint: 'brand' | 'info' | 'crit' | 'ok' | 'high'
RebuildBarChart   props: bars: RebuildBar[]; windowLabel: string; accentOf: (project: string) => string
CveExposureTable  props: projects: OverviewProject[]; accentOf: (project: string) => string
                  local reactive Record<string, boolean> expansion keyed by project path
OverviewPanel     owns: window ref (prop w/ v-model from App), useOverviewData, PROJECT_ACCENTS
                  props: overviewWindow: WindowKey; emits update:overviewWindow
```

**Key logic to carry over from the mockup verbatim:**
- `PROJECT_ACCENTS = ['#6E3FF3','#2A78D4','#1F9D55','#E08A00','#B0203A']`; `accentOf` assigns by first-appearance ordinal of the project in the snapshot (stable within a snapshot), cycling with `%`.
- `ageColor(days)`: ≥45 `var(--crit)`, ≥21 `var(--high)`, else `var(--text-secondary)`; `ageLabel(days)`: `0 → '—'`, else `` `${days}d` ``.
- Severity badge: value >0 → `text-[var(--crit)] bg-[var(--crit-tint)]` / `text-[var(--high)] bg-[var(--high-tint)]`; zero → `text-text-muted bg-bg-muted`. `min-w-[30px] text-center text-[13px] font-bold px-2 py-0.5 rounded-md`.
- Bars: grid `[215px_1fr_54px]`, track `h-[22px] bg-bg-muted rounded-md`, fill width `pct%` + accent + `transition-[width] duration-300`, `aria-label="{project} — {count} rebuilds"`.
- Table: shared grid `[1fr_90px_90px_130px_130px] gap-3`; project rows are `<button aria-expanded aria-controls>`; expanded region `bg-bg-card-2`, image rows `pl-10`; caret `▸` with `rotate-90` when open; per-project aggregate = Σ crit/high, max oldest, mean avg-fix over images-with-CVEs (`—` if none).
- Stat cards row: `grid grid-cols-5 gap-3.5` (collapse 5→3→2 like the mockup media queries — Tailwind responsive prefixes); the five cards and their footnotes exactly as the mockup (delta pill `▲/▼ {abs}%` green/red; "MOST REBUILT" name mono 16px; "MOST REBUILT REPO" same shape; "OPEN CVES" big total + two pills; "AVG CVE FIX TIME" + "oldest open: Nd" in `--high`).
- Header: P-tile (`bg-[var(--brand-purple)]`), title "Overview", subline, `WINDOW` micro-label + segmented 24h/48h/7d pills (`aria-pressed`, active = `bg-bg-card text-[var(--brand-purple)] font-bold shadow-[0_1px_2px_rgba(0,0,0,0.10)]`). **No theme toggle** (decision).
- Loading: skeleton 5 cards / 6 bar rows / 5 table rows (`animate-pulse bg-bg-muted` blocks); error: small inline banner `text-[var(--fail)] bg-[var(--fail-tint)]` above the cards, non-blocking.
- Empty states: no bars → "No rebuilds in this window" muted line; project with `—` avg-fix per rules.

**Acceptance Criteria:**
- [ ] All four components exist and compose per the contracts; `OverviewPanel` renders header → cards → chart → table with `gap-4`, `max-w-[1360px] mx-auto`.
- [ ] Age colors, badge muting, bar normalization, expansion behaviour (multiple open, survives refetch — keyed by project path), and a11y attributes match the UI spec §§8-13.
- [ ] No hardcoded theme hex in components (accents are data, allowed); all surfaces via tokens.
- [ ] `cd frontend && npm run build` passes.

**Verify:** `cd frontend && npm run build` → success

**Steps:** read the three pixel-authority files; build `StatCard.vue` first (leaf), then `RebuildBarChart.vue`, `CveExposureTable.vue`, then `OverviewPanel.vue` composing them with `useOverviewData`; translate mockup CSS to Tailwind arbitrary values with the token mapping; build; commit:

```bash
git add frontend/src/components/StatCard.vue frontend/src/components/RebuildBarChart.vue frontend/src/components/CveExposureTable.vue frontend/src/components/OverviewPanel.vue
git commit -s -m "feat(frontend): overview components (stat cards, rebuild chart, CVE table)"
```

```json:metadata
{"files": ["frontend/src/components/StatCard.vue", "frontend/src/components/RebuildBarChart.vue", "frontend/src/components/CveExposureTable.vue", "frontend/src/components/OverviewPanel.vue"], "verifyCommand": "cd frontend && npm run build", "acceptanceCriteria": ["four components per contracts composing header/cards/chart/table", "age colors, badge muting, bar normalization, expansion, a11y per UI spec", "tokens only (accents are data)", "npm run build passes"], "modelTier": "standard"}
```

---

### Task 7: Tab integration

**Goal:** Overview is the third main tab with URL state.

**Files:**
- Modify: `frontend/src/App.vue` (mainTab union + state + render block)
- Modify: `frontend/src/components/AppHeader.vue` (tab pill + prop type)
- Modify: `frontend/src/composables/useUrlState.ts` (accept `tab=overview`; persist `owin`)

**Acceptance Criteria:**
- [ ] `mainTab` type becomes `'board' | 'artifacts' | 'overview'` in `App.vue` (line ~17), `AppHeader.vue` props/emits, and `useUrlState.ts` (`UrlStateOptions.mainTab` + the hydrate check at ~line 47-48 + the persist at ~line 106).
- [ ] `AppHeader` gains an "Overview" pill after "Artifacts" (same `tab-pill` classes/pattern as the existing two).
- [ ] `App.vue` holds `overviewWindow = ref<WindowKey>('24h')`, renders `<OverviewPanel v-else-if="mainTab === 'overview'" :overview-window="overviewWindow" @update:overview-window="overviewWindow = $event" />`.
- [ ] `useUrlState` persists `owin` when ≠ '24h' and hydrates it (validate against `['24h','48h','7d']`); options struct gains `overviewWindow: Ref<WindowKey>`.
- [ ] Deep link `?tab=overview&owin=7d` restores tab + window; `npm run build` passes; `go test ./...` untouched-green.

**Verify:** `cd frontend && npm run build && cd ../backend && go test ./...` → success

**Steps:** extend the three union types; add the pill; wire the panel + window ref; extend `useUrlState` (hydrate: `if (tab === 'board' || tab === 'artifacts' || tab === 'overview')`; add `owin` param handling mirroring how `aversion` is handled); build; commit:

```bash
git add frontend/src/App.vue frontend/src/components/AppHeader.vue frontend/src/composables/useUrlState.ts
git commit -s -m "feat(frontend): overview main tab with URL state"
```

```json:metadata
{"files": ["frontend/src/App.vue", "frontend/src/components/AppHeader.vue", "frontend/src/composables/useUrlState.ts"], "verifyCommand": "cd frontend && npm run build && cd ../backend && go test ./...", "acceptanceCriteria": ["mainTab union extended in all three files", "Overview pill added", "OverviewPanel wired with window v-model", "owin URL param hydrates+persists", "build + backend suite green"], "modelTier": "standard"}
```

---

## Manual verification (after all tasks, user-run)

`docker compose up --build`, open the dashboard → Overview tab: five cards populate (delta `—`/0% acceptable while the previous window is thin), bars show real logical projects, CVE table shows the release-container exposure (high counts, `—` ages for pre-age-tracking scans), rows expand/collapse and survive SSE refetches, window switching updates rebuild numbers, both themes clean, `?tab=overview&owin=7d` deep link works.

## Self-Review

**Spec coverage:** endpoint+shape → T3; grouping table → T1; queries → T2; caching → T3; tokens/types → T4; composable+realtime → T5; components §§6-13 → T6; tab+URL → T7; branch-from-origin/main → T1 Step 0. ✓
**Placeholders:** T6 references authoritative design files instead of inlining four full SFCs — deliberate: the UI spec §§6-13 and the mockup file ARE the complete pixel specification, and the task pins contracts, token mapping, and every behavioural rule. All other tasks carry complete code. ✓
**Type consistency:** `logicalProject(root, project)` (T1/T3); `store.BuildingEntry`/`OverviewCveScan`/`OverviewCvePeriod` (T2/T3); `OverviewSnapshot` snake_case JSON (T3) ↔ `types/overview.ts` (T4) ↔ composable (T5); `RebuildBar` (T5/T6); `WindowKey`/`overviewWindow`/`owin` (T5/T6/T7). ✓
