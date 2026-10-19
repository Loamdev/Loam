package cli

import (
	"context"
	"strings"
	"testing"
)

// withOpts points the process-wide flag state at a throwaway directory for one
// test and restores it afterwards, so tests that lean on opts cannot leak into
// each other.
func withOpts(t *testing.T, dir, dbURL string) {
	t.Helper()
	saved := opts
	opts.dir = dir
	opts.dbURL = dbURL
	t.Cleanup(func() { opts = saved })
}

// TestOpenEnvironmentNeedsStateOnlyWithoutAURL pins the rule that makes `seed`
// and `reset` usable on databases loam did not create: naming the database is
// enough on its own, and when nothing names it the error has to point at the
// two ways forward rather than assuming an environment exists.
func TestOpenEnvironmentNeedsStateOnlyWithoutAURL(t *testing.T) {
	t.Run("no url and no recorded environment fails at the lookup", func(t *testing.T) {
		withOpts(t, t.TempDir(), "")

		_, err := openEnvironment(context.Background(), false)
		if err == nil {
			t.Fatal("expected an error in a directory with no recorded environment")
		}
		if !strings.Contains(err.Error(), "Finding the database") {
			t.Errorf("want the failure reported against the lookup step, got: %v", err)
		}
		if !strings.Contains(err.Error(), "--db-url") {
			t.Errorf("the hint must offer --db-url as a way out, got: %v", err)
		}
	})

	t.Run("a url makes the recorded environment unnecessary", func(t *testing.T) {
		// Unparseable on purpose: Connect rejects it before opening a socket,
		// so the test stays fast and needs no Postgres. Reaching the connect
		// step at all is the assertion - it proves the missing state file was
		// not treated as fatal.
		withOpts(t, t.TempDir(), "postgres://localhost:not-a-port/db")

		_, err := openEnvironment(context.Background(), false)
		if err == nil {
			t.Fatal("expected an error from an unusable database URL")
		}
		if strings.Contains(err.Error(), "Finding the database") {
			t.Fatalf("--db-url should have removed the need for state, got: %v", err)
		}
		if !strings.Contains(err.Error(), "Connecting to the database") {
			t.Errorf("want the failure reported against the connect step, got: %v", err)
		}
	})
}
