// Package db owns the connection pool and the migration runner.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var coreMigrations embed.FS

type DB = pgxpool.Pool

// Connect opens the pool and waits for the database to accept queries.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		slog.Info("waiting for database", "error", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// MigrateCore applies the core schema.
func MigrateCore(ctx context.Context, pool *pgxpool.Pool) error {
	sub, err := fs.Sub(coreMigrations, "migrations")
	if err != nil {
		return err
	}
	return Migrate(ctx, pool, "core", sub)
}

// Migrate applies every *.sql file in fsys, in filename order, once.
//
// Capabilities bring their own migrations under their own namespace: deleting a
// capability folder removes its migrations with it, and the ones already
// applied simply stay recorded.
func Migrate(ctx context.Context, pool *pgxpool.Pool, namespace string, fsys fs.FS) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			namespace  text NOT NULL,
			name       text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (namespace, name)
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".sql" {
			names = append(names, e.Name())
		}
	}
	// Filename order is the execution order. Anything that reshuffles data
	// belongs in a file that sorts last.
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE namespace=$1 AND name=$2)`,
			namespace, name).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		// Two servers starting at the same moment must not both run the same
		// file. The lock is held until the transaction ends.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4711)`); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		var done bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE namespace=$1 AND name=$2)`,
			namespace, name).Scan(&done); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if done {
			_ = tx.Rollback(ctx)
			continue
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s/%s: %w", namespace, name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (namespace, name) VALUES ($1, $2)`,
			namespace, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("migration applied", "namespace", namespace, "name", name)
	}
	return nil
}
