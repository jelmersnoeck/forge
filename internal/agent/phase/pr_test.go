package phase

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestExistingPRURL_ValidatesURL(t *testing.T) {
	r := require.New(t)

	// existingPRURL on a non-git dir returns "".
	result := existingPRURL(context.Background(), t.TempDir())
	r.Empty(result, "non-git directory should return empty")
}

func TestExistingPRURL_RespectsContext(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := existingPRURL(ctx, t.TempDir())
	r.Empty(result, "cancelled context should return empty")
}

func TestEnsureExistingPR_NotGitRepo(t *testing.T) {
	r := require.New(t)

	result := ensureExistingPR(context.Background(), t.TempDir(), "https://github.com/test/repo/pull/1")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "not a git repository")
}

func TestEnsureExistingPR_InvalidBranch(t *testing.T) {
	// Validates that branch names with shell metacharacters are rejected.
	r := require.New(t)
	r.False(tools.ValidateBranchName("branch;rm -rf /"))
	r.False(tools.ValidateBranchName(""))
	r.False(tools.ValidateBranchName("-malicious"))
	r.True(tools.ValidateBranchName("jelmer/fix-the-thing"))
}

func TestEnsurePR_ContextCancelledImmediate(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// EnsurePR checks ctx.Err() first and bails.
	result := EnsurePR(ctx, nil, t.TempDir(), "")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "skipped")
}

func TestPROperationError(t *testing.T) {
	tests := map[string]struct {
		err  *PROperationError
		want string
	}{
		"with stderr": {
			err: &PROperationError{
				Operation: "push",
				Stderr:    "Permission denied",
				Err:       context.DeadlineExceeded,
			},
			want: "push failed: Permission denied (context deadline exceeded)",
		},
		"without stderr": {
			err: &PROperationError{
				Operation: "fetch",
				Err:       context.Canceled,
			},
			want: "fetch failed: context canceled",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, tc.err.Error())
			r.ErrorIs(tc.err, tc.err.Err)
		})
	}
}

func TestCreatePR_DeprecatedDelegatesToInternal(t *testing.T) {
	r := require.New(t)

	// CreatePR on a non-git dir should hit the same precondition check
	// as createNewPR.
	result := CreatePR(context.Background(), nil, t.TempDir(), "")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "skipped")
}
