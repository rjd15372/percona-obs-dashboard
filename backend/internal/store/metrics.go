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

// SeriesBuckets is the fixed length of the 24h request series: one bucket
// per 5 minutes.
const SeriesBuckets = 288

// QueryMetricsPrevWindows returns summed request counts over the previous
// adjacent periods of the trailing windows — (now-12h, now-6h],
// (now-24h, now-12h], (now-48h, now-24h] and (now-14d, now-7d] — keyed
// "6h"/"12h"/"24h"/"7d". There is no "30d" key: its baseline would need
// 60d of samples, beyond the default retention.
func QueryMetricsPrevWindows(db *sql.DB, now time.Time) (map[string]int64, error) {
	cutoff := func(d time.Duration) string {
		return now.Add(-d).UTC().Format(time.RFC3339Nano)
	}
	row := db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0),
		  COALESCE(SUM(CASE WHEN ts > ? AND ts <= ? THEN count END), 0)
		FROM metrics_samples
		WHERE ts > ?`,
		cutoff(12*time.Hour), cutoff(6*time.Hour),
		cutoff(24*time.Hour), cutoff(12*time.Hour),
		cutoff(48*time.Hour), cutoff(24*time.Hour),
		cutoff(14*24*time.Hour), cutoff(7*24*time.Hour),
		cutoff(14*24*time.Hour),
	)
	var s6, s12, s24, s7d int64
	if err := row.Scan(&s6, &s12, &s24, &s7d); err != nil {
		return nil, err
	}
	return map[string]int64{"6h": s6, "12h": s12, "24h": s24, "7d": s7d}, nil
}

// QueryMetricsSeries returns total requests per 5-minute bucket over the
// trailing 24h: a slice of exactly SeriesBuckets sums, oldest bucket
// first, missing buckets zero.
func QueryMetricsSeries(db *sql.DB, now time.Time) ([]int64, error) {
	rows, err := db.Query(`SELECT ts, count FROM metrics_samples WHERE ts > ?`,
		now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]int64, SeriesBuckets)
	for rows.Next() {
		var tsStr string
		var count int64
		if err := rows.Scan(&tsStr, &count); err != nil {
			return nil, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			return nil, err
		}
		idx := SeriesBuckets - 1 - int(now.Sub(ts)/(5*time.Minute))
		if idx < 0 || idx >= SeriesBuckets {
			continue
		}
		out[idx] += count
	}
	return out, rows.Err()
}

// OldestMetricsSample returns the earliest sample timestamp, or the zero
// time when the table is empty.
func OldestMetricsSample(db *sql.DB) (time.Time, error) {
	var tsStr sql.NullString
	if err := db.QueryRow(`SELECT MIN(ts) FROM metrics_samples`).Scan(&tsStr); err != nil {
		return time.Time{}, err
	}
	if !tsStr.Valid {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, tsStr.String)
}
