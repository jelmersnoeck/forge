---
id: gateway
status: active
---
# Forge gateway: session management proxy for persistent agents

## Description
The gateway is a long-running HTTP process that manages multiple agent sessions.
It does not serve LLM traffic itself — it spawns `forge agent` subprocesses
(one per session, each in a tmux window) and proxies messages/events between
HTTP clients and those agents. Clients connect via `forge --gateway URL`.

The `forge server` subcommand and `--server` CLI flag are deprecated aliases
that still work, printing a notice directing users to `forge gateway` /
`--gateway` (absorbed from the rename-server-to-gateway spec, which is now
deleted).

## Context
- `cmd/forge/main.go` — subcommand dispatch; `gateway` and deprecated `server` cases
- `cmd/forge/gateway.go` — `runGateway()`: flag parsing, env loading, daemon mode, signal handling, `gateway.Start()`
- `cmd/forge/gateway_test.go` — unit tests for `buildChildArgs`, `resolveDaemonPath`, `checkPIDFile`, `appendIfMissing`, `writePIDFile`
- `cmd/forge/cli.go` — `--gateway` and deprecated `--server` flags; `createSession()` helper
- `internal/server/gateway/gateway.go` — HTTP routes, SSE relay from agent → bus → client
- `internal/server/backend/backend.go` — `Backend` interface (`EnsureAgent`, `StopAgent`, `AgentAddress`, `Close`)
- `internal/server/backend/tmux.go` — `TmuxBackend`: spawns agents as tmux windows, health-checks, cleanup
- `internal/server/bus/bus.go` — in-memory event pub/sub + session metadata store (package-level globals)
- `internal/types/types.go` — `SessionMeta`, `InboundMessage`, `OutboundEvent`
- `internal/envutil/env.go` — `.env` loader (first-found wins, never overrides existing vars)
- `justfile` — `dev-gateway`, `dev-gateway-daemon`, `stop-gateway`, `tail-gateway`, `gateway-status` recipes

## Behavior

### Subcommand

- `forge gateway` — starts the gateway HTTP server in the foreground; blocks until killed.
- `forge gateway -daemon` — starts the gateway in the background; writes PID and log files, parent exits immediately.
- `forge server` — deprecated alias; prints `note: 'forge server' is deprecated, use 'forge gateway'` to stderr, then runs `runGateway`.

### Startup sequence

1. Parse flags.
2. `envutil.LoadEnv(".")` — loads `.env` from CWD (first found), falls back to executable dir. Existing env vars are never overridden.
3. Resolve env vars: `GATEWAY_PORT` (default 3000), `GATEWAY_HOST` (default `0.0.0.0`), `WORKSPACE_DIR` (default `/tmp/forge/workspace`), `SESSIONS_DIR` (default `/tmp/forge/sessions`), `FORGE_BIN` (default `forge`).
4. `os.MkdirAll` for workspace and sessions dirs.
5. Create `TmuxBackend` with `forgeBin`, a random `gatewayID` (8-char UUID prefix), and `workspaceDir`.
6. Register SIGINT/SIGTERM handler that calls `be.Close()` and exits 0.
7. Log startup info (gateway ID, workspace, sessions dir, forge bin).
8. Call `gateway.Start(cfg)` which binds to `host:port` via `http.ListenAndServe`.

### Shutdown

- On SIGINT/SIGTERM: calls `TmuxBackend.Close()` which kills all agent tmux windows and the tmux session, removes worktrees, then exits.
- On `gateway.Start` error: calls `be.Close()`, logs, returns exit code 1.

### HTTP endpoints

