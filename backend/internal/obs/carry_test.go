package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestCarryForward(t *testing.T) {
	fresh := []PackageBuildState{{Repo: "RHEL_9", Arch: "x86_64", State: "building", Instance: "x"}}
	prev := []model.Target{
		{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "x"},
		{Repo: "Debian_12", Arch: "aarch64", State: "failed", Details: "oops", Instance: "y"},
	}
	if got := CarryForward(fresh, prev, nil, "p", "pkg"); len(got) != 1 {
		t.Fatalf("no failures must be a no-op, got %+v", got)
	}
	got := CarryForward(fresh, prev, []string{"y"}, "p", "pkg")
	if len(got) != 2 {
		t.Fatalf("want fresh + carried, got %+v", got)
	}
	c := got[1]
	if c.Repo != "Debian_12" || c.State != "failed" || c.Details != "oops" || c.Instance != "y" || c.Package != "pkg" {
		t.Errorf("carried state wrong: %+v", c)
	}
}

func TestWithoutInstance(t *testing.T) {
	pkg := &model.Package{
		Project: "ppg:17", Name: "pg", RollupState: model.RollupFailed, TotalTargets: 3,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "failed", Instance: "x"},
			{Repo: "Debian_12", Arch: "aarch64", State: "succeeded", Instance: "y", Published: true},
			{Repo: "Old", Arch: "x86_64", State: "failed"},
		},
	}
	out := WithoutInstance(pkg, "x")
	if len(out.Targets) != 1 || out.Targets[0].Repo != "Debian_12" || !out.Targets[0].Published {
		t.Fatalf("targets = %+v", out.Targets)
	}
	if out.RollupState != model.RollupSucceeded || out.OKTargets != 1 || out.TotalTargets != 1 {
		t.Errorf("rollup/counts = %s %d/%d", out.RollupState, out.OKTargets, out.TotalTargets)
	}
	if len(pkg.Targets) != 3 {
		t.Error("input package must not be mutated")
	}
}

func TestBuildPackageStampsInstance(t *testing.T) {
	p := buildPackage("ppg:17", "pg", nil, []PackageBuildState{{Repo: "R", Arch: "a", State: "succeeded", Instance: "y"}})
	if p.Targets[0].Instance != "y" {
		t.Errorf("instance not stamped: %+v", p.Targets[0])
	}
}
