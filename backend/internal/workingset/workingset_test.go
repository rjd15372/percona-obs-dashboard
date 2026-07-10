package workingset_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func pkg(project, name string, state model.RollupState) *model.Package {
	return &model.Package{Project: project, Name: name, RollupState: state}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func drain(t *testing.T, ws *workingset.WorkingSet) *model.Package {
	t.Helper()
	select {
	case p := <-ws.Dispatch():
		return p
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dispatch but nothing received")
		return nil
	}
}

func expectNoDispatch(t *testing.T, ws *workingset.WorkingSet) {
	t.Helper()
	select {
	case p := <-ws.Dispatch():
		t.Fatalf("unexpected dispatch of %s", p.Name)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAddNewPackage(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	select {
	case p := <-ws.Dispatch():
		if p.Name != "pkg-a" {
			t.Errorf("unexpected package %s", p.Name)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dispatch but nothing received")
	}
}

func TestAddExistingPackageIsNoop(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch()                                  // drain first Add dispatch
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed)) // second Add — no-op
	select {
	case <-ws.Dispatch():
		t.Fatal("expected no dispatch for existing package")
	case <-time.After(50 * time.Millisecond):
		// correct — no dispatch
	}
}

func TestSignalDispatchesAfterDone(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch()                                       // drain Add dispatch (package is now in-flight)
	ws.Done("proj/pkg-a", false)                          // simulate worker completion
	ws.Signal(pkg("proj", "pkg-a", model.RollupBuilding)) // should dispatch now
	select {
	case p := <-ws.Dispatch():
		if p.Name != "pkg-a" {
			t.Errorf("unexpected package %s", p.Name)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Signal did not dispatch after Done")
	}
}

func TestSignalSkippedWhileInFlight(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch() // drain — package is in-flight, no Done called
	ws.Signal(pkg("proj", "pkg-a", model.RollupBuilding))
	select {
	case <-ws.Dispatch():
		t.Fatal("Signal should not dispatch while package is in-flight")
	case <-time.After(50 * time.Millisecond):
		// correct — dispatch suppressed
	}
}

func TestSeedDoesNotDispatch(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Seed([]*model.Package{
		pkg("proj", "pkg-a", model.RollupFailed),
		pkg("proj", "pkg-b", model.RollupBuilding),
	})
	select {
	case <-ws.Dispatch():
		t.Fatal("Seed should not dispatch to channel")
	case <-time.After(50 * time.Millisecond):
		// correct
	}
}

func TestRemove(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch()
	ws.Remove("proj/pkg-a")
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed)) // should dispatch again (was removed)
	select {
	case <-ws.Dispatch():
		// correct
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dispatch after Remove+Add")
	}
}

func TestStartScheduler(t *testing.T) {
	// base doubles as the scheduler tick interval; keep it small so the test
	// doesn't need to wait for a real 30s tick.
	ws := workingset.New(10, 20*time.Millisecond, 5*time.Minute, 4)
	ws.Seed([]*model.Package{pkg("proj", "pkg-a", model.RollupFailed)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws.StartScheduler(ctx)
	select {
	case p := <-ws.Dispatch():
		if p.Name != "pkg-a" {
			t.Errorf("unexpected package %s", p.Name)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("scheduler did not dispatch seeded package")
	}
}

func TestStats(t *testing.T) {
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	ws.Seed([]*model.Package{
		pkg("p", "a", model.RollupSucceeded),
		pkg("p", "b", model.RollupSucceeded),
		pkg("p", "c", model.RollupFailed),
	})
	s := ws.Stats()
	if s.Total != 3 {
		t.Fatalf("Total = %d, want 3", s.Total)
	}
	if s.ByState["succeeded"] != 2 || s.ByState["failed"] != 1 {
		t.Fatalf("ByState = %v", s.ByState)
	}
	if s.Inflight != 0 {
		t.Fatalf("Inflight = %d, want 0", s.Inflight)
	}
}

func TestStatsReflectsLivePackagePointer(t *testing.T) {
	// Stats now reads state directly off the stored *model.Package (via the
	// entry), so mutations the worker makes in-place are visible immediately
	// — unlike the old cached-state-string behavior this replaces.
	ws := workingset.New(10, 30*time.Second, 5*time.Minute, 4)
	p := &model.Package{Project: "p", Name: "a", RollupState: model.RollupBuilding}
	ws.Seed([]*model.Package{p})
	p.RollupState = model.RollupFailed // mutate the shared pointer (as the worker would)
	s := ws.Stats()
	if s.ByState["failed"] != 1 || s.ByState["building"] != 0 {
		t.Fatalf("Stats should reflect the live package state, got %v", s.ByState)
	}
}

func TestBackoffDoublesOnUnchangedPass(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)                 // immediate dispatch on Add
	ws.Done("proj/pkg-a", false) // unchanged → interval doubles to 60s

	clock.Advance(31 * time.Second)
	ws.DispatchDue()
	expectNoDispatch(t, ws) // only 31s elapsed, due in 60s

	clock.Advance(30 * time.Second)
	ws.DispatchDue()
	drain(t, ws) // 61s elapsed ≥ 60s
}

func TestBackoffResetsOnChangedPass(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // 60s
	clock.Advance(61 * time.Second)
	ws.DispatchDue()
	drain(t, ws)
	ws.Done("proj/pkg-a", true) // changed → back to 30s

	clock.Advance(31 * time.Second)
	ws.DispatchDue()
	drain(t, ws)
}

func TestBackoffCapsAtMax(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	// 10 unchanged passes: 30s→1m→2m→4m→5m (capped)
	for i := 0; i < 10; i++ {
		ws.Done("proj/pkg-a", false)
		clock.Advance(5*time.Minute + time.Second)
		ws.DispatchDue()
		drain(t, ws)
	}
	ws.Done("proj/pkg-a", false)
	clock.Advance(4 * time.Minute)
	ws.DispatchDue()
	expectNoDispatch(t, ws) // capped at 5m, 4m is not enough
	clock.Advance(1*time.Minute + time.Second)
	ws.DispatchDue()
	drain(t, ws)
}

func TestSignalResetsBackoffAndDispatchesImmediately(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // backed off to 60s
	ws.Signal(pkg("proj", "pkg-a", model.RollupFinished))
	drain(t, ws) // immediate, no clock advance needed
}

func TestAddExistingResetsScheduleWithoutDispatch(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	ws := workingset.NewWithClock(10, 30*time.Second, 5*time.Minute, 4, clock.Now)

	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	drain(t, ws)
	ws.Done("proj/pkg-a", false) // 60s
	ws.Add(pkg("proj", "pkg-a", model.RollupBuilding))
	expectNoDispatch(t, ws) // Add on existing: no immediate dispatch...
	ws.DispatchDue()
	drain(t, ws) // ...but the schedule was reset to due-now
}
