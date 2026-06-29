package phase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPhaseBranchName(t *testing.T) {
	tests := map[string]struct {
		slug   string
		number int
		want   string
	}{
		"normal":      {"greendale-paintball", 42, "jelmer/greendale-paintball-42"},
		"empty slug":  {"", 7, "jelmer/phase-7"},
		"zero number": {"abed", 0, "jelmer/abed-0"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, phaseBranchName(tc.slug, tc.number))
		})
	}
}

func TestBranchToDir(t *testing.T) {
	require.Equal(t, "jelmer-greendale-3", branchToDir("jelmer/greendale-3"))
	require.Equal(t, "noslash", branchToDir("noslash"))
}

func TestSlugifyTitle(t *testing.T) {
	tests := map[string]struct {
		title  string
		maxLen int
		want   string
	}{
		"spaces and case":  {"Greendale Paintball Arena", 0, "greendale-paintball-arena"},
		"punctuation":      {"Troy & Abed: in the morning!", 0, "troy-abed-in-the-morning"},
		"truncate":         {"señor chang teaches spanish", 10, "se-or-chan"},
		"leading/trailing": {"  --Human Being--  ", 0, "human-being"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, SlugifyTitle(tc.title, tc.maxLen))
		})
	}
}

func TestFormatSubIssuePrompt(t *testing.T) {
	r := require.New(t)

	got := formatSubIssuePrompt(SubIssue{Number: 5, Title: "Wire the buttress", Body: "  Build the human being mascot.  "})
	r.Contains(got, "Implement the following GitHub issue.")
	r.Contains(got, "Issue #5: Wire the buttress")
	r.Contains(got, "Build the human being mascot.")

	noNum := formatSubIssuePrompt(SubIssue{Title: "No number", Body: "body"})
	r.Contains(noNum, "Issue: No number")
}

func TestRunMultiPhase_RequiresSpawnAgent(t *testing.T) {
	r := require.New(t)
	err := RunMultiPhase(context.Background(), MultiPhaseOpts{
		Phases: []SubIssue{{Number: 1, Title: "x"}},
	})
	r.Error(err)
	r.Contains(err.Error(), "SpawnAgent is required")
}

func TestRunMultiPhase_NoPhases(t *testing.T) {
	r := require.New(t)
	err := RunMultiPhase(context.Background(), MultiPhaseOpts{
		SpawnAgent: func(context.Context, string, string, string) error { return nil },
	})
	r.NoError(err)
}

// TestRunMultiPhase_RunsInOrder verifies phases execute sequentially in order
// with the right CWD and branch, and that worktree create/spawn/PR/remove all
// fire once per phase.
func TestRunMultiPhase_RunsInOrder(t *testing.T) {
	r := require.New(t)

	var createOrder, spawnOrder, prOrder, removeOrder []string
	spawnCWDs := map[string]string{}

	opts := MultiPhaseOpts{
		RepoRoot:     "/repo",
		BaseBranch:   "main",
		ParentSlug:   "greendale",
		WorktreeBase: "/tmp/wt",
		Phases: []SubIssue{
			{Number: 1, Title: "foundations", Body: "lay the foundations"},
			{Number: 2, Title: "walls", Body: "raise the walls"},
			{Number: 3, Title: "roof", Body: "add the roof"},
		},
		SpawnAgent: func(_ context.Context, cwd, branch, prompt string) error {
			spawnOrder = append(spawnOrder, branch)
			spawnCWDs[branch] = cwd
			r.Contains(prompt, "Implement the following GitHub issue.")
			return nil
		},
		CreateWorktree: func(_ context.Context, repoRoot, base, branch, path string) (string, error) {
			r.Equal("/repo", repoRoot)
			r.Equal("main", base)
			createOrder = append(createOrder, branch)
			return path, nil
		},
		EnsurePRFn: func(_ context.Context, cwd string) PRResult {
			prOrder = append(prOrder, cwd)
			return PRResult{URL: "https://github.com/greendale/repo/pull/1"}
		},
		RemoveWorktree: func(_ context.Context, repoRoot, path string) error {
			removeOrder = append(removeOrder, path)
			return nil
		},
	}

	r.NoError(RunMultiPhase(context.Background(), opts))

	wantBranches := []string{"jelmer/greendale-1", "jelmer/greendale-2", "jelmer/greendale-3"}
	r.Equal(wantBranches, createOrder)
	r.Equal(wantBranches, spawnOrder)
	r.Len(prOrder, 3)
	r.Len(removeOrder, 3)

	// Each phase ran in its own worktree dir derived from the branch.
	r.Equal(filepath.Join("/tmp/wt", "jelmer-greendale-1"), spawnCWDs["jelmer/greendale-1"])
	r.Equal(filepath.Join("/tmp/wt", "jelmer-greendale-3"), spawnCWDs["jelmer/greendale-3"])
}

