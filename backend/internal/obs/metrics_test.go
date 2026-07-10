package obs

import (
	"context"
	"testing"
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
