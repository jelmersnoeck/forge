---
id: inline-task-progress
status: draft
---
# Inline task progress with spinner and rolling output

## Description
Replace repeated TaskGet/AgentGet tool_use lines in the CLI with a single
live-updating line per task. Each task shows a spinner, description, and the
last 5 lines of output, updated every second. Multiple concurrent tasks each
get their own block.

## Context
- `cmd/forge/cli.go` — model, handleEvent, View, tick, spinner
- `internal/runtime/loop/loop.go` — tool_use event emission, toolUseSummary
- `internal/tools/task.go` — TaskGet handler, emits task status
- `internal/types/types.go` — OutboundEvent (may need new fields or event type)

## Behavior
- When a `tool_use` event arrives for `TaskGet` or `AgentGet`, the CLI does
  **not** append a new output line. Instead it updates an in-memory tracker
  keyed by task/agent ID.
- The tracker stores: task ID, description (from the tool_use content),
  status, and last 5 lines of output.
- The View renders active task trackers between the main output and the
  thinking indicator:
  ```
  ⠹ Running tests (b3)                    [running]
      PASS TestFoo
      PASS TestBar
      --- FAIL TestBaz
      expected 42, got 0
      FAIL ./pkg/...
  ```
- Once a task reaches a terminal state (completed/failed/killed), the tracker
  line is replaced with a final one-line summary (checkmark/cross + description
  + duration) and output lines are removed. This final line gets appended to
  the scrollback output.
- The spinner ticks at 100ms (existing tick rate), but task output polling
  happens every ~1s (every 10th tick).
- Multiple TaskGet calls for the same task ID update the same tracker entry.
- Multiple tasks show as separate blocks, stacked vertically.

## Constraints
- Do not change the agent-side tool handlers or event emission — all changes
  are CLI-only rendering logic.
- Do not introduce new dependencies.
- Do not break existing non-task tool_use rendering.
- The task output preview must not exceed 5 lines to keep the display compact.

## Interfaces
```go
// taskTracker holds live state for a background task being polled.
type taskTracker struct {
    taskID      string
    description string
    status      string
    outputLines []string // last 5 lines
    startTime   time.Time
}
```

Model additions:
```go
type model struct {
    // ...existing fields...
    taskTrackers map[string]*taskTracker // keyed by task/agent ID
}
```

## Edge Cases
- TaskGet for unknown/missing task: falls through to normal tool_use rendering.
- TaskGet returns terminal state on first call: show final summary immediately,
  no spinner.
- Agent exits while tasks are tracked: trackers are abandoned (no cleanup needed,
  view simply stops rendering them).
- Output is empty: show spinner + description, no indented lines.
- Very long output lines: truncate to terminal width minus indent.
