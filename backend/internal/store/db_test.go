package store

import (
	"path/filepath"
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

func TestOpenMigrationAppliesTagsAndIsRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Open fresh DB (gets full schema including new columns).
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert a row using the old-style scope='release' (simulating legacy data with default tags).
	_, err = db.Exec(`
		INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at)
		VALUES ('isv:percona:ppg:releases:17', 'pg_tde', 'release', 'succeeded', 1, 1, '[]', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	// Re-open: migrations should backfill tags and is_release.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer db2.Close()

	var tags string
	var isRelease int
	if err := db2.QueryRow(`SELECT tags, is_release FROM packages WHERE project='isv:percona:ppg:releases:17' AND name='pg_tde'`).
		Scan(&tags, &isRelease); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if tags != `["ppg","release"]` {
		t.Errorf("tags = %q, want [\"ppg\",\"release\"]", tags)
	}
	if isRelease != 1 {
		t.Errorf("is_release = %d, want 1", isRelease)
	}
}

func TestOpenTargetStateDurationsTableExists(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// target_state_durations table should exist.
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='target_state_durations'`).Scan(&name); err != nil {
		t.Errorf("target_state_durations table not found: %v", err)
	}

	// Index should exist.
	var indexName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_tsd_pkg'`).Scan(&indexName); err != nil {
		t.Errorf("idx_tsd_pkg index not found: %v", err)
	}
}
