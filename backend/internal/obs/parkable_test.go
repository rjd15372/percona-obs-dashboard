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
