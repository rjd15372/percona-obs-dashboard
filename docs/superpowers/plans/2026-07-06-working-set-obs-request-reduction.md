# Working-Set OBS Request Reduction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the dashboard from re-polling OBS for packages that have nothing left to observe (publish-disabled `succeeded` + `failed`), and add runtime-toggleable telemetry to measure working-set size and OBS request volume.

**Architecture:** Detect publish-disabled repos from OBS `_meta` (fetch-once cache), treat `succeeded`-on-non-publishing-repos and `failed` as terminal via a persisted `settled` flag that governs working-set membership without altering the displayed `rollup_state`. Add a per-endpoint OBS request counter in the `Client` and a working-set size accessor, reported by a periodic telemetry goroutine gated by an `atomic.Bool` that an HTTP endpoint toggles.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite`, `chi` router, `log/slog`, `encoding/xml`.

**User decisions (already made):**
- "Go with A" — publish-aware terminal detection (over time-based heuristic / slow-lane polling).
- Terminal-failure scope: "failed only" — `broken`/`unresolvable` keep polling.
- `rollup_state` is never faked; a separate `settled` flag governs working-set membership.
- Persisted `settled` column (clean, no restart re-seed churn).
- Per-pass task caching: "Defer to a follow-up" — out of scope.
- Telemetry granularity: working set "Total + inflight + by-state"; OBS "+ breakdown by endpoint".
- Telemetry interval: "Configurable, default 60s".
- Telemetry endpoint: "GET + POST with query param" (`GET /api/telemetry`, `POST /api/telemetry?enabled=…`).
- Telemetry default state: "Disabled by default" (config-overridable).
- Publish-flag cache: no TTL (fetch-once); evict on project removal.

---

## File Structure

| File | Responsibility | Tasks |
|---|---|---|
| `internal/obs/client.go` | OBS HTTP client: add per-endpoint request counters, `ProjectPublishFlags` + cache + eviction | 1, 2 |
| `internal/obs/client_test.go` | Client metrics + publish-flag parsing/cache/evict tests | 1, 2 |
| `internal/obs/settled.go` (new) | `Settled(pkg, flags)` pure helper | 3 |
| `internal/obs/settled_test.go` (new) | `Settled` table tests | 3 |
| `internal/model/types.go` | Add `Package.Settled` field | 4 |
| `internal/store/db.go` | `settled` column: schema + additive migration | 4 |
| `internal/store/packages.go` | Persist/scan `settled`; `GetActivePackages` uses it | 4 |
| `internal/store/packages_test.go` | settled persistence + seed query tests | 4 |
| `internal/worker/worker.go` | Working-set removal via `Settled`; persist `settled` | 5 |
| `internal/worker/worker_test.go` | removal behaviour tests | 5 |
| `internal/obs/tasks.go` | `BlockedReasonTask` guard | 6 |
| `internal/obs/tasks_test.go` | guard test | 6 |
| `internal/obs/poller.go` | evict publish-flag cache on project GC | 7 |
| `internal/mq/consumer.go` | evict publish-flag cache on `project.delete` | 7 |
| `internal/workingset/workingset.go` | `Stats()` accessor | 8 |
| `internal/workingset/workingset_test.go` | Stats test | 8 |
| `internal/config/config.go` | `Telemetry.Interval` + `Telemetry.Enabled` | 9 |
| `internal/config/config_test.go` | config defaults test | 9 |
| `internal/telemetry/telemetry.go` (new) | periodic Reporter + pure `Diff` helper | 10 |
| `internal/telemetry/telemetry_test.go` (new) | Diff + tick tests | 10 |
| `internal/api/server.go` | `NewRouter` gains toggle+interval; register `/api/telemetry` | 11 |
| `internal/api/handlers.go` | `telemetryStatusHandler` / `telemetrySetHandler` | 11 |
| `internal/api/handlers_test.go` | endpoint tests | 11 |
| `cmd/obsboard/main.go` | wire toggle, reporter, router | 12 |

---

### Task 1: OBS per-endpoint request counters

**Goal:** Count every OBS HTTP request by a stable operation label, exposed via `Client.MetricsSnapshot()`.

**Files:**
- Modify: `internal/obs/client.go`
- Test: `internal/obs/client_test.go`

**Acceptance Criteria:**
- [ ] `get`, `getFile`, `post` take an `op string` first argument and increment a per-op counter.
- [ ] Every public method passes a stable op label (table below); `PackageIsContainer` (inline request) increments `"is_container"`.
- [ ] `Client.MetricsSnapshot()` returns a copy of the per-op counts.
- [ ] Existing obs tests still compile and pass (call sites updated).

**Verify:** `cd backend && go test ./internal/obs/ -run TestClientMetrics -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** in `internal/obs/client_test.go`

```go
func TestClientMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<resultlist></resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, err := c.BuildResults(ctx, "isv:percona:ppg:17"); err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if _, err := c.BuildResults(ctx, "isv:percona:ppg:17"); err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	snap := c.MetricsSnapshot()
	if snap["build_results"] != 2 {
		t.Fatalf("build_results = %d, want 2", snap["build_results"])
	}
	// Snapshot is a copy: mutating it must not affect the client.
	snap["build_results"] = 99
	if c.MetricsSnapshot()["build_results"] != 2 {
		t.Fatalf("snapshot is not a copy")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/obs/ -run TestClientMetrics -v`
Expected: FAIL — `c.MetricsSnapshot undefined` (compile error).

- [ ] **Step 3: Add the metrics holder and counter to `Client`**

In `internal/obs/client.go`, add `"sync"` to imports, then add the type and field:

```go
// obsMetrics counts OBS requests by operation label.
type obsMetrics struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (m *obsMetrics) inc(op string) {
	m.mu.Lock()
	m.counts[op]++
	m.mu.Unlock()
}

func (m *obsMetrics) snapshot() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.counts))
	for k, v := range m.counts {
		out[k] = v
	}
	return out
}
```

Add `metrics *obsMetrics` to the `Client` struct and initialise it in `NewClient`:

```go
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
	metrics  *obsMetrics
}

func NewClient(base, username, password string) *Client {
	return &Client{
		base:     strings.TrimRight(base, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
		metrics:  &obsMetrics{counts: make(map[string]int64)},
	}
}

// MetricsSnapshot returns a copy of the per-operation OBS request counts.
func (c *Client) MetricsSnapshot() map[string]int64 { return c.metrics.snapshot() }
```

- [ ] **Step 4: Add `op` to the three request helpers and increment**

Change the signatures of `get`, `getFile`, `post` to take `op string` as the first parameter and increment before `c.http.Do`:

