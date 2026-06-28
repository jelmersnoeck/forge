---
id: pr-health-monitor
status: implemented
---
# Background PR health monitor with auto-fix

## Description
A background goroutine in the agent worker that periodically checks the health
of an open PR: whether it needs rebasing onto the base branch, and whether CI
checks are passing. If main has new commits, it injects a rebase message. If CI
is failing, it injects a message asking the agent to investigate and fix.

**TOCTOU fix (issue #192):** The monitor no longer runs git operations directly.
Instead it enqueues synthetic messages (`source: pr_check_internal`) into the hub.
The worker processes these inline in its main loop, serializing all git access.

## Context
- `internal/agent/worker.go` — spawns the monitor goroutine; main loop intercepts `pr_check_internal` messages
- `internal/agent/hub.go` — PushMessage for injecting messages
- `internal/tools/git.go` — git/gh helper functions (exported: GitOutput, GitOutputFull, GHOutput, RunGitCmd, DetectDefaultBranch, GHAvailable)
- `internal/agent/pr_monitor.go` — PR health monitor: timer goroutine + inline check handler
- `internal/agent/pr_monitor_test.go` — 11 tests covering rebase, checks, serialization
- `internal/types/types.go` — "pr_monitor" OutboundEvent type

## Behavior
- On worker start, a `prHealthMonitor` goroutine launches alongside reviewListener.
- Every 5 minutes (const `prMonitorInterval`), the monitor enqueues a synthetic
  message (`source: pr_check_internal`) into the hub via `enqueuePRCheck()`.
- The worker's main loop intercepts messages with `Source == prCheckSource` and
  calls `handlePRCheck()` inline — serialized with all other message processing.
- `handlePRCheck` delegates to `prHealthCheck`, which:
  1. Checks if a PR exists for the current branch (via `gh pr view`). If no PR,
     skips silently — this is the "haven't opened a PR yet" edge case.
  2. Fetches `origin/<base>` and checks if HEAD..origin/<base> has new commits.
     If the base branch has advanced, injects a rebase message for the agent.
  3. Checks CI status via `gh pr view --json statusCheckRollup`. If any required
     check has COMPLETED+FAILURE or COMPLETED+TIMED_OUT, pushes a message to the
     hub telling the agent to investigate and fix.
- The monitor emits `OutboundEvent` with type `"pr_monitor"` for status updates
  visible to the CLI user. It also emits `"pr_url"` events with the PR URL so
  the CLI can display it in the status bar.
- When injecting a "fix CI" or "rebase" message, `handlePRCheck` uses
  `hub.PushMessage` with source `"pr_monitor"` so the agent can distinguish
  automated requests from user messages.
- The monitor is gracefully stopped via context cancellation.
- The worker tracks `prTerminal` state: once a PR is merged/closed, subsequent
  PR check messages are skipped without running `handlePRCheck`.
- If gh CLI is not on PATH, the monitor logs once and returns immediately.
- The initial check fires immediately at startup (enqueued before the ticker starts).

## Constraints
- No mocks — all tests use real git repos with t.TempDir().
- No changes to the conversation loop or types package structure.
- Monitor interval is 5 minutes minimum (API rate limits).
- Only runs in the main worker, not in sub-agents.
- Git helpers exported from tools package (not duplicated).
- PR check runs only in the worker's main loop — never from the monitor goroutine.

## Interfaces

```go
// prMonitorInterval controls check frequency.
const prMonitorInterval = 5 * time.Minute

// prCheckSource is the sentinel Source value for internal PR health check messages.
const prCheckSource = "pr_check_internal"

// PRInfo holds the current state of a PR for the working branch.
type PRInfo struct {
    Number       int
    URL          string
    Branch       string
    Base         string
    State        string    // "OPEN", "CLOSED", "MERGED"
    ChecksOK     bool
    NeedsRebase  bool
    FailedChecks []string
}

// prHealthMonitor runs in a background goroutine (timer only, no git ops).
func (w *Worker) prHealthMonitor(ctx context.Context)

// enqueuePRCheck pushes a synthetic message into the hub.
func (w *Worker) enqueuePRCheck()

// handlePRCheck runs a PR health check inline in the worker loop.
// Returns true if PR is in terminal state (merged/closed).
func (w *Worker) handlePRCheck(ctx context.Context) bool

// prHealthCheck performs a single health check cycle.
// Returns (needsFix, fixMsg, terminal).
func (w *Worker) prHealthCheck(ctx context.Context) (bool, string, bool)

// getPRInfo queries GitHub for the current branch's PR status.
func getPRInfo(cwd string) (*PRInfo, error)

// checkNeedsRebase returns true if origin/<base> has commits not in HEAD.
func checkNeedsRebase(cwd, base string) bool

// Exported git helpers in tools package:
func GitOutput(cwd string, args ...string) (string, error)
func GitOutputFull(cwd string, args ...string) (string, string, error)
func GHOutput(cwd string, args ...string) (string, error)
func RunGitCmd(cwd string, args ...string) error
func DetectDefaultBranch(cwd string) string
```

## Edge Cases
- **No PR exists yet**: Monitor returns early, no error, no event. Rechecked every cycle.
- **PR is already merged/closed**: Worker sets prTerminal, skips subsequent check messages. Emits one-time info event.
- **gh CLI not available**: Log once, return immediately (monitor disabled for session).
- **Network errors**: Log and retry next cycle (transient).
- **Agent is busy (processing a turn)**: PR check message sits in the hub queue until the turn completes, then processes — no race.
- **Multiple failing checks**: Aggregated into single fix message with all failure names.
- **Check is still pending/in-progress**: Not treated as failure, only COMPLETED+FAILURE or COMPLETED+TIMED_OUT.
- **Rapid timer fires while agent is busy**: Messages queue up in hub; worker processes them sequentially when idle.
