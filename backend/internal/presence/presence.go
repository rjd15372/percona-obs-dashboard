// Package presence tracks whether any dashboard client is connected so
// background OBS polling can pause while nobody is looking.
// Design: docs/superpowers/specs/2026-07-14-idle-mode-design.md
package presence

import (
	"log/slog"
	"sync"
	"time"
)

// Gate reports whether background polling should run and signals
// subscribers once per idle→active transition. Safe for concurrent use.
// A disabled Gate is permanently active and never signals.
type Gate struct {
	mu             sync.Mutex
	enabled        bool
	linger         time.Duration
	clients        int
	lastDisconnect time.Time
	now            func() time.Time // injectable for tests
	subs           []chan struct{}
	wasActive      bool // last observed state, for transition logging
}

// New returns a Gate. lastDisconnect starts at now (boot grace): a
// restarted backend runs one convergence window before idling, so
// post-redeploy reconciliation doesn't wait for the next visitor.
func New(enabled bool, linger time.Duration) *Gate {
	g := &Gate{enabled: enabled, linger: linger, now: time.Now, wasActive: true}
	g.lastDisconnect = g.now()
	return g
}

// Connect records a new SSE client and wakes subscribers on idle→active.
func (g *Gate) Connect() {
	if !g.enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	wasActive := g.activeLocked()
	g.clients++
	if !wasActive {
		g.wasActive = true
		slog.Info("presence: active — resuming background polling", "clients", g.clients)
		for _, ch := range g.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// Disconnect records a departing SSE client; the linger window starts when
// the last one leaves.
func (g *Gate) Disconnect() {
	if !g.enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clients > 0 {
		g.clients--
	}
	if g.clients == 0 {
		g.lastDisconnect = g.now()
	}
}

// Active reports whether background polling should run right now.
func (g *Gate) Active() bool {
	if !g.enabled {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	active := g.activeLocked()
	if !active && g.wasActive {
		slog.Info("presence: idle — background polling paused")
	}
	g.wasActive = active
	return active
}

func (g *Gate) activeLocked() bool {
	return g.clients > 0 || g.now().Sub(g.lastDisconnect) < g.linger
}

// Subscribe returns a buffered channel signaled once per idle→active
// transition. A disabled gate's channel never fires.
func (g *Gate) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	if !g.enabled {
		return ch
	}
	g.mu.Lock()
	g.subs = append(g.subs, ch)
	g.mu.Unlock()
	return ch
}
