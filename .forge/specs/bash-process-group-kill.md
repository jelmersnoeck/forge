---
id: bash-process-group-kill
status: implemented
---
# Kill entire process tree and make loop non-blocking on interrupt

## Description
Two related issues with interrupt handling:

1. Bash tool and background task manager only killed the top-level bash process
   on context cancellation or timeout. Child processes (tail -f, sleep, servers)
   survived as orphans, holding stdout/stderr pipes open and blocking cmd.Run()
   indefinitely.

2. The conversation loop's `executeToolsGated()` blocked synchronously on tool
   execution. Even with context propagation, the loop couldn't return until
   every tool handler finished — making interrupts useless during tool execution.

## Context
- `internal/tools/bash.go` — process group kill on cancel/timeout
- `internal/runtime/task/manager.go` — same for background tasks
- `internal/runtime/loop/loop.go` — non-blocking tool execution with ctx select
- `internal/tools/bash_test.go` — cancellation/timeout tests
- `internal/runtime/loop/loop_test.go` — interrupt-during-tool-execution tests

## Behavior
- Bash commands run in a new process group (`Setpgid: true`)
- On context cancellation or timeout, `SIGKILL` is sent to the entire process
  group (`kill(-pgid, SIGKILL)`) via `cmd.Cancel`
- `cmd.WaitDelay` of 5s prevents infinite blocking if pipes stay open
- Context cancellation returns `"Command interrupted"` for bash
- Background tasks get the same process group isolation
- `executeToolsGated()` runs all tool phases in a goroutine and selects on
  both completion and `ctx.Done()` — returning immediately on interrupt
- Pre-filled "Interrupted before completion" stubs ensure valid tool_result
  blocks even for tools that haven't finished
- Context is checked between read-only and mutating phases, and between
  sequential mutating tool calls

## Constraints
- Must not change behavior for normal (non-cancelled, non-timed-out) execution
- Must work on darwin and linux (Setpgid is POSIX)
- ReadOnly/mutating phasing and ordering guarantees are preserved
- No Windows support needed (forge targets unix)

## Interfaces
```go
// Process group setup (bash.go, manager.go):
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
cmd.Cancel = func() error {
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
cmd.WaitDelay = 5 * time.Second

// Non-blocking tool execution (loop.go):
// All tool work in goroutine, main goroutine selects on done vs ctx.Done()
```

## Edge Cases
- `tail -f /dev/null` with context cancel → returns within 1s, not infinite
- `tail -f /dev/null` with 500ms timeout → returns within 1s, not infinite
- Interrupt during mutating tool → loop returns immediately, tool gets ctx cancel
- Interrupt during concurrent read-only tools → loop returns immediately
- Interrupt between phases → skips mutating phase entirely
- Normal execution → identical behavior, no observable change
- Deeply nested child processes (bash → bash → tail) → all killed via pgid
