package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func TestDiff(t *testing.T) {
	prev := map[string]int64{"build_results": 10, "version": 5}
	cur := map[string]int64{"build_results": 14, "version": 5, "publish_states": 3}
	perOp, total := Diff(prev, cur)
	if total != 7 {
		t.Fatalf("total = %d, want 7", total)
	}
	if perOp["build_results"] != 4 || perOp["publish_states"] != 3 {
		t.Fatalf("perOp = %v", perOp)
	}
	if _, ok := perOp["version"]; ok {
		t.Fatalf("zero-delta op should be omitted: %v", perOp)
	}
}

type fakeStatter struct{ s workingset.Stats }

func (f fakeStatter) Stats() workingset.Stats { return f.s }

type fakeSnap struct{ m map[string]int64 }

func (f fakeSnap) MetricsSnapshot() map[string]int64 { return f.m }

func TestTickRefreshesBaselineWhenDisabled(t *testing.T) {
	var enabled atomic.Bool // false
	r := &Reporter{
		Stats:   fakeStatter{s: workingset.Stats{Total: 3}},
		Snap:    fakeSnap{m: map[string]int64{"build_results": 100}},
		Enabled: &enabled,
	}
	prev := map[string]int64{"build_results": 90}
	newPrev := r.tick(prev)
	if newPrev["build_results"] != 100 {
		t.Fatalf("baseline not refreshed while disabled: %v", newPrev)
	}
}

func TestRunNoPanicOnZeroInterval(t *testing.T) {
	var enabled atomic.Bool
	r := &Reporter{Interval: 0, Enabled: &enabled, Stats: fakeStatter{}, Snap: fakeSnap{m: map[string]int64{}}}
	done := make(chan struct{})
	go func() { r.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return on zero interval")
	}
}

type fakeLimiter struct{ ls obs.LimiterStats }

func (f fakeLimiter) LimiterStats() obs.LimiterStats { return f.ls }

func captureTick(t *testing.T, r *Reporter) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	r.tick(map[string]int64{})
	return buf.String()
}

func TestTickLogsLimiterGauges(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
		Limiter:  fakeLimiter{ls: obs.LimiterStats{Enabled: true, Budget: 60, Remaining: 41, Waits: 17}},
	}
	out := captureTick(t, r)
	if !strings.Contains(out, "limiter_remaining=41") || !strings.Contains(out, "limiter_waits=17") {
		t.Fatalf("limiter gauges missing from log line: %q", out)
	}
}

func TestTickOmitsLimiterWhenDisabled(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
		Limiter:  fakeLimiter{}, // zero value: Enabled false
	}
	out := captureTick(t, r)
	if strings.Contains(out, "limiter_") {
		t.Fatalf("limiter fields present despite disabled limiter: %q", out)
	}
}

func TestTickNilLimiterNoPanic(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	r := &Reporter{
		Interval: time.Minute,
		Enabled:  &enabled,
		Stats:    fakeStatter{},
		Snap:     fakeSnap{m: map[string]int64{}},
	}
	_ = captureTick(t, r) // must not panic
}
