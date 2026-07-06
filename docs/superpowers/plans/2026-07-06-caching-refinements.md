# Caching Refinements (A+B+C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the residual OBS traffic found in production telemetry: negative-result refetching (`build_reason:192/min`, `version:44/min`), futile publish checks on non-publishing repos (`publish_states:44/min`), and the 24 permanently-unresolvable packages re-running the chain every 30s.

**Architecture:** Three surgical changes on top of the existing caching machinery. A: `TargetsStable` becomes the negative-cache signal — under stable targets, an empty result was already confirmed empty in this exact state, so `BuildReasonTask` gains a top-level stable-skip and `VersionTask`'s guard drops its `Version != ""` condition. B: `PublishStateTask` counts a succeeded-unpublished target toward `needsCheck` only when its repo actually publishes (cached `ProjectPublishFlags`). C: `obs.Settled` treats `unresolvable` as terminal alongside `published`/`failed`.

**Tech Stack:** Go, `httptest`, existing `internal/obs` patterns. Test file `tasks_test.go` is external package `obs_test`; `settled_test.go` is internal package `obs`.

**User decisions (already made):**
- "do A + B + C" — all three fixes approved after telemetry attribution (192/min = unresolvable empty reasons; 44/min = never-built empty versrel; 44/min = non-publishing publish checks; 24 long-lived unresolvable packages).
- C supersedes the earlier "failed only" terminal-scope decision for `unresolvable` specifically; `broken` remains non-terminal.
- `ContainerTagsTask` keeps its `len(tags) > 0` requirement — negative-caching tags is unsafe (containerinfo can lag the succeeded transition).
- `PublishStateTask` rollup-promotion logic untouched (display stays truthful; working-set exit is `Settled`'s job).
- Specs already amended and committed (`502c526`).

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `backend/internal/obs/tasks.go` | A: BuildReasonTask top-guard + VersionTask guard; B: PublishStateTask flags | 1, 2 |
| `backend/internal/obs/tasks_test.go` | A/B tests | 1, 2 |
| `backend/internal/obs/settled.go` | C: unresolvable terminal | 3 |
| `backend/internal/obs/settled_test.go` | C test update | 3 |

Tasks 1 and 2 share `tasks.go` — execute serially. Task 3 is independent but small; keep serial for simplicity.

---

### Task 1: Negative-result caching (BuildReasonTask + VersionTask)

**Goal:** Under `TargetsStable`, skip BuildReason and Version fetches entirely — empty results were already confirmed empty in this state.

**Files:**
- Modify: `backend/internal/obs/tasks.go` (BuildReasonTask.Run, VersionTask.Run)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] `BuildReasonTask`: `pkg.TargetsStable == true` → zero HTTP calls even when non-succeeded targets have empty reasons.
- [ ] `BuildReasonTask` unstable pass: per-target populated-skip still applies (existing `TestBuildReasonTaskSkipsCachedTargets` unchanged and passing).
- [ ] `VersionTask`: `pkg.TargetsStable == true` → zero HTTP calls even when `Version == ""`.
- [ ] All existing BuildReason/Version tests pass unmodified (they construct packages with `TargetsStable` unset → fetch paths unchanged).

**Verify:** `cd backend && go test ./internal/obs/ -run 'TestBuildReasonTask|TestVersionTask' -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — add to `backend/internal/obs/tasks_test.go`:

```go
// Negative-result caching: under stable targets, an empty reason was already
// confirmed empty in this exact state (e.g. unresolvable targets, which OBS
// has no reason for) — no refetch until a state transition.
func TestBuildReasonTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<reason><explain>should never be fetched</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		TargetsStable: true,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "unresolvable"}, // empty reason, stable → skip
			{Repo: "repo", Arch: "aarch64", State: "building", BuildReason: "meta change"},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildReasonTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected no OBS calls under stable targets, got %d", got)
	}
	if pkg.Targets[0].BuildReason != "" {
		t.Errorf("unresolvable target reason should stay empty: %q", pkg.Targets[0].BuildReason)
	}
}

