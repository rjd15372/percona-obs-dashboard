package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

// packagesHandler returns a handler for GET /api/products/{product}/{version}/packages.
func packagesHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		version := chi.URLParam(r, "version")

		pkgs, err := store.QueryBuildPackages(db, root, product, version)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			return
		}
	}
}

// parseTimeWindow parses window/from/to query params and returns the time range.
// Defaults to the last 24 hours when no params are provided.
func parseTimeWindow(r *http.Request) (from, to time.Time, err error) {
	now := time.Now().UTC()
	if windowStr := r.URL.Query().Get("window"); windowStr != "" {
		windowMinutes, parseErr := strconv.Atoi(windowStr)
		if parseErr != nil || windowMinutes <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid window")
		}
		return now.Add(-time.Duration(windowMinutes) * time.Minute), now, nil
	}
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		toStr := r.URL.Query().Get("to")
		if toStr == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("to required")
		}
		const layout = "2006-01-02"
		parsedFrom, parseErr := time.Parse(layout, fromStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from")
		}
		parsedTo, parseErr := time.Parse(layout, toStr)
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to")
		}
		return parsedFrom.UTC(), parsedTo.UTC().Add(24*time.Hour - time.Nanosecond), nil
	}
	return now.Add(-24 * time.Hour), now, nil
}

// eventsHandler returns a handler for GET /api/products/{product}/{version}/events.
// Query params:
//   - window=<minutes>  — last N minutes (overrides from/to)
//   - from=YYYY-MM-DD   — start of date range (inclusive)
//   - to=YYYY-MM-DD     — end of date range (inclusive, treated as end-of-day)
//
// Default (no params): last 24 hours.
func eventsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		prefix := "isv:percona:" + product

		from, to, err := parseTimeWindow(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		events, err := store.QueryEvents(db, prefix, from, to)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			return
		}
	}
}

// prContextPackagesHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/packages.
// Builds the OBS prefix as isv:percona:PR:{pr}:{subproject}.
// {version} is accepted for URL symmetry with /api/products routes but ignored server-side;
// the prefix covers all versions and version filtering is done client-side.
func prContextPackagesHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")

		pkgs, err := store.QueryPRBuildPackages(db, root, pr, subproject)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			return
		}
	}
}

// prContextEventsHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/events.
// Builds the OBS prefix as isv:percona:PR:{pr}:{subproject}.
// {version} is accepted for URL symmetry but ignored server-side (filtering is client-side).
func prContextEventsHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")

		from, to, err := parseTimeWindow(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		events, err := store.QueryPRBuildEvents(db, root, pr, subproject, from, to)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			return
		}
	}
}

// PRGroup groups all packages under a single PR project number.
type PRGroup struct {
	PR          string            `json:"pr"`
	RollupState model.RollupState `json:"rollup_state"`
	Packages    []*model.Package  `json:"packages"`
}

// prPackagesHandler returns a handler for GET /api/pr/packages.
// It returns all PR packages (isv:percona:PR:*) grouped by PR number,
// sorted by PR number descending (newest first).
func prPackagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkgs, err := store.QueryPackages(db, "isv:percona:PR")
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Group by PR number.
		byPR := map[string][]*model.Package{}
		for _, p := range pkgs {
			pr := obs.PRNumber(p.Project)
			if pr == "" {
				continue
			}
			byPR[pr] = append(byPR[pr], p)
		}

		// Build sorted slice of groups (descending PR number so latest first).
		groups := make([]PRGroup, 0, len(byPR))
		for pr, packages := range byPR {
			rollup := worstRollup(packages)
			groups = append(groups, PRGroup{PR: pr, RollupState: rollup, Packages: packages})
		}
		sort.Slice(groups, func(i, j int) bool {
			// Numeric descending; fall back to string descending on parse error.
			ni, erri := strconv.Atoi(groups[i].PR)
			nj, errj := strconv.Atoi(groups[j].PR)
			if erri == nil && errj == nil {
				return ni > nj
			}
			return groups[i].PR > groups[j].PR
		})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(groups); err != nil {
			return
		}
	}
}

// RepoInfo describes a single OBS build repository.
type RepoInfo struct {
	OBS  string `json:"obs"`
	Name string `json:"name"`
}

// ReposResponse groups OBS repos by package type.
type ReposResponse struct {
	RPM []RepoInfo `json:"rpm"`
	DEB []RepoInfo `json:"deb"`
}

// repoType returns "deb" for Debian/Ubuntu repos, "rpm" for everything else.
func repoType(obs string) string {
	if strings.HasPrefix(obs, "Debian_") || strings.HasPrefix(obs, "Ubuntu_") || strings.HasPrefix(obs, "xUbuntu_") {
		return "deb"
	}
	return "rpm"
}

// repoDisplayName generates a human-readable label from an OBS repo identifier.
// e.g. "UBI_9" → "UBI 9", "xUbuntu_24.04" → "Ubuntu 24.04".
func repoDisplayName(obs string) string {
	if strings.HasPrefix(obs, "xUbuntu_") {
		return strings.ReplaceAll(strings.TrimPrefix(obs, "x"), "_", " ")
	}
	return strings.ReplaceAll(obs, "_", " ")
}

