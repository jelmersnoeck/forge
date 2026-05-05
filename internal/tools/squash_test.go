package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initSquashRepo creates a git repo with an initial commit on main
// and a feature branch checked out. Returns the repo path.
func initSquashRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	gitExec(t, dir, "checkout", "-b", "jelmer/paintball-episode")
	return dir
}

func TestSquashBranchCommits_MultipleCommits(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	// Add 3 commits on the feature branch
	writeFile(t, dir, "paintball.go", "package paintball\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "feat: add paintball arena")

	writeFile(t, dir, "weapons.go", "package paintball\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "fix: review feedback on weapons")

	writeFile(t, dir, "scoring.go", "package paintball\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "fix: review feedback on scoring")

	result := SquashBranchCommits(context.Background(), dir)

	r.True(result.Squashed, "expected squash to succeed")
	r.Equal(3, result.CommitCount)
	r.NoError(result.Error)
	r.NotEmpty(result.CommitSHA)

	// Verify single commit above main
	out := gitOutput(t, dir, "rev-list", "--count", "main..HEAD")
	r.Equal("1", out)

	// Verify commit message format
	msg := gitOutput(t, dir, "log", "-1", "--format=%B")
	r.Contains(msg, "feat: add paintball arena")
	r.Contains(msg, "Also includes:")
	r.Contains(msg, "- fix: review feedback on weapons")
	r.Contains(msg, "- fix: review feedback on scoring")
}

func TestSquashBranchCommits_SingleCommit(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	gitExec(t, dir, "commit", "--allow-empty", "-m", "feat: single commit")

	result := SquashBranchCommits(context.Background(), dir)

	r.False(result.Squashed, "single commit should be a no-op")
	r.Equal(0, result.CommitCount)
	r.Empty(result.CommitSHA)
	r.NoError(result.Error)
}

func TestSquashBranchCommits_NoCommits(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	result := SquashBranchCommits(context.Background(), dir)

	r.False(result.Squashed)
	r.Equal(0, result.CommitCount)
	r.Empty(result.CommitSHA)
	r.NoError(result.Error)
}

func TestSquashBranchCommits_UncommittedChanges(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	writeFile(t, dir, "a.go", "package a\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "first")

	writeFile(t, dir, "b.go", "package b\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "second")

	// Leave uncommitted changes (untracked file)
	writeFile(t, dir, "dirty.txt", "uncommitted\n")

	result := SquashBranchCommits(context.Background(), dir)

	r.False(result.Squashed, "should skip when dirty")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "uncommitted")
}

func TestSquashBranchCommits_StagedChanges(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	writeFile(t, dir, "a.go", "package a\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "first")

	writeFile(t, dir, "b.go", "package b\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "second")

	// Leave staged changes
	writeFile(t, dir, "staged.txt", "staged\n")
	gitExec(t, dir, "add", ".")

	result := SquashBranchCommits(context.Background(), dir)

	r.False(result.Squashed, "should skip when staged changes exist")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "uncommitted")
}

func TestSquashBranchCommits_NoBaseBranch(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	gitExec(t, dir, "init", "-b", "totally-custom-branch")
	gitExec(t, dir, "config", "user.email", "troy@greendale.edu")
	gitExec(t, dir, "config", "user.name", "Troy Barnes")

	writeFile(t, dir, "README.md", "# Greendale\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial")

	result := SquashBranchCommits(context.Background(), dir)

	r.False(result.Squashed)
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "base branch")
}

func TestSquashBranchCommits_TwoCommitsMessage(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	writeFile(t, dir, "a.go", "package a\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "feat: implement Human Being mascot")

	writeFile(t, dir, "b.go", "package b\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "fix: Senor Chang requested changes")

	result := SquashBranchCommits(context.Background(), dir)

	r.True(result.Squashed)
	r.Equal(2, result.CommitCount)

	msg := gitOutput(t, dir, "log", "-1", "--format=%B")
	r.True(strings.HasPrefix(msg, "feat: implement Human Being mascot"),
		"first line should be original commit: %s", msg)
	r.Contains(msg, "Also includes:")
	r.Contains(msg, "- fix: Senor Chang requested changes")
}

func TestSquashBranchCommits_ContextCancelled(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := SquashBranchCommits(ctx, dir)

	r.False(result.Squashed)
	r.Error(result.Error)
}

func TestSquashBranchCommits_PreservesTreeContent(t *testing.T) {
	r := require.New(t)
	dir := initSquashRepo(t)

	// Write files across multiple commits, some modifying the same file
	writeFile(t, dir, "study_group.go", "v1\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "add study group")

	writeFile(t, dir, "study_group.go", "v2 - with Abed\n")
	writeFile(t, dir, "dean.go", "dean pelton\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "update study group, add dean")

	writeFile(t, dir, "study_group.go", "v3 - final\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "finalize study group")

	// Capture tree hash before squash
	treeBefore := gitOutput(t, dir, "rev-parse", "HEAD^{tree}")

	result := SquashBranchCommits(context.Background(), dir)
	r.True(result.Squashed)
	r.NoError(result.Error)

	// Tree hash must be identical
	treeAfter := gitOutput(t, dir, "rev-parse", "HEAD^{tree}")
	r.Equal(treeBefore, treeAfter,
		"tree hash must be identical before and after squash -- no data loss")
}

func TestBuildSquashMessage_SingleExtra(t *testing.T) {
	r := require.New(t)
	msgs := []string{"feat: add paintball", "fix: review feedback"}
	got := buildSquashMessage(msgs)
	r.Equal("feat: add paintball\n\nAlso includes:\n- fix: review feedback", got)
}

func TestBuildSquashMessage_MultipleExtras(t *testing.T) {
	r := require.New(t)
	msgs := []string{"feat: main change", "fix: typo", "fix: lint", "docs: update readme"}
	got := buildSquashMessage(msgs)
	want := "feat: main change\n\nAlso includes:\n- fix: typo\n- fix: lint\n- docs: update readme"
	r.Equal(want, got)
}

func TestBuildSquashMessage_OnlyOne(t *testing.T) {
	r := require.New(t)
	msgs := []string{"feat: single commit"}
	got := buildSquashMessage(msgs)
	r.Equal("feat: single commit", got)
}

// gitOutput runs a git command and returns trimmed stdout (test helper).
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git %v failed", args)
	return strings.TrimSpace(string(out))
}