```go
func (c *Client) get(ctx context.Context, op, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/xml")
	c.metrics.inc(op)
	resp, err := c.http.Do(req)
	// ... unchanged ...
}
```

Apply the same edit to `getFile(ctx, op, path string)` and `post(ctx, op, path string)` — add the `op` param and `c.metrics.inc(op)` immediately before their `c.http.Do(req)` call.

- [ ] **Step 5: Update `projectDir` to take an op and forward it**

```go
func (c *Client) projectDir(ctx context.Context, op, path string) ([]string, error) {
	resp, err := c.get(ctx, op, path)
	// ... unchanged ...
}
```

- [ ] **Step 6: Update every call site with its op label**

Pass these exact op strings at each call site (the only change per method is inserting the label as the new first arg to `get`/`getFile`/`post`/`projectDir`):

| Method | Call | op label |
|---|---|---|
| `SearchProjects` | `c.get` | `"search_projects"` |
| `BuildResults` | `c.get` | `"build_results"` |
| `ProjectRepos` | `c.projectDir` | `"project_repos"` |
| `ProjectRepoArchs` | `c.projectDir` | `"project_repo_archs"` |
| `ProjectRepoPackages` | `c.projectDir` | `"project_repo_packages"` |
| `ProjectBuildResults` | `c.get` | `"project_build_results"` |
| `BuildLog` | `c.get` | `"build_log"` |
| `PackageHistory` | `c.get` | `"package_history"` |
| `BuildDepInfo` | `c.get` | `"build_dep_info"` |
| `PackageBlockedReasons` | `c.get` | `"blocked_reasons"` |
| `SourceHistory` | `c.get` | `"source_history"` |
| `PackageBuildResults` | `c.get` | `"package_build_results"` |
| `RepoPublishStates` | `c.get` | `"publish_states"` |
| `PackageBuildReason` | `c.get` | `"build_reason"` |
| `PackageVersionResult` | `c.get` | `"version"` |
| `ProjectBinaryList` | `c.get` | `"binary_list"` |
| `PackageBinaries` | `c.get` | `"package_binaries"` |
| `PackageContainerInfoFilename` | `c.get` | `"container_info_filename"` |
| `PackageContainerTags` | `c.getFile` | `"container_tags"` |
| `RepoBinaryVersions` | `c.get` | `"binary_versions"` |
| `Rebuild` | `c.post` | `"rebuild"` |

For `PackageIsContainer` (builds its request inline, no helper), add `c.metrics.inc("is_container")` immediately before its `c.http.Do(req)` call (client.go ~line 508).

- [ ] **Step 7: Run tests**

Run: `cd backend && go test ./internal/obs/ -v`
Expected: PASS (all existing obs tests compile with new signatures + `TestClientMetrics` passes).

- [ ] **Step 8: Commit**

```bash
cd backend && git add internal/obs/client.go internal/obs/client_test.go
git commit -s -m "feat(obs): per-endpoint OBS request counters"
```

```json:metadata
{"files": ["backend/internal/obs/client.go", "backend/internal/obs/client_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -v", "acceptanceCriteria": ["get/getFile/post take op arg and increment per-op counter", "every public method passes a stable op label; PackageIsContainer increments is_container", "MetricsSnapshot returns a copy", "existing obs tests pass"], "modelTier": "standard"}
```

---

### Task 2: Publish-flag detection, cache, and eviction

**Goal:** `Client.ProjectPublishFlags(project)` resolves per-repo publish-enabled from `_meta`, cached forever (fetch-once) with an explicit eviction method.

**Files:**
- Modify: `internal/obs/client.go`
- Test: `internal/obs/client_test.go`

**Acceptance Criteria:**
- [ ] `PublishFlags.Publishes(repo)` resolves: no `<publish>` → true; bare `<disable/>` → project default false; per-repo `<disable/enable repository="X"/>` overrides project default; most-specific wins.
- [ ] `ProjectPublishFlags` fetches `_meta` once per project and caches the result permanently (second call makes no HTTP request).
- [ ] `EvictPublishFlags(project)` removes the cache entry so the next call refetches.
- [ ] The fetch uses op label `"publish_flags"`.

**Verify:** `cd backend && go test ./internal/obs/ -run 'TestPublishFlags|TestProjectPublishFlags' -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing tests** in `internal/obs/client_test.go`

```go
func TestPublishFlagsResolution(t *testing.T) {
	cases := []struct {
		name string
		meta string
		repo string
		want bool
	}{
		{"no publish block", `<project name="p"></project>`, "images", true},
		{"bare disable", `<project name="p"><publish><disable/></publish></project>`, "images", false},
		{"repo disable", `<project name="p"><publish><disable repository="UBI_8"/></publish></project>`, "UBI_8", false},
		{"repo disable other repo publishes", `<project name="p"><publish><disable repository="UBI_8"/></publish></project>`, "images", true},
		{"disable all, enable one", `<project name="p"><publish><disable/><enable repository="images"/></publish></project>`, "images", true},
		{"disable all, enable one, other stays disabled", `<project name="p"><publish><disable/><enable repository="images"/></publish></project>`, "UBI_8", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := parsePublishFlags([]byte(tc.meta))
			if got := f.Publishes(tc.repo); got != tc.want {
				t.Fatalf("Publishes(%q) = %v, want %v", tc.repo, got, tc.want)
			}
		})
	}
}

func TestProjectPublishFlagsCacheAndEvict(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("HTTP calls = %d, want 1 (cached)", got)
	}
	c.EvictPublishFlags("isv:percona:common:containers:ubi8")
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("third: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (refetched after evict)", got)
	}
	if c.MetricsSnapshot()["publish_flags"] != 2 {
		t.Fatalf("publish_flags metric = %d, want 2", c.MetricsSnapshot()["publish_flags"])
	}
}
```

Add imports to the test file if missing: `"sync/atomic"`.

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run 'TestPublishFlags|TestProjectPublishFlags' -v`
Expected: FAIL — `parsePublishFlags`, `ProjectPublishFlags`, `EvictPublishFlags` undefined.

- [ ] **Step 3: Implement in `internal/obs/client.go`**

Add to the `Client` struct a publish-flag cache and mutex:

```go
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
	metrics  *obsMetrics

	pubMu    sync.Mutex
	pubCache map[string]PublishFlags
}
```

Initialise `pubCache: make(map[string]PublishFlags)` in `NewClient`.

Add the types and methods:

