---
id: deterministic-pr-creation
status: implemented
---
# Move PR creation from LLM tool to deterministic orchestrator step

## Description
PR creation is currently an LLM tool (`PRCreate`) that the agent decides to
invoke. The finalize step in the SWE orchestrator prompts the LLM to call it.
This is wasteful and unreliable — PR creation is a mechanical step, not a
creative one. Move it to deterministic Go code in the orchestrator, using a
lightweight LLM call only for generating the PR title and description.

## Context
- `internal/agent/phase/orchestrator.go` — `runFinalize` replaced with
  deterministic code that calls `CreatePR` from pr.go
- `internal/agent/phase/pr.go` — NEW: deterministic PR creation (CreatePR,
  generatePRContent, fallbackPRContent, ghCreatePR, validators)
- `internal/agent/phase/prompts.go` — coder prompt step 7 (PR creation) removed
- `internal/tools/pr_create.go` — DELETED: PRCreate tool and its handler
- `internal/tools/pr_create_test.go` — DELETED: tool handler tests
- `internal/tools/git.go` — NEW: extracted git helpers (GitOutput, GitOutputFull,
  RunGitCmd, GHOutput, DetectDefaultBranch)
- `internal/tools/git_test.go` — NEW: shared test helpers for git operations
- `internal/tools/registry.go` — `NewDefaultRegistry()` no longer registers
  PRCreateTool
- `internal/agent/phase/phase.go` — `PRCreate` removed from DisallowedTools in
  all phases
- `internal/agent/phase/phase_test.go` — updated to reflect removed PRCreate
- `internal/agent/phase/orchestrator_test.go` — replaced buildFinalizePrompt
  tests with tests for branchToTitle, fallbackPRContent, parsePRContent,
  validatePRTitle, validatePRDescription
- `cmd/forge/cli.go` — comment updated (prURL source)

## Behavior
- After the review cycle completes in `runSWEPipeline`, the orchestrator runs
  `runFinalize` as deterministic Go code (no LLM loop).
- `runFinalize` performs these steps in order:
  1. Check preconditions (`shouldCreatePR` — existing logic).
  2. Fetch `origin/<base>` and rebase onto it (abort + error on conflict).
  3. Push with `--force-with-lease`.
  4. Compute diff stat and commit log.
  5. Call the LLM provider with a focused prompt to generate a PR title and
     description from the diff, commit log, and spec content. Use Haiku (cheap,
     fast model) — same pattern as session title generation.
  6. Validate the generated title/description (reuse existing validators).
  7. Call `gh pr create --draft` with the generated title/description.
  8. Emit a `pr_url` event with the PR URL.
- The `PRCreate` tool is removed from the tool registry — the LLM can no longer
  call it and no prompt instructs it to.
- The coder phase prompt no longer mentions PR creation (step 7 removed).
- `DisallowedTools` lists in phases no longer reference `PRCreate`.
- Failures in `runFinalize` are non-fatal (logged, pipeline continues) — same
  as today.

## Constraints
- Do not remove the git helper functions (`GitOutput`, `GitOutputFull`,
  `DetectDefaultBranch`, `RunGitCmd`, `GHOutput`) — they are useful utilities.
  Move them if needed, but keep them exported.
- The LLM call for title/description must use a cheap model (Haiku-class), not
  the main conversation model.
- Do not fail the pipeline if `gh` is not installed, not authenticated, or PR
  creation fails for any reason.
- Do not create a PR if there are no changes or we're on main/master — same
  preconditions as today.
- Keep the existing validation logic (title length, generic titles, description
  length, commit-list detection) but apply it as a sanity check on LLM output,
  retrying once if validation fails.

