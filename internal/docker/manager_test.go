package docker

import "testing"

func TestProjectName(t *testing.T) {
	if got := projectName("env_test_1"); got != "playbrige-env_test_1" {
		t.Fatalf("unexpected project name: %s", got)
	}
}

func TestAllContainersReady(t *testing.T) {
	tests := []struct {
		name  string
		input []containerState
		ready bool
	}{
		{
			name:  "running without healthcheck",
			input: []containerState{{Running: true, HasHealthcheck: false}},
			ready: true,
		},
		{
			name:  "unhealthy healthcheck",
			input: []containerState{{Running: true, HasHealthcheck: true, HealthStatus: "starting"}},
			ready: false,
		},
		{
			name:  "not running",
			input: []containerState{{Running: false, HasHealthcheck: true, HealthStatus: "healthy"}},
			ready: false,
		},
		{
			name: "all healthy",
			input: []containerState{
				{Running: true, HasHealthcheck: true, HealthStatus: "healthy"},
				{Running: true, HasHealthcheck: false},
			},
			ready: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := allContainersReady(tc.input); got != tc.ready {
				t.Fatalf("ready=%v, expected %v", got, tc.ready)
			}
		})
	}
}
