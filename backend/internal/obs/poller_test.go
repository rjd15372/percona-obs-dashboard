package obs

import (
	"os"
	"strings"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

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
