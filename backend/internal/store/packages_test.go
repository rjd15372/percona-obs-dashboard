package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	_ "modernc.org/sqlite"
)

func TestUpsertQueryPackage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:      "isv:percona:ppg:17",
		Name:         "pg_tde",
		Scope:        model.ScopeVersion,
		RollupState:  model.RollupFailed,
		OKTargets:    4,
		TotalTargets: 6,
		Trigger: &model.Trigger{
			What: "openssl 3.2.1 → 3.2.2",
			Kind: "dependency bump",
			At:   now,
		},
		Targets: []model.Target{
			{Repo: "EL_9", Arch: "x86_64", State: "succeeded"},
			{Repo: "Debian_12", Arch: "x86_64", State: "failed"},
		},
		UpdatedAt: now,
	}

	if err := UpsertPackageState(db, p, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1, got %d", len(pkgs))
	}
	got := pkgs[0]
	if got.Name != "pg_tde" {
		t.Errorf("name: got %q", got.Name)
	}
	if got.RollupState != model.RollupFailed {
		t.Errorf("rollup_state: got %q", got.RollupState)
	}
	if got.Trigger == nil || got.Trigger.Kind != "dependency bump" {
		t.Errorf("trigger: got %+v", got.Trigger)
	}
	if len(got.Targets) != 2 {
		t.Errorf("targets: got %d", len(got.Targets))
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg_tde",
		Scope: model.ScopeVersion, RollupState: model.RollupFailed,
		Targets: []model.Target{}, UpdatedAt: now,
	}
	UpsertPackageState(db, p, p.UpdatedAt)

	p.RollupState = model.RollupSucceeded
	UpsertPackageState(db, p, p.UpdatedAt)

	pkgs, _ := QueryPackages(db, "isv:percona")
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 after update, got %d", len(pkgs))
	}
	if pkgs[0].RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded after update, got %q", pkgs[0].RollupState)
	}
}

func TestGetActivePackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Insert a succeeded package
	succeeded := &model.Package{
		Project: "isv:percona", Name: "pkg-ok", Scope: model.ScopeCommon,
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, succeeded, succeeded.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	// Insert a failing package
	failing := &model.Package{
		Project: "isv:percona", Name: "pkg-fail", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, failing, failing.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	pkgs, err := GetActivePackages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 active package, got %d", len(pkgs))
	}
	if pkgs[0].Name != "pkg-fail" {
		t.Errorf("expected pkg-fail, got %s", pkgs[0].Name)
	}
}