All endpoints are registered on `gateway.Start()`:

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/sessions` | `handleCreateSession` | Create a new session |
| GET | `/sessions/{sessionId}` | `handleGetSession` | Get session metadata |
| POST | `/sessions/{sessionId}/messages` | `handleSendMessage` | Send message (lazy-starts agent) |
| POST | `/sessions/{sessionId}/review` | `handleReview` | Trigger code review (lazy-starts agent) |
| POST | `/sessions/{sessionId}/interrupt` | `handleInterrupt` | Interrupt agent's current work |
| GET | `/sessions/{sessionId}/events` | `handleEvents` | SSE event stream |

#### POST /sessions

Request body (JSON, optional):
```json
{ "cwd": "/path/to/workspace", "metadata": { "key": "value" } }
```
Response (201 Created):
```json
{ "sessionId": "<uuid>", "metadata": { ... } }
```
Generates a UUIDv4 session ID, stores `SessionMeta` in the bus.

#### GET /sessions/{sessionId}

Response (200 OK): full `SessionMeta` as JSON.
Response (404): `{"error":"session not found"}` if unknown ID.

#### POST /sessions/{sessionId}/messages

Request body (JSON):
```json
{ "text": "string (required)", "user": "string (default: anonymous)", "source": "string (default: api)", "metadata": {} }
```
Response (202 Accepted): agent's response forwarded as JSON (typically `{"status":"queued"}`).

Side effects:
1. Calls `Backend.EnsureAgent()` — starts agent in tmux if not running.
2. Calls `startRelay()` — idempotent; connects to agent's `/events` SSE and republishes via bus.
3. Forwards the message to the agent's `POST /messages`.
4. Updates `SessionMeta.LastActiveAt`.

Returns 400 if `text` is empty, 500 if agent fails to start, 502 if agent forward fails.

#### POST /sessions/{sessionId}/review

Request body (JSON, optional): `{"base": "main"}`.
Response (202 Accepted): `{"status":"review_started"}`.
Ensures agent is running, starts relay, forwards to agent's `POST /review`.

#### POST /sessions/{sessionId}/interrupt

Response (202 Accepted): `{"status":"interrupted"}` — forwards to agent's `POST /interrupt`.
Response (200 OK): `{"status":"no active agent"}` if no agent is running for this session.

#### GET /sessions/{sessionId}/events

SSE stream. Headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
Each event: `id: <eventID>\ndata: <JSON OutboundEvent>\n\n`.
Uses `bus.Subscribe()` — channel size 64. Slow subscribers have events dropped.
Closes when the client disconnects (`r.Context().Done()`).

### Agent spawning

The gateway does NOT start agents on session creation. Agents are lazy-started on the first `POST /sessions/{id}/messages` or `POST /sessions/{id}/review`.

`TmuxBackend.EnsureAgent()`:
1. If agent already known, return cached address.
2. Ensure the shared tmux session `forge-<gatewayID>` exists.
3. Create a git worktree for the session (via `WorktreeManager`).
4. Find a free TCP port.
5. `tmux new-window` inside the session, running `forge agent --port N --cwd <worktree> --session-id <id> --sessions-dir <dir>`.
6. Health-poll `GET /health` on the agent (up to 50 attempts × 100ms = 5s).
7. Cache the agent address on success.

### SSE relay

`startRelay()` is idempotent per session. On first call, starts a goroutine that:
1. GETs `http://<agentAddr>/events`.
2. Scans for `data: ` lines, unmarshals `OutboundEvent`, and calls `bus.PublishEvent()`.
3. On disconnect/error, removes itself from the relay map.

### Client-side connection

