package obs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
)

func TestBuildStateTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="succeeded">
            <status package="mypkg" code="succeeded"/>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		Scope:       model.ScopeCommon,
		RollupState: model.RollupFailed,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.BuildStateTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded rollup, got %s", pkg.RollupState)
	}
	if pkg.OKTargets != 1 {
		t.Errorf("expected 1 OK target, got %d", pkg.OKTargets)
	}
}

func TestBlockedReasonTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<builddepinfo>
          <package name="mypkg">
            <error>not installable</error>
          </package>
        </builddepinfo>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		Scope:       model.ScopeCommon,
		RollupState: model.RollupBlocked,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "blocked"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.BlockedReasonTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BlockedBy == "" {
		t.Error("expected BlockedBy to be set")
	}
}

func TestBuildReasonTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<reason>
          <explain>meta change</explain>
          <packagechange>
            <change revision="abc">libfoo</change>
          </packagechange>
        </reason>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		Scope:       model.ScopeCommon,
		RollupState: model.RollupBuilding,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building"},
			{Repo: "repo", Arch: "aarch64", State: "succeeded"}, // should be skipped
		},
		UpdatedAt: time.Now().UTC(),
	}

	task := obs.BuildReasonTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BuildReason != "meta change" {
		t.Errorf("expected 'meta change', got %q", pkg.Targets[0].BuildReason)
	}
	if len(pkg.Targets[0].BuildReasonPackages) != 1 || pkg.Targets[0].BuildReasonPackages[0] != "libfoo" {
		t.Errorf("unexpected BuildReasonPackages: %v", pkg.Targets[0].BuildReasonPackages)
	}
	if pkg.Targets[1].BuildReason != "" {
		t.Error("succeeded target should have no BuildReason")
	}
}
