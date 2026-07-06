# Task-Chain Per-Pass Caching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Skip the `BuildReason`/`BlockedReason`/`Version`/`ContainerTags` OBS re-fetch when the relevant target state is unchanged since the last fetch.

**Architecture:** `BuildStateTask` becomes the single invalidation point — it preserves per-target enrichment only while a target's state is unchanged (wiping it on transition) and sets a positively-confirmed `Package.TargetsStable` flag. Downstream tasks skip when their field is already populated (plus a 5-minute freshness TTL for `BlockedBy`). The cache lives in two in-memory-only `json:"-"` model fields carried across passes by the working set's shared `*model.Package` pointer — no DB schema change, no API change, no `Task` interface change.

**Tech Stack:** Go 1.22+, `httptest` for OBS fakes, existing `internal/obs` task-chain patterns.

**User decisions (already made):**
- "go with A" — invalidation localized in `BuildStateTask`; downstream tasks trust populated fields; no `Task` interface change, no stateful tasks.
- BlockedBy: "Re-fetch at reduced cadence" — cached with a **5-minute constant TTL** (not configurable); stamp-on-value-set so empty-details targets keep today's retry behaviour.
- "Yes, include it" — `ContainerTagsTask` gets the same `TargetsStable` rule; the release chain never sets the flag → release containers keep fetching every pass (accepted, no regression).
- Conservative polarity everywhere: every cold-start path (restart, MQ replace, first sighting) defaults to *fetch*.
- Spec approved: `docs/superpowers/specs/2026-07-06-task-chain-caching-design.md`.

---

## File Structure

| File | Responsibility | Tasks |
|---|---|---|
| `backend/internal/model/types.go` | Two in-memory cache fields (`Target.BlockedByFetchedAt`, `Package.TargetsStable`) | 1 |
| `backend/internal/model/types_test.go` (new) | JSON-exclusion invariant test | 1 |
| `backend/internal/obs/tasks.go` | Conditional preservation + `TargetsStable` (BuildStateTask); skip guards (BuildReason, BlockedReason, Version, ContainerTags) | 2, 3, 4, 5 |
| `backend/internal/obs/tasks_test.go` | Task-level cache-behaviour tests (external package `obs_test`) | 2, 3, 4, 5 |

Note for all tasks: `tasks_test.go` is `package obs_test` (external) — reference symbols as `obs.X`/`model.X`; unexported constants (e.g. the TTL) cannot be referenced from tests, use duration literals.

---

### Task 1: In-memory cache fields on the model

**Goal:** Add `Target.BlockedByFetchedAt` and `Package.TargetsStable` as `json:"-"` fields, with a test locking the "never serialized" invariant (which is what keeps them out of both the `targets_json` DB column and API responses — both serialize via `json.Marshal` of these structs).

**Files:**
- Modify: `backend/internal/model/types.go`
- Create: `backend/internal/model/types_test.go`

**Acceptance Criteria:**
- [ ] `model.Target` has `BlockedByFetchedAt time.Time` tagged `json:"-"`.
- [ ] `model.Package` has `TargetsStable bool` tagged `json:"-"`.
- [ ] A test proves `json.Marshal` of both structs emits neither field (checks both Go name and any snake_case form).
- [ ] `go build ./...` and full `go test ./...` stay green.

