package phase

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractPRNumberFromURL(t *testing.T) {
	tests := map[string]struct {
		input string
		want  int
	}{
		"github pr":        {"https://github.com/greendale/repo/pull/42", 42},
		"trailing newline": {"https://github.com/greendale/repo/pull/7\n", 7},
		"no number":        {"https://github.com/greendale/repo", 0},
		"issue not pr":     {"https://github.com/greendale/repo/issues/42", 0},
		"empty":            {"", 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, extractPRNumberFromURL(tc.input))
		})
	}
}

func TestHasMergeConflict(t *testing.T) {
	tests := map[string]struct {
		st   prStatus
		want bool
	}{
		"clean":              {prStatus{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}, false},
		"conflicting":        {prStatus{Mergeable: "CONFLICTING"}, true},
		"dirty state":        {prStatus{MergeStateStatus: "DIRTY"}, true},
		"unknown not a flag": {prStatus{Mergeable: "UNKNOWN", MergeStateStatus: "BLOCKED"}, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, hasMergeConflict(tc.st))
		})
	}
}

func TestWaitForMerge_InvalidPRNumber(t *testing.T) {
	r := require.New(t)
	err := WaitForMerge(context.Background(), MergeWaitOpts{PRNumber: 0})
	r.Error(err)
	r.Contains(err.Error(), "invalid PR number")
}

// TestWaitForMerge_MergedImmediately verifies a PR that is already MERGED on the
// first poll returns nil without waiting a full interval.
func TestWaitForMerge_MergedImmediately(t *testing.T) {
	r := require.New(t)

	var polls int32
	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     99,
		PollInterval: time.Hour, // would block forever if it had to wait
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			atomic.AddInt32(&polls, 1)
			return prStatus{State: prStateMerged}, nil
		},
	})
	r.NoError(err)
	r.Equal(int32(1), atomic.LoadInt32(&polls))
}

// TestWaitForMerge_PollsUntilMerged verifies the poller keeps polling while OPEN
// and returns once the PR flips to MERGED.
func TestWaitForMerge_PollsUntilMerged(t *testing.T) {
	r := require.New(t)

	var polls int32
	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     1,
		PollInterval: time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			n := atomic.AddInt32(&polls, 1)
			if n < 3 {
				return prStatus{State: prStateOpen}, nil
			}
			return prStatus{State: prStateMerged}, nil
		},
	})
	r.NoError(err)
	r.GreaterOrEqual(atomic.LoadInt32(&polls), int32(3))
}

// TestWaitForMerge_ClosedHalts verifies a CLOSED PR halts with an error.
func TestWaitForMerge_ClosedHalts(t *testing.T) {
	r := require.New(t)

	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     5,
		PollInterval: time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			return prStatus{State: prStateClosed}, nil
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "closed without merging")
}

// TestWaitForMerge_Timeout verifies the poller halts after the timeout when the
// PR never merges.
func TestWaitForMerge_Timeout(t *testing.T) {
	r := require.New(t)

	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     8,
		PollInterval: time.Millisecond,
		Timeout:      20 * time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			return prStatus{State: prStateOpen}, nil
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "not merged within")
}

// TestWaitForMerge_RebasesOnConflict verifies a CONFLICTING open PR triggers a
// rebase attempt, and the poller continues until merge.
func TestWaitForMerge_RebasesOnConflict(t *testing.T) {
	r := require.New(t)

	var rebases int32
	var polls int32
	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     3,
		PollInterval: time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			n := atomic.AddInt32(&polls, 1)
			if n == 1 {
				return prStatus{State: prStateOpen, Mergeable: "CONFLICTING"}, nil
			}
			return prStatus{State: prStateMerged}, nil
		},
		Rebase: func(context.Context, string, string) error {
			atomic.AddInt32(&rebases, 1)
			return nil
		},
	})
	r.NoError(err)
	r.Equal(int32(1), atomic.LoadInt32(&rebases), "conflict should trigger exactly one rebase")
}

// TestWaitForMerge_RebaseFailureNonFatal verifies a failed rebase does not halt
// the wait — the poller keeps going until merge.
func TestWaitForMerge_RebaseFailureNonFatal(t *testing.T) {
	r := require.New(t)

	var polls int32
	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     4,
		PollInterval: time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			n := atomic.AddInt32(&polls, 1)
			if n < 2 {
				return prStatus{State: prStateOpen, MergeStateStatus: "DIRTY"}, nil
			}
			return prStatus{State: prStateMerged}, nil
		},
		Rebase: func(context.Context, string, string) error {
			return errors.New("rebase conflict needs human")
		},
	})
	r.NoError(err)
}

// TestWaitForMerge_StatusErrorRetries verifies a transient gh error is retried,
// not treated as a halt.
func TestWaitForMerge_StatusErrorRetries(t *testing.T) {
	r := require.New(t)

	var polls int32
	err := WaitForMerge(context.Background(), MergeWaitOpts{
		PRNumber:     6,
		PollInterval: time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			n := atomic.AddInt32(&polls, 1)
			if n == 1 {
				return prStatus{}, errors.New("gh: rate limited")
			}
			return prStatus{State: prStateMerged}, nil
		},
	})
	r.NoError(err)
	r.GreaterOrEqual(atomic.LoadInt32(&polls), int32(2))
}

