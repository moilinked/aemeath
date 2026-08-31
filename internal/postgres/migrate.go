package postgres

import (
	"context"
	"embed"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate 按文件名顺序应用尚未执行的 SQL 迁移。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("postgres pool is required")
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		contents, err := migrationFiles.ReadFile(path.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, pool, version, string(contents)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version int, sql string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return err
	}

	var applied bool
	if err := tx.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		version,
	).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return tx.Commit(ctx)
	}

	for _, statement := range splitSQLStatements(sql) {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`,
		version,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func migrationVersion(name string) (int, error) {
	base := strings.TrimSuffix(name, ".sql")
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("migration %s must be named like 001_description.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %s has an invalid version prefix", name)
	}
	return version, nil
}

func splitSQLStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		statement := strings.TrimSpace(part)
		if statement == "" {
			continue
		}
		statements = append(statements, statement)
	}
	return statements
}
