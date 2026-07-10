package obs

import (
	"context"
	"testing"
	"time"
)

func TestMetricsSnapshotExcludesLimiterKeys(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(10)
	c.metrics.inc("build_results")

	snap := c.MetricsSnapshot()
	if _, ok := snap["limiter_remaining"]; ok {
		t.Fatalf("limiter_remaining leaked into snapshot: %v", snap)
	}
	if _, ok := snap["limiter_waits"]; ok {
		t.Fatalf("limiter_waits leaked into snapshot: %v", snap)
	}
	if snap["build_results"] != 1 {
		t.Fatalf("build_results = %d, want 1", snap["build_results"])
	}
}

func TestLimiterStatsDisabled(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	ls := c.LimiterStats()
	if ls.Enabled || ls.Budget != 0 || ls.Remaining != 0 || ls.Waits != 0 {
		t.Fatalf("disabled limiter stats = %+v, want zero value", ls)
	}
}

func TestLimiterStatsEnabled(t *testing.T) {
	c := NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(5)
	if err := c.limiter.acquire(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ls := c.LimiterStats()
	if !ls.Enabled || ls.Budget != 5 || ls.Remaining != 4 || ls.Waits != 0 {
		t.Fatalf("stats = %+v, want {Enabled:true Budget:5 Remaining:4 Waits:0}", ls)
	}
}

func TestRatePerSecondWindow(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	if got := m.ratePerSecond(); got != 0 {
		t.Fatalf("rate with no traffic = %v, want 0", got)
	}

	for i := 0; i < 120; i++ {
		m.inc("build_results")
	}
	if got := m.ratePerSecond(); got != 2.0 {
		t.Fatalf("rate = %v, want 2.0 (120 reqs / 60s)", got)
	}

	cur = base.Add(30 * time.Second)
	if got := m.ratePerSecond(); got != 2.0 {
		t.Fatalf("rate at +30s = %v, want 2.0 (still in window)", got)
	}

	cur = base.Add(61 * time.Second)
	if got := m.ratePerSecond(); got != 0 {
		t.Fatalf("rate at +61s = %v, want 0 (window slid past)", got)
	}
}

func TestRatePerSecondBucketReuse(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	cur := base
	m := &obsMetrics{counts: make(map[string]int64), now: func() time.Time { return cur }}

	m.inc("build_results")
	cur = base.Add(60 * time.Second) // same ring slot, different second
	m.inc("build_results")

	want := float64(1) / 60
	if got := m.ratePerSecond(); got != want {
		t.Fatalf("rate = %v, want %v (stale bucket must be zeroed on reuse)", got, want)
	}
}
