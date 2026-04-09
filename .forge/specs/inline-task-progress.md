---
id: inline-task-progress
status: implemented
---
# Inline task progress with spinner and rolling output

## Description
Replace repeated TaskGet/AgentGet tool_use lines in the CLI with a single
live-updating block per task. Each task shows a spinner, description, and the
last 5 lines of output. Multiple concurrent tasks each get their own block.
A new `task_status` event is emitted from the tool handlers to carry task
state to the CLI.

## Context
- `cmd/forge/cli.go` — model, handleEvent, View, tick, spinner, taskTracker
- `cmd/forge/task_tracker_test.go` — CLI-side tracker tests
- `internal/agent/task_broadcast.go` — background 1s broadcaster for live progress
- `internal/agent/task_broadcast_test.go` — broadcaster tests
- `internal/agent/worker.go` — wires up broadcaster goroutine
- `internal/runtime/loop/loop.go` — toolSummaryKeys for TaskGet/AgentGet/TaskOutput
- `internal/tools/task.go` — handleTaskGet, emitTaskStatus helper
- `internal/tools/task_test.go` — event emission tests
- `internal/tools/agent.go` — handleAgentGet emits task_status
- `internal/tools/agent_test.go` — agent event emission test
- `internal/types/types.go` — OutboundEvent type list (added task_status)

## Behavior
- When a `tool_use` event arrives for `TaskGet`, `AgentGet`, or `TaskOutput`,
  the CLI does **not** append a new output line.
- The tool handlers (`handleTaskGet`, `handleAgentGet`) emit a `task_status`
  event containing JSON with id, description, status, outputTail (last 5
  non-empty lines), startTime, and duration (if terminal).
- The CLI parses `task_status` events and creates/updates a `taskTracker`
  keyed by task/agent ID.
- The View renders active task trackers between the main output and the
  thinking indicator:
  ```
  ⠹ Running tests (b3)                    running
      PASS TestFoo
      PASS TestBar
      --- FAIL TestBaz
      expected 42, got 0
      FAIL ./pkg/...
  ```
- Once a task reaches a terminal state (completed/failed/killed), the tracker
  is finalized: a one-line summary (icon + description + task ID + duration)
  is appended to scrollback output and the tracker is removed.
- The spinner ticks at 100ms (existing tick rate) and animates whenever there
  are active task trackers or the thinking indicator is shown.
- A background goroutine (`taskStatusBroadcaster`) in the agent worker emits
  task_status events every 1 second for all non-terminal tasks, ensuring the
  CLI's output tail stays fresh even while the LLM is thinking or running
  other tools. This complements the per-TaskGet emissions from tool handlers.
- Multiple TaskGet calls for the same task ID update the same tracker entry.
- Multiple tasks show as separate blocks, stacked vertically, in insertion order.
- When the agent emits a `done` event, all remaining trackers are finalized.

## Constraints
- The agent-side change is minimal: a single `emitTaskStatus` call added to
  `handleTaskGet` and `handleAgentGet`, plus toolSummaryKeys entries.
- No new dependencies.
- Non-task tool_use rendering is unchanged.
- Task output preview is capped at 5 non-empty lines.
- Long output lines are truncated to terminal width minus indent.

## Interfaces
```go
// taskTracker holds live state for a background task being polled.
type taskTracker struct {
    taskID      string
    description string
    status      string
    outputTail  []string  // last 5 lines of output
    startTime   time.Time // for duration display on completion
    duration    string    // set when terminal
}

// Model additions:
type model struct {
    // ...existing fields...
    taskTrackers     map[string]*taskTracker // keyed by task/agent ID
    taskTrackerOrder []string                // insertion order for stable rendering
}

// emitTaskStatus sends a task_status OutboundEvent from tool handlers.
func emitTaskStatus(ctx types.ToolContext, id, description, status, output string, startTime time.Time, endTime *time.Time)

// task_status event Content JSON schema:
// {
//   "id":          string,
//   "description": string,
//   "status":      string,
//   "outputTail":  []string,
//   "startTime":   string,
//   "duration":    string   // only present if endTime is set
// }
```

## Edge Cases
- TaskGet for unknown/missing task: normal error result, no task_status emitted.
- TaskGet returns terminal state on first call: tracker created and immediately
  finalized — shows final summary, no spinner.
- Agent emits `done` while tasks are tracked: all trackers finalized, summaries
  appended to scrollback.
- Output is empty: show spinner + description, no indented lines.
- Very long output lines: truncated to terminal width minus 8 chars indent.
- Invalid JSON in task_status Content: silently ignored (no crash).
- TaskOutput tool_use lines: also suppressed (handled via same tracker).
