package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// gitRun runs a git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Troy Barnes",
		"GIT_AUTHOR_EMAIL=troy@greendale.edu",
		"GIT_COMMITTER_NAME=Troy Barnes",
		"GIT_COMMITTER_EMAIL=troy@greendale.edu",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// newFeatureRepo creates a git repo on a feature branch with one base commit.
// hookDir is created so the attribution prepare-commit-msg hook (if any) has a
// home; git init may skip it under custom templateDir.
func newFeatureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("greendale\n"), 0o644))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "base")
	gitRun(t, dir, "checkout", "-qb", "jelmer/paintball")
	return dir
}

func TestRepoHasPendingWork(t *testing.T) {
	r := require.New(t)
	dir := newFeatureRepo(t)
	w := &Worker{sessionID: "greendale-201", cwd: dir}
	ctx := context.Background()

	// Clean feature branch, no commits ahead, no remote -> no pending work.
	pending, _ := w.repoHasPendingWork(ctx)
	r.False(pending, "clean branch with no ahead-commits should have no pending work")

	// Dirty working tree -> pending.
	r.NoError(os.WriteFile(filepath.Join(dir, "spanish101.go"), []byte("package x\n"), 0o644))
	pending, reason := w.repoHasPendingWork(ctx)
	r.True(pending, "dirty tree should be pending work")
	r.Contains(reason, "uncommitted")
}

func TestRepoHasPendingWork_SkipsMainBranch(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	r.NoError(os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "base")
	// Dirty tree on main.
	r.NoError(os.WriteFile(filepath.Join(dir, "g.txt"), []byte("y\n"), 0o644))

	w := &Worker{sessionID: "greendale-202", cwd: dir}
	pending, _ := w.repoHasPendingWork(context.Background())
	r.False(pending, "main branch must never be enforced for PR")
}

func TestRepoHasPendingWork_NotGitRepo(t *testing.T) {
	r := require.New(t)
	w := &Worker{sessionID: "greendale-203", cwd: t.TempDir()}
	pending, _ := w.repoHasPendingWork(context.Background())
	r.False(pending, "non-git dir should report no pending work")
}

func TestCommitPendingChanges(t *testing.T) {
	r := require.New(t)
	dir := newFeatureRepo(t)
	w := &Worker{sessionID: "greendale-204", cwd: dir}
	ctx := context.Background()

	r.NoError(os.WriteFile(filepath.Join(dir, "chang.go"), []byte("package senor\n"), 0o644))
	r.True(w.workingTreeDirty(ctx))

	r.NoError(w.commitPendingChanges(ctx))
	r.False(w.workingTreeDirty(ctx), "tree should be clean after commit")
}

// TestReconcilePR_RunsWithoutToolUse is the acceptance test from issue #234:
// the worker reaches done with NO per-turn tool_use, but the repo is dirty.
// reconcilePR must still kick in — committing the dirty tree (ground truth),
// not silently skipping. gh is unavailable here, so no actual PR is created,
// but the dirty-tree reconciliation (the core regression) must happen.
func TestReconcilePR_RunsWithoutToolUse(t *testing.T) {
	r := require.New(t)
	dir := newFeatureRepo(t)
	w := &Worker{
		sessionID:   "greendale-205",
		cwd:         dir,
		ghAvailable: false, // skip the actual gh PR creation
	}
	ctx := context.Background()

	// Simulate changes produced in an earlier turn / by a phase: a dirty tree
	// with no tool_use event observed this turn.
	r.NoError(os.WriteFile(filepath.Join(dir, "abed.go"), []byte("package cool\n"), 0o644))

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) { events = append(events, e) }

	w.reconcilePR(ctx, nil, false /* not interrupted */, emit)

	// Ground truth wins: the dirty tree was committed, not silently dropped.
	r.False(w.workingTreeDirty(ctx), "reconcilePR must commit the dirty tree")
}

// TestReconcilePR_InterruptedWarnsButNoCommit asserts an interrupted turn that
// left changes warns loudly instead of silently skipping, and does NOT commit.
func TestReconcilePR_InterruptedWarnsButNoCommit(t *testing.T) {
	r := require.New(t)
	dir := newFeatureRepo(t)
	w := &Worker{sessionID: "greendale-206", cwd: dir, ghAvailable: false}
	ctx := context.Background()

	r.NoError(os.WriteFile(filepath.Join(dir, "pierce.go"), []byte("package old\n"), 0o644))

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) { events = append(events, e) }

	w.reconcilePR(ctx, nil, true /* interrupted */, emit)

	// Should warn, and should NOT auto-commit on interrupt.
	r.True(w.workingTreeDirty(ctx), "interrupt must not commit changes")
	var warned bool
	for _, e := range events {
		if e.Type == "warning" {
			warned = true
		}
	}
	r.True(warned, "interrupted session with changes must emit a warning, not skip silently")
}

// TestReconcilePR_CleanRepoNoop ensures a clean repo produces no events and no
// commits — PR enforcement only fires when there's actual work.
func TestReconcilePR_CleanRepoNoop(t *testing.T) {
	r := require.New(t)
	dir := newFeatureRepo(t)
	w := &Worker{sessionID: "greendale-207", cwd: dir, ghAvailable: false}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) { events = append(events, e) }

	w.reconcilePR(context.Background(), nil, false, emit)
	r.Empty(events, "clean repo should produce no events")
}
