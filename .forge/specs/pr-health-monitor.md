---
id: pr-health-monitor
status: implemented
---
# Background PR health monitor with auto-fix

## Description
A background goroutine in the agent worker that periodically checks the health
of an open PR: whether it needs rebasing onto the base branch, and whether CI
checks are passing. If main has new commits, it auto-rebases. If CI is failing,
it injects a message into the hub asking the agent to investigate and fix.

## Context
- `internal/agent/worker.go` — spawns the monitor goroutine alongside reviewListener (line 87)
- `internal/agent/hub.go` — PushMessage for injecting fix-it messages, new IsIdle() method
- `internal/tools/pr_create.go` — git/gh helper functions (exported: GitOutput, GitOutputFull, GHOutput, RunGitCmd, DetectDefaultBranch)
- `internal/tools/pr_create_test.go` — updated to use exported helper names
- `internal/agent/pr_monitor.go` — new file, PR health monitor logic
- `internal/agent/pr_monitor_test.go` — 10 tests covering rebase, conflicts, checks, idle detection
- `internal/types/types.go` — added "pr_monitor" to OutboundEvent type documentation

## Behavior
- On worker start, a `prHealthMonitor` goroutine launches alongside reviewListener.
- Every 5 minutes (const `prMonitorInterval`), the monitor:
  1. Checks if a PR exists for the current branch (via `gh pr view`). If no PR,
     skips silently — this is the "haven't opened a PR yet" edge case.
  2. Fetches `origin/<base>` and checks if HEAD..origin/<base> has new commits.
     If the base branch has advanced, auto-rebases and force-pushes.
  3. Checks CI status via `gh pr view --json statusCheckRollup`. If any required
     check has COMPLETED+FAILURE or COMPLETED+TIMED_OUT, pushes a message to the
     hub telling the agent to investigate and fix.
- The monitor emits `OutboundEvent` with type `"pr_monitor"` for status updates
  visible to the CLI user.
- When injecting a "fix CI" message, the monitor uses `hub.PushMessage` with
  source `"pr_monitor"` so the agent can distinguish automated requests.
- The monitor is gracefully stopped via context cancellation.
- If rebase fails (conflicts), it aborts the rebase, emits an error event, and
  does NOT inject a fix message.
- The monitor skips cycles when the agent is busy (hub.IsIdle() returns false).
- Once a PR is merged or closed, the monitor stops checking (sets prTerminal flag).
- If gh CLI is not on PATH, the monitor logs once and returns immediately.

## Constraints
- No mocks — all tests use real git repos with t.TempDir().
- No changes to the conversation loop or types package structure.
- Monitor interval is 5 minutes minimum (API rate limits).
- Does NOT attempt to fix merge conflicts — only reports them.
- Only runs in the main worker, not in sub-agents.
- Git helpers exported from tools package (not duplicated).

## Interfaces

```go
// prMonitorInterval controls check frequency.
const prMonitorInterval = 5 * time.Minute

// PRInfo holds the current state of a PR for the working branch.
type PRInfo struct {
    Number       int
    Branch       string
    Base         string
    State        string    // "OPEN", "CLOSED", "MERGED"
    ChecksOK     bool
    NeedsRebase  bool
    FailedChecks []string
}

// prHealthMonitor runs in a background goroutine.
func (w *Worker) prHealthMonitor(ctx context.Context)

// prHealthCheck performs a single health check cycle.
// Returns (needsFix, fixMsg, terminal).
func (w *Worker) prHealthCheck(ctx context.Context) (bool, string, bool)

// getPRInfo queries GitHub for the current branch's PR status.
func getPRInfo(cwd string) (*PRInfo, error)

// checkNeedsRebase returns true if origin/<base> has commits not in HEAD.
func checkNeedsRebase(cwd, base string) bool

// rebaseAndPush rebases onto origin/<base> and force-pushes.
func (w *Worker) rebaseAndPush(ctx context.Context, base string) error

// Hub.IsIdle reports whether the worker is waiting for a message.
func (h *Hub) IsIdle() bool

// Exported git helpers in tools package:
func GitOutput(cwd string, args ...string) (string, error)
func GitOutputFull(cwd string, args ...string) (string, string, error)
func GHOutput(cwd string, args ...string) (string, error)
func RunGitCmd(cwd string, args ...string) error
func DetectDefaultBranch(cwd string) string
```

## Edge Cases
- **No PR exists yet**: Monitor returns early, no error, no event. Rechecked every cycle.
- **PR is already merged/closed**: Monitor sets prTerminal flag, stops checking. Emits one-time info event.
- **Rebase conflicts**: Abort rebase, emit error event, do NOT auto-fix.
- **gh CLI not available**: Log once, return immediately (monitor disabled for session).
- **Network errors**: Log and retry next cycle (transient).
- **Agent is busy (processing message)**: Skip cycle via hub.IsIdle() check.
- **Multiple failing checks**: Aggregated into single fix message with all failure names.
- **Check is still pending/in-progress**: Not treated as failure, only COMPLETED+FAILURE or COMPLETED+TIMED_OUT.
