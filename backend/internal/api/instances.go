package api

import (
	"encoding/json"
	"net/http"

	"github.com/percona/obs-dashboard/internal/obs"
)

type instanceView struct {
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Root        string     `json:"root"`
	WebURL      string     `json:"web_url"`
	DownloadURL string     `json:"download_url"`
	Registry    string     `json:"registry"`
	Health      obs.Health `json:"health"`
}

// instancesHandler serves GET /api/instances: every configured instance's
// display name, URL bases and current health. root is what the client adds
// to a logical project name to reach the instance project.
func instancesHandler(fleet *obs.Fleet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fleet == nil {
			http.Error(w, "OBS client not configured", http.StatusServiceUnavailable)
			return
		}
		out := make([]instanceView, 0, len(fleet.Instances()))
		for _, in := range fleet.Instances() {
			out = append(out, instanceView{
				Name: in.Name, Slug: in.Slug, Root: in.PathRoot(),
				WebURL: in.WebURL, DownloadURL: in.InstanceInfo.DownloadURL, Registry: in.Registry,
				Health: in.Health(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
