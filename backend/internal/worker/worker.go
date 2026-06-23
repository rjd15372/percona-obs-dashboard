package worker

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
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// Task is implemented by types that enrich a package's state from OBS.
// Implementations live in obs/tasks.go to avoid circular imports.
type Task interface {
	Run(ctx context.Context, client *obs.Client, pkg *model.Package) error
}

type Pool struct {
	size         int
	devTasks     []Task
	releaseTasks []Task
	client       *obs.Client
	db           *sql.DB
	hub          *hubpkg.Hub
	ws           *workingset.WorkingSet
}

func NewPool(size int, devTasks, releaseTasks []Task, client *obs.Client, db *sql.DB, hub *hubpkg.Hub, ws *workingset.WorkingSet) *Pool {
	return &Pool{size: size, devTasks: devTasks, releaseTasks: releaseTasks,
		client: client, db: db, hub: hub, ws: ws}
}

func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.size; i++ {
		go p.run(ctx)
	}
}

func (p *Pool) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pkg, ok := <-p.ws.Dispatch():
			if !ok {
				return
			}
			p.ProcessOnce(ctx, pkg)
		}
	}
}

// ProcessOnce runs the task chain for pkg (devTasks or releaseTasks based on
// pkg.IsRelease), upserts to DB, emits build events and SSE for real-time
// packages only, and removes pkg from the working set when rollup reaches published.
// Exported for testing.
func (p *Pool) ProcessOnce(ctx context.Context, pkg *model.Package) {
	// Capture now before tasks run so state_changed_at reflects when the state
	// was observed, not when the (potentially slow) task chain finished.
	now := time.Now().UTC()

	// Snapshot target state before task chain.
	// BuildReasonPackages is a slice field — deep copy to avoid aliasing.
	oldTargets := make([]model.Target, len(pkg.Targets))
	for i, t := range pkg.Targets {
		c := t
		if len(t.BuildReasonPackages) > 0 {
			c.BuildReasonPackages = append([]string(nil), t.BuildReasonPackages...)
		}
		oldTargets[i] = c
	}

	tasks := p.devTasks
	if pkg.IsRelease {
		tasks = p.releaseTasks
	}
	for _, t := range tasks {
		if err := t.Run(ctx, p.client, pkg); err != nil {
			slog.Warn("worker: task error",
				"task", fmt.Sprintf("%T", t),
				"pkg", pkg.Project+"/"+pkg.Name,
				"err", err)
		}
	}

	if err := store.UpsertPackageState(p.db, pkg, now); err != nil {
		slog.Error("worker: upsert package state", "pkg", pkg.Project+"/"+pkg.Name, "err", err)
	}

	if !pkg.IsRelease {
		p.hub.Notify(hubpkg.PackageUpdate(pkg))
		p.emitBuildEvents(pkg, oldTargets)
	}

	if pkg.RollupState == model.RollupPublished && pkg.IsContainer != nil {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
}

const obsBase = "https://build.opensuse.org"

// emitBuildEvents compares oldTargets with pkg.Targets and appends one event
// per target for each meaningful state transition, implementing a per-target
// build event state machine.
func (p *Pool) emitBuildEvents(pkg *model.Package, oldTargets []model.Target) {
	oldByKey := make(map[string]model.Target, len(oldTargets))
	for _, t := range oldTargets {
		oldByKey[t.Repo+"/"+t.Arch] = t
	}

	now := time.Now().UTC()

	for _, t := range pkg.Targets {
		key := t.Repo + "/" + t.Arch
		old := oldByKey[key]

		// build_started: BuildReason newly appeared, regardless of target state.
		if old.BuildReason == "" && t.BuildReason != "" {
			why := t.BuildReason
			if len(t.BuildReasonPackages) > 0 {
				why += ": " + strings.Join(t.BuildReasonPackages, ", ")
			}
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventBuildStarted,
				Tags:    pkg.Tags,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s build started", pkg.Name),
				Why:     why,
				URL:     fmt.Sprintf("%s/package/live_build_log/%s/%s/%s/%s", obsBase, pkg.Project, pkg.Name, t.Repo, t.Arch),
				At:      now,
			})
		}

		// Intermediate states — only after build_started (guard: BuildReason present).
		if t.BuildReason != "" {
			if old.State != "blocked" && t.State == "blocked" {
				p.appendEvent(&model.Event{
					ID:      "evt_" + ulid.Make().String(),
					Type:    model.EventBlocked,
					Tags:    pkg.Tags,
					Project: pkg.Project,
					Package: pkg.Name,
					Repo:    t.Repo,
					Arch:    t.Arch,
					What:    fmt.Sprintf("%s blocked", pkg.Name),
					Why:     t.BlockedBy,
					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
					At:      now,
				})
			}
			if old.State != "unresolvable" && t.State == "unresolvable" {
				p.appendEvent(&model.Event{
					ID:      "evt_" + ulid.Make().String(),
					Type:    model.EventUnresolvable,
					Tags:    pkg.Tags,
					Project: pkg.Project,
					Package: pkg.Name,
					Repo:    t.Repo,
					Arch:    t.Arch,
					What:    fmt.Sprintf("%s unresolvable", pkg.Name),
					Why:     t.Details,
					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
					At:      now,
				})
			}
			if old.State != "broken" && t.State == "broken" {
				p.appendEvent(&model.Event{
					ID:      "evt_" + ulid.Make().String(),
					Type:    model.EventBroken,
					Tags:    pkg.Tags,
					Project: pkg.Project,
					Package: pkg.Name,
					Repo:    t.Repo,
					Arch:    t.Arch,
					What:    fmt.Sprintf("%s broken", pkg.Name),
					Why:     t.Details,
					URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
					At:      now,
				})
			}
		}

		// succeeded: publication is the real terminal success signal.
		if !old.Published && t.Published {
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventSucceeded,
				Tags:    pkg.Tags,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s succeeded", pkg.Name),
				Why:     "",
				Version: pkg.Version,
				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
				At:      now,
			})
		}

		// failed: only the terminal "failed" state; why is scaffolded for future use.
		if old.State != "failed" && t.State == "failed" {
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventFailed,
				Tags:    pkg.Tags,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s failed", pkg.Name),
				Why:     "",
				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
				At:      now,
			})
		}
	}
}

func (p *Pool) appendEvent(evt *model.Event) {
	if p.db == nil {
		return
	}
	if err := store.AppendEvent(p.db, evt); err != nil {
		slog.Error("worker: append event", "err", err)
		return
	}
	p.hub.Notify(hubpkg.NewEvent(evt))
}
