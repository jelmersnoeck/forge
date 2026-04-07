---
id: session-naming
status: implemented
---
# Generate readable session names via Haiku

## Description
When a session starts, session IDs use human-readable names instead of
timestamp-based formats. If an initial prompt is available (--spec mode), Haiku
generates a kebab-case slug. Otherwise, a random adjective-noun pair is used.
In interactive mode, the first user message triggers an async Haiku call that
updates the CLI header display.

## Context
- `cmd/forge/cli.go` — `spawnLocalAgent()` generates session ID, creates worktree/branch; `runCLI()` sets up model, displays header; `Init()` sends initialPrompt; spec-reading moved before spawn; `sessionTitleMsg` type for async updates; `generateTitle()` method
- `cmd/forge/session_name.go` — `generateSessionName()`, `fallbackSessionName()`, `sanitizeSlug()`, `extractSlug()`
- `cmd/forge/session_name_test.go` — tests for sanitizeSlug, fallbackSessionName, generateSessionName edge cases

## Behavior
- Session ID format: `YYYYMMDD-slug` (e.g. `20260406-fix-auth-timeout` or `20260406-swift-falcon`).
- When --spec is provided, the spec content is read **before** spawning the agent, and Haiku generates a slug from it. The worktree and branch get the readable name directly.
- When no initial prompt is available (interactive mode), `fallbackSessionName()` generates a random adjective-noun pair (e.g. `swift-falcon`).
- After the first user-typed message in interactive mode, an async Haiku call generates a title that updates the CLI header display (but does NOT rename the branch/worktree).
- Haiku call uses `claude-haiku-4-5` with a 3-second timeout and `MaxTokens: 32`.
- Prompt to Haiku: "Generate a 2-4 word kebab-case slug summarizing this task. Reply with ONLY the slug, nothing else." + truncated user prompt (max 200 chars).
- Branch name: `jelmer/<sessionID>` (e.g. `jelmer/20260406-fix-auth-timeout`).

## Constraints
- No new dependencies (uses existing `anthropic-sdk-go`).
- Haiku call must not block session startup for more than 3 seconds.
- Fallback name generation works offline (no API call).
- Git branches and worktree directories are never renamed after creation.
- Consistent with existing Anthropic SDK usage in `websearch.go`.

## Interfaces
```go
// generateSessionName calls Haiku to create a readable slug from the prompt.
// Returns fallback name on error or timeout.
func generateSessionName(prompt string) string

// fallbackSessionName generates a random readable name without API calls.
// Format: adjective-noun (e.g. "swift-falcon").
func fallbackSessionName() string

// sanitizeSlug normalizes arbitrary text into a valid kebab-case slug.
func sanitizeSlug(s string) string

// extractSlug pulls a clean kebab-case slug from a Haiku response.
func extractSlug(msg *anthropic.Message) string

// sessionTitleMsg is a BubbleTea message for async session title updates.
type sessionTitleMsg string

// model.generateTitle fires an async Haiku call for the first user message.
func (m model) generateTitle(prompt string) tea.Cmd
```

## Edge Cases
- Empty prompt → fallback adjective-noun name, no API call.
- Haiku returns multi-line, backtick-wrapped, or non-slug text → `sanitizeSlug` normalizes to kebab-case, collapses dashes, truncates to 40 chars.
- Haiku returns empty string → fallback name.
- No `ANTHROPIC_API_KEY` set → fallback name (no crash).
- Network timeout after 3s → fallback name.
- Very long prompt → truncated to 200 chars before sending to Haiku.
- Unicode characters in Haiku response → stripped by `slugRe` regex.
- Spec-reading moved before `spawnLocalAgent()` so prompt is available for naming.
- Interactive mode async title update only changes the header display line (`m.output[0]`), not the session ID or branch.
