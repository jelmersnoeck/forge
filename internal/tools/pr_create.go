package tools

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

const (
	prTitleMinLen       = 15
	prDescriptionMinLen = 50
)

// genericTitles are low-effort PR titles that get rejected.
var genericTitles = []string{
	"fix", "fix bug", "update", "update code", "changes",
	"wip", "stuff", "misc", "pr", "pull request",
	"refactor", "cleanup", "clean up", "minor changes",
	"quick fix", "hotfix", "patch", "test", "tests",
}

// PRCreateTool returns the tool definition for creating GitHub pull requests.
func PRCreateTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "PRCreate",
		Description: `Create a GitHub pull request for the current branch. Requires a descriptive title and description.

IMPORTANT: Before calling this tool, review ALL changes in the PR (not just the last commit) by examining the full diff against the base branch. The description must cover the entire set of changes.

The PR is always created in draft mode. Requires the 'gh' CLI to be installed and authenticated.

Before calling this tool you MUST:
1. Run "git diff origin/<base>...HEAD" and read the full diff
2. Run "git log origin/<base>..HEAD --oneline" to see all commits
3. Understand the PURPOSE of the combined changes — not each commit in isolation
4. Write a title that captures the overall intent (imperative mood: "Add X", "Fix Y")
5. Write a description with: what changed (summary), why (motivation), and notable details
6. Do NOT just list commit messages as bullet points — synthesize them into prose
7. Do NOT copy-paste the branch name as the title

The tool handles fetching, rebasing onto the latest base branch, and pushing automatically.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Descriptive PR title summarizing ALL changes. Must be at least 15 characters. Generic titles like 'fix bug' or 'update' will be rejected.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Detailed PR description covering the full set of changes (not just the last commit). Must be at least 50 characters. Should explain what changed, why, and any notable implementation details. Do NOT just list commit messages.",
				},
				"base_branch": map[string]any{
					"type":        "string",
					"description": "Base branch to target (default: repo default branch, usually 'main').",
				},
			},
			"required": []string{"title", "description"},
		},
		Handler:     prCreateHandler,
		ReadOnly:    false,
		Destructive: false,
	}
}

func prCreateHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	title, _ := input["title"].(string)
	description, _ := input["description"].(string)
	baseBranch, _ := input["base_branch"].(string)

	// Validate title
	if err := validatePRTitle(title); err != nil {
		return toolError("Invalid PR title: %s", err), nil
	}

	// Validate description
	if err := validatePRDescription(description); err != nil {
		return toolError("Invalid PR description: %s", err), nil
	}

	// Verify gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return toolError("The 'gh' CLI is not installed or not on PATH. Install it from https://cli.github.com/"), nil
	}

	// Verify we're in a git repo
	if err := RunGitCmd(ctx.CWD, "rev-parse", "--git-dir"); err != nil {
		return toolError("Not inside a git repository."), nil
	}

	// Get current branch
	currentBranch, err := GitOutput(ctx.CWD, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return toolError("Failed to get current branch: %s", err), nil
	}

	if currentBranch == "main" || currentBranch == "master" {
		return toolError("Refusing to create a PR from '%s'. Create a feature branch first.", currentBranch), nil
	}

	// Determine base branch
	base := baseBranch
	if base == "" {
		base = DetectDefaultBranch(ctx.CWD)
	}

	// Fetch latest base branch from origin
	if _, stderr, err := GitOutputFull(ctx.CWD, "fetch", "origin", base); err != nil {
		return toolError("Failed to fetch origin/%s: %s", base, stderr), nil
	}

	// Rebase onto latest base branch
	if _, stderr, err := GitOutputFull(ctx.CWD, "rebase", "origin/"+base); err != nil {
		// Abort the failed rebase so we don't leave the repo in a broken state
		_ = RunGitCmd(ctx.CWD, "rebase", "--abort")
		return toolError("Rebase onto origin/%s failed (conflicts?). Resolve manually.\n%s", base, stderr), nil
	}

	// Verify there are changes
	diffStat, _ := GitOutput(ctx.CWD, "diff", "--stat", "origin/"+base+"...HEAD")
	if diffStat == "" {
		return toolError("No changes detected between 'origin/%s' and current branch '%s'. Nothing to create a PR for.", base, currentBranch), nil
	}

	// Detect lazy commit-list descriptions
	commitLog, _ := GitOutput(ctx.CWD, "log", "origin/"+base+"..HEAD", "--oneline")
	if err := detectCommitListDescription(description, commitLog); err != nil {
		return toolError("Invalid PR description: %s", err), nil
	}

	// Push the branch
	if _, stderr, err := GitOutputFull(ctx.CWD, "push", "--force-with-lease", "origin", "HEAD"); err != nil {
		return toolError("Failed to push branch: %s", stderr), nil
	}

	// Build gh pr create command
	args := []string{"pr", "create",
		"--draft",
		"--title", title,
		"--body", description,
	}
	if baseBranch != "" {
		args = append(args, "--base", baseBranch)
	}

	cmd := exec.CommandContext(ctx.Ctx, "gh", args...)
	cmd.Dir = ctx.CWD

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return toolError("Failed to create PR: %s", strings.TrimSpace(errMsg)), nil
	}

	prURL := strings.TrimSpace(stdout.String())

	var result strings.Builder
	fmt.Fprintf(&result, "Draft PR created: %s\n\n", prURL)
	fmt.Fprintf(&result, "Branch: %s -> %s\n\n", currentBranch, base)
	result.WriteString("Diff summary:\n")
	result.WriteString(diffStat)

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: result.String(),
		}},
	}, nil
}

func validatePRTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is required")
	}

	if len(title) < prTitleMinLen {
		return fmt.Errorf("title must be at least %d characters (got %d). Be more descriptive about what this PR does", prTitleMinLen, len(title))
	}

	lower := strings.ToLower(title)
	for _, generic := range genericTitles {
		if lower == generic {
			return fmt.Errorf("'%s' is too generic. Describe what the PR actually does (e.g., 'Add user authentication via OAuth2' instead of '%s')", title, title)
		}
	}

	return nil
}

func validatePRDescription(description string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("description is required")
	}

	if len(description) < prDescriptionMinLen {
		return fmt.Errorf("description must be at least %d characters (got %d). Explain what changed, why, and any notable implementation details. Cover ALL changes, not just the last commit", prDescriptionMinLen, len(description))
	}

	return nil
}

// detectCommitListDescription rejects descriptions that are just a list of
// commit messages. Compares description lines against the commit log; if most
// non-empty lines match commit subjects, the description is lazy.
func detectCommitListDescription(description, commitLog string) error {
	if commitLog == "" {
		return nil
	}

	commits := strings.Split(strings.TrimSpace(commitLog), "\n")
	if len(commits) <= 1 {
		return nil
	}

	// Extract commit subjects (strip leading hash from --oneline)
	subjects := make(map[string]bool, len(commits))
	for _, line := range commits {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) == 2 {
			subjects[strings.ToLower(strings.TrimSpace(parts[1]))] = true
		}
	}

	// Count description lines that match commit subjects
	descLines := strings.Split(strings.TrimSpace(description), "\n")
	var nonEmpty, matched int
	for _, line := range descLines {
		cleaned := strings.TrimSpace(line)
		// Strip common list prefixes: "- ", "* ", "1. ", "- [ ] "
		cleaned = strings.TrimLeft(cleaned, "-*•")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}
		nonEmpty++
		if subjects[strings.ToLower(cleaned)] {
			matched++
		}
	}

	if nonEmpty > 0 && matched > 0 && float64(matched)/float64(nonEmpty) > 0.5 {
		return fmt.Errorf("description looks like a copy-paste of commit messages. Synthesize the changes into a coherent description: what changed, why, and notable implementation details")
	}

	return nil
}

// DetectDefaultBranch figures out the repo's default branch.
func DetectDefaultBranch(cwd string) string {
	// Try gh first — it knows the remote default
	if branch, err := GHOutput(cwd, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name"); err == nil && branch != "" {
		return branch
	}

	// Fallback: check common names
	for _, candidate := range []string{"main", "master"} {
		if RunGitCmd(cwd, "rev-parse", "--verify", candidate) == nil {
			return candidate
		}
	}

	return "main"
}

// RunGitCmd runs a git command and returns an error if it fails.
func RunGitCmd(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	return cmd.Run()
}

// GitOutput runs a git command and returns trimmed stdout.
func GitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GitOutputFull runs a git command and returns stdout, stderr, and error.
func GitOutputFull(cwd string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// GHOutput runs a gh command and returns trimmed stdout.
func GHOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// toolError builds an error ToolResult.
func toolError(format string, args ...any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: fmt.Sprintf(format, args...),
		}},
		IsError: true,
	}
}
