package store

import (
	"testing"
	"time"
)

func TestMetricsSamplesWindowsAndPrune(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	w, err := QueryMetricsWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"6h", "12h", "24h", "7d", "30d"} {
		if v, ok := w[k]; !ok || v != 0 {
			t.Fatalf("empty table: windows[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}

	seed := func(age time.Duration, op string, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{op: count}); err != nil {
			t.Fatal(err)
		}
	}
	// Inside/outside pairs around each window boundary.
	seed(5*time.Hour+59*time.Minute, "build_results", 1) // in 6h+
	seed(6*time.Hour+time.Minute, "build_results", 2)    // in 12h+, out of 6h
	seed(23*time.Hour+59*time.Minute, "version", 4)      // in 24h+
	seed(24*time.Hour+time.Minute, "version", 8)         // in 7d+, out of 24h
	seed(6*24*time.Hour+23*time.Hour, "rebuild", 16)     // in 7d+
	seed(7*24*time.Hour+time.Hour, "rebuild", 32)        // in 30d only
	seed(29*24*time.Hour, "publish_states", 64)          // in 30d only
	seed(31*24*time.Hour, "publish_states", 128)         // outside all windows

	w, err = QueryMetricsWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string]int64{
		"6h":  1,
		"12h": 1 + 2,
		"24h": 1 + 2 + 4,
		"7d":  1 + 2 + 4 + 8 + 16,
		"30d": 1 + 2 + 4 + 8 + 16 + 32 + 64,
	}
	for k, want := range expect {
		if w[k] != want {
			t.Errorf("windows[%q] = %d, want %d", k, w[k], want)
		}
	}

	// Prune at 30d: only the 31d row goes.
	n, err := PruneMetricsSamples(db, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("prune deleted %d rows, want 1", n)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metrics_samples`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 7 {
		t.Fatalf("remaining rows = %d, want 7", remaining)
	}
}
