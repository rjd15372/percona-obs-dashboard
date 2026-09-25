package obs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testInstance(root string) *Instance {
	return NewInstance(InstanceInfo{
		Name: "Percona", Slug: "percona", Root: root,
		WebURL: "https://obs.example.com", DownloadURL: "https://dl.example.com/repositories",
		Registry: "registry.example.com",
	}, nil)
}

func TestInstanceTranslation(t *testing.T) {
	for _, root := range []string{"isv:percona", "percona"} {
		in := testInstance(root)
		got, ok := in.ToLogical(root + ":ppg:17")
		if !ok || got != "ppg:17" {
			t.Errorf("root %q: ToLogical = %q,%v", root, got, ok)
		}
		if back := in.ToInstance("ppg:17"); back != root+":ppg:17" {
			t.Errorf("root %q: ToInstance = %q", root, back)
		}
		if _, ok := in.ToLogical("home:someone:ppg:17"); ok {
			t.Errorf("root %q: out-of-root project accepted", root)
		}
		if _, ok := in.ToLogical(root); ok {
			t.Errorf("root %q: bare root accepted as a project", root)
		}
	}
}

func TestIdentityInstance(t *testing.T) {
	in := LegacyInstance(nil, "isv:percona")
	if got, ok := in.ToLogical("isv:percona:ppg:17"); !ok || got != "isv:percona:ppg:17" {
		t.Errorf("identity ToLogical = %q,%v", got, ok)
	}
	if _, ok := in.ToLogical("home:x:ppg"); ok {
		t.Error("identity instance accepted out-of-root project")
	}
	if got := in.ToInstance("isv:percona:ppg:17"); got != "isv:percona:ppg:17" {
		t.Errorf("identity ToInstance = %q", got)
	}
	if in.PathRoot() != "" {
		t.Errorf("identity PathRoot = %q", in.PathRoot())
	}
	if testInstance("percona").PathRoot() != "percona" {
		t.Error("PathRoot should be the root for non-identity instances")
	}
}

func TestInstanceURLs(t *testing.T) {
	in := testInstance("percona")
	cases := map[string]string{
		in.ProjectURL("ppg:17"):                                 "https://obs.example.com/project/show/percona:ppg:17",
		in.PackageURL("ppg:17", "pg"):                           "https://obs.example.com/package/show/percona:ppg:17/pg",
		in.LiveLogURL("ppg:17", "pg", "RHEL_9", "x86_64"):       "https://obs.example.com/package/live_build_log/percona:ppg:17/pg/RHEL_9/x86_64",
		in.DownloadURL("ppg:17", "RHEL_9"):                      "https://dl.example.com/repositories/percona:/ppg:/17/RHEL_9/",
		in.ImageBase("ppg:staging:17:containers", "ubi9", "pg"): "registry.example.com/percona/ppg/staging/17/containers/ubi9/pg",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	legacy := LegacyInstance(nil, "")
	if got := legacy.ImageBase("isv:percona:PR:pr-9:ppg", "images", "pg"); got != "registry.opensuse.org/isv/percona/pr/pr-9/ppg/images/pg" {
		t.Errorf("legacy ImageBase = %q", got)
	}
}

func TestStatusErrorNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	_, err := c.PackageBinaries(Interactive(context.Background()), "p", "r", "a", "pkg")
	if !IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
	if want := "OBS /build/p/r/a/pkg: 404 Not Found — gone"; err.Error() != want {
		t.Errorf("error text = %q, want %q", err.Error(), want)
	}
	if IsNotFound(errors.New("x")) {
		t.Error("plain error reported as NotFound")
	}
}

func TestInstanceHealth(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	in := testInstance("percona")
	in.health.now = func() time.Time { return now }
	in.SetMQConnected(true)

	if !in.Health().OK {
		t.Fatal("fresh instance should be OK")
	}
	in.observe(&StatusError{Code: http.StatusNotFound})
	in.observe(context.Canceled)
	in.observe(errors.New("boom"))
	in.observe(errors.New("boom"))
	if !in.Health().OK {
		t.Fatal("2 failures (404/cancel ignored) should still be OK")
	}
	in.observe(errors.New("boom"))
	h := in.Health()
	if h.OK || h.ConsecutiveFailures != 3 || h.LastError != "boom" {
		t.Fatalf("after 3 failures: %+v", h)
	}
	in.observe(nil)
	if h := in.Health(); !h.OK || h.ConsecutiveFailures != 0 || h.LastSuccess == nil {
		t.Fatalf("success should reset: %+v", h)
	}

	in.SetMQConnected(false)
	now = now.Add(time.Minute)
	if !in.Health().OK {
		t.Fatal("MQ down 1m should still be OK")
	}
	now = now.Add(90 * time.Second)
	if in.Health().OK {
		t.Fatal("MQ down 2m30s should not be OK")
	}
	in.SetMQConnected(true)
	if !in.Health().OK {
		t.Fatal("MQ reconnect should restore OK")
	}
}
