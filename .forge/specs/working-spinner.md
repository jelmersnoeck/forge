---
id: working-spinner
status: implemented
---
# Show spinner whenever agent is working

## Description
The CLI does not display a spinner between the `tool_use` event and the next
`thinking` event. During multi-turn agentic loops the user sees long stretches
of silence (no animation, no text) while tools execute and the LLM is called
again. The spinner should be visible whenever the agent is actively working,
not just when `m.thinking` or `m.toolProgress` are set.

## Context
- `cmd/forge/cli.go` — model state (`working`, `thinking`, `toolProgress`,
  `spinnerFrame`), `Update()`, `View()`
- `internal/runtime/loop/loop.go` — emits `"thinking"` at top of each turn,
  `"tool_use"` / `"text"` during response, `"done"` at end
- `internal/tools/bash.go` — only tool that emits `"tool_progress"` events

State machine today (problem):

```
send message → (no indicator)
             → "thinking" event  → spinner "thinking..."
             → "text" event      → thinking=false, working=true  (text streams — OK)
             → "tool_use" event  → thinking=false, toolProgress="" (NO SPINNER)
             → ... tool executes ... (NO SPINNER)
             → "thinking" event  → spinner "thinking..."
             → "done" event      → working=false
```

## Behavior
1. When `m.working` is true and `m.thinking` is false and `m.toolProgress` is
   empty and no text is actively streaming, the CLI renders a spinner with the
   label "working...".
2. The spinner frame counter (`spinnerFrame`) advances on tick when `m.working`
   is true — not only when `m.thinking` or `toolProgress` are set.
3. When the user presses Enter to send a message, `m.working` is set to `true`
   immediately (before the first SSE event arrives), so the spinner appears
   without delay.
4. The three spinner states display in priority order:
   a. `toolProgress != ""` → spinner + tool progress text
   b. `thinking` → spinner + "thinking..."
   c. `working` → spinner + "working..."
5. When a `"text"` event arrives (LLM streaming text), `working` stays true
   but the spinner is suppressed while there is buffered text being rendered.
   The "working..." spinner only shows when output has been flushed (i.e.
   `m.textBuf == ""`).

## Constraints
- Must not add new event types to the agent/loop protocol.
- Must not change `internal/runtime/loop/loop.go` or any agent-side code.
- Changes are limited to `cmd/forge/cli.go`.
- Must not break existing task tracker spinner display.
- The spinner must not flicker between states (e.g. briefly showing "working..."
  between "thinking..." and tool output).

## Interfaces
No new types or exported APIs. Changes are internal to the CLI model's
`Update()` and `View()` methods.

```go
// View priority for the thinking indicator line:
//   1. toolProgress != ""          → spinner + toolProgress
//   2. thinking                    → spinner + "thinking..."
//   3. working && textBuf == ""    → spinner + "working..."
```

## Edge Cases
1. **Rapid tool_use → thinking transitions**: When a read-only tool finishes in
   milliseconds and the next "thinking" event arrives immediately, the UI should
   never briefly flash "working..." then "thinking...". The tick rate (100ms)
   provides natural debounce — if both events arrive within one tick, only the
   final state renders.
2. **Text streaming phase**: While the LLM streams text (`textBuf` is
   accumulating), "working..." should not appear — the streaming text itself
   is the progress indicator.
3. **Initial message send**: Pressing Enter should immediately show a spinner,
   covering the latency between the HTTP POST and the first SSE event. Today
   neither `working` nor `thinking` is set until the `"thinking"` event arrives.
4. **Queue drain**: When a queued message is auto-sent after "done", the same
   immediate-spinner behavior should apply.
5. **Phase orchestrator transitions**: Between phases (e.g. spec → code), the
   "working..." spinner should show during the gap between `phase_handoff`
   and the next `thinking` event — these events already set `working=true`.
