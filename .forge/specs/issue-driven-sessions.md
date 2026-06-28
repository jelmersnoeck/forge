---
id: issue-driven-sessions
status: implemented
---
# Start sessions from GitHub issue URL or reference

## Description
Add an `--issue` flag that accepts a GitHub issue URL or `#N` shorthand, fetches
the issue content via `gh`, and uses it as the initial prompt — exactly like
`--spec` does for spec files. This is the first issue-tracker integration; the
architecture should leave room for Linear and others later, but only GitHub is
implemented now.

This spec was extended to ensure that when `--issue` is used:
1. The issue number is included in the branch name for traceability.
2. The issue URL is linked in the PR description so GitHub auto-closes the issue.

## Context
- `cmd/forge/main.go` — help text, subcommand routing
- `cmd/forge/cli.go` — `runCLI()` flag parsing, `initialPrompt` construction,
  `spawnLocalAgent()` call (added `namingHint` parameter); worktree/branch
  creation in `spawnLocalAgent()`
- `cmd/forge/agent.go` — agent subcommand flag parsing, `Config` construction
- `cmd/forge/issue.go` — `fetchGitHubIssue()`, `formatIssuePrompt()`,
  `normalizeIssueRef()`, `extractIssueNumber()`, and `gh` JSON types
- `cmd/forge/issue_test.go` — tests for formatting, ref normalization, and
  issue number extraction
- `cmd/forge/session_name.go` — `generateSessionName()` (prompt → slug)
- `internal/agent/server.go` — `Config` struct, `Start()` entry point
- `internal/agent/worker.go` — `NewWorker()`, `ensurePR()` — passes issue ref
  through to `EnsurePR`
- `internal/agent/phase/pr.go` — `EnsurePR()`, `createNewPR()`,
  `generatePRContent()`, `fallbackPRContent()`, `PRAttributionOpts` — accepts
  and appends issue reference to PR body

## Behavior
- `forge --issue https://github.com/owner/repo/issues/42` fetches issue #42 and
  starts a session with its contents as the initial prompt.
- `forge --issue #42` (or `forge --issue 42`) resolves against the current repo
  via `gh issue view 42`.
- The fetched content includes: title, body, number, and comments (via
  `gh issue view <ref> --json title,body,url,number,comments`).
- The initial prompt sent to the agent is formatted as:
  ```
  Implement the following GitHub issue.

  Issue: <url>

  # <title>

  <body>

  ## Comments

  **@<author>** (<created_at>):
  <comment_body>

  ...
  ```
- If `--issue` and `--spec` are both provided, exit with error:
  `"cannot use --issue with --spec"`.
- If `--issue` and `--mode spec` are both provided, exit with error:
  `"cannot use --issue with --mode spec"`.
- If `gh` is not installed or not on PATH, exit with error:
  `"--issue requires the GitHub CLI (gh) — install from https://cli.github.com"`.
- If `gh issue view` fails (not authenticated, issue not found, not in a repo),
  surface the `gh` stderr as the error message.
- The session name is generated from the issue title (not the full body), keeping
  the slug short and descriptive.
- The `--issue` flag value is displayed in the TUI welcome banner alongside the
  session info, similar to how `--spec` shows the spec path.

### Branch naming with issue number
- When `--issue` is used, the worktree branch name includes the issue number
  as a prefix in the slug portion: `jelmer/<date>-<issueN>-<slug>`.
  Example: `forge --issue #42` with title "Fix auth timeout" →
  branch `jelmer/20260628-42-fix-auth-timeout`.
- The issue number is extracted from the `gh issue view` JSON response (`number`
  field) or parsed from the URL/ref when the number field is unavailable.
- When using a full URL (e.g., `https://github.com/o/r/issues/42`), the number
  is extracted from the URL path.

### Issue linking in PR description
- When a PR is created for an issue-driven session, the PR body includes a
  `Closes <issue_url>` line appended after the LLM-generated (or fallback)
  description, before the attribution block.
