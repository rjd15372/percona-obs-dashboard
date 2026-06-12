package hub

import "sync"

// Hub fans out SSE payloads to all registered clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan<- []byte]struct{}
}

func New() *Hub { return &Hub{clients: make(map[chan<- []byte]struct{})} }

func (h *Hub) Register(ch chan<- []byte) {
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) Unregister(ch chan<- []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// Notify sends payload to every registered client.
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
