package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTmuxBackend_Integration(t *testing.T) {
	// Skip if tmux is not on PATH.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on PATH, skipping integration test")
	}

	// Skip if the forge binary isn't built.
	forgeBin := "./forge"
	if _, err := os.Stat(forgeBin); err != nil {
		// Try to find it on PATH
		if resolved, err := exec.LookPath("forge"); err != nil {
			t.Skip("forge binary not found, skipping integration test")
		} else {
			forgeBin = resolved
		}
	}

	r := require.New(t)
	ctx := context.Background()

	workspace := t.TempDir()
	b := NewTmux(forgeBin, "test", workspace)
	defer b.Close()

	// Troy Barnes' enrollment session at Greendale Community College.
	sessionID := "troy-barnes-enrollment-2009"

	opts := AgentOptions{
		CWD:         t.TempDir(),
		SessionsDir: t.TempDir(),
	}

	// EnsureAgent should start the agent and return a non-empty address.
	address, err := b.EnsureAgent(ctx, sessionID, opts)
	r.NoError(err)
	r.NotEmpty(address)

	// AgentAddress should return the same address.
	r.Equal(address, b.AgentAddress(sessionID))

	// Hit the health endpoint directly.
	resp, err := http.Get(fmt.Sprintf("http://%s/health", address))
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)

	// The shared tmux session should exist.
	err = exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run()
	r.NoError(err, "shared tmux session should exist")

	// StopAgent should kill the window but leave the session.
	err = b.StopAgent(ctx, sessionID)
	r.NoError(err)

	// AgentAddress should return "" after stopping.
	r.Empty(b.AgentAddress(sessionID))

	// The window should be gone.
	wName := windowName(sessionID)
	out, err := exec.Command("tmux", "list-windows", "-t", b.tmuxSessionName, "-F", "#{window_name}").CombinedOutput()
	r.NoError(err)
	r.NotContains(string(out), wName)

	// Close should kill the entire session.
	err = b.Close()
	r.NoError(err)
	err = exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run()
	r.Error(err, "tmux session should not exist after Close")
}

func TestTmuxBackend_WithGitWorktrees(t *testing.T) {
	// Skip if tmux or git are not on PATH.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on PATH, skipping integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	// Skip if the forge binary isn't built.
	forgeBin := "./forge"
	if _, err := os.Stat(forgeBin); err != nil {
		// Try to find it on PATH
		if resolved, err := exec.LookPath("forge"); err != nil {
			t.Skip("forge binary not found, skipping integration test")
		} else {
			forgeBin = resolved
		}
	}

	r := require.New(t)
	ctx := context.Background()

	// Create a git repo workspace
	workspace := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = workspace
	r.NoError(cmd.Run())

	// Create an initial commit
	testFile := filepath.Join(workspace, "README.md")
	r.NoError(os.WriteFile(testFile, []byte("# Greendale\n"), 0o644))
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = workspace
	r.NoError(cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = workspace
	r.NoError(cmd.Run())

	b := NewTmux(forgeBin, "test-git", workspace)
	defer b.Close()

	r.True(b.worktreeMgr.enabled, "worktree manager should be enabled")

	sessionID := "britta-perry-psych-major"

	opts := AgentOptions{
		CWD:         workspace, // This will be overridden by the worktree
		SessionsDir: t.TempDir(),
	}

	// EnsureAgent should create a worktree and start the agent.
	address, err := b.EnsureAgent(ctx, sessionID, opts)
	r.NoError(err)
	r.NotEmpty(address)

	// Verify the worktree exists
	worktreePath := filepath.Join(filepath.Dir(workspace), "worktrees", sessionID)
	r.DirExists(worktreePath)

	// Verify the README exists in the worktree
	wtReadme := filepath.Join(worktreePath, "README.md")
	r.FileExists(wtReadme)

	// Hit the health endpoint.
	resp, err := http.Get(fmt.Sprintf("http://%s/health", address))
	r.NoError(err)
	resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)

	// StopAgent should clean up the worktree.
	err = b.StopAgent(ctx, sessionID)
	r.NoError(err)

	// The worktree directory should be gone
	r.NoDirExists(worktreePath)

	// The branch should be deleted
	cmd = exec.Command("git", "branch", "--list", "jelmer/"+sessionID)
	cmd.Dir = workspace
	out, err := cmd.Output()
	r.NoError(err)
	r.Empty(string(out))

	// Close should clean up the tmux session.
	err = b.Close()
	r.NoError(err)
	err = exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run()
	r.Error(err, "tmux session should not exist after Close")
}
