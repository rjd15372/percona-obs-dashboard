package obs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

// fakeOBS serves canned XML per path prefix; unknown paths 404.
type fakeOBS struct {
	srv   *httptest.Server
	hits  atomic.Int64
	paths map[string]string // path (with query) prefix → body
	fail  atomic.Bool
}

func newFakeOBS(t *testing.T, paths map[string]string) *fakeOBS {
	f := &fakeOBS{paths: paths}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		full := r.URL.Path
		if r.URL.RawQuery != "" {
			full += "?" + r.URL.RawQuery
		}
		for prefix, body := range f.paths {
			if strings.HasPrefix(full, prefix) {
				fmt.Fprint(w, body)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func fleetOf(t *testing.T, a, b *fakeOBS) *Fleet {
	mk := func(slug, root string, f *fakeOBS) *Instance {
		return NewInstance(InstanceInfo{Name: slug, Slug: slug, Root: root,
			WebURL: "https://" + slug, DownloadURL: "https://dl." + slug, Registry: "reg." + slug},
			NewClient(f.srv.URL, "u", "p"))
	}
	return NewFleet(mk("x", "isv:percona", a), mk("y", "percona", b))
}

const resultX = `<resultlist>
<result project="isv:percona:ppg:17" repository="RHEL_9" arch="x86_64" state="published">
  <status package="pg" code="succeeded"/>
</result></resultlist>`

const resultY = `<resultlist>
<result project="percona:ppg:17" repository="Debian_12" arch="aarch64" state="building">
  <status package="pg" code="building"/>
</result></resultlist>`

func TestFleetBuildResultsMerges(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, map[string]string{"/build/percona:ppg:17/_result": resultY})
	f := fleetOf(t, x, y)

	states, repoStates, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil || len(failed) != 0 {
		t.Fatalf("err=%v failed=%v", err, failed)
	}
	if len(states) != 2 {
		t.Fatalf("want 2 states, got %+v", states)
	}
	byRepo := map[string]PackageBuildState{}
	for _, s := range states {
		byRepo[s.Repo] = s
	}
	if byRepo["RHEL_9"].Instance != "x" || byRepo["Debian_12"].Instance != "y" {
		t.Errorf("instance stamping wrong: %+v", states)
	}
	if byRepo["RHEL_9"].Project != "ppg:17" || byRepo["Debian_12"].Project != "ppg:17" {
		t.Errorf("project not translated to logical: %+v", states)
	}
	if repoStates["RHEL_9/x86_64"] != "published" || repoStates["Debian_12/aarch64"] != "building" {
		t.Errorf("repo states: %v", repoStates)
	}
	if f.Owner("ppg:17", "Debian_12").Slug != "y" || f.Owner("ppg:17", "RHEL_9").Slug != "x" {
		t.Error("repo owners not recorded")
	}
}

func TestFleetPartialFailure(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil)
	y.fail.Store(true)
	f := fleetOf(t, x, y)

	states, _, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil {
		t.Fatalf("partial failure must not error: %v", err)
	}
	if len(states) != 1 || len(failed) != 1 || failed[0] != "y" {
		t.Fatalf("states=%+v failed=%v", states, failed)
	}

	x.fail.Store(true)
	if _, _, _, err := f.BuildResults(context.Background(), "ppg:17"); err == nil {
		t.Fatal("all instances failing must error")
	}
}

func TestFleetNotHostedIsNotFailure(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil) // 404 for everything
	f := fleetOf(t, x, y)
	_, _, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil || len(failed) != 0 {
		t.Fatalf("404 must be 'not hosted': err=%v failed=%v", err, failed)
	}

	_, _, _, err = f.BuildResults(context.Background(), "ppg:99")
	if !IsNotFound(err) {
		t.Fatalf("all-404 must return NotFound, got %v", err)
	}
}

func TestFleetMembershipLimitsFanOut(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	f.SetMembers("x", []string{"ppg:17"})
	f.SetMembers("y", nil)
	if _, _, _, err := f.BuildResults(context.Background(), "ppg:17"); err != nil {
		t.Fatal(err)
	}
	if y.hits.Load() != 0 {
		t.Errorf("non-hosting instance was queried %d times", y.hits.Load())
	}
	if !f.IsHosted("ppg:17") || f.IsHosted("ppg:18") {
		t.Error("IsHosted wrong")
	}
}

