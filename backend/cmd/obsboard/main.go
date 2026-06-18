package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/percona/obs-dashboard/internal/api"
	"github.com/percona/obs-dashboard/internal/config"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/mq"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/worker"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	db, err := store.Open(cfg.Store.DBPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	obsClient := obs.NewClient(cfg.OBS.BaseURL, cfg.OBS.Username, cfg.OBS.Password)
	h := hub.New()

	activePkgs, err := store.GetActivePackages(db)
	if err != nil {
		return fmt.Errorf("seed working set: %w", err)
	}
	ws := workingset.New(cfg.WorkerPool.QueueSize)
	ws.Seed(activePkgs)

	devTasks := []worker.Task{
		obs.PackageTypeTask{},
		obs.BuildStateTask{},
		obs.PublishStateTask{},
		obs.VersionTask{},
		obs.ContainerTagsTask{},
		obs.BlockedReasonTask{},
		obs.BuildReasonTask{},
	}
	releaseTasks := []worker.Task{
		obs.PackageTypeTask{},
		obs.BinariesCheckTask{},
	}
	pool := worker.NewPool(cfg.WorkerPool.Size, devTasks, releaseTasks, obsClient, db, h, ws)
	pool.Start(ctx)
	ws.StartScheduler(ctx, cfg.WorkerPool.PollInterval)

	poller := obs.NewPoller(obsClient, db, cfg.Poller.Interval, h, ws, cfg.OBSRoot)
	consumer := mq.NewConsumer(cfg.MQ.URL, db, h, obsClient, ws, cfg.OBSRoot)

	go poller.Run(ctx)
	go consumer.Run(ctx)
	go runPruner(ctx, db, cfg.Poller.Interval, cfg.Store.EventRetention)

	router := api.NewRouter(db, h, obsClient)

	var handler http.Handler = router
	if cfg.Server.FrontendDir != "" {
		fs := http.FileServer(http.Dir(cfg.Server.FrontendDir))
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
				router.ServeHTTP(w, r)
			} else {
				fs.ServeHTTP(w, r)
			}
		})
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("http shutdown", "err", err)
		}
	}()

	slog.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

func runPruner(ctx context.Context, db *sql.DB, interval time.Duration, retention time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-retention)
			if err := store.PruneEvents(db, cutoff); err != nil {
				slog.Error("prune events", "err", err)
			}
		}
	}
}
