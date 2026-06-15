package worker_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/hub"
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
	h := hub.New()
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
	h := hub.New()
	ws := workingset.New(10)

	ctx, cancel := context.WithCancel(context.Background())

	p := worker.NewPool(1, []worker.Task{succeedingTask{}}, nil, db, h, ws)
	p.Start(ctx)

	pkg := &model.Package{
		Project: "isv:percona", Name: "pkg-a", Scope: model.ScopeCommon,
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: time.Now().UTC(),
	}
	ws.Signal(pkg) // trigger worker — worker runs succeedingTask, then calls ws.Remove

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

func TestPoolContinuesAfterTaskError(t *testing.T) {
	db := openDB(t)
	h := hub.New()
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
