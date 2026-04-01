package backend

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeManager creates and manages git worktrees for session isolation.
type WorktreeManager struct {
	repoRoot    string // absolute path to the git repository root
	worktreeDir string // base directory for all worktrees (e.g., /tmp/forge/worktrees)
	enabled     bool   // false if not in a git repo
}

// NewWorktreeManager creates a manager that isolates sessions via git worktrees.
// If baseWorkspace is not inside a git repo, worktrees are disabled and the
// manager returns the base workspace as-is.
func NewWorktreeManager(baseWorkspace, worktreeDir string) *WorktreeManager {
	// Try to find the git repository root from baseWorkspace.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = baseWorkspace
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[worktree] not in a git repo (workspace: %s), worktrees disabled", baseWorkspace)
		return &WorktreeManager{
			repoRoot:    baseWorkspace,
			worktreeDir: worktreeDir,
			enabled:     false,
		}
	}

	repoRoot := strings.TrimSpace(string(out))
	log.Printf("[worktree] git repo detected: %s", repoRoot)

	// Ensure worktree base dir exists
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		log.Printf("[worktree] failed to create worktree dir %s: %v, worktrees disabled", worktreeDir, err)
		return &WorktreeManager{
			repoRoot:    baseWorkspace,
			worktreeDir: worktreeDir,
			enabled:     false,
		}
	}

	wm := &WorktreeManager{
		repoRoot:    repoRoot,
		worktreeDir: worktreeDir,
		enabled:     true,
	}

	// Clean up any stale worktrees on startup
	wm.cleanupStale()

	return wm
}

// EnsureWorktree creates a git worktree for the session if enabled.
// Returns the path to use as CWD for the agent.
func (wm *WorktreeManager) EnsureWorktree(sessionID string) (string, error) {
	if !wm.enabled {
		return wm.repoRoot, nil
	}

	worktreePath := filepath.Join(wm.worktreeDir, sessionID)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		log.Printf("[worktree] reusing existing worktree for session %s: %s", sessionID, worktreePath)
		return worktreePath, nil
	}

	// Get current branch to create worktree from
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = wm.repoRoot
	branchOut, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	// Create the worktree
	// Use -b to create a new branch jelmer/<sessionID> based on current HEAD
	// This isolates each session's work
	newBranch := fmt.Sprintf("jelmer/%s", sessionID)
	cmd = exec.Command("git", "worktree", "add", "-b", newBranch, worktreePath, branch)
	cmd.Dir = wm.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, out)
	}

	log.Printf("[worktree] created worktree for session %s: %s (branch: %s)", sessionID, worktreePath, newBranch)
	return worktreePath, nil
}

// RemoveWorktree removes a session's worktree.
func (wm *WorktreeManager) RemoveWorktree(sessionID string) error {
	if !wm.enabled {
		return nil
	}

	worktreePath := filepath.Join(wm.worktreeDir, sessionID)

	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil // already gone
	}

	// Remove the worktree
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = wm.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[worktree] git worktree remove failed for %s: %v: %s", sessionID, err, out)
		// Try manual cleanup as fallback
		if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
			return fmt.Errorf("failed to remove worktree directory: %w", rmErr)
		}
	}

	// Prune the worktree reference
	cmd = exec.Command("git", "worktree", "prune")
	cmd.Dir = wm.repoRoot
	cmd.Run() // ignore errors, this is just cleanup

	// Delete the branch
	branchName := fmt.Sprintf("jelmer/%s", sessionID)
	cmd = exec.Command("git", "branch", "-D", branchName)
	cmd.Dir = wm.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[worktree] failed to delete branch %s: %v: %s", branchName, err, out)
		// Non-fatal, branch might not exist
	}

	log.Printf("[worktree] removed worktree for session %s", sessionID)
	return nil
}

// cleanupStale removes any worktrees from the worktree directory that git
// doesn't know about (from previous server runs).
func (wm *WorktreeManager) cleanupStale() {
	if !wm.enabled {
		return
	}

	// List all directories in worktreeDir
	entries, err := os.ReadDir(wm.worktreeDir)
	if err != nil {
		log.Printf("[worktree] failed to read worktree dir: %v", err)
		return
	}

	// Get list of active worktrees from git
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = wm.repoRoot
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[worktree] failed to list worktrees: %v", err)
		return
	}

	// Parse output to get active worktree paths
	activeWorktrees := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			activeWorktrees[path] = true
		}
	}

	// Remove any directories that aren't active worktrees
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		worktreePath := filepath.Join(wm.worktreeDir, entry.Name())
		if !activeWorktrees[worktreePath] {
			log.Printf("[worktree] cleaning up stale worktree: %s", worktreePath)
			os.RemoveAll(worktreePath)
		}
	}

	// Prune git's worktree references
	cmd = exec.Command("git", "worktree", "prune")
	cmd.Dir = wm.repoRoot
	cmd.Run()
}
