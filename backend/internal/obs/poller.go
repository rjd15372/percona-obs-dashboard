package obs

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

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
	root     string
	hub      *hubpkg.Hub
	ws       *workingset.WorkingSet
}

func NewPoller(client *Client, db *sql.DB, interval time.Duration, h *hubpkg.Hub, ws *workingset.WorkingSet, root string) *Poller {
	return &Poller{client: client, db: db, interval: interval, root: root, hub: h, ws: ws}
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
	projects, err := p.discoverProjects(ctx, p.root)
	if err != nil {
		slog.Error("poller: discover projects", "root", p.root, "err", err)
		return
	}

	liveProjects := make(map[string]bool, len(projects))
	for _, proj := range projects {
		liveProjects[proj] = true
	}

	existing, err := store.QueryPackages(p.db, p.root)
	if err != nil {
		slog.Error("poller: query packages", "root", p.root, "err", err)
		return
	}
	byKey := make(map[string]*model.Package, len(existing))
	for _, pkg := range existing {
		byKey[pkg.Project+"/"+pkg.Name] = pkg
	}

	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		kind := Classify(p.root, project)
		if kind == KindUnknown {
			continue
		}

		results, err := p.client.BuildResults(ctx, project)
		if err != nil {
			slog.Warn("poller: build results", "project", project, "err", err)
			continue
		}

		byPkg := map[string][]PackageBuildState{}
		for _, r := range results {
			byPkg[r.Package] = append(byPkg[r.Package], r)
		}

		tags := ProjectTags(p.root, project)
		for pkgName, targets := range byPkg {
			pkg := buildPackage(project, pkgName, tags, targets)
			pkg.IsRelease = kind == KindRelease

			key := project + "/" + pkgName
			prev := byKey[key]
			preservePackageEnrichment(prev, pkg)

			// Preserve published state: OBS build results only return "succeeded",
			// never "published". Without this guard the poller would flip a published
			// package back to succeeded every tick, causing a succeeded↔published
			// oscillation and spurious SSE broadcasts.
			if prev != nil && prev.RollupState == model.RollupPublished &&
				pkg.RollupState == model.RollupSucceeded && !targetsChanged(prev, pkg) {
				pkg.RollupState = model.RollupPublished
			}

			rollupChanged := prev == nil || prev.RollupState != pkg.RollupState
			tagsChanged := prev != nil && len(prev.Tags) != len(pkg.Tags)

			if kind.IsRealTime() {
				if rollupChanged || targetsChanged(prev, pkg) || tagsChanged {
					if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
						slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
						continue
					}
					p.hub.Notify(hubpkg.PackageUpdate(pkg))
					p.ws.Add(pkg)
				}
			} else {
				// Release project: upsert silently — no SSE broadcast, no events.
				// Reset rollup to building if target set changed on an already-published package.
				if prev != nil && prev.RollupState == model.RollupPublished && targetsChanged(prev, pkg) {
					pkg.RollupState = model.RollupBuilding
				}
				if rollupChanged || targetsChanged(prev, pkg) || tagsChanged {
					if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
						slog.Error("poller: upsert release package", "pkg", pkgName, "err", err)
						continue
					}
				}
				// Add to working set only if there is work remaining.
				containerNeedsTags := pkg.IsContainer != nil && *pkg.IsContainer && len(pkg.ContainerTags) == 0
				if pkg.RollupState != model.RollupPublished || pkg.IsContainer == nil || containerNeedsTags {
					p.ws.Add(pkg)
				}
			}
		}

		// Garbage-collect packages removed from this project in OBS.
		for _, stored := range existing {
			if stored.Project != project {
				continue
			}
			if _, live := byPkg[stored.Name]; !live {
				slog.Info("poller: removing stale package", "project", project, "pkg", stored.Name)
				if err := store.DeletePackage(p.db, project, stored.Name); err != nil {
					slog.Error("poller: delete stale package", "project", project, "pkg", stored.Name, "err", err)
				}
				p.ws.Remove(project + "/" + stored.Name)
			}
		}
	}

	// Garbage-collect packages for projects no longer in OBS.
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
			for _, pkg := range existing {
				if pkg.Project == proj {
					p.ws.Remove(proj + "/" + pkg.Name)
				}
			}
		}
	}
}