func TestVersionTaskSkipsEmptyVersionWhenStable(t *testing.T) {
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
		Version:       "", // never built — empty versrel is now negative-cached
		TargetsStable: true,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call for empty version under stable targets, got %d", calls)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run 'TestBuildReasonTaskSkipsWhenStable|TestVersionTaskSkipsEmptyVersionWhenStable' -v`
Expected: both FAIL (today: stable+empty reason fetches; stable+empty version fetches).

- [ ] **Step 3: Implement** in `backend/internal/obs/tasks.go`.

At the top of `BuildReasonTask.Run` (before the target loop):

```go
	if pkg.TargetsStable {
		// Negative-result caching: under stable targets every non-succeeded
		// target was already queried in this exact state — populated reasons
		// are current, and empty ones (e.g. unresolvable targets, which OBS
		// has no reason for) will stay empty until a state transition flips
		// TargetsStable off and refetches.
		return nil
	}
```

In `VersionTask.Run`, replace:

```go
	if pkg.Version != "" && pkg.TargetsStable {
		// versrel only changes when a new build lands, which always transitions
		// target states; TargetsStable confirms none did since the last pass.
		return nil
	}
```
with:
```go
	if pkg.TargetsStable {
		// versrel only changes when a new build lands, which always transitions
		// target states. This also negative-caches never-built packages (empty
		// versrel) — they refetch only on a state transition.
		return nil
	}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run 'TestBuildReasonTask|TestVersionTask' -v && go test ./...`
Expected: all PASS (existing tests use `TargetsStable` unset → unchanged behaviour).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): negative-result caching for build reasons and versions"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run 'TestBuildReasonTask|TestVersionTask' -v && go test ./...", "acceptanceCriteria": ["BuildReasonTask: stable → zero calls even with empty reasons", "unstable pass keeps per-target populated-skip", "VersionTask: stable → zero calls even with empty version", "existing tests pass unmodified"], "modelTier": "mechanical"}
```

---

### Task 2: Publish-flag awareness in PublishStateTask

**Goal:** Skip the `RepoPublishStates` check when every succeeded-unpublished target sits in a repo that never publishes.

**Files:**
- Modify: `backend/internal/obs/tasks.go` (PublishStateTask.Run)
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] A succeeded-unpublished target in a publish-disabled repo does NOT trigger `RepoPublishStates` (no `_result` call; only the one cached `_meta` fetch).
- [ ] A succeeded-unpublished target in a publishing repo still triggers the check (existing `TestPublishStateTask` passes — its server serves no `_meta`, so flags fetch errors → zero-value → publishes → conservative check).
- [ ] Rollup-promotion logic untouched.

**Verify:** `cd backend && go test ./internal/obs/ -run TestPublishStateTask -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test** — add to `backend/internal/obs/tasks_test.go`:

```go
// Succeeded-unpublished targets in repos that never publish are futile to
// check: their repo state stays "unpublished" forever. PublishStateTask must
// consult the (cached) publish flags and skip the _result fetch entirely.
func TestPublishStateTaskSkipsNonPublishingRepos(t *testing.T) {
	var metaCalls, resultCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_meta") {
			atomic.AddInt32(&metaCalls, 1)
			fmt.Fprint(w, `<project name="p"><publish><disable/></publish></project>`)
			return
		}
		atomic.AddInt32(&resultCalls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:PR:pr-1:ppg:17", Name: "mypkg",
		Targets: []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded"}},
	}
	if err := (obs.PublishStateTask{}).Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&resultCalls) != 0 {
		t.Fatalf("expected no publish-state check for non-publishing repo, got %d", resultCalls)
	}
	if atomic.LoadInt32(&metaCalls) != 1 {
		t.Fatalf("expected exactly 1 cached _meta fetch, got %d", metaCalls)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestPublishStateTaskSkipsNonPublishingRepos -v`
Expected: FAIL (`resultCalls == 1` today — the check fires regardless of flags).

- [ ] **Step 3: Implement** in `backend/internal/obs/tasks.go`. Replace the `needsCheck` computation at the top of `PublishStateTask.Run`:

```go
	hasCandidate := false
	for _, target := range pkg.Targets {
		if target.State == "succeeded" && !target.Published {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return nil
	}

	// Skip targets whose repo never publishes: their repo state stays
	// "unpublished" forever, so checking is futile. On flags error the zero
	// value publishes everything → conservative check (same as before).
	flags, _ := client.ProjectPublishFlags(ctx, pkg.Project)
	needsCheck := false
	for _, target := range pkg.Targets {
		if target.State == "succeeded" && !target.Published && flags.Publishes(target.Repo) {
			needsCheck = true
			break
		}
	}
	if !needsCheck {
		return nil
	}
```

(The rest of the function — `RepoPublishStates` fetch, per-target `Published` assignment, all-published promotion — is unchanged.)

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run TestPublishStateTask -v && go test ./...`
Expected: both PublishStateTask tests PASS; full suite green (the existing test's server returns `<resultlist>` for the `_meta` path → parse yields no `<publish>` block → publishes → check proceeds as before).

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/tasks.go internal/obs/tasks_test.go
git commit -s -m "perf(obs): skip publish-state checks for non-publishing repos"
```

```json:metadata
{"files": ["backend/internal/obs/tasks.go", "backend/internal/obs/tasks_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestPublishStateTask -v && go test ./...", "acceptanceCriteria": ["non-publishing succeeded-unpublished target → no RepoPublishStates call", "publishing repo still checked; existing test passes", "promotion logic untouched"], "modelTier": "standard"}
```

---

### Task 3: `unresolvable` is terminal

**Goal:** `obs.Settled` treats `unresolvable` as terminal so the 24 stuck packages leave the working set after one enrichment pass.

**Files:**
- Modify: `backend/internal/obs/settled.go`
- Test: `backend/internal/obs/settled_test.go` (internal package `obs`)

**Acceptance Criteria:**
- [ ] `Settled` returns `true` for `RollupUnresolvable` (alongside `published`/`failed`); `broken` stays `false`.
- [ ] `TestSettled`'s `unresolvable` case expects `true`.
- [ ] Full suite green (worker removal for unresolvable flows through the same settled path as failed — covered by existing worker tests plus the settled table).

**Verify:** `cd backend && go test ./internal/obs/ -run TestSettled -v && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Flip the test expectation** in `backend/internal/obs/settled_test.go`: change the `unresolvable` case's `want` from `false` to `true`:

```go
		{"unresolvable", pkg(model.RollupUnresolvable), true},
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/obs/ -run TestSettled -v`
Expected: FAIL — `Settled = false, want true` for unresolvable.

- [ ] **Step 3: Implement** in `backend/internal/obs/settled.go`: add `model.RollupUnresolvable` to the terminal case and update the doc comment:

```go
	case model.RollupPublished, model.RollupFailed, model.RollupUnresolvable:
		return true
```

Doc comment update — replace the `published, failed → terminal` line with:

```go
//   - published, failed, unresolvable → terminal (unresolvable added after
//     production telemetry showed long-lived unresolvable packages re-running
//     the chain every 30s; the poller re-adds on any rollup change ≤1 tick)
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/obs/ -run TestSettled -v && go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/settled.go internal/obs/settled_test.go
git commit -s -m "feat(obs): treat unresolvable as terminal for working-set exit"
```

```json:metadata
{"files": ["backend/internal/obs/settled.go", "backend/internal/obs/settled_test.go"], "verifyCommand": "cd backend && go test ./internal/obs/ -run TestSettled -v && go test ./...", "acceptanceCriteria": ["Settled true for unresolvable; broken stays false", "TestSettled unresolvable case expects true", "full suite green"], "modelTier": "mechanical"}
```

---

## Self-Review

**Spec coverage:** amendment A → Task 1; amendment B → Task 2; working-set follow-up #2 resolution (C) → Task 3. ContainerTags exclusion: no task touches it (correct). Promotion untouched: Task 2 keeps it. ✓
**Placeholders:** none. **Type consistency:** `TargetsStable`/`PublishFlags.Publishes`/`RollupUnresolvable` all match existing definitions. ✓
**Expected telemetry after deploy:** `build_reason` ~0, `version` ~0, `publish_states` ~0 (for non-publishing repos), and `ws_packages` drops by ~24 as unresolvable packages settle out → remaining traffic ≈ the intentional polls (~135/min → then ~90/min after C).