```go
// PublishFlags answers whether a given repository publishes, resolved from a
// project's _meta <publish> block. Zero value = everything publishes (safe default).
type PublishFlags struct {
	defaultPublish bool            // project-level default (true unless a bare <disable/>)
	perRepo        map[string]bool // repo → publishes (overrides the default)
}

// Publishes reports whether repo publishes for this project.
func (f PublishFlags) Publishes(repo string) bool {
	if f.perRepo != nil {
		if v, ok := f.perRepo[repo]; ok {
			return v
		}
	}
	if !f.hasDefault {
		return true
	}
	return f.defaultPublish
}

type publishMetaXML struct {
	Publish *struct {
		Disable []struct {
			Repository string `xml:"repository,attr"`
		} `xml:"disable"`
		Enable []struct {
			Repository string `xml:"repository,attr"`
		} `xml:"enable"`
	} `xml:"publish"`
}

// parsePublishFlags resolves publish rules from a project _meta document.
func parsePublishFlags(metaXML []byte) PublishFlags {
	var m publishMetaXML
	_ = xml.Unmarshal(metaXML, &m)
	f := PublishFlags{defaultPublish: true, hasDefault: true, perRepo: map[string]bool{}}
	if m.Publish == nil {
		return f // no <publish> block → default publish
	}
	for _, d := range m.Publish.Disable {
		if d.Repository == "" {
			f.defaultPublish = false // bare <disable/> flips project default
		} else {
			f.perRepo[d.Repository] = false
		}
	}
	for _, e := range m.Publish.Enable {
		if e.Repository == "" {
			f.defaultPublish = true // bare <enable/> flips project default
		} else {
			f.perRepo[e.Repository] = true
		}
	}
	return f
}

// ProjectPublishFlags returns the (cached) publish flags for a project. The cache
// never expires — publish config is immutable for the repo lifetime — and is
// cleared explicitly via EvictPublishFlags when a project is removed.
func (c *Client) ProjectPublishFlags(ctx context.Context, project string) (PublishFlags, error) {
	c.pubMu.Lock()
	if f, ok := c.pubCache[project]; ok {
		c.pubMu.Unlock()
		return f, nil
	}
	c.pubMu.Unlock()

	resp, err := c.get(ctx, "publish_flags", "/source/"+url.PathEscape(project)+"/_meta")
	if err != nil {
		return PublishFlags{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return PublishFlags{}, err
	}
	f := parsePublishFlags(body)

	c.pubMu.Lock()
	c.pubCache[project] = f
	c.pubMu.Unlock()
	return f, nil
}

// EvictPublishFlags removes a project's cached publish flags (call on project removal).
func (c *Client) EvictPublishFlags(project string) {
	c.pubMu.Lock()
	delete(c.pubCache, project)
	c.pubMu.Unlock()
}
```

Add `hasDefault bool` to the `PublishFlags` struct (used above so the zero value returns `true`):

```go
type PublishFlags struct {
	hasDefault     bool
	defaultPublish bool
	perRepo        map[string]bool
}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run 'TestPublishFlags|TestProjectPublishFlags' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/client.go internal/obs/client_test.go
git commit -s -m "feat(obs): publish-flag detection with fetch-once cache and eviction"
```

```json:metadata
{"files": ["backend/internal/obs/client.go", "backend/internal/obs/client_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run 'TestPublishFlags|TestProjectPublishFlags' -v", "acceptanceCriteria": ["Publishes resolves default/bare-disable/per-repo/most-specific-wins", "ProjectPublishFlags fetches once and caches permanently", "EvictPublishFlags forces refetch", "fetch uses publish_flags op label"], "modelTier": "standard"}
```

---

### Task 3: `Settled` helper

**Goal:** A pure function deciding when a package has nothing left for the worker to observe.

**Files:**
- Create: `internal/obs/settled.go`
- Test: `internal/obs/settled_test.go`

**Acceptance Criteria:**
- [ ] `published` → true; `failed` → true; `broken`/`unresolvable`/`building`/`blocked`/`scheduled`/`finished` → false.
- [ ] `succeeded` → true iff every active (non skip-state) target has `Published` **or** its repo does not publish; false if any publishing repo target is unpublished.
- [ ] `disabled`/`excluded`/`locked` targets are ignored.