func TestFleetRoutesByOwner(t *testing.T) {
	x := newFakeOBS(t, nil)
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:17/_result?package=pg":           resultY,
		"/build/percona:ppg:17/Debian_12/aarch64/pg/_reason": `<reason><explain>source change</explain></reason>`,
	})
	f := fleetOf(t, x, y)

	// Unknown owner → one PackageBuildResults refresh, then route to y.
	r, err := f.PackageBuildReason(context.Background(), "ppg:17", "Debian_12", "aarch64", "pg")
	if err != nil || r.Explain != "source change" {
		t.Fatalf("reason=%+v err=%v", r, err)
	}
	xHits := x.hits.Load()
	if _, err := f.PackageBuildReason(context.Background(), "ppg:17", "Debian_12", "aarch64", "pg"); err != nil {
		t.Fatal(err)
	}
	if x.hits.Load() != xHits {
		t.Error("known owner must not touch other instances")
	}
}

func TestFleetSingleInstanceNeverRefreshes(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/build/isv:percona:ppg:17/RHEL_9/x86_64/pg/_reason": `<reason><explain>new build</explain></reason>`,
	})
	f := SingleFleet(NewClient(x.srv.URL, "u", "p"), "isv:percona")
	if _, err := f.PackageBuildReason(context.Background(), "isv:percona:ppg:17", "RHEL_9", "x86_64", "pg"); err != nil {
		t.Fatal(err)
	}
	if x.hits.Load() != 1 {
		t.Errorf("single-instance fleet made %d requests, want 1", x.hits.Load())
	}
}

func TestFleetFirstWins(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/build/isv:percona:ppg:17/_result?view=versrel": `<resultlist><result repository="RHEL_9" arch="x86_64"><status package="pg" code="building"/></result></resultlist>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:17/_result?view=versrel": `<resultlist><result repository="Debian_12" arch="aarch64"><status package="pg" code="succeeded" versrel="17.5-1"/></result></resultlist>`,
	})
	f := fleetOf(t, x, y)
	v, err := f.PackageVersionResult(context.Background(), "ppg:17", "pg")
	if err != nil || v != "17.5-1" {
		t.Fatalf("version=%q err=%v (want first non-empty)", v, err)
	}
}

func TestFleetCompositePublishFlags(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/source/isv:percona:ppg:17/_meta": `<project><publish><disable/></publish></project>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/source/percona:ppg:17/_meta": `<project/>`,
	})
	f := fleetOf(t, x, y)
	f.SetOwner("ppg:17", "RHEL_9", "x")
	f.SetOwner("ppg:17", "Debian_12", "y")
	flags, err := f.ProjectPublishFlags(context.Background(), "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if flags.Publishes("RHEL_9") {
		t.Error("RHEL_9 is on x where publishing is disabled")
	}
	if !flags.Publishes("Debian_12") {
		t.Error("Debian_12 is on y where publishing is enabled")
	}
}

