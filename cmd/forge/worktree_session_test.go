package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteReadSessionFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	info := SessionInfo{
		SessionID: "20260409-quick-flame",
		Branch:    "jelmer/20260409-quick-flame",
		RepoRoot:  "/Users/jeff/Projects/forge",
		CreatedAt: time.Date(2026, 4, 9, 14, 30, 0, 0, time.UTC),
	}

	r.NoError(writeSessionFile(dir, info))

	// File should exist
	r.FileExists(filepath.Join(dir, forgeSessionFile))

	// Round-trip
	got, err := readSessionFile(dir)
	r.NoError(err)
	r.Equal(info.SessionID, got.SessionID)
	r.Equal(info.Branch, got.Branch)
	r.Equal(info.RepoRoot, got.RepoRoot)
	r.Equal(info.CreatedAt.Unix(), got.CreatedAt.Unix())
	r.Equal(dir, got.WorktreePath)
}

func TestReadSessionFile_Missing(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	_, err := readSessionFile(dir)
	r.Error(err)
}

func TestFindResumableSessions(t *testing.T) {
	r := require.New(t)

	worktreeBase := t.TempDir()
	repoRoot := "/Users/troy/Projects/greendale"

	// Create two worktrees for our repo, one for a different repo
	for _, tc := range []struct {
		name     string
		repoRoot string
	}{
		{"session-a", repoRoot},
		{"session-b", repoRoot},
		{"session-c", "/Users/abed/Projects/dreamatorium"},
	} {
		dir := filepath.Join(worktreeBase, tc.name)
		r.NoError(os.MkdirAll(dir, 0o755))
		r.NoError(writeSessionFile(dir, SessionInfo{
			SessionID: tc.name,
			Branch:    "jelmer/" + tc.name,
			RepoRoot:  tc.repoRoot,
			CreatedAt: time.Now(),
		}))
	}

	// Also create a directory without a session file (should be ignored)
	r.NoError(os.MkdirAll(filepath.Join(worktreeBase, "orphan"), 0o755))

	sessions := findResumableSessions(repoRoot, worktreeBase)
	r.Len(sessions, 2)

	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.SessionID] = true
	}
	r.True(ids["session-a"])
	r.True(ids["session-b"])
}

func TestFindResumableSessions_EmptyDir(t *testing.T) {
	r := require.New(t)
	sessions := findResumableSessions("/some/repo", t.TempDir())
	r.Empty(sessions)
}

func TestFindResumableSessions_NonExistentDir(t *testing.T) {
	r := require.New(t)
	sessions := findResumableSessions("/some/repo", "/nonexistent/path")
	r.Empty(sessions)
}

func TestIsBranchMerged_NoGH(t *testing.T) {
	// If gh is not available, should return false (not merged)
	// This test works whether or not gh is installed because
	// it uses a branch that definitely doesn't exist
	r := require.New(t)
	r.False(isBranchMerged(t.TempDir(), "jelmer/nonexistent-branch-troy-and-abed"))
}

func TestRemoveWorktreeAndBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}

	r := require.New(t)
	repoDir := initTestRepo(t, r)

	// Create a worktree
	wtDir := filepath.Join(resolveSymlinks(t, t.TempDir()), "wt")
	branch := "jelmer/test-cleanup"
	cmd := exec.Command("git", "worktree", "add", "-b", branch, wtDir, "HEAD")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())
	r.DirExists(wtDir)

	// Remove it
	removeWorktreeAndBranch(repoDir, wtDir, branch)

	// Worktree dir should be gone
	r.NoDirExists(wtDir)

	// Branch should be gone
	cmd = exec.Command("git", "branch", "--list", branch)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	r.NoError(err)
	r.Empty(string(out))
}
