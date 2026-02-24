package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestJobQueue_Submit(t *testing.T) {
	broker := NewSSEBroker()
	q := NewJobQueue(NewMemoryJobStore(), broker)

	job := &Job{
		Type:    JobTypeBuild,
		Request: map[string]string{"issue": "gh:org/repo#1"},
	}

	id := q.Submit(job)
	if id == "" {
		t.Fatal("expected non-empty job ID")
	}

	got, ok := q.Get(id)
	if !ok {
		t.Fatal("expected to find submitted job")
	}
	if got.Status != JobStatusPending {
		t.Errorf("expected status %q, got %q", JobStatusPending, got.Status)
	}
	if got.Type != JobTypeBuild {
		t.Errorf("expected type %q, got %q", JobTypeBuild, got.Type)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestJobQueue_Get_NotFound(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)
	_, ok := q.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent job")
	}
}

func TestJobQueue_List(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	// Submit 5 jobs with staggered times.
	for i := 0; i < 5; i++ {
		q.Submit(&Job{
			Type:    JobTypeBuild,
			Request: map[string]int{"n": i},
		})
		time.Sleep(time.Millisecond) // Ensure distinct timestamps.
	}

	// List all.
	all := q.List(10, 0)
	if len(all) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(all))
	}
	// Verify newest first.
	for i := 0; i < len(all)-1; i++ {
		if all[i].CreatedAt.Before(all[i+1].CreatedAt) {
			t.Errorf("jobs not sorted newest first at index %d", i)
		}
	}

	// Paginate.
	page := q.List(2, 1)
	if len(page) != 2 {
		t.Errorf("expected 2 jobs with limit=2 offset=1, got %d", len(page))
	}

	// Offset beyond range.
	empty := q.List(10, 100)
	if len(empty) != 0 {
		t.Errorf("expected 0 jobs with large offset, got %d", len(empty))
	}
}

func TestJobQueue_Run(t *testing.T) {
	broker := NewSSEBroker()
	q := NewJobQueue(NewMemoryJobStore(), broker)

	processed := make(chan string, 1)
	q.RegisterHandler(JobTypeBuild, func(ctx context.Context, job *Job) (interface{}, error) {
		processed <- job.ID
		return map[string]string{"result": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go q.Run(ctx)

	job := &Job{
		Type:    JobTypeBuild,
		Request: map[string]string{"issue": "gh:org/repo#1"},
	}
	id := q.Submit(job)

	select {
	case processedID := <-processed:
		if processedID != id {
			t.Errorf("expected processed job ID %q, got %q", id, processedID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for job to be processed")
	}

	// Wait a moment for status update.
	time.Sleep(50 * time.Millisecond)

	got, _ := q.Get(id)
	if got.Status != JobStatusCompleted {
		t.Errorf("expected status %q, got %q", JobStatusCompleted, got.Status)
	}
}

func TestJobQueue_Run_Failure(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	q.RegisterHandler(JobTypeBuild, func(ctx context.Context, job *Job) (interface{}, error) {
		return nil, context.DeadlineExceeded
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go q.Run(ctx)

	id := q.Submit(&Job{
		Type:    JobTypeBuild,
		Request: "test",
	})

	time.Sleep(100 * time.Millisecond)

	got, _ := q.Get(id)
	if got.Status != JobStatusFailed {
		t.Errorf("expected status %q, got %q", JobStatusFailed, got.Status)
	}
	if got.Error == "" {
		t.Error("expected non-empty error on failed job")
	}
}

func TestJobQueue_AddLog(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	id := q.Submit(&Job{
		Type:    JobTypeBuild,
		Request: "test",
	})

	q.AddLog(id, "step 1")
	q.AddLog(id, "step 2")

	got, _ := q.Get(id)
	if len(got.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(got.Logs))
	}
	if got.Logs[0].Message != "step 1" {
		t.Errorf("expected first log %q, got %q", "step 1", got.Logs[0].Message)
	}
}

func TestJobQueue_Concurrent(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	q.RegisterHandler(JobTypeBuild, func(ctx context.Context, job *Job) (interface{}, error) {
		return "ok", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go q.Run(ctx)

	var wg sync.WaitGroup
	ids := make([]string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ids[idx] = q.Submit(&Job{
				Type:    JobTypeBuild,
				Request: idx,
			})
		}(i)
	}
	wg.Wait()

	// Verify all jobs exist.
	for i, id := range ids {
		if _, ok := q.Get(id); !ok {
			t.Errorf("job %d (ID %s) not found", i, id)
		}
	}
}

func TestJobQueue_Run_PanicRecovery(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	q.RegisterHandler(JobTypeBuild, func(ctx context.Context, job *Job) (interface{}, error) {
		panic("handler exploded")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go q.Run(ctx)

	id := q.Submit(&Job{
		Type:    JobTypeBuild,
		Request: "test",
	})

	time.Sleep(200 * time.Millisecond)

	got, ok := q.Get(id)
	if !ok {
		t.Fatal("expected to find job after panic")
	}
	if got.Status != JobStatusFailed {
		t.Errorf("expected status %q after panic, got %q", JobStatusFailed, got.Status)
	}
	if got.Error == "" {
		t.Error("expected non-empty error on panicked job")
	}
	if len(got.Error) < 6 || got.Error[:6] != "panic:" {
		t.Errorf("expected error to start with 'panic:', got %q", got.Error)
	}
}

func TestJobQueue_Run_PanicRecoveryWorkerSurvives(t *testing.T) {
	q := NewJobQueue(NewMemoryJobStore(), nil)

	callCount := 0
	q.RegisterHandler(JobTypeBuild, func(ctx context.Context, job *Job) (interface{}, error) {
		callCount++
		if callCount == 1 {
			panic("first job panics")
		}
		return "ok", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go q.Run(ctx)

	// First job: will panic.
	id1 := q.Submit(&Job{Type: JobTypeBuild, Request: "panic-job"})
	time.Sleep(200 * time.Millisecond)

	// Second job: should still be processed because worker survived the panic.
	id2 := q.Submit(&Job{Type: JobTypeBuild, Request: "normal-job"})
	time.Sleep(200 * time.Millisecond)

	got1, _ := q.Get(id1)
	if got1.Status != JobStatusFailed {
		t.Errorf("first job: expected status %q, got %q", JobStatusFailed, got1.Status)
	}

	got2, _ := q.Get(id2)
	if got2.Status != JobStatusCompleted {
		t.Errorf("second job: expected status %q, got %q (worker died after panic)", JobStatusCompleted, got2.Status)
	}
}

func TestGenerateJobID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateJobID()
		if id == "" {
			t.Fatal("generated empty job ID")
		}
		if seen[id] {
			t.Fatalf("duplicate job ID: %s", id)
		}
		seen[id] = true
	}
}
