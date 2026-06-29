---
id: bash-idle-watchdog
status: active
---
# Bash tool: idle watchdog, progress events, real-time output, and process cleanup

## Description
Four related improvements to the Bash tool:
1. When a command produces no output for a configurable idle period, gather
   process diagnostics and return them to the LLM for reasoning (don't just
   kill blindly).
2. Emit progress events to the TUI so users see that a command is running.
3. Stream live output to the TUI in real time so users see build/test/download
   progress instead of an opaque "running..." line (issue #252).
4. Fix process cleanup: use process groups so Ctrl+C / context cancellation
   actually kills child processes instead of orphaning them.

## Context
- `internal/tools/bash.go` — bashHandler with streaming + watchdog +
  live-output tool_progress emits; `lastNonEmptyLine` helper extracts the
  latest output line; `bashStreamThrottle` const bounds emit rate
- `internal/tools/bash_procgroup_unix.go` — setProcGroup (Setpgid + SIGTERM)
- `internal/tools/bash_procgroup_other.go` — no-op for Windows
- `internal/tools/bash_watchdog_test.go` — tests for all behaviors, incl.
  TestBashLiveOutputStreaming and TestLastNonEmptyLine
- `cmd/forge/cli.go` — tool_progress sets model.toolProgress (spinner line)
- `cmd/forge/events.go` — tool_progress handled (no output mutation; surfaced
  via model.toolProgress)
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

### C. TUI progress events and real-time output streaming
1. While the command is running with no new output, emit a heartbeat
   `tool_progress` event via `ctx.Emit()` every ~10s
   (`bashProgressInterval`):
   "command... (Xs elapsed, no output for Ys)".
2. As output arrives, emit a live `tool_progress` event carrying the most
   recent non-empty output line:
   "command (Xs elapsed) <latest output line>".
3. Live-output emits are throttled to at most one per `bashStreamThrottle`
   (100ms). This is the backpressure mechanism: the TUI status line is
   last-write-wins, so a fast producer can never flood the client — extra
   chunks are coalesced into the buffer and only the latest line is shown
   at the next throttle window.
4. The TUI renders all `tool_progress` events as an updating single-line
   status beneath the spinner (model.toolProgress in cmd/forge/cli.go).
   No new event type or TUI scrollback rendering is required — live output
   reuses the existing `tool_progress` path.
5. The full output is still captured in the buffer and returned in the final
   ToolResult (truncated to bashMaxOutputBuffer for LLM context); streaming
   only affects what the user sees live, not what the LLM receives.

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
- Live-output streaming must NOT introduce a new event type or new TUI
  scrollback rendering — reuse the existing `tool_progress` single-line
  status path. (Rationale: minimal surface area, automatic backpressure.)
- Live-output emits must be throttled (>= bashStreamThrottle between emits)
  so a fast producer cannot flood the event stream.
- Streaming must not change what the LLM receives: the final ToolResult
  content is identical to the non-streaming behavior.
- The concurrent read of the latest output line and the buffer must be
  guarded by the same mutex (outputMu) — no data race (verified with -race).

## Interfaces

New event types emitted during execution:
```go
// Heartbeat (no recent output) — every bashProgressInterval
types.OutboundEvent{
    Type:     "tool_progress",
    ToolName: "Bash",
    Content:  "docker run --rm image cmd (45s elapsed, no output for 12s)",
}

// Live output — on output arrival, throttled to bashStreamThrottle
types.OutboundEvent{
    Type:     "tool_progress",
    ToolName: "Bash",
    Content:  "go test ./... (8s elapsed) ok  github.com/foo/bar  0.4s",
}
```

Live-output line extraction helper:
```go
// lastNonEmptyLine returns the last non-empty, whitespace/CR-trimmed line
// in a chunk of output, or "" if none. Used for the live status line.
func lastNonEmptyLine(chunk []byte) string
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
- **Fast producer floods output**: Live-output emits are throttled to one per
  bashStreamThrottle; intermediate chunks are coalesced in the buffer and only
  the newest line is shown at the next window. No event-stream flooding.
- **Output chunk with no printable line** (only whitespace/newlines):
  lastNonEmptyLine returns "" and no live-output event is emitted; the idle
  timer still resets since bytes arrived.
- **Output split mid-line across reads**: The live line shows whatever the
  latest chunk's last non-empty line is; it may be a partial line until the
  next chunk. This is acceptable for a transient status display.
- **Binary/no-newline output**: lastNonEmptyLine returns the trimmed chunk as
  a single line; truncated to 80 chars for display.
