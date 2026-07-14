package hub_test

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/hub"
)

func TestHub_NotifyFansOut(t *testing.T) {
	h := hub.New()
	ch1 := make(chan []byte, 1)
	ch2 := make(chan []byte, 1)
	h.Register((chan<- []byte)(ch1))
	h.Register((chan<- []byte)(ch2))

	msg := []byte(`{"type":"test"}`)
	h.Notify(msg)

	if got := <-ch1; string(got) != string(msg) {
		t.Errorf("ch1: got %s, want %s", got, msg)
	}
	if got := <-ch2; string(got) != string(msg) {
		t.Errorf("ch2: got %s, want %s", got, msg)
	}
}

func TestHub_UnregisterStopsDelivery(t *testing.T) {
	h := hub.New()
	ch := make(chan []byte, 1)
	h.Register((chan<- []byte)(ch))
	h.Unregister((chan<- []byte)(ch))

	h.Notify([]byte(`{"type":"test"}`))

	select {
	case got := <-ch:
		t.Errorf("received message after unregister: %s", got)
	default:
		// correct — nothing delivered
	}
}

func TestHub_SlowClientDoesNotBlock(t *testing.T) {
	h := hub.New()
	// unbuffered — a blocking send would deadlock
	slow := make(chan []byte)
	fast := make(chan []byte, 1)
	h.Register((chan<- []byte)(slow))
	h.Register((chan<- []byte)(fast))

	done := make(chan struct{})
	go func() {
		h.Notify([]byte(`{"type":"test"}`))
		close(done)
	}()
	<-done // must return without blocking on slow

	if len(fast) != 1 {
		t.Error("fast client did not receive the message")
	}
	if len(slow) != 0 {
		t.Error("slow client should have been dropped, not buffered")
	}
}

type stubPresence struct {
	connects, disconnects int
}

func (s *stubPresence) Connect()    { s.connects++ }
func (s *stubPresence) Disconnect() { s.disconnects++ }

func TestPresenceHook(t *testing.T) {
	h := hub.New()
	p := &stubPresence{}
	h.Presence = p

	ch := make(chan []byte, 1)
	h.Register(ch)
	h.Register(make(chan []byte, 1))
	h.Unregister(ch)

	if p.connects != 2 || p.disconnects != 1 {
		t.Fatalf("presence hook: connects=%d disconnects=%d, want 2/1", p.connects, p.disconnects)
	}
}

func TestNilPresenceSafe(t *testing.T) {
	h := hub.New() // Presence nil
	ch := make(chan []byte, 1)
	h.Register(ch)   // must not panic
	h.Unregister(ch) // must not panic
}
