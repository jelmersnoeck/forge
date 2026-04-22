package phase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// PRResult holds the output of deterministic PR creation.
type PRResult struct {
	URL   string // GitHub PR URL (empty on failure)
	Title string // generated title
	Body  string // generated description
	Error error  // nil on success
}

// prGenerationTimeout is the per-attempt timeout for title/body generation.
const prGenerationTimeout = 15 * time.Second

// prGenerationSystemPrompt instructs the LLM to produce a JSON PR title+body.
const prGenerationSystemPrompt = `You generate pull request titles and descriptions from git diffs and commit logs.

Rules:
- Title: imperative mood ("Add X", "Fix Y"), 15-80 chars, captures overall intent.
- Description: 2-4 paragraphs explaining what changed, why, and notable details.
  Do NOT just list commit messages as bullets — synthesize into prose.
- If a spec is provided, reference its goals and how the implementation addresses them.

Respond with ONLY a JSON object:
{"title": "...", "body": "..."}`

// maxDiffLen caps the diff sent to the LLM for PR generation.
// ~8000 chars keeps us within a reasonable token budget for Haiku.
const maxDiffLen = 8000

// CreatePR performs the full deterministic PR creation workflow:
//
//	preconditions → fetch/rebase/push → LLM-generate title+body → gh pr create
//
// Deprecated: Use EnsurePR instead, which handles both creation and update.
func CreatePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult {
	return createNewPR(ctx, prov, cwd, specPath)
}

// createNewPR is the internal implementation for creating a new PR.
func createNewPR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult {
	// Check preconditions.
	ok, reason := shouldCreatePR(cwd)
	if !ok {
		return PRResult{Error: fmt.Errorf("skipped: %s", reason)}
	}

	// Determine base branch.
	base := detectDefaultBranchSafe(cwd)

	// Fetch latest base branch.
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "fetch", "origin", base); err != nil {
		return PRResult{Error: fmt.Errorf("fetch origin/%s: %s", base, stderr)}
	}

	// Rebase onto latest base.
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "rebase", "origin/"+base); err != nil {
		_ = tools.RunGitCmd(cwd, "rebase", "--abort")
		return PRResult{Error: fmt.Errorf("rebase onto origin/%s failed: %s", base, stderr)}
	}

	// Verify there are changes after rebase.
	diffStat, _ := tools.GitOutputCtx(ctx, cwd, "diff", "--stat", "origin/"+base+"...HEAD")
	if diffStat == "" {
		return PRResult{Error: fmt.Errorf("no changes after rebase")}
	}

	// Push with --force-with-lease.
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "push", "--force-with-lease", "origin", "HEAD"); err != nil {
		return PRResult{Error: fmt.Errorf("push failed: %s", stderr)}
	}

	// Gather context for title/body generation.
	diff, _ := tools.GitOutputCtx(ctx, cwd, "diff", "origin/"+base+"...HEAD")
	commitLog, _ := tools.GitOutputCtx(ctx, cwd, "log", "origin/"+base+"..HEAD", "--oneline")
	branch, _ := tools.GitOutputCtx(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")

	var specContent string
	if specPath != "" {
		if data, err := os.ReadFile(specPath); err == nil {
			specContent = string(data)
		}
	}

	// Generate title and body via LLM.
	title, body, err := generatePRContent(ctx, prov, diff, commitLog, specContent)
	if err != nil {
		log.Printf("[pr] LLM generation failed, using fallback: %v", err)
		title, body = fallbackPRContent(branch, commitLog, diffStat, specContent)
	}

	// Validate and retry once if needed.
	if err := validatePRTitle(title); err != nil {
		log.Printf("[pr] generated title invalid (%v), retrying", err)
		title2, body2, err2 := generatePRContent(ctx, prov, diff, commitLog, specContent)
		if err2 == nil && validatePRTitle(title2) == nil {
			title, body = title2, body2
		} else {
			title, body = fallbackPRContent(branch, commitLog, diffStat, specContent)
		}
	}
	if err := validatePRDescription(body); err != nil {
		log.Printf("[pr] generated description invalid (%v), using fallback", err)
		_, body = fallbackPRContent(branch, commitLog, diffStat, specContent)
	}

	// Create the PR via gh.
	prURL, err := ghCreatePR(ctx, cwd, title, body, "")
	if err != nil {
		return PRResult{Title: title, Body: body, Error: fmt.Errorf("gh pr create: %w", err)}
	}

	return PRResult{URL: prURL, Title: title, Body: body}
}

