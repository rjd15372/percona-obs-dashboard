package worker_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/worker"
	"github.com/percona/obs-dashboard/internal/workingset"
)

type captureTask struct {
	mu   sync.Mutex
	seen []*model.Package
}

func (t *captureTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	t.mu.Lock()
	t.seen = append(t.seen, pkg)
	t.mu.Unlock()
	return nil
}

type errorTask struct{}

func (t errorTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	return errors.New("task error")
}

type succeedingTask struct{}

func (t succeedingTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	pkg.RollupState = model.RollupSucceeded
	return nil
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPoolRunsTasksForDispatchedPackage(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)
	capture := &captureTask{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := worker.NewPool(2, []worker.Task{capture}, nil, db, h, ws)
	p.Start(ctx)

	pkg := &model.Package{
		Project: "isv:percona", Name: "pkg-a", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg)

	deadline := time.After(500 * time.Millisecond)
	for {
		capture.mu.Lock()
		n := len(capture.seen)
		capture.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("task was never run")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPoolRemovesSucceededPackageFromWorkingSet(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())

	p := worker.NewPool(1, []worker.Task{succeedingTask{}}, nil, db, h, ws)
	p.Start(ctx)

	// Target is already succeeded and published so that allTargetsPublished
	// returns true for the right reason (not vacuously because no succeeded
	// target exists).
	pkg := &model.Package{
		Project: "isv:percona", Name: "pkg-a", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded", Published: true}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg) // trigger worker — succeedingTask sets RollupSucceeded, then ws.Remove fires

	time.Sleep(200 * time.Millisecond) // wait for worker to finish

	cancel() // stop the worker goroutine before making assertions

	time.Sleep(10 * time.Millisecond) // give goroutine time to exit

	// Package was removed from working set. ws.Add should now dispatch again.
	ws.Add(pkg)
	select {
	case <-ws.Dispatch():
		// correct — package was removed, so Add dispatched again
	case <-time.After(100 * time.Millisecond):
		t.Fatal("package was not removed from working set after success")
	}
}

// TestPoolDoesNotRemoveWhenUnpublished verifies that the working-set removal
// gate requires allTargetsPublished: if any succeeded target is not yet
// published the package must stay in the working set.
func TestPoolDoesNotRemoveWhenUnpublished(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := worker.NewPool(1, []worker.Task{succeedingTask{}}, nil, db, h, ws)
	p.Start(ctx)

	// Target is succeeded but NOT yet published — allTargetsPublished must
	// return false so the removal is blocked.
	pkg := &model.Package{
		Project: "isv:percona", Name: "pkg-b", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded", Published: false}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg) // succeedingTask sets RollupSucceeded, but target unpublished → no removal

	time.Sleep(200 * time.Millisecond) // wait for worker to finish

	cancel()
	time.Sleep(10 * time.Millisecond)

	// Package must still be in the working set (not removed). ws.Add should
	// NOT dispatch it again because it is already present.
	ws.Add(pkg)
	select {
	case <-ws.Dispatch():
		t.Fatal("package was unexpectedly removed from working set despite unpublished target")
	case <-time.After(100 * time.Millisecond):
		// correct — package is still in the working set
	}
}

func TestPoolContinuesAfterTaskError(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)
	capture := &captureTask{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := worker.NewPool(1, []worker.Task{errorTask{}, capture}, nil, db, h, ws)
	p.Start(ctx)

	pkg := &model.Package{
		Project: "isv:percona", Name: "pkg-a", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg)

	deadline := time.After(500 * time.Millisecond)
	for {
		capture.mu.Lock()
		n := len(capture.seen)
		capture.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second task was never run after first task error")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ---- helper task types for ProcessOnce tests ----

// setReasonTask sets BuildReason on building targets.
type setReasonTask struct{ reason string }

func (t setReasonTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	for i, target := range pkg.Targets {
		if target.State == "building" {
			pkg.Targets[i].BuildReason = t.reason
		}
	}
	return nil
}

// setStateTask transitions a specific target to a new state.
type setStateTask struct{ repo, arch, state, details string }

func (t setStateTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	for i, target := range pkg.Targets {
		if target.Repo == t.repo && target.Arch == t.arch {
			pkg.Targets[i].State = t.state
			pkg.Targets[i].Details = t.details
		}
	}
	return nil
}

// setPublishedTask marks a target as published.
type setPublishedTask struct{ repo, arch string }

func (t setPublishedTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	for i, target := range pkg.Targets {
		if target.Repo == t.repo && target.Arch == t.arch {
			pkg.Targets[i].Published = true
		}
	}
	return nil
}

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestProcessOnceEmitsBuildStarted(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "mypkg",
		Scope: model.ScopeVersion, RollupState: model.RollupBuilding,
		Targets:   []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{setReasonTask{"source change"}}, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, err := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != model.EventBuildStarted {
		t.Errorf("expected build_started, got %q", evts[0].Type)
	}
	if evts[0].Why != "source change" {
		t.Errorf("expected why=source change, got %q", evts[0].Why)
	}
}

func TestProcessOnceEmitsFailedStates(t *testing.T) {
	for _, tc := range []struct{ state, details, wantWhy string }{
		{"failed", "", ""},
		{"unresolvable", "nothing provides libpq", "unresolvable: nothing provides libpq"},
		{"broken", "no source", "broken: no source"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			db := setupDB(t)
			h := hubpkg.New()
			ws := workingset.New(10)

			pkg := &model.Package{
				Project: "isv:percona:ppg:17", Name: "mypkg",
				Scope: model.ScopeVersion, RollupState: model.RollupBuilding,
				Targets:   []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
				UpdatedAt: time.Now().UTC(),
			}
			if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
				t.Fatalf("seed: %v", err)
			}

			pool := worker.NewPool(0, []worker.Task{
				setStateTask{"Ubuntu_24.04", "x86_64", tc.state, tc.details},
			}, nil, db, h, ws)
			pool.ProcessOnce(context.Background(), pkg)

			now := time.Now().UTC()
			evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
			if len(evts) != 1 {
				t.Fatalf("expected 1 event, got %d: %v", len(evts), evts)
			}
			if evts[0].Type != model.EventFailed {
				t.Errorf("expected failed, got %q", evts[0].Type)
			}
			if evts[0].Why != tc.wantWhy {
				t.Errorf("expected why=%q, got %q", tc.wantWhy, evts[0].Why)
			}
		})
	}
}

func TestProcessOnceNoEventForBlocked(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "mypkg",
		Scope: model.ScopeVersion, RollupState: model.RollupBuilding,
		Targets:   []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
	}, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 0 {
		t.Errorf("expected no events for blocked, got %d", len(evts))
	}
}

func TestProcessOnceEmitsPublished(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "mypkg",
		Scope: model.ScopeVersion, RollupState: model.RollupSucceeded,
		Targets:   []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded", Published: false}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{
		setPublishedTask{"Ubuntu_24.04", "x86_64"},
	}, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != model.EventPublished {
		t.Errorf("expected published, got %q", evts[0].Type)
	}
}
