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

## Context
- `cmd/forge/main.go` — help text, subcommand routing
- `cmd/forge/cli.go` — `runCLI()` flag parsing, `initialPrompt` construction,
  `spawnLocalAgent()` call (added `namingHint` parameter)
- `cmd/forge/issue.go` — new file: `fetchGitHubIssue()`, `formatIssuePrompt()`,
  `normalizeIssueRef()`, and `gh` JSON types
- `cmd/forge/issue_test.go` — tests for formatting and ref normalization
- `cmd/forge/session_name.go` — `generateSessionName()` (prompt → slug)

## Behavior
- `forge --issue https://github.com/owner/repo/issues/42` fetches issue #42 and
  starts a session with its contents as the initial prompt.
- `forge --issue #42` (or `forge --issue 42`) resolves against the current repo
  via `gh issue view 42`.
- The fetched content includes: title, body, and comments (via
  `gh issue view <ref> --json title,body,url,comments`).
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
- The worktree branch name is derived from the session slug (same as today).

## Constraints
- Must not add any new Go dependencies.
- Must not call the GitHub API directly — use `gh` CLI exclusively (handles auth,
  rate limits, enterprise hosts).
- Must not change the agent-side code (`internal/agent/`). The issue is resolved
  entirely in the CLI; the agent receives a plain text prompt.
- Must not block session startup if `gh` is slow — the `gh issue view` call
  happens before agent spawn (same timing as `os.ReadFile` for `--spec`), so
  acceptable latency is whatever `gh` takes.
- Comments section is omitted entirely when the issue has zero comments (no empty
  `## Comments` header).

## Interfaces

```go
// cmd/forge/cli.go — new flag in runCLI
issue := fs.String("issue", "", "GitHub issue URL or #N to use as initial prompt")

// cmd/forge/issue.go — new function
// fetchGitHubIssue resolves a GitHub issue reference and returns the formatted
// prompt text and the issue title (for session naming). cwd is used to resolve
// relative issue numbers against the current repo.
func fetchGitHubIssue(ref string, cwd string) (prompt string, title string, err error)

// cmd/forge/cli.go — spawnLocalAgent gained a namingHint parameter to allow
// --issue to pass the issue title for short session slugs instead of the full prompt.
func spawnLocalAgent(cwd string, skipWorktree bool, branchName string, initialPrompt string, mode string, specPath string, namingHint string) (string, string, string, string, func(), error)
```

```go
// gh issue view JSON structure (subset we care about)
type ghIssue struct {
    Title    string      `json:"title"`
    Body     string      `json:"body"`
    URL      string      `json:"url"`
    Comments []ghComment `json:"comments"`
}

type ghComment struct {
    Author    ghAuthor `json:"author"`
    Body      string   `json:"body"`
    CreatedAt string   `json:"createdAt"`
}

type ghAuthor struct {
    Login string `json:"login"`
}
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
