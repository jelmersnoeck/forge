---
id: create-pr-tool
status: implemented
---
# PRCreate tool: fetch/rebase, push, and reject lazy descriptions

## Description
The PRCreate tool handler was a thin wrapper around `gh pr create`. It trusted
the agent to fetch, rebase, push, and write good descriptions. With multiple
commits the agent (and the old `hp` shell alias) would just list commit messages
as bullets. The tool now enforces the full workflow and validates description
quality.

## Context
- `internal/tools/pr_create.go` — handler, validators, git helpers
- `internal/tools/pr_create_test.go` — all test cases
- `~/CLAUDE.md` — updated to reference `create-pr` skill instead of `hp` (user-level, not in repo)

## Behavior
- Tool fetches `origin/<base>` and rebases current branch onto it before creating the PR.
- On rebase conflict, the tool aborts the rebase and returns an error. Repo is not left in a broken state.
- Tool pushes with `--force-with-lease` after successful rebase. Agent does not need a separate push step.
- Tool compares PR description lines against `git log --oneline` output. If >50% of non-empty lines (after stripping `- `, `* `, `•` prefixes) match commit subjects, the description is rejected.
- Single-commit PRs skip the commit-list check (a description matching one commit is fine).
- Diff stat is computed against `origin/<base>...HEAD` (not local base ref).
- Tool description includes a numbered pre-call checklist instructing the LLM to read the full diff, understand the combined purpose, and synthesize a proper title and description.

## Constraints
- Do not mock git or gh in tests — use real repos with bare remotes.
- Do not add external dependencies.
- Rebase abort must always run if rebase fails — no half-rebased state.
- The commit-list detector must not reject descriptions that merely share some words with commits — only exact subject-line matches count.

## Interfaces
```go
// Added to pr_create.go:

func gitOutputFull(cwd string, args ...string) (stdout, stderr string, err error)

func detectCommitListDescription(description, commitLog string) error

func toolError(format string, args ...any) types.ToolResult

// Test helper:
func initGitRepoWithRemote(t *testing.T, branch string) (remote, local string)
```

## Edge Cases
- No remote configured: fetch fails with a clear error message including stderr.
- Rebase conflict: rebase is aborted, branch stays on original HEAD, error returned.
- No changes after rebase: returns "No changes detected" error.
- Single commit: commit-list detection is skipped entirely.
- Description with partial overlap (some lines match commits, most don't): accepted (threshold is >50%).
- Description uses `*` or `•` as list prefixes: prefixes are stripped before comparison.
- Push rejected (e.g. protected branch): error from git push is surfaced.
