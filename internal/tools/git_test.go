package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// initGitRepo creates a git repo with an initial commit on the given branch.
func initGitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	gitExec(t, dir, "init", "-b", branch)
	gitExec(t, dir, "config", "user.email", "chang@greendale.edu")
	gitExec(t, dir, "config", "user.name", "Señor Chang")
	writeFile(t, dir, "README.md", "# Greendale Community College\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")
}

// initGitRepoWithRemote creates a bare "remote" repo and a clone of it,
// both with an initial commit on the given branch. Returns (remote, local) paths.
func initGitRepoWithRemote(t *testing.T, branch string) (string, string) {
	t.Helper()

	// Create a temporary repo to build the initial commit
	staging := t.TempDir()
	initGitRepo(t, staging, branch)

	// Create bare remote from it
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitExec(t, staging, "clone", "--bare", staging, remote)

	// Clone it to get a proper local with origin
	local := t.TempDir()
	gitExec(t, local, "clone", remote, ".")
	gitExec(t, local, "config", "user.email", "chang@greendale.edu")
	gitExec(t, local, "config", "user.name", "Señor Chang")

	return remote, local
}

func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
