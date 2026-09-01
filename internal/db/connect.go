// Package db connects to the environment's Postgres database, reads its schema,
// and orders the tables so that parents can be seeded before their children.
package db

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReadyTimeout is how long Connect waits for Postgres to start accepting
// connections. A freshly created container needs a few seconds, and on a cold
// machine the initdb scripts can take longer still (build spec section 6, phase 2).
const ReadyTimeout = 30 * time.Second

// retryInterval is the gap between connection attempts while waiting.
const retryInterval = time.Second

// Connect opens a pool and waits for the database to accept queries.
//
// The retry loop is mandatory, not an optimization: a container that has just
// been started refuses connections for a second or two, and without this every
// `up` would be a coin flip.
func Connect(ctx context.Context, rawurl string, timeout time.Duration) (*pgxpool.Pool, error) {
	if timeout <= 0 {
		timeout = ReadyTimeout
	}

	cfg, err := pgxpool.ParseConfig(rawurl)
	if err != nil {
		// Deliberately not echoing rawurl: it carries the password.
		return nil, fmt.Errorf("invalid database URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for attempt := 1; ; attempt++ {
		// A ping proves the socket is open; a real query proves the server has
		// finished starting up and will actually answer.
		if err := pool.Ping(ctx); err == nil {
			var one int
			if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err == nil {
				return pool, nil
			}
			lastErr = err
		} else {
			lastErr = err
		}

		if ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("waiting for Postgres: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("postgres did not accept connections within %s (%d attempts): %w",
				timeout, attempt, lastErr)
		}

		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("waiting for Postgres: %w", ctx.Err())
		case <-time.After(retryInterval):
		}
	}
}

// TableNames returns the tables in the public schema, sorted.
func TableNames(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}

	sort.Strings(names)
	return names, nil
}