**Verify:** `cd backend && go test ./internal/model/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** — create `backend/internal/model/types_test.go`:

```go
package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The two cache fields are in-memory only: excluded from JSON, and therefore
// from both the targets_json DB column and API responses (both use json.Marshal).
func TestCacheFieldsExcludedFromJSON(t *testing.T) {
	tgt := Target{
		Repo: "UBI_9", Arch: "x86_64", State: "blocked",
		BlockedBy:          "not installable",
		BlockedByFetchedAt: time.Now(),
	}
	b, err := json.Marshal(tgt)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"BlockedByFetchedAt", "blocked_by_fetched_at"} {
		if strings.Contains(string(b), needle) {
			t.Fatalf("Target JSON must not contain %q: %s", needle, b)
		}
	}

	pkg := Package{
		Project: "p", Name: "n",
		TargetsStable: true,
		UpdatedAt:     time.Now(),
	}
	b, err = json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TargetsStable", "targets_stable"} {
		if strings.Contains(string(b), needle) {
			t.Fatalf("Package JSON must not contain %q: %s", needle, b)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/model/ -v`
Expected: FAIL — compile error, `BlockedByFetchedAt`/`TargetsStable` undefined.

- [ ] **Step 3: Add the fields** in `backend/internal/model/types.go`.

In `type Target struct`, after the `Published` field:

```go
	// BlockedByFetchedAt records when BlockedBy was last fetched from OBS.
	// In-memory only (json:"-"): excluded from targets_json storage and API
	// responses; zero after restart/MQ-replace → conservative refetch.
	BlockedByFetchedAt time.Time `json:"-"`
```

In `type Package struct`, after the `Settled` field:

```go
	// TargetsStable is set by BuildStateTask each worker pass: true only when
	// the previous pass had the same target set with identical states.
	// In-memory only (json:"-"); the zero value (false) on any cold-start path
	// forces downstream tasks to fetch.
	TargetsStable bool `json:"-"`
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/model/ -v && go build ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/model/types.go internal/model/types_test.go
git commit -s -m "feat(model): in-memory cache fields for task-chain caching"
```

```json:metadata
{"files": ["backend/internal/model/types.go", "backend/internal/model/types_test.go"], "verifyCommand": "cd backend && go test ./internal/model/ -v && go test ./...", "acceptanceCriteria": ["Target.BlockedByFetchedAt time.Time json:\"-\"", "Package.TargetsStable bool json:\"-\"", "json.Marshal emits neither field", "module builds and full suite green"], "modelTier": "mechanical"}
```

---

### Task 2: `BuildStateTask` — state-conditional preservation + `TargetsStable`

**Goal:** Make `BuildStateTask`'s enrichment preservation conditional on the target's state being unchanged (state transition → wipe, forcing downstream refetch), and compute `pkg.TargetsStable` with conservative polarity.

**Files:**
- Modify: `backend/internal/obs/tasks.go` (BuildStateTask.Run, currently ~lines 40-63)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] State unchanged → `BlockedBy`, `BuildReason`, `BuildReasonPackages`, `BlockedByFetchedAt` all carried forward.
- [ ] State changed → all four left at zero values (wiped).
- [ ] `TargetsStable` is `true` iff the previous pass had ≥1 target, the target set (repo/arch) is identical, and every state matched; `false` on: no previous targets, any state change, target added, target removed.
- [ ] Existing `TestBuildStateTask` still passes.

**Verify:** `cd backend && go test ./internal/obs/ -run TestBuildStateTask -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/obs/tasks_test.go`:

```go
// resultXML builds a single-package resultlist with one <result> per (repo, arch, code).
func resultXML(entries [][3]string) string {
	var sb strings.Builder
	sb.WriteString(`<resultlist>`)
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf(
			`<result project="isv:percona" repository="%s" arch="%s" state="building">
				<status package="mypkg" code="%s"/>
			</result>`, e[0], e[1], e[2]))
	}
	sb.WriteString(`</resultlist>`)
	return sb.String()
}

