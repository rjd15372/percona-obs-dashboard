# Park Succeeded Packages Awaiting Publish — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Packages whose builds all succeeded in publishing repos leave the working set (stop 30s polling) and are re-added on the MQ `repo.published` event, with the poller's existing `_result` response providing a zero-extra-request fallback via the repo-level publish state it already parses.

**Architecture:** Four small, mostly independent changes: (1) `Parkable` generalizes so `succeeded` targets qualify regardless of `Published`; (2) `client.BuildResults` additionally returns the repo-level states it currently discards; (3) the poller re-adds a stored succeeded-unpublished package when its repo·arch state flips to `published`; (4) the MQ `repo.published` handler filters woken packages to those actually waiting on the event's repo (which requires mapping the event's `"repo"` JSON key — distinct from package events' `"repository"`).

**Tech Stack:** Go backend (`backend/`), stdlib testing, in-memory SQLite via `store.Open(":memory:")`, `httptest` for OBS client tests.

**Spec:** `docs/superpowers/specs/2026-07-08-park-awaiting-publish-design.md`

**User decisions (already made):**
- Approach A: park + `repo.published` wake + poller repo-state fallback ("Can't we rely on the global 2 minute poll?" — yes, by reading the repo state the poller's `_result` already contains; no timed sweep, no `repo.publish_state` subscription).
- No direct state write from the MQ event: the user asked whether the event could just flip states; answered no (payload has no arch, per-arch publish states differ, and the worker's transition machinery must run). Wake + verify via the task chain.
- Scope: dev/PR packages only; release projects keep `BinariesCheckTask` (MQ handler already skips them).
- Work directly on `main`; commits always `git commit -s`, never a Co-Authored-By trailer.

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `backend/internal/obs/parkable.go` | modify | Generalized predicate; drops the now-unused `PublishFlags` param |
| `backend/internal/obs/parkable_test.go` | modify | Table adapted + new succeeded-awaiting-publish cases |
| `backend/internal/worker/worker.go` | modify | One-line call-site update (`Parkable(pkg)`) |
| `backend/internal/obs/client.go` | modify | `BuildResults` returns `(states, repoStates, err)` |
| `backend/internal/obs/client_test.go` | modify | New `TestBuildResultsRepoStates` |
| `backend/internal/obs/poller.go` | modify | `awaitingPublishReady` helper + fallback re-add in tick |
| `backend/internal/obs/poller_test.go` | modify | New `TestAwaitingPublishReady` |
| `backend/internal/mq/consumer.go` | modify | `"repo"` payload field, `awaitingPublishIn` filter in the `repo.published` handler |
| `backend/internal/mq/consumer_test.go` | modify | New `TestRepoPublishedWakesOnlyMatchingRepo` |

Task dependency: Task 3 (poller fallback) is blocked by Task 2 (it consumes the new `BuildResults` return value and both touch `poller.go`). Tasks 1, 2, 4 are mutually independent (disjoint files).

---

### Task 1: Generalize `Parkable` — succeeded targets park unconditionally

**Goal:** `Parkable` lets a package park when every active target is either building-with-reason or succeeded (published or not), removing the `hasBuilding` requirement and the now-unused `PublishFlags` parameter.

**Files:**
- Modify: `backend/internal/obs/parkable.go` (whole file — new predicate)
- Modify: `backend/internal/worker/worker.go:143` (call site)
- Test: `backend/internal/obs/parkable_test.go` (whole file — new table)

**Acceptance Criteria:**
- [ ] `Parkable(pkg)` (no flags param) returns true for: all-succeeded-unpublished, mixed building(+reason)+succeeded-unpublished, building(+reason)+succeeded-published, all building with reasons
- [ ] Returns false for: building without reason, any scheduled/blocked/finished/failed/broken target, only skip-state targets, no targets
- [ ] `worker.go` removal condition reads `pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg))` — Settled still checked first, enrichment gate unchanged
- [ ] `go build ./...` has no remaining callers passing flags to `Parkable`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestParkable -v` → all subtests PASS; `go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Rewrite the test table (failing first)**

Replace the body of `backend/internal/obs/parkable_test.go` with:

```go
package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestParkable(t *testing.T) {
	pkg := func(targets ...model.Target) *model.Package {
		return &model.Package{RollupState: model.RollupBuilding, Targets: targets}
	}
	building := func(reason string) model.Target {
		return model.Target{Repo: "images", Arch: "x86_64", State: "building", BuildReason: reason}
	}
	succeeded := func(arch string, published bool) model.Target {
		return model.Target{Repo: "images", Arch: arch, State: "succeeded", Published: published}
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"all building with reasons", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "building", BuildReason: "meta change"}), true},
		{"building without reason", pkg(building("")), false},
		{"building + succeeded published", pkg(building("meta change"), succeeded("aarch64", true)), true},
		{"building + succeeded unpublished", pkg(building("meta change"), succeeded("aarch64", false)), true},
		{"all succeeded unpublished (awaiting publish)", pkg(succeeded("x86_64", false), succeeded("aarch64", false)), true},
		{"succeeded mixed published/unpublished", pkg(succeeded("x86_64", true), succeeded("aarch64", false)), true},
		{"all succeeded published (Settled removes first)", pkg(succeeded("x86_64", true)), true},
		{"building + scheduled", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "scheduled", BuildReason: "meta change"}), false},
		{"building + blocked", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "blocked"}), false},
		{"building + finished", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "finished"}), false},
		{"failed target", pkg(model.Target{Repo: "images", Arch: "x86_64", State: "failed"}), false},
		{"skip-state ignored", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "disabled"}), true},
		{"only skip-state targets", pkg(model.Target{Repo: "images", Arch: "x86_64", State: "disabled"}), false},
		{"no targets", pkg(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parkable(tc.pkg); got != tc.want {
				t.Fatalf("Parkable = %v, want %v", got, tc.want)
			}
		})
	}
}
```

Note: `publishFlagsForTest()` (defined in `settled_test.go`) is no longer used here — it stays, `settled_test.go` still uses it.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestParkable -v`
Expected: compile FAIL (`too many arguments` / signature mismatch against the current two-arg `Parkable`).

- [ ] **Step 3: Rewrite `parkable.go`**

Replace the body of `backend/internal/obs/parkable.go` with:

```go
package obs

import "github.com/percona/obs-dashboard/internal/model"

// Parkable reports whether pkg is waiting only on completions that MQ will
// announce, with the poller as loss-tolerant fallback. A parkable package can
// leave the working set and be re-added by a wake signal — nothing about it
// needs active polling.
//
// A target qualifies when it is:
//   - building with its BuildReason already fetched (enrichment complete) —
//     woken by package.build_success/fail/unchanged, or
//   - succeeded — published and never-publishing targets are inert; an
//     unpublished target in a publishing repo is woken by repo.published,
//     with the poller's repo-state check as the fallback for missed events
//     and "nothing changed" publish runs.
//
// Anything else (scheduled, blocked, finished, broken, …) still needs the
// 30s poll. A package whose active targets are all inert is Settled — the
// worker checks Settled first, so Parkable returning true for it is moot.
func Parkable(pkg *model.Package) bool {
	active := 0
	for _, t := range pkg.Targets {
		if skipState(t.State) {
			continue
		}
		active++
		switch {
		case t.State == "building" && t.BuildReason != "":
		case t.State == "succeeded":
		default:
			return false
		}
	}
	return active > 0
}
```

Update the call site in `backend/internal/worker/worker.go` (currently line 143):

```go
	if pkg.Settled || (pkg.IsContainer != nil && obs.Parkable(pkg)) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
```

(Only the `Parkable` call changes; the surrounding comment block stays, but update its parenthetical to mention publish waking: replace "waiting only on build completions that MQ announces" with "waiting only on completions that MQ announces (build results or repo publication)".)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestParkable -v`
Expected: PASS (all 14 subtests). Then `go test ./...` → all PASS (worker tests compile against the new signature).

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/obs/parkable.go backend/internal/obs/parkable_test.go backend/internal/worker/worker.go
git commit -s -m "feat(workingset): park succeeded packages awaiting publish"
```

---

### Task 2: `BuildResults` returns repo-level publish states

**Goal:** `client.BuildResults` additionally returns a `map[string]string` of `"repo/arch" → state` from the `state` attribute it already parses — same single `_result` request, no new API call.

**Files:**
- Modify: `backend/internal/obs/client.go:354-380` (`BuildResults`)
- Modify: `backend/internal/obs/poller.go:77` (call site — discard the new value for now; Task 3 uses it)
- Test: `backend/internal/obs/client_test.go` (append `TestBuildResultsRepoStates`)

**Acceptance Criteria:**
- [ ] `BuildResults` signature is `(ctx, project) ([]PackageBuildState, map[string]string, error)`; the map holds one entry per `<result>` element keyed `Repository+"/"+Arch` with the `state` attribute value
- [ ] Package-state flattening behavior unchanged (same `PackageBuildState` values as before)
- [ ] Poller call site compiles, discarding the map with `_`
- [ ] Test proves both return values from one XML fixture with differing repo states

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestBuildResultsRepoStates -v` → PASS; `go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/obs/client_test.go`:

```go
func TestBuildResultsRepoStates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<resultlist state="x">
  <result project="p" repository="images" arch="x86_64" state="published">
    <status package="pkg-a" code="succeeded"/>
  </result>
  <result project="p" repository="images" arch="aarch64" state="building">
    <status package="pkg-a" code="building"/>
  </result>
</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	states, repoStates, err := c.BuildResults(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("want 2 package states, got %d", len(states))
	}
	if states[0].Package != "pkg-a" || states[0].State != "succeeded" {
		t.Fatalf("unexpected first package state: %+v", states[0])
	}
	if got := repoStates["images/x86_64"]; got != "published" {
		t.Fatalf("images/x86_64 state = %q, want published", got)
	}
	if got := repoStates["images/aarch64"]; got != "building" {
		t.Fatalf("images/aarch64 state = %q, want building", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestBuildResultsRepoStates -v`
Expected: compile FAIL (`assignment mismatch: 3 variables but ... returns 2 values`).

- [ ] **Step 3: Implement**

Replace `BuildResults` in `backend/internal/obs/client.go`:

```go
// BuildResults fetches all package build states for a project, plus the
// repo-level state per "repo/arch" — the same publish state PublishStateTask
// reads ("published" once the publisher has synced that repo·arch). Both come
// from the single _result response.
func (c *Client) BuildResults(ctx context.Context, project string) ([]PackageBuildState, map[string]string, error) {
	resp, err := c.get(ctx, "build_results", "/build/"+project+"/_result")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	repoStates := make(map[string]string, len(rl.Results))
	for _, r := range rl.Results {
		repoStates[r.Repository+"/"+r.Arch] = r.State
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
			})
		}
	}
	return out, repoStates, nil
}
```

Update the call site in `backend/internal/obs/poller.go` (currently line 77):

```go
		results, _, err := p.client.BuildResults(ctx, project)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestBuildResultsRepoStates -v`
Expected: PASS. Then `go test ./...` → all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/obs/client.go backend/internal/obs/client_test.go backend/internal/obs/poller.go
git commit -s -m "feat(obs): expose repo-level publish states from BuildResults"
```

---

### Task 3: Poller fallback — re-add awaiting-publish packages when the repo state flips

**Goal:** The poller tick re-adds a stored succeeded package to the working set when a repo·arch one of its unpublished targets sits in has reached state `"published"` — covering missed MQ events and "nothing changed" publish runs at zero extra request cost.

**Files:**
- Modify: `backend/internal/obs/poller.go` (tick wiring + new helper `awaitingPublishReady`)
- Test: `backend/internal/obs/poller_test.go` (append `TestAwaitingPublishReady`)

**Acceptance Criteria:**
- [ ] `awaitingPublishReady(stored, repoStates)` is true iff `stored` has a target with `State == "succeeded" && !Published` whose `"repo/arch"` maps to `"published"`; false for nil package, published targets, non-succeeded targets, and repo/arch keys absent or non-published
- [ ] In the real-time branch of the poller tick, when the existing changed-guard did NOT fire, `awaitingPublishReady(prev, repoStates)` triggers `p.ws.Add(prev)`
- [ ] Release branch and changed-guard behavior untouched; no `store.AppendEvent` added to poller.go (guarded by existing `TestNoPollerRollupEvents`)

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run 'TestAwaitingPublishReady|TestNoPollerRollupEvents' -v` → PASS; `go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/obs/poller_test.go`:

```go
func TestAwaitingPublishReady(t *testing.T) {
	stored := func(targets ...model.Target) *model.Package {
		return &model.Package{Targets: targets}
	}
	succ := func(repo, arch string, published bool) model.Target {
		return model.Target{Repo: repo, Arch: arch, State: "succeeded", Published: published}
	}
	repoStates := map[string]string{
		"images/x86_64":  "published",
		"images/aarch64": "building",
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"unpublished target, repo now published", stored(succ("images", "x86_64", false)), true},
		{"unpublished target, repo still building", stored(succ("images", "aarch64", false)), false},
		{"already published", stored(succ("images", "x86_64", true)), false},
		{"building target ignored", stored(model.Target{Repo: "images", Arch: "x86_64", State: "building"}), false},
		{"repo/arch missing from map", stored(succ("other", "x86_64", false)), false},
		{"one ready among several", stored(succ("images", "aarch64", false), succ("images", "x86_64", false)), true},
		{"nil package", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := awaitingPublishReady(tc.pkg, repoStates); got != tc.want {
				t.Fatalf("awaitingPublishReady = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run TestAwaitingPublishReady -v`
Expected: compile FAIL (`undefined: awaitingPublishReady`).

- [ ] **Step 3: Implement helper and wire the tick**

Append to `backend/internal/obs/poller.go` (near `skipState`):

```go
// awaitingPublishReady reports whether stored has a succeeded target not yet
// observed as published whose repo·arch has reached the "published" state in
// the current _result response. Publication is invisible to the build-state
// diff (OBS reports "succeeded" forever), so this is the loss-tolerant
// fallback that re-adds parked awaiting-publish packages when the
// repo.published MQ event was missed — or never fired ("nothing changed"
// publish runs skip it). The wake pass lets PublishStateTask confirm and
// promote.
func awaitingPublishReady(stored *model.Package, repoStates map[string]string) bool {
	if stored == nil {
		return false
	}
	for _, t := range stored.Targets {
		if t.State == "succeeded" && !t.Published && repoStates[t.Repo+"/"+t.Arch] == "published" {
			return true
		}
	}
	return false
}
```

In the tick, rename the discarded return value (from Task 2) at line ~77:

```go
		results, repoStates, err := p.client.BuildResults(ctx, project)
```

And extend the real-time branch (currently `if kind.IsRealTime() { if rollupChanged || ... { ... } }`) with an `else if`:

```go
			if kind.IsRealTime() {
				if rollupChanged || targetsChanged(prev, pkg) || tagsChanged {
					if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
						slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
						continue
					}
					p.hub.Notify(hubpkg.PackageUpdate(pkg))
					p.ws.Add(pkg)
				} else if awaitingPublishReady(prev, repoStates) {
					p.ws.Add(prev)
				}
			} else {
```

(`prev` carries the stored `Published` flags and full enrichment; `ws.Add` dedups if the package is already queued. Nothing is upserted here — the worker's wake pass writes verified state.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/ -run 'TestAwaitingPublishReady|TestNoPollerRollupEvents' -v`
Expected: PASS. Then `go test ./...` → all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/obs/poller.go backend/internal/obs/poller_test.go
git commit -s -m "feat(poller): re-add awaiting-publish packages when repo state flips to published"
```

---

### Task 4: MQ `repo.published` handler — wake only packages waiting on that repo

**Goal:** The `repo.published` handler signals only packages with a succeeded-unpublished target in the event's repo, which requires mapping the event's `"repo"` JSON payload key (package events use `"repository"`; `m.Repo` is empty for repo events today).

**Files:**
- Modify: `backend/internal/mq/consumer.go` (`mqMessage` struct, `repo.published` case, new helper `awaitingPublishIn`)
- Test: `backend/internal/mq/consumer_test.go` (append `TestRepoPublishedWakesOnlyMatchingRepo`)

**Acceptance Criteria:**
- [ ] `mqMessage` gains `RepoName string \`json:"repo"\`` (the `repo.published` payload key: `project`, `repo`, `buildid`)
- [ ] On `repo.published`, only packages with at least one target `State == "succeeded" && !Published && Repo == m.RepoName` are signaled; packages waiting on other repos or already published are not
- [ ] Release-project skip and error logging unchanged
- [ ] Test proves: waiting package in the event's repo wakes; a package in another repo and an already-published package do not

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/mq/ -run TestRepoPublishedWakesOnlyMatchingRepo -v` → PASS; `go test ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/mq/consumer_test.go`:

```go
// repo.published must wake only packages actually waiting on that repo's
// publication: at least one succeeded-unpublished target in the event's repo.
// Note the payload key is "repo" (unlike package events' "repository").
func TestRepoPublishedWakesOnlyMatchingRepo(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4)
	c := NewConsumer("", db, hubpkg.New(), nil, ws, "isv:percona")

	seed := func(name, repo string, published bool) {
		pkg := &model.Package{
			Project: "isv:percona:ppg:17", Name: name,
			RollupState: model.RollupSucceeded,
			Targets:     []model.Target{{Repo: repo, Arch: "x86_64", State: "succeeded", Published: published}},
			UpdatedAt:   time.Now().UTC(),
		}
		if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	seed("waiting-a", "repo-a", false) // in the published repo → must wake
	seed("waiting-b", "repo-b", false) // other repo → must not wake
	seed("done-a", "repo-a", true)     // already observed published → must not wake

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:17", "repo": "repo-a",
	})
	c.handle(context.Background(), amqp.Delivery{
		RoutingKey: "opensuse.obs.repo.published",
		Body:       body,
	})

	select {
	case got := <-ws.Dispatch():
		if got.Name != "waiting-a" {
			t.Fatalf("dispatched %q, want waiting-a", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("repo.published did not wake the awaiting package")
	}
	select {
	case got := <-ws.Dispatch():
		t.Fatalf("unexpected extra dispatch: %s", got.Name)
	case <-time.After(100 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/mq/ -run TestRepoPublishedWakesOnlyMatchingRepo -v`
Expected: FAIL at `unexpected extra dispatch` (the current handler signals every succeeded package of the project, including `waiting-b` and `done-a`).

- [ ] **Step 3: Implement**

In `backend/internal/mq/consumer.go`, add the payload field to `mqMessage`:

```go
type mqMessage struct {
	Project  string `json:"project"`
	Package  string `json:"package"`
	Repo     string `json:"repository"` // package.* events
	RepoName string `json:"repo"`       // repo.* events (repo.published payload: project, repo, buildid)
	Arch     string `json:"arch"`
	Reason   string `json:"reason"`
	Sender   string `json:"sender"`
	User     string `json:"user"`
	Comment  string `json:"comment"`
}
```

Update the `repo.published` case:

```go
	case key == repoRouteKey:
		// Release projects: BinariesCheckTask handles publish detection; ignore MQ repo events.
		if kind == obs.KindRelease {
			return
		}
		finished, err := store.GetFinishedPackagesByProject(c.db, m.Project)
		if err != nil {
			slog.Warn("mq: get finished packages for publish signal", "project", m.Project, "err", err)
		} else {
			for _, pkg := range finished {
				if awaitingPublishIn(pkg, m.RepoName) {
					c.ws.Signal(pkg)
				}
			}
		}
```

Add the helper (near `isPackageBuildEvent`):

```go
// awaitingPublishIn reports whether pkg has a succeeded target in repo that
// has not been observed as published yet — the packages a repo.published
// event for that repo can actually progress. Packages waiting on other repos
// (or already fully observed) are skipped to avoid needless chain passes.
// An empty repo (malformed event) matches nothing; the poller fallback
// covers that case.
func awaitingPublishIn(pkg *model.Package, repo string) bool {
	for _, t := range pkg.Targets {
		if t.Repo == repo && t.State == "succeeded" && !t.Published {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/mq/ -run TestRepoPublishedWakesOnlyMatchingRepo -v`
Expected: PASS. Then `go test ./...` → all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard
git add backend/internal/mq/consumer.go backend/internal/mq/consumer_test.go
git commit -s -m "feat(mq): wake only packages awaiting the published repo on repo.published"
```

---

## Self-Review

- **Spec coverage:** parking predicate → Task 1; poller fallback + `BuildResults` repo states → Tasks 2-3; MQ repo filter → Task 4; "unchanged" sections (Settled, releases, publish-flag cache, restart) touched by no task; races handled by design (wake+verify, no direct state writes — Task 3 explicitly avoids upserting). Testing section of the spec maps 1:1 to the four tasks' tests plus `go test ./...`. No gaps.
- **Placeholder scan:** none — every step has complete code and exact commands.
- **Type consistency:** `Parkable(pkg *model.Package) bool` matches Task 1 test and worker call site; `BuildResults(ctx, project) ([]PackageBuildState, map[string]string, error)` matches Task 2 test and Task 3's `results, repoStates, err`; `awaitingPublishReady(stored *model.Package, repoStates map[string]string) bool` consistent between helper and test; `m.RepoName` / `awaitingPublishIn(pkg, repo)` consistent between struct, handler, helper, and test.
- **Payload-key catch verified:** no existing code parses `"repo"`; `repo.published` events currently leave `m.Repo` empty — Task 4's new field is required for the filter to ever match.
