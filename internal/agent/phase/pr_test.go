package phase

import (
	"context"
	"fmt"
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
	result := EnsurePR(ctx, nil, "anthropic", t.TempDir(), "")
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

func TestPROperationError_SanitizesStderr(t *testing.T) {
	tests := map[string]struct {
		stderr     string
		wantAbsent string
	}{
		"strips auth tokens": {
			stderr:     "remote: Invalid username or password.\nfatal: Authentication failed for 'https://oauth2:gho_s3cr3tT0k3n@github.com/owner/repo.git/'",
			wantAbsent: "gho_s3cr3tT0k3n",
		},
		"strips bearer tokens": {
			stderr:     "fatal: unable to access 'https://github.com/': The requested URL returned error: 403\nAuthorization: Bearer ghp_abc123secret456",
			wantAbsent: "ghp_abc123secret456",
		},
		"strips basic auth in URL": {
			stderr:     "fatal: Authentication failed for 'https://user:password123@github.com/repo.git/'",
			wantAbsent: "password123",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			opErr := &PROperationError{
				Operation: "push",
				Stderr:    tc.stderr,
				Err:       fmt.Errorf("exit status 128"),
			}
			msg := opErr.Error()
			r.NotContains(msg, tc.wantAbsent)
		})
	}
}

func TestEnsureExistingPR_ReturnsOperationErrors(t *testing.T) {
	r := require.New(t)

	// ensureExistingPR on a real git repo with no remote will fail fetch.
	// The operation errors should be accessible in the result.
	dir := t.TempDir()
	run(t, dir, "git", "init", "-b", "main")
	writeTestFile(t, dir, "README.md", "# Greendale Community College")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial commit")

	// Create a branch so it's not on main.
	run(t, dir, "git", "checkout", "-b", "jelmer/test-feature")

	result := ensureExistingPR(context.Background(), dir, "https://github.com/test/repo/pull/1")
	// Should still return the PR URL even with operation errors.
	r.Equal("https://github.com/test/repo/pull/1", result.URL)
	// Should have operation errors attached.
	r.NotEmpty(result.OperationErrors, "should capture fetch/rebase failures")
}

func TestValidatePRURL(t *testing.T) {
	tests := map[string]struct {
		url  string
		want bool
	}{
		"github PR":         {url: "https://github.com/owner/repo/pull/42", want: true},
		"github enterprise": {url: "https://github.example.com/org/repo/pull/1", want: true},
		"gitlab MR":         {url: "https://gitlab.com/org/repo/-/merge_requests/5", want: true},
		"random URL":        {url: "https://example.com/hello", want: false},
		"not https":         {url: "http://github.com/owner/repo/pull/1", want: true},
		"ftp scheme":        {url: "ftp://github.com/owner/repo/pull/1", want: false},
		"empty":             {url: "", want: false},
		"not a URL":         {url: "not-a-url", want: false},
		"bitbucket PR":      {url: "https://bitbucket.org/org/repo/pull-requests/3", want: true},
		"azure devops PR":   {url: "https://dev.azure.com/org/project/_git/repo/pullrequest/1", want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isValidPRURL(tc.url))
		})
	}
}

func TestCreatePR_DeprecatedDelegatesToInternal(t *testing.T) {
	r := require.New(t)

	// CreatePR on a non-git dir should hit the same precondition check
	// as createNewPR.
	result := CreatePR(context.Background(), nil, "anthropic", t.TempDir(), "")
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "skipped")
}
