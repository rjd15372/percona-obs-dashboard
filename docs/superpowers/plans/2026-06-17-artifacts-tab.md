# Artifacts Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an Artifacts tab to the PPG Build Board that lets users browse RPM/DEB packages and container images published to OBS, copy repo setup instructions, and get `docker pull` commands — sourced entirely from the existing `/packages` API with a backend extension to store all container tags.

**Architecture:** A segmented pill switcher in the header toggles between Build Board and Artifacts panels. The Artifacts panel is a pure read-only view driven by a `useArtifacts` composable over the existing packages data. The only backend change is extending `Package` to store the full `ContainerTags []string` (currently only `tags[0]` is kept) and updating `client.PackageContainerTags` to return `[]string` instead of `string`.

**Tech Stack:** Go backend (model, store, obs client + task), Vue 3 Composition API + TypeScript frontend, SQLite store with idempotent `ALTER TABLE` migration, no new API endpoints.

**User decisions (already made):**
- Reuse existing backend package data (Option A) — no new API endpoints needed.
- Container image variants are separate OBS projects (`isv:percona:ppg:17:containers:ubi8`), already separate `Package` rows.
- All container tags must be stored (not just `tags[0]`); `Version` keeps `tags[0]` for existing version badge.
- Component approach B: shallow tree + one composable (`useArtifacts`).

---

## File map

| File | Action | Responsibility |
|---|---|---|
| `backend/internal/model/types.go` | Modify | Add `ContainerTags []string` to `Package` |
| `backend/internal/store/db.go` | Modify | Add `container_tags` column to schema + additive migration + include it in `migrateIsContainerNullable` |
| `backend/internal/store/packages.go` | Modify | `UpsertPackageState` marshals `ContainerTags`; `scanPackages` unmarshals it |
| `backend/internal/obs/client.go` | Modify | `PackageContainerTags` returns `[]string` instead of `string` |
| `backend/internal/obs/tasks.go` | Modify | `ContainerTagsTask` assigns `pkg.ContainerTags = tags` |
| `frontend/src/types/api.ts` | Modify | Add `container_tags?: string[]` to `Package`; add `published?: boolean` to `Target` |
| `frontend/src/composables/useArtifacts.ts` | Create | Derives `packageRows` and `containerImages` from raw packages |
| `frontend/src/components/AppHeader.vue` | Modify | Add `mainTab` prop + tab switcher pill group |
| `frontend/src/components/ArtifactsVersionBar.vue` | Create | Version pill row + OBS root chip |
| `frontend/src/components/PackagesSubTab.vue` | Create | Distro sidebar + combined repo+snippet card + package list card |
| `frontend/src/components/ContainersSubTab.vue` | Create | Container image card grid |
| `frontend/src/components/ArtifactsPanel.vue` | Create | Top-level panel: state + composable + assembles sub-components |
| `frontend/src/App.vue` | Modify | Add `mainTab` state, pass `rawPackages` to `ArtifactsPanel`, wire version emit |

---

## Task 1: Backend — ContainerTags in model, DB schema, and store

**Goal:** Add `ContainerTags []string` to the `Package` model, persist it as JSON in SQLite, and verify it round-trips correctly.

**Files:**
- Modify: `backend/internal/model/types.go`
- Modify: `backend/internal/store/db.go`
- Modify: `backend/internal/store/packages.go`
- Test: `backend/internal/store/packages_test.go`

**Acceptance Criteria:**
- [ ] `model.Package` has `ContainerTags []string` with JSON tag `container_tags,omitempty`
- [ ] `db.go` schema includes `container_tags TEXT NOT NULL DEFAULT '[]'`
- [ ] Additive migration `ALTER TABLE packages ADD COLUMN container_tags TEXT NOT NULL DEFAULT '[]'` is present
- [ ] `migrateIsContainerNullable` includes `container_tags` in both the `packages_new` CREATE TABLE and the INSERT … SELECT
- [ ] `UpsertPackageState` marshals `p.ContainerTags` to JSON; `scanPackages` unmarshals it back
- [ ] `TestContainerTagsRoundtrip` passes: upsert a container package with `ContainerTags: []string{"18.4-1-1.7","18.4-1"}`, query it back, assert equality

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/store/... -v` → all tests pass including `TestContainerTagsRoundtrip`

**Steps:**

- [ ] **Step 1: Add ContainerTags to model**

Edit `backend/internal/model/types.go`, add after the `Version` field in `Package`:

```go
ContainerTags  []string   `json:"container_tags,omitempty"`
```

Full `Package` struct after the change (lines 66-79):

```go
type Package struct {
	Project        string      `json:"project"`
	Name           string      `json:"name"`
	Scope          Scope       `json:"scope"`
	RollupState    RollupState `json:"rollup_state"`
	OKTargets      int         `json:"ok_targets"`
	TotalTargets   int         `json:"total_targets"`
	IsContainer    *bool       `json:"is_container,omitempty"`
	Version        string      `json:"version,omitempty"`
	ContainerTags  []string    `json:"container_tags,omitempty"`
	Trigger        *Trigger    `json:"trigger,omitempty"`
	Targets        []Target    `json:"targets"`
	UpdatedAt      time.Time   `json:"updated_at"`
	StateChangedAt *time.Time  `json:"state_changed_at,omitempty"`
}
```

- [ ] **Step 2: Add container_tags to the schema in db.go**

In `backend/internal/store/db.go`, inside `const schema`, add `container_tags` after the `version` column:

```sql
CREATE TABLE IF NOT EXISTS packages (
    project        TEXT NOT NULL,
    name           TEXT NOT NULL,
    scope          TEXT NOT NULL,
    rollup_state   TEXT NOT NULL,
    ok_targets     INTEGER NOT NULL DEFAULT 0,
    total_targets  INTEGER NOT NULL DEFAULT 0,
    trigger_what   TEXT,
    trigger_kind   TEXT,
    trigger_at     DATETIME,
    targets_json    TEXT NOT NULL DEFAULT '[]',
    updated_at      DATETIME NOT NULL,
    state_changed_at DATETIME,
    is_container   INTEGER,
    version        TEXT NOT NULL DEFAULT '',
    container_tags TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (project, name)
);
```

- [ ] **Step 3: Add additive migration in db.go**

In `Open()`, after the existing additive migrations, add:

```go
db.Exec(`ALTER TABLE packages ADD COLUMN container_tags TEXT NOT NULL DEFAULT '[]'`)
```

The full migrations block becomes:

```go
db.Exec(`ALTER TABLE packages ADD COLUMN state_changed_at DATETIME`)
db.Exec(`ALTER TABLE packages ADD COLUMN is_container INTEGER`)
db.Exec(`ALTER TABLE packages ADD COLUMN version TEXT NOT NULL DEFAULT ''`)
db.Exec(`ALTER TABLE events ADD COLUMN version TEXT NOT NULL DEFAULT ''`)
db.Exec(`ALTER TABLE packages ADD COLUMN container_tags TEXT NOT NULL DEFAULT '[]'`)
```

- [ ] **Step 4: Update migrateIsContainerNullable to include container_tags**

In `migrateIsContainerNullable`, update the two statements in `stmts`:

```go
stmts := []string{
    `DROP TABLE IF EXISTS packages_new`,
    `CREATE TABLE packages_new (
        project          TEXT NOT NULL,
        name             TEXT NOT NULL,
        scope            TEXT NOT NULL,
        rollup_state     TEXT NOT NULL,
        ok_targets       INTEGER NOT NULL DEFAULT 0,
        total_targets    INTEGER NOT NULL DEFAULT 0,
        trigger_what     TEXT,
        trigger_kind     TEXT,
        trigger_at       DATETIME,
        targets_json     TEXT NOT NULL DEFAULT '[]',
        updated_at       DATETIME NOT NULL,
        state_changed_at DATETIME,
        is_container     INTEGER,
        version          TEXT NOT NULL DEFAULT '',
        container_tags   TEXT NOT NULL DEFAULT '[]',
        PRIMARY KEY (project, name)
    )`,
    `INSERT INTO packages_new
        SELECT project, name, scope, rollup_state, ok_targets, total_targets,
               trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
               state_changed_at,
               CASE WHEN is_container = 1 THEN 1 ELSE NULL END,
               version,
               container_tags
        FROM packages`,
    `DROP TABLE packages`,
    `ALTER TABLE packages_new RENAME TO packages`,
    `CREATE INDEX IF NOT EXISTS idx_packages_rollup_state ON packages(rollup_state)`,
}
```

- [ ] **Step 5: Update packageSelectCols and scanPackages in packages.go**

Update `packageSelectCols` to include `container_tags`:

```go
const packageSelectCols = ` project, name, scope, rollup_state, ok_targets, total_targets,
	trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
	state_changed_at, is_container, version, container_tags`
