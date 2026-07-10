package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// AppendEvent inserts a new event row.
func AppendEvent(db *sql.DB, e *model.Event) error {
	tagsJSON, err := json.Marshal(e.Tags)
	if err != nil {
		return err
	}
	if tagsJSON == nil || string(tagsJSON) == "null" {
		tagsJSON = []byte("[]")
	}
	_, err = db.Exec(`
		INSERT INTO events (id, type, tags, project, package, repo, arch, what, why, url, at, version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, string(e.Type), string(tagsJSON),
		e.Project, e.Package, nullStr(e.Repo), nullStr(e.Arch),
		e.What, e.Why, e.URL, e.At, e.Version,
	)
	return err
}

// QueryEvents returns up to 500 events for a project prefix within [from, to], newest first.
func QueryEvents(db *sql.DB, projectPrefix string, from, to time.Time) ([]*model.Event, error) {
	rows, err := db.Query(`
		SELECT id, type, tags, project, package,
		       COALESCE(repo,''), COALESCE(arch,''),
		       what, why, url, at, COALESCE(version,'')
		FROM events
		WHERE project LIKE ? AND at >= ? AND at <= ?
		ORDER BY at DESC
		LIMIT 500`,
		projectPrefix+"%", from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEventRows(rows)
}

// QueryEventsAny returns events whose project falls under any of the given
// prefixes — same contract as QueryEvents (newest first, capped at 500) with
// an OR across prefixes. Used by the products events endpoint to include the
// shared common trees alongside the tier subtree.
func QueryEventsAny(db *sql.DB, prefixes []string, from, to time.Time) ([]*model.Event, error) {
	if len(prefixes) == 0 {
		return []*model.Event{}, nil
	}
	conds := make([]string, len(prefixes))
	args := make([]any, 0, len(prefixes)+2)
	for i, p := range prefixes {
		conds[i] = "project LIKE ?"
		args = append(args, p+"%")
	}
	args = append(args, from, to)
	rows, err := db.Query(`
		SELECT id, type, tags, project, package,
		       COALESCE(repo,''), COALESCE(arch,''),
		       what, why, url, at, COALESCE(version,'')
		FROM events
		WHERE (`+strings.Join(conds, " OR ")+`) AND at >= ? AND at <= ?
		ORDER BY at DESC
		LIMIT 500`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEventRows(rows)
}

// scanEventRows scans the shared event-row shape used by QueryEvents and
// QueryEventsAny into model.Event values.
func scanEventRows(rows *sql.Rows) ([]*model.Event, error) {
	events := make([]*model.Event, 0)
	for rows.Next() {
		e := &model.Event{}
		var tagsJSON string
		if err := rows.Scan(
			&e.ID, &e.Type, &tagsJSON, &e.Project, &e.Package,
			&e.Repo, &e.Arch, &e.What, &e.Why, &e.URL, &e.At, &e.Version,
		); err != nil {
			return nil, err
		}
		if tagsJSON != "" && tagsJSON != "[]" {
			_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// QueryPRBuildEvents returns events for all packages under a PR (every subproject),
// matching the whole-PR project prefix root:PR:<pr>.
func QueryPRBuildEvents(db *sql.DB, root, pr string, from, to time.Time) ([]*model.Event, error) {
	p := root + ":PR:" + pr
	rows, err := db.Query(`
		SELECT id, type, tags, project, package,
		       COALESCE(repo,''), COALESCE(arch,''),
		       what, why, url, at, COALESCE(version,'')
		FROM events
		WHERE at >= ? AND at <= ?
		  AND (project = ? OR project LIKE ? || ':%')
		ORDER BY at DESC
		LIMIT 500`,
		from, to, p, p,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]*model.Event, 0)
	for rows.Next() {
		e := &model.Event{}
		var tagsJSON string
		if err := rows.Scan(
			&e.ID, &e.Type, &tagsJSON, &e.Project, &e.Package,
			&e.Repo, &e.Arch, &e.What, &e.Why, &e.URL, &e.At, &e.Version,
		); err != nil {
			return nil, err
		}
		if tagsJSON != "" && tagsJSON != "[]" {
			_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// PruneEvents deletes events older than cutoff.
func PruneEvents(db *sql.DB, cutoff time.Time) error {
	_, err := db.Exec("DELETE FROM events WHERE at < ?", cutoff)
	return err
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
