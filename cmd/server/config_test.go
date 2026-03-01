package main

import (
	"testing"
	"time"
)

func TestLoadConfigFromEnvM5Defaults(t *testing.T) {
	t.Setenv("TWL_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("TWL_SSH_USERS", "alice:secret")
	t.Setenv("TWL_SSH_ADDR", "")
	t.Setenv("TWL_WATCH_WAIT_TIMEOUT_SEC", "")
	t.Setenv("TWL_SPECTATOR_MAX_PER_MATCH", "")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv returned error: %v", err)
	}
	if cfg.WatchWaitTimeout != 120*time.Second {
		t.Fatalf("watch wait timeout = %s, want 120s", cfg.WatchWaitTimeout)
	}
	if cfg.SpectatorMaxPerMatch != 20 {
		t.Fatalf("spectator cap = %d, want 20", cfg.SpectatorMaxPerMatch)
	}
}

func TestLoadConfigFromEnvM5Overrides(t *testing.T) {
	t.Setenv("TWL_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("TWL_SSH_USERS", "alice:secret")
	t.Setenv("TWL_WATCH_WAIT_TIMEOUT_SEC", "30")
	t.Setenv("TWL_SPECTATOR_MAX_PER_MATCH", "7")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv returned error: %v", err)
	}
	if cfg.WatchWaitTimeout != 30*time.Second {
		t.Fatalf("watch wait timeout = %s, want 30s", cfg.WatchWaitTimeout)
	}
	if cfg.SpectatorMaxPerMatch != 7 {
		t.Fatalf("spectator cap = %d, want 7", cfg.SpectatorMaxPerMatch)
	}
}