```

Update `scanPackages` — add `var containerTagsJSON string` and scan it, then unmarshal:

```go
func scanPackages(rows *sql.Rows) ([]*model.Package, error) {
	pkgs := make([]*model.Package, 0)
	for rows.Next() {
		p := &model.Package{}
		var trigWhat, trigKind sql.NullString
		var trigAt sql.NullTime
		var targetsJSON string
		var stateChangedAt sql.NullTime
		var isContainerNull sql.NullInt64
		var containerTagsJSON string
		if err := rows.Scan(
			&p.Project, &p.Name, &p.Scope, &p.RollupState,
			&p.OKTargets, &p.TotalTargets,
			&trigWhat, &trigKind, &trigAt,
			&targetsJSON, &p.UpdatedAt,
			&stateChangedAt, &isContainerNull, &p.Version,
			&containerTagsJSON,
		); err != nil {
			return nil, err
		}
		if isContainerNull.Valid {
			v := isContainerNull.Int64 != 0
			p.IsContainer = &v
		}
		if trigWhat.Valid {
			p.Trigger = &model.Trigger{
				What: trigWhat.String,
				Kind: trigKind.String,
				At:   trigAt.Time,
			}
		}
		if stateChangedAt.Valid {
			t := stateChangedAt.Time
			p.StateChangedAt = &t
		}
		if err := json.Unmarshal([]byte(targetsJSON), &p.Targets); err != nil {
			return nil, err
		}
		if containerTagsJSON != "" && containerTagsJSON != "[]" {
			if err := json.Unmarshal([]byte(containerTagsJSON), &p.ContainerTags); err != nil {
				return nil, err
			}
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, rows.Err()
}
```

- [ ] **Step 6: Update UpsertPackageState to include container_tags**

In `UpsertPackageState`, marshal `ContainerTags` before the exec call:

```go
containerTagsJSON, err := json.Marshal(p.ContainerTags)
if err != nil {
    return err
}
if containerTagsJSON == nil {
    containerTagsJSON = []byte("[]")
}
```

Update the INSERT to include `container_tags` in the column list and `?` in values, and update the ON CONFLICT SET clause:

```go
_, err = db.Exec(`
    INSERT INTO packages
        (project, name, scope, rollup_state, ok_targets, total_targets,
         trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
         state_changed_at, is_container, version, container_tags)
    VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(project, name) DO UPDATE SET
        scope=excluded.scope, rollup_state=excluded.rollup_state,
        ok_targets=excluded.ok_targets, total_targets=excluded.total_targets,
        trigger_what=excluded.trigger_what, trigger_kind=excluded.trigger_kind,
        trigger_at=excluded.trigger_at, targets_json=excluded.targets_json,
        updated_at=excluded.updated_at,
        is_container=excluded.is_container,
        version=excluded.version,
        container_tags=excluded.container_tags,
        state_changed_at = CASE
            WHEN excluded.rollup_state != rollup_state THEN excluded.state_changed_at
            WHEN state_changed_at IS NULL             THEN excluded.state_changed_at
            ELSE state_changed_at
        END`,
    p.Project, p.Name, string(p.Scope), string(p.RollupState),
    p.OKTargets, p.TotalTargets,
    trigWhat, trigKind, trigAt,
    string(targetsJSON), p.UpdatedAt,
    now,
    isContainerVal, p.Version,
    string(containerTagsJSON),
)
return err
```

- [ ] **Step 7: Write the test**

Add `TestContainerTagsRoundtrip` to `backend/internal/store/packages_test.go`:

```go
func TestContainerTagsRoundtrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:       "isv:percona:ppg:17:containers:ubi9",
		Name:          "percona-distribution-postgresql",
		Scope:         model.ScopeContainer,
		RollupState:   model.RollupSucceeded,
		IsContainer:   boolPtr(true),
		Version:       "17.4-1-1.7",
		ContainerTags: []string{"17.4-1-1.7", "17.4-1", "17"},
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     now,
	}
	if err := UpsertPackageState(db, p, now); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	got := pkgs[0]
	if len(got.ContainerTags) != 3 {
		t.Fatalf("ContainerTags: want 3, got %d: %v", len(got.ContainerTags), got.ContainerTags)
	}
	if got.ContainerTags[0] != "17.4-1-1.7" {
		t.Errorf("ContainerTags[0]: want %q, got %q", "17.4-1-1.7", got.ContainerTags[0])
	}
	if got.ContainerTags[2] != "17" {
		t.Errorf("ContainerTags[2]: want %q, got %q", "17", got.ContainerTags[2])
	}

	// Nil ContainerTags must not produce null in JSON — omitempty handles it.
	p2 := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg_tde",
		Scope: model.ScopeVersion, RollupState: model.RollupSucceeded,
		Targets: []model.Target{}, UpdatedAt: now,
	}
	if err := UpsertPackageState(db, p2, now); err != nil {
		t.Errorf("upsert nil ContainerTags: %v", err)
	}
	pkgs2, _ := QueryPackages(db, "isv:percona:ppg:17")
	for _, pkg := range pkgs2 {
		if pkg.Name == "pg_tde" && pkg.ContainerTags != nil {
			t.Errorf("pg_tde: ContainerTags should be nil, got %v", pkg.ContainerTags)
		}
	}
}
```

- [ ] **Step 8: Run tests and commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard/backend
go test ./internal/store/... -v
```

Expected: all tests pass including `TestContainerTagsRoundtrip`.

```bash
git add backend/internal/model/types.go backend/internal/store/db.go \
        backend/internal/store/packages.go backend/internal/store/packages_test.go
git commit -s -m "feat(backend): add container_tags field to Package model and store"
```

---

## Task 2: Backend — ContainerTagsTask stores all tags

**Goal:** Change `client.PackageContainerTags` to return `[]string` and update `ContainerTagsTask` to assign the full list to `pkg.ContainerTags`.

**Files:**
- Modify: `backend/internal/obs/client.go`
- Modify: `backend/internal/obs/tasks.go`
- Test: `backend/internal/obs/tasks_test.go`

