---
id: empty-text-block-guard
status: implemented
---
# Prevent empty text content blocks from reaching the Anthropic API

## Description
The Anthropic Messages API returns an intermittent 400 ("text content blocks
must be non-empty") when an empty or whitespace-only `text` content block reaches
the request. Empty blocks enter history via blank REPL prompts, empty steering
messages, and synthesized/empty resume prompts. Fix uses defense in depth: a
boundary guard at the single request-build chokepoint, source guards at every
injection point, and self-heal during history sanitization.
Implements GitHub issue #231.

## Context
- `internal/runtime/provider/anthropic.go` — `buildRequest` builds Anthropic SDK
  params; primary boundary guard added here.
- `internal/runtime/loop/loop.go` — `Send`, `Resume`, and the steering injection
  block; source guards added.
- `internal/agent/hub.go` — `ConsumeSteeringMessage`, `PeekSteeringMessage`;
  skip empty queue entries.
- `internal/agent/worker.go` — trims `msg.Text` after `PullMessage`.
- `internal/runtime/tokens/tokens.go` — `SanitizeHistory` + new
  `dropEmptyTextBlocks` helper; runs before every API call in the loop.
- Tests: `anthropic_test.go`, `loop_test.go`, `tokens_test.go`, `hub_test.go`.

## Behavior
- `buildRequest` skips any `text` block where `strings.TrimSpace(block.Text) ==
  ""`. A message left with zero content blocks is dropped from the request.
  `buildRequest` never emits a message containing an empty text block.
- `Loop.Send` with an empty/whitespace-only `promptText` does NOT append a user
  message but still runs the agentic loop (so resume/continuation advances).
- `Loop.Resume` with an empty prompt loads stored history and runs the loop
  without error, even when stored history contains an empty text block.
- The steering injection loop skips empty/whitespace-only steering text.
- `Hub.ConsumeSteeringMessage` discards empty/whitespace-only queued entries and
  returns the first non-empty message, or `("", false)` if none remain.
- `Hub.PeekSteeringMessage` returns the first non-empty queued message
  non-destructively, or `("", false)` if none.
- The worker trims `msg.Text` after pulling, before dispatch.
- `SanitizeHistory` drops empty/whitespace-only text blocks and removes messages
  left with no content, before validating tool_use/tool_result pairing.

## Constraints
- Must not strip non-text blocks (`tool_use`, `tool_result`) — they remain valid
  with no text.
- Must not drop a message that still has at least one non-empty block.
- Must not error when an empty prompt is sent/resumed; the loop must still run.
- Must not change `buildRequest` behavior for non-empty blocks (cache-control
  budget logic unchanged).

## Interfaces
```go
// internal/runtime/tokens/tokens.go
func dropEmptyTextBlocks(history []types.ChatMessage) []types.ChatMessage

// internal/runtime/loop/loop.go — unchanged signatures, new empty-prompt behavior
func (l *Loop) Send(ctx context.Context, promptText string, emit func(types.OutboundEvent)) error
func (l *Loop) Resume(ctx context.Context, historyID, promptText string, emit func(types.OutboundEvent)) error

// internal/agent/hub.go — unchanged signatures, now skip empty entries
func (h *Hub) ConsumeSteeringMessage() (string, bool)
func (h *Hub) PeekSteeringMessage() (string, bool)
```

## Edge Cases
- Empty string prompt to `Send`: no user message appended; loop runs; assistant
  reply is the only new history entry.
- Whitespace-only prompt (`"  \n\t "`): treated as empty.
- Resume with empty prompt + stored empty text block: no error; sanitized
  history contains no empty text blocks.
- Steering queue with only empty entries: `ConsumeSteeringMessage` returns
  `("", false)` after discarding them; loop does not inject anything.
- Steering queue with leading empties then a real message: empties discarded,
  real message returned and consumed.
- Assistant message with empty text + tool_use: text dropped, tool_use survives,
  message retained (both in `SanitizeHistory` and `buildRequest`).
- Message with only an empty text block: dropped entirely from the request and
  from sanitized history.
