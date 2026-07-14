package store

import (
	"database/sql"
	"time"
)

// metricsWindows are the trailing windows /api/metrics reports, tightest
// first. The last entry also bounds the table scan in QueryMetricsWindows
// and matches the default retention.
var metricsWindows = []struct {
	key string
	d   time.Duration
}{
	{"6h", 6 * time.Hour},
	{"12h", 12 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

// InsertMetricsSamples writes one row per op with the given counts at ts.
// Zero-count ops must be filtered by the caller. ts is stored as an
// RFC3339Nano UTC string, matching every other datetime column.
func InsertMetricsSamples(db *sql.DB, ts time.Time, deltas map[string]int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tsStr := ts.UTC().Format(time.RFC3339Nano)
	for op, count := range deltas {
		if _, err := tx.Exec(`INSERT INTO metrics_samples (ts, op, count) VALUES (?, ?, ?)`,
			tsStr, op, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryMetricsWindows returns summed request counts over the trailing
// windows, keyed "6h"/"12h"/"24h"/"7d"/"30d". The scan is bounded by the
// widest window.
func QueryMetricsWindows(db *sql.DB, now time.Time) (map[string]int64, error) {
	cutoff := func(d time.Duration) string {
		return now.Add(-d).UTC().Format(time.RFC3339Nano)
	}
	row := db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? THEN count END), 0),
		  COALESCE(SUM(count), 0)
		FROM metrics_samples
		WHERE ts > ?`,
		cutoff(metricsWindows[0].d), cutoff(metricsWindows[1].d),
		cutoff(metricsWindows[2].d), cutoff(metricsWindows[3].d),
		cutoff(metricsWindows[4].d),
	)
	sums := make([]int64, len(metricsWindows))
	if err := row.Scan(&sums[0], &sums[1], &sums[2], &sums[3], &sums[4]); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(metricsWindows))
	for i, wdef := range metricsWindows {
		out[wdef.key] = sums[i]
	}
	return out, nil
}

// PruneMetricsSamples deletes samples older than cutoff and returns how
// many rows were removed.
func PruneMetricsSamples(db *sql.DB, cutoff time.Time) (int64, error) {
	res, err := db.Exec(`DELETE FROM metrics_samples WHERE ts < ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
