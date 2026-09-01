package cli

import "github.com/spf13/cobra"

// newDownCmd builds `playbrige down`: tear the environment down completely.
func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down the environment and remove its volumes",
		Long: `Stop and remove the containers, networks, and volumes of the environment
recorded in .playbrige/state.json, then forget that state. Nothing is left
behind on the machine.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			todo("down", "phase 1")
			return nil
		},
	}
}
