package hub

import "sync"

// Presence is notified as SSE clients come and go so background polling
// can pause while nobody is watching. See internal/presence.
type Presence interface {
	Connect()
	Disconnect()
}

// Hub fans out SSE payloads to all registered clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan<- []byte]struct{}

	// Presence, when non-nil, is told about client arrivals/departures.
	// Set once at startup, before any Register/Unregister call.
	Presence Presence
}

func New() *Hub { return &Hub{clients: make(map[chan<- []byte]struct{})} }

func (h *Hub) Register(ch chan<- []byte) {
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	if h.Presence != nil {
		h.Presence.Connect()
	}
}

func (h *Hub) Unregister(ch chan<- []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	if h.Presence != nil {
		h.Presence.Disconnect()
	}
}

// Clients returns the number of currently registered SSE clients.
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Notify sends payload to every registered client.
// Callers must not retain or modify payload after this call returns.
// If a client's channel buffer is full the message is dropped for that
// client — the non-blocking select prevents Notify from stalling callers.
func (h *Hub) Notify(payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- payload:
		default:
		}
	}
}
