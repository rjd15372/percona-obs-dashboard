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
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="building">
            <status package="mypkg" code="blocked">
              <details>not installable</details>
            </status>
          </result>
        </resultlist>`)
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
	if pkg.Targets[0].BlockedBy != "not installable" {
		t.Errorf("expected BlockedBy to be set, got %q", pkg.Targets[0].BlockedBy)
	}
}

func TestPublishStateTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result repository="Ubuntu_24.04" arch="x86_64" state="published">
            <status package="mypkg" code="succeeded"/>
          </result>
          <result repository="Ubuntu_24.04" arch="aarch64" state="building">
            <status package="mypkg" code="succeeded"/>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona",
		Name:    "mypkg",
		Scope:   model.ScopeCommon,
		Targets: []model.Target{
			{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded"},  // repo published → Published=true
			{Repo: "Ubuntu_24.04", Arch: "aarch64", State: "succeeded"}, // repo building → Published=false
			{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"},   // not succeeded → skip
		},
	}

	task := obs.PublishStateTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if !pkg.Targets[0].Published {
		t.Error("expected Ubuntu_24.04/x86_64 to be Published=true (repo state=published, target succeeded)")
	}
	if pkg.Targets[1].Published {
		t.Error("expected Ubuntu_24.04/aarch64 to be Published=false (repo state=building)")
	}
	if pkg.Targets[2].Published {
		t.Error("expected RockyLinux_9/x86_64 to be Published=false (target not succeeded)")
	}
}

func TestBuildReasonTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<reason>
          <explain>meta change</explain>
          <packagechange change="md5sum" key="libfoo"/>
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
