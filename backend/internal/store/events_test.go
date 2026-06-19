package store

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestAppendQueryPruneEvents(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	e := &model.Event{
		ID:      "evt_01",
		Type:    model.EventFailed,
		Tags:    []string{"ppg"},
		Project: "isv:percona:ppg:17",
		Package: "pg_tde",
		What:    "build failed",
		Why:     "openssl bump",
		URL:     "https://build.opensuse.org/package/show/isv:percona:ppg:17/pg_tde",
		At:      now,
	}
	if err := AppendEvent(db, e); err != nil {
		t.Fatal(err)
	}

	// Query in range — should find 1 event
	events, err := QueryEvents(db, "isv:percona", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "evt_01" {
		t.Errorf("expected 1 event evt_01, got %d events", len(events))
	}
	if len(events[0].Tags) != 1 || events[0].Tags[0] != "ppg" {
		t.Errorf("expected tags [ppg], got %v", events[0].Tags)
	}

	// Query out of range — should find 0 events
	events, _ = QueryEvents(db, "isv:percona", now.Add(time.Hour), now.Add(2*time.Hour))
	if len(events) != 0 {
		t.Errorf("expected 0 out of range, got %d", len(events))
	}

	// Prune removes old events
	if err := PruneEvents(db, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	events, _ = QueryEvents(db, "isv:percona", now.Add(-time.Hour), now.Add(time.Hour))
	if len(events) != 0 {
		t.Errorf("expected 0 after prune, got %d", len(events))
	}
}

func TestEventVersionRoundtrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	evt := &model.Event{
		ID:      "evt_01JTEST",
		Type:    model.EventSucceeded,
		Tags:    []string{"ppg"},
		Project: "isv:percona:ppg:17",
		Package: "percona-pg_tde",
		Repo:    "UBI_9",
		Arch:    "x86_64",
		What:    "percona-pg_tde succeeded",
		Why:     "",
		Version: "17.5-1",
		URL:     "https://build.opensuse.org/package/show/isv:percona:ppg:17/percona-pg_tde",
		At:      now,
	}
	if err := AppendEvent(db, evt); err != nil {
		t.Fatal(err)
	}
	evts, err := QueryEvents(db, "isv:percona", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Version != "17.5-1" {
		t.Errorf("Version: got %q, want %q", evts[0].Version, "17.5-1")
	}

	// Event without version
	evt2 := &model.Event{
		ID:      "evt_02JTEST",
		Type:    model.EventBuildStarted,
		Tags:    []string{"ppg"},
		Project: "isv:percona:ppg:17",
		Package: "percona-pg_tde",
		Repo:    "UBI_9",
		Arch:    "x86_64",
		What:    "percona-pg_tde build started",
		Why:     "source change",
		URL:     "https://build.opensuse.org/package/live_build_log/isv:percona:ppg:17/percona-pg_tde/UBI_9/x86_64",
		At:      now.Add(-time.Minute),
	}
	if err := AppendEvent(db, evt2); err != nil {
		t.Fatal(err)
	}
	evts2, err := QueryEvents(db, "isv:percona", now.Add(-2*time.Minute), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var foundBuildStarted bool
	for _, e := range evts2 {
		if e.Type == model.EventBuildStarted && e.Version != "" {
			t.Errorf("build_started event should have empty version, got %q", e.Version)
		}
		if e.Type == model.EventBuildStarted {
			foundBuildStarted = true
		}
	}
	if !foundBuildStarted {
		t.Error("build_started event not found")
	}
}

func TestQueryPRBuildEventsIncludesPRCommon(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	events := []*model.Event{
		{
			ID:      "evt_pr_ppg",
			Type:    model.EventFailed,
			Tags:    []string{"ppg", "pr"},
			Project: "isv:percona:PR:pr-104:ppg:17",
			Package: "pg_tde",
			What:    "build failed",
			URL:     "https://build.opensuse.org/package/show/isv:percona:PR:pr-104:ppg:17/pg_tde",
			At:      now,
		},
		{
			ID:      "evt_pr_common",
			Type:    model.EventSucceeded,
			Tags:    []string{"common", "pr"},
			Project: "isv:percona:PR:pr-104:common",
			Package: "common_pkg",
			What:    "build succeeded",
			URL:     "https://build.opensuse.org/package/show/isv:percona:PR:pr-104:common/common_pkg",
			At:      now.Add(-time.Second),
		},
		{
			ID:      "evt_other_subproject",
			Type:    model.EventFailed,
			Project: "isv:percona:PR:pr-104:other:17",
			Package: "other_pkg",
			What:    "build failed",
			URL:     "https://build.opensuse.org/package/show/isv:percona:PR:pr-104:other:17/other_pkg",
			At:      now,
		},
	}
	for _, evt := range events {
		if err := AppendEvent(db, evt); err != nil {
			t.Fatal(err)
		}
	}

	got, err := QueryPRBuildEvents(db, "isv:percona", "pr-104", "ppg", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	ids := make(map[string]bool)
	for _, evt := range got {
		ids[evt.ID] = true
	}
	if !ids["evt_pr_ppg"] || !ids["evt_pr_common"] {
		t.Fatalf("expected PR ppg and common events, got %v", ids)
	}
	if ids["evt_other_subproject"] {
		t.Fatalf("unexpected event from another subproject: %v", ids)
	}
}
