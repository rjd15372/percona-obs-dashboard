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
