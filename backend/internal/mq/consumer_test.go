package mq

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestMergePackageTargetPreservesDetailsForRepeatedState(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	existing := &model.Package{
		Project:      "isv:percona:PR:pr-33:ppg:17",
		Name:         "pg_tde",
		Tags:         []string{"ppg", "pr"},
		RollupState:  model.RollupFinished,
		OKTargets:    0,
		TotalTargets: 1,
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished", Details: "succeeded"},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, existing, time.Now().UTC()); err != nil {
		t.Fatalf("upsert existing package: %v", err)
	}

	consumer := &Consumer{db: db, root: "isv:percona"}
	merged := consumer.mergePackageTarget(mqMessage{
		Project: "isv:percona:PR:pr-33:ppg:17",
		Package: "pg_tde",
		Repo:    "images",
		Arch:    "x86_64",
	}, model.RollupFinished)

	if len(merged.Targets) != 1 {
		t.Fatalf("expected one target, got %d", len(merged.Targets))
	}
	if merged.Targets[0].Details != "succeeded" {
		t.Fatalf("expected details to be preserved, got %q", merged.Targets[0].Details)
	}
}

func TestMQStateToRollupUnchangedIsFinished(t *testing.T) {
	if got := mqStateToRollup("opensuse.obs.package.build_unchanged"); got != model.RollupFinished {
		t.Fatalf("build_unchanged → %s, want finished", got)
	}
}

// build_unchanged must wake the working set: the build completed (with an
// identical result), which un-parks a package waiting on MQ completions.
func TestBuildUnchangedWakesWorkingSet(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4)
	h := hubpkg.New()
	c := NewConsumer("", db, h, nil, ws, "isv:percona")

	// Seed a stored package with a building target (as if parked).
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pkg-a",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:17", "package": "pkg-a",
		"repository": "repo", "arch": "x86_64",
	})
	c.handle(context.Background(), amqp.Delivery{
		RoutingKey: "opensuse.obs.package.build_unchanged",
		Body:       body,
	})

	select {
	case got := <-ws.Dispatch():
		if got.Name != "pkg-a" {
			t.Fatalf("dispatched %q, want pkg-a", got.Name)
		}
		for _, tgt := range got.Targets {
			if tgt.Repo == "repo" && tgt.Arch == "x86_64" && tgt.State != "finished" {
				t.Fatalf("merged target state = %q, want finished", tgt.State)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("build_unchanged did not signal the working set")
	}
}
