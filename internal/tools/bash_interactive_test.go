package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// TestBashNonInteractiveGit tests that git commands that would normally
// open an editor work non-interactively thanks to GIT_EDITOR=true
func TestBashNonInteractiveGit(t *testing.T) {
	r := require.New(t)

	// Create temp dir with git repo
	tempDir := t.TempDir()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tempDir,
	}

	// Initialize git repo
	result, err := bashHandler(map[string]any{
		"command": "git init && git config user.email 'test@example.com' && git config user.name 'Test User'",
	}, ctx)
	r.NoError(err)
	r.False(result.IsError, "git init should succeed: %s", result.Content[0].Text)

	// Create a file and commit it
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("initial content"), 0644)
	r.NoError(err)

	result, err = bashHandler(map[string]any{
		"command": "git add test.txt && git commit -m 'Initial commit'",
	}, ctx)
	r.NoError(err)
	r.False(result.IsError, "git commit should succeed: %s", result.Content[0].Text)

	// Modify the file
	err = os.WriteFile(testFile, []byte("modified content"), 0644)
	r.NoError(err)

	result, err = bashHandler(map[string]any{
		"command": "git add test.txt && git commit -m 'Second commit'",
	}, ctx)
	r.NoError(err)
	r.False(result.IsError, "second commit should succeed: %s", result.Content[0].Text)

	// Test git rebase without -i flag - this should work fine
	// We'll rebase onto the first commit (which is a no-op but tests the mechanism)
	result, err = bashHandler(map[string]any{
		"command": "git rebase HEAD~1",
	}, ctx)
	r.NoError(err)
	// This might fail with "Current branch main is up to date" which is fine
	// The important part is it doesn't hang waiting for an editor
	t.Logf("git rebase output: %s", result.Content[0].Text)

	// Test that git commit --amend works non-interactively with --no-edit
	// (--no-edit is valid for commit, not rebase)
	result, err = bashHandler(map[string]any{
		"command": "git commit --amend --no-edit",
	}, ctx)
	r.NoError(err)
	r.False(result.IsError, "git commit --amend --no-edit should succeed: %s", result.Content[0].Text)
}

// TestBashEnvironmentVariables verifies that the bash tool sets the correct
// environment variables to prevent interactive editors
func TestBashEnvironmentVariables(t *testing.T) {
	r := require.New(t)

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	// Check that GIT_EDITOR is set to 'true'
	result, err := bashHandler(map[string]any{
		"command": "echo \"GIT_EDITOR=${GIT_EDITOR}\" && echo \"EDITOR=${EDITOR}\" && echo \"VISUAL=${VISUAL}\"",
	}, ctx)
	r.NoError(err)
	r.False(result.IsError)

	output := result.Content[0].Text
	r.Contains(output, "GIT_EDITOR=true", "GIT_EDITOR should be set to 'true'")
	r.Contains(output, "EDITOR=true", "EDITOR should be set to 'true'")
	r.Contains(output, "VISUAL=true", "VISUAL should be set to 'true'")
}
