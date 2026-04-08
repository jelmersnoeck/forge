---
id: bash-process-group-kill
status: implemented
---
# Kill entire process tree on bash cancel/timeout

## Description
Bash tool and background task manager only killed the top-level bash process on
context cancellation or timeout. Child processes (tail -f, sleep, servers, etc.)
survived as orphans, holding stdout/stderr pipes open and blocking cmd.Run()
indefinitely. This made interrupts effectively useless for long-running commands.

## Context
- `internal/tools/bash.go` — bashHandler, the Bash tool executor
- `internal/runtime/task/manager.go` — background task bash runner
- `internal/tools/bash_test.go` — new cancellation/timeout tests

## Behavior
- Bash commands run in a new process group (`Setpgid: true`)
- On context cancellation or timeout, `SIGKILL` is sent to the entire process
  group (`kill(-pgid, SIGKILL)`) via `cmd.Cancel`
- If pipes somehow remain open after SIGKILL, `cmd.WaitDelay` of 5s prevents
  infinite blocking
- Context cancellation now returns `"Command interrupted"` instead of a generic
  error
- Background tasks get the same process group isolation

## Constraints
- Must not change behavior for normal (non-cancelled, non-timed-out) commands
- Must work on darwin and linux (Setpgid is POSIX)
- No Windows support needed (forge targets unix)

## Interfaces
```go
// cmd setup in bashHandler:
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
cmd.Cancel = func() error {
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
cmd.WaitDelay = 5 * time.Second
```

## Edge Cases
- `tail -f /dev/null` with context cancel → returns within 1s, not infinite
- `tail -f /dev/null` with 500ms timeout → returns within 1s, not infinite
- Normal `echo hello` → unchanged behavior
- Nonzero exit code → unchanged behavior
- Deeply nested child processes (bash → bash → tail) → all killed via pgid
