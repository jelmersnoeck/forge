package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileCheckpointStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	cp := &Checkpoint{
		IssueRef:   "gh:test/repo#42",
		Phase:      "plan",
		Iteration:  0,
		BranchName: "forge/github-42",
		PlanOutput: "1. Create file\n2. Implement logic",
		CreatedAt:  now,
	}

	ctx := context.Background()
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file was created.
	expectedFile := filepath.Join(dir, "gh_test_repo_42.json")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("expected checkpoint file at %s", expectedFile)
	}

	// Load it back.
	loaded, err := store.Load(ctx, "gh:test/repo#42")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil checkpoint")
	}

	if loaded.IssueRef != cp.IssueRef {
		t.Errorf("IssueRef = %q, want %q", loaded.IssueRef, cp.IssueRef)
	}
	if loaded.Phase != cp.Phase {
		t.Errorf("Phase = %q, want %q", loaded.Phase, cp.Phase)
	}
	if loaded.Iteration != cp.Iteration {
		t.Errorf("Iteration = %d, want %d", loaded.Iteration, cp.Iteration)
	}
	if loaded.BranchName != cp.BranchName {
		t.Errorf("BranchName = %q, want %q", loaded.BranchName, cp.BranchName)
	}
	if loaded.PlanOutput != cp.PlanOutput {
		t.Errorf("PlanOutput = %q, want %q", loaded.PlanOutput, cp.PlanOutput)
	}
	if !loaded.CreatedAt.Equal(cp.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", loaded.CreatedAt, cp.CreatedAt)
	}
}

func TestFileCheckpointStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	cp, err := store.Load(context.Background(), "gh:test/repo#999")
	if err != nil {
		t.Fatalf("Load for missing checkpoint should not error: %v", err)
	}
	if cp != nil {
		t.Error("expected nil checkpoint for missing issue")
	}
}

func TestFileCheckpointStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()
	cp := &Checkpoint{
		IssueRef:   "gh:test/repo#10",
		Phase:      "code",
		Iteration:  1,
		BranchName: "forge/github-10",
		CreatedAt:  time.Now(),
	}

	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify it exists.
	loaded, err := store.Load(ctx, "gh:test/repo#10")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected checkpoint to exist after save")
	}

	// Delete it.
	if err := store.Delete(ctx, "gh:test/repo#10"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's gone.
	loaded, err = store.Load(ctx, "gh:test/repo#10")
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil checkpoint after delete")
	}
}

func TestFileCheckpointStore_DeleteMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	// Deleting a non-existent checkpoint should not error.
	if err := store.Delete(context.Background(), "gh:test/repo#404"); err != nil {
		t.Fatalf("Delete for missing checkpoint should not error: %v", err)
	}
}

func TestFileCheckpointStore_Overwrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Save initial checkpoint.
	cp1 := &Checkpoint{
		IssueRef:   "gh:test/repo#5",
		Phase:      "plan",
		Iteration:  0,
		BranchName: "forge/github-5",
		PlanOutput: "initial plan",
		CreatedAt:  time.Now(),
	}
	if err := store.Save(ctx, cp1); err != nil {
		t.Fatalf("Save 1: %v", err)
	}

	// Save updated checkpoint for same issue.
	cp2 := &Checkpoint{
		IssueRef:   "gh:test/repo#5",
		Phase:      "code",
		Iteration:  1,
		BranchName: "forge/github-5",
		PlanOutput: "initial plan",
		CreatedAt:  time.Now(),
	}
	if err := store.Save(ctx, cp2); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	// Load and verify it's the updated one.
	loaded, err := store.Load(ctx, "gh:test/repo#5")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Phase != "code" {
		t.Errorf("Phase = %q, want %q", loaded.Phase, "code")
	}
	if loaded.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", loaded.Iteration)
	}
}

func TestFileCheckpointStore_SaveNilCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	err = store.Save(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil checkpoint")
	}
}

func TestFileCheckpointStore_SaveEmptyIssueRef(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	err = store.Save(context.Background(), &Checkpoint{
		Phase: "plan",
	})
	if err == nil {
		t.Error("expected error for empty issue_ref")
	}
}

func TestFileCheckpointStore_LoadEmptyIssueRef(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	_, err = store.Load(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty issue_ref")
	}
}

func TestFileCheckpointStore_DeleteEmptyIssueRef(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	err = store.Delete(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty issue_ref")
	}
}

func TestFileCheckpointStore_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "checkpoints")
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	// Verify the directory was created.
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected path to be a directory")
	}

	// Verify we can save and load.
	ctx := context.Background()
	cp := &Checkpoint{
		IssueRef:  "gh:test/repo#1",
		Phase:     "plan",
		CreatedAt: time.Now(),
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "gh:test/repo#1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Error("expected to load checkpoint")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gh:test/repo#42", "gh_test_repo_42"},
		{"jira:PROJECT-123", "jira_PROJECT-123"},
		{"linear:TEAM-789", "linear_TEAM-789"},
		{"simple", "simple"},
		{"a/b/c", "a_b_c"},
		{"colon:slash/hash#space back\\slash", "colon_slash_hash_space_back_slash"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
