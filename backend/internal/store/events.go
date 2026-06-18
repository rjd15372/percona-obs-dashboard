package store

import (
	"database/sql"
	"encoding/json"
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

// QueryEvents returns events for a project prefix within [from, to], newest first.
func QueryEvents(db *sql.DB, projectPrefix string, from, to time.Time) ([]*model.Event, error) {
	rows, err := db.Query(`
		SELECT id, type, tags, project, package,
		       COALESCE(repo,''), COALESCE(arch,''),
		       what, why, url, at, COALESCE(version,'')
		FROM events
		WHERE project LIKE ? AND at >= ? AND at <= ?
		ORDER BY at DESC`,
		projectPrefix+"%", from, to,
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