func TestFleetBinaryListTranslatesAndOwns(t *testing.T) {
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:releases:17/_result?view=binarylist": `<resultlist>
<result project="percona:ppg:releases:17" repository="Debian_12" arch="aarch64">
  <binarylist package="pg"><binary filename="pg_17.5_arm64.deb" size="1" mtime="2"/></binarylist>
</result></resultlist>`,
	})
	x := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	bins, err := f.ProjectBinaryList(context.Background(), "ppg:releases:17")
	if err != nil || len(bins) != 1 {
		t.Fatalf("bins=%+v err=%v", bins, err)
	}
	if bins[0].Project != "ppg:releases:17" || bins[0].Instance != "y" {
		t.Errorf("binary not translated/stamped: %+v", bins[0])
	}
	if f.Owner("ppg:releases:17", "Debian_12").Slug != "y" {
		t.Error("binary list must record repo owner")
	}
}

func TestFleetDiscover(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/search/project/id": `<collection><project name="isv:percona:ppg:17"/><project name="isv:percona:common"/></collection>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/search/project/id": `<collection><project name="percona:ppg:17"/><project name="percona:ppg:18"/></collection>`,
	})
	f := fleetOf(t, x, y)
	projects, answered, err := f.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 || !answered["x"] || !answered["y"] {
		t.Fatalf("projects=%v answered=%v", projects, answered)
	}
	if hs := f.hosts("ppg:18"); len(hs) != 1 || hs[0].Slug != "y" {
		t.Errorf("ppg:18 hosts = %v", hs)
	}

	y.fail.Store(true)
	_, answered, err = f.Discover(context.Background())
	if err != nil || answered["y"] {
		t.Fatalf("err=%v answered=%v", err, answered)
	}
	if hs := f.hosts("ppg:18"); len(hs) != 1 || hs[0].Slug != "y" {
		t.Error("membership of an instance that failed discovery must be kept")
	}
}

// SeedOwners must seed membership from stored targets, so an instance that
// is down at startup (never answers Discover) still counts as a host.
func TestFleetSeedOwnersSeedsMembership(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/search/project/id": `<collection><project name="isv:percona:ppg:17"/></collection>`})
	y := newFakeOBS(t, map[string]string{"/search/project/id": `<collection></collection>`})
	f := fleetOf(t, x, y)
	f.SeedOwners([]*model.Package{
		{Project: "ppg:17", Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", Instance: "y"}}},
		{Project: "ppg:18", Targets: []model.Target{{Repo: "RHEL_9", Arch: "x86_64"}}},
	})
	if hs := f.hosts("ppg:17"); len(hs) != 1 || hs[0].Slug != "y" {
		t.Errorf("ppg:17 hosts = %v, want [y] from seeded membership", hs)
	}
	if !f.IsHosted("ppg:17") {
		t.Error("seeded project must be hosted")
	}
	if f.IsHosted("ppg:18") {
		t.Error("unstamped targets must not seed membership")
	}

	// y is down: Discover keeps y's seeded membership; x's answer adds x.
	y.fail.Store(true)
	if _, answered, err := f.Discover(context.Background()); err != nil || answered["y"] {
		t.Fatalf("err=%v answered=%v", err, answered)
	}
	if hs := f.hosts("ppg:17"); len(hs) != 2 {
		t.Errorf("ppg:17 hosts after partial discover = %v, want [x y]", hs)
	}

	// y answers without ppg:17: its seeded membership is replaced.
	y.fail.Store(false)
	if _, _, err := f.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hs := f.hosts("ppg:17"); len(hs) != 1 || hs[0].Slug != "x" {
		t.Errorf("ppg:17 hosts after full discover = %v, want [x]", hs)
	}
}

func TestFleetMetricsSum(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, map[string]string{"/build/percona:ppg:17/_result": resultY})
	f := fleetOf(t, x, y)
	_, _, _, _ = f.BuildResults(context.Background(), "ppg:17")
	if got := f.MetricsSnapshot()["build_results"]; got != 2 {
		t.Errorf("summed build_results = %d, want 2", got)
	}
	st := f.Statuses()
	if len(st) != 2 || st[0].Total != 1 || st[1].Total != 1 {
		t.Errorf("statuses = %+v", st)
	}
}

func TestFleetHealthFlipNotifiesOnce(t *testing.T) {
	x := newFakeOBS(t, nil)
	y := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	for _, in := range f.Instances() {
		in.SetMQConnected(true)
	}
	last := f.healthBaseline()
	var got []string
	notify := func(slug string, h Health) { got = append(got, fmt.Sprintf("%s:%v", slug, h.OK)) }

	yi := f.Instance("y")
	for i := 0; i < 3; i++ {
		yi.observe(fmt.Errorf("boom"))
	}
	f.checkHealth(last, notify)
	f.checkHealth(last, notify)
	yi.observe(nil)
	f.checkHealth(last, notify)
	if strings.Join(got, ",") != "y:false,y:true" {
		t.Errorf("notifications = %v", got)
	}
}
