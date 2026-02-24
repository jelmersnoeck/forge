package server

import (
	"context"
	"testing"
	"time"
)

// testJobStore runs the full JobStore contract against any implementation.
func testJobStore(t *testing.T, newStore func() JobStore) {
	t.Helper()

	t.Run("CreateAndGet", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		ctx := context.Background()

		job := &Job{
			Type:    JobTypeBuild,
			Status:  JobStatusPending,
			Request: map[string]string{"issue": "gh:org/repo#1"},
		}
		id, err := store.Create(ctx, job)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if id == "" {
			t.Fatal("expected non-empty ID")
		}

		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil {
			t.Fatal("expected job, got nil")
		}
		if got.Status != JobStatusPending {
			t.Errorf("status: got %q, want %q", got.Status, JobStatusPending)
		}
		if got.CreatedAt.IsZero() {
			t.Error("expected non-zero CreatedAt")
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		got, err := store.Get(context.Background(), "nonexistent")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for missing job, got %+v", got)
		}
	})

	t.Run("Update", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		ctx := context.Background()

		job := &Job{Type: JobTypeBuild, Status: JobStatusPending, Request: "test"}
		id, _ := store.Create(ctx, job)

		job.ID = id
		job.Status = JobStatusRunning
		job.UpdatedAt = time.Now().UTC()
		if err := store.Update(ctx, job); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got, _ := store.Get(ctx, id)
		if got.Status != JobStatusRunning {
			t.Errorf("status after update: got %q, want %q", got.Status, JobStatusRunning)
		}
	})

	t.Run("ListFilters", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		ctx := context.Background()

		// Create jobs of different types and statuses.
		store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusPending, Request: "a"})
		time.Sleep(time.Millisecond)
		store.Create(ctx, &Job{Type: JobTypeReview, Status: JobStatusCompleted, Request: "b"})
		time.Sleep(time.Millisecond)
		store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusCompleted, Request: "c"})

		// Filter by status.
		completed, _ := store.List(ctx, JobFilter{Status: JobStatusCompleted})
		if len(completed) != 2 {
			t.Errorf("completed filter: got %d, want 2", len(completed))
		}

		// Filter by type.
		builds, _ := store.List(ctx, JobFilter{Type: JobTypeBuild})
		if len(builds) != 2 {
			t.Errorf("build filter: got %d, want 2", len(builds))
		}

		// Pagination.
		page, _ := store.List(ctx, JobFilter{Limit: 1, Offset: 0})
		if len(page) != 1 {
			t.Errorf("pagination: got %d, want 1", len(page))
		}

		// Newest first.
		all, _ := store.List(ctx, JobFilter{})
		if len(all) < 2 {
			t.Fatal("expected at least 2 jobs")
		}
		if all[0].CreatedAt.Before(all[1].CreatedAt) {
			t.Error("expected newest first ordering")
		}
	})

	t.Run("AddLog", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		ctx := context.Background()

		id, _ := store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusPending, Request: "test"})
		store.AddLog(ctx, id, LogEntry{Time: time.Now().UTC(), Message: "step 1"})
		store.AddLog(ctx, id, LogEntry{Time: time.Now().UTC(), Message: "step 2"})

		got, _ := store.Get(ctx, id)
		if len(got.Logs) != 2 {
			t.Fatalf("logs: got %d, want 2", len(got.Logs))
		}
		if got.Logs[0].Message != "step 1" {
			t.Errorf("first log: got %q, want %q", got.Logs[0].Message, "step 1")
		}
	})

	t.Run("DeleteAndDeleteBefore", func(t *testing.T) {
		store := newStore()
		defer store.Close()
		ctx := context.Background()

		id1, _ := store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusPending, Request: "a"})
		time.Sleep(10 * time.Millisecond)
		cutoff := time.Now().UTC()
		time.Sleep(10 * time.Millisecond)
		id2, _ := store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusPending, Request: "b"})

		// Delete single.
		if err := store.Delete(ctx, id2); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		got, _ := store.Get(ctx, id2)
		if got != nil {
			t.Error("expected deleted job to be nil")
		}

		// DeleteBefore.
		n, err := store.DeleteBefore(ctx, cutoff)
		if err != nil {
			t.Fatalf("DeleteBefore: %v", err)
		}
		if n != 1 {
			t.Errorf("DeleteBefore: got %d deleted, want 1", n)
		}
		got, _ = store.Get(ctx, id1)
		if got != nil {
			t.Error("expected old job to be deleted")
		}
	})
}

func TestJobStoreContract_Memory(t *testing.T) {
	testJobStore(t, func() JobStore { return NewMemoryJobStore() })
}
