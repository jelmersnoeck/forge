---
id: context-summarization-compaction
status: implemented
---
# Summarize dropped conversation history before lossy compaction

## Description
When history exceeds the token budget, `tokens.Compact` deletes the middle
messages and leaves only a count-based boundary marker, permanently losing
early requirements and decisions. This spec adds Phase 1 of issue #254:
generate an LLM summary of the messages about to be dropped and inject it as
the boundary marker instead of a bare "N messages removed" note. Phases 2
(pinned-message retention) and 3 (hierarchical summarization) from the issue
are noted as extension points only, not implemented here.

## Context
- `internal/runtime/tokens/tokens.go` — `Compact` (pure function, no LLM
  access). Currently builds a static boundary `ChatMessage` at lines ~184-190.
  Returns `([]types.ChatMessage, int)`.
- `internal/runtime/loop/loop.go` — two compaction call sites:
  - Pre-send proactive compaction, ~line 319-335 (`ShouldCompact` → `Compact`).
  - Post-error reactive compaction, ~line 388-404 (`classified.ShouldCompact`).
  - `Loop` struct (line 30) holds `provider types.LLMProvider`, `model string`,
    `sessionID`, `budget`.
- `internal/agent/phase/classify.go` — reference pattern for a lightweight,
  model-fallback LLM call draining a delta channel (`classifyFullWithModel`).
- `internal/types/types.go` — `LLMProvider.Chat`, `LightweightModels`,
  `ChatMessage`, `ChatContentBlock`, `SystemBlock`, `ChatRequest`.
- New file: `internal/runtime/tokens/summarize.go` (summarizer + prompt).
- Tests: `internal/runtime/tokens/tokens_test.go`, new
  `internal/runtime/tokens/summarize_test.go`.

## Behavior
- `Compact` gains a way to accept a pre-built summary string. When a non-empty
  summary is supplied, the injected boundary message text is
  `"[Summary of N earlier messages removed to stay within context limits:\n\n<summary>\n\nThe conversation continues below.]"`.
  When the summary is empty, the existing count-only boundary text is used
  (backward compatible — no behavior change when summarization is unavailable
  or fails).
- A new `Summarize` function in the `tokens` package takes the slice of
  messages that `Compact` would drop, calls the LLM via a provided
  `types.LLMProvider`, and returns a plain-text summary string.
  - It tries each model in `types.LightweightModels` in order, falling through
    on error (same pattern as `classify.go`).
  - It uses a fixed system prompt instructing the model to preserve: original
    user requirements/goals, key decisions, files changed, errors encountered,
    and unresolved TODOs; and to be terse (target ≤ 500 tokens output).
  - It runs under a context timeout (default 30s) and `MaxTokens` cap (~1024).
  - On any failure (all models error, timeout, empty response) it returns
    `("", err)` — the caller proceeds with empty-summary compaction.
- The loop's pre-send compaction path computes the drop set, calls `Summarize`,
  then calls `Compact` with the summary. Summarization failure is non-fatal:
  log + emit a `warning` event, fall back to count-only compaction, continue.
- The loop emits the existing `compact` event; when a summary was produced the
  event `Content` notes that a summary was injected (e.g.
  `"Compacted conversation: summarized and removed N messages (now ~T tokens)"`).
- Recursion guard: `Summarize` itself never triggers compaction. It sends only
  the drop-set messages (already bounded below the full budget) plus a small
  system prompt; it does not route through `Loop.runLoop` and does not call
  `ShouldCompact`. If the drop set itself exceeds a hard cap
  (`maxSummarizeInputTokens`, default = budget threshold), it is truncated
  from the oldest end before the call.
- The post-error reactive compaction path (`classified.ShouldCompact`) MAY pass
  an empty summary to keep that hot recovery path cheap and fast; if it does,
  behavior is identical to today. Implementer's choice whether to summarize
  there — default to empty summary to avoid extra latency mid-failure.

## Constraints
- Must not split tool_use/tool_result pairs — preserve existing pairing logic
  in `Compact`; the drop-set boundary is the same `keepFromIdx` it computes today.
- Must not call the LLM from inside `tokens.Compact` — `Compact` stays pure and
  synchronous. Summarization is a separate function the loop orchestrates.
- Must not block the turn indefinitely: `Summarize` must honor the passed
  context and its own timeout.
- Must not regress the no-summary path: when summary is `""`, output bytes of
  `Compact` must be identical to the pre-change implementation (same boundary
  text, same kept messages).
- Must not recurse: `Summarize` must not invoke `ShouldCompact`, `Compact`, or
  `Loop.runLoop`.
- Must not send empty text blocks to the API (Anthropic 400) — skip
  empty/whitespace messages when building the summarization request.
- Do not implement Phase 2 pinning or Phase 3 hierarchical summarization.

## Interfaces
```go
// internal/runtime/tokens/tokens.go
// Compact keeps the existing signature for the no-summary path; a
// summary-aware variant and a drop-set helper are added.

// Compact delegates to CompactWithSummary(..., "") — unchanged signature,
// byte-identical output when no summary is available.
func Compact(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int) ([]types.ChatMessage, int)
func CompactWithSummary(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int, summary string) ([]types.ChatMessage, int)

// DropSet returns the messages Compact would remove for the given budget,
// so the loop can summarize them before compacting. Returns nil if no
// compaction would occur. Equals history[1:keepFromIdx].
func DropSet(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int) []types.ChatMessage

// Internal: compactKeepIdx and boundaryText are the shared helpers behind the
// three exported functions above (keep-index computation + boundary text).

// internal/runtime/tokens/summarize.go
// Summarize produces a terse plain-text summary of the given messages using a
// lightweight model. Returns ("", err) on failure; callers must tolerate this.
// Takes budget so it can truncate the input from the oldest end if the drop
// set itself exceeds the threshold.
func Summarize(ctx context.Context, provider types.LLMProvider, msgs []types.ChatMessage, budget Budget) (string, error)

const (
    summarizeTimeout         = 30 * time.Second
    maxSummarizeOutputTokens = 1024
)
```

## Edge Cases
- **Empty drop set** (`DropSet` returns nil): loop skips `Summarize` entirely,
  no LLM call, no compaction. `Compact`/`CompactWithSummary` returns history
  unchanged with removed=0.
- **All lightweight models 404/error**: `Summarize` returns `("", err)`; loop
  logs warning, emits `warning` event, falls back to count-only compaction.
  Turn proceeds normally.
- **Summarization timeout**: context deadline hit mid-call → `Summarize`
  returns `("", ctx.Err())`; same fallback as above. Must not leak the
  delta-channel goroutine (drain or rely on ctx cancellation).
- **Drop set is only tool_use/tool_result pairs (no text)**: summary still
  generated from the tool names/inputs/results; if the model returns empty,
  treat as failure and fall back to count-only.
- **Drop set itself exceeds budget threshold**: truncate oldest messages from
  the drop set before sending to the summarizer so the summarization request
  never itself overflows context.
- **Provider returns a `"error"` delta** mid-stream: `Summarize` returns
  `("", err)` carrying the stream error text (no silent swallow).
- **Concurrent/resumed sessions**: summary is injected as an ordinary `user`
  text message in history and persists through JSONL like the current boundary
  marker — resume must reconstruct valid history (no orphaned tool_results,
  honored by existing `SanitizeHistory`).

## Alternatives
- Make `Compact` itself take an `LLMProvider` and summarize internally —
  rejected: couples the pure token math to network I/O and complicates testing.
- Summarize the entire history every turn — rejected: cost and latency; only
  summarize the about-to-be-dropped slice, and only when compacting.
