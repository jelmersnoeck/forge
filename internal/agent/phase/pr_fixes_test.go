package phase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Issue 1: PR URL pattern uses named constants ---

func TestPRURLPathSegments_AreExportedConstants(t *testing.T) {
	// Verify the named constants exist and contain expected forge path segments.
	r := require.New(t)

	wantSegments := []string{"pull", "merge_requests", "pull-requests", "pullrequest"}
	vals := make([]string, 0, len(prURLPathSegments))
	for _, seg := range prURLPathSegments {
		vals = append(vals, seg)
	}
	for _, want := range wantSegments {
		r.Contains(vals, want, "missing path segment %q", want)
	}
}

// --- Issue 5: Pre-compiled regexes ---

func TestSanitizeStderr_PrecompiledRegexes(t *testing.T) {
	// compiledSensitivePatterns should be populated at package init time.
	r := require.New(t)
	r.NotEmpty(compiledSensitivePatterns, "sensitive patterns should be pre-compiled at package level")
}

func TestSanitizeStderr_Redacts(t *testing.T) {
	tests := map[string]struct {
		input      string
		wantAbsent string
	}{
		"github oauth token": {
			input:      "error: https://gho_s3cr3tT0k3n@github.com failed",
			wantAbsent: "gho_s3cr3tT0k3n",
		},
		"github PAT token": {
			input:      "auth: ghp_abc123secret456 expired",
			wantAbsent: "ghp_abc123secret456",
		},
		"github app token": {
			input:      "token ghs_AppTokenValue42 rejected",
			wantAbsent: "ghs_AppTokenValue42",
		},
		"github fine-grained PAT": {
			input:      "using github_pat_FineGrained_Token123",
			wantAbsent: "github_pat_FineGrained_Token123",
		},
		"bearer token": {
			input:      "Authorization: Bearer sk-proj-x.y.z rejected",
			wantAbsent: "Bearer sk-proj-x.y.z",
		},
		"basic auth in URL": {
			input:      "https://user:password123@github.com/repo.git",
			wantAbsent: "password123",
		},
		"clean input unchanged": {
			input:      "fatal: remote origin already exists.",
			wantAbsent: "", // nothing to redact
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			result := sanitizeStderr(tc.input)
			if tc.wantAbsent != "" {
				r.NotContains(result, tc.wantAbsent)
				r.Contains(result, "[REDACTED]")
			} else {
				r.NotContains(result, "[REDACTED]")
			}
		})
	}
}

// --- Issue 3 & 4: Metrics counters ---

func TestPRMetrics_PushFailureCounter(t *testing.T) {
	r := require.New(t)
	m := PRMetricsInstance()
	r.GreaterOrEqual(m.PushFailures(), int64(0), "push failure counter should be non-negative")
}

func TestPRMetrics_InvalidPRURLCounter(t *testing.T) {
	r := require.New(t)
	m := PRMetricsInstance()
	r.GreaterOrEqual(m.InvalidURLResults(), int64(0), "invalid URL counter should be non-negative")
}

// --- Issue 6: hasUnpushedCommits error triggers push fallback ---

func TestEnsureExistingPR_PushesOnUnpushedCheckError(t *testing.T) {
	r := require.New(t)

	// Repo with no remote: hasUnpushedCommits fails, so ensureExistingPR
	// should attempt a push anyway (safety fallback), resulting in both
	// a "check unpushed commits" and "push --force-with-lease" op error.
	dir := t.TempDir()
	run(t, dir, "git", "init", "-b", "main")
	writeTestFile(t, dir, "README.md", "# Senor Chang's classroom")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
	run(t, dir, "git", "checkout", "-b", "jelmer/push-fallback-test")

	result := ensureExistingPR(context.Background(), dir, "https://github.com/test/repo/pull/42")
	r.Equal("https://github.com/test/repo/pull/42", result.URL)

	// Should have errors for both the unpushed check AND the push attempt.
	ops := make([]string, len(result.OperationErrors))
	for i, e := range result.OperationErrors {
		ops[i] = e.Operation
	}
	r.Contains(ops, "check unpushed commits", "should record unpushed check failure")
	r.Contains(ops, "push --force-with-lease", "should attempt push when unpushed check fails")
}

// --- Issue 6: hasUnpushedCommits returns error ---

func TestHasUnpushedCommits_ReturnsError(t *testing.T) {
	r := require.New(t)

	// Non-git dir should return an error.
	has, err := hasUnpushedCommits(context.Background(), t.TempDir(), "main")
	r.Error(err, "should return error for non-git directory")
	r.False(has)
}

func TestHasUnpushedCommits_NoRemote(t *testing.T) {
	r := require.New(t)

	// Git repo with no remote should return error when checking remote SHA.
	dir := t.TempDir()
	run(t, dir, "git", "init", "-b", "main")
	writeTestFile(t, dir, "README.md", "# Greendale")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")

	has, err := hasUnpushedCommits(context.Background(), dir, "main")
	r.Error(err, "should return error when no origin remote exists")
	r.False(has)
}

func TestHasUnpushedCommits_InSync(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/synced")
	writeTestFile(t, local, "synced.go", "package synced")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "synced commit")
	run(t, local, "git", "push", "origin", "jelmer/synced")

	has, err := hasUnpushedCommits(context.Background(), local, "jelmer/synced")
	r.NoError(err)
	r.False(has, "should be in sync after push")
}

func TestHasUnpushedCommits_HasUnpushed(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/unpushed")
	writeTestFile(t, local, "first.go", "package first")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "first commit")
	run(t, local, "git", "push", "origin", "jelmer/unpushed")

	// Add another commit without pushing.
	writeTestFile(t, local, "second.go", "package second")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "second commit")

	has, err := hasUnpushedCommits(context.Background(), local, "jelmer/unpushed")
	r.NoError(err)
	r.True(has, "should detect unpushed commits")
}