**Acceptance Criteria:**
- [ ] `client.PackageContainerTags` signature is `(ctx, project, repo, arch, pkg, filename string) ([]string, error)`
- [ ] It strips the image name prefix from every tag (same logic as before for tag[0]) so raw `"percona-distribution-postgresql:18.4-1"` becomes `"18.4-1"`
- [ ] `ContainerTagsTask.Run` assigns `pkg.Version = tags[0]` and `pkg.ContainerTags = tags`
- [ ] `TestContainerTagsTask` asserts `len(pkg.ContainerTags) == 2` and `pkg.ContainerTags[1] == "18.4-1"`

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/backend && go test ./internal/obs/... -v` → all tests pass

**Steps:**

- [ ] **Step 1: Update PackageContainerTags in client.go**

Replace the existing `PackageContainerTags` function (starting at line 471 approximately):

```go
// PackageContainerTags fetches a .containerinfo JSON file and returns all tag
// strings with the image-name prefix stripped (e.g. "percona-distribution-postgresql:18.4-1-1.7"
// → "18.4-1-1.7"). Returns nil if tags is empty.
func (c *Client) PackageContainerTags(ctx context.Context, project, repo, arch, pkg, filename string) ([]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg),
		url.PathEscape(filename))
	resp, err := c.getFile(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info struct {
		Tags []string `json:"tags"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse containerinfo: %w", err)
	}
	if len(info.Tags) == 0 {
		return nil, nil
	}
	tags := make([]string, 0, len(info.Tags))
	for _, raw := range info.Tags {
		tag := raw
		if idx := strings.LastIndex(raw, ":"); idx >= 0 {
			tag = raw[idx+1:]
		}
		tags = append(tags, tag)
	}
	return tags, nil
}
```

- [ ] **Step 2: Update ContainerTagsTask in tasks.go**

Replace the body of `ContainerTagsTask.Run`:

```go
func (t ContainerTagsTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	if pkg.IsContainer == nil || !*pkg.IsContainer || len(pkg.Targets) == 0 {
		return nil
	}
	target := firstSucceededTarget(pkg.Targets)
	filename, err := client.PackageContainerInfoFilename(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name)
	if err != nil {
		slog.Warn("obs: container info filename", "pkg", pkg.Name, "err", err)
		return nil
	}
	if filename == "" {
		return nil
	}
	tags, err := client.PackageContainerTags(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name, filename)
	if err != nil {
		slog.Warn("obs: container tags", "pkg", pkg.Name, "err", err)
		return nil
	}
	if len(tags) == 0 {
		return nil
	}
	pkg.Version = tags[0]
	pkg.ContainerTags = tags
	return nil
}
```

- [ ] **Step 3: Update TestContainerTagsTask in tasks_test.go**

The existing test mock returns:
```json
{"tags":["percona-distribution-postgresql:18.4-1-1.7","percona-distribution-postgresql:18.4-1"]}
```

After the change, `pkg.ContainerTags` should be `["18.4-1-1.7", "18.4-1"]`. Update the test assertions:

```go
func TestContainerTagsTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-1-1.7","percona-distribution-postgresql:18.4-1"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		Scope:       model.ScopeContainer,
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		Targets:     []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.ContainerTagsTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "18.4-1-1.7" {
		t.Errorf("Version: want %q, got %q", "18.4-1-1.7", pkg.Version)
	}
	if len(pkg.ContainerTags) != 2 {
		t.Fatalf("ContainerTags: want 2, got %d: %v", len(pkg.ContainerTags), pkg.ContainerTags)
	}
	if pkg.ContainerTags[0] != "18.4-1-1.7" {
		t.Errorf("ContainerTags[0]: want %q, got %q", "18.4-1-1.7", pkg.ContainerTags[0])
	}
	if pkg.ContainerTags[1] != "18.4-1" {
		t.Errorf("ContainerTags[1]: want %q, got %q", "18.4-1", pkg.ContainerTags[1])
	}
}
```

- [ ] **Step 4: Run tests and commit**

```bash
cd /home/rdias/Work/percona-obs-dashboard/backend
go test ./internal/obs/... -v
```

Expected: all tests pass including the updated `TestContainerTagsTask`.

```bash
git add backend/internal/obs/client.go backend/internal/obs/tasks.go \
        backend/internal/obs/tasks_test.go
git commit -s -m "feat(backend): ContainerTagsTask stores all image tags, not just the first"
```

---

## Task 3: Frontend — api.ts type updates

**Goal:** Add `container_tags?: string[]` to the `Package` interface and `published?: boolean` to the `Target` interface so the frontend can use both fields without TypeScript errors.

**Files:**
- Modify: `frontend/src/types/api.ts`

**Acceptance Criteria:**
- [ ] `Package` interface has `container_tags?: string[]`
- [ ] `Target` interface has `published?: boolean`
- [ ] TypeScript compilation passes with no errors

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Update api.ts**

Replace the content of `frontend/src/types/api.ts` with:

```ts
export type BuildState = 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'scheduled' | 'building' | 'finished'
export type PackageScope = 'common' | 'ppgcommon' | 'version' | 'container' | 'release' | 'pr'
export type EventType = 'triggered' | 'started' | 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'published' | 'created' | 'deleted' | 'build_started' | 'build_finished' | 'version_change' | 'updated'

export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg" or "/api/pr/pr-92/ppg"
  prefix: string   // e.g. "isv:percona:ppg" or "isv:percona:PR:pr-92:ppg"
}

export interface Trigger {
  what: string
  kind: string
  at: string // ISO 8601
}

export interface Target {
  repo: string
  arch: string
  state: BuildState
  details?: string
  blocked_by?: string
  build_reason?: string
  build_reason_packages?: string[]
  published?: boolean
}

export interface Package {
  project: string
  name: string
  scope: PackageScope
  rollup_state: BuildState
  ok_targets: number
  total_targets: number
  is_container?: boolean
  version?: string
  container_tags?: string[]
  trigger?: Trigger // optional
  targets: Target[]
  updated_at: string // ISO 8601
  state_changed_at?: string // ISO 8601; absent when NULL
}

export interface PRGroup {
  pr: string
  rollup_state: BuildState
  packages: Package[]
}

export interface Event {
  id: string
  type: EventType
  scope: string
  project: string
  package: string
  repo?: string // optional
  arch?: string // optional
  what: string
  why: string
  version?: string
  url: string
  at: string // ISO 8601
}
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/types/api.ts
git commit -s -m "feat(frontend): add container_tags and Target.published to API types"
```

---

## Task 4: Frontend — useArtifacts composable

**Goal:** Create `useArtifacts.ts` — the single composable that derives `packageRows` and `containerImages` from the raw packages array, and exports the static `REPOS` list.

**Files:**
- Create: `frontend/src/composables/useArtifacts.ts`

**Acceptance Criteria:**
- [ ] Exports `REPOS: Repo[]` with exactly 6 entries in the order: el9, el8, deb12, deb11, ub2404, ub2204
- [ ] `packageRows` filters to scopes `common`, `ppgcommon`, `version`; for `version` scope it additionally filters by `pkg.project.includes(':ppg:' + version)` when version is non-empty
- [ ] `packageRows` only includes packages that have a target matching the selected `artRepo` × `artArch`
- [ ] `containerImages` maps all `container`-scoped packages to `ContainerImage` objects; `baseOs` is derived from the `:containers:` suffix of the project path
- [ ] TypeScript compilation passes

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Create the file**

Create `frontend/src/composables/useArtifacts.ts`:

```ts
import { computed } from 'vue'
import type { ComputedRef, Ref } from 'vue'
import type { Package, Target } from '../types/api'

export interface Repo {
  short: string
  name: string
  obs: string
  type: 'rpm' | 'deb'
}

export const REPOS: Repo[] = [
  { short: 'el9',    name: 'RHEL 9',       obs: 'RHEL_9',        type: 'rpm' },
  { short: 'el8',    name: 'RHEL 8',       obs: 'RHEL_8',        type: 'rpm' },
  { short: 'deb12',  name: 'Debian 12',    obs: 'Debian_12',     type: 'deb' },
  { short: 'deb11',  name: 'Debian 11',    obs: 'Debian_11',     type: 'deb' },
  { short: 'ub2404', name: 'Ubuntu 24.04', obs: 'xUbuntu_24.04', type: 'deb' },
  { short: 'ub2204', name: 'Ubuntu 22.04', obs: 'xUbuntu_22.04', type: 'deb' },
]

const BASE_OS_LABELS: Record<string, string> = {
  ubi8: 'Red Hat UBI 8',
  ubi9: 'Red Hat UBI 9',
}

function deriveBaseOs(project: string): string {
  const suffix = project.split(':containers:')[1] ?? ''
  return BASE_OS_LABELS[suffix] ?? suffix
}

export interface PackageRow {
  pkg: Package
  target: Target
  repoType: 'rpm' | 'deb'
}

export interface ContainerImage {
  id: string
  name: string
  project: string
  baseOs: string
  tags: string[]
  pullCmd: string
  published: boolean
}

export function useArtifacts(
  packages: Ref<Package[]>,
  version: Ref<string>,
  artRepo: Ref<string>,
  artArch: Ref<string>,
) {
  const packageRows: ComputedRef<PackageRow[]> = computed(() => {
    const repo = REPOS.find(r => r.short === artRepo.value)
    if (!repo) return []
    return packages.value
      .filter(pkg => ['common', 'ppgcommon', 'version'].includes(pkg.scope))
      .filter(pkg =>
        pkg.scope !== 'version' ||
        !version.value ||
        pkg.project.includes(`:ppg:${version.value}`)
      )
      .flatMap(pkg => {
        const target = pkg.targets.find(
          t => t.repo === repo.obs && t.arch === artArch.value
        )
        if (!target) return []
        return [{ pkg, target, repoType: repo.type }]
      })
  })

  const containerImages: ComputedRef<ContainerImage[]> = computed(() =>
    packages.value
      .filter(pkg => pkg.scope === 'container')
      .map(pkg => ({
        id:        `${pkg.project}/${pkg.name}`,
        name:      pkg.name,
        project:   pkg.project,
        baseOs:    deriveBaseOs(pkg.project),
        tags:      pkg.container_tags ?? [],
        pullCmd:   `docker pull percona/${pkg.name}:${(pkg.container_tags ?? [])[0] ?? ''}`,
        published: pkg.targets.some(t => t.published === true),
      }))
  )

  return { packageRows, containerImages, REPOS }
}
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/composables/useArtifacts.ts
git commit -s -m "feat(frontend): add useArtifacts composable"
```

---

## Task 5: Frontend — AppHeader tab switcher

**Goal:** Add a `mainTab` prop and a pill-group tab switcher to `AppHeader.vue` that lets users toggle between Build Board and Artifacts.

**Files:**
- Modify: `frontend/src/components/AppHeader.vue`

**Acceptance Criteria:**
- [ ] Component accepts `mainTab: 'board' | 'artifacts'` prop
- [ ] Component emits `update:main-tab` with the new tab value on pill click
- [ ] Pill group appears between the title and the theme toggle button
- [ ] Active pill: `background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.12); border: none`
- [ ] Inactive pill: `background: transparent; color: var(--text-muted); border: none`
- [ ] Wrapper: `background: var(--bg-muted); padding: 3px; border-radius: 11px`
- [ ] TypeScript compilation passes

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Update AppHeader.vue**

Replace the entire file with:

```vue
<script setup lang="ts">
defineProps<{ theme: 'light' | 'dark'; mainTab: 'board' | 'artifacts' }>()
const emit = defineEmits<{ 'toggle-theme': []; 'update:main-tab': [tab: 'board' | 'artifacts'] }>()
</script>

<template>
  <header style="display: flex; align-items: center; justify-content: space-between; gap: 20px;">
    <div style="display: flex; align-items: center; gap: 12px;">
      <div style="width: 34px; height: 34px; border-radius: 9px; background: var(--brand-purple); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: 800; font-size: 17px; font-family: 'Roboto Condensed', 'Roboto', sans-serif;">P</div>
      <div style="display: flex; flex-direction: column; gap: 1px;">
        <h1 style="margin: 0; font-size: 21px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-primary);">PPG Build Board</h1>
        <span style="font-size: 12.5px; color: var(--text-muted);">Failure-first build monitor across every subproject of a product</span>
      </div>
    </div>

    <div style="display: flex; align-items: center; gap: 12px;">
      <!-- Main tab switcher -->
      <div style="display: flex; gap: 3px; background: var(--bg-muted); padding: 3px; border-radius: 11px;">
        <button
          @click="emit('update:main-tab', 'board')"
          :style="mainTab === 'board'
            ? 'background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.12);'
            : 'background: transparent; color: var(--text-muted);'"
          style="padding: 6px 14px; border-radius: 8px; font-size: 13px; font-weight: 700; border: none; cursor: pointer; font-family: inherit;"
        >Build Board</button>
        <button
          @click="emit('update:main-tab', 'artifacts')"
          :style="mainTab === 'artifacts'
            ? 'background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.12);'
            : 'background: transparent; color: var(--text-muted);'"
          style="padding: 6px 14px; border-radius: 8px; font-size: 13px; font-weight: 700; border: none; cursor: pointer; font-family: inherit;"
        >Artifacts</button>
      </div>

      <!-- Theme toggle -->
      <button
        @click="emit('toggle-theme')"
        style="flex-shrink: 0; display: inline-flex; align-items: center; gap: 8px; padding: 8px 14px; border-radius: 10px; border: 1px solid var(--border); background: var(--bg-card); color: var(--text-secondary); font-family: inherit; font-size: 13px; font-weight: 600; cursor: pointer;"
      >
        <span style="width: 8px; height: 8px; border-radius: 99px; background: var(--brand-purple);"></span>
        {{ theme === 'dark' ? 'Dark' : 'Light' }} mode
      </button>
    </div>
  </header>
</template>
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0. If App.vue errors because it doesn't pass `mainTab` yet, that's expected — it will be fixed in Task 9.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/AppHeader.vue
git commit -s -m "feat(frontend): add main tab switcher to AppHeader"
```

---

## Task 6: Frontend — ArtifactsVersionBar component

**Goal:** Create `ArtifactsVersionBar.vue` — a single horizontal card with a PG badge, version pills (both states bordered, active filled purple), and the OBS root chip.

**Files:**
- Create: `frontend/src/components/ArtifactsVersionBar.vue`

**Acceptance Criteria:**
- [ ] Accepts props: `version: string`, `availableVersions: string[]`, `obsRoot: string`
- [ ] Emits `update:version` with the clicked version string
- [ ] Active pill style: `background: var(--brand-purple); color: #fff; border: 1px solid var(--brand-purple)`
- [ ] Inactive pill style: `background: var(--bg-card); color: var(--text-secondary); border: 1px solid var(--border)`
- [ ] OBS root chip uses `font-family: var(--font-mono)` and `background: var(--bg-muted)`
- [ ] TypeScript compilation passes

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Create ArtifactsVersionBar.vue**

Create `frontend/src/components/ArtifactsVersionBar.vue`:

```vue
<script setup lang="ts">
defineProps<{
  version: string
  availableVersions: string[]
  obsRoot: string
}>()
const emit = defineEmits<{ 'update:version': [v: string] }>()
</script>

<template>
  <div style="background: var(--bg-card); border-radius: 12px; padding: 14px 18px; display: flex; align-items: center; gap: 14px; flex-wrap: wrap;">
    <!-- PG tech badge -->
    <span style="display: inline-flex; align-items: center; gap: 5px; padding: 4px 10px; border-radius: 7px; background: var(--tint-postgres); color: var(--tech-postgres); font-size: 12px; font-weight: 700; letter-spacing: 0.04em; flex-shrink: 0; font-family: 'Roboto Condensed', sans-serif;">
      PG
    </span>

    <!-- VERSION label -->
    <span style="font-size: 11px; font-weight: 700; color: var(--text-muted); letter-spacing: 0.06em; text-transform: uppercase; flex-shrink: 0;">
      VERSION
    </span>

    <!-- Version pills -->
    <div style="display: flex; gap: 6px; flex-wrap: wrap;">
      <button
        v-for="v in availableVersions"
        :key="v"
        @click="emit('update:version', v)"
        :style="v === version
          ? 'background: var(--brand-purple); color: #fff; border: 1px solid var(--brand-purple);'
          : 'background: var(--bg-card); color: var(--text-secondary); border: 1px solid var(--border);'"
        style="padding: 4px 12px; border-radius: 6px; font-size: 13px; font-weight: 600; cursor: pointer; font-family: inherit;"
      >{{ v }}</button>
    </div>

    <!-- OBS root chip -->
    <code style="margin-left: auto; font-size: 12px; background: var(--bg-muted); color: var(--text-muted); padding: 3px 8px; border-radius: 5px; font-family: var(--font-mono); white-space: nowrap;">{{ obsRoot }}</code>
  </div>
</template>
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/ArtifactsVersionBar.vue
git commit -s -m "feat(frontend): add ArtifactsVersionBar component"
```

---

## Task 7: Frontend — PackagesSubTab component

**Goal:** Create `PackagesSubTab.vue` — the two-column layout with a sticky distro sidebar and a right column containing a combined header+snippet card and a package list card.

**Files:**
- Create: `frontend/src/components/PackagesSubTab.vue`

**Acceptance Criteria:**
- [ ] Left sidebar shows RPM group (el9, el8) and DEB group (deb12, deb11, ub2404, ub2204) with correct group label borders
- [ ] Active repo row: `background: var(--brand-purple-tint); color: var(--brand-purple); font-weight: 700`
- [ ] Arch selector uses borderless pill-switcher style (wrapper with `var(--bg-muted)` background, active uses `var(--bg-card)` + box-shadow, no borders)
- [ ] Repo + setup are a single card; package list is a separate card
- [ ] `<pre>` code block uses `background: var(--bg-card-2)`
- [ ] RPM snippet and DEB snippet are both correct per spec
- [ ] Copy button shows "✓ Copied" when `copiedKey === 'repo-config'`
- [ ] Package list rows show scope badge, install command, build status, download button
- [ ] Download button is disabled (opacity 0.4, pointer-events none) when state ≠ `succeeded`
- [ ] TypeScript compilation passes

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Create PackagesSubTab.vue**

Create `frontend/src/components/PackagesSubTab.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { Repo, PackageRow } from '../composables/useArtifacts'

const props = defineProps<{
  packageRows: PackageRow[]
  repos: Repo[]
  artRepo: string
  artArch: string
  version: string
  copiedKey: string | null
}>()

const emit = defineEmits<{
  'update:art-repo': [repo: string]
  'update:art-arch': [arch: string]
  copy: [key: string, text: string]
}>()

const rpmRepos = computed(() => props.repos.filter(r => r.type === 'rpm'))
const debRepos = computed(() => props.repos.filter(r => r.type === 'deb'))

const selectedRepo = computed(() => props.repos.find(r => r.short === props.artRepo))

const snippet = computed(() => {
  const repo = selectedRepo.value
  if (!repo) return ''
  const v = props.version || '17'
  const obsRepo = repo.obs
  const repoName = repo.name
  if (repo.type === 'rpm') {
    return `[percona-ppg${v}]\nname=Percona PPG ${v} — ${repoName}\nbaseurl=https://download.opensuse.org/repositories/isv:percona:ppg:${v}/${obsRepo}/\nenabled=1\ngpgcheck=0\n\n# Save to /etc/yum.repos.d/percona-ppg${v}.repo, then:\ndnf makecache\ndnf install percona-postgresql${v}-server`
  }
  return `# 1. Add repository\necho "deb https://download.opensuse.org/repositories/isv:percona:ppg:${v}/${obsRepo}/ ./" \\\n  | sudo tee /etc/apt/sources.list.d/percona-ppg${v}.list\n\n# 2. Import GPG key\nwget -qO- https://download.opensuse.org/repositories/isv:percona:ppg:${v}/${obsRepo}/Release.key \\\n  | sudo apt-key add -\n\n# 3. Update and install\nsudo apt-get update\nsudo apt-get install percona-postgresql-${v}`
})

