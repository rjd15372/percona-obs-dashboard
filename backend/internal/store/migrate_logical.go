package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
)

// MigrateLogicalNames converts a single-instance database to root-free
// logical project names: it strips "<legacyRoot>:" from project columns and
// stamps slug on targets and target-level events that carry no instance.
// Idempotent; runs in one transaction.
func MigrateLogicalNames(db *sql.DB, legacyRoot, slug string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	prefix := legacyRoot + ":"
	for _, table := range []string{"packages", "events", "target_state_durations", "cve_scans", "cve_periods"} {
		res, err := tx.Exec(`UPDATE `+table+` SET project = substr(project, ?)
			WHERE substr(project, 1, ?) = ?`, len(prefix)+1, len(prefix), prefix)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Info("store: stripped root from project names", "table", table, "rows", n)
		}
	}

	rows, err := tx.Query(`SELECT project, name, targets_json FROM packages`)
	if err != nil {
		return err
	}
	type update struct{ project, name, targets string }
	var updates []update
	for rows.Next() {
		var project, name, raw string
		if err := rows.Scan(&project, &name, &raw); err != nil {
			rows.Close()
			return err
		}
		out, changed, err := stampMissingInstance(raw, slug)
		if err != nil {
			slog.Warn("store: skipping unparseable targets_json", "project", project, "name", name, "error", err)
			continue
		}
		if !changed {
			continue
		}
		updates = append(updates, update{project, name, out})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := tx.Exec(`UPDATE packages SET targets_json = ? WHERE project = ? AND name = ?`,
			u.targets, u.project, u.name); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE events SET instance = ?
		WHERE instance IS NULL AND repo IS NOT NULL AND repo != ''`, slug); err != nil {
		return err
	}
	return tx.Commit()
}

// stampMissingInstance appends `"instance":"<slug>"` to every target object
// in raw that carries no "instance" key, preserving the existing key order
// and byte layout of every other field so the migration is a minimal,
// reviewable diff of the stored JSON rather than a full re-marshal.
func stampMissingInstance(raw, slug string) (out string, changed bool, err error) {
	var targets []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return raw, false, err
	}
	stamp, err := json.Marshal(slug)
	if err != nil {
		return raw, false, err
	}
	for i, t := range targets {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(t, &probe); err != nil {
			continue
		}
		if _, ok := probe["instance"]; ok {
			continue
		}
		trimmed := bytes.TrimSuffix(bytes.TrimSpace(t), []byte("}"))
		trimmed = bytes.TrimRight(trimmed, " \t\n")
		buf := bytes.Buffer{}
		buf.Write(trimmed)
		if !bytes.HasSuffix(trimmed, []byte("{")) {
			buf.WriteByte(',')
		}
		buf.WriteString(`"instance":`)
		buf.Write(stamp)
		buf.WriteByte('}')
		targets[i] = buf.Bytes()
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	buf := bytes.Buffer{}
	buf.WriteByte('[')
	for i, t := range targets {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(t)
	}
	buf.WriteByte(']')
	return buf.String(), true, nil
}
