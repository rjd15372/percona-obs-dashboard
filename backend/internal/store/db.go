package store

import (
	"database/sql"
	"encoding/json"
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
    tags           TEXT NOT NULL DEFAULT '[]',
    is_release     INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS target_state_durations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project     TEXT     NOT NULL,
    package     TEXT     NOT NULL,
    repo        TEXT     NOT NULL,
    arch        TEXT     NOT NULL,
    state       TEXT     NOT NULL,
    entered_at  DATETIME NOT NULL,
    exited_at   DATETIME,
    duration_ms INTEGER
);

CREATE INDEX IF NOT EXISTS idx_tsd_pkg ON target_state_durations (project, package);
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
	db.Exec(`ALTER TABLE packages ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'`)
	db.Exec(`ALTER TABLE packages ADD COLUMN is_release INTEGER NOT NULL DEFAULT 0`)

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

	// Data migrations: backfill derived columns.
	if err := migrateTagsAndIsRelease(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate tags and is_release: %w", err)
	}
	if err := migrateSucceededToPublished(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate succeeded to published: %w", err)
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
			tags             TEXT NOT NULL DEFAULT '[]',
			is_release       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (project, name)
		)`,
		`INSERT INTO packages_new
			SELECT project, name, scope, rollup_state, ok_targets, total_targets,
			       trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
			       state_changed_at,
			       CASE WHEN is_container = 1 THEN 1 ELSE NULL END,
			       version,
			       container_tags,
			       tags,
			       is_release
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

// migrateTagsAndIsRelease backfills tags JSON and is_release from the scope column.
// Idempotent: only updates rows where tags is still the default '[]'.
func migrateTagsAndIsRelease(db *sql.DB) error {
	_, err := db.Exec(`
		UPDATE packages SET tags = CASE
			WHEN scope = 'version'                               THEN '["ppg"]'
			WHEN scope = 'pr'                                    THEN '["ppg","pr"]'
			WHEN scope = 'ppgcommon'                             THEN '["ppg","common"]'
			WHEN scope = 'common'                                THEN '["common"]'
			WHEN scope = 'release'                               THEN '["ppg","release"]'
			WHEN scope = 'container' AND project LIKE '%:PR:%'  THEN '["ppg","pr"]'
			WHEN scope = 'container'                             THEN '["ppg"]'
			ELSE '[]'
		END
		WHERE tags = '[]'
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE packages SET is_release = 1 WHERE scope = 'release' AND is_release = 0`)
	return err
}

// migrateSucceededToPublished promotes packages where rollup_state = 'succeeded'
// and every target in targets_json already has published=true to rollup_state = 'published'.
// Idempotent: only processes rows still at 'succeeded'.
func migrateSucceededToPublished(db *sql.DB) error {
	rows, err := db.Query(`SELECT project, name, targets_json FROM packages WHERE rollup_state = 'succeeded'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type candidate struct{ project, name, targetsJSON string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.project, &c.name, &c.targetsJSON); err != nil {
			return err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, c := range candidates {
		var targets []struct {
			State     string `json:"state"`
			Published bool   `json:"published"`
		}
		if err := json.Unmarshal([]byte(c.targetsJSON), &targets); err != nil {
			continue
		}
		if len(targets) == 0 {
			continue
		}
		allPublished := true
		for _, t := range targets {
			if t.State == "succeeded" && !t.Published {
				allPublished = false
				break
			}
		}
		if allPublished {
			db.Exec(`UPDATE packages SET rollup_state = 'published' WHERE project = ? AND name = ?`,
				c.project, c.name)
		}
	}
	return nil
}
