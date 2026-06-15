package obs

import (
	"context"
	"log/slog"

	"github.com/percona/obs-dashboard/internal/model"
)

// BuildStateTask refreshes the package's targets, rollup state, and counts
// by fetching current build results from OBS for the specific package.
type BuildStateTask struct{}

func (t BuildStateTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	results, err := client.PackageBuildResults(ctx, pkg.Project, pkg.Name)
	if err != nil {
		return err
	}
	updated := buildPackage(pkg.Project, pkg.Name, pkg.Scope, results)
	// Preserve existing per-target enrichment from prior task runs.
	for i := range updated.Targets {
		for _, old := range pkg.Targets {
			if old.Repo == updated.Targets[i].Repo && old.Arch == updated.Targets[i].Arch {
				updated.Targets[i].BlockedBy = old.BlockedBy
				updated.Targets[i].BuildReason = old.BuildReason
				updated.Targets[i].BuildReasonPackages = old.BuildReasonPackages
				break
			}
		}
	}
	pkg.Targets = updated.Targets
	pkg.RollupState = updated.RollupState
	pkg.OKTargets = updated.OKTargets
	pkg.TotalTargets = updated.TotalTargets
	pkg.UpdatedAt = updated.UpdatedAt
	return nil
}

// BlockedReasonTask populates BlockedBy on blocked targets.
type BlockedReasonTask struct{}

func (t BlockedReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	EnrichBlockedTargets(ctx, client, pkg)
	return nil
}

// BuildReasonTask fetches the build trigger reason for non-succeeded targets.
type BuildReasonTask struct{}

func (t BuildReasonTask) Run(ctx context.Context, client *Client, pkg *model.Package) error {
	for i, target := range pkg.Targets {
		if target.State == "succeeded" {
			continue
		}
		result, err := client.PackageBuildReason(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name)
		if err != nil {
			slog.Warn("worker: build reason",
				"pkg", pkg.Name,
				"repo", target.Repo,
				"arch", target.Arch,
				"err", err)
			continue
		}
		pkg.Targets[i].BuildReason = result.Explain
		if result.Explain == "meta change" {
			pkg.Targets[i].BuildReasonPackages = result.Packages
		} else {
			pkg.Targets[i].BuildReasonPackages = nil
		}
	}
	return nil
}
