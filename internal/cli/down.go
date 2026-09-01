package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shivansh-source/stateful-playwright/internal/env"
	"github.com/spf13/cobra"
)

// composeDownTimeout bounds `docker compose down`.
const composeDownTimeout = 3 * time.Minute

// newDownCmd builds `playbrige down`: tear the environment down completely.
func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down the environment and remove its volumes",
		Long: `Stop and remove the containers, networks, and volumes of the environment
recorded in .playbrige/state.json, then forget that state. Nothing is left
behind on the machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDown(cmd.Context())
		},
	}
}

// runDown removes everything `up` created and forgets the environment.
func runDown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, composeDownTimeout)
	defer cancel()

	state, err := env.LoadState(opts.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing to do is a success, not a failure: `down` must be safe to
			// run twice, and safe to run in a directory that was never up.
			Step("No environment is running")
			return nil
		}
		return Failf("Reading the environment state", err,
			"delete "+env.StatePath(opts.dir)+" if it is corrupt, then clean up with: docker compose ls")
	}

	if err := env.CheckDocker(ctx); err != nil {
		return Failf("Docker check", err,
			"start Docker Desktop (or your engine) so the environment can be removed")
	}

	if err := env.Down(ctx, state.ComposeFile, state.Project); err != nil {
		return Failf("Tearing down "+state.Project, err,
			"remove it by hand with: docker compose -p "+state.Project+" -f "+state.ComposeFile+" down -v")
	}

	// The build spec's phase 1 acceptance test is that nothing is left over, so
	// verify it rather than trusting the exit code.
	containers, networks, volumes, err := env.ProjectResources(ctx, state.Project)
	if err != nil {
		Warn("could not verify teardown: %v", err)
	} else if leftovers := describeLeftovers(containers, networks, volumes); leftovers != "" {
		return Failf("Tearing down "+state.Project,
			fmt.Errorf("resources still present: %s", leftovers),
			"remove them by hand with: docker compose -p "+state.Project+" down -v --remove-orphans")
	}

	if err := env.DeleteState(opts.dir); err != nil {
		return Failf("Forgetting the environment", err,
			"delete "+env.StatePath(opts.dir)+" by hand")
	}

	Step("Environment %s removed (containers, networks, volumes)", state.Project)
	return nil
}

// describeLeftovers summarizes anything teardown failed to remove, or "" when
// the machine is genuinely clean.
func describeLeftovers(containers, networks, volumes []string) string {
	var parts []string
	if n := len(containers); n > 0 {
		parts = append(parts, fmt.Sprintf("%d container(s): %s", n, strings.Join(containers, ", ")))
	}
	if n := len(networks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d network(s): %s", n, strings.Join(networks, ", ")))
	}
	if n := len(volumes); n > 0 {
		parts = append(parts, fmt.Sprintf("%d volume(s): %s", n, strings.Join(volumes, ", ")))
	}
	return strings.Join(parts, "; ")
}
