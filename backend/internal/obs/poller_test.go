package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func TestPreservePublishedAcrossTransientStateChange(t *testing.T) {
	// A target that was succeeded+published briefly goes to blocked (dependency
	// waiting), then is succeeded again by the time the next poller tick runs.
	// Published must be preserved so the worker does not fire a spurious succeeded
	// event for a package that never actually rebuilt.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "aarch64", State: "succeeded", Published: true},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "aarch64", State: "blocked", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if !next.Targets[0].Published {
		t.Error("Published should be preserved when target was succeeded+published and is now blocked")
	}
}

func TestPreservePublishedSucceededToSucceeded(t *testing.T) {
	// Unchanged state: Published must still carry over.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: true},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if !next.Targets[0].Published {
		t.Error("Published should be preserved when state is unchanged")
	}
}

func TestDoNotPreservePublishedWhenPrevNotPublished(t *testing.T) {
	// Prev was not published (active build in progress) — Published must stay false
	// so PublishStateTask can detect the new publish and fire the succeeded event.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "building", Published: false},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if next.Targets[0].Published {
		t.Error("Published must not be set when previous target was not published")
	}
}

func TestTargetsChangedDetectsDetailsChange(t *testing.T) {
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished"},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished", Details: "succeeded"},
		},
	}

	if !targetsChanged(prev, next) {
		t.Fatal("expected target details change to be detected")
	}
}

func TestNoPollerRollupEvents(t *testing.T) {
	data, err := os.ReadFile("poller.go")
	if err != nil {
		t.Fatalf("read poller.go: %v", err)
	}
	if strings.Contains(string(data), "AppendEvent(") {
		t.Error("poller.go must not call store.AppendEvent — worker is the sole event emitter")
	}
}

func TestEvictPublishFlagsRefetches(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, err := c.ProjectPublishFlags(ctx, "gone-project"); err != nil {
		t.Fatal(err)
	}
	c.EvictPublishFlags("gone-project")
	if _, err := c.ProjectPublishFlags(ctx, "gone-project"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected refetch after evict, calls=%d", calls)
	}
}

func TestAwaitingPublishReady(t *testing.T) {
	stored := func(targets ...model.Target) *model.Package {
		return &model.Package{Targets: targets}
	}
	succ := func(repo, arch string, published bool) model.Target {
		return model.Target{Repo: repo, Arch: arch, State: "succeeded", Published: published}
	}
	repoStates := map[string]string{
		"images/x86_64":  "published",
		"images/aarch64": "building",
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"unpublished target, repo now published", stored(succ("images", "x86_64", false)), true},
		{"unpublished target, repo still building", stored(succ("images", "aarch64", false)), false},
		{"already published", stored(succ("images", "x86_64", true)), false},
		{"building target ignored", stored(model.Target{Repo: "images", Arch: "x86_64", State: "building"}), false},
		{"repo/arch missing from map", stored(succ("other", "x86_64", false)), false},
		{"one ready among several", stored(succ("images", "aarch64", false), succ("images", "x86_64", false)), true},
		{"nil package", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := awaitingPublishReady(tc.pkg, repoStates); got != tc.want {
				t.Fatalf("awaitingPublishReady = %v, want %v", got, tc.want)
			}
		})
	}
}

// The discovery pass is bounded (one call per live project per interval) and
// must not queue behind working-set traffic: its build-results fetch bypasses
// the background rate limiter.
func TestPollerFetchProjectResultsBypassesLimiter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`<resultlist state="x"></resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	c.SetMinuteBudget(1)
	// Exhaust the minute budget so any limiter-governed request blocks until
	// the next window — far beyond this test's context deadline.
	if err := c.limiter.acquire(context.Background()); err != nil {
		t.Fatalf("drain budget: %v", err)
	}

	p := &Poller{client: c}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if _, _, err := p.fetchProjectResults(ctx, "isv:percona:ppg:devel:17"); err != nil {
		t.Fatalf("fetch blocked by exhausted limiter (no bypass): %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("OBS endpoint hits = %d, want 1", hits.Load())
	}
}

type stubGate struct {
	active atomic.Bool
	wake   chan struct{}
}

func (s *stubGate) Active() bool               { return s.active.Load() }
func (s *stubGate) Subscribe() <-chan struct{} { return s.wake }

func TestPollerRunGatedByPresence(t *testing.T) {
	var searchHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/") {
			searchHits.Add(1)
		}
		w.Write([]byte(`<collection matches="0"></collection>`))
	}))
	defer srv.Close()

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	g := &stubGate{wake: make(chan struct{}, 1)}
	p := &Poller{
		client:   NewClient(srv.URL, "u", "p"),
		db:       db,
		interval: 25 * time.Millisecond,
		root:     "isv:percona",
		gate:     g,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Startup tick is unconditional: exactly one discovery fetch.
	waitFor(t, func() bool { return searchHits.Load() == 1 })
	time.Sleep(120 * time.Millisecond) // several ticks pass while idle
	if got := searchHits.Load(); got != 1 {
		t.Fatalf("idle poller fetched: %d discovery calls, want 1 (startup only)", got)
	}

	g.wake <- struct{}{} // wake: immediate tick
	waitFor(t, func() bool { return searchHits.Load() == 2 })

	g.active.Store(true) // active: ticks resume
	waitFor(t, func() bool { return searchHits.Load() >= 3 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
