package store

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
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
