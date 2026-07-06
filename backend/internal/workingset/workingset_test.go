package workingset_test

import (
	"context"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func pkg(project, name string, state model.RollupState) *model.Package {
	return &model.Package{Project: project, Name: name, RollupState: state}
}

func TestAddNewPackage(t *testing.T) {
	ws := workingset.New(10)
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
	ws := workingset.New(10)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch() // drain first Add dispatch
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed)) // second Add — no-op
	select {
	case <-ws.Dispatch():
		t.Fatal("expected no dispatch for existing package")
	case <-time.After(50 * time.Millisecond):
		// correct — no dispatch
	}
}

func TestSignalDispatchesAfterDone(t *testing.T) {
	ws := workingset.New(10)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch()               // drain Add dispatch (package is now in-flight)
	ws.Done("proj/pkg-a")         // simulate worker completion
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
	ws := workingset.New(10)
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
	ws := workingset.New(10)
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
	ws := workingset.New(10)
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
	ws := workingset.New(10)
	ws.Seed([]*model.Package{pkg("proj", "pkg-a", model.RollupFailed)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws.StartScheduler(ctx, 20*time.Millisecond)
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
	ws := workingset.New(10)
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

func TestStatsUsesRecordedStateNotLivePointer(t *testing.T) {
	ws := workingset.New(10)
	p := &model.Package{Project: "p", Name: "a", RollupState: model.RollupBuilding}
	ws.Seed([]*model.Package{p})
	p.RollupState = model.RollupFailed // mutate the shared pointer (as the worker would)
	s := ws.Stats()
	if s.ByState["building"] != 1 || s.ByState["failed"] != 0 {
		t.Fatalf("Stats should reflect recorded state under lock, got %v", s.ByState)
	}
}
