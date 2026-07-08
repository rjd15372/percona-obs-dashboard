package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
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
