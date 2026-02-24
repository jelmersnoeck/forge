package server

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryJobStore is an in-memory JobStore for testing and development.
type MemoryJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

// NewMemoryJobStore creates a new in-memory job store.
func NewMemoryJobStore() *MemoryJobStore {
	return &MemoryJobStore{
		jobs: make(map[string]*Job),
	}
}

func (s *MemoryJobStore) Create(_ context.Context, job *Job) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job.ID == "" {
		job.ID = generateJobID()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now

	cp := *job
	cp.Logs = make([]LogEntry, len(job.Logs))
	copy(cp.Logs, job.Logs)
	s.jobs[cp.ID] = &cp
	job.ID = cp.ID
	return cp.ID, nil
}

func (s *MemoryJobStore) Get(_ context.Context, id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.jobs[id]
	if !ok {
		return nil, nil
	}
	cp := *j
	cp.Logs = make([]LogEntry, len(j.Logs))
	copy(cp.Logs, j.Logs)
	return &cp, nil
}

func (s *MemoryJobStore) Update(_ context.Context, job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.jobs[job.ID]
	if !ok {
		return nil
	}
	existing.Status = job.Status
	existing.Result = job.Result
	existing.Error = job.Error
	existing.UpdatedAt = job.UpdatedAt
	return nil
}

func (s *MemoryJobStore) List(_ context.Context, f JobFilter) ([]*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []*Job
	for _, j := range s.jobs {
		if f.Status != "" && j.Status != f.Status {
			continue
		}
		if f.Type != "" && j.Type != f.Type {
			continue
		}
		if !f.Since.IsZero() && !j.CreatedAt.After(f.Since) {
			continue
		}
		if !f.Before.IsZero() && !j.CreatedAt.Before(f.Before) {
			continue
		}
		matches = append(matches, j)
	}

	sort.Slice(matches, func(i, k int) bool {
		return matches[i].CreatedAt.After(matches[k].CreatedAt)
	})

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := f.Offset
	if offset >= len(matches) {
		return nil, nil
	}
	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}

	result := make([]*Job, 0, end-offset)
	for _, j := range matches[offset:end] {
		cp := *j
		cp.Logs = make([]LogEntry, len(j.Logs))
		copy(cp.Logs, j.Logs)
		result = append(result, &cp)
	}
	return result, nil
}

func (s *MemoryJobStore) AddLog(_ context.Context, jobID string, entry LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if j, ok := s.jobs[jobID]; ok {
		j.Logs = append(j.Logs, entry)
	}
	return nil
}

func (s *MemoryJobStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
	return nil
}

func (s *MemoryJobStore) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int64
	for id, j := range s.jobs {
		if j.CreatedAt.Before(before) {
			delete(s.jobs, id)
			count++
		}
	}
	return count, nil
}

func (s *MemoryJobStore) Close() error {
	return nil
}