function scopeLabel(row: PackageRow): string {
  if (row.pkg.scope === 'version') return `PPG ${props.version || ''}`
  if (row.pkg.scope === 'ppgcommon') return 'PPG·Common'
  return 'Common'
}

function scopeStyle(row: PackageRow): string {
  if (row.pkg.scope === 'version') {
    return 'background: var(--brand-purple-tint); color: var(--brand-purple);'
  }
  return 'background: var(--bg-muted); color: var(--text-secondary);'
}

function stateColor(state: string): string {
  const colors: Record<string, string> = {
    succeeded: 'var(--ok)',
    failed: 'var(--fail)',
    broken: 'var(--broken)',
    blocked: 'var(--blocked)',
    unresolvable: 'var(--brand-purple)',
    building: 'var(--warn)',
    scheduled: 'var(--warn)',
    finished: 'var(--warn)',
  }
  return colors[state] ?? 'var(--text-muted)'
}

function downloadUrl(row: PackageRow): string {
  const obsRepo = selectedRepo.value?.obs ?? ''
  return `https://build.opensuse.org/package/binaries/${encodeURIComponent(row.pkg.project)}/${encodeURIComponent(row.pkg.name)}/${encodeURIComponent(obsRepo)}?arch=${encodeURIComponent(props.artArch)}`
}
</script>

