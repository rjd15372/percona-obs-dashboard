package obs

import (
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// CarryForward returns fresh plus, for every instance in failed, the
// previous targets that instance owned, converted back to build states. An
// instance outage therefore freezes its targets at their last known state
// instead of dropping them.
func CarryForward(fresh []PackageBuildState, prev []model.Target, failed []string, project, pkg string) []PackageBuildState {
	if len(failed) == 0 || len(prev) == 0 {
		return fresh
	}
	down := make(map[string]bool, len(failed))
	for _, s := range failed {
		down[s] = true
	}
	out := append([]PackageBuildState(nil), fresh...)
	for _, t := range prev {
		if t.Instance != "" && down[t.Instance] {
			out = append(out, PackageBuildState{
				Project: project, Package: pkg,
				Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details,
				Instance: t.Instance,
			})
		}
	}
	return out
}

// WithoutInstance returns a copy of pkg without the targets owned by slug —
// and without unstamped (pre-migration) targets — with rollup and counts
// recomputed. Remaining targets keep their enrichment.
func WithoutInstance(pkg *model.Package, slug string) *model.Package {
	var kept []model.Target
	var states []PackageBuildState
	for _, t := range pkg.Targets {
		if t.Instance == slug || t.Instance == "" {
			continue
		}
		kept = append(kept, t)
		states = append(states, PackageBuildState{Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details, Instance: t.Instance})
	}
	rebuilt := buildPackage(pkg.Project, pkg.Name, pkg.Tags, states)
	out := *pkg
	out.Targets = kept
	out.RollupState = rebuilt.RollupState
	out.OKTargets = rebuilt.OKTargets
	out.TotalTargets = rebuilt.TotalTargets
	out.UpdatedAt = time.Now().UTC()
	return &out
}
