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
	"sort"
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

// JobQueue is an in-memory, thread-safe job queue with background processing.
type JobQueue struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	pending  chan string
	handlers map[JobType]JobHandler
	broker   *SSEBroker
}

// JobHandler processes a job and returns a result or error.
type JobHandler func(ctx context.Context, job *Job) (interface{}, error)

// NewJobQueue creates a new in-memory job queue.
func NewJobQueue(broker *SSEBroker) *JobQueue {
	return &JobQueue{
		jobs:     make(map[string]*Job),
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
	q.mu.Lock()
	defer q.mu.Unlock()

	if job.ID == "" {
		job.ID = generateJobID()
	}
	now := time.Now().UTC()
	job.Status = JobStatusPending
	job.CreatedAt = now
	job.UpdatedAt = now
	q.jobs[job.ID] = job

	// Non-blocking send to the pending channel.
	select {
	case q.pending <- job.ID:
	default:
		// Queue is full; the job stays pending and will be picked up
		// when Run drains the channel.
	}

	if q.broker != nil {
		q.broker.Publish(job.ID, Event{
			Type: "job_submitted",
			Data: map[string]interface{}{
				"job_id": job.ID,
				"type":   job.Type,
				"status": job.Status,
			},
		})
	}

	return job.ID
}

// Get retrieves a job by ID.
func (q *JobQueue) Get(id string) (*Job, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	j, ok := q.jobs[id]
	if !ok {
		return nil, false
	}
	// Return a shallow copy to avoid races on the caller's side.
	cp := *j
	cp.Logs = make([]LogEntry, len(j.Logs))
	copy(cp.Logs, j.Logs)
	return &cp, true
}

// List returns jobs ordered by creation time (newest first) with pagination.
func (q *JobQueue) List(limit, offset int) []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()

	all := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		all = append(all, j)
	}
	sort.Slice(all, func(i, k int) bool {
		return all[i].CreatedAt.After(all[k].CreatedAt)
	})

	if offset >= len(all) {
		return nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}

	result := make([]*Job, end-offset)
	for i, j := range all[offset:end] {
		cp := *j
		cp.Logs = make([]LogEntry, len(j.Logs))
		copy(cp.Logs, j.Logs)
		result[i] = &cp
	}
	return result
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
			q.mu.Lock()
			if job, ok := q.jobs[jobID]; ok {
				job.Status = JobStatusFailed
				job.Error = fmt.Sprintf("panic: %v", r)
				job.UpdatedAt = time.Now().UTC()
			}
			q.mu.Unlock()

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

	q.mu.Lock()
	job, ok := q.jobs[jobID]
	if !ok {
		q.mu.Unlock()
		return
	}
	handler, hasHandler := q.handlers[job.Type]
	if !hasHandler {
		job.Status = JobStatusFailed
		job.Error = "no handler registered for job type: " + string(job.Type)
		job.UpdatedAt = time.Now().UTC()
		q.mu.Unlock()
		return
	}
	job.Status = JobStatusRunning
	job.UpdatedAt = time.Now().UTC()
	q.mu.Unlock()

	if q.broker != nil {
		q.broker.Publish(jobID, Event{
			Type: "job_started",
			Data: map[string]interface{}{
				"job_id": jobID,
				"type":   job.Type,
			},
		})
	}

	result, err := handler(ctx, job)

	q.mu.Lock()
	if err != nil {
		job.Status = JobStatusFailed
		job.Error = err.Error()
	} else {
		job.Status = JobStatusCompleted
		job.Result = result
	}
	job.UpdatedAt = time.Now().UTC()
	q.mu.Unlock()

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
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[jobID]; ok {
		j.Logs = append(j.Logs, LogEntry{
			Time:    time.Now().UTC(),
			Message: message,
		})
	}
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
