# Blocked Package Reason Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the OBS blocking reason string in each blocked target row of the package card, fetched from `_builddepinfo` by both the MQ consumer (real-time) and the poller (every tick while blocked).

**Architecture:** `Target` gains a `blocked_by` field. The OBS client gains `PackageBlockedReason`. An exported `EnrichBlockedTargets` helper (in the `obs` package) enriches all blocked targets on a package by calling `PackageBlockedReason` per target. The poller calls it every tick; the consumer calls it immediately when processing build events that result in blocked targets. Enriched data flows through the existing store → SSE hub → frontend pipeline unchanged.

**Tech Stack:** Go standard library (`net/http`, `encoding/xml`), Vue 3 Composition API. No new dependencies.

**User decisions (already made):**
- Blocking reason fetched by the backend, not on-demand by the frontend.
- Per-target granularity — each blocked target row shows its own reason.
- Both the MQ consumer (real-time) and the poller (every tick while blocked) enrich blocked targets.
- The `_builddepinfo` endpoint's `error` XML element already contains the blocking reason string.

---

## Task 1: Extend data model and OBS client

**Goal:** Add `BlockedBy` to `Target`, extend `DepInfo` with `Error`, and implement `PackageBlockedReason` with tests.

**Files:**
- Modify: `backend/internal/model/package.go`
- Modify: `backend/internal/obs/client.go`
- Modify: `backend/internal/obs/client_test.go`

**Acceptance Criteria:**
- [ ] `go test ./internal/obs/... -run TestPackageBlockedReason` exits 0
- [ ] `PackageBlockedReason` returns the `error` element text for a matching package entry
- [ ] `PackageBlockedReason` returns `("", nil)` when no `error` element is present
- [ ] `Target.BlockedBy` is `omitempty` in JSON so non-blocked targets add no overhead
- [ ] `go build ./...` exits 0

**Verify:** `cd backend && go test ./internal/obs/... -run TestPackageBlockedReason -v` → `PASS`

**Steps:**

- [ ] **Step 1: Write failing tests in `client_test.go`**

Add at the bottom of `backend/internal/obs/client_test.go`:

```go
func TestPackageBlockedReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" {
			http.Error(w, "missing package param", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
				<error>libfoo is not yet built</error>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reason, err := c.PackageBlockedReason(context.Background(), "isv:percona:ppg:17", "standard", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "libfoo is not yet built" {
		t.Errorf("expected blocking reason, got %q", reason)
	}
}

func TestPackageBlockedReasonNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reason, err := c.PackageBlockedReason(context.Background(), "isv:percona:ppg:17", "standard", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd backend && go test ./internal/obs/... -run TestPackageBlockedReason -v
```

Expected: compile error `c.PackageBlockedReason undefined`

- [ ] **Step 3: Add `BlockedBy` to `Target` in `model/package.go`**

Current `Target` struct (lines 44–48):
```go
type Target struct {
	Repo  string `json:"repo"`
	Arch  string `json:"arch"`
	State string `json:"state"`
}
```

Replace with:
```go
type Target struct {
	Repo      string `json:"repo"`
	Arch      string `json:"arch"`
	State     string `json:"state"`
	BlockedBy string `json:"blocked_by,omitempty"`
}
```

- [ ] **Step 4: Extend `DepInfo` and implement `PackageBlockedReason` in `client.go`**

Current `DepInfo` (lines 81–84):
```go
type DepInfo struct {
	Package string   `xml:"package,attr"`
	Deps    []string `xml:"pkgdep"`
}
```

Replace with:
```go
type DepInfo struct {
	Package string   `xml:"package,attr"`
	Deps    []string `xml:"pkgdep"`
	Error   string   `xml:"error"`
}
```