- `forge --gateway URL` — connect to a remote gateway.
- `forge --gateway URL --resume SESSION_ID` — resume an existing session.
- Deprecated: `forge --server URL` prints a deprecation notice and maps to `--gateway`.
- On error connecting, the CLI prints a hint: `hint: start the gateway with 'just dev-gateway'`.
- The CLI creates a session via `POST /sessions`, then sends messages via `POST /sessions/{id}/messages` and subscribes to `GET /sessions/{id}/events`.

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAY_PORT` | `3000` | HTTP listen port |
| `GATEWAY_HOST` | `0.0.0.0` | HTTP listen host |
| `WORKSPACE_DIR` | `/tmp/forge/workspace` | Default workspace for agents |
| `SESSIONS_DIR` | `/tmp/forge/sessions` | JSONL session storage |
| `FORGE_BIN` | `forge` | Path to forge binary (resolved to absolute path by TmuxBackend) |
| `FORGE_RUN_DIR` | (unset) | Directory for daemon PID/log files; takes precedence over `SESSIONS_DIR` |

Precedence: explicit env vars > `.env` file values. `envutil.LoadEnv(".")` never overrides existing vars.

### Daemon mode

`forge gateway -daemon` runs the gateway in the background.

**Flags:**
- `-daemon` — fork into background; parent prints PID/log paths and exits.
- `-pid-file PATH` — explicit PID file path.
- `-log-file PATH` — explicit log file path.

**PID/log file resolution order:**
1. `-pid-file PATH` / `-log-file PATH` flag if provided.
2. `$FORGE_RUN_DIR/forge.pid` / `$FORGE_RUN_DIR/forge.log` if `FORGE_RUN_DIR` is set.
3. `$SESSIONS_DIR/forge.pid` / `$SESSIONS_DIR/forge.log` as final fallback (default: `/tmp/forge/sessions`).

**Re-exec mechanism (approach (a)):** The parent process calls `exec.Command(exe, childArgs...)` where `childArgs` is `os.Args[1:]` with `-daemon` stripped. This means the child sees `gateway -pid-file ... -log-file ...` and enters `runGateway` in foreground mode. The `FORGE_DAEMON_CHILD=1` env var is set on the child so the child knows to write a PID file even though `-pid-file` was injected rather than user-supplied.

**PID file lifecycle:**
1. Before forking, `checkPIDFile()` reads any existing PID file.
2. If the file exists and the PID is alive (`syscall.Kill(pid, 0)`), abort with error: `gateway already running (pid <N>, see <path>)`.
3. If the file exists but the PID is dead or the file is corrupt, remove the stale file and continue.
4. The child writes the PID file after startup.
5. On clean exit (SIGINT/SIGTERM) or `gateway.Start` error, the PID file is removed via `defer` and signal handler.

**Log file:** Opened in append mode (`O_CREATE|O_WRONLY|O_APPEND`). Log rotation is the user's responsibility (e.g., `logrotate`). Not built in.

**Parent directories:** Both PID file and log file directories are created with `os.MkdirAll` if they don't exist.

### Justfile recipes

| Recipe | Command | Description |
|--------|---------|-------------|
| `dev-gateway` | `./forge gateway` | Build + run gateway foreground |
| `dev-gateway-daemon` | `./forge gateway -daemon` | Build + run gateway daemon |
| `stop-gateway` | `kill $(cat <pidFile>)` | Stop daemon gateway |
| `tail-gateway` | `tail -f <logFile>` | Tail daemon logs |
| `gateway-status` | print PID/status/log | Show daemon status |

The `stop-gateway`, `tail-gateway`, and `gateway-status` recipes resolve paths using the same `FORGE_RUN_DIR > SESSIONS_DIR > /tmp/forge/sessions` fallback chain via justfile variables.

All build recipes depend on `build` (which runs `go build -o forge ./cmd/forge`).

## Constraints
- The gateway does not serve LLM traffic — it proxies to `forge agent` subprocesses.
- The `internal/server/` package tree is NOT renamed (even though the subcommand is `gateway`).
- `GATEWAY_PORT` and `GATEWAY_HOST` env vars keep their names (already correct).
- `forge server` and `--server` must continue working with deprecation notices — do not remove.
- Session metadata is in-memory only (`bus` package globals) — gateway restart loses all sessions.
- The bus drops events for slow SSE subscribers (non-blocking send on channel size 64).
- The tmux backend propagates the gateway's full environment to the tmux session so agent windows inherit it.

## Interfaces

```go
// gateway.Config — passed to gateway.Start()
type Config struct {
    Port         int
    Host         string
    WorkspaceDir string
    SessionsDir  string
    Backend      backend.Backend
}

