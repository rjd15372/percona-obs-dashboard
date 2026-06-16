# Package State Duration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show how long a package has been in its current state in the package card, for `scheduled`, `building`, and `finishing` rollup states.

**Architecture:** A new `state_changed_at` column is added to the `packages` table via an `ALTER TABLE` migration on top of the existing `CREATE TABLE IF NOT EXISTS` schema. `UpsertPackageState` always passes `now` as the candidate timestamp; a `CASE` expression in the `ON CONFLICT DO UPDATE` clause applies it only when `rollup_state` actually changes. The frontend reads `state_changed_at` from the API and renders a right-aligned relative duration in the card header row.

**Tech Stack:** Go (`database/sql`, `modernc.org/sqlite`), Vue 3 Composition API, TypeScript.

**User decisions (already made):**
- Show duration only for `scheduled`, `building`, and `finishing` (rollup_state value `"finished"`)
- Placement: right-aligned in row 1 of the package card, before the OBS link

---

## File Structure

| File | Change |
|------|--------|
| `backend/internal/model/types.go` | Add `StateChangedAt *time.Time` to `Package` |
| `backend/internal/store/db.go` | Add `state_changed_at` to schema + `ALTER TABLE` migration |
| `backend/internal/store/packages.go` | Update upsert SQL, `scanPackages`, all three SELECT queries |
| `backend/internal/store/packages_test.go` | Add `TestStateChangedAt` test |
| `frontend/src/types/api.ts` | Add `state_changed_at?: string` to `Package` |
| `frontend/src/components/PackageCard.vue` | Add `stateAge` computed, render in row 1 |

---

### Task 1: Backend — persist `state_changed_at`

**Goal:** Add `state_changed_at` to the model, schema, and store so it is written on first insert and updated only when `rollup_state` changes.

**Files:**
- Modify: `backend/internal/model/types.go:66-76`
- Modify: `backend/internal/store/db.go:8-55`
- Modify: `backend/internal/store/packages.go:10-134`
- Modify: `backend/internal/store/packages_test.go` (add test)

**Acceptance Criteria:**
- [ ] `model.Package` has `StateChangedAt *time.Time \`json:"state_changed_at,omitempty"\``
- [ ] Fresh DB: `state_changed_at` set on first insert; all SELECT queries return it
- [ ] Same-state upsert: `state_changed_at` unchanged
- [ ] State-change upsert: `state_changed_at` updated to the new time
- [ ] `go test ./backend/internal/store/... -v` passes

**Verify:** `cd backend && go test ./internal/store/... -v -run TestStateChangedAt` → PASS

**Steps:**

- [ ] **Step 1: Add field to model**

In `backend/internal/model/types.go`, add `StateChangedAt` as the last field of `Package`:

```go
type Package struct {
	Project        string      `json:"project"`
	Name           string      `json:"name"`
	Scope          Scope       `json:"scope"`
	RollupState    RollupState `json:"rollup_state"`
	OKTargets      int         `json:"ok_targets"`
	TotalTargets   int         `json:"total_targets"`
	Trigger        *Trigger    `json:"trigger,omitempty"`
	Targets        []Target    `json:"targets"`
	UpdatedAt      time.Time   `json:"updated_at"`
	StateChangedAt *time.Time  `json:"state_changed_at,omitempty"`
}
```

- [ ] **Step 2: Write the failing test**

Add `TestStateChangedAt` to `backend/internal/store/packages_test.go`:

