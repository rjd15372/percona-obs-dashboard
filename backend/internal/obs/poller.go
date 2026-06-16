package obs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// Poller periodically fetches OBS build results and reconciles them with the store.
type Poller struct {
	client   *Client
	db       *sql.DB
	interval time.Duration
	roots    []string
	hub      *hubpkg.Hub
	ws       *workingset.WorkingSet
}

func NewPoller(client *Client, db *sql.DB, interval time.Duration, h *hubpkg.Hub, ws *workingset.WorkingSet) *Poller {
	return &Poller{client: client, db: db, interval: interval, roots: []string{"isv:percona", "isv:common"}, hub: h, ws: ws}
}

// Run blocks until ctx is cancelled. It ticks immediately on first call.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	var projects []string
	for _, root := range p.roots {
		proj, err := p.discoverProjects(ctx, root)
		if err != nil {
			slog.Error("poller: discover projects", "root", root, "err", err)
			return
		}
		projects = append(projects, proj...)
	}

	liveProjects := make(map[string]bool, len(projects))
	for _, proj := range projects {
		liveProjects[proj] = true
	}

	// Load current store state keyed by (project, package)
	var existing []*model.Package
	for _, root := range p.roots {
		pkgs, err := store.QueryPackages(p.db, root)
		if err != nil {
			slog.Error("poller: query packages", "root", root, "err", err)
			return
		}
		existing = append(existing, pkgs...)
	}
	byKey := make(map[string]*model.Package, len(existing))
	for _, pkg := range existing {
		byKey[pkg.Project+"/"+pkg.Name] = pkg
	}

	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		results, err := p.client.BuildResults(ctx, project)
		if err != nil {
			slog.Warn("poller: build results", "project", project, "err", err)
			continue
		}

		// Group results by package name
		byPkg := map[string][]PackageBuildState{}
		for _, r := range results {
			byPkg[r.Package] = append(byPkg[r.Package], r)
		}

		scope := InferScope(project)
		for pkgName, targets := range byPkg {
			pkg := buildPackage(project, pkgName, scope, targets)
			key := project + "/" + pkgName
			prev := byKey[key]

			rollupChanged := prev == nil || prev.RollupState != pkg.RollupState
			scopeChanged := prev != nil && prev.Scope != pkg.Scope
			if rollupChanged || targetsChanged(prev, pkg) || scopeChanged {
				if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
					slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
					continue
				}
				p.hub.Notify(hubpkg.PackageUpdate(pkg))
				p.ws.Add(pkg)
				if rollupChanged {
					evt := stateChangeEvent(pkg, prev)
					if err := store.AppendEvent(p.db, evt); err != nil {
						slog.Error("poller: append event", "err", err)
					} else {
						p.hub.Notify(hubpkg.NewEvent(evt))
					}
				}
			}
		}
	}

	// Garbage-collect packages for projects that no longer exist in OBS.
	// We only do this when discovery succeeded (projects is non-empty or root
	// itself returned no subprojects), so a transient API failure cannot wipe
	// the store.
	storedProjects := make(map[string]bool)
	for _, pkg := range existing {
		storedProjects[pkg.Project] = true
	}
	for proj := range storedProjects {
		if !liveProjects[proj] {
			slog.Info("poller: removing packages for deleted project", "project", proj)
			if err := store.DeletePackagesByProject(p.db, proj); err != nil {
				slog.Error("poller: delete packages", "project", proj, "err", err)
			}
		}
	}
}

// targetsChanged returns true when any individual target state differs between
// the stored package and the freshly-polled one. This catches transient state
// changes (e.g. succeeded→finished→succeeded) that don't alter the rollup.
func targetsChanged(prev *model.Package, next *model.Package) bool {
	if prev == nil {
		return true
	}
	if len(prev.Targets) != len(next.Targets) {
		return true
	}
	prevStates := make(map[string]string, len(prev.Targets))
	for _, t := range prev.Targets {
		prevStates[t.Repo+"/"+t.Arch] = t.State
	}
	for _, t := range next.Targets {
		if prevStates[t.Repo+"/"+t.Arch] != t.State {
			return true
		}
	}
	return false
}