// generatePRContent uses a cheap LLM call to produce a PR title and description.
func generatePRContent(ctx context.Context, prov types.LLMProvider, diff, commitLog, specContent string) (string, string, error) {
	// Truncate diff to keep token count reasonable.
	truncatedDiff := diff
	if len(truncatedDiff) > maxDiffLen {
		truncatedDiff = truncatedDiff[:maxDiffLen] + "\n... [truncated]"
	}

	var prompt strings.Builder
	prompt.WriteString("Generate a PR title and description for these changes.\n\n")
	prompt.WriteString("## Commit log\n```\n")
	prompt.WriteString(commitLog)
	prompt.WriteString("\n```\n\n")
	prompt.WriteString("## Diff\n```\n")
	prompt.WriteString(truncatedDiff)
	prompt.WriteString("\n```\n")

	if specContent != "" {
		prompt.WriteString("\n## Spec\n```\n")
		prompt.WriteString(specContent)
		prompt.WriteString("\n```\n")
	}

	var lastErr error
	for _, model := range classificationModels {
		title, body, err := generateWithModel(ctx, prov, model, prompt.String())
		if err == nil {
			return title, body, nil
		}
		lastErr = err
		log.Printf("[pr] model %s failed: %v — trying next", model, err)
	}

	return "", "", fmt.Errorf("all models failed: %w", lastErr)
}

// generateWithModel runs a single PR generation attempt against a specific model.
func generateWithModel(ctx context.Context, prov types.LLMProvider, model, prompt string) (string, string, error) {
	genCtx, cancel := context.WithTimeout(ctx, prGenerationTimeout)
	defer cancel()

	req := types.ChatRequest{
		Model: model,
		System: []types.SystemBlock{
			{Type: "text", Text: prGenerationSystemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: prompt},
				},
			},
		},
		MaxTokens: 1024,
		Stream:    true,
	}

	deltaChan, err := prov.Chat(genCtx, req)
	if err != nil {
		return "", "", fmt.Errorf("provider.Chat: %w", err)
	}

	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return "", "", fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	return parsePRContent(text.String())
}

// parsePRContent extracts title and body from the LLM's JSON response.
func parsePRContent(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
			raw = strings.TrimSpace(raw)
		}
	}

	var result struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", "", fmt.Errorf("parse error: %w — raw: %q", err, truncateForLog(raw, 200))
	}

	if result.Title == "" {
		return "", "", fmt.Errorf("empty title in response")
	}
	if result.Body == "" {
		return "", "", fmt.Errorf("empty body in response")
	}

	return result.Title, result.Body, nil
}

// fallbackPRContent generates a deterministic title and body when LLM fails.
func fallbackPRContent(branch, commitLog, diffStat, specContent string) (string, string) {
	// Title from branch name: "jelmer/add-paintball" → "Add paintball"
	title := branchToTitle(branch)

	var body strings.Builder
	if specContent != "" {
		// Extract first heading from spec.
		for _, line := range strings.Split(specContent, "\n") {
			if strings.HasPrefix(line, "# ") {
				body.WriteString(strings.TrimPrefix(line, "# "))
				body.WriteString("\n\n")
				break
			}
		}
	}

	body.WriteString("## Changes\n\n")
	body.WriteString("```\n")
	body.WriteString(diffStat)
	body.WriteString("\n```\n\n")

	if commitLog != "" {
		body.WriteString("## Commits\n\n")
		for _, line := range strings.Split(commitLog, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				body.WriteString("- " + line + "\n")
			}
		}
	}

	return title, body.String()
}