Then add this method after `BuildDepInfo` (after line 203):
```go
// PackageBlockedReason returns the blocking reason for a specific package in a
// given (project, repo, arch) tuple, as reported by OBS _builddepinfo.
// Returns ("", nil) when the package entry has no error element.
func (c *Client) PackageBlockedReason(ctx context.Context, project, repo, arch, pkg string) (string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/_builddepinfo?package=%s", project, repo, arch, pkg)
	resp, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Packages []DepInfo `xml:"package"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse _builddepinfo for %s/%s/%s/%s: %w", project, repo, arch, pkg, err)
	}
	for _, d := range result.Packages {
		if d.Package == pkg {
			return d.Error, nil
		}
	}
	return "", nil
}
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
cd backend && go test ./internal/obs/... -run TestPackageBlockedReason -v
```

Expected: `PASS` for both `TestPackageBlockedReason` and `TestPackageBlockedReasonNoError`

- [ ] **Step 6: Verify full build**

```bash
cd backend && go build ./...
```

Expected: exits 0, no output

- [ ] **Step 7: Commit**

```bash
cd backend && git add internal/model/package.go internal/obs/client.go internal/obs/client_test.go
git commit -s -m "feat(backend): add BlockedBy to Target and PackageBlockedReason to OBS client"
```

---

## Task 2: Poller enrichment

**Goal:** Add the exported `EnrichBlockedTargets` helper to the `obs` package and wire it into the poller's tick loop.

**Files:**
- Modify: `backend/internal/obs/poller.go`
- Modify: `backend/internal/obs/client_test.go`

**Acceptance Criteria:**
- [ ] `go test ./internal/obs/... -run TestEnrichBlockedTargets` exits 0
- [ ] `EnrichBlockedTargets` sets `BlockedBy` on blocked targets and leaves non-blocked targets unchanged
- [ ] Errors from `PackageBlockedReason` are logged as warnings; enrichment continues for other targets
- [ ] `go build ./...` exits 0

**Verify:** `cd backend && go test ./internal/obs/... -run TestEnrichBlockedTargets -v` → `PASS`

**Steps:**

- [ ] **Step 1: Write failing test in `client_test.go`**

Add at the bottom of `backend/internal/obs/client_test.go`:

```go
func TestEnrichBlockedTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
				<error>libfoo is not yet built</error>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17",
		Name:    "mypkg",
		Targets: []model.Target{
			{Repo: "standard", Arch: "x86_64", State: "blocked"},
			{Repo: "standard", Arch: "aarch64", State: "succeeded"},
		},
	}

	EnrichBlockedTargets(context.Background(), client, pkg)

	if pkg.Targets[0].BlockedBy != "libfoo is not yet built" {
		t.Errorf("blocked target: expected reason, got %q", pkg.Targets[0].BlockedBy)
	}
	if pkg.Targets[1].BlockedBy != "" {
		t.Errorf("succeeded target: BlockedBy should be empty, got %q", pkg.Targets[1].BlockedBy)
	}
}
```

Note: this test requires importing `"github.com/percona/obs-dashboard/internal/model"` in `client_test.go`. Add it to the import block if not already present.

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd backend && go test ./internal/obs/... -run TestEnrichBlockedTargets -v
```

Expected: compile error `EnrichBlockedTargets undefined`

- [ ] **Step 3: Add `EnrichBlockedTargets` to `poller.go`**

Add this exported function immediately after the `buildPackage` function in `backend/internal/obs/poller.go` (after line 255):

```go
// EnrichBlockedTargets fetches the blocking reason for each blocked target in pkg
// and sets Target.BlockedBy. Non-blocked targets are untouched. API errors are
// logged as warnings and do not stop enrichment of other targets.
func EnrichBlockedTargets(ctx context.Context, client *Client, pkg *model.Package) {
	for i, t := range pkg.Targets {
		if t.State != "blocked" {
			continue
		}
		reason, err := client.PackageBlockedReason(ctx, pkg.Project, t.Repo, t.Arch, pkg.Name)
		if err != nil {
			slog.Warn("obs: blocked reason", "pkg", pkg.Name, "repo", t.Repo, "arch", t.Arch, "err", err)
			continue
		}
		pkg.Targets[i].BlockedBy = reason
	}
}
```

- [ ] **Step 4: Wire `EnrichBlockedTargets` into `tick()`**

In `backend/internal/obs/poller.go`, inside the `tick()` function, find the line (around line 87):

```go
			pkg := buildPackage(project, pkgName, scope, targets)
			key := project + "/" + pkgName
```

Add the enrichment call between those two lines:

```go
			pkg := buildPackage(project, pkgName, scope, targets)
			EnrichBlockedTargets(ctx, p.client, pkg)
			key := project + "/" + pkgName
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
cd backend && go test ./internal/obs/... -v
```

Expected: all tests PASS including `TestEnrichBlockedTargets`

- [ ] **Step 6: Commit**

