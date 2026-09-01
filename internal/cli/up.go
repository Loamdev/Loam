package cli

import "github.com/spf13/cobra"

// newUpCmd builds `playbrige up`: start an isolated stack, wait for Postgres,
// introspect the schema, seed it, and print the summary block.
func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start an isolated stack and seed its database",
		Long: `Start the compose stack under a unique project name, wait for Postgres to
accept connections, read its schema, seed every table with foreign-key-correct
synthetic data, and print the app and database URLs.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			todo("up", "phase 1-6")
			return nil
		},
	}
}
