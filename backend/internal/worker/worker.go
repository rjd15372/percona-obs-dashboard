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
	size   int
	tasks  []Task
	client *obs.Client
	db     *sql.DB
	hub    *hubpkg.Hub
	ws     *workingset.WorkingSet
}

func NewPool(size int, tasks []Task, client *obs.Client, db *sql.DB, hub *hubpkg.Hub, ws *workingset.WorkingSet) *Pool {
	return &Pool{size: size, tasks: tasks, client: client, db: db, hub: hub, ws: ws}
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

// ProcessOnce runs the full task chain for pkg, upserts to DB,
// emits build events for state transitions, and removes pkg from the
// working set once all succeeded targets are published.
// Exported for testing.
func (p *Pool) ProcessOnce(ctx context.Context, pkg *model.Package) {
	// Snapshot target state before task chain. BuildReasonPackages is a slice
	// field — deep copy to avoid aliasing if a task appends in-place.
	oldTargets := make([]model.Target, len(pkg.Targets))
	for i, t := range pkg.Targets {
		c := t
		if len(t.BuildReasonPackages) > 0 {
			c.BuildReasonPackages = append([]string(nil), t.BuildReasonPackages...)
		}
		oldTargets[i] = c
	}

	for _, t := range p.tasks {
		if err := t.Run(ctx, p.client, pkg); err != nil {
			slog.Warn("worker: task error",
				"task", fmt.Sprintf("%T", t),
				"pkg", pkg.Project+"/"+pkg.Name,
				"err", err)
		}
	}

	if err := store.UpsertPackageState(p.db, pkg); err != nil {
		slog.Error("worker: upsert package state", "pkg", pkg.Project+"/"+pkg.Name, "err", err)
	}
	p.hub.Notify(hubpkg.PackageUpdate(pkg))
	p.emitBuildEvents(pkg, oldTargets)

	if pkg.RollupState == model.RollupSucceeded && allTargetsPublished(pkg) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
}

// allTargetsPublished returns true when every succeeded target has been published.
func allTargetsPublished(pkg *model.Package) bool {
	for _, t := range pkg.Targets {
		if t.State == "succeeded" && !t.Published {
			return false
		}
	}
	return true
}

var failStates = map[string]bool{"failed": true, "unresolvable": true, "broken": true}

const obsBase = "https://build.opensuse.org"

// emitBuildEvents compares oldTargets with pkg.Targets and appends one event
// per target for each meaningful state transition.
func (p *Pool) emitBuildEvents(pkg *model.Package, oldTargets []model.Target) {
	oldByKey := make(map[string]model.Target, len(oldTargets))
	for _, t := range oldTargets {
		oldByKey[t.Repo+"/"+t.Arch] = t
	}

	now := time.Now().UTC()

	for _, t := range pkg.Targets {
		key := t.Repo + "/" + t.Arch
		old := oldByKey[key]

		// build_started: reason newly appeared.
		if old.BuildReason == "" && t.BuildReason != "" {
			why := t.BuildReason
			if len(t.BuildReasonPackages) > 0 {
				why += ": " + strings.Join(t.BuildReasonPackages, ", ")
			}
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventBuildStarted,
				Scope:   pkg.Scope,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s build started on %s", pkg.Name, key),
				Why:     why,
				URL:     fmt.Sprintf("%s/package/live_build_log/%s/%s/%s/%s", obsBase, pkg.Project, pkg.Name, t.Repo, t.Arch),
				At:      now,
			})
		}

		// failed (includes unresolvable, broken).
		if !failStates[old.State] && failStates[t.State] {
			why := ""
			if t.State == "unresolvable" && t.Details != "" {
				why = "unresolvable: " + t.Details
			} else if t.State == "broken" && t.Details != "" {
				why = "broken: " + t.Details
			}
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventFailed,
				Scope:   pkg.Scope,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s failed on %s", pkg.Name, key),
				Why:     why,
				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
				At:      now,
			})
		}

		// succeeded.
		if old.State != "succeeded" && t.State == "succeeded" {
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventSucceeded,
				Scope:   pkg.Scope,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s succeeded on %s", pkg.Name, key),
				Why:     "",
				URL:     fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name),
				At:      now,
			})
		}

		// published.
		if !old.Published && t.Published {
			p.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventPublished,
				Scope:   pkg.Scope,
				Project: pkg.Project,
				Package: pkg.Name,
				Repo:    t.Repo,
				Arch:    t.Arch,
				What:    fmt.Sprintf("%s published on %s", pkg.Name, key),
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
