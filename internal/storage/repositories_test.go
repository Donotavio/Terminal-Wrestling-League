package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParseMigrationFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "0001_test.sql")
	content := strings.Join([]string{
		"-- +twl Up",
		"CREATE TABLE hello (id INT);",
		"",
		"-- +twl Down",
		"DROP TABLE hello;",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	up, down, err := parseMigrationFile(path)
	if err != nil {
		t.Fatalf("parseMigrationFile: %v", err)
	}
	if !strings.Contains(up, "CREATE TABLE hello") {
		t.Fatalf("up SQL was not parsed correctly: %q", up)
	}
	if !strings.Contains(down, "DROP TABLE hello") {
		t.Fatalf("down SQL was not parsed correctly: %q", down)
	}
}

func TestParseMigrationFileMissingUp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "0001_bad.sql")
	content := strings.Join([]string{
		"-- +twl Down",
		"DROP TABLE hello;",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	_, _, err := parseMigrationFile(path)
	if err == nil {
		t.Fatalf("expected error for missing up block")
	}
}

func TestApplyMigrationsIdempotentIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations first run: %v", err)
	}
	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations second run: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations count = %d, want 1", count)
	}
}

func TestFinalizeMatchTransactionIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	p1, err := repos.Create(ctx, "alice")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "bob")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	winner := p1.ID
	result, err := repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   &winner,
		ResultType: MatchResultKO,
		StartedAt:  start,
		EndedAt:    end,
	})
	if err != nil {
		t.Fatalf("finalize match: %v", err)
	}
	if result.Match.ID == "" {
		t.Fatalf("expected match id")
	}

	var matchResultsCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM match_results`).Scan(&matchResultsCount); err != nil {
		t.Fatalf("count match_results: %v", err)
	}
	if matchResultsCount != 2 {
		t.Fatalf("match_results count = %d, want 2", matchResultsCount)
	}

	var ratingsCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM player_ratings`).Scan(&ratingsCount); err != nil {
		t.Fatalf("count player_ratings: %v", err)
	}
	if ratingsCount != 2 {
		t.Fatalf("player_ratings count = %d, want 2", ratingsCount)
	}

	rows, err := pool.Query(ctx, `SELECT player_id, score FROM match_results ORDER BY player_id`)
	if err != nil {
		t.Fatalf("query scores: %v", err)
	}
	defer rows.Close()

	scores := map[string]float64{}
	for rows.Next() {
		var playerID string
		var score float64
		if err := rows.Scan(&playerID, &score); err != nil {
			t.Fatalf("scan score: %v", err)
		}
		scores[playerID] = score
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate scores: %v", err)
	}
	if scores[p1.ID] != 1.0 || scores[p2.ID] != 0.0 {
		t.Fatalf("unexpected scores: p1=%.1f p2=%.1f", scores[p1.ID], scores[p2.ID])
	}
}

func TestFinalizeMatchDrawIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	p1, err := repos.Create(ctx, "charlie")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "dave")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 1, 12, 10, 0, 0, time.UTC)
	end := start.Add(1 * time.Minute)
	_, err = repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   nil,
		ResultType: MatchResultDraw,
		StartedAt:  start,
		EndedAt:    end,
	})
	if err != nil {
		t.Fatalf("finalize draw: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT score FROM match_results ORDER BY player_id`)
	if err != nil {
		t.Fatalf("query draw scores: %v", err)
	}
	defer rows.Close()

	var found int
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			t.Fatalf("scan draw score: %v", err)
		}
		found++
		if score != 0.5 {
			t.Fatalf("expected draw score 0.5, got %.1f", score)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate draw scores: %v", err)
	}
	if found != 2 {
		t.Fatalf("draw score rows = %d, want 2", found)
	}
}

func migrationDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(root, "db", "migrations")
}

func newIsolatedPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create admin pool: %v", err)
	}

	schema := fmt.Sprintf("twl_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		admin.Close()
		t.Fatalf("create isolated pool: %v", err)
	}

	cleanup := func() {
		pool.Close()
		_, _ = admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		admin.Close()
	}
	return pool, cleanup
}
