package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
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

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Seed: 7 requests 1h ago (in all windows), 9 requests 3 days ago
	// (7d/30d only).
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-time.Hour),
		map[string]int64{"build_results": 7}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-3*24*time.Hour),
		map[string]int64{"build_results": 9}); err != nil {
		t.Fatal(err)
	}
	// 5 requests 8h ago: inside the 6h baseline (6h, 12h] and the 12h/24h
	// windows. 20 requests 8 days ago: inside the 7d baseline (7d, 14d]
	// and the 30d window only.
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-8*time.Hour),
		map[string]int64{"build_results": 5}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertMetricsSamples(db, time.Now().UTC().Add(-8*24*time.Hour),
		map[string]int64{"build_results": 20}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	metricsHandler(c, ws, fakeClientCounter{n: 3}, db)(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

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
	wantWindows := map[string]int64{"6h": 7, "12h": 12, "24h": 12, "7d": 21, "30d": 41}
	for k, want := range wantWindows {
		if got.OBS.Windows[k] != want {
			t.Fatalf("obs.windows[%q] = %d, want %d", k, got.OBS.Windows[k], want)
		}
	}
	wantPrev := map[string]int64{"6h": 5, "12h": 0, "24h": 0, "7d": 20}
	if len(got.OBS.WindowsPrev) != len(wantPrev) {
		t.Fatalf("obs.windows_prev = %v, want exactly keys of %v", got.OBS.WindowsPrev, wantPrev)
	}
	for k, want := range wantPrev {
		if got.OBS.WindowsPrev[k] != want {
			t.Fatalf("obs.windows_prev[%q] = %d, want %d", k, got.OBS.WindowsPrev[k], want)
		}
	}
	if len(got.OBS.Series) != store.SeriesBuckets {
		t.Fatalf("len(obs.series) = %d, want %d", len(got.OBS.Series), store.SeriesBuckets)
	}
	if got.OBS.Series[275] != 7 || got.OBS.Series[191] != 5 {
		t.Fatalf("obs.series[275]=%d obs.series[191]=%d, want 7 and 5",
			got.OBS.Series[275], got.OBS.Series[191])
	}
	if got.OBS.OldestSample == "" {
		t.Fatalf("obs.oldest_sample empty, want RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, got.OBS.OldestSample); err != nil {
		t.Fatalf("obs.oldest_sample %q not RFC3339: %v", got.OBS.OldestSample, err)
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
	for _, k := range []string{"windows_prev", "series", "oldest_sample"} {
		if _, ok := raw["obs"].(map[string]any)[k]; !ok {
			t.Fatalf("obs.%s key missing from raw response", k)
		}
	}
}
