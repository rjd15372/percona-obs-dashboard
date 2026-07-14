package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/workingset"
)

type fakeStatter struct{ s workingset.Stats }

func (f fakeStatter) Stats() workingset.Stats { return f.s }

type fakeClientCounter struct{ n int }

func (f fakeClientCounter) Clients() int { return f.n }

func TestMetricsHandler(t *testing.T) {
	c := obs.NewClient("https://obs.example", "u", "p")
	c.SetMinuteBudget(60)
	ws := fakeStatter{s: workingset.Stats{
		Total:    214,
		Inflight: 3,
		ByState:  map[string]int{"succeeded": 180, "building": 20},
	}}

	rec := httptest.NewRecorder()
	metricsHandler(c, ws, fakeClientCounter{n: 3})(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got metricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OBS.Total != 0 || got.OBS.ReqPerS != 0 {
		t.Fatalf("obs = %+v, want zero total and rate with no traffic", got.OBS)
	}
	if got.OBS.ByEndpoint == nil {
		t.Fatalf("obs.by_endpoint must be a map, got null")
	}
	if got.OBS.Windows == nil {
		t.Fatalf("obs.windows must be a map, got null")
	}
	for _, k := range []string{"6h", "12h", "24h"} {
		if v, ok := got.OBS.Windows[k]; !ok || v != 0 {
			t.Fatalf("obs.windows[%q] = %d (present=%v), want 0 present", k, v, ok)
		}
	}
	if !got.Limiter.Enabled || got.Limiter.Budget != 60 || got.Limiter.Remaining != 60 || got.Limiter.Waits != 0 {
		t.Fatalf("limiter = %+v, want {enabled:true budget:60 remaining:60 waits:0}", got.Limiter)
	}
	if got.WorkingSet.Packages != 214 || got.WorkingSet.Inflight != 3 || got.WorkingSet.ByState["succeeded"] != 180 {
		t.Fatalf("working_set = %+v", got.WorkingSet)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	up, ok := raw["uptime_seconds"].(float64)
	if !ok || up < 0 {
		t.Fatalf("uptime_seconds = %v (present=%v), want number >= 0", raw["uptime_seconds"], ok)
	}
	if got.SSEClients != 3 {
		t.Fatalf("sse_clients = %d, want 3", got.SSEClients)
	}
	if _, ok := raw["sse_clients"]; !ok {
		t.Fatalf("sse_clients key missing from raw response")
	}
}
