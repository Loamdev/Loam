package docker

import (
	"context"
	"fmt"
	"os/exec"
)

func projectName(envID string) string {
	return "playbrige-" + envID
}

func (m *DockerManager) composeUp(ctx context.Context, envID, composeFilePath string) error {
	project := projectName(envID)
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composeFilePath, "-p", project, "up", "-d")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up failed for %s: %w: %s", project, err, string(output))
	}
	return nil
}

func (m *DockerManager) composeDown(ctx context.Context, envID, composeFilePath string) error {
	project := projectName(envID)
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composeFilePath, "-p", project, "down", "-v")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down failed for %s: %w: %s", project, err, string(output))
	}
	return nil
}