<template>
  <div style="display: flex; gap: 16px; align-items: flex-start;">
    <!-- Distro sidebar -->
    <div style="width: 220px; flex-shrink: 0; background: var(--bg-card); border-radius: 12px; overflow: hidden; position: sticky; top: 24px;">
      <!-- RPM group -->
      <div style="padding: 8px 12px 6px; font-size: 10px; font-weight: 800; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.08em; border-bottom: 1px solid var(--border);">
        RPM
      </div>
      <button
        v-for="repo in rpmRepos" :key="repo.short"
        @click="emit('update:art-repo', repo.short)"
        :style="artRepo === repo.short
          ? 'background: var(--brand-purple-tint); color: var(--brand-purple); font-weight: 700;'
          : 'background: transparent; color: var(--text-secondary); font-weight: 500;'"
        style="display: block; width: 100%; text-align: left; padding: 9px 14px; font-size: 13px; border: none; cursor: pointer; font-family: inherit;"
      >{{ repo.name }}</button>

      <!-- DEB group -->
      <div style="padding: 8px 12px 6px; font-size: 10px; font-weight: 800; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.08em; border-top: 1px solid var(--border); border-bottom: 1px solid var(--border);">
        DEB
      </div>
      <button
        v-for="repo in debRepos" :key="repo.short"
        @click="emit('update:art-repo', repo.short)"
        :style="artRepo === repo.short
          ? 'background: var(--brand-purple-tint); color: var(--brand-purple); font-weight: 700;'
          : 'background: transparent; color: var(--text-secondary); font-weight: 500;'"
        style="display: block; width: 100%; text-align: left; padding: 9px 14px; font-size: 13px; border: none; cursor: pointer; font-family: inherit;"
      >{{ repo.name }}</button>
    </div>

    <!-- Right column -->
    <div style="flex: 1; display: flex; flex-direction: column; gap: 16px; min-width: 0;">

      <!-- Combined repo header + setup card -->
      <div style="background: var(--bg-card); border-radius: 14px; padding: 18px;">
        <!-- Repo header row -->
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px;">
          <div>
            <div style="font-size: 17px; font-weight: 700; color: var(--text-primary);">{{ selectedRepo?.name ?? '' }}</div>
            <code style="font-size: 12px; color: var(--text-muted); font-family: var(--font-mono);">{{ selectedRepo?.obs ?? '' }}</code>
          </div>
          <!-- Arch selector: borderless pill-switcher -->
          <div style="display: flex; gap: 3px; background: var(--bg-muted); padding: 3px; border-radius: 9px; flex-shrink: 0;">
            <button
              v-for="arch in ['x86_64', 'aarch64']" :key="arch"
              @click="emit('update:art-arch', arch)"
              :style="artArch === arch
                ? 'background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.10);'
                : 'background: transparent; color: var(--text-muted);'"
              style="padding: 5px 12px; border-radius: 6px; font-size: 12px; font-weight: 600; border: none; cursor: pointer; font-family: var(--font-mono);"
            >{{ arch }}</button>
          </div>
        </div>

        <!-- Repository setup label + snippet -->
        <div style="font-size: 10px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.07em; margin-bottom: 8px;">
          Repository Setup
        </div>
        <div style="position: relative;">
          <pre style="background: var(--bg-card-2); border-radius: 8px; padding: 14px; font-family: var(--font-mono); font-size: 12px; color: var(--text-secondary); overflow-x: auto; margin: 0; white-space: pre-wrap; word-break: break-word;">{{ snippet }}</pre>
          <button
            @click="emit('copy', 'repo-config', snippet)"
            :style="copiedKey === 'repo-config' ? 'color: var(--ok);' : 'color: var(--text-muted);'"
            style="position: absolute; top: 8px; right: 8px; padding: 4px 10px; border-radius: 6px; border: 1px solid var(--border); background: var(--bg-card); font-size: 11px; font-weight: 600; cursor: pointer; font-family: inherit;"
          >{{ copiedKey === 'repo-config' ? '✓ Copied' : 'Copy' }}</button>
        </div>
      </div>

      <!-- Package list card -->
      <div style="background: var(--bg-card); border-radius: 12px; padding: 18px;">
        <div style="margin-bottom: 14px;">
          <span style="font-size: 15px; font-weight: 700; color: var(--text-primary);">Packages</span>
          <span style="font-size: 12px; color: var(--text-muted); margin-left: 8px;">{{ packageRows.length }} available · {{ selectedRepo?.name ?? '' }} / {{ artArch }}</span>
        </div>

        <div v-if="packageRows.length === 0" style="color: var(--text-muted); font-size: 13px; padding: 12px 0;">
          No packages found for this repo / arch combination.
        </div>

        <div v-for="row in packageRows" :key="row.pkg.project + '/' + row.pkg.name"
          style="display: flex; align-items: center; gap: 10px; padding: 10px 0; border-bottom: 1px solid var(--border);">
          <!-- Package name -->
          <code style="font-family: var(--font-mono); font-size: 13.5px; font-weight: 700; color: var(--text-primary); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ row.pkg.name }}</code>

          <!-- Scope badge -->
          <span :style="scopeStyle(row)"
            style="font-size: 10px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; padding: 2px 7px; border-radius: 4px; flex-shrink: 0; white-space: nowrap;">
            {{ scopeLabel(row) }}
          </span>

          <!-- Install command -->
          <code style="font-family: var(--font-mono); font-size: 11px; color: var(--text-muted); flex-shrink: 0; white-space: nowrap;">
            {{ row.repoType === 'rpm' ? `dnf install ${row.pkg.name}` : `apt-get install ${row.pkg.name}` }}
          </code>

          <!-- Build status -->
          <span :style="`color: ${stateColor(row.target.state)};`"
            style="font-size: 12px; font-weight: 600; flex-shrink: 0; white-space: nowrap;">
            {{ row.target.state === 'succeeded' ? 'Built' : row.target.state }}
          </span>

          <!-- Download button -->
          <a :href="downloadUrl(row)" target="_blank" rel="noopener"
            :style="row.target.state !== 'succeeded'
              ? 'opacity: 0.4; pointer-events: none;'
              : ''"
            style="display: inline-flex; align-items: center; padding: 4px 10px; border-radius: 6px; background: var(--brand-purple); color: #fff; font-size: 11px; font-weight: 600; text-decoration: none; flex-shrink: 0; white-space: nowrap;">
            Download
          </a>
        </div>
      </div>

    </div>
  </div>
