package store

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestQueryProjectPackagesExact(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	for _, p := range []*model.Package{
		{Project: "ppg:17", Name: "a", RollupState: model.RollupSucceeded, UpdatedAt: now,
			Targets: []model.Target{{Repo: "R", Arch: "x", State: "succeeded", Instance: "y"}}},
		{Project: "ppg:17:containers", Name: "b", RollupState: model.RollupSucceeded, UpdatedAt: now},
	} {
		if err := UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := QueryProjectPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Targets[0].Instance != "y" {
		t.Errorf("target instance lost in targets_json: %+v", got[0].Targets[0])
	}
}

func TestQueryTargetCountsByInstance(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	pkgs := []*model.Package{
		{Project: "ppg:17", Name: "a", RollupState: model.RollupFailed, UpdatedAt: now, Targets: []model.Target{
			{Repo: "R", Arch: "x", State: "succeeded", Instance: "opensuse"},
			{Repo: "D", Arch: "y", State: "failed", Instance: "percona"},
			{Repo: "D2", Arch: "y", State: "building", Instance: "percona"},
			{Repo: "D3", Arch: "y", State: "blocked", Instance: "percona"},
		}},
		{Project: "ppg:releases:17", Name: "r", IsRelease: true, RollupState: model.RollupFailed, UpdatedAt: now,
			Targets: []model.Target{{Repo: "Z", Arch: "x", State: "failed", Instance: "percona"}}},
	}
	for _, p := range pkgs {
		if err := UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := QueryTargetCountsByInstance(db)
	if err != nil {
		t.Fatal(err)
	}
	want := []InstanceTargetCounts{
		{Instance: "opensuse", OK: 1},
		{Instance: "percona", Failing: 1, Building: 1, Blocked: 1},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v (release packages excluded)", got, want)
	}
}