// TestWaitForMerge_ContextCancelled verifies a cancelled parent context halts
// with a cancellation error (distinct from the timeout message).
func TestWaitForMerge_ContextCancelled(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	err := WaitForMerge(ctx, MergeWaitOpts{
		PRNumber:     2,
		PollInterval: 10 * time.Millisecond,
		PRStatus: func(context.Context, string, int) (prStatus, error) {
			cancel() // cancel right after the first poll
			return prStatus{State: prStateOpen}, nil
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "cancelled")
}

// TestRunMultiPhase_WaitsForMergeBetweenPhases verifies the coordinator waits
// for each phase's PR to merge (and pulls the base) before the next phase.
func TestRunMultiPhase_WaitsForMergeBetweenPhases(t *testing.T) {
	r := require.New(t)

	var order []string
	opts := MultiPhaseOpts{
		ParentSlug:          "greendale",
		BaseBranch:          "main",
		WaitForMergeEnabled: true,
		Phases: []SubIssue{
			{Number: 1, Title: "a"},
			{Number: 2, Title: "b"},
		},
		SpawnAgent: func(_ context.Context, _, branch, _ string) error {
			order = append(order, "spawn:"+branch)
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/greendale/repo/pull/77"}
		},
		WaitForMergeFn: func(_ context.Context, prNumber int, _, base string) error {
			r.Equal(77, prNumber)
			r.Equal("main", base)
			order = append(order, "merge")
			return nil
		},
		PullMainFn: func(context.Context, string, string) error {
			order = append(order, "pull")
			return nil
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Equal([]string{
		"spawn:jelmer/greendale-1", "merge", "pull",
		"spawn:jelmer/greendale-2", "merge", "pull",
	}, order)
}

// TestRunMultiPhase_HaltsOnClosedPR verifies a merge-wait error (closed PR)
// halts the pipeline and stops later phases.
func TestRunMultiPhase_HaltsOnClosedPR(t *testing.T) {
	r := require.New(t)

	spawned := 0
	opts := MultiPhaseOpts{
		ParentSlug:          "p",
		WaitForMergeEnabled: true,
		Phases:              []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			spawned++
			return nil
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/greendale/repo/pull/3"}
		},
		WaitForMergeFn: func(context.Context, int, string, string) error {
			return errors.New("PR #3 was closed without merging")
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	err := RunMultiPhase(context.Background(), opts)
	r.Error(err)
	r.Contains(err.Error(), "merge wait")
	r.Contains(err.Error(), "phase 0")
	r.Equal(1, spawned, "second phase must not start after a closed PR")
}

// TestRunMultiPhase_SkipsMergeWaitWhenNoPR verifies a phase whose PR creation
// was skipped does not attempt a merge wait and does not halt.
func TestRunMultiPhase_SkipsMergeWaitWhenNoPR(t *testing.T) {
	r := require.New(t)

	mergeCalls := 0
	opts := MultiPhaseOpts{
		ParentSlug:          "p",
		WaitForMergeEnabled: true,
		Phases:              []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent:          func(context.Context, string, string, string) error { return nil },
		CreateWorktree:      func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{Error: errors.New("no changes after rebase")}
		},
		WaitForMergeFn: func(context.Context, int, string, string) error {
			mergeCalls++
			return nil
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Zero(mergeCalls, "merge wait must not run when there is no PR")
}

// TestGitRebaseAndForcePush exercises the real rebase helper against a repo
// whose branch is behind origin/main but has no conflicting changes.
func TestGitRebaseAndForcePush(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")

	// Advance origin/main with a new commit from a second clone.
	remoteURL, err := exec.Command("git", "-C", local, "remote", "get-url", "origin").Output()
	r.NoError(err)
	remote := string(remoteURL[:len(remoteURL)-1]) // strip trailing newline

	other := t.TempDir()
	run(t, "", "git", "clone", remote, other)
	run(t, other, "git", "checkout", "main")
	writeTestFile(t, other, "upstream.txt", "human being mascot")
	run(t, other, "git", "add", ".")
	run(t, other, "git", "commit", "-m", "upstream change")
	run(t, other, "git", "push", "origin", "main")

	// Local makes its own commit on a feature branch and pushes it.
	run(t, local, "git", "checkout", "-b", "jelmer/feature-1")
	writeTestFile(t, local, "feature.txt", "paintball arena")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "feature work")
	run(t, local, "git", "push", "origin", "jelmer/feature-1")

	// Rebase the feature branch onto the advanced main and force-push.
	r.NoError(gitRebaseAndForcePush(context.Background(), local, "main"))

	// The upstream file should now be present in the local working tree.
	_, statErr := os.Stat(filepath.Join(local, "upstream.txt"))
	r.NoError(statErr, "rebase should bring in upstream commits")
}
