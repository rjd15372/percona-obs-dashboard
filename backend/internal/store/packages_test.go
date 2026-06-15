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

	if err := UpsertPackageState(db, p); err != nil {
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
	UpsertPackageState(db, p)

	p.RollupState = model.RollupSucceeded
	UpsertPackageState(db, p)

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
	if err := UpsertPackageState(db, succeeded); err != nil {
		t.Fatal(err)
	}

	// Insert a failing package
	failing := &model.Package{
		Project: "isv:percona", Name: "pkg-fail", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets: []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, failing); err != nil {
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
