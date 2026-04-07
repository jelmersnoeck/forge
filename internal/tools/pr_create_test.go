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

func TestDetectCommitListDescription(t *testing.T) {
	tests := map[string]struct {
		description string
		commitLog   string
		wantErr     bool
	}{
		"empty commit log": {
			description: "Some description that is long enough to pass validation.",
			commitLog:   "",
			wantErr:     false,
		},
		"single commit is always ok": {
			description: "add tournament scaffold",
			commitLog:   "abc1234 add tournament scaffold",
			wantErr:     false,
		},
		"bullet list of commits": {
			description: "- add tournament scaffold\n- add scoring\n- fix bracket seeding",
			commitLog:   "abc1234 add tournament scaffold\ndef5678 add scoring\nghi9012 fix bracket seeding",
			wantErr:     true,
		},
		"asterisk list of commits": {
			description: "* add tournament scaffold\n* add scoring\n* fix bracket seeding",
			commitLog:   "abc1234 add tournament scaffold\ndef5678 add scoring\nghi9012 fix bracket seeding",
			wantErr:     true,
		},
		"plain list of commits": {
			description: "add tournament scaffold\nadd scoring\nfix bracket seeding",
			commitLog:   "abc1234 add tournament scaffold\ndef5678 add scoring\nghi9012 fix bracket seeding",
			wantErr:     true,
		},
		"synthesized description with commit words": {
			description: "This PR adds the paintball tournament system including bracket generation and a scoring module. The bracket seeding algorithm uses past performance data.",
			commitLog:   "abc1234 add tournament scaffold\ndef5678 add scoring\nghi9012 fix bracket seeding",
			wantErr:     false,
		},
		"partial overlap is fine": {
			description: "## What changed\nAdded tournament scaffold and scoring system.\n\n## Why\nGreendale needs this for the annual paintball event.\n\n- add scoring",
			commitLog:   "abc1234 add tournament scaffold\ndef5678 add scoring",
			wantErr:     false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			err := detectCommitListDescription(tc.description, tc.commitLog)
			if tc.wantErr {
				r.Error(err)
				r.Contains(err.Error(), "copy-paste")
				return
			}
			r.NoError(err)
		})
	}
}

