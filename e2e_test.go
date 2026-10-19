//go:build e2e

// Package main's end-to-end suite: the build spec's section 11 definition of
// done, run against the bundled testapp/ fixture through the real CLI binary.
//
// Build-tagged so `go test ./...` never needs Docker. Run it with:
//
//	go test -tags=e2e -v -timeout 10m .
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// wholeCycleBudget is the build spec's section 11 target: clone to seeded app in
// about thirty seconds. The assertion is generous because a cold CI runner has
// to pull images, but it still catches a real regression.
const wholeCycleBudget = 90 * time.Second

// binary builds the CLI once and returns its path.
func binary(t *testing.T) string {
	t.Helper()

	name := "loam-e2e"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)

	build := exec.Command("go", "build", "-o", path, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the CLI: %v\n%s", err, out)
	}
	return path
}

// fixture copies testapp/ somewhere disposable, so the repo is never dirtied by
// a .loam/ directory and each test gets its own environment.
func fixture(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "app")
	if err := copyTree("testapp", dir); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}
	return dir
}

// copyTree copies a directory recursively.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path) //nolint:gosec // fixture files in the repo
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644) //nolint:gosec // fixture files
	})
}

// loam runs the CLI and returns its combined output.
func loam(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(bin, append(args, "--dir", dir)...) //nolint:gosec // test-built binary
	out, err := cmd.CombinedOutput()
	t.Logf("$ loam %s\n%s", strings.Join(args, " "), out)
	return string(out), err
}

// TestEndToEndDefinitionOfDone is the build spec's single acceptance test: a
// developer clones a Postgres-backed app they have never seen, runs one command,
// and has a working app full of foreign-key-correct data - then runs one more and
// it is all gone.
func TestEndToEndDefinitionOfDone(t *testing.T) {
	bin := binary(t)
	dir := fixture(t)

	// Always tear down, even if an assertion fails, so a failed run never leaves
	// containers on the machine.
	t.Cleanup(func() {
		if _, err := loam(t, bin, dir, "down"); err != nil {
			t.Logf("cleanup down failed: %v", err)
		}
	})

	started := time.Now()
	out, err := loam(t, bin, dir, "up")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	// The output contract from build spec section 7.
	for _, want := range []string{
		"Environment pb_", "started (2 services)",
		"Database ready",
		"Schema read: 9 tables, 8 foreign keys",
		"Seeded ", " rows across 9 tables in ",
		"App:", "http://localhost:8080",
		"DB:", "postgres://appuser:****@localhost:5433/appdb",
		"Ready. Run 'loam down' when finished.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("up output is missing %q", want)
		}
	}

	// The password must never reach a terminal (section 12.2).
	if strings.Contains(out, "apppass") {
		t.Error("up printed the database password unmasked")
	}

	if elapsed > wholeCycleBudget {
		t.Errorf("up took %s, past the %s budget for the whole cycle", elapsed.Round(time.Millisecond), wholeCycleBudget)
	}
	t.Logf("up completed in %s", elapsed.Round(time.Millisecond))

	// Seeding time is reported and must be inside the spec's target.
	if seconds, ok := seededSeconds(out); ok {
		t.Logf("seeding took %.2fs", seconds)
		if seconds > 5 {
			t.Errorf("seeding took %.2fs; the spec targets ~10k rows in under 2s - is this still using COPY?", seconds)
		}
	} else {
		t.Errorf("could not find the seeding time in the output")
	}

	// status must describe the live environment.
	statusOut, err := loam(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, want := range []string{"Environment pb_", "running", "Database:", "9 tables"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("status output is missing %q", want)
		}
	}
	if strings.Contains(statusOut, "apppass") {
		t.Error("status printed the database password unmasked")
	}

	// reset must empty and refill without restarting a container.
	resetOut, err := loam(t, bin, dir, "reset")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	for _, want := range []string{"Emptied 9 tables", "Seeded "} {
		if !strings.Contains(resetOut, want) {
			t.Errorf("reset output is missing %q", want)
		}
	}

	// logs is a passthrough and must not error.
	if _, err := loam(t, bin, dir, "logs", "db"); err != nil {
		t.Errorf("logs failed: %v", err)
	}

	// down must leave nothing behind (section 6 phase 1 acceptance).
	downOut, err := loam(t, bin, dir, "down")
	if err != nil {
		t.Fatalf("down failed: %v", err)
	}
	if !strings.Contains(downOut, "removed") {
		t.Errorf("down output is missing the removal confirmation: %q", downOut)
	}

	project := projectName(out)
	if project == "" {
		t.Fatal("could not determine the project name from the up output")
	}
	for _, check := range []struct {
		what string
		args []string
	}{
		{"containers", []string{"ps", "-a", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.Names}}"}},
		{"networks", []string{"network", "ls", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.Name}}"}},
		{"volumes", []string{"volume", "ls", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.Name}}"}},
	} {
		leftover, err := exec.Command("docker", check.args...).Output()
		if err != nil {
			t.Errorf("could not check leftover %s: %v", check.what, err)
			continue
		}
		if trimmed := strings.TrimSpace(string(leftover)); trimmed != "" {
			t.Errorf("down left %s behind: %s", check.what, trimmed)
		}
	}

	// The state file must be gone, and a second down must be harmless.
	if _, err := os.Stat(filepath.Join(dir, ".loam", "state.json")); !os.IsNotExist(err) {
		t.Errorf("down left the state file behind: %v", err)
	}
	if _, err := loam(t, bin, dir, "down"); err != nil {
		t.Errorf("a second down should be harmless, got %v", err)
	}
}

// seededPattern extracts the reported seeding time, e.g. "in 1.2s".
var seededPattern = regexp.MustCompile(`Seeded .* in ([\d.]+)s`)

// seededSeconds reads the seeding duration out of the up output.
func seededSeconds(out string) (float64, bool) {
	m := seededPattern.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return seconds, true
}

// projectPattern extracts the generated compose project name.
var projectPattern = regexp.MustCompile(`(pb_\d+_[0-9a-f]+)`)

// projectName reads the project name out of the up output.
func projectName(out string) string {
	if m := projectPattern.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}
