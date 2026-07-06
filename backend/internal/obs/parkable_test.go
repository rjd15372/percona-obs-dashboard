package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestParkable(t *testing.T) {
	flags := publishFlagsForTest() // UBI_8 does not publish; other repos do

	pkg := func(targets ...model.Target) *model.Package {
		return &model.Package{RollupState: model.RollupBuilding, Targets: targets}
	}
	building := func(reason string) model.Target {
		return model.Target{Repo: "images", Arch: "x86_64", State: "building", BuildReason: reason}
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"all building with reasons", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "building", BuildReason: "meta change"}), true},
		{"building without reason", pkg(building("")), false},
		{"building + succeeded published", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "succeeded", Published: true}), true},
		{"building + succeeded non-publishing repo", pkg(building("meta change"), model.Target{Repo: "UBI_8", Arch: "x86_64", State: "succeeded"}), true},
		{"building + succeeded unpublished publishing repo", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "succeeded"}), false},
		{"building + scheduled", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "scheduled", BuildReason: "meta change"}), false},
		{"building + blocked", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "blocked"}), false},
		{"building + finished", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "finished"}), false},
		{"all inert no building", pkg(model.Target{Repo: "UBI_8", Arch: "x86_64", State: "succeeded"}), false},
		{"skip-state ignored", pkg(building("meta change"), model.Target{Repo: "images", Arch: "aarch64", State: "disabled"}), true},
		{"no targets", pkg(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parkable(tc.pkg, flags); got != tc.want {
				t.Fatalf("Parkable = %v, want %v", got, tc.want)
			}
		})
	}
}
