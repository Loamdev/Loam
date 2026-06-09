package models

import "time"

const (
	EnvironmentStatusCreating  = "creating"
	EnvironmentStatusReady     = "ready"
	EnvironmentStatusDestroyed = "destroyed"
)

type Environment struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
