package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// deadURL points at a port nothing listens on, so Connect exercises its retry
// loop and then gives up.
const deadURL = "postgres://nobody:nothing@127.0.0.1:1/nodb"

func TestConnectRejectsAnUnparseableURL(t *testing.T) {
	_, err := Connect(context.Background(), "://not a url", time.Second)
	if err == nil {
		t.Fatal("Connect with a malformed URL returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "invalid database URL") {
		t.Errorf("error = %q, want it to say the URL is invalid", err)
	}
}

func TestConnectNeverEchoesThePassword(t *testing.T) {
	// Connection errors get printed. The URL must not travel with them
	// (build spec section 12.2).
	const password = "sup3rs3cr3t"

	for _, rawurl := range []string{
		"postgres://user:" + password + "@127.0.0.1:1/db?sslmode=bogus-value",
		"postgres://user:" + password + "@127.0.0.1:1/db",
	} {
		_, err := Connect(context.Background(), rawurl, 1500*time.Millisecond)
		if err == nil {
			t.Fatal("Connect to a dead port returned nil, want an error")
		}
		if strings.Contains(err.Error(), password) {
			t.Errorf("Connect error leaked the password: %q", err)
		}
	}
}

func TestConnectGivesUpAfterTheTimeout(t *testing.T) {
	timeout := 2 * time.Second

	start := time.Now()
	_, err := Connect(context.Background(), deadURL, timeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Connect to a dead port returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "did not accept connections") {
		t.Errorf("error = %q, want it to say the database never came up", err)
	}

	// It must actually retry rather than failing on the first refusal, and it
	// must not run away past the deadline.
	if elapsed < timeout {
		t.Errorf("gave up after %s, want it to keep retrying for the full %s", elapsed, timeout)
	}
	if elapsed > timeout+5*time.Second {
		t.Errorf("took %s, far past the %s timeout", elapsed, timeout)
	}
}

func TestConnectStopsWhenTheContextIsCanceled(t *testing.T) {
	// Ctrl-C during `up` must abandon the wait promptly, not sit out the
	// full 30 seconds.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := Connect(ctx, deadURL, 30*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Connect returned nil after cancellation, want an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %s to notice cancellation", elapsed)
	}
}

func TestConnectDefaultsAZeroTimeout(t *testing.T) {
	if ReadyTimeout != 30*time.Second {
		t.Errorf("ReadyTimeout = %s, want 30s per build spec section 6 phase 2", ReadyTimeout)
	}
}
