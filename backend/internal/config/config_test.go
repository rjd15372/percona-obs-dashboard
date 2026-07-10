package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	os.Setenv("OBS_USERNAME", "testuser")
	os.Setenv("OBS_PASSWORD", "testpass")
	defer os.Unsetenv("OBS_USERNAME")
	defer os.Unsetenv("OBS_PASSWORD")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poller.Interval != 2*time.Minute {
		t.Errorf("expected 2m, got %v", cfg.Poller.Interval)
	}
	if cfg.Store.EventRetention != 7*24*time.Hour {
		t.Errorf("expected 168h, got %v", cfg.Store.EventRetention)
	}
}

func TestLoadMissingUsername(t *testing.T) {
	os.Unsetenv("OBS_USERNAME")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing OBS_USERNAME")
	}
}

func TestLoadEnvOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	os.Setenv("POLL_INTERVAL", "2m")
	defer os.Unsetenv("OBS_USERNAME")
	defer os.Unsetenv("POLL_INTERVAL")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poller.Interval != 2*time.Minute {
		t.Errorf("expected 2m override, got %v", cfg.Poller.Interval)
	}
}

func TestTelemetryDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Interval != 60*time.Second {
		t.Fatalf("interval = %v, want 60s", cfg.Telemetry.Interval)
	}
	if cfg.Telemetry.Enabled {
		t.Fatalf("enabled = true, want false by default")
	}
}

func TestTrafficReductionDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 5*time.Minute {
		t.Errorf("BackoffMax = %v, want 5m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 4 {
		t.Errorf("BatchThreshold = %d, want 4", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.OBS.MinuteRequestBudget != 60 {
		t.Errorf("MinuteRequestBudget = %d, want 60", cfg.OBS.MinuteRequestBudget)
	}
}

func TestTrafficReductionEnvOverride(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("WORKER_POOL_BACKOFF_MAX", "2m")
	t.Setenv("WORKER_POOL_BATCH_THRESHOLD", "8")
	t.Setenv("OBS_MINUTE_REQUEST_BUDGET", "30")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 2*time.Minute {
		t.Errorf("BackoffMax = %v, want 2m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 8 {
		t.Errorf("BatchThreshold = %d, want 8", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.OBS.MinuteRequestBudget != 30 {
		t.Errorf("MinuteRequestBudget = %d, want 30", cfg.OBS.MinuteRequestBudget)
	}
}
