package obs

import (
	"context"
	"sync"
	"time"
)

// minuteLimiter is a fixed-window per-minute request budget. Background OBS
// requests acquire a slot before dispatch; once the window's budget is
// exhausted they block until the next clock minute opens. Requests are
// delayed, never dropped. A budget <= 0 disables limiting.
type minuteLimiter struct {
	mu     sync.Mutex
	budget int
	now    func() time.Time

	window time.Time // start of the current minute window
	used   int
	waits  int64 // cumulative count of acquisitions that had to block
}

func newMinuteLimiter(budget int) *minuteLimiter {
	return &minuteLimiter{budget: budget, now: time.Now}
}

// acquire blocks until a slot is available in the current minute window or
// ctx is cancelled.
func (l *minuteLimiter) acquire(ctx context.Context) error {
	if l.budget <= 0 {
		return nil
	}
	blocked := false
	for {
		l.mu.Lock()
		now := l.now()
		windowStart := now.Truncate(time.Minute)
		if !windowStart.Equal(l.window) {
			l.window = windowStart
			l.used = 0
		}
		if l.used < l.budget {
			l.used++
			l.mu.Unlock()
			return nil
		}
		if !blocked {
			blocked = true
			l.waits++
		}
		wait := windowStart.Add(time.Minute).Sub(now)
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// stats returns the cumulative number of blocked acquisitions and the
// remaining budget in the current window.
func (l *minuteLimiter) stats() (waits, remaining int64) {
	if l.budget <= 0 {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.now().Truncate(time.Minute).Equal(l.window) {
		return l.waits, int64(l.budget)
	}
	return l.waits, int64(l.budget - l.used)
}
