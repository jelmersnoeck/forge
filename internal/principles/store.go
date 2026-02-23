package principles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Store loads, caches, and queries principle sets from YAML files.
type Store struct {
	mu   sync.RWMutex
	sets map[string]*PrincipleSet
}

// NewStore creates a new empty Store.
func NewStore() *Store {
	return &Store{
		sets: make(map[string]*PrincipleSet),
	}
}

// LoadDir loads all .yaml/.yml principle files from a directory.
func (s *Store) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading principles directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := s.LoadFile(path); err != nil {
			return fmt.Errorf("loading principle file %s: %w", path, err)
		}
	}
	return nil
}

// LoadFile loads a single principle YAML file into the store.
func (s *Store) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var set PrincipleSet
	if err := yaml.Unmarshal(data, &set); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	if set.Name == "" {
		return fmt.Errorf("principle set in %s has no name", path)
	}

	s.mu.Lock()
	s.sets[set.Name] = &set
	s.mu.Unlock()
	return nil
}

// Get returns a principle set by name.
func (s *Store) Get(name string) (*PrincipleSet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set, ok := s.sets[name]
	return set, ok
}

// Load returns all principles from the named sets, merged into a flat list.
func (s *Store) Load(_ context.Context, names ...string) ([]Principle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Principle
	for _, name := range names {
		set, ok := s.sets[name]
		if !ok {
			return nil, fmt.Errorf("principle set %q not found", name)
		}
		result = append(result, set.Principles...)
	}
	return result, nil
}

// All returns every loaded principle.
func (s *Store) All() []Principle {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Principle
	for _, set := range s.sets {
		result = append(result, set.Principles...)
	}
	return result
}

// Sets returns the names of all loaded principle sets.
func (s *Store) Sets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.sets))
	for name := range s.sets {
		names = append(names, name)
	}
	return names
}
