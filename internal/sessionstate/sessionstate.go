// Package sessionstate persists a small routing-metadata file
// (.forge/.forge-state) in the worktree so an agent can resume its orchestrator phase, history
// IDs, and pipeline position after a process restart.
//
// The conversation history itself is NOT stored here — it already lives in the
// session JSONL and is replayed via loop.Resume(). This file holds only the
// ~200 bytes of routing metadata needed to know which history ID maps to the
// coder/spec/QA conversation and which phase the orchestrator was in.
package sessionstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StateFile is the bare metadata filename (unchanged): ".forge-state".
const StateFile = ".forge-state"

// StateDir is the subdirectory under the worktree root where state lives.
const StateDir = ".forge"

// Version is the current schema version. Bump on breaking changes; readers
// reject unknown versions rather than guessing.
const Version = 1

// State is the orchestrator routing metadata persisted across restarts.
type State struct {
	Version          int       `json:"version"`
	SessionID        string    `json:"sessionID"`
	Phase            string    `json:"phase"`            // "idle", "qa", "investigate", "triage", "orchestrator", "done"
	HistoryID        string    `json:"coderHistoryID"`   // coder/plain loop history for Resume()
	QAHistoryID      string    `json:"qaHistoryID"`      // Q&A conversation history
	InvestigateID    string    `json:"investigateID"`    // investigation conversation history
	TriageID         string    `json:"triageID"`         // triage conversation history
	OrchestratorDone bool      `json:"orchestratorDone"` // true once a task pipeline ran
	HeadCommit       string    `json:"headCommit"`       // worktree HEAD at write time
	UpdatedAt        time.Time `json:"updatedAt"`
}

// RelPath returns the worktree-relative path to the state file
// (".forge/.forge-state"), for callers/tests that need to locate it.
func RelPath() string {
	return filepath.Join(StateDir, StateFile)
}

// path returns the absolute state-file path inside worktreeRoot/.forge.
func path(worktreeRoot string) string {
	return filepath.Join(worktreeRoot, StateDir, StateFile)
}

// legacyPath returns the pre-migration root-level path used as a read fallback.
func legacyPath(worktreeRoot string) string {
	return filepath.Join(worktreeRoot, StateFile)
}

// Write atomically persists state to worktreeRoot/.forge/.forge-state, creating
// .forge/ if needed and ensuring .forge/.gitignore ignores .forge-state. The
// version is stamped automatically. Write is best-effort from the caller's
// perspective: callers log but do not fail a turn on a write error. A failure
// to seed .forge/.gitignore is non-fatal — it is logged but does not fail an
// otherwise-successful state write.
func Write(worktreeRoot string, s State) error {
	s.Version = Version
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	dir := filepath.Join(worktreeRoot, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tmp := path(worktreeRoot) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		removeTemp(tmp)
		return fmt.Errorf("write session state: %w", err)
	}
	if err := os.Rename(tmp, path(worktreeRoot)); err != nil {
		removeTemp(tmp)
		return fmt.Errorf("rename session state: %w", err)
	}
	if err := seedGitignore(dir); err != nil {
		log.Printf("sessionstate: seed .forge/.gitignore: %v", err)
	}
	return nil
}

// removeTemp deletes a leftover atomic-write temp file. A missing file is not
// an error (WriteFile may have failed before creating it); any other removal
// failure is logged so a lingering temp file is visible during debugging.
func removeTemp(tmp string) {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("sessionstate: remove temp state file %s: %v", tmp, err)
	}
}

// seedGitignore ensures dir/.gitignore contains an exact ".forge-state" line.
// Idempotent: a matching line (ignoring surrounding whitespace) leaves the file
// byte-identical. Only the .forge-state entry is added; nothing else is touched.
func seedGitignore(dir string) error {
	gi := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gi)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.WriteFile(gi, []byte(StateFile+"\n"), 0o644)
	case err != nil:
		return err
	}

	ignored, err := ignoresStateFile(data)
	if err != nil {
		return err
	}
	if ignored {
		return nil // already ignored — idempotent
	}
	return os.WriteFile(gi, appendStateFileLine(data), 0o644)
}

// ignoresStateFile reports whether gitignore content already has an exact
// ".forge-state" line (ignoring surrounding whitespace). A commented or
// mid-line occurrence does not count — git would not honor it as the ignore
// pattern, so it is treated as absent. A scanner error is wrapped and
// propagated through seedGitignore to Write, which logs it for operators
// (gitignore seeding is best-effort and never aborts a state write).
func ignoresStateFile(data []byte) (bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == StateFile {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan gitignore: %w", err)
	}
	return false, nil
}

// appendStateFileLine returns data with a trailing ".forge-state\n" line,
// inserting a leading newline only when data lacks a final newline so no blank
// line is introduced.
func appendStateFileLine(data []byte) []byte {
	out := data
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, []byte(StateFile+"\n")...)
}

// Read loads state from worktreeRoot/.forge/.forge-state, falling back to the
// legacy worktreeRoot/.forge-state when the new path is absent. Returns
// os.ErrNotExist (wrapped) when neither file exists so callers can treat a
// missing file as a fresh session. A parse or version error from the new file
// does NOT trigger legacy fallback — only os.ErrNotExist does.
func Read(worktreeRoot string) (State, error) {
	data, err := os.ReadFile(path(worktreeRoot))
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(legacyPath(worktreeRoot))
	}
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
