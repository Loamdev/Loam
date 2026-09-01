package cli

import (
	"context"
	"errors"
	"os"

	"github.com/shivansh-source/stateful-playwright/internal/env"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd.Context(), args)
		},
	}

	// No shorthand: -f is already claimed by the root's --file flag.
	cmd.Flags().BoolVar(&logsOpts.follow, "follow", false, "keep streaming new log output")

	return cmd
}

// runLogs is a thin passthrough to `docker compose logs` for the recorded
// environment. Docker's output goes straight to the terminal, unbuffered.
func runLogs(ctx context.Context, services []string) error {
	state, err := env.LoadState(opts.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Failf("Reading the environment state", err,
				"run 'playbrige up' first")
		}
		return Failf("Reading the environment state", err,
			"delete "+env.StatePath(opts.dir)+" if it is corrupt")
	}

	if err := env.Logs(ctx, state.ComposeFile, state.Project, logsOpts.follow, services); err != nil {
		return Failf("Streaming logs for "+state.Project, err,
			"check the environment is still up with: playbrige status")
	}
	return nil
}
