---
id: bash-idle-watchdog
status: implemented
---
# Bash tool: idle watchdog, progress events, and proper process cleanup

## Description
Three related improvements to the Bash tool:
1. When a command produces no output for a configurable idle period, gather
   process diagnostics and return them to the LLM for reasoning (don't just
   kill blindly).
2. Emit progress events to the TUI so users see that a command is running.
3. Fix process cleanup: use process groups so Ctrl+C / context cancellation
   actually kills child processes instead of orphaning them.

## Context
- `internal/tools/bash.go` — bashHandler rewritten with streaming + watchdog
- `internal/tools/bash_procgroup_unix.go` — setProcGroup (Setpgid + SIGTERM)
- `internal/tools/bash_procgroup_other.go` — no-op for Windows
- `internal/tools/bash_watchdog_test.go` — new tests for all behaviors
- `cmd/forge/cli.go` — tool_progress event rendering + spinner for progress
- `internal/types/types.go` — OutboundEvent (unchanged, uses existing shape)

## Behavior

### A. Process group cleanup (interrupt fix)
1. Set `SysProcAttr.Setpgid = true` so bash + children share a process group.
2. Set `cmd.Cancel` to send SIGTERM to the negative PID (entire group).
3. Set `cmd.WaitDelay = 5s` so Go waits briefly then SIGKILLs if needed.
4. This ensures Ctrl+C (context cancel) kills docker, compilers, etc. —
   not just the parent bash process.

### B. Streaming output capture
1. Replace `cmd.Run()` + buffer with `cmd.Start()` + goroutine reading
   stdout/stderr into a ring buffer via `io.Pipe` / scanner.
2. Each chunk of output resets the idle timer.

### C. TUI progress events
1. While the command is running, emit `tool_progress` events via
   `ctx.Emit()` every ~10s so the TUI shows:
   "Bash: running command... (Xs elapsed, last output Ys ago)"
2. The TUI renders these as an updating status line beneath the tool_use line.

### D. Idle watchdog with LLM investigation
1. Configurable idle timeout (default 30s). Timer resets every time new
   output is received. Hard timeout still applies as backstop.
2. On idle timeout — investigate, don't kill:
   a. Gather diagnostics: check if process is alive, capture its process
      tree (ps children), check listening ports (lsof), check CPU usage.
      All diagnostic commands have their own 5s timeout.
   b. Return to LLM as tool result:
      - Output captured so far
      - Diagnostic summary (process tree, ports, CPU)
      - Message: "Command produced no new output for {N}s. Process is
        still running (PID {pid}). Diagnostics: ..."
      - The PID so LLM can kill via follow-up Bash call
   c. Process is NOT killed. LLM decides next steps.
3. Hard timeout (default 120s): kills the process group and returns
   output + "Command timed out" (existing behavior, now group-aware).
4. Fast commands (complete before any idle check): unaffected.

## Constraints
- Do not change the tool's return type or the ToolResult contract.
- Do not modify the conversation loop (loop.go) — all changes in bash.go
  and cli.go.
- The TUI must handle unknown event types gracefully (it already ignores
  unknown types in handleEvent's switch).
- Do not add new dependencies.
- The idle timeout must not fire for commands that are actively producing
  output (timer resets on each line/chunk).
- Process diagnostics must not themselves hang — use short timeouts on ps/lsof.
- Process group kill must be SIGTERM first, then SIGKILL after WaitDelay.
  Never just SIGKILL — give processes a chance to clean up (docker stop, etc.).

## Interfaces

New event type emitted during execution:
```go
// Emitted periodically while Bash command is running
types.OutboundEvent{
    Type:     "tool_progress",
    ToolName: "Bash",
    Content:  "docker run --rm image cmd (45s elapsed, last output 12s ago)",
}
```

TUI rendering in cli.go handleEvent:
```go
case "tool_progress":
    // Update the last tool_use line or show a transient status
```

Diagnostic info included in ToolResult on idle timeout:
```
Command produced no new output for 30s but is still running.

--- Output so far ---
{captured output}

--- Process diagnostics ---
PID: 48930 (bash -c docker run ...)
Children:
  48931 docker run --rm internal-fly-gateway-test caddy version
Listening ports:
  (none from this process tree)
CPU: 0.1% (idle)

The process is still running. You can:
- Kill it: kill 48930
- Wait longer by re-checking: ps -p 48930
- Investigate: docker logs <container>
```

## Edge Cases
- **Command finishes during diagnostic gathering**: The select loop returns
  on waitDone before idleTimer can fire if both are ready simultaneously.
  If process exits between idle fire and diag commands, ps/lsof just
  return empty (gracefully handled).
- **Command produces output exactly at idle boundary**: Timer reset on
  outputCh wins; no false positive.
- **Very large output**: Cap at 100KB (bashMaxOutputBuffer). Once exceeded,
  truncated flag is set and noted in result.
- **No Emit function**: All emit calls guarded with `if emit != nil`.
- **Multiple idle timeouts**: Only fires once — the handler returns
  immediately on idle, ending the tool call. LLM decides next steps.
- **Platform differences**: Setpgid and negative-PID kill work on both
  Darwin and Linux. Windows gets no-op via build tags.
- **Context cancellation (Ctrl+C)**: SIGTERM sent to process group via
  cmd.Cancel. WaitDelay of 5s gives cleanup time before SIGKILL.
