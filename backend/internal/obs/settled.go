package obs

import "github.com/percona/obs-dashboard/internal/model"

// Settled reports whether pkg has nothing left for the worker to observe and can
// leave the working set. It does NOT mutate rollup_state — it only decides
// working-set membership.
//
//   - published, failed → terminal
//   - succeeded         → terminal iff every active target has published OR sits
//     in a non-publishing repo (nothing will ever flip it to published)
//   - everything else   → keep polling
func Settled(pkg *model.Package, flags PublishFlags) bool {
	switch pkg.RollupState {
	case model.RollupPublished, model.RollupFailed:
		return true
	case model.RollupSucceeded:
		for _, t := range pkg.Targets {
			if skipState(t.State) {
				continue
			}
			if flags.Publishes(t.Repo) && !t.Published {
				return false
			}
		}
		return true
	default:
		return false
	}
}