func TestBuildStateTaskPreservationMatrix(t *testing.T) {
	fetchedAt := time.Now().UTC().Add(-time.Minute)
	enriched := func(state string) model.Target {
		return model.Target{
			Repo: "repo", Arch: "x86_64", State: state,
			BlockedBy:           "waiting on libfoo",
			BuildReason:         "meta change",
			BuildReasonPackages: []string{"libfoo"},
			BlockedByFetchedAt:  fetchedAt,
		}
	}

	cases := []struct {
		name         string
		prevTargets  []model.Target
		serverStates [][3]string // repo, arch, code
		wantPreserve bool
		wantStable   bool
	}{
		{
			name:         "state unchanged: preserved and stable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: true,
			wantStable:   true,
		},
		{
			name:         "state changed: wiped and unstable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "building"}},
			wantPreserve: false,
			wantStable:   false,
		},
		{
			name:         "target added: unstable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}, {"repo", "aarch64", "building"}},
			wantPreserve: true, // the matching target still preserves
			wantStable:   false,
		},
		{
			name:         "target removed: unstable",
			prevTargets:  []model.Target{enriched("blocked"), {Repo: "repo", Arch: "aarch64", State: "building"}},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: true,
			wantStable:   false,
		},
		{
			name:         "no previous targets: unstable",
			prevTargets:  nil,
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: false, // nothing to preserve from
			wantStable:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, resultXML(tc.serverStates))
			}))
			defer ts.Close()

			c := obs.NewClient(ts.URL, "u", "p")
			pkg := &model.Package{
				Project: "isv:percona", Name: "mypkg",
				Targets: tc.prevTargets, UpdatedAt: time.Now().UTC(),
			}
			if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg); err != nil {
				t.Fatal(err)
			}

			if pkg.TargetsStable != tc.wantStable {
				t.Errorf("TargetsStable = %v, want %v", pkg.TargetsStable, tc.wantStable)
			}
			// Find the repo/x86_64 target in the result.
			var got *model.Target
			for i := range pkg.Targets {
				if pkg.Targets[i].Repo == "repo" && pkg.Targets[i].Arch == "x86_64" {
					got = &pkg.Targets[i]
					break
				}
			}
			if got == nil {
				t.Fatal("repo/x86_64 target missing from result")
			}
			if tc.wantPreserve {
				if got.BlockedBy != "waiting on libfoo" || got.BuildReason != "meta change" ||
					len(got.BuildReasonPackages) != 1 || !got.BlockedByFetchedAt.Equal(fetchedAt) {
					t.Errorf("enrichment not preserved: %+v", got)
				}
			} else {
				if got.BlockedBy != "" || got.BuildReason != "" ||
					got.BuildReasonPackages != nil || !got.BlockedByFetchedAt.IsZero() {
					t.Errorf("enrichment not wiped: %+v", got)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestBuildStateTaskPreservationMatrix -v`
Expected: FAIL — "state changed" case finds enrichment preserved (today's unconditional copy), and `TargetsStable` is never set.

- [ ] **Step 3: Replace the preservation block** in `BuildStateTask.Run` (`backend/internal/obs/tasks.go`). Replace:

```go
	// Preserve existing per-target enrichment from prior task runs.
	for i := range updated.Targets {
		for _, old := range pkg.Targets {
			if old.Repo == updated.Targets[i].Repo && old.Arch == updated.Targets[i].Arch {
				updated.Targets[i].BlockedBy = old.BlockedBy
				updated.Targets[i].BuildReason = old.BuildReason
				updated.Targets[i].BuildReasonPackages = old.BuildReasonPackages
				break
			}
		}
	}
```

with:

```go
	// Preserve per-target enrichment from prior passes only while the target's
	// state is unchanged; a state transition leaves the fields at their zero
	// values, forcing the downstream tasks to refetch. Also compute
	// TargetsStable: true only when the previous pass had the same target set
	// with identical states — all cold-start paths (no previous targets, MQ
	// replace, restart) yield false so downstream tasks fetch conservatively.
	stable := len(pkg.Targets) > 0 && len(pkg.Targets) == len(updated.Targets)
	for i := range updated.Targets {
		matched := false
		for _, old := range pkg.Targets {
			if old.Repo == updated.Targets[i].Repo && old.Arch == updated.Targets[i].Arch {
				matched = true
				if old.State == updated.Targets[i].State {
					updated.Targets[i].BlockedBy = old.BlockedBy
					updated.Targets[i].BuildReason = old.BuildReason
					updated.Targets[i].BuildReasonPackages = old.BuildReasonPackages
					updated.Targets[i].BlockedByFetchedAt = old.BlockedByFetchedAt
				} else {
					stable = false
				}
				break
			}
		}
		if !matched {
			stable = false
		}
	}
	pkg.TargetsStable = stable
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run TestBuildStateTask -v && go test ./...`
Expected: matrix + existing `TestBuildStateTask` PASS; full suite green (event emission compares the pre-chain snapshot, unaffected by mid-chain wiping).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "feat(obs): state-conditional enrichment preservation and TargetsStable"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestBuildStateTask -v && go test ./...", "acceptanceCriteria": ["state unchanged preserves all four enrichment fields", "state changed wipes them", "TargetsStable true only for identical target set with identical states", "existing TestBuildStateTask passes"], "modelTier": "standard"}
```

---

### Task 3: `BuildReasonTask` — skip populated targets

**Goal:** Skip the per-target `_reason` fetch when `BuildReason` is already populated (cycle-cached; Task 2's wipe-on-transition is the invalidation).

**Files:**
- Modify: `backend/internal/obs/tasks.go` (BuildReasonTask.Run, currently ~lines 189-215)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] A non-succeeded target with `BuildReason != ""` triggers no HTTP call.
- [ ] A non-succeeded target with empty `BuildReason` still fetches (including alongside a cached sibling).
- [ ] Existing `TestBuildReasonTask` and `TestBuildReasonTaskRetriesOnTransientError` still pass.

**Verify:** `cd backend && go test ./internal/obs/ -run TestBuildReasonTask -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** — add to `backend/internal/obs/tasks_test.go`:

```go
func TestBuildReasonTaskSkipsCachedTargets(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<reason><explain>new cycle</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"}, // cached → skip
			{Repo: "repo", Arch: "aarch64", State: "building"},                            // empty → fetch
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildReasonTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 fetch (cached target skipped), got %d", got)
	}
	if pkg.Targets[0].BuildReason != "meta change" {
		t.Errorf("cached reason overwritten: %q", pkg.Targets[0].BuildReason)
	}
	if pkg.Targets[1].BuildReason != "new cycle" {
		t.Errorf("uncached target not fetched: %q", pkg.Targets[1].BuildReason)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestBuildReasonTaskSkipsCachedTargets -v`
Expected: FAIL — `calls == 2` (today both targets fetch) and target 0's reason is overwritten.

- [ ] **Step 3: Add the guard** in `BuildReasonTask.Run`, immediately after the `succeeded` skip:

```go
		if target.State == "succeeded" {
			continue
		}
		if target.BuildReason != "" {
			// Cached for this build cycle: BuildStateTask wipes BuildReason on
			// any state transition, so a populated value is current. Targets
			// whose OBS _reason is legitimately empty keep fetching (no regression).
			continue
		}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run TestBuildReasonTask -v && go test ./...`
Expected: all PASS (existing tests use targets with empty `BuildReason` → unaffected).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): skip build-reason fetch for targets cached this cycle"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestBuildReasonTask -v && go test ./...", "acceptanceCriteria": ["populated BuildReason → no HTTP call for that target", "empty BuildReason still fetches alongside cached sibling", "existing BuildReason tests pass"], "modelTier": "mechanical"}
```

---

### Task 4: `BlockedReasonTask` — populated + 5-minute freshness

**Goal:** Fetch blocked reasons only when some blocked target has an empty `BlockedBy` or a stale `BlockedByFetchedAt` (> 5m); stamp the timestamp when a non-empty reason is received.

**Files:**
- Modify: `backend/internal/obs/tasks.go` (BlockedReasonTask.Run, currently ~lines 160-184)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] All blocked targets populated and fresh (< 5m) → no HTTP call.
- [ ] Any blocked target stale (> 5m) → fetch; `BlockedBy` refreshed and `BlockedByFetchedAt` re-stamped.
- [ ] Any blocked target with empty `BlockedBy` → fetch (existing behaviour; a target OBS reports no details for keeps today's per-pass retry).
- [ ] `BlockedByFetchedAt` is stamped only when a non-empty reason is received.
- [ ] Existing `TestBlockedReasonTask` and `TestBlockedReasonTaskSkipsWhenNoBlocked` still pass.

**Verify:** `cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/obs/tasks_test.go` (the TTL constant is unexported; tests use duration literals):

```go
func TestBlockedReasonTaskSkipsWhenFresh(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{{
			Repo: "repo", Arch: "x86_64", State: "blocked",
			BlockedBy:          "waiting on libfoo",
			BlockedByFetchedAt: time.Now().UTC().Add(-time.Minute), // fresh
		}},
	}
	if err := (obs.BlockedReasonTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call for fresh blocked reason, got %d", calls)
	}
	if pkg.Targets[0].BlockedBy != "waiting on libfoo" {
		t.Errorf("cached BlockedBy lost: %q", pkg.Targets[0].BlockedBy)
	}
}

func TestBlockedReasonTaskRefetchesWhenStale(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="building">
            <status package="mypkg" code="blocked">
              <details>now waiting on libbar</details>
            </status>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	stale := time.Now().UTC().Add(-6 * time.Minute)
	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{{
			Repo: "repo", Arch: "x86_64", State: "blocked",
			BlockedBy:          "waiting on libfoo",
			BlockedByFetchedAt: stale,
		}},
	}
	if err := (obs.BlockedReasonTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BlockedBy != "now waiting on libbar" {
		t.Errorf("stale BlockedBy not refreshed: %q", pkg.Targets[0].BlockedBy)
	}
	if !pkg.Targets[0].BlockedByFetchedAt.After(stale) {
		t.Error("BlockedByFetchedAt not re-stamped after refetch")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run 'TestBlockedReasonTaskSkipsWhenFresh|TestBlockedReasonTaskRefetchesWhenStale' -v`
Expected: `SkipsWhenFresh` FAILS (today it always fetches when a blocked target exists, and the empty resultlist wipes `BlockedBy`).

- [ ] **Step 3: Implement** in `backend/internal/obs/tasks.go`. Add the constant near the top of the file (after the imports/withRetry):

```go
// blockedByTTL bounds BlockedBy staleness: while a target stays blocked, the
// blocker list evolves as dependencies finish, so a cached reason is refreshed
// once it is older than this. Constant by design (YAGNI on config).
const blockedByTTL = 5 * time.Minute
```

Replace `BlockedReasonTask.Run`'s guard and assignment loop:

```go
func (t BlockedReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	needsFetch := false
	for _, target := range pkg.Targets {
		if target.State != "blocked" {
			continue
		}
		if target.BlockedBy == "" || time.Since(target.BlockedByFetchedAt) > blockedByTTL {
			needsFetch = true
			break
		}
	}
	if !needsFetch {
		return nil
	}

	reasons, err := client.PackageBlockedReasons(ctx, pkg.Project, pkg.Name)
	if err != nil {
		slog.Warn("obs: blocked reasons", "pkg", pkg.Name, "err", err)
		return nil
	}
	now := time.Now().UTC()
	for i, target := range pkg.Targets {
		if target.State != "blocked" {
			continue
		}
		reason := reasons[target.Repo+"/"+target.Arch]
		pkg.Targets[i].BlockedBy = reason
		if reason != "" {
			// Stamp only on value receipt: a blocked target whose details OBS
			// hasn't produced yet keeps retrying every pass (today's freshness).
			pkg.Targets[i].BlockedByFetchedAt = now
		}
	}
	return nil
}
```

(The old `hasBlocked` guard is subsumed: no blocked targets → `needsFetch` stays false → return, so `TestBlockedReasonTaskSkipsWhenNoBlocked` keeps passing.)

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v && go test ./...`
Expected: all four BlockedReason tests PASS (existing `TestBlockedReasonTask`'s target has empty `BlockedBy` → still fetches); full suite green.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): refresh blocked reasons on a 5m TTL instead of every pass"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestBlockedReasonTask -v && go test ./...", "acceptanceCriteria": ["fresh populated blocked targets → no HTTP call", "stale target → refetch + re-stamp", "empty BlockedBy still fetches", "stamp only on non-empty value", "existing BlockedReason tests pass"], "modelTier": "mechanical"}
```

---

### Task 5: `VersionTask` + `ContainerTagsTask` — skip when stable

**Goal:** Both tasks skip their fetch when their value is already known and `TargetsStable` confirmed no target changed (versrel and image tags only change when a new build lands, which always transitions target states).

**Files:**
- Modify: `backend/internal/obs/tasks.go` (VersionTask.Run ~line 242; ContainerTagsTask.Run ~line 264)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] `VersionTask`: `Version != "" && TargetsStable` → no HTTP call; empty version or `TargetsStable == false` → fetches.
- [ ] `ContainerTagsTask`: `len(ContainerTags) > 0 && TargetsStable` → no HTTP call; empty tags or unstable → fetches (release chain never sets the flag → always fetches, same as today).
- [ ] Existing `TestVersionTask`, `TestVersionTaskSkipsContainers`, `TestContainerTagsTask`, `TestContainerTagsTaskSkipsNonContainers` still pass.

**Verify:** `cd backend && go test ./internal/obs/ -run 'TestVersionTask|TestContainerTagsTask' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/obs/tasks_test.go`:

```go
func TestVersionTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "percona-pg_tde",
		IsContainer:   boolPtr(false),
		Version:       "17.5-1",
		TargetsStable: true,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when version known and targets stable, got %d", calls)
	}
	if pkg.Version != "17.5-1" {
		t.Errorf("version changed: %q", pkg.Version)
	}
}

func TestVersionTaskFetchesWhenUnstable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-2"/>
			</result>
		</resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "percona-pg_tde",
		IsContainer:   boolPtr(false),
		Version:       "17.5-1",
		TargetsStable: false, // a target changed → version may have moved
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 OBS call when unstable, got %d", calls)
	}
	if pkg.Version != "17.5-2" {
		t.Errorf("version not refreshed: %q", pkg.Version)
	}
}

func TestContainerTagsTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17:containers", Name: "percona-distribution-postgresql",
		IsContainer:   boolPtr(true),
		ContainerTags: []string{"18.4-1-1.7", "18.4-1"},
		TargetsStable: true,
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when tags known and targets stable, got %d", calls)
	}
}

func TestContainerTagsTaskFetchesWhenUnstable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-2-1.8","percona-distribution-postgresql:18.4-2"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.8.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17:containers", Name: "percona-distribution-postgresql",
		IsContainer:   boolPtr(true),
		ContainerTags: []string{"18.4-1-1.7", "18.4-1"},
		TargetsStable: false, // new build landed
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.ContainerTags) != 2 || pkg.ContainerTags[0] != "18.4-2-1.8" {
		t.Errorf("tags not refreshed: %v", pkg.ContainerTags)
	}
	if pkg.Version != "18.4-2-1.8" {
		t.Errorf("version not updated from refreshed tags: %q", pkg.Version)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run 'TestVersionTaskSkipsWhenStable|TestContainerTagsTaskSkipsWhenStable' -v`
Expected: both FAIL — today both tasks fetch regardless of stability.

- [ ] **Step 3: Add the guards** in `backend/internal/obs/tasks.go`.

In `VersionTask.Run`, after the container skip:

```go
	if pkg.IsContainer != nil && *pkg.IsContainer {
		return nil
	}
	if pkg.Version != "" && pkg.TargetsStable {
		// versrel only changes when a new build lands, which always transitions
		// target states; TargetsStable confirms none did since the last pass.
		return nil
	}
```

In `ContainerTagsTask.Run`, after the `IsContainer` guard:

```go
	if pkg.IsContainer == nil || !*pkg.IsContainer {
		return nil
	}
	if len(pkg.ContainerTags) > 0 && pkg.TargetsStable {
		// Image tags only change when a new build lands (same invariant as
		// versrel). The release chain has no BuildStateTask so TargetsStable is
		// never set there — release containers keep fetching, same as today.
		return nil
	}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run 'TestVersionTask|TestContainerTagsTask' -v && go test ./...`
Expected: all PASS (existing tests construct packages with `TargetsStable` unset → fetch path unchanged); full suite green.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): skip version and container-tag fetch while targets are stable"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run 'TestVersionTask|TestContainerTagsTask' -v && go test ./...", "acceptanceCriteria": ["VersionTask skips when version known + stable, fetches otherwise", "ContainerTagsTask skips when tags known + stable, fetches otherwise", "existing Version/ContainerTags tests pass"], "modelTier": "mechanical"}
```

---

## Self-Review

**Spec coverage:**
- §"Where the memory lives" (two `json:"-"` fields + serialization invariant) → Task 1. ✓
- §1 BuildStateTask conditional preservation + `TargetsStable` polarity → Task 2. ✓
- §2 BuildReasonTask skip-populated → Task 3. ✓
- §3 BlockedReasonTask 5m TTL, stamp-on-value → Task 4. ✓
- §4 VersionTask + §5 ContainerTagsTask stable-skip (incl. release-chain caveat as comment) → Task 5. ✓
- §Untouched paths: no task touches worker/poller/MQ — regression covered by each task's full-suite run. ✓
- §Task audit (PackageType/PublishState/BinariesCheck/BuildState untouched): no task modifies them. ✓

**Placeholders:** none — every step has complete code and exact commands.

**Type consistency:** `BlockedByFetchedAt time.Time` (Tasks 1, 2, 4), `TargetsStable bool` (Tasks 1, 2, 5), `blockedByTTL` unexported const (Task 4 only; tests use literals per the external-test-package note). Consistent.