func TestPRCreateTool_Schema(t *testing.T) {
	r := require.New(t)

	tool := PRCreateTool()
	r.Equal("PRCreate", tool.Name)
	r.False(tool.ReadOnly)
	r.False(tool.Destructive)

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

	remote, local := initGitRepoWithRemote(t, "main")
	_ = remote

	// Create a branch with no changes
	gitExec(t, local, "checkout", "-b", "jelmer/no-changes")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: local,
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

func TestPRCreateHandler_RebasesBeforeCreating(t *testing.T) {
	r := require.New(t)

	remote, local := initGitRepoWithRemote(t, "main")

	// Create feature branch with a change
	gitExec(t, local, "checkout", "-b", "jelmer/add-paintball")
	writeFile(t, local, "tournament.go", "package paintball\n\nfunc Bracket() {}\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add tournament scaffold")

	// Simulate someone else pushing to main on the remote
	otherClone := t.TempDir()
	gitExec(t, otherClone, "clone", remote, ".")
	gitExec(t, otherClone, "config", "user.email", "winger@greendale.edu")
	gitExec(t, otherClone, "config", "user.name", "Jeff Winger")
	writeFile(t, otherClone, "other.go", "package other\n")
	gitExec(t, otherClone, "add", ".")
	gitExec(t, otherClone, "commit", "-m", "other work on main")
	gitExec(t, otherClone, "push", "origin", "main")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: local,
	}

	input := map[string]any{
		"title":       "Implement paintball tournament bracket system for Greendale",
		"description": "This PR adds the full paintball tournament bracket system for Greendale's annual event. Includes bracket generation that handles arbitrary team counts.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)

	// Will fail at gh pr create (no GitHub remote), but should have rebased first
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Failed to create PR")

	// Verify the rebase happened — our branch should now have the other commit
	log, _ := GitOutput(local, "log", "--oneline")
	r.Contains(log, "other work on main")
	r.Contains(log, "add tournament scaffold")
}

func TestPRCreateHandler_RebaseConflictAborts(t *testing.T) {
	r := require.New(t)

	remote, local := initGitRepoWithRemote(t, "main")

	// Create feature branch that modifies README
	gitExec(t, local, "checkout", "-b", "jelmer/edit-readme")
	writeFile(t, local, "README.md", "# Greendale: City College Sucks\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "update readme")

	// Push a conflicting README change to main via another clone
	otherClone := t.TempDir()
	gitExec(t, otherClone, "clone", remote, ".")
	gitExec(t, otherClone, "config", "user.email", "winger@greendale.edu")
	gitExec(t, otherClone, "config", "user.name", "Jeff Winger")
	writeFile(t, otherClone, "README.md", "# Greendale: Go Human Beings\n")
	gitExec(t, otherClone, "add", ".")
	gitExec(t, otherClone, "commit", "-m", "different readme edit")
	gitExec(t, otherClone, "push", "origin", "main")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: local,
	}

	input := map[string]any{
		"title":       "Update Greendale README with rivalry declaration",
		"description": "This PR updates the Greendale README to include a bold statement about City College. Morale is important.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Rebase onto origin/main failed")

	// Verify rebase was aborted (not left in progress)
	branch, _ := GitOutput(local, "rev-parse", "--abbrev-ref", "HEAD")
	r.Equal("jelmer/edit-readme", branch)
}

func TestPRCreateHandler_RejectsCommitListDescription(t *testing.T) {
	r := require.New(t)

	_, local := initGitRepoWithRemote(t, "main")

	// Create a branch with multiple commits
	gitExec(t, local, "checkout", "-b", "jelmer/multi-commit")

	writeFile(t, local, "tournament.go", "package paintball\n\nfunc Bracket() {}\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add tournament scaffold")

	writeFile(t, local, "scoring.go", "package paintball\n\nfunc Score() int { return 42 }\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add scoring")

	writeFile(t, local, "teams.go", "package paintball\n\nfunc Teams() []string { return nil }\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add teams")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: local,
	}

	input := map[string]any{
		"title": "Implement paintball tournament system for Greendale",
		"description": `- add tournament scaffold
- add scoring
- add teams`,
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "copy-paste")
}

func TestPRCreateHandler_WithChanges(t *testing.T) {
	r := require.New(t)

	// This test verifies the full flow up to the gh pr create call.
	// Since we can't actually create a PR without a GitHub remote, we verify
	// validation passes and the gh error is about missing remote (not our validation).

	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh CLI not available")
	}

	_, local := initGitRepoWithRemote(t, "main")

	// Create a branch with actual changes
	gitExec(t, local, "checkout", "-b", "jelmer/add-paintball")

	// Add multiple commits to verify full-diff behavior
	writeFile(t, local, "tournament.go", "package paintball\n\nfunc Bracket() {}\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add tournament scaffold")

	writeFile(t, local, "scoring.go", "package paintball\n\nfunc Score() int { return 42 }\n")
	gitExec(t, local, "add", ".")
	gitExec(t, local, "commit", "-m", "add scoring")

	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: local,
	}

	input := map[string]any{
		"title":       "Implement paintball tournament bracket and scoring system",
		"description": "This PR adds the full paintball tournament system for Greendale's annual event. Includes bracket generation and a scoring module that tracks eliminations across rounds.",
	}

	tool := PRCreateTool()
	result, err := tool.Handler(input, ctx)
	r.NoError(err)

	// Without a real GitHub remote, gh will fail — but it should fail at the gh step,
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

			branch := DetectDefaultBranch(tmpDir)
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

// initGitRepoWithRemote creates a bare "remote" repo and a clone of it,
// both with an initial commit on the given branch. Returns (remote, local) paths.
func initGitRepoWithRemote(t *testing.T, branch string) (string, string) {
	t.Helper()

	// Create a temporary repo to build the initial commit
	staging := t.TempDir()
	initGitRepo(t, staging, branch)

	// Create bare remote from it
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitExec(t, staging, "clone", "--bare", staging, remote)

	// Clone it to get a proper local with origin
	local := t.TempDir()
	gitExec(t, local, "clone", remote, ".")
	gitExec(t, local, "config", "user.email", "chang@greendale.edu")
	gitExec(t, local, "config", "user.name", "Señor Chang")

	return remote, local
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