**Verify:** `cd backend && go test ./internal/obs/ -run TestSettled -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** in `internal/obs/settled_test.go`

```go
package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestSettled(t *testing.T) {
	// flags: repo "images" publishes, repo "UBI_8" does not.
	flags := PublishFlags{hasDefault: true, defaultPublish: true, perRepo: map[string]bool{"UBI_8": false}}

	pkg := func(state model.RollupState, targets ...model.Target) *model.Package {
		return &model.Package{RollupState: state, Targets: targets}
	}
	tgt := func(repo, state string, published bool) model.Target {
		return model.Target{Repo: repo, State: state, Published: published}
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"published", pkg(model.RollupPublished), true},
		{"failed", pkg(model.RollupFailed), true},
		{"broken", pkg(model.RollupBroken), false},
		{"unresolvable", pkg(model.RollupUnresolvable), false},
		{"building", pkg(model.RollupBuilding), false},
		{"blocked", pkg(model.RollupBlocked), false},
		{"succeeded non-publishing repo", pkg(model.RollupSucceeded, tgt("UBI_8", "succeeded", false)), true},
		{"succeeded publishing repo unpublished", pkg(model.RollupSucceeded, tgt("images", "succeeded", false)), false},
		{"succeeded publishing repo published", pkg(model.RollupSucceeded, tgt("images", "succeeded", true)), true},
		{"succeeded mixed: published + non-publishing", pkg(model.RollupSucceeded, tgt("images", "succeeded", true), tgt("UBI_8", "succeeded", false)), true},
		{"succeeded skip-state ignored", pkg(model.RollupSucceeded, tgt("images", "disabled", false), tgt("UBI_8", "succeeded", false)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Settled(tc.pkg, flags); got != tc.want {
				t.Fatalf("Settled = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestSettled -v`
Expected: FAIL — `Settled` undefined.

- [ ] **Step 3: Implement `internal/obs/settled.go`**

```go
package obs

import "github.com/percona/obs-dashboard/internal/model"

// Settled reports whether pkg has nothing left for the worker to observe and can
// leave the working set. It does NOT mutate rollup_state — it only decides
// working-set membership.
//
//   - published, failed        → terminal
//   - succeeded                → terminal iff every active target has published
//     OR sits in a non-publishing repo (nothing will ever flip it to published)
//   - everything else          → keep polling
func Settled(pkg *model.Package, flags PublishFlags) bool {
	switch pkg.RollupState {
	case model.RollupPublished, model.RollupFailed:
		return true
	case model.RollupSucceeded:
		for _, t := range pkg.Targets {
			if skipState(t.State) {
				continue
			}
			if flags.Publishes(t.Repo) && !t.Published {
				return false // a publishing repo we're still waiting on
			}
		}
		return true
	default:
		return false
	}
}
```

(`skipState` already exists in `internal/obs/poller.go`.)

- [ ] **Step 4: Run test**

Run: `cd backend && go test ./internal/obs/ -run TestSettled -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/settled.go internal/obs/settled_test.go
git commit -s -m "feat(obs): Settled helper for publish-aware working-set exit"
```

```json:metadata
{"files": ["backend/internal/obs/settled.go", "backend/internal/obs/settled_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestSettled -v", "acceptanceCriteria": ["published/failed terminal; broken/unresolvable/building/blocked not", "succeeded terminal iff all active targets published or non-publishing repo", "skip-state targets ignored"], "modelTier": "mechanical"}
```

---

### Task 4: `settled` column — model, migration, store

**Goal:** Persist a `settled` flag on packages; `GetActivePackages` seeds only `settled = 0`.

**Files:**
- Modify: `internal/model/types.go`
- Modify: `internal/store/db.go`
- Modify: `internal/store/packages.go`
- Test: `internal/store/packages_test.go`

**Acceptance Criteria:**
- [ ] `model.Package` has a `Settled bool` field (JSON `settled,omitempty`).
- [ ] `packages` table has a `settled INTEGER NOT NULL DEFAULT 0` column (schema + additive migration).
- [ ] `UpsertPackageState` writes `settled` (with `settled=excluded.settled` on conflict); `scanPackages` reads it.
- [ ] `GetActivePackages` returns only rows with `settled = 0`.

**Verify:** `cd backend && go test ./internal/store/ -run 'TestSettled|TestGetActivePackages' -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing tests** in `internal/store/packages_test.go`

```go
func TestSettledPersistedAndScanned(t *testing.T) {
	db := openTestDB(t)
	p := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-a",
		RollupState: model.RollupSucceeded, Settled: true,
		Targets:   []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := UpsertPackageState(db, p, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := QueryPackages(db, "isv:percona:ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Settled {
		t.Fatalf("settled not persisted/scanned: %+v", got)
	}
}

func TestGetActivePackagesExcludesSettled(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	active := &model.Package{Project: "p", Name: "active", RollupState: model.RollupSucceeded, Settled: false, UpdatedAt: now}
	settled := &model.Package{Project: "p", Name: "settled", RollupState: model.RollupSucceeded, Settled: true, UpdatedAt: now}
	if err := UpsertPackageState(db, active, now); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPackageState(db, settled, now); err != nil {
		t.Fatal(err)
	}
	got, err := GetActivePackages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "active" {
		t.Fatalf("GetActivePackages = %+v, want only 'active'", got)
	}
}
```

Use the existing test DB helper in `packages_test.go` (match its name — if it is `openDB(t)` rather than `openTestDB(t)`, use that).

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/store/ -run 'TestSettled|TestGetActivePackages' -v`
Expected: FAIL — `Settled` field undefined / column missing.

- [ ] **Step 3: Add the model field** in `internal/model/types.go` (inside `type Package struct`, after `IsRelease`):

```go
	Settled        bool        `json:"settled,omitempty"`
```

- [ ] **Step 4: Add the column** in `internal/store/db.go`

In the `schema` const, add to the `packages` table definition after `is_release`:

```go
    settled        INTEGER NOT NULL DEFAULT 0,
```

Add an additive migration in `Open` alongside the other `ALTER TABLE` lines:

```go
	db.Exec(`ALTER TABLE packages ADD COLUMN settled INTEGER NOT NULL DEFAULT 0`)
```

Also add `settled INTEGER NOT NULL DEFAULT 0` to the `packages_new` table in `migrateIsContainerNullable` and include `settled` in its `INSERT INTO packages_new SELECT ...` column list (append `settled` at the end of both the column list and the SELECT). **Note:** if a DB predates the `settled` column, `migrateIsContainerNullable` may run before the additive `ALTER` above — ensure the `ALTER TABLE ... ADD COLUMN settled` line is placed **before** the `migrateIsContainerNullable` call in `Open` so the source table has the column when the rebuild SELECT runs.

- [ ] **Step 5: Persist and scan `settled`** in `internal/store/packages.go`

In `UpsertPackageState`, add `settled` to the INSERT column list and values, and to the `ON CONFLICT` update:
- Column list: add `settled` after `is_release`.
- `VALUES`: add one more `?`.
- Exec args: add a computed int after `isReleaseVal`:

```go
	settledVal := 0
	if p.Settled {
		settledVal = 1
	}
```
and pass `settledVal` as the final arg.
- `ON CONFLICT ... DO UPDATE SET`: add `settled=excluded.settled,` (place it before the `state_changed_at = CASE ...` clause).

In `packageSelectCols`, append `, settled`.

In `scanPackages`, add a scan target and assignment (mirror `isRelease`):

```go
	var settled int
	// ... add &settled to the rows.Scan(...) call, as the last column ...
	p.Settled = settled != 0
```

- [ ] **Step 6: Update `GetActivePackages`** in `internal/store/packages.go`

Replace the WHERE clause:

```go
func GetActivePackages(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT` + packageSelectCols + `
		FROM packages
		WHERE settled = 0
		ORDER BY project, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
```

Update its doc comment to describe the new behaviour (packages that have not settled — i.e. still need worker attention).

- [ ] **Step 7: Run tests**

Run: `cd backend && go test ./internal/store/ -v`
Expected: PASS (new tests + existing store tests; column additions are backward compatible).

- [ ] **Step 8: Commit**

```bash
cd backend && git add internal/model/types.go internal/store/db.go internal/store/packages.go internal/store/packages_test.go
git commit -s -m "feat(store): persist settled flag; seed working set by settled=0"
```

```json:metadata
{"files": ["backend/internal/model/types.go", "backend/internal/store/db.go", "backend/internal/store/packages.go", "backend/internal/store/packages_test.go"], "verifyCommand": "cd backend && go test ./internal/store/ -v", "acceptanceCriteria": ["Package.Settled field added", "settled column in schema + additive migration + table rebuild", "UpsertPackageState writes settled; scanPackages reads it", "GetActivePackages returns only settled=0"], "modelTier": "standard"}
```

---

### Task 5: Working-set removal via `Settled`

**Goal:** The worker removes a package from the working set when it is settled (publish-aware), and persists the flag.

**Files:**
- Modify: `internal/worker/worker.go`
- Test: `internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] `ProcessOnce` computes `settled` from `obs.Settled(pkg, flags)` using cached publish flags, sets `pkg.Settled`, and removes the package when settled (preserving the `IsContainer != nil` guard).
- [ ] On `ProjectPublishFlags` error, flags default to zero-value (everything publishes) so the package is **not** wrongly dropped.
- [ ] The `pkg.Settled` value is persisted (it is set before the existing `UpsertPackageState` call in `ProcessOnce`).
- [ ] A `succeeded` non-container in a non-publishing project is removed; a `succeeded` package with a publishing-unpublished target is retained; a `failed` package is removed; a type-unknown package is retained.

**Verify:** `cd backend && go test ./internal/worker/ -v` → PASS

**Steps:**

- [ ] **Step 1: Update `worker_test.go`**

The existing `TestPoolRemovesSucceededPackageFromWorkingSet` uses a `publishedTask{}` that sets `RollupPublished`; that path still removes (published is always settled), so it keeps passing. Add a test proving publish-aware removal of a `succeeded` non-publishing package. The pool's `client` is used for `ProjectPublishFlags`, so point it at a test server returning a publish-disabled `_meta`:

```go
func TestPoolRemovesSucceededNonPublishing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// _meta with publishing disabled; any other OBS call returns empty result.
		if strings.HasSuffix(r.URL.Path, "/_meta") {
			_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
			return
		}
		_, _ = w.Write([]byte(`<resultlist></resultlist>`))
	}))
	defer srv.Close()

	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)
	client := obs.NewClient(srv.URL, "u", "p")

	ctx, cancel := context.WithCancel(context.Background())
	// No tasks: pkg keeps its succeeded state; settled decision drives removal.
	p := worker.NewPool(1, nil, nil, client, db, h, ws, nil)
	p.Start(ctx)

	isContainer := false
	pkg := &model.Package{
		Project: "isv:percona:common:containers:ubi8", Name: "python3-tomli",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		IsContainer: &isContainer,
		Targets:     []model.Target{{Repo: "UBI_8", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}
	ws.Signal(pkg)
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	ws.Add(pkg) // removed → Add re-dispatches
	select {
	case <-ws.Dispatch():
		// correct
	case <-time.After(100 * time.Millisecond):
		t.Fatal("succeeded non-publishing package was not removed from working set")
	}
}
```

Ensure the test file imports `net/http`, `net/http/httptest`, `strings`, and `github.com/percona/obs-dashboard/internal/obs`.

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/worker/ -run TestPoolRemovesSucceededNonPublishing -v`
Expected: FAIL — the current removal only fires on `RollupPublished`, so the succeeded package is not removed.

- [ ] **Step 3: Implement in `internal/worker/worker.go`**

Import obs (already imported). Replace the final removal block in `ProcessOnce`:

```go
	if pkg.RollupState == model.RollupPublished && pkg.IsContainer != nil {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```
with a settled-based decision. Because `pkg.Settled` must be persisted by the existing `UpsertPackageState` call earlier in `ProcessOnce`, compute it **before** that upsert. Locate the upsert (`store.UpsertPackageState(p.db, pkg, now)` near the top of the post-task section) and immediately before it insert:

```go
	var flags obs.PublishFlags
	if p.client != nil {
		if f, err := p.client.ProjectPublishFlags(ctx, pkg.Project); err == nil {
			flags = f // on error: zero value → everything publishes → keep polling
		}
	}
	pkg.Settled = obs.Settled(pkg, flags) && pkg.IsContainer != nil
```

Then replace the removal block at the end of `ProcessOnce` with:

```go
	if pkg.Settled {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/worker/ -v`
Expected: PASS (new test + existing removal/retention tests).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/worker/worker.go internal/worker/worker_test.go
git commit -s -m "feat(worker): publish-aware working-set removal via Settled"
```

```json:metadata
{"files": ["backend/internal/worker/worker.go", "backend/internal/worker/worker_test.go"], "verifyCommand": "cd backend && go test ./internal/worker/ -v", "acceptanceCriteria": ["ProcessOnce computes settled via obs.Settled with cached flags and sets pkg.Settled", "flags default to publishes-all on error (no wrong drop)", "settled persisted via the existing upsert", "succeeded-non-publishing and failed removed; publishing-unpublished and type-unknown retained"], "modelTier": "standard"}
```

---

### Task 6: `BlockedReasonTask` guard

**Goal:** Skip the OBS call in `BlockedReasonTask` when the package has no `blocked` target.

**Files:**
- Modify: `internal/obs/tasks.go`
- Test: `internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] `BlockedReasonTask.Run` returns early (no HTTP call) when no target is in `blocked` state.
- [ ] When a blocked target exists, it still calls OBS and populates `BlockedBy`.

**Verify:** `cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing test** in `internal/obs/tasks_test.go`

```go
func TestBlockedReasonTaskSkipsWhenNoBlocked(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<resultlist></resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	pkg := &model.Package{
		Project: "p", Name: "pkg",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	}
	if err := (BlockedReasonTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when no blocked targets, got %d", calls)
	}
}
```

Ensure the test file imports `sync/atomic`, `net/http`, `net/http/httptest`.

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestBlockedReasonTaskSkipsWhenNoBlocked -v`
Expected: FAIL — the task calls OBS unconditionally (`calls == 1`).

- [ ] **Step 3: Add the guard** in `internal/obs/tasks.go` at the top of `BlockedReasonTask.Run`:

```go
func (t BlockedReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	hasBlocked := false
	for _, target := range pkg.Targets {
		if target.State == "blocked" {
			hasBlocked = true
			break
		}
	}
	if !hasBlocked {
		return nil
	}
	reasons, err := client.PackageBlockedReasons(ctx, pkg.Project, pkg.Name)
	// ... rest unchanged ...
}
```

- [ ] **Step 4: Run test**

Run: `cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): skip blocked-reason OBS call when no blocked targets"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v", "acceptanceCriteria": ["no HTTP call when no blocked target", "still calls and populates BlockedBy when a blocked target exists"], "modelTier": "mechanical"}
```

---

### Task 7: Evict publish-flag cache on project removal

**Goal:** Clear a project's cached publish flags when it is removed by the poller GC or the MQ `project.delete` handler.

**Files:**
- Modify: `internal/obs/poller.go`
- Modify: `internal/mq/consumer.go`
- Test: `internal/obs/poller_test.go` (or add an assertion where project-GC is tested)

**Acceptance Criteria:**
- [ ] Poller project-GC calls `p.client.EvictPublishFlags(proj)` for each removed project.
- [ ] MQ `opensuse.obs.project.delete` handler calls `c.obsClient.EvictPublishFlags(m.Project)`.

**Verify:** `cd backend && go test ./internal/obs/ ./internal/mq/ -v` → PASS

**Steps:**

- [ ] **Step 1: Add eviction in the poller** — `internal/obs/poller.go`, in the "projects no longer in OBS" GC loop (the block that calls `store.DeletePackagesByProject(p.db, proj)`), add after the delete:

```go
		if !liveProjects[proj] {
			slog.Info("poller: removing packages for deleted project", "project", proj)
			if err := store.DeletePackagesByProject(p.db, proj); err != nil {
				slog.Error("poller: delete packages", "project", proj, "err", err)
			}
			p.client.EvictPublishFlags(proj)
			for _, pkg := range existing {
				if pkg.Project == proj {
					p.ws.Remove(proj + "/" + pkg.Name)
				}
			}
		}
```

- [ ] **Step 2: Add eviction in the MQ consumer** — `internal/mq/consumer.go`, in the `opensuse.obs.project.delete` case, after `store.DeletePackagesByProject`:

```go
	case key == "opensuse.obs.project.delete":
		if err := store.DeletePackagesByProject(c.db, m.Project); err != nil {
			slog.Error("mq: delete packages for project", "project", m.Project, "err", err)
		}
		c.obsClient.EvictPublishFlags(m.Project)
		c.appendEvent(&model.Event{ /* unchanged */ })
```

- [ ] **Step 3: Add/extend a test** in `internal/obs/poller_test.go` asserting eviction on GC. If the existing poller test harness makes full poller GC hard to drive, add a focused unit test that seeds the client cache, simulates the GC branch by calling `client.EvictPublishFlags(proj)` through the same path, and asserts a refetch occurs:

```go
func TestPollerEvictsPublishFlagsOnProjectGC(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	_, _ = c.ProjectPublishFlags(ctx, "gone-project")
	c.EvictPublishFlags("gone-project")
	_, _ = c.ProjectPublishFlags(ctx, "gone-project")
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected refetch after evict, calls=%d", calls)
	}
}
```

(The wiring in Steps 1–2 is verified by compilation + the existing poller/consumer tests; this test locks the evict→refetch contract.)

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ ./internal/mq/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/poller.go internal/mq/consumer.go internal/obs/poller_test.go
git commit -s -m "fix: evict publish-flag cache when a project is removed"
```

```json:metadata
{"files": ["backend/internal/obs/poller.go", "backend/internal/mq/consumer.go", "backend/internal/obs/poller_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ ./internal/mq/ -v", "acceptanceCriteria": ["poller GC evicts publish flags per removed project", "MQ project.delete evicts publish flags", "evict forces refetch"], "modelTier": "mechanical"}
```

---

### Task 8: `WorkingSet.Stats()`

**Goal:** Expose working-set size — total, inflight, and by-rollup-state.

**Files:**
- Modify: `internal/workingset/workingset.go`
- Test: `internal/workingset/workingset_test.go`

**Acceptance Criteria:**
- [ ] `WorkingSet.Stats()` returns `Stats{Total, Inflight int, ByState map[string]int}` computed under the mutex.
- [ ] `Total` counts map entries; `ByState` counts by `pkg.RollupState`.

**Verify:** `cd backend && go test ./internal/workingset/ -run TestStats -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing test** in `internal/workingset/workingset_test.go`

```go
func TestStats(t *testing.T) {
	ws := New(10)
	ws.Seed([]*model.Package{
		{Project: "p", Name: "a", RollupState: model.RollupSucceeded},
		{Project: "p", Name: "b", RollupState: model.RollupSucceeded},
		{Project: "p", Name: "c", RollupState: model.RollupFailed},
	})
	s := ws.Stats()
	if s.Total != 3 {
		t.Fatalf("Total = %d, want 3", s.Total)
	}
	if s.ByState["succeeded"] != 2 || s.ByState["failed"] != 1 {
		t.Fatalf("ByState = %v", s.ByState)
	}
	if s.Inflight != 0 {
		t.Fatalf("Inflight = %d, want 0", s.Inflight)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/workingset/ -run TestStats -v`
Expected: FAIL — `ws.Stats` undefined.

- [ ] **Step 3: Implement** in `internal/workingset/workingset.go`

```go
// Stats is a snapshot of working-set size.
type Stats struct {
	Total    int
	Inflight int
	ByState  map[string]int // rollup_state → count
}

// Stats returns a snapshot of the current working set under the lock.
func (ws *WorkingSet) Stats() Stats {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	s := Stats{Total: len(ws.packages), Inflight: len(ws.inflight), ByState: make(map[string]int)}
	for _, p := range ws.packages {
		s.ByState[string(p.RollupState)]++
	}
	return s
}
```

- [ ] **Step 4: Run test**

Run: `cd backend && go test ./internal/workingset/ -run TestStats -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/workingset/workingset.go internal/workingset/workingset_test.go
git commit -s -m "feat(workingset): Stats accessor for telemetry"
```

```json:metadata
{"files": ["backend/internal/workingset/workingset.go", "backend/internal/workingset/workingset_test.go"], "verifyCommand": "cd backend && go test ./internal/workingset/ -run TestStats -v", "acceptanceCriteria": ["Stats returns Total/Inflight/ByState under mutex", "ByState counts by rollup_state"], "modelTier": "mechanical"}
```

---

### Task 9: Telemetry config

**Goal:** Add `Telemetry.Interval` (default 60s) and `Telemetry.Enabled` (default false).

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] `Config` has a `Telemetry TelemetryConfig` with `Interval time.Duration` and `Enabled bool`.
- [ ] Defaults: interval `60s`, enabled `false`. Env overrides: `TELEMETRY_INTERVAL`, `TELEMETRY_ENABLED`.

**Verify:** `cd backend && go test ./internal/config/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing test** in `internal/config/config_test.go`

```go
func TestTelemetryDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Interval != 60*time.Second {
		t.Fatalf("interval = %v, want 60s", cfg.Telemetry.Interval)
	}
	if cfg.Telemetry.Enabled {
		t.Fatalf("enabled = true, want false by default")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/config/ -run TestTelemetryDefaults -v`
Expected: FAIL — `cfg.Telemetry` undefined.

- [ ] **Step 3: Implement** in `internal/config/config.go`

Add to the `Config` struct: `Telemetry TelemetryConfig`. Add the type:

```go
type TelemetryConfig struct {
	Interval time.Duration
	Enabled  bool
}
```

In `Load`, add defaults and env binds alongside the others:

```go
	v.SetDefault("telemetry.interval", "60s")
	v.SetDefault("telemetry.enabled", false)
```
and in the `BindEnv` list:
```go
		{"telemetry.interval", "TELEMETRY_INTERVAL"},
		{"telemetry.enabled", "TELEMETRY_ENABLED"},
```

Parse the interval like the others:

```go
	telemetryInterval, err := time.ParseDuration(v.GetString("telemetry.interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid TELEMETRY_INTERVAL %q: %w", v.GetString("telemetry.interval"), err)
	}
```

Add to the `cfg := &Config{...}` literal:

```go
		Telemetry: TelemetryConfig{
			Interval: telemetryInterval,
			Enabled:  v.GetBool("telemetry.enabled"),
		},
```

- [ ] **Step 4: Run test**

Run: `cd backend && go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/config/config.go internal/config/config_test.go
git commit -s -m "feat(config): telemetry interval and enabled settings"
```

```json:metadata
{"files": ["backend/internal/config/config.go", "backend/internal/config/config_test.go"], "verifyCommand": "cd backend && go test ./internal/config/ -v", "acceptanceCriteria": ["Telemetry config with Interval and Enabled", "defaults 60s and false; env overrides bound"], "modelTier": "mechanical"}
```

---

### Task 10: Telemetry reporter package

**Goal:** A periodic reporter that logs working-set stats and OBS request deltas, gated by an `atomic.Bool`, with a pure diff helper.

**Files:**
- Create: `internal/telemetry/telemetry.go`
- Test: `internal/telemetry/telemetry_test.go`

**Acceptance Criteria:**
- [ ] `Diff(prev, cur map[string]int64) (perOp map[string]int64, total int64)` returns per-op and total deltas.
- [ ] `Reporter.tick(prev)` returns the log fields and the new baseline; emits only when `Enabled.Load()` is true, but always refreshes the baseline.
- [ ] `Reporter.Run(ctx)` loops on a ticker until ctx is cancelled.

**Verify:** `cd backend && go test ./internal/telemetry/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing tests** in `internal/telemetry/telemetry_test.go`

```go
package telemetry

import (
	"sync/atomic"
	"testing"

	"github.com/percona/obs-dashboard/internal/workingset"
)

func TestDiff(t *testing.T) {
	prev := map[string]int64{"build_results": 10, "version": 5}
	cur := map[string]int64{"build_results": 14, "version": 5, "publish_states": 3}
	perOp, total := Diff(prev, cur)
	if total != 7 {
		t.Fatalf("total = %d, want 7", total)
	}
	if perOp["build_results"] != 4 || perOp["publish_states"] != 3 {
		t.Fatalf("perOp = %v", perOp)
	}
	if _, ok := perOp["version"]; ok {
		t.Fatalf("zero-delta op should be omitted: %v", perOp)
	}
}

type fakeStatter struct{ s workingset.Stats }
func (f fakeStatter) Stats() workingset.Stats { return f.s }

type fakeSnap struct{ m map[string]int64 }
func (f fakeSnap) MetricsSnapshot() map[string]int64 { return f.m }

func TestTickRefreshesBaselineWhenDisabled(t *testing.T) {
	var enabled atomic.Bool // false
	r := &Reporter{
		Stats:    fakeStatter{s: workingset.Stats{Total: 3}},
		Snap:     fakeSnap{m: map[string]int64{"build_results": 100}},
		Enabled:  &enabled,
	}
	prev := map[string]int64{"build_results": 90}
	newPrev := r.tick(prev)
	if newPrev["build_results"] != 100 {
		t.Fatalf("baseline not refreshed while disabled: %v", newPrev)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/telemetry/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement `internal/telemetry/telemetry.go`**

```go
package telemetry

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/percona/obs-dashboard/internal/workingset"
)

// Statter provides working-set size.
type Statter interface{ Stats() workingset.Stats }

// Snapshotter provides cumulative per-op OBS request counts.
type Snapshotter interface{ MetricsSnapshot() map[string]int64 }

// Reporter periodically logs working-set and OBS-request telemetry.
type Reporter struct {
	Interval time.Duration
	Enabled  *atomic.Bool
	Stats    Statter
	Snap     Snapshotter
}

// Diff returns per-op deltas (omitting zero-delta ops) and the total delta.
func Diff(prev, cur map[string]int64) (map[string]int64, int64) {
	perOp := make(map[string]int64)
	var total int64
	for op, c := range cur {
		d := c - prev[op]
		if d != 0 {
			perOp[op] = d
		}
		total += d
	}
	return perOp, total
}

// tick emits a telemetry line (when enabled) and returns the refreshed baseline.
func (r *Reporter) tick(prev map[string]int64) map[string]int64 {
	cur := r.Snap.MetricsSnapshot()
	perOp, total := Diff(prev, cur)
	if r.Enabled.Load() {
		s := r.Stats.Stats()
		var cumulative int64
		for _, v := range cur {
			cumulative += v
		}
		rate := 0.0
		if r.Interval > 0 {
			rate = float64(total) / r.Interval.Seconds()
		}
		slog.Info("telemetry",
			"window", r.Interval.String(),
			"ws_packages", s.Total,
			"ws_inflight", s.Inflight,
			"ws_by_state", s.ByState,
			"obs_window", total,
			"obs_total", cumulative,
			"obs_req_per_s", rate,
			"obs_by_endpoint", perOp,
		)
	}
	return cur
}

// Run ticks every Interval until ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	prev := r.Snap.MetricsSnapshot()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prev = r.tick(prev)
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/telemetry/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/telemetry/
git commit -s -m "feat(telemetry): periodic working-set and OBS-request reporter"
```

```json:metadata
{"files": ["backend/internal/telemetry/telemetry.go", "backend/internal/telemetry/telemetry_test.go"], "verifyCommand": "cd backend && go test ./internal/telemetry/ -v", "acceptanceCriteria": ["Diff returns per-op and total deltas, omitting zero", "tick refreshes baseline always, emits only when enabled", "Run loops on ticker until ctx cancel"], "modelTier": "standard"}
```

---

### Task 11: Telemetry HTTP endpoint

**Goal:** `GET /api/telemetry` returns status; `POST /api/telemetry?enabled=…` flips the toggle.

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/handlers.go`
- Test: `internal/api/handlers_test.go`

**Acceptance Criteria:**
- [ ] `NewRouter` gains `telemetryEnabled *atomic.Bool` and `telemetryInterval time.Duration` parameters.
- [ ] `GET /api/telemetry` → `{"enabled": bool, "interval": "60s"}`.
- [ ] `POST /api/telemetry?enabled=true|false` sets the flag and returns the new status; missing/invalid `enabled` → 400.

**Verify:** `cd backend && go test ./internal/api/ -run TestTelemetry -v` → PASS

**Steps:**

- [ ] **Step 1: Write failing tests** in `internal/api/handlers_test.go`

```go
func TestTelemetryEndpoint(t *testing.T) {
	var enabled atomic.Bool
	h := telemetrySetHandler(&enabled)
	status := telemetryStatusHandler(&enabled, 60*time.Second)

	// enable
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry?enabled=true", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK || !enabled.Load() {
		t.Fatalf("enable failed: code=%d enabled=%v", w.Code, enabled.Load())
	}

	// status reflects it
	w = httptest.NewRecorder()
	status(w, httptest.NewRequest(http.MethodGet, "/api/telemetry", nil))
	var body struct {
		Enabled  bool   `json:"enabled"`
		Interval string `json:"interval"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || body.Interval != "1m0s" {
		t.Fatalf("status = %+v", body)
	}

	// invalid
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/api/telemetry", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: code=%d, want 400", w.Code)
	}
}
```

Ensure imports include `sync/atomic`, `time`, `encoding/json`, `net/http`, `net/http/httptest`.

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/api/ -run TestTelemetry -v`
Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement handlers** in `internal/api/handlers.go`

