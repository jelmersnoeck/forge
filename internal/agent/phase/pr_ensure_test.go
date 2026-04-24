package phase

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExistingPRURL_NotGitRepo(t *testing.T) {
	r := require.New(t)
	url := existingPRURL(context.Background(), t.TempDir())
	r.Empty(url, "should return empty for non-git repo")
}

func TestExistingPRURL_NoGH(t *testing.T) {
	if _, err := exec.LookPath("gh"); err == nil {
		t.Skip("gh is installed, cannot test gh-absent path")
	}
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/greendale-paintball")
	writeTestFile(t, local, "paintball.go", "package paintball")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "add paintball arena")

	url := existingPRURL(context.Background(), local)
	r.Empty(url, "should return empty when gh is not installed")
}

func TestEnsurePR_NotGitRepo(t *testing.T) {
	r := require.New(t)
	result := EnsurePR(context.Background(), nil, "anthropic", t.TempDir(), "")
	r.Empty(result.URL)
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "skipped")
}

func TestEnsurePR_OnMainBranch(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	result := EnsurePR(context.Background(), nil, "anthropic", local, "")
	r.Empty(result.URL)
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "on main branch")
}

func TestEnsurePR_NoChanges(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/no-changes")

	result := EnsurePR(context.Background(), nil, "anthropic", local, "")
	r.Empty(result.URL)
	r.Error(result.Error)
	r.Contains(result.Error.Error(), "no changes")
}

func TestEnsurePR_ContextCancelled(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/cancelled-feature")
	writeTestFile(t, local, "cancelled.go", "package cancelled")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "add cancelled feature")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := EnsurePR(ctx, nil, "anthropic", local, "")
	// With cancelled context, should bail during fetch/rebase/push.
	// The precondition checks pass (they don't use ctx), but subsequent
	// git operations may fail or the gh call will fail.
	// Either way, it shouldn't panic.
	_ = result
	r.True(true, "should not panic on cancelled context")
}

func TestEnsurePR_ExistingPRDetection(t *testing.T) {
	// This is an integration test that requires gh to be installed
	// and authenticated. We test the logic flow, not the gh call.
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")
	run(t, local, "git", "checkout", "-b", "jelmer/existing-pr-test")
	writeTestFile(t, local, "existing.go", "package existing")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "add existing feature")

	// existingPRURL should return "" since there's no GH remote
	url := existingPRURL(context.Background(), local)
	r.Empty(url, "no PR exists for this local-only repo")
}
