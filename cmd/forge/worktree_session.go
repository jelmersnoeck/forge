package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// forgeSession is the metadata file written to the worktree root, linking the
// worktree back to its session for auto-resume.
const forgeSessionFile = ".forge-session"

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