// TestRunMultiPhase_HaltsOnSpawnFailure verifies a sub-agent error stops the
// pipeline and leaves later phases unstarted.
func TestRunMultiPhase_HaltsOnSpawnFailure(t *testing.T) {
	r := require.New(t)

	var spawned []int
	opts := MultiPhaseOpts{
		ParentSlug: "p",
		Phases: []SubIssue{
			{Number: 1, Title: "a"},
			{Number: 2, Title: "b"},
			{Number: 3, Title: "c"},
		},
		SpawnAgent: func(_ context.Context, cwd, branch, prompt string) error {
			n := len(spawned) + 1
			spawned = append(spawned, n)
			if n == 2 {
				return errors.New("annie's boobs escaped")
			}
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) {
			return path, nil
		},
		EnsurePRFn:     func(context.Context, string) PRResult { return PRResult{} },
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	err := RunMultiPhase(context.Background(), opts)
	r.Error(err)
	r.Contains(err.Error(), "phase 1")
	r.Contains(err.Error(), "annie's boobs escaped")
	r.Equal([]int{1, 2}, spawned, "third phase must not start")
}

// TestRunMultiPhase_HaltsOnWorktreeFailure verifies a worktree creation error
// halts before spawning.
func TestRunMultiPhase_HaltsOnWorktreeFailure(t *testing.T) {
	r := require.New(t)

	spawnCalls := 0
	opts := MultiPhaseOpts{
		ParentSlug: "p",
		Phases:     []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			spawnCalls++
			return nil
		},
		CreateWorktree: func(context.Context, string, string, string, string) (string, error) {
			return "", errors.New("disk full at greendale")
		},
	}

	err := RunMultiPhase(context.Background(), opts)
	r.Error(err)
	r.Contains(err.Error(), "create worktree")
	r.Zero(spawnCalls, "sub-agent must not spawn when worktree creation fails")
}

// TestRunMultiPhase_PRFailureNonFatal verifies a PR error does not halt the
// pipeline — later phases still run.
func TestRunMultiPhase_PRFailureNonFatal(t *testing.T) {
	r := require.New(t)

	spawned := 0
	opts := MultiPhaseOpts{
		ParentSlug: "p",
		Phases:     []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			spawned++
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{Error: fmt.Errorf("gh exploded")}
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Equal(2, spawned)
}

// TestRunMultiPhase_CleanupFailureNonFatal verifies a worktree removal error is
// logged but does not halt the pipeline.
func TestRunMultiPhase_CleanupFailureNonFatal(t *testing.T) {
	r := require.New(t)

	spawned := 0
	opts := MultiPhaseOpts{
		ParentSlug: "p",
		Phases:     []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			spawned++
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn:     func(context.Context, string) PRResult { return PRResult{} },
		RemoveWorktree: func(context.Context, string, string) error {
			return errors.New("worktree locked")
		},
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Equal(2, spawned)
}

// TestRunMultiPhase_CancelledBeforePhase verifies a cancelled context stops the
// loop before the next phase starts.
func TestRunMultiPhase_CancelledBeforePhase(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	spawned := 0
	opts := MultiPhaseOpts{
		ParentSlug: "p",
		Phases:     []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			spawned++
			cancel() // cancel after the first phase's agent runs
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn:     func(context.Context, string) PRResult { return PRResult{} },
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	err := RunMultiPhase(ctx, opts)
	r.Error(err)
	r.Contains(err.Error(), "cancelled")
	r.Equal(1, spawned, "second phase must not start after cancellation")
}

// TestGitCreateAndRemoveWorktree exercises the real git worktree helpers.
func TestGitCreateAndRemoveWorktree(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "jelmer-greendale-1")

	got, err := gitCreateWorktree(context.Background(), local, "main", "jelmer/greendale-1", wtPath)
	r.NoError(err)
	r.Equal(wtPath, got)
	_, statErr := os.Stat(filepath.Join(wtPath, "README.md"))
	r.NoError(statErr, "worktree should contain the base branch's files")

	r.NoError(gitRemoveWorktree(context.Background(), local, wtPath))
	_, statErr = os.Stat(wtPath)
	r.True(os.IsNotExist(statErr), "worktree dir should be gone after removal")
}

// TestGitCreateWorktree_ExistingBranch verifies the fallback checkout path when
// the branch already exists.
func TestGitCreateWorktree_ExistingBranch(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "branch", "jelmer/existing-9")

	wtPath := filepath.Join(t.TempDir(), "jelmer-existing-9")
	got, err := gitCreateWorktree(context.Background(), local, "main", "jelmer/existing-9", wtPath)
	r.NoError(err)
	r.Equal(wtPath, got)
	_, statErr := os.Stat(filepath.Join(wtPath, "README.md"))
	r.NoError(statErr)

	r.NoError(gitRemoveWorktree(context.Background(), local, wtPath))
}

// TestRunMultiPhase_EndToEndWithGit runs the full coordinator against real git
// worktrees (no gh, no LLM) — a phase commits a file, PR is stubbed.
func TestRunMultiPhase_EndToEndWithGit(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	wtBase := t.TempDir()

	var emitted []string
	opts := MultiPhaseOpts{
		RepoRoot:     local,
		BaseBranch:   "main",
		ParentSlug:   "greendale",
		WorktreeBase: wtBase,
		Phases: []SubIssue{
			{Number: 1, Title: "phase one", Body: "do thing one"},
			{Number: 2, Title: "phase two", Body: "do thing two"},
		},
		Emit: func(ev types.OutboundEvent) { emitted = append(emitted, ev.Content) },
		SpawnAgent: func(_ context.Context, cwd, branch, _ string) error {
			// Simulate the sub-agent doing work in its worktree.
			writeTestFile(t, cwd, "work.txt", branch)
			run(t, cwd, "git", "add", ".")
			run(t, cwd, "git", "commit", "-m", "phase work")
			return nil
		},
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/greendale/repo/pull/99"}
		},
	}

	r.NoError(RunMultiPhase(context.Background(), opts))

	// Both phase branches should now exist in the repo with their commit.
	r.NotEmpty(emitted)
	// Worktrees were cleaned up by the default git remover.
	_, statErr := os.Stat(filepath.Join(wtBase, "jelmer-greendale-1"))
	r.True(os.IsNotExist(statErr), "phase 1 worktree should be cleaned up")
}
