package cli

import "github.com/spf13/cobra"

// newStatusCmd builds `playbrige status`: report on the recorded environment.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current environment's project, containers, and URLs",
		Long: `Read .playbrige/state.json and report the project name, which containers are
running, how many tables and rows the database holds, and the app and database
URLs (with the password masked).`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			todo("status", "phase 6")
			return nil
		},
	}
}
