package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

type stubPlayerRepo struct {
	getByHandleFn func(ctx context.Context, handle string) (storage.Player, error)
	createFn      func(ctx context.Context, handle string) (storage.Player, error)
	getProfileFn  func(ctx context.Context, playerID string) (storage.PlayerProfile, error)
}

func (s *stubPlayerRepo) GetByHandle(ctx context.Context, handle string) (storage.Player, error) {
	if s.getByHandleFn == nil {
		return storage.Player{}, errors.New("unexpected GetByHandle call")
	}
	return s.getByHandleFn(ctx, handle)
}

func (s *stubPlayerRepo) Create(ctx context.Context, handle string) (storage.Player, error) {
	if s.createFn == nil {
		return storage.Player{}, errors.New("unexpected Create call")
	}
	return s.createFn(ctx, handle)
}

func (s *stubPlayerRepo) GetOrCreateProfile(ctx context.Context, playerID string) (storage.PlayerProfile, error) {
	if s.getProfileFn == nil {
		return storage.PlayerProfile{}, errors.New("unexpected GetOrCreateProfile call")
	}
	return s.getProfileFn(ctx, playerID)
}

func TestEnsurePlayerSessionRecoversFromUniqueViolation(t *testing.T) {
	lookupCalls := 0
	repo := &stubPlayerRepo{}
	repo.getByHandleFn = func(_ context.Context, handle string) (storage.Player, error) {
		lookupCalls++
		if lookupCalls == 1 {
			return storage.Player{}, storage.ErrNotFound
		}
		return storage.Player{ID: "player-alice", Handle: handle}, nil
	}
	repo.createFn = func(_ context.Context, _ string) (storage.Player, error) {
		return storage.Player{}, fmt.Errorf("insert player: %w", &pgconn.PgError{Code: "23505"})
	}
	repo.getProfileFn = func(_ context.Context, playerID string) (storage.PlayerProfile, error) {
		return storage.PlayerProfile{PlayerID: playerID}, nil
	}

	ensurer := &sqlPlayerEnsurer{repos: repo}
	id, profile, err := ensurer.EnsurePlayerSession(context.Background(), "alice")
	if err != nil {
		t.Fatalf("EnsurePlayerSession returned error: %v", err)
	}
	if id != "player-alice" {
		t.Fatalf("player id = %s, want player-alice", id)
	}
	if profile.PlayerID != id {
		t.Fatalf("profile player id = %s, want %s", profile.PlayerID, id)
	}
	if lookupCalls != 2 {
		t.Fatalf("GetByHandle calls = %d, want 2", lookupCalls)
	}
}

func TestEnsurePlayerSessionReturnsNonUniqueCreateError(t *testing.T) {
	createErr := errors.New("write failed")
	repo := &stubPlayerRepo{}
	repo.getByHandleFn = func(_ context.Context, _ string) (storage.Player, error) {
		return storage.Player{}, storage.ErrNotFound
	}
	repo.createFn = func(_ context.Context, _ string) (storage.Player, error) {
		return storage.Player{}, createErr
	}

	ensurer := &sqlPlayerEnsurer{repos: repo}
	_, _, err := ensurer.EnsurePlayerSession(context.Background(), "alice")
	if err == nil {
		t.Fatalf("expected EnsurePlayerSession to fail")
	}
	if !errors.Is(err, createErr) {
		t.Fatalf("expected wrapped create error, got %v", err)
	}
	if !strings.Contains(err.Error(), "create player") {
		t.Fatalf("error should mention create player, got %v", err)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505"})) {
		t.Fatalf("expected unique violation to be detected")
	}
	if isUniqueViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40001"})) {
		t.Fatalf("did not expect non-unique pg error to match")
	}
}
