package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shivansh-source/stateful-playwright/internal/env"
	"github.com/spf13/cobra"
)

// composeUpTimeout bounds `docker compose up`. It is generous because the first
// run on a machine has to pull images.
const composeUpTimeout = 10 * time.Minute

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
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUp(cmd.Context())
		},
	}
}

// runUp performs the phase 1 half of `up`: bring an isolated stack online and
// record it. Waiting for Postgres, reading the schema, and seeding are wired in
// by the later phases.
func runUp(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, composeUpTimeout)
	defer cancel()

	// Everything cheap and local is checked first, so a typo in --file or a
	// forgotten `down` fails in milliseconds instead of after a Docker round trip.
	if err := ensureDirExists(opts.dir); err != nil {
		return Failf("Reading the project directory", err, "check the --dir flag")
	}

	composeFile := resolveComposeFile(opts.dir, opts.file)
	compose, err := env.ParseCompose(composeFile)
	if err != nil {
		return Failf("Reading "+composeFile, err,
			"run playbrige from a directory containing a docker-compose.yml, or pass --file")
	}

	// Refuse to strand an environment the user has already started: without the
	// recorded project name, `down` could never clean it up.
	if existing, loadErr := env.LoadState(opts.dir); loadErr == nil {
		return Failf("Starting the environment",
			fmt.Errorf("environment %s is already recorded in %s", existing.Project, env.StatePath(opts.dir)),
			"run 'playbrige down' first, or 'playbrige status' to see what is running")
	}

	if err := env.CheckDocker(ctx); err != nil {
		return Failf("Docker check", err,
			"start Docker Desktop (or your engine) and try again")
	}

	project := env.NewProjectName(time.Now())
	if err := env.Up(ctx, composeFile, project); err != nil {
		return Failf("Starting the environment", err,
			"check the compose file starts cleanly with: docker compose -f "+composeFile+" config")
	}

	services := compose.ServiceNames()
	state := &env.State{
		Project:     project,
		ComposeFile: composeFile,
		CreatedAt:   time.Now().UTC(),
		Services:    services,
	}
	if err := env.SaveState(opts.dir, state); err != nil {
		// The stack is up but unrecorded, which would strand it. Tear it back
		// down rather than leaving containers the user cannot find.
		_ = env.Down(ctx, composeFile, project)
		return Failf("Recording the environment", err,
			"check that "+opts.dir+" is writable")
	}

	Step("Environment %s started (%s)", project, countServices(len(services)))
	Ready()
	return nil
}

// resolveComposeFile joins the project directory and the --file flag, leaving an
// absolute path untouched.
func resolveComposeFile(dir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	if dir == "" || dir == "." {
		return file
	}
	return filepath.Join(dir, file)
}

// countServices renders a service count with the right plural.
func countServices(n int) string {
	if n == 1 {
		return "1 service"
	}
	return fmt.Sprintf("%d services", n)
}

// ensureDirExists reports a clear error when --dir points at nothing, rather
// than letting a later file operation fail obscurely.
func ensureDirExists(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("project directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project directory %s is not a directory", dir)
	}
	return nil
}
