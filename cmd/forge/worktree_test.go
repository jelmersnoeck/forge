package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsInWorktree(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		setup func(t *testing.T) string
		want  bool
	}{
		"not in git repo": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				return dir
			},
			want: false,
		},
		"in main repo": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()

				// Init git repo
				cmd := exec.Command("git", "init")
				cmd.Dir = dir
				r.NoError(cmd.Run())

				// Create initial commit
				testFile := filepath.Join(dir, "README.md")
				r.NoError(os.WriteFile(testFile, []byte("# Test\n"), 0o644))

				cmd = exec.Command("git", "config", "user.email", "test@greendale.edu")
				cmd.Dir = dir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "config", "user.name", "Troy Barnes")
				cmd.Dir = dir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "add", "README.md")
				cmd.Dir = dir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "commit", "-m", "Initial commit")
				cmd.Dir = dir
				r.NoError(cmd.Run())

				return dir
			},
			want: false,
		},
		"in worktree": {
			setup: func(t *testing.T) string {
				mainDir := t.TempDir()

				// Init git repo
				cmd := exec.Command("git", "init")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				// Create initial commit
				testFile := filepath.Join(mainDir, "README.md")
				r.NoError(os.WriteFile(testFile, []byte("# Test\n"), 0o644))

				cmd = exec.Command("git", "config", "user.email", "test@greendale.edu")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "config", "user.name", "Troy Barnes")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "add", "README.md")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				cmd = exec.Command("git", "commit", "-m", "Initial commit")
				cmd.Dir = mainDir
				r.NoError(cmd.Run())

				// Create worktree
				wtDir := filepath.Join(t.TempDir(), "worktree")
				cmd = exec.Command("git", "worktree", "add", "-b", "test-branch", wtDir, "HEAD")
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
