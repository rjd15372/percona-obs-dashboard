package obs

import (
	"context"
	"testing"
	"time"
)

func TestLimiterAllowsWithinBudget(t *testing.T) {
	l := newMinuteLimiter(2)
	base := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l.now = func() time.Time { return base }

	for i := 0; i < 2; i++ {
		if err := l.acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
}

func TestLimiterBlocksWhenExhausted(t *testing.T) {
	l := newMinuteLimiter(1)
	base := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l.now = func() time.Time { return base }

	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Exhausted: a cancelled context must return immediately with an error
	// instead of sleeping until the next window.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.acquire(ctx); err == nil {
		t.Fatal("expected context error when budget exhausted")
	}
	waits, remaining := l.stats()
	if waits != 1 {
		t.Errorf("waits = %d, want 1", waits)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
}

func TestLimiterWindowRolloverResetsBudget(t *testing.T) {
	current := time.Date(2026, 7, 10, 12, 0, 10, 0, time.UTC)
	l := newMinuteLimiter(1)
	l.now = func() time.Time { return current }

	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	current = current.Add(time.Minute) // next window
	if err := l.acquire(context.Background()); err != nil {
		t.Fatalf("acquire after rollover: %v", err)
	}
}

func TestLimiterDisabledByZeroBudget(t *testing.T) {
	l := newMinuteLimiter(0)
	for i := 0; i < 100; i++ {
		if err := l.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInteractiveContextBypassesLimiter(t *testing.T) {
	ctx := Interactive(context.Background())
	if !IsInteractive(ctx) {
		t.Fatal("expected interactive context to be detected")
	}
	if IsInteractive(context.Background()) {
		t.Fatal("plain context must not be interactive")
	}
}
