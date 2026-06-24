package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// UpsertCveScan inserts or replaces one arch scan result.
func UpsertCveScan(db *sql.DB, project, pkg string, scan model.CveScan) error {
	findingsJSON, err := json.Marshal(scan.Findings)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT OR REPLACE INTO cve_scans
			(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		project, pkg, scan.Arch, scan.ImageRef,
		scan.ScannedAt.UTC().Format(time.RFC3339),
		scan.CriticalCount, scan.HighCount, string(findingsJSON),
	)
	return err
}

// QueryCveScans returns all arch scan results for a package.
func QueryCveScans(db *sql.DB, project, pkg string) ([]model.CveScan, error) {
	rows, err := db.Query(`
		SELECT arch, image_ref, scanned_at, critical_count, high_count, findings_json
		FROM cve_scans WHERE project = ? AND package = ?`,
		project, pkg,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCveRows(rows)
}

// AttachCveScans batch-fetches CVE scan results for a slice of packages and
// sets each package's CveScans field. One SQL query for all packages.
func AttachCveScans(db *sql.DB, packages []*model.Package) error {
	if len(packages) == 0 {
		return nil
	}
	keys := make([]interface{}, 0, len(packages))
	index := make(map[string]*model.Package, len(packages))
	for _, p := range packages {
		k := p.Project + "/" + p.Name
		if _, seen := index[k]; !seen {
			keys = append(keys, k)
			index[k] = p
		}
	}
	placeholders := make([]byte, 0, len(keys)*2)
	for i := range keys {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	rows, err := db.Query(
		`SELECT project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json
		 FROM cve_scans WHERE project || '/' || package IN (`+string(placeholders)+`)`,
		keys...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var project, pkg string
		var scan model.CveScan
		var scannedAtStr string
		var findingsJSON string
		if err := rows.Scan(&project, &pkg, &scan.Arch, &scan.ImageRef,
			&scannedAtStr, &scan.CriticalCount, &scan.HighCount, &findingsJSON); err != nil {
			return err
		}
		scan.ScannedAt, _ = time.Parse(time.RFC3339, scannedAtStr)
		if findingsJSON != "" && findingsJSON != "[]" {
			_ = json.Unmarshal([]byte(findingsJSON), &scan.Findings)
		}
		k := project + "/" + pkg
		if p, ok := index[k]; ok {
			p.CveScans = append(p.CveScans, scan)
		}
	}
	return rows.Err()
}

// QueryPublishedContainers returns all confirmed container packages with rollup_state='published'.
func QueryPublishedContainers(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT` + packageSelectCols + `
		FROM packages WHERE is_container = 1 AND rollup_state = 'published'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(rows)
}

// GetPackage returns a single package by primary key, or nil if not found.
func GetPackage(db *sql.DB, project, name string) (*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pkgs, err := scanPackages(rows)
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}
	return pkgs[0], nil
}

func scanCveRows(rows *sql.Rows) ([]model.CveScan, error) {
	var scans []model.CveScan
	for rows.Next() {
		var scan model.CveScan
		var scannedAtStr, findingsJSON string
		if err := rows.Scan(&scan.Arch, &scan.ImageRef, &scannedAtStr,
			&scan.CriticalCount, &scan.HighCount, &findingsJSON); err != nil {
			return nil, err
		}
		scan.ScannedAt, _ = time.Parse(time.RFC3339, scannedAtStr)
		if findingsJSON != "" && findingsJSON != "[]" {
			_ = json.Unmarshal([]byte(findingsJSON), &scan.Findings)
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}
