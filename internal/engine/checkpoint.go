package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Checkpoint represents a saved state of a build loop execution.
// Checkpoints are saved at phase boundaries so that if the build process
// crashes, it can resume from the last completed phase rather than
// starting over from scratch.
type Checkpoint struct {
	IssueRef   string    `json:"issue_ref"`
	Phase      string    `json:"phase"`      // "plan", "code", "review"
	Iteration  int       `json:"iteration"`
	BranchName string    `json:"branch_name"`
	PlanOutput string    `json:"plan_output,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// CheckpointStore saves and loads checkpoints for build loop recovery.
type CheckpointStore interface {
	// Save persists a checkpoint for the given issue reference.
	Save(ctx context.Context, cp *Checkpoint) error

	// Load retrieves a previously saved checkpoint for the given issue reference.
	// Returns nil, nil if no checkpoint exists.
	Load(ctx context.Context, issueRef string) (*Checkpoint, error)

	// Delete removes a checkpoint for the given issue reference.
	Delete(ctx context.Context, issueRef string) error
}

// FileCheckpointStore persists checkpoints as JSON files in a directory.
// Each checkpoint is stored as a separate file named by a sanitized
// version of the issue reference.
type FileCheckpointStore struct {
	dir string
}

// NewFileCheckpointStore creates a new FileCheckpointStore that saves
// checkpoints in the given directory. The directory is created if it
// does not exist.
func NewFileCheckpointStore(dir string) (*FileCheckpointStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating checkpoint directory: %w", err)
	}
	return &FileCheckpointStore{dir: dir}, nil
}

// Save persists a checkpoint as a JSON file.
func (s *FileCheckpointStore) Save(_ context.Context, cp *Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("saving checkpoint: checkpoint is nil")
	}
	if cp.IssueRef == "" {
		return fmt.Errorf("saving checkpoint: issue_ref is required")
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}

	path := s.pathFor(cp.IssueRef)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing checkpoint file: %w", err)
	}

	return nil
}

// Load retrieves a checkpoint from its JSON file.
// Returns nil, nil if no checkpoint file exists.
func (s *FileCheckpointStore) Load(_ context.Context, issueRef string) (*Checkpoint, error) {
	if issueRef == "" {
		return nil, fmt.Errorf("loading checkpoint: issue_ref is required")
	}

	path := s.pathFor(issueRef)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading checkpoint file: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshaling checkpoint: %w", err)
	}

	return &cp, nil
}

// Delete removes a checkpoint file.
func (s *FileCheckpointStore) Delete(_ context.Context, issueRef string) error {
	if issueRef == "" {
		return fmt.Errorf("deleting checkpoint: issue_ref is required")
	}

	path := s.pathFor(issueRef)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing checkpoint file: %w", err)
	}

	return nil
}

// pathFor returns the file path for a given issue reference.
// The issue reference is sanitized to be a valid file name.
func (s *FileCheckpointStore) pathFor(issueRef string) string {
	return filepath.Join(s.dir, sanitizeFilename(issueRef)+".json")
}

// sanitizeFilename replaces characters that are not safe in file names.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		":", "_",
		"#", "_",
		" ", "_",
		"\\", "_",
	)
	return replacer.Replace(name)
}