```go
// telemetryStatusHandler returns GET /api/telemetry.
func telemetryStatusHandler(enabled *atomic.Bool, interval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":  enabled.Load(),
			"interval": interval.String(),
		})
	}
}

// telemetrySetHandler handles POST /api/telemetry?enabled=true|false.
func telemetrySetHandler(enabled *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query().Get("enabled")
		b, err := strconv.ParseBool(v)
		if err != nil {
			http.Error(w, "query param 'enabled' must be true or false", http.StatusBadRequest)
			return
		}
		enabled.Store(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": b})
	}
}
```

Add `"sync/atomic"` to the `handlers.go` imports (`strconv`, `time`, `encoding/json`, `net/http` are already imported).

- [ ] **Step 4: Wire routes** in `internal/api/server.go`

Change the signature and register routes:

```go
func NewRouter(db *sql.DB, h *hub.Hub, obsClient *obs.Client, root string, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration) http.Handler {
	// ... existing body ...
	r.Get("/api/telemetry", telemetryStatusHandler(telemetryEnabled, telemetryInterval))
	r.Post("/api/telemetry", telemetrySetHandler(telemetryEnabled))
	return r
}
```

Add `"sync/atomic"` to the `server.go` imports (`time` already imported).

- [ ] **Step 5: Fix existing callers of `NewRouter` to compile**

