package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
	"github.com/shivansh-source/stateful-playwright/internal/models"
	"gopkg.in/yaml.v3"
)

const (
	defaultStartupTimeout = 2 * time.Minute
	defaultPollInterval   = 2 * time.Second
)

type DockerManager struct {
	client        *client.Client
	startupTimout time.Duration
	pollInterval  time.Duration
	mu            sync.RWMutex
	composeFiles  map[string]string
}

func NewDockerManager() (*DockerManager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &DockerManager{
		client:        cli,
		startupTimout: defaultStartupTimeout,
		pollInterval:  defaultPollInterval,
		composeFiles:  make(map[string]string),
	}, nil
}

func (m *DockerManager) CreateEnvironment(envID, composeFilePath string) error {
	if m == nil || m.client == nil {
		return errors.New("docker manager is not initialized")
	}
	if envID == "" {
		return errors.New("env id is required")
	}
	if err := validateComposeFile(composeFilePath); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.startupTimout)
	defer cancel()

	if err := m.composeUp(ctx, envID, composeFilePath); err != nil {
		return err
	}
	m.mu.Lock()
	m.composeFiles[envID] = composeFilePath
	m.mu.Unlock()

	if err := m.waitForReady(ctx, envID); err != nil {
		return err
	}

	return nil
}

func (m *DockerManager) DestroyEnvironment(envID string) error {
	if m == nil || m.client == nil {
		return errors.New("docker manager is not initialized")
	}
	if envID == "" {
		return errors.New("env id is required")
	}
	m.mu.RLock()
	composeFilePath, ok := m.composeFiles[envID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("compose file path not found for environment %s", envID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.startupTimout)
	defer cancel()

	if err := m.composeDown(ctx, envID, composeFilePath); err != nil {
		return err
	}
	if err := verifyCleanup(ctx, m.client, envID); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.composeFiles, envID)
	m.mu.Unlock()

	return nil
}

func (m *DockerManager) GetEnvironmentStatus(envID string) (string, error) {
	if m == nil || m.client == nil {
		return "", errors.New("docker manager is not initialized")
	}
	if envID == "" {
		return "", errors.New("env id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	project := projectName(envID)
	containerIDs, err := dockerIDs(
		ctx,
		"ps",
		"-a",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return "", fmt.Errorf("list containers for %s: %w", project, err)
	}

	if len(containerIDs) == 0 {
		return models.EnvironmentStatusDestroyed, nil
	}

	ready, err := m.containersReady(ctx, containerIDs)
	if err != nil {
		return "", err
	}
	if ready {
		return models.EnvironmentStatusReady, nil
	}
	return models.EnvironmentStatusCreating, nil
}

func (m *DockerManager) HealthCheck(envID string) bool {
	status, err := m.GetEnvironmentStatus(envID)
	if err != nil {
		return false
	}
	return status == models.EnvironmentStatusReady
}

func (m *DockerManager) waitForReady(ctx context.Context, envID string) error {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		status, err := m.GetEnvironmentStatus(envID)
		if err == nil && status == models.EnvironmentStatusReady {
			return nil
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("wait for environment %s: %w", envID, err)
			}
			return fmt.Errorf("wait for environment %s timed out", envID)
		case <-ticker.C:
		}
	}
}

func (m *DockerManager) containersReady(ctx context.Context, containerIDs []string) (bool, error) {
	states := make([]containerState, 0, len(containerIDs))
	for _, id := range containerIDs {
		output, err := dockerOutput(
			ctx,
			"inspect",
			"-f",
			"{{.State.Running}} {{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}",
			id,
		)
		if err != nil {
			return false, fmt.Errorf("inspect container %s: %w", id, err)
		}

		parts := strings.Fields(strings.TrimSpace(output))
		if len(parts) != 2 {
			return false, fmt.Errorf("unexpected inspect output for container %s: %q", id, output)
		}

		states = append(states, containerState{
			Running:        parts[0] == "true",
			HasHealthcheck: parts[1] != "no-healthcheck",
			HealthStatus:   parts[1],
		})
	}
	return allContainersReady(states), nil
}

type containerState struct {
	Running        bool
	HasHealthcheck bool
	HealthStatus   string
}

func allContainersReady(states []containerState) bool {
	if len(states) == 0 {
		return false
	}
	for _, state := range states {
		if !state.Running {
			return false
		}
		if state.HasHealthcheck && state.HealthStatus != "healthy" {
			return false
		}
	}
	return true
}

func validateComposeFile(path string) error {
	if path == "" {
		return errors.New("compose file path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read compose file: %w", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parse compose file: %w", err)
	}
	if _, ok := parsed["services"]; !ok {
		return errors.New("compose file is missing services section")
	}

	return nil
}

func dockerIDs(ctx context.Context, args ...string) ([]string, error) {
	output, err := dockerOutput(ctx, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids = append(ids, line)
	}
	return ids, nil
}

func dockerOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
