package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
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

	migrations, err := loadMigrations(migrationDir(t))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	expected := len(migrations)
	if count != expected {
		t.Fatalf("schema_migrations count = %d, want %d", count, expected)
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

func TestFinalizeMatchWithReplayRoundtripIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	p1, err := repos.Create(ctx, "eve")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "frank")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Second)
	winner := p1.ID
	replayWrite := buildReplayWrite(p1.ID, p2.ID)

	result, err := repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   &winner,
		ResultType: MatchResultKO,
		StartedAt:  start,
		EndedAt:    end,
		Replay:     &replayWrite,
	})
	if err != nil {
		t.Fatalf("finalize match with replay: %v", err)
	}

	var replayCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM match_replays`).Scan(&replayCount); err != nil {
		t.Fatalf("count match_replays: %v", err)
	}
	if replayCount != 1 {
		t.Fatalf("match_replays count = %d, want 1", replayCount)
	}

	var turnCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM match_replay_turns`).Scan(&turnCount); err != nil {
		t.Fatalf("count match_replay_turns: %v", err)
	}
	if turnCount != len(replayWrite.Turns) {
		t.Fatalf("match_replay_turns count = %d, want %d", turnCount, len(replayWrite.Turns))
	}

	got, err := repos.GetMatchReplay(ctx, result.Match.ID)
	if err != nil {
		t.Fatalf("get match replay: %v", err)
	}
	if got.MatchID != result.Match.ID {
		t.Fatalf("match id = %s, want %s", got.MatchID, result.Match.ID)
	}
	if got.Seed != replayWrite.Seed {
		t.Fatalf("seed = %d, want %d", got.Seed, replayWrite.Seed)
	}
	if !reflect.DeepEqual(got.InitialState, replayWrite.InitialState) {
		t.Fatalf("initial state mismatch")
	}
	if len(got.Turns) != len(replayWrite.Turns) {
		t.Fatalf("replay turns len = %d, want %d", len(got.Turns), len(replayWrite.Turns))
	}
	for i := range got.Turns {
		if got.Turns[i].Turn != replayWrite.Turns[i].Turn {
			t.Fatalf("turn[%d].turn = %d, want %d", i, got.Turns[i].Turn, replayWrite.Turns[i].Turn)
		}
		if got.Turns[i].RelativeMS != replayWrite.Turns[i].RelativeMS {
			t.Fatalf("turn[%d].relative_ms = %d, want %d", i, got.Turns[i].RelativeMS, replayWrite.Turns[i].RelativeMS)
		}
		if got.Turns[i].Checksums != replayWrite.Turns[i].Checksums {
			t.Fatalf("turn[%d].checksums mismatch", i)
		}
		if !reflect.DeepEqual(got.Turns[i].Inputs, replayWrite.Turns[i].Inputs) {
			t.Fatalf("turn[%d].inputs mismatch", i)
		}
	}
}

func TestFinalizeMatchReplayFailureRollsBackTransactionIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	p1, err := repos.Create(ctx, "gina")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "henry")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 2, 10, 20, 0, 0, time.UTC)
	end := start.Add(45 * time.Second)
	winner := p1.ID
	replayWrite := buildReplayWrite(p1.ID, p2.ID)
	// Duplicate turn id causes PK violation in match_replay_turns.
	replayWrite.Turns = append(replayWrite.Turns, replayWrite.Turns[0])

	_, err = repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   &winner,
		ResultType: MatchResultKO,
		StartedAt:  start,
		EndedAt:    end,
		Replay:     &replayWrite,
	})
	if err == nil {
		t.Fatalf("expected finalize match to fail with invalid replay")
	}

	assertTableCount(t, pool, "matches", 0)
	assertTableCount(t, pool, "match_results", 0)
	assertTableCount(t, pool, "player_ratings", 0)
	assertTableCount(t, pool, "match_replays", 0)
	assertTableCount(t, pool, "match_replay_turns", 0)
}

func buildReplayWrite(p1ID, p2ID string) MatchReplayWrite {
	p1, _ := combat.NewFighter(p1ID, combat.ArchetypeBalanced)
	p2, _ := combat.NewFighter(p2ID, combat.ArchetypeTechnician)
	initial := combat.NewMatchState(p1, p2)
	return MatchReplayWrite{
		Seed:         424242,
		InitialState: initial,
		Turns: []MatchReplayTurnWrite{
			{
				Turn:       1,
				RelativeMS: 25,
				Inputs: []combat.TurnInput{
					{PlayerID: p1ID, Action: combat.ActionStrike, Target: combat.ZoneHead, DecisionMS: 100},
					{PlayerID: p2ID, Action: combat.ActionBlock, Target: combat.ZoneTorso, DecisionMS: 115},
				},
				Checksums: combat.TurnChecksum{StateHash: 11, RollHash: 12},
			},
			{
				Turn:       2,
				RelativeMS: 50,
				Inputs: []combat.TurnInput{
					{PlayerID: p1ID, Action: combat.ActionGrapple, Target: combat.ZoneTorso, DecisionMS: 135},
					{PlayerID: p2ID, Action: combat.ActionDodge, Target: combat.ZoneLegs, DecisionMS: 140},
				},
				Checksums: combat.TurnChecksum{StateHash: 21, RollHash: 22},
			},
		},
	}
}

func assertTableCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var got int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, pgx.Identifier{table}.Sanitize())
	if err := pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
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
