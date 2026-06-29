package phase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatProgressComment(t *testing.T) {
	tests := map[string]struct {
		index int
		total int
		rec   phaseRecord
		want  string
	}{
		"with PR": {
			2, 6,
			phaseRecord{Title: "provider-aware-lightweight", PRURL: "https://github.com/o/r/pull/217"},
			"Phase 2/6 complete: **provider-aware-lightweight** — PR #217 merged.",
		},
		"no PR": {
			1, 3,
			phaseRecord{Title: "foundations"},
			"Phase 1/3 complete: **foundations**.",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, formatProgressComment(tc.index, tc.total, tc.rec))
		})
	}
}

func TestFormatCompletionSummary(t *testing.T) {
	r := require.New(t)

	records := []phaseRecord{
		{Number: 221, Title: "a", PRURL: "https://github.com/o/r/pull/215", Merged: true},
		{Number: 222, Title: "b", PRURL: "https://github.com/o/r/pull/216", Merged: true},
		{Number: 223, Title: "c"},
	}
	got := formatCompletionSummary(records)
	r.Contains(got, "All 3 phases complete:")
	r.Contains(got, "- #221 → PR #215 (merged)")
	r.Contains(got, "- #222 → PR #216 (merged)")
	r.Contains(got, "- #223 → no PR")
	// No trailing newline.
	r.NotEqual("\n", got[len(got)-1:])
}

func TestFormatCompletionSummary_NoNumberFallsBackToTitle(t *testing.T) {
	r := require.New(t)
	got := formatCompletionSummary([]phaseRecord{
		{Title: "señor chang", PRURL: "https://github.com/o/r/pull/9", Merged: false},
	})
	r.Contains(got, "- señor chang → PR #9 (open)")
}

func TestFormatPhaseFailureComment(t *testing.T) {
	r := require.New(t)

	rec := phaseRecord{Number: 42, Title: "annie's boobs", PRURL: "https://github.com/o/r/pull/7"}
	got := formatPhaseFailureComment(2, 6, rec, errors.New("escaped the lab"))
	r.Contains(got, "Phase 2/6 failed: **annie's boobs** (#42).")
	r.Contains(got, "Error: escaped the lab")
	r.Contains(got, "PR: https://github.com/o/r/pull/7")
}

func TestFormatPhaseFailureComment_NoPRNoNumber(t *testing.T) {
	r := require.New(t)
	got := formatPhaseFailureComment(1, 2, phaseRecord{Title: "troy"}, errors.New("boom"))
	r.Contains(got, "Phase 1/2 failed: **troy**.")
	r.Contains(got, "Error: boom")
	r.NotContains(got, "PR:")
	r.NotContains(got, "(#")
}

// TestRunMultiPhase_PostsProgressAndSummary verifies progress comments fire per
// merged phase, a completion summary fires once, and the parent is closed.
func TestRunMultiPhase_PostsProgressAndSummary(t *testing.T) {
	r := require.New(t)

	var comments []string
	var closedParents, closedSubs []int

	opts := MultiPhaseOpts{
		ParentSlug:   "greendale",
		ParentNumber: 100,
		Phases: []SubIssue{
			{Number: 1, Title: "foundations"},
			{Number: 2, Title: "walls"},
		},
		SpawnAgent:     func(context.Context, string, string, string) error { return nil },
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/o/r/pull/50"}
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
		WaitForMerge:   true,
		WaitForMergeFn: func(context.Context, WaitForMergeOpts) (MergeOutcome, error) {
			return MergeOutcomeMerged, nil
		},
		PullBase: func(context.Context, string, string) error { return nil },
		CloseSubIssue: func(_ context.Context, _ string, n int) error {
			closedSubs = append(closedSubs, n)
			return nil
		},
		CommentIssue: func(_ context.Context, _ string, _ int, body string) error {
			comments = append(comments, body)
			return nil
		},
		CloseParent: func(_ context.Context, _ string, n int) error {
			closedParents = append(closedParents, n)
			return nil
		},
	}

	r.NoError(RunMultiPhase(context.Background(), opts))

	r.Equal([]int{1, 2}, closedSubs, "both sub-issues closed")
	r.Equal([]int{100}, closedParents, "parent closed once after all phases")

	// 2 progress comments + 1 completion summary.
	r.Len(comments, 3)
	r.Contains(comments[0], "Phase 1/2 complete: **foundations**")
	r.Contains(comments[1], "Phase 2/2 complete: **walls**")
	r.Contains(comments[2], "All 2 phases complete:")
}

