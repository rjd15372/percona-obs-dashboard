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

type publishedTask struct{}

func (t publishedTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	pkg.RollupState = model.RollupPublished
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

	p := worker.NewPool(2, []worker.Task{capture}, nil, nil, db, h, ws)
	p.Start(ctx)

	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "pkg-a",
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

	p := worker.NewPool(1, []worker.Task{publishedTask{}}, nil, nil, db, h, ws)
	p.Start(ctx)

	// IsContainer must be non-nil and RollupPublished must be set for removal.
	isContainer := false
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "pkg-a",
		RollupState: model.RollupFailed, OKTargets: 1, TotalTargets: 1,
		IsContainer: &isContainer,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded", Published: true}},
		UpdatedAt:   time.Now().UTC(),
	}
	ws.Signal(pkg) // trigger worker — publishedTask sets RollupPublished, then ws.Remove fires

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

	p := worker.NewPool(1, []worker.Task{succeedingTask{}}, nil, nil, db, h, ws)
	p.Start(ctx)

	// Target is succeeded but NOT yet published — allTargetsPublished must
	// return false so the removal is blocked.
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "pkg-b",
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

	p := worker.NewPool(1, []worker.Task{errorTask{}, capture}, nil, nil, db, h, ws)
	p.Start(ctx)

	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "pkg-a",
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

// setTargetReasonTask sets BuildReason on a specific target unconditionally.
type setTargetReasonTask struct{ repo, arch, reason string }

func (t setTargetReasonTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	for i, target := range pkg.Targets {
		if target.Repo == t.repo && target.Arch == t.arch {
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
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{setReasonTask{"source change"}}, nil, nil, db, h, ws)
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

func TestProcessOnceEmitsFailedTerminal(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "failed", ""},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(evts), evts)
	}
	if evts[0].Type != model.EventFailed {
		t.Errorf("expected failed, got %q", evts[0].Type)
	}
	if evts[0].Why != "" {
		t.Errorf("expected empty why, got %q", evts[0].Why)
	}
}

func TestProcessOnceNoEventForBlocked(t *testing.T) {
	t.Run("no build reason → 0 events", func(t *testing.T) {
		db := setupDB(t)
		h := hubpkg.New()
		ws := workingset.New(10)

		pkg := &model.Package{
			Project:     "isv:percona:ppg:17",
			Name:        "mypkg",
			RollupState: model.RollupBuilding,
			Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
			UpdatedAt:   time.Now().UTC(),
		}
		if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pool := worker.NewPool(0, []worker.Task{
			setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
		}, nil, nil, db, h, ws)
		pool.ProcessOnce(context.Background(), pkg)

		now := time.Now().UTC()
		evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
		if len(evts) != 0 {
			t.Errorf("expected 0 events, got %d", len(evts))
		}
	})

	t.Run("with build reason → build_started then blocked", func(t *testing.T) {
		db := setupDB(t)
		h := hubpkg.New()
		ws := workingset.New(10)

		pkg := &model.Package{
			Project:     "isv:percona:ppg:17",
			Name:        "mypkg",
			RollupState: model.RollupBuilding,
			Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
			UpdatedAt:   time.Now().UTC(),
		}
		if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pool := worker.NewPool(0, []worker.Task{
			setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
			setTargetReasonTask{"Ubuntu_24.04", "x86_64", "source change"},
		}, nil, nil, db, h, ws)
		pool.ProcessOnce(context.Background(), pkg)

		now := time.Now().UTC()
		evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
		if len(evts) != 2 {
			t.Fatalf("expected 2 events, got %d", len(evts))
		}
		// QueryEvents returns newest-first; within the same timestamp events are
		// returned in reverse insertion order (blocked inserted after build_started).
		types := map[model.EventType]bool{}
		for _, e := range evts {
			types[e.Type] = true
		}
		if !types[model.EventBuildStarted] {
			t.Errorf("expected build_started event, got %v", evts)
		}
		if !types[model.EventBlocked] {
			t.Errorf("expected blocked event, got %v", evts)
		}
	})
}

func TestProcessOnceEmitsSucceededOnPublish(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded", Published: false}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{
		setPublishedTask{"Ubuntu_24.04", "x86_64"},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != model.EventSucceeded {
		t.Errorf("expected succeeded, got %q", evts[0].Type)
	}
	if evts[0].Repo != "Ubuntu_24.04" || evts[0].Arch != "x86_64" {
		t.Errorf("expected repo/arch on event, got %q/%q", evts[0].Repo, evts[0].Arch)
	}
}

func TestBuildStartedFiresOnBlockedState(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	// Seed: target already blocked, no BuildReason yet.
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "blocked"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Task: set BuildReason while still blocked. The blocked event does NOT re-fire
	// because there is no state transition (old.State == "blocked" == t.State).
	// Only build_started fires.
	pool := worker.NewPool(0, []worker.Task{
		setTargetReasonTask{"Ubuntu_24.04", "x86_64", "dep changed"},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(evts), evts)
	}
	if evts[0].Type != model.EventBuildStarted {
		t.Errorf("expected build_started, got %q", evts[0].Type)
	}
}

