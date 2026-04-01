package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorktreeManager_Integration(t *testing.T) {
	// Skip if git is not on PATH.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	r := require.New(t)

	// Create a test git repo
	repoDir := t.TempDir()
	worktreeDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.email", "test@greendale.edu")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())
	cmd = exec.Command("git", "config", "user.name", "Troy Barnes")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	// Create an initial commit
	testFile := filepath.Join(repoDir, "README.md")
	r.NoError(os.WriteFile(testFile, []byte("# Greendale Community College\n"), 0o644))
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	// Create worktree manager
	wm := NewWorktreeManager(repoDir, worktreeDir)
	r.True(wm.enabled, "worktrees should be enabled in a git repo")

	sessionID := "troy-barnes-spanish-101"

	// Ensure worktree
	path, err := wm.EnsureWorktree(sessionID)
	r.NoError(err)
	r.Equal(filepath.Join(worktreeDir, sessionID), path)

	// Verify the worktree directory exists
	r.DirExists(path)

	// Verify the file from the main repo is in the worktree
	wtFile := filepath.Join(path, "README.md")
	r.FileExists(wtFile)

	// Verify git knows about this worktree
	cmd = exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	r.NoError(err)
	r.Contains(string(out), path)

	// Calling EnsureWorktree again should be idempotent
	path2, err := wm.EnsureWorktree(sessionID)
	r.NoError(err)
	r.Equal(path, path2)

	// Remove worktree
	err = wm.RemoveWorktree(sessionID)
	r.NoError(err)

	// Verify the directory is gone
	r.NoDirExists(path)

	// Verify git no longer tracks it
	cmd = exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err = cmd.Output()
	r.NoError(err)
	r.NotContains(string(out), path)

	// Verify the branch is deleted
	cmd = exec.Command("git", "branch", "--list", "jelmer/"+sessionID)
	cmd.Dir = repoDir
	out, err = cmd.Output()
	r.NoError(err)
	r.Empty(string(out))
}

func TestWorktreeManager_NotInGitRepo(t *testing.T) {
	r := require.New(t)

	// Create a non-git directory
	workspace := t.TempDir()
	worktreeDir := t.TempDir()

	wm := NewWorktreeManager(workspace, worktreeDir)
	r.False(wm.enabled, "worktrees should be disabled when not in a git repo")

	sessionID := "abed-nadir-film-class"

	// EnsureWorktree should just return the base workspace
	path, err := wm.EnsureWorktree(sessionID)
	r.NoError(err)
	r.Equal(workspace, path)

	// RemoveWorktree should be a no-op
	err = wm.RemoveWorktree(sessionID)
	r.NoError(err)
}

func TestWorktreeManager_CleanupStale(t *testing.T) {
	// Skip if git is not on PATH.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	r := require.New(t)

	// Create a test git repo
	repoDir := t.TempDir()
	worktreeDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	// Create an initial commit
	testFile := filepath.Join(repoDir, "README.md")
	r.NoError(os.WriteFile(testFile, []byte("# Study Group\n"), 0o644))
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	// Create a fake stale worktree directory (not tracked by git)
	staleDir := filepath.Join(worktreeDir, "stale-session")
	r.NoError(os.MkdirAll(staleDir, 0o755))
	r.NoError(os.WriteFile(filepath.Join(staleDir, "test.txt"), []byte("stale"), 0o644))

	// Create worktree manager - should clean up stale on init
	wm := NewWorktreeManager(repoDir, worktreeDir)
	r.True(wm.enabled)

	// The stale directory should be gone
	r.NoDirExists(staleDir, "stale worktree should be cleaned up")
}
