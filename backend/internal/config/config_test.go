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

func TestLoadUnblockerDefaultsAndOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Unblocker.Enabled {
		t.Error("unblocker should be disabled by default")
	}
	if cfg.Unblocker.Threshold != 30*time.Minute {
		t.Errorf("default threshold = %v, want 30m", cfg.Unblocker.Threshold)
	}

	os.Setenv("UNBLOCKER_ENABLED", "true")
	os.Setenv("UNBLOCKER_THRESHOLD", "45m")
	defer os.Unsetenv("UNBLOCKER_ENABLED")
	defer os.Unsetenv("UNBLOCKER_THRESHOLD")

	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Unblocker.Enabled || cfg.Unblocker.Threshold != 45*time.Minute {
		t.Errorf("override: enabled=%v threshold=%v, want true/45m", cfg.Unblocker.Enabled, cfg.Unblocker.Threshold)
	}
}

func TestLoadIdleDefaultsAndOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Idle.Enabled {
		t.Error("idle mode should be enabled by default")
	}
	if cfg.Idle.Linger != 5*time.Minute {
		t.Errorf("default linger = %v, want 5m", cfg.Idle.Linger)
	}

	os.Setenv("IDLE_ENABLED", "false")
	os.Setenv("IDLE_LINGER", "10m")
	defer os.Unsetenv("IDLE_ENABLED")
	defer os.Unsetenv("IDLE_LINGER")

	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Idle.Enabled || cfg.Idle.Linger != 10*time.Minute {
		t.Errorf("override: enabled=%v linger=%v, want false/10m", cfg.Idle.Enabled, cfg.Idle.Linger)
	}
}

func TestLoadMetricsRetention(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 30*24*time.Hour {
		t.Errorf("default metrics retention = %v, want 720h", cfg.Store.MetricsRetention)
	}

	os.Setenv("METRICS_RETENTION", "60d")
	defer os.Unsetenv("METRICS_RETENTION")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 60*24*time.Hour {
		t.Errorf("override metrics retention = %v, want 1440h", cfg.Store.MetricsRetention)
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