</template>
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/PackagesSubTab.vue
git commit -s -m "feat(frontend): add PackagesSubTab component"
```

---

## Task 8: Frontend — ContainersSubTab component

**Goal:** Create `ContainersSubTab.vue` — a responsive grid of container image cards, each with 4 sections: header (icon + name + OS + status badge), registry (bg-card-2), available tags (first tag purple-highlighted), and docker pull command with copy button.

**Files:**
- Create: `frontend/src/components/ContainersSubTab.vue`

**Acceptance Criteria:**
- [ ] Grid: `display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 16px`
- [ ] Section 2 (Registry) background: `var(--bg-card-2)`, padding `10px 18px`
- [ ] Section 3 (Tags) padding `12px 18px`; `tags[0]` chip: `var(--brand-purple-tint)` background, bold
- [ ] Section 4 (Docker pull) code block: `background: var(--bg-card-2)`, padding `12px 18px`
- [ ] Copy button shows "✓ Copied" when `copiedKey === image.id`
- [ ] Container icon is the specified box/shelf SVG
- [ ] TypeScript compilation passes

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0

**Steps:**

- [ ] **Step 1: Create ContainersSubTab.vue**

Create `frontend/src/components/ContainersSubTab.vue`:

```vue
<script setup lang="ts">
import type { ContainerImage } from '../composables/useArtifacts'

defineProps<{
  images: ContainerImage[]
  copiedKey: string | null
}>()
const emit = defineEmits<{ copy: [key: string, text: string] }>()
</script>