```bash
cd backend && git add internal/obs/poller.go internal/obs/client_test.go
git commit -s -m "feat(backend): add EnrichBlockedTargets and wire into poller tick"
```

---

## Task 3: Consumer enrichment

**Goal:** Add `obsClient` to the MQ consumer, thread `ctx` through `handle`, and call `EnrichBlockedTargets` before upserting packages with blocked targets.

**Files:**
- Modify: `backend/internal/mq/consumer.go`
- Modify: `backend/cmd/obsboard/main.go`

**Acceptance Criteria:**
- [ ] `go build ./...` exits 0
- [ ] `Consumer` struct has an `obsClient *obs.Client` field
- [ ] `NewConsumer` signature is `func NewConsumer(url string, db *sql.DB, h *hubpkg.Hub, obsClient *obs.Client) *Consumer`
- [ ] `handle` accepts `ctx context.Context` as its first parameter
- [ ] `obs.EnrichBlockedTargets(ctx, c.obsClient, pkg)` is called before `c.upsertPackage(pkg)` in the `isPackageBuildEvent` branch
- [ ] `main.go` passes `obsClient` to `NewConsumer`

**Verify:** `cd backend && go build ./...` → exits 0, no output

**Steps:**

- [ ] **Step 1: Add `obsClient` field and update `NewConsumer`**

In `backend/internal/mq/consumer.go`, add the `obs` import. The current import block is:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	amqp "github.com/rabbitmq/amqp091-go"
)
```

Replace with (adding `obs` import):

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	amqp "github.com/rabbitmq/amqp091-go"
)
```

Current `Consumer` struct (lines 46–50):
```go
type Consumer struct {
	url string
	db  *sql.DB
	hub *hubpkg.Hub
}
```

Replace with:
```go
type Consumer struct {
	url       string
	db        *sql.DB
	hub       *hubpkg.Hub
	obsClient *obs.Client
}
```

Current `NewConsumer` (lines 52–54):
```go
func NewConsumer(url string, db *sql.DB, h *hubpkg.Hub) *Consumer {
	return &Consumer{url: url, db: db, hub: h}
}
```

Replace with:
```go
func NewConsumer(url string, db *sql.DB, h *hubpkg.Hub, obsClient *obs.Client) *Consumer {
	return &Consumer{url: url, db: db, hub: h, obsClient: obsClient}
}
```

- [ ] **Step 2: Thread `ctx` through `run` → `handle`**

In `run(ctx context.Context)`, find the message dispatch line (around line 157):

```go
			c.handle(msg)
```

Replace with:

```go
			c.handle(ctx, msg)
```

Update the `handle` signature (line 162):

```go
func (c *Consumer) handle(msg amqp.Delivery) {
```

Replace with:

```go
func (c *Consumer) handle(ctx context.Context, msg amqp.Delivery) {
```

- [ ] **Step 3: Call `EnrichBlockedTargets` before `upsertPackage`**

In `handle`, find the `isPackageBuildEvent` branch (around line 299):

```go
		pkg := c.mergePackageTarget(m, scope, rollup)

		if err := c.upsertPackage(pkg); err != nil {
```

Replace with:

```go
		pkg := c.mergePackageTarget(m, scope, rollup)
		obs.EnrichBlockedTargets(ctx, c.obsClient, pkg)

		if err := c.upsertPackage(pkg); err != nil {
```

- [ ] **Step 4: Update `main.go` to pass `obsClient` to `NewConsumer`**

In `backend/cmd/obsboard/main.go`, find line 47:

```go
	consumer := mq.NewConsumer(cfg.MQ.URL, db, h)
```

Replace with:

```go
	consumer := mq.NewConsumer(cfg.MQ.URL, db, h, obsClient)
```

- [ ] **Step 5: Build to verify**

```bash
cd backend && go build ./...
```

Expected: exits 0, no output

- [ ] **Step 6: Run all tests**

```bash
cd backend && go test ./...
```

Expected: all packages pass (the two pre-existing test failures in `obs` and `config` that existed before this feature are already fixed)

- [ ] **Step 7: Commit**

```bash
cd backend && git add internal/mq/consumer.go cmd/obsboard/main.go
git commit -s -m "feat(backend): wire MQ consumer to enrich blocked targets via obs client"
```

---

## Task 4: Frontend — show blocking reason in PackageCard

**Goal:** Add `blocked_by` to the `Target` type and display it as a second line in blocked target rows.

