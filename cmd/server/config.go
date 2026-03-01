package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the SSH authoritative server.
type Config struct {
	DatabaseURL          string
	SSHAddr              string
	SSHUsers             map[string]string
	QueueTimeout         time.Duration
	TurnTimeout          time.Duration
	MaxTurns             int
	WatchWaitTimeout     time.Duration
	SpectatorMaxPerMatch int
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		DatabaseURL: strings.TrimSpace(os.Getenv("TWL_DATABASE_URL")),
		SSHAddr:     strings.TrimSpace(os.Getenv("TWL_SSH_ADDR")),
	}
	if cfg.SSHAddr == "" {
		cfg.SSHAddr = ":2222"
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("TWL_DATABASE_URL is required")
	}

	usersRaw := strings.TrimSpace(os.Getenv("TWL_SSH_USERS"))
	users, err := parseUsers(usersRaw)
	if err != nil {
		return Config{}, err
	}
	cfg.SSHUsers = users

	queueTimeoutSec, err := parseIntEnvDefault("TWL_QUEUE_TIMEOUT_SEC", 45)
	if err != nil {
		return Config{}, err
	}
	turnTimeoutSec, err := parseIntEnvDefault("TWL_TURN_TIMEOUT_SEC", 5)
	if err != nil {
		return Config{}, err
	}
	maxTurns, err := parseIntEnvDefault("TWL_MAX_TURNS", 120)
	if err != nil {
		return Config{}, err
	}
	watchWaitTimeoutSec, err := parseIntEnvDefault("TWL_WATCH_WAIT_TIMEOUT_SEC", 120)
	if err != nil {
		return Config{}, err
	}
	spectatorMaxPerMatch, err := parseIntEnvDefault("TWL_SPECTATOR_MAX_PER_MATCH", 20)
	if err != nil {
		return Config{}, err
	}
	if maxTurns <= 0 {
		return Config{}, fmt.Errorf("TWL_MAX_TURNS must be > 0")
	}

	cfg.QueueTimeout = time.Duration(queueTimeoutSec) * time.Second
	cfg.TurnTimeout = time.Duration(turnTimeoutSec) * time.Second
	cfg.MaxTurns = maxTurns
	cfg.WatchWaitTimeout = time.Duration(watchWaitTimeoutSec) * time.Second
	cfg.SpectatorMaxPerMatch = spectatorMaxPerMatch
	return cfg, nil
}

func parseUsers(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, fmt.Errorf("TWL_SSH_USERS is required (format user:pass,user2:pass2)")
	}
	pairs := strings.Split(raw, ",")
	users := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid TWL_SSH_USERS entry %q", pair)
		}
		user := strings.TrimSpace(parts[0])
		pass := parts[1]
		if user == "" || pass == "" {
			return nil, fmt.Errorf("invalid TWL_SSH_USERS entry %q", pair)
		}
		users[user] = pass
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("TWL_SSH_USERS must contain at least one user")
	}
	return users, nil
}

func parseIntEnvDefault(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be > 0", name)
	}
	return v, nil
}
