package store

import (
	"database/sql"
	"encoding/json"
	"sort"

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

// InstanceTargetCounts is the live target-state tally for one OBS instance.
type InstanceTargetCounts struct {
	Instance string
	OK       int
	Failing  int
	Building int
	Blocked  int
}

// QueryTargetCountsByInstance tallies current targets of non-release
// packages per instance, grouped for the Overview "By instance" card.
func QueryTargetCountsByInstance(db *sql.DB) ([]InstanceTargetCounts, error) {
	rows, err := db.Query(`SELECT targets_json FROM packages WHERE is_release = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*InstanceTargetCounts{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var targets []struct {
			State    string `json:"state"`
			Instance string `json:"instance"`
		}
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			continue
		}
		for _, t := range targets {
			c := by[t.Instance]
			if c == nil {
				c = &InstanceTargetCounts{Instance: t.Instance}
				by[t.Instance] = c
			}
			switch t.State {
			case "succeeded", "published":
				c.OK++
			case "failed", "broken", "unresolvable":
				c.Failing++
			case "building", "finished", "scheduled":
				c.Building++
			case "blocked":
				c.Blocked++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]InstanceTargetCounts, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}
