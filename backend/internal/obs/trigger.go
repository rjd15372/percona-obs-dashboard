package obs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// InferTrigger attempts to determine why a package build state changed.
// It tries several OBS endpoints in order and returns the best explanation found.
// Always returns a non-nil Trigger (falls back to kind:"unknown").
func InferTrigger(ctx context.Context, c *Client, pkg *model.Package) *model.Trigger {
	// Pick the first failing target for per-target endpoint calls
	var failRepo, failArch string
	for _, t := range pkg.Targets {
		if t.State != "succeeded" {
			failRepo, failArch = t.Repo, t.Arch
			break
		}
	}

	// 1. Build history reason field
	if failRepo != "" {
		entries, err := c.PackageHistory(ctx, pkg.Project, failRepo, failArch, pkg.Name)
		if err == nil && len(entries) > 0 {
			last := entries[len(entries)-1]
			if last.Reason != "" {
				kind := classifyReason(last.Reason)
				return &model.Trigger{What: last.Reason, Kind: kind, At: time.Now().UTC()}
			}
		}
	}

	// 2. Build dep info diff
	if failRepo != "" {
		deps, err := c.BuildDepInfo(ctx, pkg.Project, failRepo, failArch)
		if err == nil {
			if what := depInfoSummary(deps, pkg.Name); what != "" {
				return &model.Trigger{What: what, Kind: "dependency bump", At: time.Now().UTC()}
			}
		}
	}

	// 3. For failed state: tail build log for compile error summary
	if pkg.RollupState == model.RollupFailed && failRepo != "" {
		log, err := c.BuildLog(ctx, pkg.Project, failRepo, failArch, pkg.Name, 4096)
		if err == nil {
			if summary := extractLogError(log); summary != "" {
				return &model.Trigger{What: summary, Kind: "build error", At: time.Now().UTC()}
			}
		}
	}

	// 4. Source history — check for recent commit
	commits, err := c.SourceHistory(ctx, pkg.Project, pkg.Name)
	if err == nil && len(commits) > 0 {
		last := commits[len(commits)-1]
		if last.Comment != "" {
			return &model.Trigger{
				What: truncate(last.Comment, 80),
				Kind: "service",
				At:   time.Unix(last.Time, 0).UTC(),
			}
		}
	}

	// 5. Fallback
	return &model.Trigger{
		What: fmt.Sprintf("%s state: %s", pkg.Name, string(pkg.RollupState)),
		Kind: "unknown",
		At:   time.Now().UTC(),
	}
}

func classifyReason(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "rebuild"):
		return "dependency bump"
	case strings.Contains(lower, "toolchain") || strings.Contains(lower, "gcc") || strings.Contains(lower, "clang"):
		return "toolchain bump"
	case strings.Contains(lower, "base image") || strings.Contains(lower, "baseimage"):
		return "base image"
	case strings.Contains(lower, "source"):
		return "service"
	default:
		return "unknown"
	}
}

// depInfoSummary returns a human-readable dep summary for the named package, or "".
func depInfoSummary(deps []DepInfo, pkgName string) string {
	for _, d := range deps {
		if d.Package == pkgName && len(d.Deps) > 0 {
			if len(d.Deps) == 1 {
				return d.Deps[0] + " updated"
			}
			return fmt.Sprintf("%s and %d other dependencies updated", d.Deps[0], len(d.Deps)-1)
		}
	}
	return ""
}

// extractLogError extracts the first error line from a build log tail.
func extractLogError(log string) string {
	for _, line := range strings.Split(log, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error:") || strings.Contains(lower, "fatal error:") {
			trimmed := strings.TrimSpace(line)
			return truncate(trimmed, 120)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
