package storage

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationNamePattern = regexp.MustCompile(`^(\d+)_([a-zA-Z0-9_\-]+)\.sql$`)

const (
	markerUp   = "-- +twl Up"
	markerDown = "-- +twl Down"
)

type migrationFile struct {
	Version  int64
	Name     string
	Path     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

// ApplyMigrations loads, parses and applies pending SQL migrations.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error {
	if pool == nil {
		return fmt.Errorf("nil pool")
	}
	if migrationsDir == "" {
		return fmt.Errorf("migrations dir is required")
	}

	migrations, err := loadMigrations(migrationsDir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return nil
	}

	if err := ensureSchemaMigrations(ctx, pool); err != nil {
		return err
	}

	applied, err := loadAppliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if _, exists := applied[m.Version]; exists {
			continue
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration tx %d: %w", m.Version, err)
		}

		if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("exec migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum, applied_at)
			 VALUES ($1, $2, $3, now())`,
			m.Version, m.Name, m.Checksum,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	return nil
}

func ensureSchemaMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	const sql = `
	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version BIGINT PRIMARY KEY,
	    name TEXT NOT NULL,
	    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	    checksum TEXT NOT NULL
	);`
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	return nil
}

func loadAppliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int64]struct{}, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied versions: %w", err)
	}
	defer rows.Close()

	versions := map[int64]struct{}{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan applied version: %w", err)
		}
		versions[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied versions: %w", err)
	}
	return versions, nil
}

func loadMigrations(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		matches := migrationNamePattern.FindStringSubmatch(name)
		if len(matches) != 3 {
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid migration version in %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		upSQL, downSQL, err := parseMigrationFile(path)
		if err != nil {
			return nil, fmt.Errorf("parse migration %s: %w", name, err)
		}
		migrations = append(migrations, migrationFile{
			Version:  version,
			Name:     matches[2],
			Path:     path,
			UpSQL:    upSQL,
			DownSQL:  downSQL,
			Checksum: checksumSQL(upSQL),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].Version)
		}
	}

	return migrations, nil
}

func parseMigrationFile(path string) (upSQL string, downSQL string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	section := ""
	upLines := make([]string, 0, 64)
	downLines := make([]string, 0, 32)
	for scanner.Scan() {
		line := scanner.Text()
		switch strings.TrimSpace(line) {
		case markerUp:
			section = "up"
			continue
		case markerDown:
			section = "down"
			continue
		}

		switch section {
		case "up":
			upLines = append(upLines, line)
		case "down":
			downLines = append(downLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if len(upLines) == 0 {
		return "", "", fmt.Errorf("missing %s block", markerUp)
	}

	up := strings.TrimSpace(strings.Join(upLines, "\n"))
	down := strings.TrimSpace(strings.Join(downLines, "\n"))
	if up == "" {
		return "", "", fmt.Errorf("empty up SQL")
	}
	return up, down, nil
}

func checksumSQL(sql string) string {
	h := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(h[:])
}
