package presence

import (
	"testing"
	"time"
)

func newTestGate(enabled bool, linger time.Duration, at *time.Time) *Gate {
	g := New(enabled, linger)
	g.now = func() time.Time { return *at }
	// Re-stamp boot grace with the fake clock.
	g.lastSeen = *at
	return g
}

func TestBootGraceThenIdle(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)

	if !g.Active() {
		t.Fatal("fresh gate must be active (boot grace)")
	}
	cur = cur.Add(5*time.Minute + time.Second)
	if g.Active() {
		t.Fatal("gate must be idle after boot grace expires with no heartbeats")
	}
}

func TestHeartbeatKeepsActiveThenExpires(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)
	cur = cur.Add(10 * time.Minute) // past boot grace: idle

	g.Heartbeat()
	if !g.Active() {
		t.Fatal("gate must be active right after a heartbeat")
	}
	cur = cur.Add(4 * time.Minute)
	g.Heartbeat() // refresh within the window
	cur = cur.Add(4 * time.Minute)
	if !g.Active() {
		t.Fatal("gate must stay active while heartbeats keep arriving")
	}
	cur = cur.Add(5 * time.Minute) // 9m since last beat > 5m linger
	if g.Active() {
		t.Fatal("gate must be idle after linger passes with no heartbeat")
	}
}

func TestWakeSignaledOncePerTransition(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(2 * time.Minute) // idle

	g.Heartbeat() // idle → active: signal
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on idle→active")
	}

	g.Heartbeat() // beat while active: no signal
	select {
	case <-wake:
		t.Fatal("unexpected wake signal on heartbeat while active")
	default:
	}

	cur = cur.Add(2 * time.Minute) // idle again
	if g.Active() {
		t.Fatal("expected idle after linger")
	}
	g.Heartbeat() // second transition: signal again
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on second idle→active transition")
	}
}

func TestState(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, time.Minute, &cur)
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active during boot grace", g.State())
	}
	cur = cur.Add(2 * time.Minute)
	if g.State() != "idle" {
		t.Fatalf("State() = %q, want idle after linger", g.State())
	}
	g.Heartbeat()
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active after heartbeat", g.State())
	}
}

func TestDisabledAlwaysActiveNeverSignals(t *testing.T) {
	cur := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	g := newTestGate(false, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(time.Hour)

	if !g.Active() {
		t.Fatal("disabled gate must always be active")
	}
	if g.State() != "active" {
		t.Fatalf("State() = %q, want active for disabled gate", g.State())
	}
	g.Heartbeat()
	select {
	case <-wake:
		t.Fatal("disabled gate must never signal")
	default:
	}
}
