package obs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestBuildReasonTaskRetriesOnTransientError(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// Simulate a transient server error on the first two attempts.
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `<reason><explain>source change</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building"},
		},
		UpdatedAt: time.Now().UTC(),
	}

	task := obs.BuildReasonTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BuildReason != "source change" {
		t.Errorf("expected 'source change', got %q", pkg.Targets[0].BuildReason)
	}
	if attempts != 3 {
		t.Errorf("expected 3 server attempts (2 retries), got %d", attempts)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestPackageTypeTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<sourceinfo><filename>Dockerfile</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		RollupState: model.RollupSucceeded,
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.IsContainer == nil || !*pkg.IsContainer {
		t.Error("expected IsContainer=true for Dockerfile package")
	}
}

func TestPackageTypeTaskRPM(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<sourceinfo><filename>percona-pg_tde.spec</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		RollupState: model.RollupSucceeded,
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.IsContainer != nil && *pkg.IsContainer {
		t.Error("expected IsContainer=false for spec-only package")
	}
}

func TestPackageTypeTaskSkipsWhenAlreadySet(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, `<sourceinfo><filename>Dockerfile</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		IsContainer: boolPtr(false),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("PackageTypeTask should not call OBS when IsContainer is already set")
	}
	if pkg.IsContainer == nil || *pkg.IsContainer {
		t.Error("IsContainer should remain false when task is skipped")
	}
}

func TestVersionTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
		</resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(false),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.VersionTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "17.5-1" {
		t.Errorf("expected %q, got %q", "17.5-1", pkg.Version)
	}
}

func TestVersionTaskSkipsContainers(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mycontainer",
		IsContainer: boolPtr(true),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.VersionTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("VersionTask should not call OBS for container packages")
	}
}

func TestContainerTagsTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-1-1.7","percona-distribution-postgresql:18.4-1"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		Targets:     []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.ContainerTagsTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "18.4-1-1.7" {
		t.Errorf("expected Version %q, got %q", "18.4-1-1.7", pkg.Version)
	}
	if len(pkg.ContainerTags) != 2 {
		t.Fatalf("expected 2 ContainerTags, got %d: %v", len(pkg.ContainerTags), pkg.ContainerTags)
	}
	if pkg.ContainerTags[1] != "18.4-1" {
		t.Errorf("expected ContainerTags[1] %q, got %q", "18.4-1", pkg.ContainerTags[1])
	}
}

func TestContainerTagsTaskSkipsNonContainers(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	c := obs.NewClient(ts.URL, "u", "p")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		IsContainer: boolPtr(false),
		Targets:     []model.Target{{Repo: "UBI_9", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.ContainerTagsTask{}
	if err := task.Run(context.Background(), c, pkg); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("ContainerTagsTask should not call OBS for non-container packages")
	}
}
