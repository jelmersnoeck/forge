---
id: history-integrity-and-interrupt
status: implemented
---
# Fix history corruption and interrupt handling

## Description
Three related bugs: (1) Compact could split tool_use/tool_result pairs, causing
400 errors from Anthropic. (2) Ctrl+C sent an interrupt signal but nobody
read it — the agent loop ignored interrupts entirely. (3) After an API error,
the loop didn't emit "done", so the CLI never returned to the input prompt.

## Context
- `internal/runtime/tokens/tokens.go` — Compact, SanitizeHistory, hasToolResults, hasToolUse, dropToolResults
- `internal/runtime/tokens/tokens_test.go` — TestCompactPreservesToolPairs, TestSanitizeHistory
- `internal/runtime/loop/loop.go` — runLoop (context check + SanitizeHistory call)
- `internal/agent/worker.go` — Worker.Run (per-turn cancel context + interrupt wiring + done emission on error)
- `internal/agent/hub.go` — TriggerInterrupt, InterruptChannel (now actually used)
- `cmd/forge/cli.go` — Ctrl+C handler (unchanged, already worked — server side was broken)

## Behavior
1. Compact never splits tool_use/tool_result pairs. If keepFromIdx lands on a
   user message with tool_result blocks, it adjusts back one to include the
   matching assistant message.
2. SanitizeHistory runs before every API call, validating that every tool_result
   has a matching tool_use in the preceding assistant message. Orphaned
   tool_result blocks are dropped. Trailing assistant messages with tool_use
   but no following tool_result are dropped.
3. Worker creates a per-turn cancellable context wired to the hub's interrupt
   channel. When interrupted, the loop stops and the worker emits both an error
   event ("Interrupted by user") and a done event.
4. The loop checks ctx.Err() at the top of each turn to bail early on
   cancellation.
5. On any error (API failure, interrupt, etc.), the worker emits "done" so the
   CLI returns to the input prompt.

## Constraints
- No changes to the Anthropic API contract or types.
- Compact still respects budget — pair integrity adjustment only.
- Interrupt is best-effort; long-running tool execution may delay it.

## Interfaces
```go
// Compact — adjusted to respect tool_use/tool_result pairing.
func Compact(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int) ([]types.ChatMessage, int)

// SanitizeHistory validates tool_use/tool_result pairing in history.
func SanitizeHistory(history []types.ChatMessage) []types.ChatMessage

// hasToolResults reports whether msg contains any tool_result content blocks.
func hasToolResults(msg types.ChatMessage) bool

// hasToolUse reports whether msg contains any tool_use content blocks.
func hasToolUse(msg types.ChatMessage) bool

// dropToolResults returns a copy of msg with tool_result blocks removed.
func dropToolResults(msg types.ChatMessage) types.ChatMessage
```

## Edge Cases
- History ends with an assistant message containing tool_use but no following
  tool_result (interrupted mid-turn) → SanitizeHistory drops the trailing assistant message.
- Compact boundary falls exactly between tool_use assistant and tool_result
  user message → keepFromIdx adjusted to include the assistant, keeping the pair.
- Double interrupt (Ctrl+C twice while working) → first interrupts via cancel,
  second exits the CLI as before (exitAttempts logic in CLI unchanged).
- Empty history or single-message history → no-op for both sanitize and compact.
- Orphaned tool_result at start of history (no preceding assistant) → dropped.
- tool_result with mismatched ID (ID not in preceding assistant's tool_use) → dropped.