## Interfaces
```go
// internal/agent/phase/pr.go

// PRResult holds the output of deterministic PR creation.
type PRResult struct {
    URL             string              // GitHub PR URL (empty on failure)
    Title           string              // generated title
    Body            string              // generated description
    Error           error               // nil on success
    OperationErrors []*PROperationError // non-fatal operation failures during ensure
}

// PROperationError describes a specific git/gh operation failure during
// ensureExistingPR, providing actionable context for debugging.
// Stderr is sanitized to strip credentials/tokens before inclusion in Error().
type PROperationError struct {
    Operation string // e.g., "fetch", "rebase", "push"
    Stderr    string // raw stderr from the command
    Err       error  // underlying error
}

// CreatePR performs the full PR creation workflow deterministically.
// Deprecated: Use EnsurePR instead, which handles both creation and update.
func CreatePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult

// EnsurePR creates a new PR or updates an existing one.
// Returns the PR URL on success. Errors are logged, not propagated.
func EnsurePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult

// generatePRContent uses a cheap LLM call to produce a title and description.
func generatePRContent(ctx context.Context, prov types.LLMProvider, diff, commitLog, specContent string) (title, body string, err error)

// branchToTitle converts a branch name to a PR title.
func branchToTitle(branch string) string

// fallbackPRContent generates deterministic title and body when LLM fails.
func fallbackPRContent(branch, commitLog, diffStat, specContent string) (title, body string)

// ghCreatePR calls gh pr create --draft and returns the PR URL.
func ghCreatePR(ctx context.Context, cwd, title, body, baseBranch string) (string, error)

// hasUnpushedCommits reports whether the local branch has commits not yet
// pushed to origin.
func hasUnpushedCommits(ctx context.Context, cwd, branch string) bool

// isValidPRURL checks that a string is an HTTP(S) URL pointing to a
// pull request or merge request on a known forge (GitHub, GitLab, Bitbucket, Azure DevOps).
func isValidPRURL(raw string) bool
```

```go
// internal/tools/git.go (extracted from pr_create.go)

func DetectDefaultBranch(cwd string) string
func RunGitCmd(cwd string, args ...string) error
func GitOutput(cwd string, args ...string) (string, error)
func GitOutputFull(cwd string, args ...string) (string, string, error)
func GHOutput(cwd string, args ...string) (string, error)

// ValidateBranchName checks that a git branch name is safe to use in commands.
// Rejects shell metacharacters, argument injection (-prefix), path traversal
// (.. components), and git-forbidden patterns (dot-prefixed components).
func ValidateBranchName(branch string) bool
```

## Edge Cases
- `gh` not installed: `runFinalize` returns error, pipeline continues, no PR
  created. Logged as non-fatal.
- Rebase conflict: rebase aborted, error returned. Pipeline continues without PR.
- LLM generates a bad title (too short, generic): retry once with feedback.
  If still bad after retry, use a deterministic fallback title derived from the
  branch name.
- LLM call fails entirely (API error, timeout): use deterministic fallback
  title/description from spec summary + commit log.
- Spec content unreadable: generate PR content from diff + commit log only.
- PR already exists for branch: EnsurePR detects it via `gh pr view`, pushes
  any new commits, and returns the existing URL. Operation errors are captured
  in PRResult.OperationErrors for debugging.
- No ANTHROPIC_API_KEY for Haiku call: use the existing provider from opts
  (it already has auth); fall back to deterministic content if provider fails.
- Context cancelled mid-finalize: standard cancellation — abort and return.
  ensurePR in the worker detects pre-cancelled context and skips immediately.
- Path traversal in branch names: ValidateBranchName rejects `..` components,
  dot-prefixed segments, and trailing `..` patterns matching git's ref-format rules.
- Sensitive stderr: PROperationError.Error() sanitizes stderr to strip GitHub
  tokens (gho_, ghp_, ghs_, github_pat_), bearer tokens, and basic auth in URLs.
- Non-PR URLs from gh output: existingPRURL validates the URL matches known
  PR/MR path patterns (GitHub /pull/N, GitLab /merge_requests/N, etc.).