**Files:**
- Modify: `frontend/src/types/api.ts`
- Modify: `frontend/src/components/PackageCard.vue`

**Acceptance Criteria:**
- [ ] `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` exits 0
- [ ] `Target` interface has `blocked_by?: string`
- [ ] Blocked target rows with a non-empty `blocked_by` show the reason string on a second line below the repo/arch
- [ ] Non-blocked target rows are visually unchanged
- [ ] Blocked target rows without a `blocked_by` (empty or undefined) are visually unchanged

**Verify:** `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` → exits 0, no output

**Steps:**

- [ ] **Step 1: Add `blocked_by` to `Target` in `types/api.ts`**

Current `Target` interface (lines 17–21):
```ts
export interface Target {
  repo: string
  arch: string
  state: BuildState
}
```

Replace with:
```ts
export interface Target {
  repo: string
  arch: string
  state: BuildState
  blocked_by?: string
}
```

- [ ] **Step 2: Update the failing target rows in `PackageCard.vue`**

In `backend/../frontend/src/components/PackageCard.vue`, find the `v-for` block that renders failing targets (lines 115–131):

```html
        <a
          v-for="t in visibleFailing"
          :key="`${t.repo}-${t.arch}`"
          :href="logUrl(t.repo, t.arch)"
          target="_blank"
          rel="noopener"
          :style="{
            display: 'flex', alignItems: 'center', gap: '9px',
            textDecoration: 'none', padding: '5px 9px', borderRadius: '7px',
            background: STATE_BG[t.state] ?? 'var(--blocked-tint)',
          }"
        >
          <span :style="{ width: '8px', height: '8px', borderRadius: '2px', background: STATE_COLOR[t.state] ?? 'var(--blocked)', flexShrink: '0' }"></span>
          <code style="font-family: var(--font-mono); font-size: 11.5px; color: var(--text-primary); flex-shrink: 0;">{{ t.repo }}/{{ t.arch }}</code>
          <span :style="{ fontSize: '11px', color: STATE_COLOR[t.state] ?? 'var(--text-secondary)', marginLeft: 'auto', fontWeight: '600', flexShrink: '0' }">{{ t.state }}</span>
          <span style="font-size: 10.5px; color: var(--brand-purple); font-weight: 700; flex-shrink: 0;">log ↗</span>
        </a>
```

Replace with (outer `<a>` becomes `flexDirection: 'column'`, existing row content moves into an inner `<div>`, blocked reason added as conditional `<span>`):

```html
        <a
          v-for="t in visibleFailing"
          :key="`${t.repo}-${t.arch}`"
          :href="logUrl(t.repo, t.arch)"
          target="_blank"
          rel="noopener"
          :style="{
            display: 'flex', flexDirection: 'column', gap: '3px',
            textDecoration: 'none', padding: '5px 9px', borderRadius: '7px',
            background: STATE_BG[t.state] ?? 'var(--blocked-tint)',
          }"
        >
          <div style="display: flex; align-items: center; gap: 9px;">
            <span :style="{ width: '8px', height: '8px', borderRadius: '2px', background: STATE_COLOR[t.state] ?? 'var(--blocked)', flexShrink: '0' }"></span>
            <code style="font-family: var(--font-mono); font-size: 11.5px; color: var(--text-primary); flex-shrink: 0;">{{ t.repo }}/{{ t.arch }}</code>
            <span :style="{ fontSize: '11px', color: STATE_COLOR[t.state] ?? 'var(--text-secondary)', marginLeft: 'auto', fontWeight: '600', flexShrink: '0' }">{{ t.state }}</span>
            <span style="font-size: 10.5px; color: var(--brand-purple); font-weight: 700; flex-shrink: 0;">log ↗</span>
          </div>
          <span
            v-if="t.state === 'blocked' && t.blocked_by"
            style="font-family: var(--font-mono); font-size: 10.5px; color: var(--text-muted); padding-left: 17px;"
          >{{ t.blocked_by }}</span>
        </a>
```

- [ ] **Step 3: Run type check**

```bash
cd frontend && ./node_modules/.bin/vue-tsc --noEmit
```

Expected: exits 0, no output

- [ ] **Step 4: Commit**

```bash
cd frontend && git add src/types/api.ts src/components/PackageCard.vue
git commit -s -m "feat(frontend): show blocking reason in blocked package target rows"
```
