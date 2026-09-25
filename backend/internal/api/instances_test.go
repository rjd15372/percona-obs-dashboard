package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/obs"
)

func TestInstancesHandler(t *testing.T) {
	fleet := obs.NewFleet(
		obs.LegacyInstance(nil, "isv:percona"),
		obs.NewInstance(obs.InstanceInfo{Name: "Percona", Slug: "percona", Root: "percona",
			WebURL: "https://obs.example.com", DownloadURL: "https://dl.example.com/repositories", Registry: "reg.example.com"}, nil),
	)
	rec := httptest.NewRecorder()
	instancesHandler(fleet)(rec, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
	var got []instanceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Slug != "opensuse" || got[0].Root != "" {
		t.Errorf("identity instance must expose empty root: %+v", got[0])
	}
	if got[1].Root != "percona" || got[1].WebURL != "https://obs.example.com" || got[1].Registry != "reg.example.com" {
		t.Errorf("instance 2: %+v", got[1])
	}
}

func TestInstancesAndMetricsNilFleet(t *testing.T) {
	rec := httptest.NewRecorder()
	instancesHandler(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("instances with nil fleet: status %d, want 503", rec.Code)
	}
	rec = httptest.NewRecorder()
	metricsHandler(nil, nil, nil, nil, nil)(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("metrics with nil fleet: status %d, want 503", rec.Code)
	}
}
