package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
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

	// packages must not have a scope column
	if columnExists(db, "packages", "scope") {
		t.Error("packages.scope should not exist on fresh DB")
	}
	// events must have tags, not scope
	if !columnExists(db, "events", "tags") {
		t.Error("events.tags missing on fresh DB")
	}
	if columnExists(db, "events", "scope") {
		t.Error("events.scope should not exist on fresh DB")
	}
}

func TestSettledBackfillMarksTerminalPublishedRows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	insert := func(project, name, rollupState string, isContainer sql.NullInt64) {
		_, err := db.Exec(
			`INSERT INTO packages (project, name, rollup_state, is_container, settled, updated_at)
			 VALUES (?, ?, ?, ?, 0, datetime('now'))`,
			project, name, rollupState, isContainer,
		)
		if err != nil {
			t.Fatalf("insert %s/%s failed: %v", project, name, err)
		}
	}

	insert("p", "published-known", "published", sql.NullInt64{Int64: 1, Valid: true})
	insert("p", "published-unknown", "published", sql.NullInt64{})
	insert("p", "building", "building", sql.NullInt64{Int64: 1, Valid: true})

	if _, err := db.Exec(`UPDATE packages SET settled = 1 WHERE rollup_state = 'published' AND is_container IS NOT NULL`); err != nil {
		t.Fatalf("backfill exec failed: %v", err)
	}

	settled := func(name string) int {
		var s int
		if err := db.QueryRow(`SELECT settled FROM packages WHERE project = 'p' AND name = ?`, name).Scan(&s); err != nil {
			t.Fatalf("query settled for %s failed: %v", name, err)
		}
		return s
	}

	if got := settled("published-known"); got != 1 {
		t.Errorf("published-known settled = %d, want 1", got)
	}
	if got := settled("published-unknown"); got != 0 {
		t.Errorf("published-unknown settled = %d, want 0 (is_container IS NULL should not be backfilled)", got)
	}
	if got := settled("building"); got != 0 {
		t.Errorf("building settled = %d, want 0", got)
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

	// Simulate a legacy DB: create the old schema (with scope) directly,
	// insert a row, then run Open() to trigger the migration.
	{
		legacyDB, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("raw open: %v", err)
		}
		_, err = legacyDB.Exec(`
			CREATE TABLE IF NOT EXISTS packages (
				project          TEXT NOT NULL,
				name             TEXT NOT NULL,
				scope            TEXT NOT NULL,
				rollup_state     TEXT NOT NULL,
				ok_targets       INTEGER NOT NULL DEFAULT 0,
				total_targets    INTEGER NOT NULL DEFAULT 0,
				trigger_what     TEXT,
				trigger_kind     TEXT,
				trigger_at       DATETIME,
				targets_json     TEXT NOT NULL DEFAULT '[]',
				updated_at       DATETIME NOT NULL,
				state_changed_at DATETIME,
				is_container     INTEGER NOT NULL DEFAULT 0,
				version          TEXT NOT NULL DEFAULT '',
				container_tags   TEXT NOT NULL DEFAULT '[]',
				tags             TEXT NOT NULL DEFAULT '[]',
				is_release       INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (project, name)
			)
		`)
		if err != nil {
			legacyDB.Close()
			t.Fatalf("create legacy table: %v", err)
		}
		_, err = legacyDB.Exec(`
			INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at)
			VALUES ('isv:percona:ppg:releases:17', 'pg_tde', 'release', 'succeeded', 1, 1, '[]', datetime('now'))
		`)
		if err != nil {
			legacyDB.Close()
			t.Fatalf("insert legacy row: %v", err)
		}
		legacyDB.Close()
	}

	// Open via our Open() function: migrations should backfill tags and is_release.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var tags string
	var isRelease int
	if err := db.QueryRow(`SELECT tags, is_release FROM packages WHERE project='isv:percona:ppg:releases:17' AND name='pg_tde'`).
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
