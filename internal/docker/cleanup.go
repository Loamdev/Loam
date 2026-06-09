package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
)

func verifyCleanup(ctx context.Context, cli *client.Client, envID string) error {
	_ = cli
	project := projectName(envID)

	containers, err := dockerIDs(
		ctx,
		"ps",
		"-a",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return fmt.Errorf("list containers for cleanup verification: %w", err)
	}
	if len(containers) != 0 {
		return fmt.Errorf("cleanup incomplete for %s: %d container(s) still present", project, len(containers))
	}

	networks, err := dockerIDs(
		ctx,
		"network",
		"ls",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return fmt.Errorf("list networks for cleanup verification: %w", err)
	}
	if len(networks) != 0 {
		return fmt.Errorf("cleanup incomplete for %s: %d network(s) still present", project, len(networks))
	}

	volumes, err := dockerIDs(
		ctx,
		"volume",
		"ls",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.Name}}",
	)
	if err != nil {
		return fmt.Errorf("list volumes for cleanup verification: %w", err)
	}
	if len(volumes) != 0 {
		return fmt.Errorf("cleanup incomplete for %s: %d volume(s) still present", project, len(volumes))
	}

	return nil
}
