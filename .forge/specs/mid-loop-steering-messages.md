---
id: mid-loop-steering-messages
status: implemented
---
# Mid-loop steering messages to redirect coder between LLM calls

## Description
Allow the user to send messages that get injected into the conversation history
mid-turn, between LLM call iterations. Currently messages queue until the turn
finishes; this lets users course-correct without interrupting.

## Context
- `internal/runtime/loop/loop.go` — main agentic loop, steering checkpoint
- `internal/agent/hub.go` — message queue, new `PeekSteeringMessage` / `ConsumeSteeringMessage`
- `internal/agent/worker.go` — wires hub into loop via callback
- `internal/types/types.go` — OutboundEvent (new `steering` event type)
- `cmd/forge/cli.go` — send messages immediately even while working

## Behavior
- When the user types a message while the agent is working, it is sent to the
  agent HTTP API immediately (not client-side queued).
- Between LLM call iterations in the loop, a steering checkpoint checks for
  queued messages.
- If a steering message exists, it is appended to the conversation history as a
  user message before the next LLM call.
- A `steering` outbound event is emitted so the CLI can confirm the message was
  injected (not just queued).
- The steering message is persisted to the session JSONL like any other message.
- Multiple steering messages between a single LLM call are all consumed and
  injected (not just the first).
- The CLI shows a visual indicator when a steering message is injected
  (e.g., dimmed "steering message injected" line).

## Constraints
- Don't change the interrupt mechanism (Ctrl+C still cancels the turn).
- Don't add a separate HTTP endpoint — reuse `/messages`.
- Don't break the existing queue-and-send-after-done behavior for gateway mode.
- Steering messages are regular user messages in history — no special role or
  type in the Anthropic API payload.
- The loop must not block waiting for steering messages — it's a non-blocking
  peek/consume pattern.

## Interfaces

### Hub additions
```go
// PeekSteeringMessage returns a queued message without removing it, if any.
// Non-blocking: returns ("", false) if the queue is empty.
func (h *Hub) PeekSteeringMessage() (string, bool)

// ConsumeSteeringMessage removes and returns the next queued message.
// Non-blocking: returns ("", false) if the queue is empty.
func (h *Hub) ConsumeSteeringMessage() (string, bool)
```

### Loop changes
```go
// Options gains a SteeringSource callback.
type Options struct {
    // ...existing fields...
    // SteeringSource is called between LLM iterations to check for
    // mid-turn user messages. Returns (text, true) if available.
    SteeringSource func() (string, bool)
}
```

### New outbound event type
```
type: "steering"
content: <the steering message text>
```

## Edge Cases
- No steering messages queued: loop proceeds normally, zero overhead.
- Multiple steering messages between one LLM call: all consumed and injected
  as separate user messages in order.
- Steering message arrives during tool execution: picked up at next checkpoint
  (after tools complete, before next LLM call).
- Steering message on a turn with no tool use (LLM responds with text only):
  not picked up (turn ends, message becomes a regular queued message for next
  turn via existing PullMessage path).
- Context cancelled while consuming steering: loop returns ctx.Err() as usual.
