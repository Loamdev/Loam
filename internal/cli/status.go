package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shivansh-source/stateful-playwright/internal/db"
	"github.com/shivansh-source/stateful-playwright/internal/env"
	"github.com/spf13/cobra"
)

// statusTimeout bounds `status`. It has to stay quick: this is the command
// people run when something already looks wrong.
const statusTimeout = 45 * time.Second

// newStatusCmd builds `playbrige status`: report on the recorded environment.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current environment's project, containers, and URLs",
		Long: `Read .playbrige/state.json and report the project name, which containers are
running, how many tables and rows the database holds, and the app and database
URLs (with the password masked).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd.Context())
		},
	}
}

// runStatus reports what is running, degrading gracefully: a stopped stack or an
// unreachable database is information, not an error.
func runStatus(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()

	state, err := env.LoadState(opts.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing running is a legitimate answer, not a failure.
			Step("No environment is running")
			Resource("Start one with: playbrige up")
			return nil
		}
		return Failf("Reading the environment state", err,
			"delete "+env.StatePath(opts.dir)+" if it is corrupt")
	}

	Step("Environment %s", state.Project)
	Resource("Compose:  %s", state.ComposeFile)
	if !state.CreatedAt.IsZero() {
		Resource("Created:  %s (%s ago)",
			state.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			roundDuration(time.Since(state.CreatedAt)))
	}

	reportContainers(ctx, state)
	reportDatabase(ctx, state)
	printEndpoints(state)
	return nil
}

// reportContainers describes which of the project's containers are up.
func reportContainers(ctx context.Context, state *env.State) {
	if err := env.CheckDocker(ctx); err != nil {
		Warn("Docker is not reachable, so container status is unknown: %v", err)
		return
	}

	running, err := env.RunningContainers(ctx, state.Project)
	if err != nil {
		Warn("could not list containers: %v", err)
		return
	}

	switch {
	case len(running) == 0:
		Resource("Status:   stopped (no running containers for %s)", state.Project)
	case len(running) < len(state.Services):
		Resource("Status:   partially running (%d of %d services): %s",
			len(running), len(state.Services), strings.Join(shortNames(running), ", "))
	default:
		Resource("Status:   running (%s): %s",
			countServices(len(running)), strings.Join(shortNames(running), ", "))
	}
}

// reportDatabase describes the schema and how full it is.
func reportDatabase(ctx context.Context, state *env.State) {
	pool, err := db.Connect(ctx, state.DBURL, 5*time.Second)
	if err != nil {
		// Expected when the stack is stopped, so report the recorded numbers
		// rather than treating it as a failure.
		Resource("Database: not reachable")
		if state.Rows > 0 {
			Resource("Seeded:   %s at last run", countRows(state.Rows))
		}
		return
	}
	defer pool.Close()

	tables, err := db.TableNames(ctx, pool)
	if err != nil {
		Warn("could not list tables: %v", err)
		return
	}
	if len(tables) == 0 {
		Resource("Database: reachable, no tables in the public schema")
		return
	}

	counts, err := db.RowCounts(ctx, pool, tables)
	if err != nil {
		Resource("Database: reachable, %s", countTables(len(tables)))
		Warn("could not count rows: %v", err)
		return
	}

	var total int64
	for _, n := range counts {
		total += n
	}
	Resource("Database: %s, %s", countTables(len(tables)), countRows(int(total)))

	for _, line := range topTables(counts, 5) {
		Resource("            %s", line)
	}
}

// topTables renders the fullest tables, largest first, so a glance shows where
// the data actually is.
func topTables(counts map[string]int64, limit int) []string {
	type entry struct {
		name string
		n    int64
	}
	entries := make([]entry, 0, len(counts))
	for name, n := range counts {
		entries = append(entries, entry{name, n})
	}
	// Sort by size, then name, so the output is deterministic.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].name < entries[j].name
	})

	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%-24s %s", e.name, withThousands(int(e.n))))
	}
	return out
}

// printEndpoints prints the app and database URLs, always masked.
func printEndpoints(state *env.State) {
	if state.AppURL != "" {
		Resource("App:      %s", state.AppURL)
	}
	if state.DBURL != "" {
		masked := env.MaskURL(state.DBURL)
		if masked != state.DBURL {
			Resource("DB:       %s   (password masked)", masked)
		} else {
			Resource("DB:       %s", masked)
		}
	}
}

// shortNames trims the project prefix off container names, which is noise when
// the project name is already on screen.
func shortNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		if idx := strings.Index(n, "-"); idx > 0 && strings.HasPrefix(n, "pb_") {
			out[i] = n[idx+1:]
			continue
		}
		out[i] = n
	}
	sort.Strings(out)
	return out
}

// roundDuration renders an age without spurious precision.
func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
