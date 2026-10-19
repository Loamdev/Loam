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

// liveEnv is a connection to a database, with its schema already read and
// ordered. seed and reset both need exactly this.
type liveEnv struct {
	// State is what `up` wrote to .loam/state.json, or nil when the database
	// was named directly and loam never created an environment for it.
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

// openEnvironment works out which database to talk to and connects to it.
//
// It deliberately does not touch Docker: a command that only reads or writes
// data has no business starting or stopping containers, and this way it works
// even when the engine is busy.
//
// The recorded environment is a fallback, not a requirement. When --db-url or
// database.url in loam.yml says where the database is, no state is needed and
// liveEnv.State is left nil. That is what lets `seed` and `reset` run against
// any reachable Postgres - in Kubernetes, on RDS, or one a colleague started by
// hand - without loam having created it.
func openEnvironment(ctx context.Context, withSchema bool) (*liveEnv, error) {
	cfg, err := config.Load(opts.dir)
	if err != nil {
		return nil, Failf("Reading "+config.FileName, err,
			"fix the file, or delete it: loam works with no config at all")
	}

	// Precedence: the flag typed on this run, then the config file, then
	// whatever `up` recorded. The first two are explicit enough that a missing
	// state file is not an error.
	dbURL := opts.dbURL
	if dbURL == "" {
		dbURL = cfg.Database.URL
	}

	// Load the state regardless, so that a seed run inside a loam environment
	// still updates the recorded row count even when --db-url was given.
	state, stateErr := env.LoadState(opts.dir)
	if stateErr != nil {
		if dbURL == "" {
			if errors.Is(stateErr, os.ErrNotExist) {
				return nil, Failf("Finding the database", stateErr,
					"run 'loam up' first, or point loam at an existing database with --db-url")
			}
			return nil, Failf("Reading the environment state", stateErr,
				"delete "+env.StatePath(opts.dir)+" if it is corrupt")
		}
		// Standalone: there is no environment to update afterwards.
		state = nil
	} else if dbURL == "" {
		dbURL = state.DBURL
	}

	if dbURL == "" {
		return nil, Failf("Finding the database",
			errors.New("no database URL was recorded for this environment"),
			"pass --db-url, or run 'loam down' and 'loam up' again")
	}

	pool, err := db.Connect(ctx, dbURL, db.ReadyTimeout)
	if err != nil {
		// Pointing at someone else's database is a different mistake from
		// losing your own environment, so the two get different advice.
		hint := "check the database is running and reachable at the URL you gave"
		if state != nil {
			hint = "check the environment is still up with 'loam status'"
		}
		return nil, Failf("Connecting to the database", err, hint)
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
