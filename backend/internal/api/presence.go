package api

import "net/http"

// Beater receives dashboard-tab heartbeats. See internal/presence.
type Beater interface{ Heartbeat() }

// presenceHandler handles POST /api/presence: a visible dashboard tab's
// periodic heartbeat, driving the idle-mode gate. Always responds 204.
func presenceHandler(gate Beater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gate.Heartbeat()
		w.WriteHeader(http.StatusNoContent)
	}
}
