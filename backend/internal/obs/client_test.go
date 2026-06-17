package obs

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestPackageBlockedReasons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" || r.URL.Query().Get("view") != "status" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="blocked">
					<details>libfoo is not yet built</details>
				</status>
			</result>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="aarch64" state="building">
				<status package="mypkg" code="succeeded"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reasons, err := c.PackageBlockedReasons(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reasons["openSUSE_Tumbleweed/x86_64"] != "libfoo is not yet built" {
		t.Errorf("expected blocked reason for x86_64, got %q", reasons["openSUSE_Tumbleweed/x86_64"])
	}
	if _, ok := reasons["openSUSE_Tumbleweed/aarch64"]; ok {
		t.Error("succeeded target should not appear in reasons map")
	}
}

func TestPackageBlockedReasonsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="blocked"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reasons, err := c.PackageBlockedReasons(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	// blocked with no <details> → omitted from map
	if len(reasons) != 0 {
		t.Errorf("expected empty map, got %v", reasons)
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
			  <packagechange change="md5sum" key="libfoo"/>
			  <packagechange change="md5sum" key="libbar"/>
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

func TestRepoPublishStates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" || r.URL.Query().Get("view") != "status" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result repository="Ubuntu_24.04" arch="x86_64" state="published">
				<status package="mypkg" code="succeeded"/>
			</result>
			<result repository="Ubuntu_24.04" arch="aarch64" state="building">
				<status package="mypkg" code="building"/>
			</result>
		</resultlist>`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "p")
	states, err := c.RepoPublishStates(context.Background(), "isv:percona", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if states["Ubuntu_24.04/x86_64"] != "published" {
		t.Errorf("expected published for Ubuntu_24.04/x86_64, got %q", states["Ubuntu_24.04/x86_64"])
	}
	if states["Ubuntu_24.04/aarch64"] != "building" {
		t.Errorf("expected building for Ubuntu_24.04/aarch64, got %q", states["Ubuntu_24.04/aarch64"])
	}
}

func TestPackageIsContainer(t *testing.T) {
	tests := []struct {
		name     string
		xml      string
		expected bool
	}{
		{
			name:     "dockerfile",
			xml:      `<sourceinfo><filename>Dockerfile</filename><filename>config.sh</filename></sourceinfo>`,
			expected: true,
		},
		{
			name:     "kiwi",
			xml:      `<sourceinfo><filename>percona-distribution-postgresql.kiwi</filename></sourceinfo>`,
			expected: true,
		},
		{
			name:     "rpm spec",
			xml:      `<sourceinfo><filename>percona-pg_tde.spec</filename></sourceinfo>`,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.xml))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "u", "p")
			got, err := c.PackageIsContainer(context.Background(), "isv:percona:ppg:17", "mypkg")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestPackageIsContainerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	got, err := c.PackageIsContainer(context.Background(), "isv:percona:ppg:17", "percona-patroni")
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
	if got {
		t.Error("expected false for package with no images repository")
	}
}

func TestPackageVersionResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("view") != "versrel" {
			http.Error(w, "bad view", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
			<result repository="UBI_9" arch="aarch64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versrel, err := c.PackageVersionResult(context.Background(), "isv:percona:ppg:17", "percona-pg_tde")
	if err != nil {
		t.Fatal(err)
	}
	if versrel != "17.5-1" {
		t.Errorf("expected %q, got %q", "17.5-1", versrel)
	}
}

func TestPackageVersionResultEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<resultlist>
			<result repository="UBI_9" arch="x86_64" state="building">
				<status package="mypkg" code="building"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versrel, err := c.PackageVersionResult(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if versrel != "" {
		t.Errorf("expected empty string for unbuilt package, got %q", versrel)
	}
}

func TestPackageContainerInfoFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<binarylist>
			<binary filename="percona-distribution-postgresql-18.4-1.x86_64-1.7.tar" size="305101312" mtime="1781559533"/>
			<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1234" mtime="1781559533"/>
		</binarylist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	filename, err := c.PackageContainerInfoFilename(context.Background(),
		"isv:percona:ppg:17:containers:ubi8", "images", "x86_64", "percona-distribution-postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "percona-distribution-postgresql.x86_64-1.7.containerinfo" {
		t.Errorf("unexpected filename: %q", filename)
	}
}

func TestPackageContainerInfoFilenameAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<binarylist></binarylist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	filename, err := c.PackageContainerInfoFilename(context.Background(), "proj", "repo", "arch", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "" {
		t.Errorf("expected empty, got %q", filename)
	}
}

func TestPackageContainerTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"version": "18.4-1",
			"tags": [
				"percona-distribution-postgresql:18.4-1-1.7",
				"percona-distribution-postgresql:18.4-1",
				"percona-distribution-postgresql:18.4",
				"percona-distribution-postgresql:18"
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	tag, err := c.PackageContainerTags(context.Background(),
		"proj", "images", "x86_64", "pkg", "pkg.containerinfo")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "18.4-1-1.7" {
		t.Errorf("expected %q, got %q", "18.4-1-1.7", tag)
	}
}

func TestPackageContainerTagsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tags": []}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	tag, err := c.PackageContainerTags(context.Background(), "proj", "repo", "arch", "pkg", "file.containerinfo")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "" {
		t.Errorf("expected empty, got %q", tag)
	}
}
