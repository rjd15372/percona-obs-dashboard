package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

// packagesHandler returns a handler for GET /api/products/{product}/{version}/packages.
func packagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		product := chi.URLParam(r, "product")
		// Use the product-level prefix so common packages (isv:percona:ppg:common)
		// are included alongside version-specific ones. Version filtering is done
		// client-side so the version tabs actually work.
		prefix := "isv:percona:" + product

		pkgs, err := store.QueryPackages(db, prefix)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// isv:percona:common:* subprojects are product-agnostic shared dependencies.
		perconaCommon, err := store.QueryPackages(db, "isv:percona:common")
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		pkgs = append(pkgs, perconaCommon...)

		isvCommon, err := store.QueryPackages(db, "isv:common")
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		pkgs = append(pkgs, isvCommon...)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			// Response already started; nothing we can do.
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
func prContextPackagesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")
		prefix := "isv:percona:PR:" + pr + ":" + subproject

		pkgs, err := store.QueryPackages(db, prefix)
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
func prContextEventsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pr := chi.URLParam(r, "pr")
		subproject := chi.URLParam(r, "subproject")
		prefix := "isv:percona:PR:" + pr + ":" + subproject

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

// PRGroup groups all packages under a single PR project number.
type PRGroup struct {
	PR          string           `json:"pr"`
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
