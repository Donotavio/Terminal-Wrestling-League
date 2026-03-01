package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/matchmaking"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/ranking"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/telemetry"
)

func main() {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := storage.NewPool(ctx, storage.Config{DatabaseURL: cfg.DatabaseURL})
	if err != nil {
		log.Fatalf("database bootstrap failed: %v", err)
	}
	defer pool.Close()

	if err := storage.ApplyMigrations(ctx, pool, "db/migrations"); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	rankingSvc := ranking.NewGlicko2Service(ranking.DefaultConfig())
	repos := storage.NewSQLRepositories(pool, rankingSvc)
	lobbySvc := lobby.NewInMemoryService()
	metrics := telemetry.NewInMemoryCollector()
	matchSvc := matchmaking.NewInMemoryService(lobbySvc, repos, matchmaking.MatchConfig{
		QueueTimeout: cfg.QueueTimeout,
		TurnTimeout:  cfg.TurnTimeout,
		MaxTurns:     cfg.MaxTurns,
	}, metrics)

	srv, err := newSSHServer(cfg, lobbySvc, matchSvc, &sqlPlayerEnsurer{repos: repos}, metrics, log.Default())
	if err != nil {
		log.Fatalf("create ssh server: %v", err)
	}

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("ssh server stopped with error: %v", err)
	}
}
