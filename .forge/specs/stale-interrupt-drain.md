---
id: stale-interrupt-drain
status: draft
---
# Drain stale interrupt signals before each turn

## Description
When a user presses Ctrl+C to interrupt a turn, there's a race window where the
interrupt signal arrives after the turn has already finished but before the next
turn's interrupt goroutine is set up. The stale signal sits in the buffered
channel and immediately cancels the next turn, causing "interrupted by user" to
appear seconds after sending a new message.

## Context
- `internal/agent/hub.go` — `interruptCh chan struct{}` (buffered size 1), `TriggerInterrupt()`, `InterruptChannel()`
- `internal/agent/worker.go` — turn loop (lines 114-239), interrupt goroutine (lines 162-168)
- `cmd/forge/cli.go` — `sendInterrupt()` (line 1264), Ctrl+C handler (line 455-471)

## Behavior
- At the start of each turn (after `PullMessage()`, before creating the interrupt
  goroutine), drain any pending signal from the interrupt channel.
- A Ctrl+C intended for turn N must never cancel turn N+1.
- A Ctrl+C that arrives during an active turn still cancels that turn immediately.
- No change to CLI-side behavior or the interrupt HTTP endpoint.

## Constraints
- Must not introduce a mutex or change the channel type.
- Must not block — use a non-blocking select drain.
- Must not affect the ability to interrupt an in-progress turn.

## Interfaces
```go
// In Hub, add:
func (h *Hub) DrainInterrupt() {
    select {
    case <-h.interruptCh:
    default:
    }
}
```

## Edge Cases
- **Rapid Ctrl+C + Enter**: User presses Ctrl+C and immediately sends a new
  message. The drain at the start of the next turn discards the stale signal.
  Expected: new turn runs uninterrupted.
- **Ctrl+C during active turn**: Signal is consumed by the interrupt goroutine
  before the turn finishes. Drain at next turn start finds nothing. Expected:
  current turn interrupted, next turn runs normally.
- **Multiple Ctrl+C presses**: Channel is buffered size 1, so `TriggerInterrupt`
  drops extras. At most one stale signal to drain. Expected: one drain clears it.
- **No Ctrl+C**: Drain finds nothing (hits `default`). Zero cost. Expected: no
  behavioral change.
