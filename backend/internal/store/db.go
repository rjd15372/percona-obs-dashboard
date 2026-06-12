package store

import (
	"database/sql"
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
    targets_json   TEXT NOT NULL DEFAULT '[]',
    updated_at     DATETIME NOT NULL,
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
    at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS events_at ON events(at);
`

// Open opens (or creates) the SQLite database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
