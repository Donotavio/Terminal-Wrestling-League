package storage

import (
	"fmt"
	"time"
)

// Config controls PostgreSQL connection settings.
type Config struct {
	DatabaseURL       string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

func (c Config) withDefaults() Config {
	cfg := c
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 8
	}
	if cfg.MinConns < 0 {
		cfg.MinConns = 0
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = 30 * time.Minute
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 5 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	return cfg
}

func (c Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("database url is required")
	}
	if c.MaxConns < c.MinConns {
		return fmt.Errorf("max conns must be >= min conns")
	}
	return nil
}
