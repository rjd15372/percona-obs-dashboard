package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

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
			p.process(ctx, pkg)
		}
	}
}

func (p *Pool) process(ctx context.Context, pkg *model.Package) {
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
	if pkg.RollupState == model.RollupSucceeded {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
}