// branchToTitle converts a branch name to a PR title.
// "jelmer/add-cool-feature" → "Add cool feature"
func branchToTitle(branch string) string {
	// Strip prefix up to last /.
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branch = branch[idx+1:]
	}

	// Replace hyphens and underscores with spaces.
	branch = strings.NewReplacer("-", " ", "_", " ").Replace(branch)
	branch = strings.TrimSpace(branch)

	if branch == "" {
		return "Update implementation"
	}

	// Capitalize first letter.
	runes := []rune(branch)
	if len(runes) > 0 && runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] = runes[0] - 32
	}

	return string(runes)
}

// ghCreatePR calls `gh pr create --draft` and returns the PR URL.
func ghCreatePR(ctx context.Context, cwd, title, body, baseBranch string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("gh CLI not installed (https://cli.github.com/)")
	}

	args := []string{"pr", "create",
		"--draft",
		"--title", title,
		"--body", body,
	}
	if baseBranch != "" {
		args = append(args, "--base", baseBranch)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// validatePRTitle checks that a title meets quality standards.
func validatePRTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is required")
	}

	if len(title) < 15 {
		return fmt.Errorf("title too short (%d chars)", len(title))
	}

	lower := strings.ToLower(title)
	for _, generic := range genericTitles {
		if lower == generic {
			return fmt.Errorf("title too generic: %q", title)
		}
	}

	return nil
}

// validatePRDescription checks that a description meets quality standards.
func validatePRDescription(body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("description is required")
	}
	if len(body) < 50 {
		return fmt.Errorf("description too short (%d chars)", len(body))
	}
	return nil
}

// genericTitles are low-effort PR titles that get rejected.
var genericTitles = []string{
	"fix", "fix bug", "update", "update code", "changes",
	"wip", "stuff", "misc", "pr", "pull request",
	"refactor", "cleanup", "clean up", "minor changes",
	"quick fix", "hotfix", "patch", "test", "tests",
}

// truncateForLog truncates a string for log output.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// EnsurePR creates a new PR or updates an existing one.
// Returns the PR URL on success. Non-fatal: errors are logged, not propagated.
//
//	┌────────────────┐
//	│ preconditions   │ git repo? feature branch? has changes?
//	└───────┬────────┘
//	        │
//	        ▼
//	┌────────────────┐
//	│ existingPRURL  │ gh pr view → URL or ""
//	└───────┬────────┘
//	        │
//	   ┌────┴────┐
//	   │         │
//	   ▼         ▼
//	 no PR    has PR
//	   │         │
//	   ▼         ▼
//	 createNewPR  push --force-with-lease
//	   │         │
//	   ▼         ▼
//	 PRResult  PRResult{URL: existing}
func EnsurePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult {
	// Bail if context is already cancelled.
	if ctx.Err() != nil {
		return PRResult{Error: fmt.Errorf("skipped: %w", ctx.Err())}
	}

	// Check preconditions.
	ok, reason := shouldCreatePR(cwd)
	if !ok {
		return PRResult{Error: fmt.Errorf("skipped: %s", reason)}
	}

	// Check for existing PR.
	existing := existingPRURL(ctx, cwd)

	switch existing {
	case "":
		// No existing PR — create one via the full workflow.
		return createNewPR(ctx, prov, cwd, specPath)

	default:
		// PR exists — push any new commits and return existing URL.
		return ensureExistingPR(ctx, cwd, existing)
	}
}

// PROperationError describes a specific git/gh operation failure during
// ensureExistingPR, providing actionable context for debugging.
type PROperationError struct {
	Operation string // e.g., "fetch", "rebase", "push"
	Stderr    string // raw stderr from the command
	Err       error  // underlying error
}