<template>
  <div style="display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 16px;">
    <div v-for="image in images" :key="image.id"
      style="background: var(--bg-card); border-radius: 12px; border: 1px solid var(--border); overflow: hidden; display: flex; flex-direction: column;">

      <!-- Section 1: Header -->
      <div style="padding: 16px; display: flex; align-items: flex-start; gap: 12px;">
        <div style="width: 36px; height: 36px; border-radius: 8px; background: var(--info-tint); display: flex; align-items: center; justify-content: center; flex-shrink: 0; color: var(--info);">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <rect x="2" y="7" width="20" height="14" rx="3"/>
            <path d="M7 7V5a2 2 0 012-2h6a2 2 0 012 2v2"/>
            <path d="M2 13h20"/>
          </svg>
        </div>
        <div style="flex: 1; min-width: 0;">
          <div style="font-size: 14px; font-weight: 700; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ image.name }}</div>
          <div style="font-size: 11.5px; color: var(--text-muted); margin-top: 2px;">{{ image.baseOs }}</div>
        </div>
        <span :style="image.published
            ? 'background: var(--ok-tint); color: var(--ok);'
            : 'background: var(--fail-tint); color: var(--fail);'"
          style="font-size: 11px; font-weight: 700; padding: 3px 8px; border-radius: 5px; flex-shrink: 0;">
          {{ image.published ? 'Published' : 'Build failing' }}
        </span>
      </div>

      <div style="border-top: 1px solid var(--border);"></div>

      <!-- Section 2: Registry -->
      <div style="padding: 10px 18px; background: var(--bg-card-2);">
        <div style="font-size: 10px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.07em; margin-bottom: 4px;">Registry</div>
        <code style="font-family: var(--font-mono); font-size: 12px; color: var(--text-secondary); word-break: break-all;">docker.io/percona/{{ image.name }}</code>
      </div>

      <div style="border-top: 1px solid var(--border);"></div>

      <!-- Section 3: Available tags -->
      <div style="padding: 12px 18px;">
        <div style="font-size: 10px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.07em; margin-bottom: 8px;">Available Tags</div>
        <div style="display: flex; flex-wrap: wrap; gap: 6px;">
          <span v-if="image.tags.length === 0" style="font-size: 11px; color: var(--text-muted);">No tags</span>
          <span
            v-for="(tag, i) in image.tags" :key="tag"
            :style="i === 0
              ? 'background: var(--brand-purple-tint); color: var(--brand-purple); font-weight: 700;'
              : 'background: var(--bg-muted); color: var(--text-secondary);'"
            style="font-family: var(--font-mono); font-size: 11px; padding: 2px 7px; border-radius: 4px;"
          >{{ tag }}</span>
        </div>
      </div>

      <div style="border-top: 1px solid var(--border);"></div>

      <!-- Section 4: Docker pull -->
      <div style="padding: 12px 18px;">
        <div style="font-size: 10px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.07em; margin-bottom: 8px;">Docker Pull</div>
        <div style="position: relative;">
          <code style="display: block; background: var(--bg-card-2); border-radius: 7px; padding: 10px 12px; font-family: var(--font-mono); font-size: 12px; color: var(--text-secondary); word-break: break-all; padding-right: 80px;">{{ image.pullCmd }}</code>
          <button
            @click="emit('copy', image.id, image.pullCmd)"
            :style="copiedKey === image.id ? 'color: var(--ok);' : 'color: var(--text-muted);'"
            style="position: absolute; top: 6px; right: 6px; padding: 4px 10px; border-radius: 6px; border: 1px solid var(--border); background: var(--bg-card); font-size: 11px; font-weight: 600; cursor: pointer; font-family: inherit;">
            {{ copiedKey === image.id ? '✓ Copied' : 'Copy' }}
          </button>
        </div>
      </div>

    </div>

    <div v-if="images.length === 0" style="grid-column: 1 / -1; color: var(--text-muted); font-size: 13px; padding: 24px 0; text-align: center;">
      No container images found.
    </div>
  </div>
</template>
```

- [ ] **Step 2: Verify TypeScript**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/ContainersSubTab.vue
git commit -s -m "feat(frontend): add ContainersSubTab component"
```

---

## Task 9: Frontend — ArtifactsPanel + App.vue wiring

**Goal:** Create `ArtifactsPanel.vue` (the top-level Artifacts panel) and wire everything together in `App.vue` — the `mainTab` state, the tab switcher, and passing `rawPackages` to the panel.

**Files:**
- Create: `frontend/src/components/ArtifactsPanel.vue`
- Modify: `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `ArtifactsPanel` manages local state: `artifactsTab`, `artRepo` (default `'el9'`), `artArch` (default `'x86_64'`), `copiedKey`
- [ ] `copy(key, text)` helper writes to clipboard, sets `copiedKey`, reverts after 2 s
- [ ] `obsRoot` computed: `` `isv:percona:ppg:${version}` ``
- [ ] Sub-tab switcher pills use the same borderless pill style as the main tab switcher
- [ ] `App.vue` has `mainTab` ref defaulting to `'board'`
- [ ] `AppHeader` receives `:main-tab="mainTab"` and `@update:main-tab="mainTab = $event"`
- [ ] Board content (ContextBar, HealthHeader, MainGrid) is shown when `mainTab === 'board'`
- [ ] `ArtifactsPanel` is shown when `mainTab === 'artifacts'`; receives `rawPackages` (not `allPackages`), `version`, `availableVersions`
- [ ] TypeScript compilation passes; app loads and tab switching works

**Verify:** `cd /home/rdias/Work/percona-obs-dashboard/frontend && npx vue-tsc --noEmit` → exits 0, then `npm run dev` and verify in browser: tab switcher toggles panels, version pills work, distro sidebar works, copy button shows "✓ Copied"

**Steps:**

- [ ] **Step 1: Create ArtifactsPanel.vue**

Create `frontend/src/components/ArtifactsPanel.vue`:

```vue
<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Package } from '../types/api'
import { useArtifacts, REPOS } from '../composables/useArtifacts'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'

const props = defineProps<{
  packages: Package[]
  version: string
  availableVersions: string[]
}>()
const emit = defineEmits<{ 'update:version': [v: string] }>()

const artifactsTab = ref<'packages' | 'containers'>('packages')
const artRepo = ref('el9')
const artArch = ref('x86_64')
const copiedKey = ref<string | null>(null)

const packagesRef = computed(() => props.packages)
const versionRef = computed(() => props.version)

const { packageRows, containerImages } = useArtifacts(packagesRef, versionRef, artRepo, artArch)

const obsRoot = computed(() => `isv:percona:ppg:${props.version}`)

function handleCopy(key: string, text: string) {
  navigator.clipboard.writeText(text)
  copiedKey.value = key
  setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = null
  }, 2000)
}
</script>

<template>
  <div style="display: flex; flex-direction: column; gap: 16px;">
    <ArtifactsVersionBar
      :version="version"
      :available-versions="availableVersions"
      :obs-root="obsRoot"
      @update:version="emit('update:version', $event)"
    />

    <!-- Sub-tab switcher -->
    <div style="display: flex; gap: 3px; background: var(--bg-muted); padding: 3px; border-radius: 11px; width: fit-content;">
      <button
        @click="artifactsTab = 'packages'"
        :style="artifactsTab === 'packages'
          ? 'background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.12);'
          : 'background: transparent; color: var(--text-muted);'"
        style="padding: 6px 16px; border-radius: 8px; font-size: 13px; font-weight: 700; border: none; cursor: pointer; font-family: inherit;"
      >Packages</button>
      <button
        @click="artifactsTab = 'containers'"
        :style="artifactsTab === 'containers'
          ? 'background: var(--bg-card); color: var(--brand-purple); box-shadow: 0 1px 2px rgba(0,0,0,0.12);'
          : 'background: transparent; color: var(--text-muted);'"
        style="padding: 6px 16px; border-radius: 8px; font-size: 13px; font-weight: 700; border: none; cursor: pointer; font-family: inherit;"
      >Container Images</button>
    </div>

    <PackagesSubTab
      v-if="artifactsTab === 'packages'"
      :package-rows="packageRows"
      :repos="REPOS"
      :art-repo="artRepo"
      :art-arch="artArch"
      :version="version"
      :copied-key="copiedKey"
      @update:art-repo="artRepo = $event"
      @update:art-arch="artArch = $event"
      @copy="handleCopy"
    />

    <ContainersSubTab
      v-else
      :images="containerImages"
      :copied-key="copiedKey"
      @copy="handleCopy"
    />
  </div>
