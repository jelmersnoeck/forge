---
id: session-naming
status: draft
---
# Generate readable session names via Haiku

## Description
When a session starts and the first prompt is sent, use Anthropic Haiku to
generate a short human-readable session title from the prompt. This title
replaces the timestamp-based `cli-YYYYMMDD-HHMMSS` format for the session ID,
git branch name, and worktree directory. If the Haiku call fails or exceeds a
timeout, fall back to a generated readable name (adjective-noun).

## Context
- `cmd/forge/cli.go` — `spawnLocalAgent()` generates session ID, creates worktree/branch
- `cmd/forge/cli.go` — `runCLI()` sets up the model, displays session ID in header
- `cmd/forge/cli.go` — `Init()` sends initialPrompt if present (--spec mode)
- `internal/runtime/provider/anthropic.go` — Anthropic SDK usage
- `internal/tools/websearch.go` — existing pattern for non-streaming Haiku sub-calls

## Behavior
- Session ID format changes from `cli-YYYYMMDD-HHMMSS` to a readable slug
  like `fix-auth-timeout` or `add-mcp-support`.
- When an initial prompt is known at spawn time (--spec mode), the Haiku call
  happens **before** creating the worktree/branch so they get the readable name
  directly.
- When no initial prompt is available (interactive mode), use a fallback
  readable name (adjective-noun combo, e.g. `swift-falcon`) generated locally.
  After the first user message, async Haiku call generates a title that updates
  the CLI display header (but does NOT rename the branch/worktree — too risky).
- Haiku call has a 3-second timeout. On failure/timeout, use fallback name.
- The prompt to Haiku: "Generate a 2-4 word kebab-case slug summarizing this
  task. Reply with ONLY the slug, nothing else." + truncated user prompt.
- Session ID is prefixed with date for uniqueness: `YYYYMMDD-slug`
  (e.g. `20260406-fix-auth-timeout`).
- Branch name remains `jelmer/<sessionID>`.

## Constraints
- No new dependencies.
- Haiku call must not block session startup for more than 3 seconds.
- Fallback name generation must work offline (no API call).
- Do not rename git branches or move worktree directories after creation.
- Keep the Anthropic SDK usage pattern consistent with `websearch.go`.

## Interfaces
```go
// generateSessionName calls Haiku to create a readable slug from the prompt.
// Returns fallback name on error or timeout.
func generateSessionName(prompt string) string

// fallbackSessionName generates a random readable name without API calls.
func fallbackSessionName() string
```

## Edge Cases
- Empty prompt → use fallback name.
- Haiku returns multi-line or non-slug text → sanitize to kebab-case, truncate.
- Haiku returns empty string → use fallback name.
- No ANTHROPIC_API_KEY set → use fallback name (don't crash).
- Network timeout → use fallback name within 3s.
- Very long prompt → truncate to ~200 chars before sending to Haiku.
- Concurrent sessions started in same second → date prefix + unique slug
  makes collisions unlikely; if slug collides, append short random suffix.