```go
func TestStateChangedAt(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg_tde",
		Scope: model.ScopeVersion, RollupState: model.RollupBuilding,
		Targets: []model.Target{}, UpdatedAt: now,
	}

	// First insert: state_changed_at must be set.
	if err := UpsertPackageState(db, p); err != nil {
		t.Fatal(err)
	}
	pkgs, _ := QueryPackages(db, "isv:percona:ppg:17")
	if pkgs[0].StateChangedAt == nil {
		t.Fatal("state_changed_at should be set on first insert")
	}
	first := *pkgs[0].StateChangedAt

	// Same-state upsert: state_changed_at must not change.
	later := now.Add(5 * time.Minute)
	p.UpdatedAt = later
	if err := UpsertPackageState(db, p); err != nil {
		t.Fatal(err)
	}
	pkgs, _ = QueryPackages(db, "isv:percona:ppg:17")
	if !pkgs[0].StateChangedAt.Equal(first) {
		t.Errorf("same-state upsert: state_changed_at changed from %v to %v", first, *pkgs[0].StateChangedAt)
	}

	// State-change upsert: state_changed_at must update.
	p.RollupState = model.RollupSucceeded
	p.UpdatedAt = later
	if err := UpsertPackageState(db, p); err != nil {
		t.Fatal(err)
	}
	pkgs, _ = QueryPackages(db, "isv:percona:ppg:17")
	if pkgs[0].StateChangedAt == nil || pkgs[0].StateChangedAt.Equal(first) {
		t.Errorf("state-change upsert: state_changed_at should have changed; got %v", pkgs[0].StateChangedAt)
	}
}
```

- [ ] **Step 3: Run test — expect failure**

```bash
cd backend && go test ./internal/store/... -v -run TestStateChangedAt
```

Expected: compilation error or FAIL (column doesn't exist yet).

- [ ] **Step 4: Add column to schema + migration**

In `backend/internal/store/db.go`, add `state_changed_at` to the `CREATE TABLE` block and add a migration call in `Open`:

```go
const schema = `
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
    targets_json   TEXT NOT NULL DEFAULT '[]',
    updated_at     DATETIME NOT NULL,
    state_changed_at DATETIME,
    PRIMARY KEY (project, name)
);

