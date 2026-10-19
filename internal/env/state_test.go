package env

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatePath(t *testing.T) {
	got := StatePath("myproject")
	want := filepath.Join("myproject", StateDir, "state.json")
	if got != want {
		t.Errorf("StatePath(%q) = %q, want %q", "myproject", got, want)
	}
}

func TestSaveLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &State{
		Project:     "pb_1720001234_a3f9",
		ComposeFile: "docker-compose.yml",
		CreatedAt:   time.Unix(1720001234, 0).UTC(),
		DBURL:       "postgres://appuser:apppass@localhost:5433/appdb",
		AppURL:      "http://localhost:8080",
		Services:    []string{"db", "web"},
		Rows:        12400,
	}

	if err := SaveState(dir, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Project != want.Project || got.ComposeFile != want.ComposeFile ||
		got.DBURL != want.DBURL || got.AppURL != want.AppURL || got.Rows != want.Rows {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if len(got.Services) != 2 || got.Services[0] != "db" || got.Services[1] != "web" {
		t.Errorf("Services = %v, want [db web]", got.Services)
	}
}

func TestSaveStateCreatesTheStateDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Project: "pb_1"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, StateDir)); err != nil {
		t.Errorf("state directory was not created: %v", err)
	}
}

func TestSaveStateLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Project: "pb_1"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, StateDir))
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("state dir holds %v, want just [state.json]", names)
	}
}

func TestSaveStateOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Project: "pb_first"}); err != nil {
		t.Fatalf("first SaveState: %v", err)
	}
	if err := SaveState(dir, &State{Project: "pb_second"}); err != nil {
		t.Fatalf("second SaveState: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Project != "pb_second" {
		t.Errorf("Project = %q, want pb_second", got.Project)
	}
}

func TestSaveStateRejectsNil(t *testing.T) {
	if err := SaveState(t.TempDir(), nil); err == nil {
		t.Fatal("want an error when saving a nil state")
	}
}

func TestLoadStateWhenNothingIsRunning(t *testing.T) {
	_, err := LoadState(t.TempDir())
	if err == nil {
		t.Fatal("want an error when no environment is recorded")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error must wrap os.ErrNotExist so the CLI can hint 'run loam up'; got %v", err)
	}
}

func TestLoadStateRejectsGarbage(t *testing.T) {
	t.Run("not json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(StatePath(dir), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadState(dir); err == nil {
			t.Fatal("want an error for a corrupt state file")
		}
	})

	t.Run("no project name", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body, err := json.Marshal(State{ComposeFile: "docker-compose.yml"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(StatePath(dir), body, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadState(dir); err == nil {
			t.Fatal("want an error when the state names no project: down would have nothing to tear down")
		}
	})
}

func TestDeleteStateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Project: "pb_1"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if err := DeleteState(dir); err != nil {
		t.Fatalf("first DeleteState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, StateDir)); !os.IsNotExist(err) {
		t.Errorf("empty state directory should be removed too, stat gave: %v", err)
	}

	// `down` must be safe to run twice.
	if err := DeleteState(dir); err != nil {
		t.Errorf("second DeleteState: %v, want nil", err)
	}
}

func TestDeleteStateKeepsADirectoryTheUserIsUsing(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Project: "pb_1"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	other := filepath.Join(dir, StateDir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := DeleteState(dir); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("DeleteState removed a file it does not own: %v", err)
	}
}
