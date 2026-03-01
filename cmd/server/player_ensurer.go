package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

type playerRepository interface {
	GetByHandle(ctx context.Context, handle string) (storage.Player, error)
	Create(ctx context.Context, handle string) (storage.Player, error)
}

type sqlPlayerEnsurer struct {
	repos playerRepository
}

func (e *sqlPlayerEnsurer) EnsurePlayer(ctx context.Context, handle string) (string, error) {
	if e == nil || e.repos == nil {
		return "", fmt.Errorf("player repository is not configured")
	}
	p, err := e.repos.GetByHandle(ctx, handle)
	if err == nil {
		return p.ID, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", fmt.Errorf("get player by handle: %w", err)
	}
	created, err := e.repos.Create(ctx, handle)
	if err != nil {
		if isUniqueViolation(err) {
			existing, lookupErr := e.repos.GetByHandle(ctx, handle)
			if lookupErr == nil {
				return existing.ID, nil
			}
			return "", fmt.Errorf("create player race recovery lookup: %w", lookupErr)
		}
		return "", fmt.Errorf("create player: %w", err)
	}
	return created.ID, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
