package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS packages (
    project        TEXT NOT NULL,
    name           TEXT NOT NULL,
    scope          TEXT NOT NULL,
    rollup_state   TEXT NOT NULL,
    ok_targets     INTEGER NOT NULL DEFAULT 0,
    total_targets  INTEGER NOT NULL DEFAULT 0,
    trigger_what   TEXT,
    trigger_kind   TEXT,
    trigger_at     DATETIME,
    targets_json    TEXT NOT NULL DEFAULT '[]',
    updated_at      DATETIME NOT NULL,
    state_changed_at DATETIME,
    is_container   INTEGER,
    version        TEXT NOT NULL DEFAULT '',
    container_tags TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (project, name)
);

CREATE TABLE IF NOT EXISTS events (
    id       TEXT PRIMARY KEY,
    type     TEXT NOT NULL,
    scope    TEXT NOT NULL,
    project  TEXT NOT NULL,
    package  TEXT NOT NULL,
    repo     TEXT,
    arch     TEXT,
    what     TEXT NOT NULL,
    why      TEXT NOT NULL,
    url      TEXT NOT NULL,
    at       DATETIME NOT NULL,
    version  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS events_at ON events(at);

CREATE INDEX IF NOT EXISTS idx_packages_rollup_state ON packages(rollup_state);
`

// Open opens (or creates) the SQLite database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Additive migrations: add columns to existing databases.
	// Fails silently if the column already exists (fresh DBs have it from the schema above).
	db.Exec(`ALTER TABLE packages ADD COLUMN state_changed_at DATETIME`)
	db.Exec(`ALTER TABLE packages ADD COLUMN is_container INTEGER`)
	db.Exec(`ALTER TABLE packages ADD COLUMN version TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE events ADD COLUMN version TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE packages ADD COLUMN container_tags TEXT NOT NULL DEFAULT '[]'`)

	// Structural migration: make is_container nullable.
	// SQLite does not support ALTER COLUMN, so we recreate the table when the
	// column still carries its original NOT NULL constraint.
	var isContainerNotNull int
	if err := db.QueryRow(
		`SELECT "notnull" FROM pragma_table_info('packages') WHERE name = 'is_container'`,
	).Scan(&isContainerNotNull); err == nil && isContainerNotNull == 1 {
		if err := migrateIsContainerNullable(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate is_container nullable: %w", err)
		}
	}

	return db, nil
}

// migrateIsContainerNullable recreates the packages table without the NOT NULL
// constraint on is_container. Existing rows with is_container=1 keep their value;
// rows with 0 (the old default, meaning "never checked") are reset to NULL so the
// PackageTypeTask will re-detect them.
func migrateIsContainerNullable(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`DROP TABLE IF EXISTS packages_new`,
		`CREATE TABLE packages_new (
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
			is_container     INTEGER,
			version          TEXT NOT NULL DEFAULT '',
			container_tags   TEXT NOT NULL DEFAULT '[]',
			PRIMARY KEY (project, name)
		)`,
		`INSERT INTO packages_new
			SELECT project, name, scope, rollup_state, ok_targets, total_targets,
			       trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
			       state_changed_at,
			       CASE WHEN is_container = 1 THEN 1 ELSE NULL END,
			       version,
			       container_tags
			FROM packages`,
		`DROP TABLE packages`,
		`ALTER TABLE packages_new RENAME TO packages`,
		`CREATE INDEX IF NOT EXISTS idx_packages_rollup_state ON packages(rollup_state)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return tx.Commit()
}
