// Package cli implements the loam command-line interface: the cobra
// command tree, the global flags every subcommand shares, and the output
// printers that render the CLI contract from build spec section 7.
//
// Phase 0 wires the tree and the flags only; each subcommand prints a
// placeholder line and later phases fill in the behavior.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is what `loam --version` reports.
const version = "0.1.0-dev"

// globalOptions holds the values bound to the root command's persistent flags.
// Later phases read this state instead of re-declaring flags of their own.
type globalOptions struct {
	file  string // --file/-f: compose file to run
	dir   string // --dir: project directory holding the compose file and .loam state
	dbURL string // --db-url: Postgres URL override; empty means auto-detect from compose
	rows  int    // --rows: base rows per leaf table; 0 means use loam.yml or the built-in default
}

// opts is the process-wide flag state. NewRootCmd rebinds every field on each
// call, so the documented defaults are always restored with a fresh tree.
var opts globalOptions

// NewRootCmd builds the loam command tree: the root command, its four
// persistent flags, and the six subcommands (up, down, status, reset, seed,
// logs). Usage and error printing are silenced because Execute renders errors
// itself in the spec's output format.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "loam",
		Short: "Boot an isolated Compose stack seeded with realistic, FK-correct data",
		Long: `Loam runs a repository's docker-compose.yml as an isolated, throwaway
stack, waits for Postgres, reads the schema, and fills every table with
realistic synthetic data whose foreign keys all point at rows that exist.

One command brings the app up full of believable data; 'loam down' makes
it vanish, containers and volumes included. Everything runs locally against
your own Docker - no server, no accounts, no network calls.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&opts.file, "file", "f", "docker-compose.yml", "compose file to run")
	pf.StringVar(&opts.dir, "dir", ".", "project directory containing the compose file and .loam state")
	pf.StringVar(&opts.dbURL, "db-url", "", "Postgres to use; lets seed and reset run on any database (default: detect from the compose file)")
	pf.IntVar(&opts.rows, "rows", 0, "base row count per leaf table (0: use loam.yml or the built-in default)")

	root.AddCommand(
		newUpCmd(),
		newDownCmd(),
		newStatusCmd(),
		newResetCmd(),
		newSeedCmd(),
		newLogsCmd(),
	)

	return root
}

// Execute builds the command tree, runs it, and reports any failure on the
// error stream in the spec's failure format. It is the only place errors are
// printed, and main.go's only entry point.
func Execute() error {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(errOut, "  %s %v\n", glyphCross, err)
		return err
	}
	return nil
}
