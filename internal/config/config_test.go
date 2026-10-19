package config

import (
	"os"
	"path/filepath"
	"testing"
)

// write drops a loam.yml into a fresh temp dir and returns the dir.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", FileName, err)
	}
	return dir
}

func TestLoadWithNoFileIsTheHappyPath(t *testing.T) {
	// The zero-config path is the one that has to work (spec section 12.4):
	// a missing loam.yml is not an error.
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load with no config returned %v, want nil", err)
	}
	if cfg.Seed.DefaultRows != DefaultRows {
		t.Errorf("DefaultRows = %d, want %d", cfg.Seed.DefaultRows, DefaultRows)
	}
	if cfg.Database.URL != "" || cfg.App.URL != "" {
		t.Errorf("defaults should override nothing, got database=%q app=%q", cfg.Database.URL, cfg.App.URL)
	}
}

func TestLoadReadsEveryField(t *testing.T) {
	// This is the example from build spec section 8, verbatim in shape.
	dir := write(t, `
database:
  url: postgres://user:pass@localhost:5433/appdb
app:
  url: http://localhost:8080
seed:
  default_rows: 5000
  tables:
    users: 2000
    orders: 20000
  distributions:
    users.status: { active: 0.6, trial: 0.3, churned: 0.1 }
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.URL != "postgres://user:pass@localhost:5433/appdb" {
		t.Errorf("database.url = %q", cfg.Database.URL)
	}
	if cfg.App.URL != "http://localhost:8080" {
		t.Errorf("app.url = %q", cfg.App.URL)
	}
	if cfg.Seed.DefaultRows != 5000 {
		t.Errorf("default_rows = %d, want 5000", cfg.Seed.DefaultRows)
	}

	if rows, ok := cfg.RowsFor("users"); !ok || rows != 2000 {
		t.Errorf("RowsFor(users) = (%d, %v), want (2000, true)", rows, ok)
	}
	if rows, ok := cfg.RowsFor("orders"); !ok || rows != 20000 {
		t.Errorf("RowsFor(orders) = (%d, %v), want (20000, true)", rows, ok)
	}
	if _, ok := cfg.RowsFor("products"); ok {
		t.Error("RowsFor(products) reported an override that was never configured")
	}

	dist, ok := cfg.DistributionFor("users", "status")
	if !ok {
		t.Fatal("DistributionFor(users, status) found nothing")
	}
	if dist["active"] != 0.6 || dist["trial"] != 0.3 || dist["churned"] != 0.1 {
		t.Errorf("distribution = %v", dist)
	}
	if _, ok := cfg.DistributionFor("orders", "status"); ok {
		t.Error("DistributionFor(orders, status) reported a profile that was never configured")
	}
}

func TestLoadFillsInPartialConfig(t *testing.T) {
	// Every field is optional: naming only database.url must not zero the
	// row-count default.
	cfg, err := Load(write(t, "database:\n  url: postgres://localhost/db\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Seed.DefaultRows != DefaultRows {
		t.Errorf("DefaultRows = %d, want the default %d to survive a partial config",
			cfg.Seed.DefaultRows, DefaultRows)
	}
}

func TestLoadRejectsConfigThatWouldSilentlyDoNothing(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed yaml", "seed:\n  default_rows: [unclosed\n"},
		{"negative row count", "seed:\n  tables:\n    users: -5\n"},
		{"empty distribution", "seed:\n  distributions:\n    users.status: {}\n"},
		{"all-zero weights", "seed:\n  distributions:\n    users.status: { active: 0, churned: 0 }\n"},
		{"negative weight", "seed:\n  distributions:\n    users.status: { active: 1, churned: -1 }\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A config the user deliberately wrote must not be silently ignored.
			if _, err := Load(write(t, tc.body)); err == nil {
				t.Errorf("Load accepted %s, want an error", tc.name)
			}
		})
	}
}

func TestLoadTreatsZeroDefaultRowsAsUnset(t *testing.T) {
	cfg, err := Load(write(t, "seed:\n  default_rows: 0\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Seed.DefaultRows != DefaultRows {
		t.Errorf("DefaultRows = %d, want %d: zero rows would seed nothing", cfg.Seed.DefaultRows, DefaultRows)
	}
}
