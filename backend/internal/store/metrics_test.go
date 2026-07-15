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

func TestQueryMetricsPrevWindows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	p, err := QueryMetricsPrevWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"6h", "12h", "24h", "7d"} {
		if v, ok := p[k]; !ok || v != 0 {
			t.Fatalf("empty table: prev[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}
	if _, ok := p["30d"]; ok {
		t.Fatalf("prev must not carry a 30d key")
	}

	seed := func(age time.Duration, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{"build_results": count}); err != nil {
			t.Fatal(err)
		}
	}

	seed(5*time.Hour+59*time.Minute, 1)   // current 6h window — no baseline
	seed(6*time.Hour+time.Minute, 2)      // 6h baseline (6h, 12h]
	seed(11*time.Hour+59*time.Minute, 4)  // 6h baseline, far edge
	seed(12*time.Hour+time.Minute, 8)     // 12h baseline (12h, 24h]
	seed(23*time.Hour+59*time.Minute, 16) // 12h baseline, far edge
	seed(24*time.Hour+time.Minute, 32)    // 24h baseline (24h, 48h]
	seed(47*time.Hour+59*time.Minute, 64) // 24h baseline, far edge
	seed(48*time.Hour+time.Minute, 128)   // gap: no baseline covers (48h, 7d]
	seed(7*24*time.Hour+time.Hour, 256)   // 7d baseline (7d, 14d]
	seed(14*24*time.Hour-time.Hour, 512)  // 7d baseline, far edge
	seed(14*24*time.Hour+time.Hour, 1024) // outside every baseline

	p, err = QueryMetricsPrevWindows(db, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"6h": 6, "12h": 24, "24h": 96, "7d": 768}
	for k, w := range want {
		if p[k] != w {
			t.Fatalf("prev[%q] = %d, want %d", k, p[k], w)
		}
	}
}

func TestQueryMetricsSeries(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	s, err := QueryMetricsSeries(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != SeriesBuckets {
		t.Fatalf("len(series) = %d, want %d", len(s), SeriesBuckets)
	}
	for i, v := range s {
		if v != 0 {
			t.Fatalf("empty table: series[%d] = %d, want 0", i, v)
		}
	}

	seed := func(age time.Duration, op string, count int64) {
		t.Helper()
		if err := InsertMetricsSamples(db, now.Add(-age), map[string]int64{op: count}); err != nil {
			t.Fatal(err)
		}
	}
	seed(2*time.Minute, "build_results", 3)               // newest bucket, slot 287
	seed(2*time.Minute, "project_meta", 11)               // same bucket, sums
	seed(7*time.Minute, "build_results", 5)               // slot 286
	seed(23*time.Hour+58*time.Minute, "build_results", 7) // oldest bucket, slot 0
	seed(24*time.Hour+time.Minute, "build_results", 13)   // older than 24h — excluded

	s, err = QueryMetricsSeries(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if s[287] != 14 || s[286] != 5 || s[0] != 7 {
		t.Fatalf("series slots = [287]=%d [286]=%d [0]=%d, want 14/5/7", s[287], s[286], s[0])
	}
	var total int64
	for _, v := range s {
		total += v
	}
	if total != 26 {
		t.Fatalf("series total = %d, want 26 (excluded row must not leak in)", total)
	}
}

func TestOldestMetricsSample(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := OldestMetricsSample(db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("empty table: oldest = %v, want zero time", got)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	earliest := now.Add(-8 * 24 * time.Hour)
	if err := InsertMetricsSamples(db, now.Add(-time.Hour), map[string]int64{"build_results": 1}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMetricsSamples(db, earliest, map[string]int64{"build_results": 2}); err != nil {
		t.Fatal(err)
	}

	got, err = OldestMetricsSample(db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(earliest) {
		t.Fatalf("oldest = %v, want %v", got, earliest)
	}
}
