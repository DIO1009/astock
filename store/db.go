// Package store manages the PostgreSQL connection pool and schema migration.
package store

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

const postSchemaMigrations = `
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_order_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_executions_event
    ON executions (order_id, execution_time, symbol, side);
`

// Config holds connection pool parameters.
// DSN format: postgres://user:password@host:5432/dbname?sslmode=disable
type Config struct {
	DSN             string
	MaxConns        int32         // default 10
	MinConns        int32         // default 1
	MaxConnLifetime time.Duration // default 30 min
	MaxConnIdleTime time.Duration // default 5 min
}

// Store wraps a pgxpool connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates a Store, validates the connection with a ping, and returns it.
// The caller must call Close() when done.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("store.Open: parse DSN: %w", err)
	}

	if cfg.MaxConns > 0 {
		pc.MaxConns = cfg.MaxConns
	} else {
		pc.MaxConns = 10
	}
	if cfg.MinConns > 0 {
		pc.MinConns = cfg.MinConns
	} else {
		pc.MinConns = 1
	}
	if cfg.MaxConnLifetime > 0 {
		pc.MaxConnLifetime = cfg.MaxConnLifetime
	} else {
		pc.MaxConnLifetime = 30 * time.Minute
	}
	if cfg.MaxConnIdleTime > 0 {
		pc.MaxConnIdleTime = cfg.MaxConnIdleTime
	} else {
		pc.MaxConnIdleTime = 5 * time.Minute
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("store.Open: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store.Open: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases all pool connections.
func (s *Store) Close() { s.pool.Close() }

// Migrate runs the embedded schema SQL to create all tables and indexes.
// Safe to call on every startup (all statements use IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("store.Migrate: %w", err)
	}
	if _, err := s.pool.Exec(ctx, postSchemaMigrations); err != nil {
		return fmt.Errorf("store.Migrate postSchemaMigrations: %w", err)
	}
	return nil
}