func TestIntermediateStateRequiresBuildReason(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Transition to unresolvable with no BuildReason: must not emit any event.
	pool := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "unresolvable", "nothing provides libpq"},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 0 {
		t.Errorf("expected 0 events without BuildReason, got %d", len(evts))
	}
}

func TestIntermediateStatesAllFire(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	// Cycle 1: target transitions to blocked with BuildReason.
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool1 := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "blocked", ""},
		setTargetReasonTask{"Ubuntu_24.04", "x86_64", "source change"},
	}, nil, nil, db, h, ws)
	pool1.ProcessOnce(context.Background(), pkg)

	// Cycle 2: unresolvable (BuildReason carried over in pkg after cycle 1).
	pool2 := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "unresolvable", "nothing provides libpq"},
	}, nil, nil, db, h, ws)
	pool2.ProcessOnce(context.Background(), pkg)

	// Cycle 3: broken.
	pool3 := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "broken", "patch failed"},
	}, nil, nil, db, h, ws)
	pool3.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))

	// Verify all four event types are present.
	if len(evts) != 4 {
		t.Fatalf("expected 4 events, got %d: %v", len(evts), evts)
	}
	wantTypes := map[model.EventType]bool{
		model.EventBuildStarted: true,
		model.EventBlocked:      true,
		model.EventUnresolvable: true,
		model.EventBroken:       true,
	}
	for _, e := range evts {
		if !wantTypes[e.Type] {
			t.Errorf("unexpected event type %q", e.Type)
		}
	}

	// Verify Why values by looking up events by type.
	byType := make(map[model.EventType]*model.Event, len(evts))
	for _, e := range evts {
		byType[e.Type] = e
	}
	if e := byType[model.EventUnresolvable]; e == nil || e.Why != "nothing provides libpq" {
		why := ""
		if e != nil {
			why = e.Why
		}
		t.Errorf("unresolvable why: want %q, got %q", "nothing provides libpq", why)
	}
	if e := byType[model.EventBroken]; e == nil || e.Why != "patch failed" {
		why := ""
		if e != nil {
			why = e.Why
		}
		t.Errorf("broken why: want %q, got %q", "patch failed", why)
	}
}

func TestSucceededOnPublishNotOnState(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	// Seed: building state.
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "building", Published: false}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Transition State to "succeeded" but leave Published false.
	pool := worker.NewPool(0, []worker.Task{
		setStateTask{"Ubuntu_24.04", "x86_64", "succeeded", ""},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 0 {
		t.Errorf("expected 0 events when State==succeeded but Published==false, got %d", len(evts))
	}
}

func TestSucceededOnPublishFlip(t *testing.T) {
	db := setupDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "mypkg",
		Version:     "17.5-1",
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded", Published: false}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := worker.NewPool(0, []worker.Task{
		setPublishedTask{"Ubuntu_24.04", "x86_64"},
	}, nil, nil, db, h, ws)
	pool.ProcessOnce(context.Background(), pkg)

	now := time.Now().UTC()
	evts, _ := store.QueryEvents(db, "isv:percona:ppg:17", now.Add(-time.Minute), now.Add(time.Minute))
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != model.EventSucceeded {
		t.Errorf("expected succeeded, got %q", evts[0].Type)
	}
	if evts[0].Repo != "Ubuntu_24.04" {
		t.Errorf("expected Repo=Ubuntu_24.04, got %q", evts[0].Repo)
	}
	if evts[0].Arch != "x86_64" {
		t.Errorf("expected Arch=x86_64, got %q", evts[0].Arch)
	}
	if evts[0].Version != "17.5-1" {
		t.Errorf("expected Version=17.5-1, got %q", evts[0].Version)
	}
}

type versionTask struct{ v string }

func (t versionTask) Run(_ context.Context, _ *obs.Client, pkg *model.Package) error {
	pkg.Version = t.v
	return nil
}

func TestPoolRoutesDevVsReleaseTasks(t *testing.T) {
	db := openDB(t)
	h := hubpkg.New()
	ws := workingset.New(10)

	devTasks := []worker.Task{versionTask{"dev"}}
	releaseTasks := []worker.Task{versionTask{"release"}}

	pool := worker.NewPool(0, devTasks, releaseTasks, nil, db, h, ws)

	// Dev package: devTasks should run.
	devPkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "pg_tde",
		IsRelease:   false,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	pool.ProcessOnce(context.Background(), devPkg)
	if devPkg.Version != "dev" {
		t.Errorf("dev package: expected version 'dev', got %q", devPkg.Version)
	}

	// Release package: releaseTasks should run, hub.Notify should NOT fire.
	notifyCh := make(chan []byte, 16)
	h.Register(notifyCh)
	defer h.Unregister(notifyCh)

	relPkg := &model.Package{
		Project:     "isv:percona:ppg:releases:17",
		Name:        "pg_tde",
		IsRelease:   true,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	pool.ProcessOnce(context.Background(), relPkg)
	if relPkg.Version != "release" {
		t.Errorf("release package: expected version 'release', got %q", relPkg.Version)
	}

	// Give a moment to drain any unexpected notifications.
	time.Sleep(10 * time.Millisecond)
	if len(notifyCh) > 0 {
		t.Errorf("expected no hub notifications for release package, got %d", len(notifyCh))
	}
}
