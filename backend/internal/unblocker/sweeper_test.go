package unblocker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

type fakeRebuilder struct {
	calls []string
	err   error
}

func (f *fakeRebuilder) Rebuild(_ context.Context, project, repo, arch, pkg string) error {
	f.calls = append(f.calls, project+"/"+pkg+"/"+repo+"/"+arch)
	return f.err
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

func seedBlocked(t *testing.T, db *sql.DB, project, pkg string, enteredAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, is_release, updated_at)
		VALUES (?, ?, 'blocked', 0, 0, '[]', 0, ?)`, project, pkg, enteredAt); err != nil {
		t.Fatal(err)
	}
	// entered_at seeded as the production write path stores it: a
	// pre-formatted RFC3339Nano string (raw time.Time would be rendered
	// in an incompatible format by the driver).
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES (?, ?, 'Fedora_42', 'x86_64', 'blocked', ?)`, project, pkg, enteredAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

// closeEpisode marks the target's current blocked episode as exited and
// opens a new one entered at newEnteredAt (simulating blocked→…→blocked).
// Timestamps are seeded as pre-formatted RFC3339Nano strings, matching the
// production write path.
func closeEpisode(t *testing.T, db *sql.DB, project, pkg string, newEnteredAt time.Time) {
	t.Helper()
	ts := newEnteredAt.UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE target_state_durations SET exited_at = ? WHERE project = ? AND package = ? AND exited_at IS NULL`,
		ts, project, pkg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES (?, ?, 'Fedora_42', 'x86_64', 'blocked', ?)`, project, pkg, ts); err != nil {
		t.Fatal(err)
	}
}

func newTestSweeper(db *sql.DB, rb Rebuilder, at *time.Time) *Sweeper {
	return &Sweeper{
		DB:        db,
		Rebuilder: rb,
		Threshold: 30 * time.Minute,
		now:       func() time.Time { return *at },
	}
}

func TestSweepTriggersStaleAndPacesRetries(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	s.sweep(context.Background())
	if len(rb.calls) != 1 {
		t.Fatalf("first sweep: %d calls, want 1", len(rb.calls))
	}

	// 5 minutes later (next tick): still blocked, but within pacing window.
	cur = cur.Add(5 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 1 {
		t.Fatalf("paced sweep retriggered: %d calls, want still 1", len(rb.calls))
	}

	// Threshold later: second attempt fires.
	cur = cur.Add(30 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 2 {
		t.Fatalf("after pacing window: %d calls, want 2", len(rb.calls))
	}
}

func TestSweepAttemptCapAndEpisodeReset(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	for i := 0; i < 6; i++ { // far more sweeps than allowed attempts
		s.sweep(context.Background())
		cur = cur.Add(35 * time.Minute) // always past the pacing window
	}
	if len(rb.calls) != 3 {
		t.Fatalf("attempt cap: %d calls, want 3", len(rb.calls))
	}

	// New episode (state changed and re-blocked): count resets.
	closeEpisode(t, db, "isv:percona:ppg:devel:17", "stuck", cur)
	cur = cur.Add(45 * time.Minute)
	s.sweep(context.Background())
	if len(rb.calls) != 4 {
		t.Fatalf("episode reset: %d calls, want 4", len(rb.calls))
	}
}

func TestSweepPerSweepCap(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	// Staggered entered_at so ORDER BY entered_at gives a deterministic
	// iteration order (pkg00 oldest … pkg10 newest); all far past cutoff.
	for i := 0; i < 11; i++ {
		seedBlocked(t, db, "isv:percona:ppg:devel:17", fmt.Sprintf("pkg%02d", i),
			base.Add(time.Duration(i)*time.Second))
	}

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{}
	s := newTestSweeper(db, rb, &cur)

	s.sweep(context.Background())
	if len(rb.calls) != 10 {
		t.Fatalf("per-sweep cap: %d calls, want 10", len(rb.calls))
	}
	// The cap admits 10 per sweep in entered_at order, so pkg00-09 keep
	// winning the slots until they cap out at 3 attempts each (sweeps 2-3),
	// then the straggler pkg10 gets its 3 paced attempts (sweeps 4-6).
	// Cumulative: 10, 20, 30, 31, 32, 33.
	for _, want := range []int{20, 30, 31, 32, 33} {
		cur = cur.Add(35 * time.Minute)
		s.sweep(context.Background())
		if len(rb.calls) != want {
			t.Fatalf("cumulative calls = %d, want %d", len(rb.calls), want)
		}
	}
	seen := make(map[string]bool)
	for _, c := range rb.calls {
		seen[c] = true
	}
	if len(seen) != 11 {
		t.Fatalf("distinct targets triggered = %d, want all 11", len(seen))
	}
}

func TestSweepFailedRebuildCountsAsAttempt(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	seedBlocked(t, db, "isv:percona:ppg:devel:17", "stuck", base)

	cur := base.Add(45 * time.Minute)
	rb := &fakeRebuilder{err: errors.New("obs unavailable")}
	s := newTestSweeper(db, rb, &cur)

	for i := 0; i < 5; i++ {
		s.sweep(context.Background())
		cur = cur.Add(35 * time.Minute)
	}
	if len(rb.calls) != 3 {
		t.Fatalf("failed rebuilds must still cap at 3 attempts, got %d calls", len(rb.calls))
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	db := testDB(t)
	rb := &fakeRebuilder{}
	s := &Sweeper{DB: db, Rebuilder: rb, Threshold: 30 * time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
}
