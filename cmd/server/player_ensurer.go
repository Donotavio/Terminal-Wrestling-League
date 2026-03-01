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
	GetOrCreateProfile(ctx context.Context, playerID string) (storage.PlayerProfile, error)
}

type sqlPlayerEnsurer struct {
	repos playerRepository
}

func (e *sqlPlayerEnsurer) EnsurePlayerSession(ctx context.Context, handle string) (string, storage.PlayerProfile, error) {
	if e == nil || e.repos == nil {
		return "", storage.PlayerProfile{}, fmt.Errorf("player repository is not configured")
	}
	p, err := e.repos.GetByHandle(ctx, handle)
	if err == nil {
		profile, profileErr := e.repos.GetOrCreateProfile(ctx, p.ID)
		if profileErr != nil {
			return "", storage.PlayerProfile{}, fmt.Errorf("get or create profile: %w", profileErr)
		}
		return p.ID, profile, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", storage.PlayerProfile{}, fmt.Errorf("get player by handle: %w", err)
	}
	created, err := e.repos.Create(ctx, handle)
	if err != nil {
		if isUniqueViolation(err) {
			existing, lookupErr := e.repos.GetByHandle(ctx, handle)
			if lookupErr == nil {
				profile, profileErr := e.repos.GetOrCreateProfile(ctx, existing.ID)
				if profileErr != nil {
					return "", storage.PlayerProfile{}, fmt.Errorf("get or create profile: %w", profileErr)
				}
				return existing.ID, profile, nil
			}
			return "", storage.PlayerProfile{}, fmt.Errorf("create player race recovery lookup: %w", lookupErr)
		}
		return "", storage.PlayerProfile{}, fmt.Errorf("create player: %w", err)
	}
	profile, profileErr := e.repos.GetOrCreateProfile(ctx, created.ID)
	if profileErr != nil {
		return "", storage.PlayerProfile{}, fmt.Errorf("get or create profile: %w", profileErr)
	}
	return created.ID, profile, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
