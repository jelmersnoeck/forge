---
id: history-integrity-and-interrupt
status: draft
---
# Fix history corruption and interrupt handling

## Description
Two related bugs: (1) Compact can split tool_use/tool_result pairs, causing
400 errors from Anthropic. (2) Ctrl+C sends an interrupt signal but nobody
reads it — the agent loop ignores interrupts entirely. After an API error,
the session enters a death loop with no recovery.

## Context
- `internal/runtime/tokens/tokens.go` — Compact function
- `internal/runtime/loop/loop.go` — runLoop, collectAssistantMessage
- `internal/agent/worker.go` — Worker.Run, interrupt handling
- `internal/agent/hub.go` — TriggerInterrupt, InterruptChannel (dead code)
- `cmd/forge/cli.go` — Ctrl+C handler, sendInterrupt

## Behavior
1. Compact must never split tool_use/tool_result pairs. An assistant message
   with tool_use blocks and its following user message with tool_result blocks
   must always be kept together or removed together.
2. When interrupted, the loop should stop after the current turn (not mid-stream).
   The worker creates a cancel context from the hub's interrupt channel and
   passes it to the loop.
3. After an error or interrupt, the loop should emit "done" so the CLI returns
   to the input prompt and the user can continue the session.
4. History sanitization on Send/Resume: before sending to the API, validate
   that every tool_result has a matching tool_use in the preceding assistant
   message. Drop orphaned tool_results to prevent 400 errors.

## Constraints
- Do not change the Anthropic API contract or types.
- Compact must still respect budget — just ensure pair integrity.
- Interrupt is best-effort; long-running tool execution may delay it.

## Interfaces
```go
// Compact — updated to respect tool_use/tool_result pairing.
// After finding keepFromIdx, adjust it to not split a pair.
func Compact(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int) ([]types.ChatMessage, int)

// sanitizeHistory validates tool_use/tool_result pairing in history.
// Drops orphaned tool_result blocks and fixes up broken pairs.
func sanitizeHistory(history []types.ChatMessage) []types.ChatMessage
```

## Edge Cases
- History ends with an assistant message containing tool_use but no following
  tool_result (interrupted mid-turn) → drop the trailing assistant message.
- Compact boundary falls exactly between tool_use assistant and tool_result
  user message → adjust to keep the pair or remove it entirely.
- Double interrupt (Ctrl+C twice while working) → first interrupts, second
  exits as before.
- Empty history or single-message history → no-op for sanitize/compact.
