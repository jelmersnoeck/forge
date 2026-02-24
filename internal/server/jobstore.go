package server

import (
	"context"
	"time"
)

// JobFilter defines query parameters for listing jobs.
type JobFilter struct {
	Status   JobStatus // Filter by status (empty = all).
	Type     JobType   // Filter by type (empty = all).
	IssueRef string    // Filter by issue reference (empty = all).
	Since    time.Time // Jobs created after this time (zero = no filter).
	Before   time.Time // Jobs created before this time (zero = no filter).
	Limit    int       // Max results (0 = default 20).
	Offset   int       // Pagination offset.
}

// JobStore abstracts job persistence. Implementations must be safe
// for concurrent use.
type JobStore interface {
	// Create persists a new job and returns its assigned ID.
	Create(ctx context.Context, job *Job) (string, error)

	// Get retrieves a job by ID. Returns nil, nil if not found.
	Get(ctx context.Context, id string) (*Job, error)

	// Update persists changes to an existing job's mutable fields
	// (Status, Result, Error, UpdatedAt).
	Update(ctx context.Context, job *Job) error

	// List returns jobs matching the filter, ordered newest-first.
	List(ctx context.Context, filter JobFilter) ([]*Job, error)

	// AddLog appends a timestamped log entry to a job.
	AddLog(ctx context.Context, jobID string, entry LogEntry) error

	// Delete removes a job by ID.
	Delete(ctx context.Context, id string) error

	// DeleteBefore removes all jobs created before the given time.
	// Returns the number of deleted jobs.
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)

	// Close releases any resources held by the store.
	Close() error
}
