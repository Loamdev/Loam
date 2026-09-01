package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shivansh-source/stateful-playwright/internal/env"
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

// parseGlobals binds the root command's persistent flags without executing a
// subcommand, so flag tests stay independent of what the commands now do.
func parseGlobals(t *testing.T, args ...string) {
	t.Helper()

	if err := NewRootCmd().PersistentFlags().Parse(args); err != nil {
		t.Fatalf("parse global flags %v: %v", args, err)
	}
}

func TestGlobalFlagsBindToOptions(t *testing.T) {
	parseGlobals(t, "-f", "compose.dev.yml", "--dir", "svc", "--db-url", "postgres://x@localhost/y", "--rows", "250")

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
	if err := logs.ValidateArgs([]string{"db", "web"}); err != nil {
		t.Errorf("logs should accept service name arguments: %v", err)
	}

	if err := logs.Flags().Parse([]string{"--follow"}); err != nil {
		t.Fatalf("parse --follow: %v", err)
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

// TestSubcommandsStubOutSuccessfully covers the commands the later phases still
// have to fill in. up, down, and logs are real as of phase 1 and are covered by
// the tests below and by the end-to-end run against testapp/.
func TestSubcommandsStubOutSuccessfully(t *testing.T) {
	for _, name := range []string{"status", "reset", "seed"} {
		t.Run(name, func(t *testing.T) {
			printed := capture(t, func() {
				if _, err := run(t, name); err != nil {
					t.Fatalf("%s returned %v, want nil until its phase lands", name, err)
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

func TestDownWithNoEnvironmentSucceeds(t *testing.T) {
	// `down` has to be safe in a directory that was never up, and safe to run
	// twice. It must not require Docker to say "there is nothing here".
	dir := t.TempDir()

	printed := capture(t, func() {
		if _, err := run(t, "down", "--dir", dir); err != nil {
			t.Fatalf("down with no environment returned %v, want nil", err)
		}
	})

	if !strings.Contains(printed, "No environment is running") {
		t.Errorf("down printed %q, want it to say no environment is running", printed)
	}
}

func TestLogsWithNoEnvironmentExplainsItself(t *testing.T) {
	_, err := run(t, "logs", "--dir", t.TempDir())
	if err == nil {
		t.Fatal("logs with no environment returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "playbrige up") {
		t.Errorf("error = %q, want it to point at 'playbrige up'", err)
	}
}

func TestUpRejectsAMissingComposeFile(t *testing.T) {
	// Cheap local checks come before any Docker call, so this must fail fast
	// whether or not an engine is running.
	_, err := run(t, "up", "--dir", t.TempDir(), "--file", "nope.yml")
	if err == nil {
		t.Fatal("up with no compose file returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "nope.yml") {
		t.Errorf("error = %q, want it to name the missing file", err)
	}
}

func TestUpRefusesToStrandAnExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  db:\n    image: postgres:16-alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	if err := env.SaveState(dir, &env.State{Project: "pb_already_running"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	_, err := run(t, "up", "--dir", dir)
	if err == nil {
		t.Fatal("up over an existing environment returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "pb_already_running") {
		t.Errorf("error = %q, want it to name the running environment", err)
	}
	if !strings.Contains(err.Error(), "playbrige down") {
		t.Errorf("error = %q, want it to hint at 'playbrige down'", err)
	}
}

func TestResolveComposeFile(t *testing.T) {
	// t.TempDir is absolute on every OS, including Windows where a leading
	// separator alone is not.
	absolute := filepath.Join(t.TempDir(), "compose.yml")

	tests := []struct {
		name string
		dir  string
		file string
		want string
	}{
		{"current directory", ".", "docker-compose.yml", "docker-compose.yml"},
		{"empty directory", "", "docker-compose.yml", "docker-compose.yml"},
		{"joined", "testapp", "docker-compose.yml", filepath.Join("testapp", "docker-compose.yml")},
		{"absolute file wins", "testapp", absolute, absolute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveComposeFile(tc.dir, tc.file); got != tc.want {
				t.Errorf("resolveComposeFile(%q, %q) = %q, want %q", tc.dir, tc.file, got, tc.want)
			}
		})
	}
}

func TestCountServices(t *testing.T) {
	for n, want := range map[int]string{0: "0 services", 1: "1 service", 3: "3 services"} {
		if got := countServices(n); got != want {
			t.Errorf("countServices(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDescribeLeftovers(t *testing.T) {
	if got := describeLeftovers(nil, nil, nil); got != "" {
		t.Errorf("a clean teardown described as %q, want empty", got)
	}

	got := describeLeftovers([]string{"pb_1-db-1"}, nil, []string{"pb_1_pgdata"})
	for _, want := range []string{"container", "pb_1-db-1", "volume", "pb_1_pgdata"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeLeftovers() = %q, want it to mention %q", got, want)
		}
	}
	if strings.Contains(got, "network") {
		t.Errorf("describeLeftovers() = %q, should not mention networks when none are left", got)
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
