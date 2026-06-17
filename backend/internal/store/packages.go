package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// UpsertPackageState inserts or replaces a package row.
func UpsertPackageState(db *sql.DB, p *model.Package, now time.Time) error {
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
	var isContainerVal interface{}
	if p.IsContainer != nil {
		if *p.IsContainer {
			isContainerVal = 1
		} else {
			isContainerVal = 0
		}
	}
	_, err = db.Exec(`
		INSERT INTO packages
			(project, name, scope, rollup_state, ok_targets, total_targets,
			 trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
			 state_changed_at, is_container, version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project, name) DO UPDATE SET
			scope=excluded.scope, rollup_state=excluded.rollup_state,
			ok_targets=excluded.ok_targets, total_targets=excluded.total_targets,
			trigger_what=excluded.trigger_what, trigger_kind=excluded.trigger_kind,
			trigger_at=excluded.trigger_at, targets_json=excluded.targets_json,
			updated_at=excluded.updated_at,
			is_container=excluded.is_container,
			version=excluded.version,
			state_changed_at = CASE
				WHEN excluded.rollup_state != rollup_state THEN excluded.state_changed_at
				WHEN state_changed_at IS NULL             THEN excluded.state_changed_at
				ELSE state_changed_at
			END`,
		p.Project, p.Name, string(p.Scope), string(p.RollupState),
		p.OKTargets, p.TotalTargets,
		trigWhat, trigKind, trigAt,
		string(targetsJSON), p.UpdatedAt,
		now,
		isContainerVal, p.Version,
	)
	return err
}

// DeletePackagesByProject removes all package rows for an exact project name.
// Used by the poller to garbage-collect projects that no longer exist in OBS.
func DeletePackagesByProject(db *sql.DB, project string) error {
	_, err := db.Exec(`DELETE FROM packages WHERE project = ?`, project)
	return err
}

// DeletePackage removes a single package row.
// Used by the MQ consumer on package.delete events.
func DeletePackage(db *sql.DB, project, name string) error {
	_, err := db.Exec(`DELETE FROM packages WHERE project = ? AND name = ?`, project, name)
	return err
}

const packageSelectCols = ` project, name, scope, rollup_state, ok_targets, total_targets,
	trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
	state_changed_at, is_container, version`

// scanPackages is a helper that extracts the scan loop pattern used by multiple query functions.
// It expects rows to have been created with the standard package column order:
// project, name, scope, rollup_state, ok_targets, total_targets,
// trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at,
// is_container, version
func scanPackages(rows *sql.Rows) ([]*model.Package, error) {
	pkgs := make([]*model.Package, 0)
	for rows.Next() {
		p := &model.Package{}
		var trigWhat, trigKind sql.NullString
		var trigAt sql.NullTime
		var targetsJSON string
		var stateChangedAt sql.NullTime
		var isContainerNull sql.NullInt64
		if err := rows.Scan(
			&p.Project, &p.Name, &p.Scope, &p.RollupState,
			&p.OKTargets, &p.TotalTargets,
			&trigWhat, &trigKind, &trigAt,
			&targetsJSON, &p.UpdatedAt,
			&stateChangedAt, &isContainerNull, &p.Version,
		); err != nil {
			return nil, err
		}
		if isContainerNull.Valid {
			v := isContainerNull.Int64 != 0
			p.IsContainer = &v
		}
		if trigWhat.Valid {
			p.Trigger = &model.Trigger{
				What: trigWhat.String,
				Kind: trigKind.String,
				At:   trigAt.Time,
			}
		}
		if stateChangedAt.Valid {
			t := stateChangedAt.Time
			p.StateChangedAt = &t
		}
		if err := json.Unmarshal([]byte(targetsJSON), &p.Targets); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, rows.Err()
}

// QueryPackages returns all packages for a given OBS project prefix.
func QueryPackages(db *sql.DB, projectPrefix string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project LIKE ? ORDER BY project, name`,
		projectPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// GetActivePackages returns all packages where rollup_state is not 'succeeded'.
func GetActivePackages(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE rollup_state != 'succeeded' ORDER BY project, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// GetFinishedPackagesByProject returns succeeded packages for a project.
// Used by the MQ consumer on repo.published to signal packages for a publish
// state re-check via the worker pool.
func GetFinishedPackagesByProject(db *sql.DB, project string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? AND rollup_state = 'succeeded'`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}