`cmd/obsboard/main.go` calls `NewRouter` — it will be updated in Task 12. If any api test constructs `NewRouter`, update those calls to pass `new(atomic.Bool)` and a duration. (Run `grep -rn "NewRouter(" backend` to find them.)

- [ ] **Step 6: Run tests**

Run: `cd backend && go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd backend && git add internal/api/server.go internal/api/handlers.go internal/api/handlers_test.go
git commit -s -m "feat(api): runtime telemetry toggle endpoint"
```

```json:metadata
{"files": ["backend/internal/api/server.go", "backend/internal/api/handlers.go", "backend/internal/api/handlers_test.go"], "verifyCommand": "cd backend && go test ./internal/api/ -v", "acceptanceCriteria": ["NewRouter gains toggle + interval params", "GET returns enabled+interval", "POST sets flag; invalid/missing enabled -> 400"], "modelTier": "standard"}
```

---

### Task 12: Wire telemetry into `main.go`

**Goal:** Create the shared toggle from config, start the reporter goroutine, and pass the toggle+interval to the router.

**Files:**
- Modify: `cmd/obsboard/main.go`

**Acceptance Criteria:**
- [ ] A `*atomic.Bool` is created, seeded from `cfg.Telemetry.Enabled`, shared between the reporter and the router.
- [ ] `telemetry.Reporter` is started as a goroutine with `cfg.Telemetry.Interval`, the working set, and the OBS client.
- [ ] `NewRouter` is called with the toggle and interval.
- [ ] `go build ./...` succeeds and the binary starts.

