package store

import (
	"database/sql"
	"time"
)

// BuildCompletion is one target entering a build-completion state —
// "finished" (successful or unchanged build) or "failed" — the Overview's
// unit of "one rebuild". The MQ consumer's merge writes exactly one such
// transition per completed build at event time, so the count is
// polling-independent (build starts are only observable by polling and
// vanish while the idle gate pauses it).
type BuildCompletion struct {
	Project string
	Package string
	Repo    string
}

// QueryBuildCompletions returns every target_state_durations row that
// entered "finished" or "failed" within [since, until). Timestamps in the
// table are RFC3339Nano strings (UTC); lexicographic comparison is
// chronologically correct to within sub-second edge cases (a whole-second
// timestamp sorts after the same second with a fractional part), which is
// negligible for window counting.
func QueryBuildCompletions(db *sql.DB, since, until time.Time) ([]BuildCompletion, error) {
	rows, err := db.Query(`
		SELECT project, package, repo FROM target_state_durations
		WHERE state IN ('finished', 'failed') AND entered_at >= ? AND entered_at < ?`,
		since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuildCompletion
	for rows.Next() {
		var e BuildCompletion
		if err := rows.Scan(&e.Project, &e.Package, &e.Repo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OverviewCveScan is one arch scan row for the Overview aggregation.
type OverviewCveScan struct {
	Project  string
	Package  string
	Arch     string
	Critical int
	High     int
	CveSince *time.Time // nil for clean images or pre-age-tracking rows
}

// QueryAllCveScans returns every cve_scans row (counts + open-since).
func QueryAllCveScans(db *sql.DB) ([]OverviewCveScan, error) {
	rows, err := db.Query(`
		SELECT project, package, arch, critical_count, high_count, cve_since FROM cve_scans`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverviewCveScan
	for rows.Next() {
		var s OverviewCveScan
		var since sql.NullString
		if err := rows.Scan(&s.Project, &s.Package, &s.Arch, &s.Critical, &s.High, &since); err != nil {
			return nil, err
		}
		if since.Valid {
			if t, err := parseRFC3339(since.String); err == nil {
				s.CveSince = &t
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OverviewCvePeriod is one closed CVE episode.
type OverviewCvePeriod struct {
	Project    string
	Package    string
	CveSince   time.Time
	CleanSince time.Time
}

// QueryAllCvePeriods returns every closed CVE episode.
func QueryAllCvePeriods(db *sql.DB) ([]OverviewCvePeriod, error) {
	rows, err := db.Query(`SELECT project, package, cve_since, clean_since FROM cve_periods`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverviewCvePeriod
	for rows.Next() {
		var p OverviewCvePeriod
		var cs, cl string
		if err := rows.Scan(&p.Project, &p.Package, &cs, &cl); err != nil {
			return nil, err
		}
		p.CveSince, _ = parseRFC3339(cs)
		p.CleanSince, _ = parseRFC3339(cl)
		out = append(out, p)
	}
	return out, rows.Err()
}
