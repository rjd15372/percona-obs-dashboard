package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestSettled(t *testing.T) {
	pkg := func(state model.RollupState, targets ...model.Target) *model.Package {
		return &model.Package{RollupState: state, Targets: targets}
	}
	tgt := func(repo, state string, published bool) model.Target {
		return model.Target{Repo: repo, State: state, Published: published}
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"published", pkg(model.RollupPublished), true},
		{"failed", pkg(model.RollupFailed), true},
		{"broken", pkg(model.RollupBroken), false},
		{"unresolvable", pkg(model.RollupUnresolvable), false},
		{"building", pkg(model.RollupBuilding), false},
		{"blocked", pkg(model.RollupBlocked), false},
		{"succeeded non-publishing repo", pkg(model.RollupSucceeded, tgt("UBI_8", "succeeded", false)), true},
		{"succeeded publishing repo unpublished", pkg(model.RollupSucceeded, tgt("images", "succeeded", false)), false},
		{"succeeded publishing repo published", pkg(model.RollupSucceeded, tgt("images", "succeeded", true)), true},
		{"succeeded mixed: published + non-publishing", pkg(model.RollupSucceeded, tgt("images", "succeeded", true), tgt("UBI_8", "succeeded", false)), true},
		{"succeeded skip-state ignored", pkg(model.RollupSucceeded, tgt("images", "disabled", false), tgt("UBI_8", "succeeded", false)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Settled(tc.pkg, publishFlagsForTest()); got != tc.want {
				t.Fatalf("Settled = %v, want %v", got, tc.want)
			}
		})
	}
}

func publishFlagsForTest() PublishFlags {
	f, err := parsePublishFlags([]byte(`<project name="p"><publish><disable repository="UBI_8"/></publish></project>`))
	if err != nil {
		panic(err)
	}
	return f
}