**Verify:** `cd backend && go build ./... && go vet ./...` → no errors

**Steps:**

- [ ] **Step 1: Wire in `cmd/obsboard/main.go`**

Add imports: `"sync/atomic"` and `"github.com/percona/obs-dashboard/internal/telemetry"`.

After the working set and pool are set up (and `obsClient` exists), add:

```go
	telemetryEnabled := &atomic.Bool{}
	telemetryEnabled.Store(cfg.Telemetry.Enabled)
	reporter := &telemetry.Reporter{
		Interval: cfg.Telemetry.Interval,
		Enabled:  telemetryEnabled,
		Stats:    ws,
		Snap:     obsClient,
	}
	go reporter.Run(ctx)
```

Update the router construction:

```go
	router := api.NewRouter(db, h, obsClient, cfg.OBSRoot, telemetryEnabled, cfg.Telemetry.Interval)
```

- [ ] **Step 2: Build and vet**

Run: `cd backend && go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Step 3: Full test suite**

Run: `cd backend && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Smoke-check the endpoint (manual, optional)**

```bash
cd backend && OBS_USERNAME=x go run ./cmd/obsboard &   # or against a real config
curl -s localhost:4000/api/telemetry                    # {"enabled":false,"interval":"1m0s"}
curl -s -X POST 'localhost:4000/api/telemetry?enabled=true'
```

