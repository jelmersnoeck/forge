package server

import (
	"context"
	"testing"
	"time"
)

func TestRunCleanup(t *testing.T) {
	store := NewMemoryJobStore()
	ctx := context.Background()

	// Create two jobs with a time gap.
	store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusCompleted, Request: "old"})
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	store.Create(ctx, &Job{Type: JobTypeBuild, Status: JobStatusCompleted, Request: "recent"})

	// DeleteBefore the cutoff should remove the old job.
	n, err := store.DeleteBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}

	// Verify only 1 job remains.
	jobs, _ := store.List(ctx, JobFilter{})
	if len(jobs) != 1 {
		t.Errorf("expected 1 remaining job, got %d", len(jobs))
	}
}