// discoverProjects returns all OBS projects under root using the search API.
func (p *Poller) discoverProjects(ctx context.Context, root string) ([]string, error) {
	sub, err := p.client.SearchProjects(ctx, root)
	if err != nil {
		return nil, err
	}
	return append([]string{root}, sub...), nil
}

// InferScope classifies an OBS project name into a Scope tier.
func InferScope(project string) model.Scope {
	lower := strings.ToLower(project)
	switch {
	// PR projects: isv:percona:PR:pr-<number>[:<subproject>]
	case strings.HasPrefix(lower, "isv:percona:pr:"):
		return model.ScopePR
	case strings.Contains(lower, "release"):
		return model.ScopeRelease
	case strings.Contains(lower, ":ppg:common"):
		return model.ScopePPGCommon
	case strings.Contains(lower, "ppgcommon"):
		return model.ScopePPGCommon
	// isv:percona:common:* subprojects (e.g. :deps:build, :containers:ubi9) are
	// all common regardless of further path segments like "container".
	case strings.HasPrefix(lower, "isv:percona:common:"):
		return model.ScopeCommon
	case strings.Contains(lower, "container"):
		return model.ScopeContainer
	case strings.Contains(lower, "common"):
		return model.ScopeCommon
	default:
		// projects like isv:percona:ppg:17 have a version number segment
		parts := strings.Split(project, ":")
		if len(parts) >= 4 {
			return model.ScopeVersion
		}
		return model.ScopeCommon
	}
}

// PRNumber extracts the PR number from a PR project path.
// Returns "" if the project is not a PR project.
// Example: "isv:percona:PR:pr-42:ppg17" → "42"
func PRNumber(project string) string {
	parts := strings.Split(project, ":")
	for i, p := range parts {
		if strings.EqualFold(p, "PR") && i+1 < len(parts) {
			prSegment := parts[i+1]
			return strings.TrimPrefix(strings.ToLower(prSegment), "pr-")
		}
	}
	return ""
}

// skipState returns true for OBS states that represent a build being intentionally
// off and should not contribute to the rollup or target counts.
func skipState(state string) bool {
	switch state {
	case "disabled", "excluded", "locked":
		return true
	}
	return false
}

// buildPackage aggregates target states into a Package with worst-case rollup.
// Targets with state disabled/excluded/locked are silently dropped.
func buildPackage(project, name string, scope model.Scope, targets []PackageBuildState) *model.Package {
	// Precedence from worst to best. finished/scheduled are transient in-progress
	// states and must appear before succeeded so they are not silently ignored.
	stateOrder := []model.RollupState{
		model.RollupBroken, model.RollupFailed, model.RollupUnresolvable,
		model.RollupBlocked, model.RollupBuilding, model.RollupFinished,
		model.RollupScheduled, model.RollupSucceeded,
	}
	stateSet := map[string]bool{}
	var active []PackageBuildState
	for _, t := range targets {
		if skipState(t.State) {
			continue
		}
		active = append(active, t)
		stateSet[t.State] = true
	}

	rollup := model.RollupSucceeded
	for _, s := range stateOrder {
		if stateSet[string(s)] {
			rollup = s
			break
		}
	}

	ok := 0
	mTargets := make([]model.Target, len(active))
	for i, t := range active {
		mTargets[i] = model.Target{Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details}
		if t.State == "succeeded" {
			ok++
		}
	}

	return &model.Package{
		Project:      project,
		Name:         name,
		Scope:        scope,
		RollupState:  rollup,
		OKTargets:    ok,
		TotalTargets: len(active),
		Targets:      mTargets,
		UpdatedAt:    time.Now().UTC(),
	}
}

func stateChangeEvent(pkg *model.Package, prev *model.Package) *model.Event {
	evtType := model.EventType(string(pkg.RollupState))
	what := fmt.Sprintf("%s %s", pkg.Name, string(pkg.RollupState))
	why := "first observed"
	if prev != nil {
		why = fmt.Sprintf("state changed from %s", string(prev.RollupState))
	}
	return &model.Event{
		ID:      "evt_" + ulid.Make().String(),
		Type:    evtType,
		Scope:   pkg.Scope,
		Project: pkg.Project,
		Package: pkg.Name,
		What:    what,
		Why:     why,
		URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", pkg.Project, pkg.Name),
		At:      pkg.UpdatedAt,
	}
}