// TestRunMultiPhase_NoParentNumberSkipsLifecycle verifies that with
// ParentNumber == 0 no parent comments or parent close occur.
func TestRunMultiPhase_NoParentNumberSkipsLifecycle(t *testing.T) {
	r := require.New(t)

	commentCalls, closeParentCalls := 0, 0
	opts := MultiPhaseOpts{
		ParentSlug:     "p",
		ParentNumber:   0,
		Phases:         []SubIssue{{Number: 1, Title: "a"}},
		SpawnAgent:     func(context.Context, string, string, string) error { return nil },
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/o/r/pull/1"}
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
		WaitForMerge:   true,
		WaitForMergeFn: func(context.Context, WaitForMergeOpts) (MergeOutcome, error) {
			return MergeOutcomeMerged, nil
		},
		PullBase:      func(context.Context, string, string) error { return nil },
		CloseSubIssue: func(context.Context, string, int) error { return nil },
		CommentIssue: func(context.Context, string, int, string) error {
			commentCalls++
			return nil
		},
		CloseParent: func(context.Context, string, int) error {
			closeParentCalls++
			return nil
		},
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Zero(commentCalls, "no parent comments when ParentNumber is 0")
	r.Zero(closeParentCalls, "parent not closed when ParentNumber is 0")
}

// TestRunMultiPhase_PostsFailureCommentOnSpawnFailure verifies a sub-agent
// failure posts an error comment to the parent and does not post a summary or
// close the parent.
func TestRunMultiPhase_PostsFailureCommentOnSpawnFailure(t *testing.T) {
	r := require.New(t)

	var comments []string
	closeParentCalls := 0
	opts := MultiPhaseOpts{
		ParentSlug:   "p",
		ParentNumber: 100,
		Phases:       []SubIssue{{Number: 1, Title: "doomed"}, {Number: 2, Title: "never"}},
		SpawnAgent: func(context.Context, string, string, string) error {
			return errors.New("dean called")
		},
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		CommentIssue: func(_ context.Context, _ string, _ int, body string) error {
			comments = append(comments, body)
			return nil
		},
		CloseParent: func(context.Context, string, int) error {
			closeParentCalls++
			return nil
		},
	}

	err := RunMultiPhase(context.Background(), opts)
	r.Error(err)
	r.Len(comments, 1, "one failure comment, no summary")
	r.Contains(comments[0], "Phase 1/2 failed: **doomed** (#1).")
	r.Contains(comments[0], "dean called")
	r.Zero(closeParentCalls, "parent not closed on failure")
}

// TestRunMultiPhase_CommentFailureNonFatal verifies a parent comment error does
// not halt the pipeline.
func TestRunMultiPhase_CommentFailureNonFatal(t *testing.T) {
	r := require.New(t)

	spawned := 0
	opts := MultiPhaseOpts{
		ParentSlug:     "p",
		ParentNumber:   100,
		Phases:         []SubIssue{{Number: 1, Title: "a"}, {Number: 2, Title: "b"}},
		SpawnAgent:     func(context.Context, string, string, string) error { spawned++; return nil },
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/o/r/pull/1"}
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
		WaitForMerge:   true,
		WaitForMergeFn: func(context.Context, WaitForMergeOpts) (MergeOutcome, error) {
			return MergeOutcomeMerged, nil
		},
		PullBase:      func(context.Context, string, string) error { return nil },
		CloseSubIssue: func(context.Context, string, int) error { return nil },
		CommentIssue: func(context.Context, string, int, string) error {
			return fmt.Errorf("rate limited")
		},
		CloseParent: func(context.Context, string, int) error { return nil },
	}

	r.NoError(RunMultiPhase(context.Background(), opts))
	r.Equal(2, spawned, "comment failures must not halt the pipeline")
}

// TestRunMultiPhase_PostsFailureCommentOnMergeWait verifies a merge-wait halt
// (closed/timeout) posts a failure comment naming the phase.
func TestRunMultiPhase_PostsFailureCommentOnMergeWait(t *testing.T) {
	r := require.New(t)

	var comments []string
	opts := MultiPhaseOpts{
		ParentSlug:     "p",
		ParentNumber:   100,
		Phases:         []SubIssue{{Number: 5, Title: "stuck"}},
		SpawnAgent:     func(context.Context, string, string, string) error { return nil },
		CreateWorktree: func(_ context.Context, _, _, _, path string) (string, error) { return path, nil },
		EnsurePRFn: func(context.Context, string) PRResult {
			return PRResult{URL: "https://github.com/o/r/pull/9"}
		},
		RemoveWorktree: func(context.Context, string, string) error { return nil },
		WaitForMerge:   true,
		WaitForMergeFn: func(context.Context, WaitForMergeOpts) (MergeOutcome, error) {
			return MergeOutcomeClosed, errors.New("closed without merging")
		},
		CommentIssue: func(_ context.Context, _ string, _ int, body string) error {
			comments = append(comments, body)
			return nil
		},
		CloseParent: func(context.Context, string, int) error { return nil },
	}

	err := RunMultiPhase(context.Background(), opts)
	r.Error(err)
	r.Len(comments, 1)
	r.Contains(comments[0], "Phase 1/1 failed: **stuck** (#5).")
	r.Contains(comments[0], "closed without merging")
	r.Contains(comments[0], "PR: https://github.com/o/r/pull/9")
}
