package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// findResumableSessions scans worktreeBase for .forge-session files belonging
// to the given repoRoot.
func findResumableSessions(repoRoot, worktreeBase string) []SessionInfo {
	entries, err := os.ReadDir(worktreeBase)
	if err != nil {
		return nil
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wtPath := filepath.Join(worktreeBase, entry.Name())
		info, err := readSessionFile(wtPath)
		if err != nil {
			continue
		}
		if info.RepoRoot != repoRoot {
			continue
		}
		sessions = append(sessions, info)
	}
	return sessions
}

// cleanupMergedWorktrees removes worktrees whose branch PR has been merged.
// Requires `gh` CLI. Silently skips if gh is not available.
func cleanupMergedWorktrees(repoRoot, worktreeBase string) {
	if _, err := exec.LookPath("gh"); err != nil {
		return
	}

	sessions := findResumableSessions(repoRoot, worktreeBase)
	for _, s := range sessions {
		if isBranchMerged(repoRoot, s.Branch) {
			fmt.Fprintln(os.Stderr, dimStyle.Render("  merged PR detected, cleaning up: "+s.Branch))
			removeWorktreeAndBranch(repoRoot, s.WorktreePath, s.Branch)
		}
	}
}

// isBranchMerged checks if the branch's PR was merged using `gh pr view`.
func isBranchMerged(repoRoot, branch string) bool {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "state", "--jq", ".state")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "MERGED"
}

// removeWorktreeAndBranch removes a worktree directory and its branch.
func removeWorktreeAndBranch(repoRoot, worktreePath, branch string) {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoRoot
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("  worktree remove failed: "+err.Error()))
		// Try manual removal as fallback
		_ = os.RemoveAll(worktreePath)
	}

	cmd = exec.Command("git", "worktree", "prune")
	cmd.Dir = repoRoot
	_ = cmd.Run()

	cmd = exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoRoot
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("  branch delete failed: "+err.Error()))
	}
}