// backend.Backend — agent lifecycle
type Backend interface {
    EnsureAgent(ctx context.Context, sessionID string, opts AgentOptions) (string, error)
    StopAgent(ctx context.Context, sessionID string) error
    AgentAddress(sessionID string) string
    Close() error
}

type AgentOptions struct {
    CWD         string
    SessionsDir string
}

// types.SessionMeta — stored in bus
type SessionMeta struct {
    SessionID    string         `json:"sessionId"`
    HistoryID    string         `json:"historyId,omitempty"`
    CWD          string         `json:"cwd,omitempty"`
    Metadata     map[string]any `json:"metadata"`
    CreatedAt    int64          `json:"createdAt"`
    LastActiveAt int64          `json:"lastActiveAt"`
}

// Gateway HTTP endpoints
// POST   /sessions                         → 201 { sessionId, metadata }
// GET    /sessions/{sessionId}             → 200 SessionMeta | 404
// POST   /sessions/{sessionId}/messages    → 202 { status } | 400 | 500 | 502
// POST   /sessions/{sessionId}/review      → 202 { status: "review_started" }
// POST   /sessions/{sessionId}/interrupt   → 202 { status: "interrupted" } | 200 { status: "no active agent" }
// GET    /sessions/{sessionId}/events      → SSE stream of OutboundEvent
```

## Edge Cases
- Gateway receives message for unknown session ID — `handleSendMessage` calls `bus.GetSession` which returns nil; CWD falls back to `cfg.WorkspaceDir`. Agent starts normally, session just has no metadata entry.
- Agent fails to start (tmux unavailable, port conflict) — `EnsureAgent` returns error; gateway returns 500 with error message. Tmux window is killed on health timeout.
- SSE client disconnects — `r.Context().Done()` fires; `unsub()` removes channel from bus, closes it. No goroutine leak.
- Multiple simultaneous messages to same session — `EnsureAgent` is idempotent (mutex-guarded). `startRelay` is idempotent (relay map check). Second message still forwarded; agent queues it.
- Agent crashes mid-session — relay goroutine sees EOF on SSE stream, cleans up. Next message to that session will re-start the agent via `EnsureAgent` (agent map entry was removed by relay? No — relay only removes itself from `relays` map, not from `agents` map; the dead agent's cached address will be reused, and the forward will fail with 502).
- Gateway receives SIGINT during active sessions — signal handler calls `be.Close()` which kills all tmux windows and the tmux session. Sessions are lost (in-memory only).
- `.env` file missing — `envutil.LoadEnv` silently continues; env vars use defaults.
- `FORGE_BIN` points to nonexistent binary — `TmuxBackend` resolves path at creation; `EnsureAgent` will fail when tmux tries to run the command.
- Stale PID file from crash — `checkPIDFile()` detects the dead PID via `syscall.Kill(pid, 0)`, removes the file, and proceeds with a new daemon.
- PID file in non-existent directory — `os.MkdirAll(filepath.Dir(pidFile))` creates parent dirs before writing.
- Corrupt PID file (non-numeric content) — detected by `strconv.Atoi` failure; file is removed and startup proceeds.
- Log file rotation — deferred to the user (logrotate, etc.). The daemon opens the log in append mode.

## Compatibility
- `forge server` dispatches to `runGateway` with a deprecation notice on stderr.
- `--server URL` sets `gatewayFlag` with a deprecation notice on stderr; if both `--server` and `--gateway` are provided, `--gateway` takes precedence.
- Old justfile recipes (`dev-server`, etc.) no longer exist — removed during the rename. Only `dev-gateway` recipe is present.

## Known Issues

**Agent crash does not invalidate cached address.** When an agent process crashes, the `TmuxBackend.agents` map still holds the stale address. The next message forward will fail with a connection error (502 to client). The agent is not automatically restarted because `EnsureAgent` short-circuits on finding the cached entry. The relay goroutine cleans up from the `relays` map but does not remove the agent from the backend's `agents` map.
