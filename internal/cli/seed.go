package cli

import "github.com/spf13/cobra"

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
		RunE: func(_ *cobra.Command, _ []string) error {
			todo("seed", "phase 5")
			return nil
		},
	}

	cmd.Flags().BoolVar(&seedOpts.truncate, "truncate", false, "empty every table before seeding")

	return cmd
}
