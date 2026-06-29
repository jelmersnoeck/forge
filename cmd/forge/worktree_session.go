package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/sessionstate"
)

const (
	// forgeSession is the metadata file written to the worktree root, linking the
	// worktree back to its session for auto-resume.
	forgeSessionFile = ".forge-session"

	// defaultSessionsDir is the default directory for session JSONL files.
	// Must stay in sync with the agent's --sessions-dir default.
	defaultSessionsDir = "/tmp/forge/sessions"

	// sessionFileExt is the file extension for session JSONL files.
	sessionFileExt = ".jsonl"
)

// SessionInfo describes a resumable session backed by a worktree.
type SessionInfo struct {
	SessionID    string    `json:"sessionID"`
	Branch       string    `json:"branch"`
	RepoRoot     string    `json:"repoRoot"`
	CreatedAt    time.Time `json:"createdAt"`
	WorktreePath string    `json:"-"` // set at scan time, not persisted
}

// writeSessionFile writes the .forge-session metadata into the worktree root.
func writeSessionFile(worktreePath string, info SessionInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session info: %w", err)
	}
	return os.WriteFile(filepath.Join(worktreePath, forgeSessionFile), data, 0o644)
}

// readSessionFile reads the .forge-session metadata from a worktree.
func readSessionFile(worktreePath string) (SessionInfo, error) {
	data, err := os.ReadFile(filepath.Join(worktreePath, forgeSessionFile))
	if err != nil {
		return SessionInfo{}, err
	}
	var info SessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return SessionInfo{}, fmt.Errorf("parse session info: %w", err)
	}
	info.WorktreePath = worktreePath
	return info, nil
}

// sessionsDir returns the sessions directory, honoring SESSIONS_DIR if set.
func sessionsDir() string {
	if v := os.Getenv("SESSIONS_DIR"); v != "" {
		return v
	}
	return defaultSessionsDir
}

// sessionFilePath returns the JSONL file path for a session, or an error if the
// sessionID contains path traversal components.
func sessionFilePath(sessionID string) (string, error) {
	// Reject path traversal: only allow the base name portion.
	if filepath.Base(sessionID) != sessionID || sessionID == "." || sessionID == ".." {
		return "", fmt.Errorf("invalid session ID: %q", sessionID)
	}
	return filepath.Join(sessionsDir(), sessionID+sessionFileExt), nil
}

// warnIfStateStale compares the headCommit recorded in .forge-state against the
// worktree's current HEAD. If they diverge, the persisted conversation history
// no longer matches the working tree — print a warning. Missing/unreadable
// state or an absent HEAD is silent (a fresh or non-git worktree).
func warnIfStateStale(worktreePath string) {
	st, err := sessionstate.Read(worktreePath)
	if err != nil {
		return // no state, corrupt, or version mismatch — nothing to compare
	}
	if st.HeadCommit == "" {
		return
	}

	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return
	}
	current := strings.TrimSpace(string(out))
	if current == "" || current == st.HeadCommit {
		return
	}

	fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf(
		"  warning: worktree HEAD (%s) diverged from saved state (%s); resumed context may be stale",
		current, st.HeadCommit)))
}
