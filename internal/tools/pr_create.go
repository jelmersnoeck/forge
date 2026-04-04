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

The PR is always created in draft mode. Requires the 'gh' CLI to be installed and authenticated.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Descriptive PR title summarizing ALL changes. Must be at least 15 characters. Generic titles like 'fix bug' or 'update' will be rejected.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Detailed PR description covering the full set of changes (not just the last commit). Must be at least 50 characters. Should explain what changed, why, and any notable implementation details.",
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
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid PR title: %s", err),
			}},
			IsError: true,
		}, nil
	}

	// Validate description
	if err := validatePRDescription(description); err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid PR description: %s", err),
			}},
			IsError: true,
		}, nil
	}

	// Verify gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: "The 'gh' CLI is not installed or not on PATH. Install it from https://cli.github.com/",
			}},
			IsError: true,
		}, nil
	}

	// Verify we're in a git repo
	if err := runGitCmd(ctx.CWD, "rev-parse", "--git-dir"); err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: "Not inside a git repository.",
			}},
			IsError: true,
		}, nil
	}

	// Get current branch
	currentBranch, err := gitOutput(ctx.CWD, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get current branch: %s", err),
			}},
			IsError: true,
		}, nil
	}

	if currentBranch == "main" || currentBranch == "master" {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Refusing to create a PR from '%s'. Create a feature branch first.", currentBranch),
			}},
			IsError: true,
		}, nil
	}

	// Determine base branch for diff context
	base := baseBranch
	if base == "" {
		base = detectDefaultBranch(ctx.CWD)
	}

	// Get full diff against base for validation context
	diffStat, _ := gitOutput(ctx.CWD, "diff", "--stat", base+"...HEAD")
	if diffStat == "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("No changes detected between '%s' and current branch '%s'. Nothing to create a PR for.", base, currentBranch),
			}},
			IsError: true,
		}, nil
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
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to create PR: %s", strings.TrimSpace(errMsg)),
			}},
			IsError: true,
		}, nil
	}

	prURL := strings.TrimSpace(stdout.String())

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Draft PR created: %s\n\n", prURL))
	result.WriteString(fmt.Sprintf("Branch: %s -> %s\n\n", currentBranch, base))
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

// detectDefaultBranch figures out the repo's default branch.
func detectDefaultBranch(cwd string) string {
	// Try gh first — it knows the remote default
	if branch, err := ghOutput(cwd, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name"); err == nil && branch != "" {
		return branch
	}

	// Fallback: check common names
	for _, candidate := range []string{"main", "master"} {
		if runGitCmd(cwd, "rev-parse", "--verify", candidate) == nil {
			return candidate
		}
	}

	return "main"
}

// runGitCmd runs a git command and returns an error if it fails.
func runGitCmd(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	return cmd.Run()
}

// gitOutput runs a git command and returns trimmed stdout.
func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ghOutput runs a gh command and returns trimmed stdout.
func ghOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
