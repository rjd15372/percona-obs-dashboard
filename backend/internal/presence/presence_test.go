package presence

import (
	"testing"
	"time"
)

func newTestGate(enabled bool, linger time.Duration, at *time.Time) *Gate {
	g := New(enabled, linger)
	g.now = func() time.Time { return *at }
	// Re-stamp boot grace with the fake clock.
	g.lastDisconnect = *at
	return g
}

func TestBootGraceThenIdle(t *testing.T) {
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)

	if !g.Active() {
		t.Fatal("fresh gate must be active (boot grace)")
	}
	cur = cur.Add(5*time.Minute + time.Second)
	if g.Active() {
		t.Fatal("gate must be idle after boot grace expires with no clients")
	}
}

func TestConnectDisconnectLinger(t *testing.T) {
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, 5*time.Minute, &cur)
	cur = cur.Add(10 * time.Minute) // past boot grace: idle

	g.Connect()
	if !g.Active() {
		t.Fatal("gate must be active with a connected client")
	}
	g.Disconnect()
	if !g.Active() {
		t.Fatal("gate must stay active during linger after last disconnect")
	}
	cur = cur.Add(5*time.Minute + time.Second)
	if g.Active() {
		t.Fatal("gate must be idle after linger expires")
	}
}

func TestWakeSignaledOncePerTransition(t *testing.T) {
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	g := newTestGate(true, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(2 * time.Minute) // idle

	g.Connect() // idle → active: signal
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on idle→active")
	}

	g.Connect() // second client while active: no signal
	select {
	case <-wake:
		t.Fatal("unexpected wake signal on connect while active")
	default:
	}

	g.Disconnect()
	g.Disconnect()
	cur = cur.Add(2 * time.Minute) // idle again
	if g.Active() {
		t.Fatal("expected idle after linger")
	}
	g.Connect() // second transition: signal again
	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal on second idle→active transition")
	}
}

func TestDisabledAlwaysActiveNeverSignals(t *testing.T) {
	cur := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	g := newTestGate(false, time.Minute, &cur)
	wake := g.Subscribe()
	cur = cur.Add(time.Hour)

	if !g.Active() {
		t.Fatal("disabled gate must always be active")
	}
	g.Connect()
	g.Disconnect()
	select {
	case <-wake:
		t.Fatal("disabled gate must never signal")
	default:
	}
}
