package cli

import "github.com/spf13/cobra"

// newResetCmd builds `playbrige reset`: empty the database and seed it again.
func newResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Truncate every table and seed fresh data",
		Long: `Empty every table in the running environment's database and generate a fresh
set of synthetic rows, without restarting any container.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			todo("reset", "phase 6")
			return nil
		},
	}
}
