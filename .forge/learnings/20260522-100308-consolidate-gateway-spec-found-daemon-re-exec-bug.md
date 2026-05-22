# Learnings - 2026-05-22 10:03

- The forge gateway daemon mode is broken: `daemonize()` in `cmd/forge/gateway.go` calls `exec.Command(exe)` with no subcommand args, so the child process runs `runCLI` (interactive mode) instead of `runGateway`. The `FORGE_DAEMON_CHILD=1` env var is set but never checked in `main.go`. Fix: pass `os.Args[1:]` to the child command, or add a `FORGE_DAEMON_CHILD` check in `main()` routing.
- TmuxBackend has a stale agent address problem: when an agent crashes, the `agents` map still holds the dead host:port. The SSE relay goroutine cleans up its own `relays` map but doesn't invalidate the backend's agent entry, so `EnsureAgent` short-circuits and returns the stale address on the next message.
