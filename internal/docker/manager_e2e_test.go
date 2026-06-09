package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParallelEnvironmentIsolation(t *testing.T) {
	if os.Getenv("PLAYBRIGE_E2E") != "1" {
		t.Skip("set PLAYBRIGE_E2E=1 to run docker e2e test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available")
	}

	mgr, err := NewDockerManager()
	if err != nil {
		t.Fatalf("new docker manager: %v", err)
	}

	composeFile := filepath.Join("..", "..", "docker-compose.test.yml")
	env1 := "env_test_1"
	env2 := "env_test_2"

	defer func() {
		_ = mgr.DestroyEnvironment(env1)
		_ = mgr.DestroyEnvironment(env2)
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, envID := range []string{env1, env2} {
		envID := envID
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- mgr.CreateEnvironment(envID, composeFile)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("create environment failed: %v", err)
		}
	}

	status1, err := mgr.GetEnvironmentStatus(env1)
	if err != nil || status1 != "ready" {
		t.Fatalf("env1 status = %q err=%v", status1, err)
	}

	status2, err := mgr.GetEnvironmentStatus(env2)
	if err != nil || status2 != "ready" {
		t.Fatalf("env2 status = %q err=%v", status2, err)
	}

	if err := mgr.DestroyEnvironment(env1); err != nil {
		t.Fatalf("destroy env1: %v", err)
	}

	if !waitForStatus(t, mgr, env1, "destroyed", 30*time.Second) {
		t.Fatalf("env1 did not reach destroyed status")
	}
	if !waitForStatus(t, mgr, env2, "ready", 30*time.Second) {
		t.Fatalf("env2 did not stay ready")
	}
}

func waitForStatus(t *testing.T, mgr *DockerManager, envID, expected string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := mgr.GetEnvironmentStatus(envID)
		if err == nil && status == expected {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return false
}
