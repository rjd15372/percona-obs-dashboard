package obs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetrySucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetryRetriesOnError(t *testing.T) {
	calls := 0
	sentinel := errors.New("transient")
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return sentinel
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success on third attempt, got: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	calls := 0
	sentinel := errors.New("permanent")
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected exactly 3 calls, got %d", calls)
	}
}

func TestWithRetryStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := withRetry(ctx, 5, time.Hour, func() error {
		calls++
		cancel()
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
	if calls != 1 {
		t.Errorf("expected 1 call before cancel, got %d", calls)
	}
}

func TestWithRetryExponentialDelay(t *testing.T) {
	calls := 0
	base := 10 * time.Millisecond
	start := time.Now()
	_ = withRetry(context.Background(), 3, base, func() error {
		calls++
		return errors.New("fail")
	})
	elapsed := time.Since(start)
	// 3 attempts: sleep after attempt 1 (10ms) and attempt 2 (20ms) = 30ms minimum
	if elapsed < 30*time.Millisecond {
		t.Errorf("expected at least 30ms elapsed for exponential backoff, got %v", elapsed)
	}
}
