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

func TestSignalAlwaysDispatches(t *testing.T) {
	ws := workingset.New(10)
	ws.Add(pkg("proj", "pkg-a", model.RollupFailed))
	<-ws.Dispatch() // drain Add dispatch
	ws.Signal(pkg("proj", "pkg-a", model.RollupFailed)) // already in set — should still dispatch
	select {
	case p := <-ws.Dispatch():
		if p.Name != "pkg-a" {
			t.Errorf("unexpected package %s", p.Name)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Signal did not dispatch for existing package")
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