func (e *PROperationError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s failed: %s (%v)", e.Operation, e.Stderr, e.Err)
	}
	return fmt.Sprintf("%s failed: %v", e.Operation, e.Err)
}

func (e *PROperationError) Unwrap() error { return e.Err }

// ensureExistingPR pushes new commits to an existing PR.
// Skips push if there's nothing new to push. Returns structured errors
// for operation failures to aid production debugging.
func ensureExistingPR(ctx context.Context, cwd, prURL string) PRResult {
	// Verify we're in a git repository.
	if err := tools.RunGitCmd(cwd, "rev-parse", "--git-dir"); err != nil {
		return PRResult{Error: fmt.Errorf("not a git repository: %w", err)}
	}

	base := detectDefaultBranchSafe(cwd)

	// Fetch + rebase (best-effort — if it fails, still try to push with existing state).
	var opErrors []*PROperationError
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "fetch", "origin", base); err != nil {
		opErr := &PROperationError{Operation: "fetch origin/" + base, Stderr: stderr, Err: err}
		opErrors = append(opErrors, opErr)
		log.Printf("[pr] %v", opErr)
	} else if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "rebase", "origin/"+base); err != nil {
		opErr := &PROperationError{Operation: "rebase origin/" + base, Stderr: stderr, Err: err}
		opErrors = append(opErrors, opErr)
		log.Printf("[pr] %v", opErr)
		_ = tools.RunGitCmd(cwd, "rebase", "--abort")
	}

	// Check if we have unpushed commits.
	branch, err := tools.GitOutputCtx(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return PRResult{URL: prURL, Error: fmt.Errorf("cannot determine branch: %w", err)}
	}

	if !tools.ValidateBranchName(branch) {
		return PRResult{URL: prURL, Error: fmt.Errorf("invalid branch name %q", branch)}
	}

	localSHA, _ := tools.GitOutputCtx(ctx, cwd, "rev-parse", "HEAD")
	remoteSHA, _ := tools.GitOutputCtx(ctx, cwd, "rev-parse", "origin/"+branch)

	// Only push if local and remote differ AND there are actual commits to push.
	if localSHA != "" && localSHA != remoteSHA {
		unpushed, _ := tools.GitOutputCtx(ctx, cwd, "rev-list", "--count", "origin/"+branch+"..HEAD")
		if unpushed != "" && unpushed != "0" {
			if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "push", "--force-with-lease", "origin", "HEAD"); err != nil {
				opErr := &PROperationError{
					Operation: "push --force-with-lease",
					Stderr:    stderr,
					Err:       err,
				}
				opErrors = append(opErrors, opErr)
				log.Printf("[pr] %v (check auth, permissions, network)", opErr)
			}
		}
	}

	// If all operations failed, return the combined error for visibility.
	if len(opErrors) > 0 {
		var msgs []string
		for _, e := range opErrors {
			msgs = append(msgs, e.Error())
		}
		log.Printf("[pr] ensureExistingPR completed with %d operation error(s): %s",
			len(opErrors), strings.Join(msgs, "; "))
	}

	return PRResult{URL: prURL}
}

// existingPRURL checks if a PR already exists for the current branch.
// Returns the PR URL or "" if none exists. Respects context cancellation.
func existingPRURL(ctx context.Context, cwd string) string {
	// Try with --jq first (modern gh), fall back to raw JSON parse.
	out, err := tools.GHOutputCtx(ctx, cwd, "pr", "view", "--json", "url", "--jq", ".url")
	if err != nil {
		return ""
	}
	result := strings.TrimSpace(out)

	// Validate the output looks like a URL to avoid injection.
	if result == "" {
		return ""
	}
	parsed, err := url.Parse(result)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		log.Printf("[pr] existingPRURL got invalid URL %q, ignoring", result)
		return ""
	}
	return result
}