- The full issue URL is used (not just `#N`) so it works for cross-repo
  references and is unambiguous.
- When the PR already exists (update path in `ensureExistingPR`), no body
  modification occurs — the link was set at creation time.
- The issue URL is passed from CLI → agent config → worker → `EnsurePR` →
  `createNewPR` via `PRAttributionOpts.IssueURL`.

## Constraints
- Must not add any new Go dependencies.
- Must not call the GitHub API directly — use `gh` CLI exclusively (handles auth,
  rate limits, enterprise hosts).
- Must not block session startup if `gh` is slow — the `gh issue view` call
  happens before agent spawn (same timing as `os.ReadFile` for `--spec`), so
  acceptable latency is whatever `gh` takes.
- Comments section is omitted entirely when the issue has zero comments (no empty
  `## Comments` header).
- The `Closes` line must use the full URL, not `#N`, to avoid ambiguity in
  forks or cross-repo references.
- The issue number in the branch name must be the raw number (no `#` prefix)
  since `#` is not valid in git branch names.

## Interfaces

```go
// cmd/forge/issue.go — updated struct to include Number
type ghIssue struct {
    Title    string      `json:"title"`
    Body     string      `json:"body"`
    URL      string      `json:"url"`
    Number   int         `json:"number"`
    Comments []ghComment `json:"comments"`
}

// cmd/forge/issue.go — updated return signature to include issue number and URL
func fetchGitHubIssue(ref string, cwd string) (prompt string, title string, issueNum int, issueURL string, err error)

// cmd/forge/issue.go — extract issue number from URL or ref string (fallback)
func extractIssueNumber(ref string) int
```

```go
// internal/agent/server.go — Config gains IssueURL field
type Config struct {
    // ... existing fields ...
    IssueURL string // GitHub issue URL for PR linking (from --issue)
}
```

```go
// internal/agent/phase/pr.go — PRAttributionOpts gains IssueURL field
type PRAttributionOpts struct {
    SessionID string
    CoAuthor  string
    Enabled   bool
    IssueURL  string // when set, appends "Closes <url>" to PR body
}
```

```go
// cmd/forge/cli.go — spawnLocalAgent signature (extended with issueNum, issueURL)
func spawnLocalAgent(cwd string, skipWorktree bool, branchName string,
    initialPrompt string, mode string, specPath string, modelName string,
    namingHint string, issueNum int, issueURL string) (string, string, string, string, func(), error)
```

## Edge Cases
- **Issue number without `#`**: `forge --issue 42` works the same as
  `forge --issue #42` — strip leading `#` before passing to `gh`.
- **Full URL with fragment/query**: `forge --issue https://github.com/o/r/issues/42#comment-123`
  — pass the URL as-is to `gh issue view`, which handles URL parsing.
- **Issue with empty body**: Title is present but body is empty string — skip the
  body section, don't render an empty line.
- **Issue with very long body (>100KB)**: No truncation — pass the full body. The
  LLM provider handles context window limits. Session naming only uses the title.
- **Not in a git repo + `--issue 42`**: `gh issue view` fails because it can't
  determine the repo — surface the `gh` error.
- **`gh` auth expired**: `gh issue view` exits non-zero — surface the stderr.
- **Issue number 0 or missing**: If `gh` somehow returns `number: 0`, omit the
  issue number from the branch name entirely (fall back to slug-only behavior).
- **Existing PR body already contains `Closes`**: No deduplication needed because
  `createNewPR` only runs when no PR exists. The `ensureExistingPR` path does
  not modify the body.
- **`--issue` with `--branch`**: When `--branch` is explicitly set, the user's
  branch name takes precedence. The issue number is not injected into an
  explicitly-provided branch name. The issue URL is still linked in the PR body.
- **Cross-repo issue URL**: `forge --issue https://github.com/other-org/other-repo/issues/99`
  — the full URL is used in `Closes`, which GitHub resolves correctly for
  cross-repo references.