CREATE TABLE IF NOT EXISTS events (
    id       TEXT PRIMARY KEY,
    type     TEXT NOT NULL,
    scope    TEXT NOT NULL,
    project  TEXT NOT NULL,
    package  TEXT NOT NULL,
    repo     TEXT,
    arch     TEXT,
    what     TEXT NOT NULL,
    why      TEXT NOT NULL,
    url      TEXT NOT NULL,
    at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS events_at ON events(at);

CREATE INDEX IF NOT EXISTS idx_packages_rollup_state ON packages(rollup_state);
`

// Open opens (or creates) the SQLite database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Additive migration: add state_changed_at to existing databases.
	// Fails silently if the column already exists (fresh DBs have it from the schema above).
	db.Exec(`ALTER TABLE packages ADD COLUMN state_changed_at DATETIME`)
	return db, nil
}
```

- [ ] **Step 5: Update `UpsertPackageState`**

Replace the entire `UpsertPackageState` function in `backend/internal/store/packages.go`:

```go
// UpsertPackageState inserts or replaces a package row.
func UpsertPackageState(db *sql.DB, p *model.Package) error {
	targetsJSON, err := json.Marshal(p.Targets)
	if err != nil {
		return err
	}
	var trigWhat, trigKind sql.NullString
	var trigAt sql.NullTime
	if p.Trigger != nil {
		trigWhat = sql.NullString{String: p.Trigger.What, Valid: true}
		trigKind = sql.NullString{String: p.Trigger.Kind, Valid: true}
		trigAt = sql.NullTime{Time: p.Trigger.At, Valid: true}
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO packages
			(project, name, scope, rollup_state, ok_targets, total_targets,
			 trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project, name) DO UPDATE SET
			scope=excluded.scope, rollup_state=excluded.rollup_state,
			ok_targets=excluded.ok_targets, total_targets=excluded.total_targets,
			trigger_what=excluded.trigger_what, trigger_kind=excluded.trigger_kind,
			trigger_at=excluded.trigger_at, targets_json=excluded.targets_json,
			updated_at=excluded.updated_at,
			state_changed_at = CASE
				WHEN excluded.rollup_state != rollup_state THEN excluded.state_changed_at
				ELSE state_changed_at
			END`,
		p.Project, p.Name, string(p.Scope), string(p.RollupState),
		p.OKTargets, p.TotalTargets,
		trigWhat, trigKind, trigAt,
		string(targetsJSON), p.UpdatedAt, now,
	)
	return err
}
```

Note: `time` must be imported — add it to the import block if not already present:
```go
import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)
```

- [ ] **Step 6: Update `scanPackages` to read the new column**

Replace `scanPackages` in `backend/internal/store/packages.go`. Add `var stateChangedAt sql.NullTime` to the scan vars and scan it:

```go
// scanPackages is a helper that extracts the scan loop pattern used by multiple query functions.
// It expects rows to have been created with the standard package column order:
// project, name, scope, rollup_state, ok_targets, total_targets,
// trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at
func scanPackages(rows *sql.Rows) ([]*model.Package, error) {
	pkgs := make([]*model.Package, 0)
	for rows.Next() {
		p := &model.Package{}
		var trigWhat, trigKind sql.NullString
		var trigAt, stateChangedAt sql.NullTime
		var targetsJSON string
		if err := rows.Scan(
			&p.Project, &p.Name, &p.Scope, &p.RollupState,
			&p.OKTargets, &p.TotalTargets,
			&trigWhat, &trigKind, &trigAt,
			&targetsJSON, &p.UpdatedAt, &stateChangedAt,
		); err != nil {
			return nil, err
		}
		if trigWhat.Valid {
			p.Trigger = &model.Trigger{
				What: trigWhat.String,
				Kind: trigKind.String,
				At:   trigAt.Time,
			}
		}
		if stateChangedAt.Valid {
			p.StateChangedAt = &stateChangedAt.Time
		}
		if err := json.Unmarshal([]byte(targetsJSON), &p.Targets); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, rows.Err()
}
```

- [ ] **Step 7: Update all three SELECT queries to include `state_changed_at`**

`QueryPackages`, `GetActivePackages`, and `GetFinishedPackagesByProject` all use `scanPackages` and must select `state_changed_at` as the 12th column. Update each SELECT clause:

```go
// QueryPackages returns all packages for a given OBS project prefix.
func QueryPackages(db *sql.DB, projectPrefix string) ([]*model.Package, error) {
	rows, err := db.Query(`
		SELECT project, name, scope, rollup_state, ok_targets, total_targets,
		       trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at
		FROM packages WHERE project LIKE ? ORDER BY project, name`,
		projectPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// GetActivePackages returns all packages where rollup_state is not 'succeeded'.
func GetActivePackages(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`
		SELECT project, name, scope, rollup_state, ok_targets, total_targets,
		       trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at
		FROM packages WHERE rollup_state != 'succeeded' ORDER BY project, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// GetFinishedPackagesByProject returns succeeded packages for a project.
// Used by the MQ consumer on repo.published to signal packages for a publish
// state re-check via the worker pool.
func GetFinishedPackagesByProject(db *sql.DB, project string) ([]*model.Package, error) {
	rows, err := db.Query(`
		SELECT project, name, scope, rollup_state, ok_targets, total_targets,
		       trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at
		FROM packages WHERE project = ? AND rollup_state = 'succeeded'`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}
```

- [ ] **Step 8: Run tests — expect pass**

```bash
cd backend && go test ./internal/store/... -v
```

Expected: all tests PASS including `TestStateChangedAt`.

- [ ] **Step 9: Build entire backend to confirm no compile errors**

```bash
cd backend && go build ./...
```

Expected: exits 0, no output.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/model/types.go \
        backend/internal/store/db.go \
        backend/internal/store/packages.go \
        backend/internal/store/packages_test.go
git commit -s -m "feat(store): track state_changed_at per package"
```

---

### Task 2: Frontend — display state duration in PackageCard

**Goal:** Show "for 23m" right-aligned in row 1 of the package card, only for `scheduled`, `building`, and `finishing` rollup states.

**Files:**
- Modify: `frontend/src/types/api.ts:27-37`
- Modify: `frontend/src/components/PackageCard.vue`

**Acceptance Criteria:**
- [ ] `Package` interface has `state_changed_at?: string`
- [ ] `stateAge` returns a formatted string for `scheduled`, `building`, `finished` when `state_changed_at` is present
- [ ] `stateAge` returns `null` for all other states (succeeded, failed, blocked, etc.)
- [ ] Duration renders right-aligned in row 1, before the OBS link
- [ ] OBS link no longer has `margin-left: auto` (the duration span takes that role)
- [ ] `cd frontend && npx vue-tsc --noEmit` exits 0

**Verify:** `cd frontend && npx vue-tsc --noEmit` → exits 0 with no output

**Steps:**

- [ ] **Step 1: Add field to the TypeScript type**

In `frontend/src/types/api.ts`, add `state_changed_at` to the `Package` interface after `updated_at`:

```ts
export interface Package {
  project: string
  name: string
  scope: PackageScope
  rollup_state: BuildState
  ok_targets: number
  total_targets: number
  trigger?: Trigger // optional
  targets: Target[]
  updated_at: string // ISO 8601
  state_changed_at?: string // ISO 8601; absent when NULL
}
```

- [ ] **Step 2: Add `stateAge` computed to `PackageCard.vue`**

In `frontend/src/components/PackageCard.vue`, add after the `const obsUrl` computed (before the closing `</script>` tag):

```ts
const IN_PROGRESS_STATES = new Set(['scheduled', 'building', 'finished'])

const stateAge = computed((): string | null => {
  if (!IN_PROGRESS_STATES.has(props.pkg.rollup_state)) return null
  if (!props.pkg.state_changed_at) return null
  const ms = Date.now() - new Date(props.pkg.state_changed_at).getTime()
  const m = Math.floor(ms / 60000)
  if (m < 1) return 'for <1m'
  if (m < 60) return `for ${m}m`
  return `for ${Math.floor(m / 60)}h ${m % 60}m`
})
```

- [ ] **Step 3: Update row 1 in the template to render `stateAge`**

Find the existing row 1 `<div>` in the `<template>` (the one containing the state pill, package name, and OBS link) and replace it with the version below. The key changes are: `stateAge` span added with `margin-left: auto`; OBS link loses its `margin-left: auto`.

Current row 1:
```html
<!-- Row 1: state pill + name + OBS link -->
<div style="display: flex; align-items: center; gap: 9px;">
  <span :style="{
    fontSize: '10.5px', fontWeight: '700', textTransform: 'uppercase',
    letterSpacing: '0.04em', padding: '3px 9px', borderRadius: '6px',
    color: rollupColor, background: rollupBg,
  }">{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>
  <code style="font-family: var(--font-mono); font-size: 13.5px; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ pkg.name }}</code>
  <a :href="obsUrl" target="_blank" rel="noopener" style="margin-left: auto; font-size: 11.5px; font-weight: 700; color: var(--brand-purple); text-decoration: none; white-space: nowrap; flex-shrink: 0;">OBS ↗</a>
</div>
```

Replace with:
```html
<!-- Row 1: state pill + name + duration + OBS link -->
<div style="display: flex; align-items: center; gap: 9px;">
  <span :style="{
    fontSize: '10.5px', fontWeight: '700', textTransform: 'uppercase',
    letterSpacing: '0.04em', padding: '3px 9px', borderRadius: '6px',
    color: rollupColor, background: rollupBg,
  }">{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>
  <code style="font-family: var(--font-mono); font-size: 13.5px; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ pkg.name }}</code>
  <span v-if="stateAge" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ stateAge }}</span>
  <a :href="obsUrl" target="_blank" rel="noopener" :style="{ marginLeft: stateAge ? '0' : 'auto', fontSize: '11.5px', fontWeight: '700', color: 'var(--brand-purple)', textDecoration: 'none', whiteSpace: 'nowrap', flexShrink: '0' }">OBS ↗</a>
</div>
```

Note: the OBS link uses `:style` binding so `margin-left` is `auto` when there's no duration (old behaviour) and `0` when a duration is shown (duration takes the `margin-left: auto` role).

- [ ] **Step 4: Type-check**

```bash
cd frontend && npx vue-tsc --noEmit
```

Expected: exits 0 with no output.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types/api.ts frontend/src/components/PackageCard.vue
git commit -s -m "feat(ui): show state duration in package card"
```
