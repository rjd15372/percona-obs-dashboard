package store

import (
	"database/sql"

	"github.com/percona/obs-dashboard/internal/model"
)

// QueryProjectPackages returns the packages of exactly project (no
// subprojects). Used for instance-scoped project deletion.
func QueryProjectPackages(db *sql.DB, project string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? ORDER BY name`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
