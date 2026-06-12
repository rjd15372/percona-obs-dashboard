package api

import (
	"database/sql"
	"encoding/json"
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

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			// Response already started; nothing we can do.
			return
		}
	}
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

		now := time.Now().UTC()
		var from, to time.Time

		if windowStr := r.URL.Query().Get("window"); windowStr != "" {
			windowMinutes, err := strconv.Atoi(windowStr)
			if err != nil || windowMinutes <= 0 {
				http.Error(w, "invalid window parameter", http.StatusBadRequest)
				return
			}
			from = now.Add(-time.Duration(windowMinutes) * time.Minute)
			to = now
		} else if fromStr := r.URL.Query().Get("from"); fromStr != "" {
			toStr := r.URL.Query().Get("to")
			if toStr == "" {
				http.Error(w, "to parameter is required when from is set", http.StatusBadRequest)
				return
			}
			const dateLayout = "2006-01-02"
			parsedFrom, err := time.Parse(dateLayout, fromStr)
			if err != nil {
				http.Error(w, "invalid from date", http.StatusBadRequest)
				return
			}
			parsedTo, err := time.Parse(dateLayout, toStr)
			if err != nil {
				http.Error(w, "invalid to date", http.StatusBadRequest)
				return
			}
			from = parsedFrom.UTC()
			// Include all events up to end of the 'to' day.
			to = parsedTo.UTC().Add(24*time.Hour - time.Nanosecond)
		} else {
			// Default: last 24 hours.
			from = now.Add(-24 * time.Hour)
			to = now
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
