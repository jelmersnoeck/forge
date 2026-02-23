package agent

import (
	"fmt"
	"sync"
)

// Registry is a map-based registry for looking up agents by name.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]Agent),
	}
}

// Register adds an agent to the registry under the given name.
// If an agent with the same name already exists, it is overwritten.
func (r *Registry) Register(name string, agent Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[name] = agent
}

// Get returns the agent registered under the given name.
// The second return value indicates whether the agent was found.
func (r *Registry) Get(name string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok
}

// MustGet returns the agent registered under the given name or returns an error.
func (r *Registry) MustGet(name string) (Agent, error) {
	a, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", name)
	}
	return a, nil
}

// List returns the names of all registered agents.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}
