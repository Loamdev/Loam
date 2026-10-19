// Package env owns the environment: parsing the user's docker-compose.yml,
// running it under an isolated project name via the docker CLI, and recording
// what was created in .loam/state.json so that a later `down`, `status`,
// or `logs` knows what to act on.
//
// Everything here is local. Nothing is uploaded, and no process is left
// running in the background (build spec section 12.1).
package env

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateDir is the per-project directory holding Loam's runtime state.
// It is expected to be gitignored.
const StateDir = ".loam"

// stateFile is the name of the state document inside StateDir.
const stateFile = "state.json"

// State is what Loam remembers about the environment it started. It holds
// nothing the user did not already have in their own compose file: the database
// URL is recorded so `status` and `seed` can reconnect without re-parsing, but
// it is always masked before being printed (build spec section 1, phase 1).
type State struct {
	// Project is the isolated compose project name, e.g. "pb_1720001234".
	Project string `json:"project"`
	// ComposeFile is the path to the compose file the environment was started from.
	ComposeFile string `json:"composeFile"`
	// CreatedAt is when `up` succeeded.
	CreatedAt time.Time `json:"createdAt"`
	// DBURL is the Postgres connection URL. Never print it unmasked; use MaskURL.
	DBURL string `json:"dbUrl,omitempty"`
	// AppURL is the best-effort HTTP URL of the app service, if one was detected.
	AppURL string `json:"appUrl,omitempty"`
	// Services are the compose service names in the environment.
	Services []string `json:"services,omitempty"`
	// Rows is the total number of rows seeded, or 0 when nothing has been seeded.
	Rows int `json:"rows,omitempty"`
}

// StatePath returns the path of the state document for the project rooted at dir.
func StatePath(dir string) string {
	return filepath.Join(dir, StateDir, stateFile)
}

// SaveState writes s to dir's state document, creating the state directory if
// needed. The file is written atomically via a temporary file and a rename, so
// an interrupted write can never leave a half-parsed state behind.
func SaveState(dir string, s *State) error {
	if s == nil {
		return errors.New("save state: nil state")
	}

	path := StatePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), stateFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best effort: on the happy path the rename has already consumed it.
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadState reads dir's state document. When no environment has been started
// the returned error wraps os.ErrNotExist, so callers can offer the "run
// loam up first" hint with errors.Is.
func LoadState(dir string) (*State, error) {
	path := StatePath(dir)
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the user's own --dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no environment recorded at %s: %w", path, os.ErrNotExist)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Project == "" {
		return nil, fmt.Errorf("parse %s: no project name recorded", path)
	}
	return &s, nil
}

// DeleteState removes dir's state document, and the state directory too when
// nothing else is left in it. A missing state document is not an error: `down`
// must be safe to run twice.
func DeleteState(dir string) error {
	path := StatePath(dir)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	// Leave the directory alone if the user put anything else in it.
	stateDir := filepath.Dir(path)
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", stateDir, err)
	}
	if len(entries) == 0 {
		if err := os.Remove(stateDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", stateDir, err)
		}
	}
	return nil
}
