package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// logicalProject maps a raw OBS project to the Overview row it belongs to:
// dev version roots absorb their :containers:* subprojects; :extras is its own
// row (absorbing its subtree); the common trees, the releases tree, and each PR
// collapse to one row each. Unknown shapes return "" (excluded).
func logicalProject(root, project string) string {
	prefix := root + ":"
	if !strings.HasPrefix(project, prefix) {
		return ""
	}
	rel := strings.Split(project[len(prefix):], ":")
	switch rel[0] {
	case "PR":
		if len(rel) >= 2 {
			return root + ":PR:" + rel[1]
		}
		return ""
	case "common":
		return root + ":common"
	case "ppg":
		if len(rel) < 2 {
			return ""
		}
		switch rel[1] {
		case "common":
			return root + ":ppg:common"
		case "releases":
			return root + ":ppg:releases"
		default:
			if len(rel) >= 3 && rel[2] == "extras" {
				return root + ":ppg:" + rel[1] + ":extras"
			}
			return root + ":ppg:" + rel[1]
		}
	}
	return ""
}

// ── snapshot types (snake_case JSON, app convention) ──

type OverviewCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type OverviewImage struct {
	Project        string `json:"project"` // raw OBS project (logical rows can aggregate several)
	Name           string `json:"name"`
	Critical       int    `json:"critical"`
	High           int    `json:"high"`
	OldestOpenDays int    `json:"oldest_open_days"` // 0 = none open / unknown
	AvgFixDays     int    `json:"avg_fix_days"`     // 0 = no closed episodes yet
}

type OverviewProject struct {
	Project    string          `json:"project"`
	Rebuilds   int             `json:"rebuilds"`
	TopPackage *OverviewCount  `json:"top_package,omitempty"`
	Images     []OverviewImage `json:"images"`
}

type OverviewSnapshot struct {
	Window                     string            `json:"window"`
	GeneratedAt                string            `json:"generated_at"`
	PreviousWindowRebuildTotal int               `json:"previous_window_rebuild_total"`
	TopRepo                    *OverviewCount    `json:"top_repo,omitempty"`
	Projects                   []OverviewProject `json:"projects"`
}

var overviewWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"48h": 48 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// buildOverviewSnapshot assembles the snapshot from raw store rows.
// Aggregation rules: rebuilds/top_package per logical project, top_repo global,
// per-image CVE counts as max across archs, oldest_open_days from the oldest
// non-nil CveSince among vulnerable archs, avg_fix_days as the rounded mean of
// the image's closed episodes.
func buildOverviewSnapshot(root, window string, now time.Time,
	cur, prev []store.BuildingEntry, scans []store.OverviewCveScan, periods []store.OverviewCvePeriod,
) OverviewSnapshot {
	type projAgg struct {
		rebuilds int
		pkgCount map[string]int
		images   map[string]*OverviewImage
	}
	agg := map[string]*projAgg{}
	getAgg := func(logical string) *projAgg {
		a, ok := agg[logical]
		if !ok {
			a = &projAgg{pkgCount: map[string]int{}, images: map[string]*OverviewImage{}}
			agg[logical] = a
		}
		return a
	}

	repoCount := map[string]int{}
	for _, e := range cur {
		logical := logicalProject(root, e.Project)
		if logical == "" {
			continue
		}
		a := getAgg(logical)
		a.rebuilds++
		a.pkgCount[e.Package]++
		repoCount[e.Repo]++
	}

	prevTotal := 0
	for _, e := range prev {
		if logicalProject(root, e.Project) != "" {
			prevTotal++
		}
	}

	type imgKey struct{ project, pkg string }
	imgSince := map[imgKey]*time.Time{}
	imgAt := map[imgKey]*OverviewImage{}
	imgLogical := map[imgKey]string{}
	for _, s := range scans {
		logical := logicalProject(root, s.Project)
		if logical == "" {
			continue
		}
		k := imgKey{s.Project, s.Package}
		img, ok := imgAt[k]
		if !ok {
			img = &OverviewImage{Project: s.Project, Name: s.Package}
			imgAt[k] = img
			imgLogical[k] = logical
		}
		if s.Critical > img.Critical {
			img.Critical = s.Critical
		}
		if s.High > img.High {
			img.High = s.High
		}
		if (s.Critical > 0 || s.High > 0) && s.CveSince != nil {
			if cur := imgSince[k]; cur == nil || s.CveSince.Before(*cur) {
				imgSince[k] = s.CveSince
			}
		}
	}
	for k, since := range imgSince {
		imgAt[k].OldestOpenDays = int(now.Sub(*since).Hours() / 24)
	}

	fixDays := map[imgKey][]float64{}
	for _, p := range periods {
		k := imgKey{p.Project, p.Package}
		fixDays[k] = append(fixDays[k], p.CleanSince.Sub(p.CveSince).Hours()/24)
	}
	for k, days := range fixDays {
		img, ok := imgAt[k]
		if !ok {
			continue // period for an image with no current scan row
		}
		sum := 0.0
		for _, d := range days {
			sum += d
		}
		img.AvgFixDays = int(sum/float64(len(days)) + 0.5)
	}

	for k, img := range imgAt {
		getAgg(imgLogical[k]).images[k.project+"/"+k.pkg] = img
	}

	projects := []OverviewProject{}
	for logical, a := range agg {
		if a.rebuilds == 0 && len(a.images) == 0 {
			continue
		}
		p := OverviewProject{Project: logical, Rebuilds: a.rebuilds, Images: []OverviewImage{}}
		var topPkg string
		for name, n := range a.pkgCount {
			if topPkg == "" || n > a.pkgCount[topPkg] || (n == a.pkgCount[topPkg] && name < topPkg) {
				topPkg = name
			}
		}
		if topPkg != "" {
			p.TopPackage = &OverviewCount{Name: topPkg, Count: a.pkgCount[topPkg]}
		}
		var names []string
		for k := range a.images {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p.Images = append(p.Images, *a.images[k])
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Rebuilds != projects[j].Rebuilds {
			return projects[i].Rebuilds > projects[j].Rebuilds
		}
		return projects[i].Project < projects[j].Project
	})

	snap := OverviewSnapshot{
		Window:                     window,
		GeneratedAt:                now.Format(time.RFC3339),
		PreviousWindowRebuildTotal: prevTotal,
		Projects:                   projects,
	}
	var topRepo string
	for name, n := range repoCount {
		if topRepo == "" || n > repoCount[topRepo] || (n == repoCount[topRepo] && name < topRepo) {
			topRepo = name
		}
	}
	if topRepo != "" {
		snap.TopRepo = &OverviewCount{Name: topRepo, Count: repoCount[topRepo]}
	}
	return snap
}

// ── cache (mirrors releaseArtifactsCache: TTL + singleflight) ──

type overviewCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]overviewCacheEntry
	inflight map[string]chan struct{}
}

type overviewCacheEntry struct {
	snapshot OverviewSnapshot
	expires  time.Time
	err      error
}

func newOverviewCache(ttl time.Duration) *overviewCache {
	return &overviewCache{ttl: ttl, entries: map[string]overviewCacheEntry{}, inflight: map[string]chan struct{}{}}
}

func (c *overviewCache) Get(ctx context.Context, key string, fetch func(context.Context) (OverviewSnapshot, error)) (OverviewSnapshot, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.snapshot, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return OverviewSnapshot{}, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.snapshot, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	snapshot, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = overviewCacheEntry{snapshot: snapshot, expires: expires, err: err}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return snapshot, err
}

// ── handler ──

// overviewHandler serves GET /api/overview?window=24h|48h|7d.
func overviewHandler(db *sql.DB, root string, cache *overviewCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		dur, ok := overviewWindows[window]
		if !ok {
			http.Error(w, "invalid window (24h|48h|7d)", http.StatusBadRequest)
			return
		}
		snap, err := cache.Get(r.Context(), window, func(ctx context.Context) (OverviewSnapshot, error) {
			now := time.Now().UTC()
			cur, err := store.QueryBuildingEntries(db, now.Add(-dur), now)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			prev, err := store.QueryBuildingEntries(db, now.Add(-2*dur), now.Add(-dur))
			if err != nil {
				return OverviewSnapshot{}, err
			}
			scans, err := store.QueryAllCveScans(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			periods, err := store.QueryAllCvePeriods(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			return buildOverviewSnapshot(root, window, now, cur, prev, scans, periods), nil
		})
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}
}
