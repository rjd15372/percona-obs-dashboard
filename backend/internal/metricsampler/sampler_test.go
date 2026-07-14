package metricsampler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

type fakeSnap struct{ m map[string]int64 }

func (f *fakeSnap) MetricsSnapshot() map[string]int64 {
	out := make(map[string]int64, len(f.m))
	for k, v := range f.m {
		out[k] = v
	}
	return out
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func rowsFor(t *testing.T, db *sql.DB, op string) (n int, total int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(count),0) FROM metrics_samples WHERE op = ?`, op).
		Scan(&n, &total); err != nil {
		t.Fatal(err)
	}
	return n, total
}

func TestSamplerDeltasAndZeroSuppression(t *testing.T) {
	db := testDB(t)
	snap := &fakeSnap{m: map[string]int64{"build_results": 10}}
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	s := &Sampler{DB: db, Snap: snap}
	s.now = func() time.Time { return cur }
	s.prev = snap.MetricsSnapshot() // boot baseline, as Run does

	// No change: no rows.
	s.sample()
	if n, _ := rowsFor(t, db, "build_results"); n != 0 {
		t.Fatalf("zero-delta tick wrote %d rows, want 0", n)
	}

	// +5 requests: one row with count 5.
	snap.m["build_results"] = 15
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db, "build_results"); n != 1 || total != 5 {
		t.Fatalf("delta tick: rows=%d total=%d, want 1/5", n, total)
	}

	// New op appears: its full count is the delta.
	snap.m["rebuild"] = 3
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db, "rebuild"); n != 1 || total != 3 {
		t.Fatalf("new-op tick: rows=%d total=%d, want 1/3", n, total)
	}
}

func TestSamplerFailedInsertAdvancesBaseline(t *testing.T) {
	db := testDB(t)
	snap := &fakeSnap{m: map[string]int64{"build_results": 10}}
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	s := &Sampler{DB: db, Snap: snap}
	s.now = func() time.Time { return cur }
	s.prev = snap.MetricsSnapshot()

	snap.m["build_results"] = 20
	db.Close() // force insert failure
	s.sample()

	// Reopen-equivalent: fresh DB, next delta must NOT include the lost 10.
	db2 := testDB(t)
	s.DB = db2
	snap.m["build_results"] = 22
	cur = cur.Add(5 * time.Minute)
	s.sample()
	if n, total := rowsFor(t, db2, "build_results"); n != 1 || total != 2 {
		t.Fatalf("after failed insert: rows=%d total=%d, want 1/2 (lost bucket not re-counted)", n, total)
	}
}

func TestSamplerRunStopsOnCancel(t *testing.T) {
	db := testDB(t)
	s := &Sampler{DB: db, Snap: &fakeSnap{m: map[string]int64{}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on cancel")
	}
}
