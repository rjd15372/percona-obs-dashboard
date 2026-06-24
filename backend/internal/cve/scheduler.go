package cve

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

type NightlyScheduler struct {
	db      *sql.DB
	scanner *Scanner
}

func NewNightlyScheduler(db *sql.DB, scanner *Scanner) *NightlyScheduler {
	return &NightlyScheduler{db: db, scanner: scanner}
}

func (n *NightlyScheduler) Run(ctx context.Context) {
	n.enqueueUnscanned(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextNightlyRun())):
		}
		n.enqueueAll(ctx)
	}
}

func (n *NightlyScheduler) enqueueUnscanned(ctx context.Context) {
	pkgs, err := store.QueryPublishedContainers(n.db)
	if err != nil {
		slog.Error("cve scheduler: query published containers", "err", err)
		return
	}
	for _, pkg := range pkgs {
		if ctx.Err() != nil {
			return
		}
		scans, err := store.QueryCveScans(n.db, pkg.Project, pkg.Name)
		if err != nil || len(scans) > 0 {
			continue
		}
		n.scanner.Enqueue(packageToRequest(pkg))
	}
}

func (n *NightlyScheduler) enqueueAll(ctx context.Context) {
	pkgs, err := store.QueryPublishedContainers(n.db)
	if err != nil {
		slog.Error("cve scheduler: query published containers", "err", err)
		return
	}
	for _, pkg := range pkgs {
		if ctx.Err() != nil {
			return
		}
		n.scanner.Enqueue(packageToRequest(pkg))
	}
}

func packageToRequest(pkg *model.Package) ScanRequest {
	primaryTag := ""
	if len(pkg.ContainerTags) > 0 {
		primaryTag = pkg.ContainerTags[0]
	}
	return ScanRequest{
		Project:    pkg.Project,
		Package:    pkg.Name,
		Tags:       pkg.Tags,
		ImageBase:  ImageBase(pkg.Project, pkg.Name),
		PrimaryTag: primaryTag,
		Targets:    SucceededTargets(pkg.Targets),
	}
}

// nextNightlyRun returns the next 02:00 UTC time.
func nextNightlyRun() time.Time {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
