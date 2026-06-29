// Package sessionstate persists a small routing-metadata file (.forge-state)
// in the worktree root so an agent can resume its orchestrator phase, history
// IDs, and pipeline position after a process restart.
//
// The conversation history itself is NOT stored here — it already lives in the
// session JSONL and is replayed via loop.Resume(). This file holds only the
// ~200 bytes of routing metadata needed to know which history ID maps to the
// coder/spec/QA conversation and which phase the orchestrator was in.
package sessionstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateFile is the metadata file written to the worktree root.
const StateFile = ".forge-state"

// Version is the current schema version. Bump on breaking changes; readers
// reject unknown versions rather than guessing.
const Version = 1

// State is the orchestrator routing metadata persisted across restarts.
type State struct {
	Version          int       `json:"version"`
	SessionID        string    `json:"sessionID"`
	Phase            string    `json:"phase"`            // "idle", "qa", "investigate", "orchestrator", "done"
	HistoryID        string    `json:"coderHistoryID"`   // coder/plain loop history for Resume()
	QAHistoryID      string    `json:"qaHistoryID"`      // Q&A conversation history
	InvestigateID    string    `json:"investigateID"`    // investigation conversation history
	OrchestratorDone bool      `json:"orchestratorDone"` // true once a task pipeline ran
	HeadCommit       string    `json:"headCommit"`       // worktree HEAD at write time
	UpdatedAt        time.Time `json:"updatedAt"`
}

// path returns the .forge-state path inside the given worktree root.
func path(worktreeRoot string) string {
	return filepath.Join(worktreeRoot, StateFile)
}

// Write atomically persists state to worktreeRoot/.forge-state. The version is
// stamped automatically. Write is best-effort from the caller's perspective:
// callers log but do not fail a turn on a write error.
func Write(worktreeRoot string, s State) error {
	s.Version = Version
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	tmp := path(worktreeRoot) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	if err := os.Rename(tmp, path(worktreeRoot)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session state: %w", err)
	}
	return nil
}

// Read loads state from worktreeRoot/.forge-state. Returns os.ErrNotExist
// (wrapped) when the file is absent so callers can treat a missing file as a
// fresh session. Returns an error for an unknown schema version.
func Read(worktreeRoot string) (State, error) {
	data, err := os.ReadFile(path(worktreeRoot))
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("parse session state: %w", err)
	}
	if s.Version != Version {
		return State{}, fmt.Errorf("unsupported session state version %d (want %d)", s.Version, Version)
	}
	return s, nil
}
