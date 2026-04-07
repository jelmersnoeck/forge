---
id: pr-health-monitor
status: draft
---
# Background PR health monitor with auto-fix

## Description
A background goroutine in the agent worker that periodically checks the health
of an open PR: whether it needs rebasing onto the base branch, and whether CI
checks are passing. If main has new commits, it auto-rebases. If CI is failing,
it injects a message into the hub asking the agent to investigate and fix.

## Context
- `internal/agent/worker.go` — spawns the monitor goroutine alongside reviewListener
- `internal/agent/hub.go` — PushMessage for injecting fix-it messages
- `internal/tools/pr_create.go` — git/gh helper functions (reuse)
- `internal/tools/helpers.go` — shared git helpers (exported for reuse)
- `internal/agent/pr_monitor.go` — new file, PR health monitor logic
- `internal/agent/pr_monitor_test.go` — tests

## Behavior
- On worker start, a `prHealthMonitor` goroutine launches alongside reviewListener.
- Every 5 minutes (configurable via const), the monitor:
  1. Checks if a PR exists for the current branch (via `gh pr view`). If no PR,
     skips silently — this is the "haven't opened a PR yet" edge case.
  2. Fetches `origin/<base>` and compares with HEAD. If the base branch has
     advanced, auto-rebases and force-pushes.
  3. Checks CI status via `gh pr checks`. If any required check is failing,
     pushes a message to the hub telling the agent to investigate the failure
     and fix it.
- The monitor emits `OutboundEvent` with type `"pr_monitor"` for status updates
  visible to the CLI user (e.g., "PR is up to date", "Rebasing onto main...",
  "CI failing, investigating...").
- When injecting a "fix CI" message, the monitor uses `hub.PushMessage` with
  source `"pr_monitor"` so the agent can distinguish automated requests.
- The monitor is gracefully stopped via context cancellation (same as reviewListener).
- The rebase + push is only attempted once per cycle. If rebase fails (conflicts),
  it emits an error event and does NOT inject a fix message (conflicts need human
  judgment).
- The monitor should not run checks while the agent is actively processing a
  message (to avoid git conflicts with ongoing work). It checks hub idle state.

## Constraints
- Do NOT use mocks — use real git/gh commands in tests (or skip if gh not available).
- Do NOT modify the conversation loop or types package for this feature.
- Do NOT poll more frequently than every 2 minutes (API rate limits).
- Do NOT attempt to fix merge conflicts — only report them.
- Do NOT run the monitor in sub-agents, only in the main worker.
- Git helper functions (gitOutput, gitOutputFull, etc.) must be exported from
  tools package for reuse, or duplicated in agent package. Prefer export.

## Interfaces

```go
// prHealthMonitor runs in a background goroutine, checking PR health
// periodically and taking corrective action.
func (w *Worker) prHealthMonitor(ctx context.Context)

// prHealthCheck performs a single health check cycle.
// Returns true if the agent should investigate CI failures.
func (w *Worker) prHealthCheck(ctx context.Context, emit func(types.OutboundEvent)) (needsFix bool, fixMsg string)

// PRInfo holds the current state of a PR for the working branch.
type PRInfo struct {
    Number    int
    Branch    string
    Base      string
    State     string    // "OPEN", "CLOSED", "MERGED"
    ChecksOK  bool
    NeedsRebase bool
    FailedChecks []string
}

// getPRInfo queries GitHub for the current branch's PR status.
func getPRInfo(cwd string) (*PRInfo, error)
```

## Edge Cases
- **No PR exists yet**: Monitor returns early, no error, no event emitted. Checked
  every cycle since a PR might be created mid-session.
- **PR is already merged/closed**: Monitor stops checking (emits one-time info event).
- **Rebase conflicts**: Abort rebase, emit error event, do NOT auto-fix.
- **gh CLI not available**: Log once, disable monitor for session.
- **Network errors**: Log and retry next cycle (transient).
- **Agent is busy (processing message)**: Skip this cycle to avoid git races.
- **Multiple failing checks**: Aggregate all failures into a single fix message.
- **Check is still pending/in-progress**: Don't treat as failure, wait for completion.
