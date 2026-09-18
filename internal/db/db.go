package db

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a connection pool to PostgreSQL and verifies connectivity.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

// Migrate applies the schema found in the given SQL source, split on
// semicolon-terminated statements. It is intentionally simple (no external
// migration framework) so the service has zero extra runtime dependencies.
func Migrate(ctx context.Context, pool *pgxpool.Pool, sqlSource string) error {
	statements := splitStatements(sqlSource)
	sort.Strings(statements) // deterministic but irrelevant ordering for a single file
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning migration tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, stmt := range splitStatements(sqlSource) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("executing migration statement: %w\nstatement: %s", err, stmt)
		}
	}
	return tx.Commit(ctx)
}

func splitStatements(sqlSource string) []string {
	// Strip line comments, then split on ';'.
	lines := strings.Split(sqlSource, "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned = append(cleaned, l)
	}
	return strings.Split(strings.Join(cleaned, "\n"), ";")
}
