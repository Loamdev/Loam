package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// capture redirects both output streams into one buffer for the duration of fn.
// Tests using it must not call t.Parallel: the writers are package state.
func capture(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	prevOut, prevErr := out, errOut
	out, errOut = &buf, &buf
	t.Cleanup(func() { out, errOut = prevOut, prevErr })

	fn()

	return buf.String()
}

func TestStepFormat(t *testing.T) {
	got := capture(t, func() { Step("Schema read: %d tables, %d foreign keys", 14, 9) })

	want := "  " + glyphCheck + " Schema read: 14 tables, 9 foreign keys\n"
	if got != want {
		t.Errorf("Step() = %q, want %q", got, want)
	}
}

func TestResourceFormat(t *testing.T) {
	got := capture(t, func() { Resource("App:  %s", "http://localhost:8080") })

	want := "  " + glyphArrow + " App:  http://localhost:8080\n"
	if got != want {
		t.Errorf("Resource() = %q, want %q", got, want)
	}
}

func TestWarnFormat(t *testing.T) {
	got := capture(t, func() { Warn("cycle broken by deferring %s", "employees.manager_id") })

	want := "  " + glyphWarn + " cycle broken by deferring employees.manager_id\n"
	if got != want {
		t.Errorf("Warn() = %q, want %q", got, want)
	}
}

func TestReadyPrintsExactLine(t *testing.T) {
	got := capture(t, func() { Ready() })

	want := "  Ready. Run 'loam down' when finished.\n"
	if got != want {
		t.Errorf("Ready() = %q, want %q", got, want)
	}
}

func TestPrintersUseSingleLine(t *testing.T) {
	got := capture(t, func() {
		Step("one")
		Resource("two")
		Ready()
	})

	if lines := strings.Count(got, "\n"); lines != 3 {
		t.Errorf("got %d lines, want 3: %q", lines, got)
	}
}

func TestFailfMessageNamesStepErrAndHint(t *testing.T) {
	cause := errors.New("dial tcp 127.0.0.1:5433: connection refused")
	hint := "is the postgres service healthy? Run 'loam logs db'"

	err := Failf("Database ready", cause, hint)
	if err == nil {
		t.Fatal("Failf() = nil, want an error")
	}

	msg := err.Error()
	for _, want := range []string{"Database ready", "failed", cause.Error(), hint} {
		if !strings.Contains(msg, want) {
			t.Errorf("Failf() message %q does not contain %q", msg, want)
		}
	}

	if !errors.Is(err, cause) {
		t.Error("Failf() must wrap the cause so errors.Is finds it")
	}
}

func TestFailfDoesNotPrint(t *testing.T) {
	got := capture(t, func() {
		_ = Failf("Seed", errors.New("boom"), "retry with --rows 100")
	})

	if got != "" {
		t.Errorf("Failf() printed %q, want nothing: Execute owns error printing", got)
	}
}

func TestFailfWithoutErrOrHint(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		hint  string
		wants []string
	}{
		{name: "no cause", err: nil, hint: "check docker is running", wants: []string{"Compose up failed", "check docker is running"}},
		{name: "no hint", err: errors.New("exit status 1"), hint: "", wants: []string{"Compose up failed", "exit status 1"}},
		{name: "neither", err: nil, hint: "", wants: []string{"Compose up failed"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Failf("Compose up", tc.err, tc.hint)
			if err == nil {
				t.Fatal("Failf() = nil, want an error")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Failf() message %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestDefaultWritersAreStdStreams(t *testing.T) {
	// Guards against a stray reassignment landing in production code, and
	// against capture() failing to restore what it borrowed.
	if out != io.Writer(os.Stdout) {
		t.Errorf("out = %v, want os.Stdout", out)
	}
	if errOut != io.Writer(os.Stderr) {
		t.Errorf("errOut = %v, want os.Stderr", errOut)
	}
}
