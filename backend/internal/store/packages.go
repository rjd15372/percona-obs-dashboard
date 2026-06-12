package store

import (
	"database/sql"
	"encoding/json"

	"github.com/percona/obs-dashboard/internal/model"
)

// UpsertPackageState inserts or replaces a package row.
func UpsertPackageState(db *sql.DB, p *model.Package) error {
	targetsJSON, err := json.Marshal(p.Targets)
	if err != nil {
		return err
	}
	var trigWhat, trigKind sql.NullString
	var trigAt sql.NullTime
	if p.Trigger != nil {
		trigWhat = sql.NullString{String: p.Trigger.What, Valid: true}
		trigKind = sql.NullString{String: p.Trigger.Kind, Valid: true}
		trigAt = sql.NullTime{Time: p.Trigger.At, Valid: true}
	}
	_, err = db.Exec(`
		INSERT INTO packages
			(project, name, scope, rollup_state, ok_targets, total_targets,
			 trigger_what, trigger_kind, trigger_at, targets_json, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project, name) DO UPDATE SET
			scope=excluded.scope, rollup_state=excluded.rollup_state,
			ok_targets=excluded.ok_targets, total_targets=excluded.total_targets,
			trigger_what=excluded.trigger_what, trigger_kind=excluded.trigger_kind,
			trigger_at=excluded.trigger_at, targets_json=excluded.targets_json,
			updated_at=excluded.updated_at`,
		p.Project, p.Name, string(p.Scope), string(p.RollupState),
		p.OKTargets, p.TotalTargets,
		trigWhat, trigKind, trigAt,
		string(targetsJSON), p.UpdatedAt,
	)
	return err
}

// QueryPackages returns all packages for a given OBS project prefix.
func QueryPackages(db *sql.DB, projectPrefix string) ([]*model.Package, error) {
	rows, err := db.Query(`
		SELECT project, name, scope, rollup_state, ok_targets, total_targets,
		       trigger_what, trigger_kind, trigger_at, targets_json, updated_at
		FROM packages WHERE project LIKE ? ORDER BY project, name`,
		projectPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pkgs := make([]*model.Package, 0)
	for rows.Next() {
		p := &model.Package{}
		var trigWhat, trigKind sql.NullString
		var trigAt sql.NullTime
		var targetsJSON string
		if err := rows.Scan(
			&p.Project, &p.Name, &p.Scope, &p.RollupState,
			&p.OKTargets, &p.TotalTargets,
			&trigWhat, &trigKind, &trigAt,
			&targetsJSON, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if trigWhat.Valid {
			p.Trigger = &model.Trigger{
				What: trigWhat.String,
				Kind: trigKind.String,
				At:   trigAt.Time,
			}
		}
		if err := json.Unmarshal([]byte(targetsJSON), &p.Targets); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, rows.Err()
}
