package obs

import "github.com/percona/obs-dashboard/internal/model"

// Parkable reports whether pkg is waiting only on build completions that MQ
// will announce (build_success/build_fail/build_unchanged), with the poller as
// loss-tolerant fallback. A parkable package can leave the working set and be
// re-added by the wake signal — nothing about it needs active polling.
//
// A target qualifies when it is:
//   - building with its BuildReason already fetched (enrichment complete), or
//   - succeeded and inert: already Published, or in a repo that never
//     publishes. Succeeded-unpublished in a publishing repo does NOT qualify —
//     publication is only detected by polling (a missed repo.published event
//     has no poller fallback), so those packages keep polling.
//
// At least one building target is required: an all-inert package is Settled's
// territory, and anything else (scheduled, blocked, finished, broken, …)
// still needs the 30s poll.
func Parkable(pkg *model.Package, flags PublishFlags) bool {
	hasBuilding := false
	for _, t := range pkg.Targets {
		if skipState(t.State) {
			continue
		}
		switch {
		case t.State == "building" && t.BuildReason != "":
			hasBuilding = true
		case t.State == "succeeded" && (t.Published || !flags.Publishes(t.Repo)):
			// inert: nothing pending for this target
		default:
			return false
		}
	}
	return hasBuilding
}
