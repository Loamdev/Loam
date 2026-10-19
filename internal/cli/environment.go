package cli

import (
	"context"
	"errors"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/loamdev/loam/internal/config"
	"github.com/loamdev/loam/internal/db"
	"github.com/loamdev/loam/internal/env"
)

// liveEnv is a connection to the environment `up` recorded, with its schema
// already read and ordered. seed, reset, and status all need exactly this.
type liveEnv struct {
	// State is what `up` wrote to .loam/state.json.
	State *env.State
	// Config is the optional loam.yml, or defaults.
	Config *config.Config
	// Pool is an open connection pool. Close releases it.
	Pool *pgxpool.Pool
	// Schema is the introspected, topologically ordered public schema.
	Schema *db.Schema
}

// Close releases the connection pool.
func (e *liveEnv) Close() {
	if e != nil && e.Pool != nil {
		e.Pool.Close()
	}
}

// openEnvironment loads the recorded environment and connects to its database.
//
// It deliberately does not touch Docker: a command that only reads or writes
// data has no business starting or stopping containers, and this way it works
// even when the engine is busy.
func openEnvironment(ctx context.Context, withSchema bool) (*liveEnv, error) {
	state, err := env.LoadState(opts.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Failf("Reading the environment state", err, "run 'loam up' first")
		}
		return nil, Failf("Reading the environment state", err,
			"delete "+env.StatePath(opts.dir)+" if it is corrupt")
	}

	cfg, err := config.Load(opts.dir)
	if err != nil {
		return nil, Failf("Reading "+config.FileName, err,
			"fix the file, or delete it: loam works with no config at all")
	}

	// A flag or config override still wins over what `up` recorded, so a moved
	// database can be reached without tearing the environment down.
	dbURL := state.DBURL
	if opts.dbURL != "" {
		dbURL = opts.dbURL
	} else if cfg.Database.URL != "" {
		dbURL = cfg.Database.URL
	}
	if dbURL == "" {
		return nil, Failf("Finding the database",
			errors.New("no database URL was recorded for this environment"),
			"pass --db-url, or run 'loam down' and 'loam up' again")
	}

	pool, err := db.Connect(ctx, dbURL, db.ReadyTimeout)
	if err != nil {
		return nil, Failf("Connecting to the database", err,
			"check the environment is still up with 'loam status'")
	}

	live := &liveEnv{State: state, Config: cfg, Pool: pool}
	if !withSchema {
		return live, nil
	}

	schema, err := db.Introspect(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, Failf("Reading the schema", err, "check the database is fully initialized")
	}
	warnings, err := db.TopoSort(schema)
	if err != nil {
		pool.Close()
		return nil, Failf("Ordering the tables", err,
			"the schema has foreign keys that cannot be satisfied in any order")
	}
	for _, w := range warnings {
		Warn("%s", w)
	}

	live.Schema = schema
	return live, nil
}
