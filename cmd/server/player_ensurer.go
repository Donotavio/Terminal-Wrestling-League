package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
)

type sqlPlayerEnsurer struct {
	repos *storage.SQLRepositories
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
		return "", fmt.Errorf("create player: %w", err)
	}
	return created.ID, nil
}
