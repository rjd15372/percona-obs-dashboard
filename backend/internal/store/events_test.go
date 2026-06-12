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
		Scope:   model.ScopeVersion,
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
