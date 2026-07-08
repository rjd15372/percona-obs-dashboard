package obs

import "github.com/percona/obs-dashboard/internal/model"

// Parkable reports whether pkg is waiting only on completions that MQ will
// announce, with the poller as loss-tolerant fallback. A parkable package can
// leave the working set and be re-added by a wake signal — nothing about it
// needs active polling.
//
// A target qualifies when it is:
//   - building with its BuildReason already fetched (enrichment complete) —
//     woken by package.build_success/fail/unchanged, or
//   - succeeded — published and never-publishing targets are inert; an
//     unpublished target in a publishing repo is woken by repo.published,
//     with the poller's repo-state check as the fallback for missed events
//     and "nothing changed" publish runs.
//
// Anything else (scheduled, blocked, finished, broken, …) still needs the
// 30s poll. A package whose active targets are all inert is Settled — the
// worker checks Settled first, so Parkable returning true for it is moot.
func Parkable(pkg *model.Package) bool {
	active := 0
	for _, t := range pkg.Targets {
		if skipState(t.State) {
			continue
		}
		active++
		switch {
		case t.State == "building" && t.BuildReason != "":
		case t.State == "succeeded":
		default:
			return false
		}
	}
	return active > 0
}
