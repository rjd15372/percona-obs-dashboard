package obs

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<collection></collection>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	_, err := c.SearchProjects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != expected {
		t.Errorf("auth header: got %q, want %q", gotAuth, expected)
	}
}

func TestSearchProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<collection>
			<project name="isv:percona:ppg"/>
			<project name="isv:percona:pmm"/>
		</collection>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	projects, err := c.SearchProjects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(projects), projects)
	}
	if projects[0] != "isv:percona:ppg" || projects[1] != "isv:percona:pmm" {
		t.Errorf("unexpected projects: %v", projects)
	}
}

func TestNon200Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	_, err := c.SearchProjects(context.Background(), "isv:percona")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestPackageBlockedReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" {
			http.Error(w, "missing package param", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
				<error>libfoo is not yet built</error>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reason, err := c.PackageBlockedReason(context.Background(), "isv:percona:ppg:17", "standard", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "libfoo is not yet built" {
		t.Errorf("expected blocking reason, got %q", reason)
	}
}

func TestPackageBlockedReasonNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reason, err := c.PackageBlockedReason(context.Background(), "isv:percona:ppg:17", "standard", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestEnrichBlockedTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<builddepinfo>
			<package name="mypkg">
				<pkgdep>libfoo</pkgdep>
				<error>libfoo is not yet built</error>
			</package>
		</builddepinfo>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17",
		Name:    "mypkg",
		Targets: []model.Target{
			{Repo: "standard", Arch: "x86_64", State: "blocked"},
			{Repo: "standard", Arch: "aarch64", State: "succeeded"},
		},
	}
	EnrichBlockedTargets(context.Background(), c, pkg)

	if pkg.Targets[0].BlockedBy != "libfoo is not yet built" {
		t.Errorf("expected blocked reason, got %q", pkg.Targets[0].BlockedBy)
	}
	if pkg.Targets[1].BlockedBy != "" {
		t.Errorf("non-blocked target should not have BlockedBy, got %q", pkg.Targets[1].BlockedBy)
	}
}

func TestEnrichBlockedTargetsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17",
		Name:    "mypkg",
		Targets: []model.Target{
			{Repo: "standard", Arch: "x86_64", State: "blocked"},
		},
	}
	// Should not panic; BlockedBy stays empty on error
	EnrichBlockedTargets(context.Background(), c, pkg)
	if pkg.Targets[0].BlockedBy != "" {
		t.Errorf("expected empty BlockedBy on error, got %q", pkg.Targets[0].BlockedBy)
	}
}

func TestPackageBuildResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/build/isv:percona/_result" && r.URL.Query().Get("package") == "mypkg" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<resultlist>
			  <result project="isv:percona" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="building"/>
			  </result>
			  <result project="isv:percona" repository="openSUSE_Tumbleweed" arch="aarch64" state="failed">
				<status package="mypkg" code="failed"/>
			  </result>
			</resultlist>`))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	results, err := c.PackageBuildResults(context.Background(), "isv:percona", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	found := map[string]string{}
	for _, r := range results {
		found[r.Arch] = r.State
	}
	if found["x86_64"] != "building" {
		t.Errorf("x86_64 expected building, got %s", found["x86_64"])
	}
	if found["aarch64"] != "failed" {
		t.Errorf("aarch64 expected failed, got %s", found["aarch64"])
	}
}

func TestPackageBuildReason(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/build/isv:percona/openSUSE_Tumbleweed/x86_64/mypkg/_reason" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<reason>
			  <explain>meta change</explain>
			  <time>1234567890</time>
			  <packagechange>
				<change revision="abc">libfoo</change>
				<change revision="def">libbar</change>
			  </packagechange>
			</reason>`))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	res, err := c.PackageBuildReason(context.Background(), "isv:percona", "openSUSE_Tumbleweed", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if res.Explain != "meta change" {
		t.Errorf("expected 'meta change', got %q", res.Explain)
	}
	if len(res.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(res.Packages))
	}
	if res.Packages[0] != "libfoo" || res.Packages[1] != "libbar" {
		t.Errorf("unexpected packages: %v", res.Packages)
	}
}

func TestPackageBuildReasonNonMeta(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<reason><explain>source change</explain></reason>`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	res, err := c.PackageBuildReason(context.Background(), "isv:percona", "repo", "arch", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if res.Explain != "source change" {
		t.Errorf("expected 'source change', got %q", res.Explain)
	}
	if len(res.Packages) != 0 {
		t.Errorf("expected no packages for non-meta reason, got %v", res.Packages)
	}
}
