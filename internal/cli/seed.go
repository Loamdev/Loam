package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivansh-source/stateful-playwright/internal/config"
	"github.com/shivansh-source/stateful-playwright/internal/db"
	"github.com/shivansh-source/stateful-playwright/internal/env"
	"github.com/shivansh-source/stateful-playwright/internal/seed"
	"github.com/spf13/cobra"
)

// seedOptions holds the flags specific to `playbrige seed`.
type seedOptions struct {
	truncate bool // --truncate: empty every table before generating rows
}

// seedOpts is the flag state for `playbrige seed`, rebound on every NewRootCmd.
var seedOpts seedOptions

// newSeedCmd builds `playbrige seed`: seed the already-running environment.
func newSeedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Seed the running environment's database",
		Long: `Generate synthetic, foreign-key-correct rows into the database of an
environment that is already up. Rows are appended unless --truncate is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSeed(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&seedOpts.truncate, "truncate", false, "empty every table before seeding")

	return cmd
}

// seedEnvironment generates and loads data for an already-ordered schema, and
// prints the summary line from build spec section 7.
func seedEnvironment(ctx context.Context, pool *pgxpool.Pool, schema *db.Schema, cfg *config.Config) (*seed.Result, error) {
	if len(schema.Tables) == 0 {
		return &seed.Result{RowsByTable: map[string]int{}}, nil
	}

	// Best effort: a column whose permitted values cannot be read just falls
	// back to a distribution profile, so this must never fail the run.
	allowed, err := db.AllowedValues(ctx, pool, schema)
	if err != nil {
		Warn("could not read enum and check constraints, using generic value profiles: %v", err)
		allowed = map[string][]string{}
	}

	result, err := seed.Seed(ctx, pool, schema, seedOptionsFrom(cfg, allowed))
	if err != nil {
		return nil, Failf("Seeding", err,
			"run 'playbrige logs db' to see what Postgres rejected")
	}
	for _, w := range result.Warnings {
		Warn("%s", w)
	}

	Step("Seeded %s across %s in %s",
		countRows(result.TotalRows), countTables(len(result.RowsByTable)), formatDuration(result.Duration))
	return result, nil
}

// seedOptionsFrom builds generation options from config, the --rows flag, and
// the constraints read out of the database.
func seedOptionsFrom(cfg *config.Config, allowed map[string][]string) seed.Options {
	o := seed.Options{
		DefaultRows:   config.DefaultRows,
		AllowedValues: allowed,
		// A fixed default seed makes two runs of the same schema produce the
		// same data, which is what makes a bug reproducible.
		Seed: defaultSeedValue,
	}
	if cfg != nil {
		if cfg.Seed.DefaultRows > 0 {
			o.DefaultRows = cfg.Seed.DefaultRows
		}
		o.TableRows = cfg.Seed.Tables
		o.Distributions = cfg.Seed.Distributions
	}
	// An explicit flag beats configuration.
	if opts.rows > 0 {
		o.DefaultRows = opts.rows
	}
	return o
}

// defaultSeedValue is the rng seed used when none is given, chosen so runs are
// reproducible rather than merely random.
const defaultSeedValue = 1

// countRows renders a row count with thousands separators and the right plural.
func countRows(n int) string {
	if n == 1 {
		return "1 row"
	}
	return withThousands(n) + " rows"
}

// withThousands formats an integer with comma separators, so 10000 reads as
// 10,000 exactly as the spec's output block shows.
func withThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, digit := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digit)
	}
	return string(out)
}

// formatDuration renders a seeding time the way the spec's summary does: "0.8s".
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// seedTimeout bounds a seeding run. Generation is fast; the ceiling exists so a
// wedged connection cannot hang the CLI forever.
const seedTimeout = 10 * time.Minute

// runSeed seeds the database of an environment that is already up.
func runSeed(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, seedTimeout)
	defer cancel()

	live, err := openEnvironment(ctx, true)
	if err != nil {
		return err
	}
	defer live.Close()

	if len(live.Schema.Tables) == 0 {
		Warn("the database has no tables in the public schema - there is nothing to seed")
		return nil
	}

	if seedOpts.truncate {
		if err := db.Truncate(ctx, live.Pool, live.Schema.Order); err != nil {
			return Failf("Emptying the tables", err,
				"another connection may hold a lock; close it and retry")
		}
		Step("Emptied %s", countTables(len(live.Schema.Order)))
	}

	result, err := seedEnvironment(ctx, live.Pool, live.Schema, live.Config)
	if err != nil {
		return err
	}

	// Keep the recorded row count honest: appending adds to it, truncating
	// replaces it.
	if seedOpts.truncate {
		live.State.Rows = result.TotalRows
	} else {
		live.State.Rows += result.TotalRows
	}
	if err := env.SaveState(opts.dir, live.State); err != nil {
		Warn("could not record the new row count: %v", err)
	}
	return nil
}
