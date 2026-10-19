package cli

import (
	"context"
	"time"

	"github.com/loamdev/loam/internal/db"
	"github.com/loamdev/loam/internal/env"
	"github.com/spf13/cobra"
)

// resetTimeout bounds a reset. It is the same order as seeding, because that is
// what most of the work is.
const resetTimeout = 10 * time.Minute

// newResetCmd builds `loam reset`: empty the database and seed it again.
func newResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Truncate every table and seed fresh data",
		Long: `Empty every table in the running environment's database and generate a fresh
set of synthetic rows, without restarting any container.

This is the fast way back to a known state: no image pulls, no container
restart, just new data.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReset(cmd.Context())
		},
	}
}

// runReset empties every table and seeds it again.
func runReset(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()

	live, err := openEnvironment(ctx, true)
	if err != nil {
		return err
	}
	defer live.Close()

	if len(live.Schema.Tables) == 0 {
		Warn("the database has no tables in the public schema - there is nothing to reset")
		return nil
	}

	// One TRUNCATE ... CASCADE clears the whole graph regardless of foreign key
	// order, and RESTART IDENTITY rewinds the sequences so the fresh data starts
	// from 1 again.
	if err := db.Truncate(ctx, live.Pool, live.Schema.Order); err != nil {
		return Failf("Emptying the tables", err,
			"another connection may hold a lock; close it and retry")
	}
	Step("Emptied %s", countTables(len(live.Schema.Order)))

	result, err := seedEnvironment(ctx, live.Pool, live.Schema, live.Config)
	if err != nil {
		return err
	}

	live.State.Rows = result.TotalRows
	if err := env.SaveState(opts.dir, live.State); err != nil {
		Warn("could not record the new row count: %v", err)
	}

	printEndpoints(live.State)
	Ready()
	return nil
}
