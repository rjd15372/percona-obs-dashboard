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
