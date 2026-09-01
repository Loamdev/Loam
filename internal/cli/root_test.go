package cli

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// run executes a fresh command tree with the given args, capturing cobra's own
// output so tests never write to the terminal.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)

	err := root.Execute()

	return buf.String(), err
}

func TestNewRootCmdRegistersExactlySixSubcommands(t *testing.T) {
	want := []string{"down", "logs", "reset", "seed", "status", "up"}

	var got []string
	for _, c := range NewRootCmd().Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("subcommands = %v, want %v", got, want)
	}
}

func TestRootCommandMetadata(t *testing.T) {
	root := NewRootCmd()

	if root.Use != "playbrige" {
		t.Errorf("Use = %q, want %q", root.Use, "playbrige")
	}
	if root.Version != "0.1.0-dev" {
		t.Errorf("Version = %q, want %q", root.Version, "0.1.0-dev")
	}
	if root.Short == "" || root.Long == "" {
		t.Error("root command needs both a Short and a Long description")
	}
	if !root.SilenceUsage || !root.SilenceErrors {
		t.Error("SilenceUsage and SilenceErrors must be true: Execute prints our own errors")
	}
}

func TestGlobalPersistentFlagDefaults(t *testing.T) {
	root := NewRootCmd()

	tests := []struct {
		name      string
		shorthand string
		defValue  string
	}{
		{name: "file", shorthand: "f", defValue: "docker-compose.yml"},
		{name: "dir", shorthand: "", defValue: "."},
		{name: "db-url", shorthand: "", defValue: ""},
		{name: "rows", shorthand: "", defValue: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := root.PersistentFlags().Lookup(tc.name)
			if f == nil {
				t.Fatalf("--%s is not registered on the root command", tc.name)
			}
			if f.DefValue != tc.defValue {
				t.Errorf("--%s default = %q, want %q", tc.name, f.DefValue, tc.defValue)
			}
			if f.Shorthand != tc.shorthand {
				t.Errorf("--%s shorthand = %q, want %q", tc.name, f.Shorthand, tc.shorthand)
			}
			if f.Usage == "" {
				t.Errorf("--%s needs usage text", tc.name)
			}
		})
	}
}

func TestGlobalFlagsBindToOptions(t *testing.T) {
	if _, err := run(t, "up", "-f", "compose.dev.yml", "--dir", "svc", "--db-url", "postgres://x@localhost/y", "--rows", "250"); err != nil {
		t.Fatalf("up with global flags: %v", err)
	}

	if opts.file != "compose.dev.yml" {
		t.Errorf("opts.file = %q, want %q", opts.file, "compose.dev.yml")
	}
	if opts.dir != "svc" {
		t.Errorf("opts.dir = %q, want %q", opts.dir, "svc")
	}
	if opts.dbURL != "postgres://x@localhost/y" {
		t.Errorf("opts.dbURL = %q, want the parsed URL", opts.dbURL)
	}
	if opts.rows != 250 {
		t.Errorf("opts.rows = %d, want 250", opts.rows)
	}

	// A fresh tree must restore the documented defaults.
	NewRootCmd()
	if opts.file != "docker-compose.yml" || opts.dir != "." || opts.dbURL != "" || opts.rows != 0 {
		t.Errorf("NewRootCmd did not restore defaults: %+v", opts)
	}
}

func TestSeedHasTruncateFlag(t *testing.T) {
	seed := findCmd(t, "seed")

	f := seed.Flags().Lookup("truncate")
	if f == nil {
		t.Fatal("seed is missing the --truncate flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--truncate default = %q, want %q", f.DefValue, "false")
	}
}

func TestLogsFollowHasNoShorthandAndTakesServices(t *testing.T) {
	logs := findCmd(t, "logs")

	f := logs.Flags().Lookup("follow")
	if f == nil {
		t.Fatal("logs is missing the --follow flag")
	}
	if f.Shorthand != "" {
		t.Errorf("--follow shorthand = %q, want none: -f belongs to the global --file flag", f.Shorthand)
	}

	// Service names are positional and unrestricted.
	if _, err := run(t, "logs", "--follow", "db", "web"); err != nil {
		t.Errorf("logs with service arguments: %v", err)
	}
	if !logsOpts.follow {
		t.Error("--follow did not bind to logsOpts.follow")
	}
}

func TestHelpSucceeds(t *testing.T) {
	got, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help returned %v, want nil", err)
	}

	for _, name := range []string{"up", "down", "status", "reset", "seed", "logs"} {
		if !strings.Contains(got, name) {
			t.Errorf("--help output does not list %q:\n%s", name, got)
		}
	}
}

func TestSubcommandsStubOutSuccessfully(t *testing.T) {
	for _, name := range []string{"up", "down", "status", "reset", "seed", "logs"} {
		t.Run(name, func(t *testing.T) {
			printed := capture(t, func() {
				if _, err := run(t, name); err != nil {
					t.Fatalf("%s returned %v, want nil so phase 0 exits 0", name, err)
				}
			})

			if !strings.Contains(printed, name) || !strings.Contains(printed, "not implemented yet") {
				t.Errorf("%s printed %q, want a TODO line naming the command and its phase", name, printed)
			}
			if !strings.Contains(printed, "phase") {
				t.Errorf("%s printed %q, want the implementing phase named", name, printed)
			}
		})
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, err := run(t, "teleport"); err == nil {
		t.Error("unknown command returned nil, want an error")
	}
}

// findCmd returns the registered subcommand with the given name.
func findCmd(t *testing.T, name string) *cobra.Command {
	t.Helper()

	for _, c := range NewRootCmd().Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q is not registered", name)

	return nil
}
