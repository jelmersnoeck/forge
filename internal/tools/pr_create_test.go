package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestValidatePRTitle(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		title   string
		wantErr bool
	}{
		"empty title": {
			title:   "",
			wantErr: true,
		},
		"too short": {
			title:   "fix auth",
			wantErr: true,
		},
		"generic fix": {
			title:   "fix",
			wantErr: true,
		},
		"generic update": {
			title:   "update",
			wantErr: true,
		},
		"generic wip": {
			title:   "WIP",
			wantErr: true,
		},
		"generic cleanup": {
			title:   "cleanup",
			wantErr: true,
		},
		"generic refactor": {
			title:   "refactor",
			wantErr: true,
		},
		"generic tests": {
			title:   "tests",
			wantErr: true,
		},
		"whitespace only": {
			title:   "   ",
			wantErr: true,
		},
		"descriptive title": {
			title:   "Add OAuth2 authentication for Greendale API",
			wantErr: false,
		},
		"minimum length title": {
			title:   "Fix user signup",
			wantErr: false,
		},
		"title with fix prefix but descriptive": {
			title:   "Fix race condition in session cleanup",
			wantErr: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validatePRTitle(tc.title)
			if tc.wantErr {
				r.Error(err, "expected error for title: %q", tc.title)
				return
			}
			r.NoError(err, "unexpected error for title: %q", tc.title)
		})
	}
}

func TestValidatePRDescription(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		description string
		wantErr     bool
	}{
		"empty description": {
			description: "",
			wantErr:     true,
		},
		"too short": {
			description: "Fixed the thing that was broken.",
			wantErr:     true,
		},
		"whitespace only": {
			description: "     ",
			wantErr:     true,
		},
		"descriptive enough": {
			description: "This PR adds a new PRCreate tool that validates PR titles and descriptions to ensure they are descriptive enough.",
			wantErr:     false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validatePRDescription(tc.description)
			if tc.wantErr {
				r.Error(err, "expected error for description: %q", tc.description)
				return
			}
			r.NoError(err, "unexpected error for description: %q", tc.description)
		})
	}
}

func TestPRCreateTool_Schema(t *testing.T) {
	r := require.New(t)

	tool := PRCreateTool()
	r.Equal("PRCreate", tool.Name)
	r.False(tool.ReadOnly)

	props, ok := tool.InputSchema["properties"].(map[string]any)
	r.True(ok)
	r.Contains(props, "title")
	r.Contains(props, "description")
	r.Contains(props, "base_branch")

	required, ok := tool.InputSchema["required"].([]string)
	r.True(ok)
	r.Contains(required, "title")
	r.Contains(required, "description")
}

func TestPRCreateHandler_NotGitRepo(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "Add the Human Being mascot to the campus map",
		"description": "This PR adds the Greendale Human Being mascot to the interactive campus map, including SVG assets and proper positioning near the library.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Not inside a git repository")
}

func TestPRCreateHandler_OnMainBranch(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "Enroll Troy Barnes in Advanced Pillow Fighting",
		"description": "This PR adds Troy Barnes to the Advanced Pillow Fighting roster, updates the class schedule, and adds his rivalry stats with Abed.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Refusing to create a PR from 'main'")
}

func TestPRCreateHandler_InvalidTitle(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "fix",
		"description": "This PR adds a comprehensive fix for the issue where Señor Chang's grade distribution was incorrectly calculated.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Invalid PR title")
}

func TestPRCreateHandler_InvalidDescription(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "Add paintball tournament bracket generator",
		"description": "fixed stuff",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Invalid PR description")
}

func TestPRCreateHandler_NoChanges(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	// Create a branch with no changes
	gitExec(t, tmpDir, "checkout", "-b", "jelmer/no-changes")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "Add Dean Pelton's costume rotation scheduler",
		"description": "This PR implements the Dean Pelton costume rotation scheduler that ensures no dalmatian outfit is repeated within a two-week window.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "No changes detected")
}

func TestPRCreateHandler_WithChanges(t *testing.T) {
	r := require.New(t)

	// This test verifies the full flow up to the gh pr create call.
	// Since we can't actually create a PR without a remote, we just verify
	// validation passes and the gh error is about missing remote (not our validation).

	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh CLI not available")
	}

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	// Create a branch with actual changes
	gitExec(t, tmpDir, "checkout", "-b", "jelmer/add-paintball")

	// Add multiple commits to verify full-diff behavior
	writeFile(t, tmpDir, "tournament.go", "package paintball\n\nfunc Bracket() {}\n")
	gitExec(t, tmpDir, "add", ".")
	gitExec(t, tmpDir, "commit", "-m", "add tournament scaffold")

	writeFile(t, tmpDir, "scoring.go", "package paintball\n\nfunc Score() int { return 42 }\n")
	gitExec(t, tmpDir, "add", ".")
	gitExec(t, tmpDir, "commit", "-m", "add scoring")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: tmpDir,
	}

	input := map[string]any{
		"title":       "Implement paintball tournament bracket and scoring system",
		"description": "This PR adds the full paintball tournament system for Greendale's annual event. Includes bracket generation and a scoring module that tracks eliminations across rounds.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)

	// Without a real remote, gh will fail — but it should fail at the gh step,
	// not our validation. The error should be from gh, not from our tool.
	r.True(result.IsError)
	r.NotContains(result.Content[0].Text, "Invalid PR title")
	r.NotContains(result.Content[0].Text, "Invalid PR description")
	r.NotContains(result.Content[0].Text, "No changes detected")
	r.Contains(result.Content[0].Text, "Failed to create PR")
}

func TestDetectDefaultBranch(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		defaultBranch string
	}{
		"main branch": {
			defaultBranch: "main",
		},
		"master branch": {
			defaultBranch: "master",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			initGitRepo(t, tmpDir, tc.defaultBranch)

			branch := detectDefaultBranch(tmpDir)
			r.Equal(tc.defaultBranch, branch)
		})
	}
}

// initGitRepo creates a git repo with an initial commit on the given branch.
func initGitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	gitExec(t, dir, "init", "-b", branch)
	gitExec(t, dir, "config", "user.email", "chang@greendale.edu")
	gitExec(t, dir, "config", "user.name", "Señor Chang")
	writeFile(t, dir, "README.md", "# Greendale Community College\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")
}

func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
