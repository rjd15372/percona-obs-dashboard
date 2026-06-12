package hub

import (
	"encoding/json"

	"github.com/percona/obs-dashboard/internal/model"
)

// Msg is the typed envelope sent over the SSE stream.
type Msg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// PackageUpdate serialises a package delta for the SSE stream.
func PackageUpdate(pkg *model.Package) []byte {
	d, _ := json.Marshal(pkg)
	out, _ := json.Marshal(Msg{Type: "package_update", Data: d})
	return out
}

// NewEvent serialises an event delta for the SSE stream.
func NewEvent(evt *model.Event) []byte {
	d, _ := json.Marshal(evt)
	out, _ := json.Marshal(Msg{Type: "new_event", Data: d})
	return out
}
