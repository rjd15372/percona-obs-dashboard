package store

import (
	"testing"
)

func TestOpen(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Verify tables exist
	for _, table := range []string{"packages", "events"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	// Verify index exists
	var idx string
	if err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='events_at'",
	).Scan(&idx); err != nil {
		t.Errorf("events_at index not found: %v", err)
	}
}

func TestOpenIdempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	db2, err := Open(":memory:")
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	db2.Close()
}