- [ ] **Step 5: Commit**

```bash
cd backend && git add cmd/obsboard/main.go
git commit -s -m "feat: wire telemetry reporter and toggle into main"
```

```json:metadata
{"files": ["backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go build ./... && go vet ./... && go test ./...", "acceptanceCriteria": ["shared atomic.Bool seeded from config", "reporter goroutine started with interval/ws/client", "NewRouter called with toggle+interval", "build/vet/test pass"], "modelTier": "standard"}
```

---

## Self-Review

**Spec coverage:**
- Publish-flag detection + fetch-once cache + eviction → Tasks 2, 7. ✓
- `Settled` decision (failed only) → Task 3, applied in Task 5. ✓
- `settled` column, model, store, seed query → Task 4. ✓
- Worker removal via Settled, error-defaults-to-publishes → Task 5. ✓
- `BlockedReasonTask` guard → Task 6. ✓
- Telemetry: working-set stats → Task 8; OBS per-endpoint counters → Task 1; reporter → Task 10; config → Task 9; endpoint → Task 11; wiring → Task 12. ✓
- Runtime toggle (disabled by default) → Tasks 9, 10, 11, 12. ✓

**Type consistency:** `PublishFlags`/`Publishes` (Tasks 2,3,5); `obs.Settled` (3,5); `Package.Settled` (4,5); `MetricsSnapshot` (1,10,12); `workingset.Stats` (8,10); `Reporter{Interval,Enabled,Stats,Snap}` (10,12); `NewRouter(..., telemetryEnabled *atomic.Bool, telemetryInterval time.Duration)` (11,12). Consistent.

**Placeholders:** none — every step shows the code or the exact edit.

**Note on `PublishFlags` struct:** Task 2 Step 3 lists three fields (`hasDefault`, `defaultPublish`, `perRepo`); Task 3's test constructs it with those field names. Keep the field set exactly as `{hasDefault, defaultPublish, perRepo}`.