func TestGetFinishedPackagesByProject(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// GetFinishedPackagesByProject returns succeeded packages (for publish re-check).
	pkgSucceeded1 := &model.Package{
		Project: "isv:percona:ppg:17", Name: "postgres17", Scope: model.ScopeVersion,
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	pkgSucceeded2 := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pgaudit17", Scope: model.ScopeVersion,
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "aarch64", State: "succeeded"}},
		UpdatedAt: now,
	}
	pkgBuilding := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg_stat_monitor", Scope: model.ScopeVersion,
		RollupState: model.RollupBuilding, OKTargets: 0, TotalTargets: 1,
		Targets: []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "building"}},
		UpdatedAt: now,
	}
	pkgOtherProject := &model.Package{
		Project: "isv:percona:ppg:16", Name: "postgres16", Scope: model.ScopeVersion,
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets: []model.Target{{Repo: "Percona-PPG-16", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	for _, pkg := range []*model.Package{pkgSucceeded1, pkgSucceeded2, pkgBuilding, pkgOtherProject} {
		if err := UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := GetFinishedPackagesByProject(db, "isv:percona:ppg:17")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 succeeded packages, got %d", len(got))
	}
	names := map[string]bool{}
	for _, p := range got {
		names[p.Name] = true
		if p.RollupState != model.RollupSucceeded {
			t.Errorf("package %s: want RollupSucceeded, got %s", p.Name, p.RollupState)
		}
		if p.Project != "isv:percona:ppg:17" {
			t.Errorf("package %s: wrong project %s", p.Name, p.Project)
		}
	}
	if !names["postgres17"] || !names["pgaudit17"] {
		t.Errorf("wrong packages returned: %v", names)
	}

	// Empty result case
	got2, err := GetFinishedPackagesByProject(db, "isv:percona:ppg:99")
	if err != nil {
		t.Fatalf("unexpected error on empty: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("want 0 packages for unknown project, got %d", len(got2))
	}
}

func boolPtr(b bool) *bool { return &b }

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

	// Nil ContainerTags must round-trip as nil (not empty slice)
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

func TestVersionRoundtrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		Scope:       model.ScopeVersion,
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(false),
		Version:     "17.5-1",
		Targets:     []model.Target{{Repo: "UBI_9", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
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
	if pkgs[0].Version != "17.5-1" {
		t.Errorf("Version: got %q, want %q", pkgs[0].Version, "17.5-1")
	}
	if pkgs[0].IsContainer != nil && *pkgs[0].IsContainer {
		t.Error("IsContainer: expected false")
	}

	// Container package
	c := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		Scope:       model.ScopeContainer,
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		Version:     "18.4-1-1.7",
		Targets:     []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
	}
	if err := UpsertPackageState(db, c, now); err != nil {
		t.Fatal(err)
	}
	pkgs2, err := QueryPackages(db, "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Package
	for _, p := range pkgs2 {
		if p.Name == "percona-distribution-postgresql" {
			found = p
		}
	}
	if found == nil {
		t.Fatal("container package not found")
	}
	if found.Version != "18.4-1-1.7" {
		t.Errorf("Version: got %q, want %q", found.Version, "18.4-1-1.7")
	}
	if found.IsContainer == nil || !*found.IsContainer {
		t.Error("IsContainer: expected true")
	}
}

func TestIsContainerNullableMigration(t *testing.T) {
	// Simulate a database created before is_container became nullable.
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	// Create old schema with NOT NULL constraint.
	if _, err := db.Exec(`CREATE TABLE packages (
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
		is_container     INTEGER NOT NULL DEFAULT 0,
		version          TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project, name)
	)`); err != nil {
		t.Fatal(err)
	}
	// Insert rows: one non-container (0) and one container (1).
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		"p", "rpm", "version", "succeeded", 1, 1, "[]", now, 0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		"p", "img", "container", "succeeded", 1, 1, "[]", now, 1,
	); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Re-open via Open() which should apply the migration automatically.
	// We can't use the same in-memory DB after Close(), so use a temp file.
	tmp := t.TempDir() + "/test.db"
	dbOld, err := sql.Open("sqlite", tmp+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	dbOld.SetMaxOpenConns(1)
	if _, err := dbOld.Exec(`CREATE TABLE packages (
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
		is_container     INTEGER NOT NULL DEFAULT 0,
		version          TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project, name)
	); CREATE TABLE events (
		id TEXT PRIMARY KEY, type TEXT NOT NULL, scope TEXT NOT NULL,
		project TEXT NOT NULL, package TEXT NOT NULL, repo TEXT, arch TEXT,
		what TEXT NOT NULL, why TEXT NOT NULL, url TEXT NOT NULL,
		at DATETIME NOT NULL, version TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbOld.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?),
		        (?,?,?,?,?,?,?,?,?)`,
		"p", "rpm", "version", "succeeded", 1, 1, "[]", now, 0,
		"p", "img", "container", "succeeded", 1, 1, "[]", now, 1,
	); err != nil {
		t.Fatal(err)
	}
	dbOld.Close()

	migratedDB, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer migratedDB.Close()

	pkgs, err := QueryPackages(migratedDB, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}

	if byName["rpm"].IsContainer != nil {
		t.Errorf("rpm: is_container should be NULL after migration (was 0=default), got %v", byName["rpm"].IsContainer)
	}
	if byName["img"].IsContainer == nil || !*byName["img"].IsContainer {
		t.Errorf("img: is_container should be true after migration, got %v", byName["img"].IsContainer)
	}

	// Verify we can now insert a package with NULL is_container.
	newPkg := &model.Package{
		Project: "p", Name: "new", Scope: "version", RollupState: "building",
		Targets: []model.Target{}, UpdatedAt: now,
	}
	if err := UpsertPackageState(migratedDB, newPkg, now); err != nil {
		t.Errorf("upsert with nil IsContainer failed: %v", err)
	}
}

func TestStateChangedAt(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	t0 := time.Now().UTC().Truncate(time.Second)
	t1 := t0.Add(5 * time.Minute)
	t2 := t0.Add(10 * time.Minute)

	p := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg_tde",
		Scope: model.ScopeVersion, RollupState: model.RollupBuilding,
		Targets: []model.Target{}, UpdatedAt: t0,
	}

	// First insert: state_changed_at must be set to t0.
	if err := UpsertPackageState(db, p, t0); err != nil {
		t.Fatal(err)
	}
	pkgs, err := QueryPackages(db, "isv:percona:ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil {
		t.Fatal("state_changed_at should be set on first insert")
	}
	if !pkgs[0].StateChangedAt.Equal(t0) {
		t.Errorf("first insert: want state_changed_at=%v, got %v", t0, *pkgs[0].StateChangedAt)
	}

	// Same-state upsert: state_changed_at must not change.
	p.UpdatedAt = t1
	if err := UpsertPackageState(db, p, t1); err != nil {
		t.Fatal(err)
	}
	pkgs, err = QueryPackages(db, "isv:percona:ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil || !pkgs[0].StateChangedAt.Equal(t0) {
		t.Errorf("same-state upsert: want state_changed_at=%v, got %v", t0, pkgs[0].StateChangedAt)
	}

	// State-change upsert: state_changed_at must update to t2.
	p.RollupState = model.RollupSucceeded
	p.UpdatedAt = t2
	if err := UpsertPackageState(db, p, t2); err != nil {
		t.Fatal(err)
	}
	pkgs, err = QueryPackages(db, "isv:percona:ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil || !pkgs[0].StateChangedAt.Equal(t2) {
		t.Errorf("state-change upsert: want state_changed_at=%v, got %v", t2, pkgs[0].StateChangedAt)
	}
}
