package phase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParsePRState(t *testing.T) {
	tests := map[string]struct {
		raw        string
		wantState  string
		wantMerged bool
		wantClosed bool
		wantErr    bool
	}{
		"open": {
			raw:       `{"state":"OPEN","mergedAt":null}`,
			wantState: "OPEN",
		},
		"merged via state": {
			raw:        `{"state":"MERGED","mergedAt":"2026-06-29T10:00:00Z"}`,
			wantState:  "MERGED",
			wantMerged: true,
		},
		"merged via mergedAt only (legacy gh)": {
			raw:        `{"state":"CLOSED","mergedAt":"2026-06-29T10:00:00Z"}`,
			wantState:  "CLOSED",
			wantMerged: true,
		},
		"closed unmerged": {
			raw:        `{"state":"CLOSED","mergedAt":""}`,
			wantState:  "CLOSED",
			wantClosed: true,
		},
		"malformed json": {
			raw:     `{not json`,
			wantErr: true,
		},
		"empty object": {
			raw:     `{}`,
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			st, err := parsePRState([]byte(tc.raw))
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Equal(tc.wantState, st.State)
			r.Equal(tc.wantMerged, st.merged())
			r.Equal(tc.wantClosed, st.closed())
		})
	}
}

func TestExtractPRNumberFromURL(t *testing.T) {
	tests := map[string]struct {
		url  string
		want int
	}{
		"github pull":      {"https://github.com/greendale/repo/pull/42", 42},
		"gitlab mr":        {"https://gitlab.com/greendale/repo/-/merge_requests/7", 7},
		"bitbucket":        {"https://bitbucket.org/g/r/pull-requests/13", 13},
		"empty":            {"", 0},
		"no number":        {"https://github.com/g/r/pull/", 0},
		"unrelated url":    {"https://github.com/g/r/issues/9", 0},
		"trailing segment": {"https://github.com/g/r/pull/99/files", 99},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, extractPRNumberFromURL(tc.url))
		})
	}
}

// TestWaitForMerge_MergedImmediately verifies an already-merged PR returns
// without waiting a full interval.
func TestWaitForMerge_MergedImmediately(t *testing.T) {
	r := require.New(t)

	calls := 0
	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     42,
		PollInterval: time.Hour, // never reached
		Timeout:      time.Minute,
		QueryState: func(context.Context, string, int) (PRState, error) {
			calls++
			return PRState{State: "MERGED"}, nil
		},
		Rebase: func(context.Context, string, string) error { return nil },
	})
	r.NoError(err)
	r.Equal(MergeOutcomeMerged, outcome)
	r.Equal(1, calls, "should merge on the immediate probe")
}

// TestWaitForMerge_MergesAfterPolling verifies OPEN states are polled until the
// PR merges.
func TestWaitForMerge_MergesAfterPolling(t *testing.T) {
	r := require.New(t)

	states := []PRState{
		{State: "OPEN"},
		{State: "OPEN"},
		{State: "MERGED"},
	}
	idx := 0
	rebases := 0
	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     7,
		PollInterval: time.Millisecond,
		Timeout:      time.Minute,
		QueryState: func(context.Context, string, int) (PRState, error) {
			st := states[idx]
			if idx < len(states)-1 {
				idx++
			}
			return st, nil
		},
		Rebase: func(context.Context, string, string) error { rebases++; return nil },
	})
	r.NoError(err)
	r.Equal(MergeOutcomeMerged, outcome)
	r.GreaterOrEqual(rebases, 2, "OPEN states should trigger rebase attempts")
}

// TestWaitForMerge_ClosedHalts verifies a closed-unmerged PR returns an error.
func TestWaitForMerge_ClosedHalts(t *testing.T) {
	r := require.New(t)

	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     9,
		PollInterval: time.Millisecond,
		Timeout:      time.Minute,
		QueryState: func(context.Context, string, int) (PRState, error) {
			return PRState{State: "CLOSED"}, nil
		},
		Rebase: func(context.Context, string, string) error { return nil },
	})
	r.Error(err)
	r.Equal(MergeOutcomeClosed, outcome)
	r.Contains(err.Error(), "closed without merging")
}

// TestWaitForMerge_Timeout verifies the timeout elapses when the PR stays OPEN.
func TestWaitForMerge_Timeout(t *testing.T) {
	r := require.New(t)

	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     5,
		PollInterval: 5 * time.Millisecond,
		Timeout:      20 * time.Millisecond,
		QueryState: func(context.Context, string, int) (PRState, error) {
			return PRState{State: "OPEN"}, nil
		},
		Rebase: func(context.Context, string, string) error { return nil },
	})
	r.Error(err)
	r.Equal(MergeOutcomeTimeout, outcome)
	r.Contains(err.Error(), "timed out")
}

// TestWaitForMerge_QueryErrorRetries verifies transient query errors are retried
// rather than aborting the wait.
func TestWaitForMerge_QueryErrorRetries(t *testing.T) {
	r := require.New(t)

	calls := 0
	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     3,
		PollInterval: time.Millisecond,
		Timeout:      time.Minute,
		QueryState: func(context.Context, string, int) (PRState, error) {
			calls++
			switch calls {
			case 1, 2:
				return PRState{}, errors.New("network blip at greendale")
			default:
				return PRState{State: "MERGED"}, nil
			}
		},
		Rebase: func(context.Context, string, string) error { return nil },
	})
	r.NoError(err)
	r.Equal(MergeOutcomeMerged, outcome)
	r.GreaterOrEqual(calls, 3, "transient errors must not abort the wait")
}

// TestWaitForMerge_ContextCancelled verifies parent cancellation returns the
// context error distinct from a timeout.
func TestWaitForMerge_ContextCancelled(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	outcome, err := WaitForMerge(ctx, WaitForMergeOpts{
		PRNumber:     1,
		PollInterval: 5 * time.Millisecond,
		Timeout:      time.Hour,
		QueryState: func(context.Context, string, int) (PRState, error) {
			cancel()
			return PRState{State: "OPEN"}, nil
		},
		Rebase: func(context.Context, string, string) error { return nil },
	})
	r.Error(err)
	r.Equal(MergeOutcomeTimeout, outcome)
	r.Contains(err.Error(), "cancelled")
}

// TestWaitForMerge_RebaseFailureNonFatal verifies a failed rebase is logged but
// polling continues until the PR merges.
func TestWaitForMerge_RebaseFailureNonFatal(t *testing.T) {
	r := require.New(t)

	states := []PRState{{State: "OPEN"}, {State: "MERGED"}}
	idx := 0
	outcome, err := WaitForMerge(context.Background(), WaitForMergeOpts{
		PRNumber:     2,
		PollInterval: time.Millisecond,
		Timeout:      time.Minute,
		QueryState: func(context.Context, string, int) (PRState, error) {
			st := states[idx]
			if idx < len(states)-1 {
				idx++
			}
			return st, nil
		},
		Rebase: func(context.Context, string, string) error {
			return errors.New("merge conflict in jeff's car")
		},
	})
	r.NoError(err)
	r.Equal(MergeOutcomeMerged, outcome)
}
