package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
)

// ArtifactMetadataItem is a single item in a POST /api/artifacts/metadata request.
type ArtifactMetadataItem struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	Repo    string `json:"repo"` // empty = any repo (used for containers)
	Arch    string `json:"arch"` // empty = any arch (used for containers)
	Kind    string `json:"kind"` // "package" | "container"
}

// ArtifactMetadataResult is the per-item metadata response.
type ArtifactMetadataResult struct {
	Project  string           `json:"project"`
	Name     string           `json:"name"`
	Repo     string           `json:"repo"`
	Arch     string           `json:"arch"`
	Kind     string           `json:"kind"`
	BuiltAt  string           `json:"built_at,omitempty"`
	MTime    int64            `json:"mtime,omitempty"`
	Binaries []ArtifactBinary `json:"binaries,omitempty"`
}

type artifactMetadataResponse struct {
	Items []ArtifactMetadataResult `json:"items"`
}

// binaryListCache caches ProjectBinaryList results per project for a configurable TTL.
// It deduplicates concurrent requests for the same project using an inflight map,
// matching the pattern used by releaseArtifactsCache.
type binaryListCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]binaryListCacheEntry
	inflight map[string]chan struct{}
}

type binaryListCacheEntry struct {
	binaries []obs.BinaryArtifact
	expires  time.Time
	err      error
}

func newBinaryListCache(ttl time.Duration) *binaryListCache {
	return &binaryListCache{
		ttl:      ttl,
		entries:  map[string]binaryListCacheEntry{},
		inflight: map[string]chan struct{}{},
	}
}

func (c *binaryListCache) Get(ctx context.Context, key string, fetch func(context.Context) ([]obs.BinaryArtifact, error)) ([]obs.BinaryArtifact, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.binaries, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.binaries, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	binaries, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = binaryListCacheEntry{binaries: binaries, expires: expires, err: err}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return binaries, err
}

func artifactMetadataHandler(obsClient *obs.Client, cache *binaryListCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if obsClient == nil {
			http.Error(w, "OBS client not configured", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Items []ArtifactMetadataItem `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 {
			http.Error(w, "items must be a non-empty array", http.StatusBadRequest)
			return
		}

		// Deduplicate projects so we call ProjectBinaryList at most once per project.
		projects := map[string]struct{}{}
		for _, item := range req.Items {
			projects[item.Project] = struct{}{}
		}
		projectBinaries := make(map[string][]obs.BinaryArtifact, len(projects))
		for project := range projects {
			bins, err := cache.Get(r.Context(), project, func(ctx context.Context) ([]obs.BinaryArtifact, error) {
				return obsClient.ProjectBinaryList(ctx, project)
			})
			if err != nil {
				slog.Warn("artifact_metadata: binarylist fetch failed", "project", project, "err", err)
			}
			projectBinaries[project] = bins
		}

		results := make([]ArtifactMetadataResult, len(req.Items))
		for i, item := range req.Items {
			results[i] = resolveMetadataItem(item, projectBinaries[item.Project])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(artifactMetadataResponse{Items: results})
	}
}

// resolveMetadataItem matches a single request item against a project's binary list.
// For packages: collects distributable binaries matching name/repo/arch; built_at is
// max mtime among those. For containers: finds .containerinfo with highest mtime.
func resolveMetadataItem(item ArtifactMetadataItem, binaries []obs.BinaryArtifact) ArtifactMetadataResult {
	result := ArtifactMetadataResult{
		Project: item.Project,
		Name:    item.Name,
		Repo:    item.Repo,
		Arch:    item.Arch,
		Kind:    item.Kind,
	}

	if item.Kind == "container" {
		for _, b := range binaries {
			if b.Package != item.Name || !strings.HasSuffix(b.Filename, ".containerinfo") {
				continue
			}
			if b.MTime > result.MTime {
				result.MTime = b.MTime
				result.BuiltAt = b.BuiltAt.Format(time.RFC3339)
			}
		}
		return result
	}

	// kind == "package"
	for _, b := range binaries {
		if b.Package != item.Name {
			continue
		}
		if item.Repo != "" && b.Repo != item.Repo {
			continue
		}
		if item.Arch != "" && b.Arch != item.Arch {
			continue
		}
		if !obs.IsDistributableBinary(b.Filename) {
			continue
		}
		result.Binaries = append(result.Binaries, releaseBinary(b))
		if b.MTime > result.MTime {
			result.MTime = b.MTime
			result.BuiltAt = b.BuiltAt.Format(time.RFC3339)
		}
	}
	return result
}
