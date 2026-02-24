// Package server implements the Forge HTTP server, REST API, webhooks,
// job queue, and SSE streaming.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// JobType identifies the kind of work a job performs.
type JobType string

const (
	JobTypeBuild  JobType = "build"
	JobTypeReview JobType = "review"
	JobTypePlan   JobType = "plan"
)

// JobStatus tracks the lifecycle of a job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// Job represents a unit of asynchronous work.
type Job struct {
	ID        string      `json:"id"`
	Type      JobType     `json:"type"`
	Status    JobStatus   `json:"status"`
	Request   interface{} `json:"request"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Logs      []LogEntry  `json:"logs,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// LogEntry is a timestamped log message attached to a job.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// JobQueue is a thread-safe job queue with background processing.
// It delegates persistence to a JobStore and owns orchestration
// (handlers, pending channel, SSE events, panic recovery).
type JobQueue struct {
	store    JobStore
	pending  chan string
	handlers map[JobType]JobHandler
	broker   *SSEBroker
	mu       sync.RWMutex // protects handlers map only
}

// JobHandler processes a job and returns a result or error.
type JobHandler func(ctx context.Context, job *Job) (interface{}, error)

// NewJobQueue creates a new job queue backed by the given store.
func NewJobQueue(store JobStore, broker *SSEBroker) *JobQueue {
	return &JobQueue{
		store:    store,
		pending:  make(chan string, 1000),
		handlers: make(map[JobType]JobHandler),
		broker:   broker,
	}
}

// RegisterHandler registers a handler function for a given job type.
func (q *JobQueue) RegisterHandler(jobType JobType, handler JobHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[jobType] = handler
}

// Submit adds a new job to the queue and returns its ID.
func (q *JobQueue) Submit(job *Job) string {
	job.Status = JobStatusPending
	id, err := q.store.Create(context.Background(), job)
	if err != nil {
		slog.Error("failed to create job", "error", err)
		return ""
	}

	// Non-blocking send to the pending channel.
	select {
	case q.pending <- id:
	default:
		// Queue is full; the job stays pending and will be picked up
		// when Run drains the channel.
	}

	if q.broker != nil {
		q.broker.Publish(id, Event{
			Type: "job_submitted",
			Data: map[string]interface{}{
				"job_id": id,
				"type":   job.Type,
				"status": job.Status,
			},
		})
	}

	return id
}

// Get retrieves a job by ID.
func (q *JobQueue) Get(id string) (*Job, bool) {
	job, err := q.store.Get(context.Background(), id)
	if err != nil || job == nil {
		return nil, false
	}
	return job, true
}

// List returns jobs ordered by creation time (newest first) with pagination.
func (q *JobQueue) List(limit, offset int) []*Job {
	jobs, err := q.store.List(context.Background(), JobFilter{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		return nil
	}
	return jobs
}

// Run starts the background worker that processes pending jobs.
// It blocks until ctx is cancelled.
func (q *JobQueue) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case jobID := <-q.pending:
			q.processJob(ctx, jobID)
		}
	}
}

func (q *JobQueue) processJob(ctx context.Context, jobID string) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			slog.Error("job handler panicked",
				"job_id", jobID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(stack),
			)
			job, _ := q.store.Get(ctx, jobID)
			if job != nil {
				job.Status = JobStatusFailed
				job.Error = fmt.Sprintf("panic: %v", r)
				job.UpdatedAt = time.Now().UTC()
				q.store.Update(ctx, job)
			}

			if q.broker != nil {
				q.broker.Publish(jobID, Event{
					Type: "job_completed",
					Data: map[string]interface{}{
						"job_id": jobID,
						"status": JobStatusFailed,
						"error":  fmt.Sprintf("panic: %v", r),
					},
				})
			}
		}
	}()

	job, err := q.store.Get(ctx, jobID)
	if err != nil || job == nil {
		return
	}

	q.mu.RLock()
	handler, hasHandler := q.handlers[job.Type]
	q.mu.RUnlock()

	if !hasHandler {
		job.Status = JobStatusFailed
		job.Error = "no handler registered for job type: " + string(job.Type)
		job.UpdatedAt = time.Now().UTC()
		q.store.Update(ctx, job)
		return
	}

	job.Status = JobStatusRunning
	job.UpdatedAt = time.Now().UTC()
	q.store.Update(ctx, job)

	if q.broker != nil {
		q.broker.Publish(jobID, Event{
			Type: "job_started",
			Data: map[string]interface{}{
				"job_id": jobID,
				"type":   job.Type,
			},
		})
	}

	result, handleErr := handler(ctx, job)

	job, _ = q.store.Get(ctx, jobID)
	if job == nil {
		return
	}
	if handleErr != nil {
		job.Status = JobStatusFailed
		job.Error = handleErr.Error()
	} else {
		job.Status = JobStatusCompleted
		job.Result = result
	}
	job.UpdatedAt = time.Now().UTC()
	q.store.Update(ctx, job)

	if q.broker != nil {
		q.broker.Publish(jobID, Event{
			Type: "job_completed",
			Data: map[string]interface{}{
				"job_id": jobID,
				"status": job.Status,
				"error":  job.Error,
			},
		})
	}
}

// AddLog appends a log entry to a job.
func (q *JobQueue) AddLog(jobID, message string) {
	q.store.AddLog(context.Background(), jobID, LogEntry{
		Time:    time.Now().UTC(),
		Message: message,
	})
}

// generateJobID creates a random hex string for job identification.
func generateJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if random fails.
		return "job-" + time.Now().UTC().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b)
}