func preservePackageEnrichment(prev, next *model.Package) {
	if prev == nil || next == nil {
		return
	}
	if next.IsContainer == nil {
		next.IsContainer = prev.IsContainer
	}
	if next.Version == "" {
		next.Version = prev.Version
	}
	if len(next.ContainerTags) == 0 {
		next.ContainerTags = prev.ContainerTags
	}
	if next.Trigger == nil {
		next.Trigger = prev.Trigger
	}
	// For containers whose builds are intentionally disabled (e.g. release
	// containers), the poller's buildPackage returns no active targets because
	// skipState filters them out. Preserve whatever targets ContainerTagsTask
	// stored so that arch info survives across poll ticks.
	if len(next.Targets) == 0 && len(prev.Targets) > 0 {
		allDisabled := true
		for _, t := range prev.Targets {
			if t.State != "disabled" {
				allDisabled = false
				break
			}
		}
		if allDisabled {
			next.Targets = prev.Targets
		}
	}
	if len(prev.Tags) > 0 {
		seen := make(map[string]bool, len(next.Tags)+len(prev.Tags))
		for _, tag := range next.Tags {
			seen[tag] = true
		}
		for _, tag := range prev.Tags {
			if !seen[tag] {
				next.Tags = append(next.Tags, tag)
			}
		}
	}

	prevTargets := make(map[string]model.Target, len(prev.Targets))
	for _, target := range prev.Targets {
		prevTargets[target.Repo+"/"+target.Arch] = target
	}
	for i := range next.Targets {
		prevTarget, ok := prevTargets[next.Targets[i].Repo+"/"+next.Targets[i].Arch]
		if !ok {
			continue
		}
		// Preserve Published unconditionally: a transient blocked (or any other
		// intermediate) state does not remove the published artifact from the OBS
		// repo. Without this, a brief state change resets Published=false and causes
		// a spurious "succeeded" event when the target re-publishes without having
		// actually rebuilt. BuildStateTask in the worker always re-derives Published
		// from fresh OBS data, so this only affects the ProcessOnce before-snapshot.
		if !next.Targets[i].Published && prevTarget.Published {
			next.Targets[i].Published = true
		}
		if prevTarget.State != next.Targets[i].State {
			continue
		}
		if next.Targets[i].Details == "" {
			next.Targets[i].Details = prevTarget.Details
		}
		if next.Targets[i].BlockedBy == "" {
			next.Targets[i].BlockedBy = prevTarget.BlockedBy
		}
		if next.Targets[i].BuildReason == "" {
			next.Targets[i].BuildReason = prevTarget.BuildReason
		}
		if len(next.Targets[i].BuildReasonPackages) == 0 {
			next.Targets[i].BuildReasonPackages = prevTarget.BuildReasonPackages
		}
	}
}

// targetsChanged returns true when any individual target state or detail differs
// between the stored package and the freshly-polled one. This catches transient
// state changes (e.g. succeeded→finished→succeeded) and late OBS details (e.g.
// finished with outcome succeeded) that don't alter the rollup.
func targetsChanged(prev *model.Package, next *model.Package) bool {
	if prev == nil {
		return true
	}
	if len(prev.Targets) != len(next.Targets) {
		return true
	}
	prevTargets := make(map[string]model.Target, len(prev.Targets))
	for _, t := range prev.Targets {
		prevTargets[t.Repo+"/"+t.Arch] = t
	}
	for _, t := range next.Targets {
		prevTarget, ok := prevTargets[t.Repo+"/"+t.Arch]
		if !ok || prevTarget.State != t.State || prevTarget.Details != t.Details {
			return true
		}
	}
	return false
}

// discoverProjects returns all OBS projects whose names start with root+":".
// The root itself is not included — it is a namespace prefix, not a pollable project.
func (p *Poller) discoverProjects(ctx context.Context, root string) ([]string, error) {
	return p.client.SearchProjects(ctx, root)
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
func buildPackage(project, name string, tags []string, targets []PackageBuildState) *model.Package {
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
		Tags:         tags,
		RollupState:  rollup,
		OKTargets:    ok,
		TotalTargets: len(active),
		Targets:      mTargets,
		UpdatedAt:    time.Now().UTC(),
	}
}