// reposHandlerWithPrefix is the shared implementation for all /repos endpoints.
// prefixFn extracts the full OBS project prefix from the request URL params.
func reposHandlerWithPrefix(db *sql.DB, prefixFn func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obsRepos, err := store.QueryDistinctRepos(db, prefixFn(r))
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := ReposResponse{RPM: []RepoInfo{}, DEB: []RepoInfo{}}
		for _, obs := range obsRepos {
			info := RepoInfo{OBS: obs, Name: repoDisplayName(obs)}
			if repoType(obs) == "deb" {
				resp.DEB = append(resp.DEB, info)
			} else {
				resp.RPM = append(resp.RPM, info)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
	}
}

// reposHandler returns a handler for GET /api/products/{product}/{version}/repos.
// It queries the DB for distinct OBS repository names found in non-container
// packages' targets, and returns them grouped into rpm and deb categories.
func reposHandler(db *sql.DB) http.HandlerFunc {
	return reposHandlerWithPrefix(db, func(r *http.Request) string {
		return "isv:percona:" + chi.URLParam(r, "product") + ":" + chi.URLParam(r, "version")
	})
}

// releasesPackagesHandler returns a handler for GET /api/releases/ppg/{version}/packages.
// Serves release packages from the DB instead of hitting OBS live.
func releasesPackagesHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefix := root + ":ppg:releases"
		pkgs, err := store.QueryReleasePackages(db, prefix)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			return
		}
	}
}

// releasesReposHandler returns a handler for GET /api/releases/ppg/{version}/repos.
// Serves repos from the DB instead of hitting OBS live.
func releasesReposHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := chi.URLParam(r, "version")
		prefix := root + ":ppg:releases:" + version
		repos, err := store.QueryDistinctRepos(db, prefix)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := ReposResponse{RPM: []RepoInfo{}, DEB: []RepoInfo{}}
		for _, obsName := range repos {
			info := RepoInfo{OBS: obsName, Name: repoDisplayName(obsName)}
			if repoType(obsName) == "deb" {
				resp.DEB = append(resp.DEB, info)
			} else {
				resp.RPM = append(resp.RPM, info)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
	}
}

// prReposHandler returns a handler for GET /api/pr/{pr}/{subproject}/{version}/repos.
func prReposHandler(db *sql.DB, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repos, err := store.QueryPRDistinctRepos(
			db,
			root,
			chi.URLParam(r, "pr"),
			chi.URLParam(r, "subproject"),
			chi.URLParam(r, "version"),
		)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := ReposResponse{RPM: []RepoInfo{}, DEB: []RepoInfo{}}
		for _, obsName := range repos {
			info := RepoInfo{OBS: obsName, Name: repoDisplayName(obsName)}
			if repoType(obsName) == "deb" {
				resp.DEB = append(resp.DEB, info)
			} else {
				resp.RPM = append(resp.RPM, info)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
	}
}

// binariesHandler returns a handler for GET /api/binaries.
// Query params: project, repo, arch, package.
// It proxies the OBS binary listing API and returns distributable filenames as JSON.
func binariesHandler(obsClient *obs.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := r.URL.Query().Get("project")
		repo := r.URL.Query().Get("repo")
		arch := r.URL.Query().Get("arch")
		pkg := r.URL.Query().Get("package")
		if project == "" || repo == "" || arch == "" || pkg == "" {
			http.Error(w, "project, repo, arch, package are required", http.StatusBadRequest)
			return
		}
		if obsClient == nil {
			http.Error(w, "OBS client not configured", http.StatusServiceUnavailable)
			return
		}

		filenames, err := obsClient.PackageBinaries(r.Context(), project, repo, arch, pkg)
		if err != nil {
			http.Error(w, "failed to fetch binaries: "+err.Error(), http.StatusBadGateway)
			return
		}
		if filenames == nil {
			filenames = []string{}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"binaries": filenames}); err != nil {
			return
		}
	}
}

// worstRollupFromTargets returns the worst RollupState derived from a slice of Targets.
func worstRollupFromTargets(targets []model.Target) model.RollupState {
	worst := model.RollupSucceeded
	for _, t := range targets {
		var rs model.RollupState
		switch t.State {
		case "failed":
			rs = model.RollupFailed
		case "broken":
			rs = model.RollupBroken
		case "unresolvable":
			rs = model.RollupUnresolvable
		case "blocked":
			rs = model.RollupBlocked
		case "building", "finished", "scheduled":
			rs = model.RollupBuilding
		default:
			rs = model.RollupSucceeded
		}
		if rs.Severity() > worst.Severity() {
			worst = rs
		}
	}
	return worst
}

// worstRollup returns the worst RollupState across a slice of packages.
func worstRollup(pkgs []*model.Package) model.RollupState {
	worst := model.RollupSucceeded
	for _, p := range pkgs {
		if p.RollupState.Severity() > worst.Severity() {
			worst = p.RollupState
		}
	}
	return worst
}

// rebuildHandler returns a handler for POST /api/rebuild.
// Decodes {"project","repo","arch","package"} JSON body and triggers an OBS rebuild.
func rebuildHandler(obsClient *obs.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Project string `json:"project"`
			Repo    string `json:"repo"`
			Arch    string `json:"arch"`
			Package string `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Project == "" || body.Repo == "" || body.Arch == "" || body.Package == "" {
			http.Error(w, "project, repo, arch, package are required", http.StatusBadRequest)
			return
		}
		if err := obsClient.Rebuild(r.Context(), body.Project, body.Repo, body.Arch, body.Package); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
