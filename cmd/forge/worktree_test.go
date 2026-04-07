package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// resolveSymlinks resolves symlinks for path comparison on macOS where
// /var is a symlink to /private/var.
func resolveSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// initTestRepo creates a git repo with one commit and returns its resolved path.
func initTestRepo(t *testing.T, r *require.Assertions) string {
	t.Helper()
	dir := resolveSymlinks(t, t.TempDir())

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@greendale.edu"},
		{"config", "user.name", "Troy Barnes"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		r.NoError(cmd.Run())
	}

	r.NoError(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Greendale\n"), 0o644))

	cmd := exec.Command("git", "add", "README.md")
	cmd.Dir = dir
	r.NoError(cmd.Run())

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = dir
	r.NoError(cmd.Run())

	return dir
}

func TestIsInWorktree(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		setup func(t *testing.T) string
		want  bool
	}{
		"not in git repo": {
			setup: func(t *testing.T) string {
				return resolveSymlinks(t, t.TempDir())
			},
			want: false,
		},
		"in main repo": {
			setup: func(t *testing.T) string {
				return initTestRepo(t, r)
			},
			want: false,
		},
		"in worktree": {
			setup: func(t *testing.T) string {
				mainDir := initTestRepo(t, r)

				// Create worktree
				wtDir := filepath.Join(resolveSymlinks(t, t.TempDir()), "worktree")
				cmd := exec.Command("git", "worktree", "add", "-b", "test-branch", wtDir, "HEAD")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				return wtDir
			},
			want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := tc.setup(t)
			got := isInWorktree(dir)
			r.Equal(tc.want, got)
		})
	}
}

func TestFindRepoRoot(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		setup func(t *testing.T) string
		want  string
	}{
		"not a git repo": {
			setup: func(t *testing.T) string {
				return resolveSymlinks(t, t.TempDir())
			},
			want: "",
		},
		"at repo root": {
			setup: func(t *testing.T) string {
				return initTestRepo(t, r)
			},
			want: "SELF",
		},
		"in subdirectory": {
			setup: func(t *testing.T) string {
				dir := initTestRepo(t, r)
				sub := filepath.Join(dir, "sub", "deep")
				r.NoError(os.MkdirAll(sub, 0o755))
				return sub
			},
			want: "PARENT",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := tc.setup(t)
			got := findRepoRoot(dir)
			switch tc.want {
			case "":
				r.Empty(got)
			case "SELF":
				r.Equal(dir, got)
			case "PARENT":
				r.NotEmpty(got)
				r.True(len(got) < len(dir), "repo root should be a parent of %s, got %s", dir, got)
			}
		})
	}
}

func TestFindWorktreeForBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}

	r := require.New(t)
	repoDir := initTestRepo(t, r)

	// Create a worktree on a known branch
	wtDir := filepath.Join(resolveSymlinks(t, t.TempDir()), "senor-chang")
	cmd := exec.Command("git", "worktree", "add", "-b", "jelmer/spanish-101", wtDir, "HEAD")
	cmd.Dir = repoDir
	r.NoError(cmd.Run())

	tests := map[string]struct {
		branch string
		want   string
	}{
		"finds existing worktree": {
			branch: "jelmer/spanish-101",
			want:   wtDir,
		},
		"branch not checked out anywhere": {
			branch: "jelmer/anthropology-101",
			want:   "",
		},
		"partial match does not count": {
			branch: "spanish-101",
			want:   "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := findWorktreeForBranch(repoDir, tc.branch)
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}