</template>
```

- [ ] **Step 2: Update App.vue**

Add `mainTab` state and wire the two panels. The key changes to `App.vue`:

1. Add `mainTab` import and ref.
2. Change `AppHeader` to pass `:main-tab="mainTab"` and `@update:main-tab="mainTab = $event"`.
3. Wrap the board content in `v-if`.
4. Add `ArtifactsPanel` with `v-else`.
5. Import `ArtifactsPanel`.

Replace the `<script setup>` section with:

```ts
<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import ContextBar from './components/ContextBar.vue'
import HealthHeader from './components/HealthHeader.vue'
import MainGrid from './components/MainGrid.vue'
import ArtifactsPanel from './components/ArtifactsPanel.vue'
import type { Context } from './types/api'
import { usePackages } from './composables/usePackages'
import { useEvents } from './composables/useEvents'
import { usePRPackages } from './composables/usePRPackages'
import { useRealtimeStream } from './composables/useRealtimeStream'

// Theme
const theme = ref<'light' | 'dark'>('light')
watch(theme, (val) => {
  document.documentElement.setAttribute('data-theme', val === 'dark' ? 'dark' : '')
}, { immediate: true })

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
}

// Main tab
const mainTab = ref<'board' | 'artifacts'>('board')

// Context
const DEFAULT_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
}
const selectedContext = ref<Context>(DEFAULT_CONTEXT)
const prefixDepth = computed(() => selectedContext.value.prefix.split(':').length)

// Navigation state
const version = ref('')
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
  refresh()
}

// Event window state
const windowMin = ref(1440)
const customFrom = ref<string | null>(null)
const customTo = ref<string | null>(null)

// Data fetching
const apiBase = computed(() => selectedContext.value.apiBase)
const { data: allPackages, rawData: rawPackages, availableVersions, refresh: refreshPackages, filterByScope } = usePackages(apiBase, version, prefixDepth)
const { data: events, refresh: refreshEvents, filterEvents } = useEvents(apiBase, version)
const { data: prGroups, refresh: refreshPR } = usePRPackages()

watch(availableVersions, (vers) => {
  if (vers.length > 0 && version.value !== '' && !vers.includes(version.value)) {
    version.value = vers[0]
  }
})

const contexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 2 >= parts.length) continue
      const prSegment = parts[prIdx + 1]
      const subproject = parts[prIdx + 2]
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
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [DEFAULT_CONTEXT, ...prContexts]
})

const filteredPackages = computed(() => filterByScope(activeScopes.value))
const filteredEvents = computed(() => filterEvents(activeScopes.value, version.value, prefixDepth.value, selectedContext.value.prefix))
const updatedAt = ref<string | null>(null)

async function refresh() {
  const isCustom = windowMin.value === -1
  const hasCustomRange = customFrom.value != null && customTo.value != null
  const eventsOpts = isCustom
    ? (hasCustomRange ? { from: customFrom.value!, to: customTo.value! } : null)
    : { window: windowMin.value }
  await Promise.all([
    refreshPackages(),
    eventsOpts ? refreshEvents(eventsOpts) : Promise.resolve(),
    refreshPR(),
  ])
  updatedAt.value = new Date().toISOString()
}

onMounted(() => {
  refresh()
})
useRealtimeStream(rawPackages, events, prGroups, refresh, refreshPR)

watch(version, () => refresh())
watch([windowMin, customFrom, customTo], () => refresh())
</script>
```

Replace the `<template>` section with:

```html
<template>
  <div class="min-h-screen bg-bg-app" style="padding: 24px 28px 60px;">
    <div style="max-width: 1360px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px;">
      <AppHeader
        :theme="theme"
        :main-tab="mainTab"
        @toggle-theme="toggleTheme"
        @update:main-tab="mainTab = $event"
      />
      <template v-if="mainTab === 'board'">
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
          :events="filteredEvents"
          :window-min="windowMin"
          :custom-from="customFrom"
          :custom-to="customTo"
          @update:window-min="windowMin = $event"
          @update:custom-from="customFrom = $event"
          @update:custom-to="customTo = $event"
        />
      </template>
      <ArtifactsPanel
        v-else
        :packages="rawPackages"
        :version="version"
        :available-versions="availableVersions"
        @update:version="version = $event"
      />
    </div>
  </div>
</template>
```

- [ ] **Step 3: Fix TypeScript — useArtifacts ref wrapping**

`ArtifactsPanel.vue` uses `computed(() => props.packages)` to wrap the packages array into a ref-like for `useArtifacts`. This works because `useArtifacts` accepts `Ref<Package[]>`. However, `computed()` returns `ComputedRef`, which satisfies `Ref`. Verify no type errors:

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

If there are type errors about `Ref` vs `ComputedRef`, change the `useArtifacts` signature to accept `MaybeRef<Package[]>`:

In `useArtifacts.ts`, update the import and function signature:

```ts
import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  artRepo: MaybeRef<string>,
  artArch: MaybeRef<string>,
) {
  const packageRows = computed(() => {
    const repo = REPOS.find(r => r.short === toValue(artRepo))
    if (!repo) return []
    return toValue(packages)
      .filter(pkg => ['common', 'ppgcommon', 'version'].includes(pkg.scope))
      .filter(pkg =>
        pkg.scope !== 'version' ||
        !toValue(version) ||
        pkg.project.includes(`:ppg:${toValue(version)}`)
      )
      .flatMap(pkg => {
        const target = pkg.targets.find(
          t => t.repo === repo.obs && t.arch === toValue(artArch)
        )
        if (!target) return []
        return [{ pkg, target, repoType: repo.type as 'rpm' | 'deb' }]
      })
  })

  const containerImages = computed(() =>
    toValue(packages)
      .filter(pkg => pkg.scope === 'container')
      .map(pkg => ({
        id:        `${pkg.project}/${pkg.name}`,
        name:      pkg.name,
        project:   pkg.project,
        baseOs:    deriveBaseOs(pkg.project),
        tags:      pkg.container_tags ?? [],
        pullCmd:   `docker pull percona/${pkg.name}:${(pkg.container_tags ?? [])[0] ?? ''}`,
        published: pkg.targets.some(t => t.published === true),
      }))
  )

  return { packageRows, containerImages, REPOS }
}
```

And in `ArtifactsPanel.vue`, pass the refs directly (no extra computed wrapping needed):

```ts
const { packageRows, containerImages } = useArtifacts(
  computed(() => props.packages),
  computed(() => props.version),
  artRepo,
  artArch,
)
```

- [ ] **Step 4: Run TypeScript check and verify in browser**

```bash
cd /home/rdias/Work/percona-obs-dashboard/frontend
npx vue-tsc --noEmit
```

Expected: exits 0.

```bash
npm run dev
```

Open http://localhost:5173 in a browser and verify:
- The header shows "Build Board" and "Artifacts" pill buttons
- Clicking "Artifacts" shows the version bar, sub-tab switcher, and distro sidebar
- Clicking a version pill updates the OBS root chip
- Clicking a distro row updates the right column header and snippet
- Switching arch updates the package list
- Copy button shows "✓ Copied" for 2 s then reverts
- Switching to "Container Images" sub-tab shows the image cards
- Switching back to "Build Board" shows the original board
- Dark mode still works in both tabs

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ArtifactsPanel.vue frontend/src/App.vue \
        frontend/src/composables/useArtifacts.ts
git commit -s -m "feat(frontend): add ArtifactsPanel and wire main tab switcher in App.vue"
```
