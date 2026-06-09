package store

import (
	"sync"
	"time"

	"github.com/shivansh-source/stateful-playwright/internal/models"
)

type EnvironmentStore struct {
	mu           sync.RWMutex
	environments map[string]models.Environment
}

func NewEnvironmentStore() *EnvironmentStore {
	return &EnvironmentStore{environments: make(map[string]models.Environment)}
}

func (s *EnvironmentStore) Upsert(env models.Environment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if existing, ok := s.environments[env.ID]; ok {
		env.CreatedAt = existing.CreatedAt
	}
	if env.CreatedAt.IsZero() {
		env.CreatedAt = now
	}
	env.UpdatedAt = now
	s.environments[env.ID] = env
}

func (s *EnvironmentStore) Get(id string) (models.Environment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	env, ok := s.environments[id]
	return env, ok
}

func (s *EnvironmentStore) List() []models.Environment {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]models.Environment, 0, len(s.environments))
	for _, env := range s.environments {
		out = append(out, env)
	}
	return out
}

func (s *EnvironmentStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.environments, id)
}
