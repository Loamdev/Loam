package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// logsOptions holds the flags specific to `playbrige logs`.
type logsOptions struct {
	follow bool // --follow: keep streaming new output
}

// logsOpts is the flag state for `playbrige logs`, rebound on every NewRootCmd.
var logsOpts logsOptions

// newLogsCmd builds `playbrige logs`: stream container logs for the current
// environment, optionally narrowed to the named services.
func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [service...]",
		Short: "Stream container logs for the current environment",
		Long: `Show the logs of the environment recorded in .playbrige/state.json. Name one
or more compose services to narrow the output; with no arguments, every service
is included.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			command := "logs"
			if len(args) > 0 {
				command += " " + strings.Join(args, " ")
			}
			todo(command, "phase 6")
			return nil
		},
	}

	// No shorthand: -f is already claimed by the root's --file flag.
	cmd.Flags().BoolVar(&logsOpts.follow, "follow", false, "keep streaming new log output")

	return cmd
}
