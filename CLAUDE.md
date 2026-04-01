# forge

Async coding agent — headless Claude Code behind a platform-agnostic HTTP API.

## Architecture

Forge uses a flexible 2-mode architecture:

- **CLI** (`cmd/cli/`) — interactive REPL with two modes:
  - **Interactive mode (default):** spawns local agent, talks directly via HTTP
  - **Server mode:** connects to gateway server via HTTP for persistent sessions
- **Server** (`cmd/server/`) — session management, spawns agents in backends (tmux), forwards messages, relays events. Optional for multi-session deployments.
- **Agent** (`cmd/agent/`) — runs inside a backend target (e.g. tmux session) or as local process. Has its own HTTP server. Runs the conversation loop, tools, talks to Anthropic. Long-lived per session.

## Repository layout

```
cmd/
  server/          entry point (loads .env, starts gateway)
  agent/           agent binary (conversation loop + tools + HTTP server)
  cli/             interactive REPL client
internal/
  types/           shared contracts (messages, events, tools, context)
  tools/           tool registry + implementations (Read, Write, Edit, Bash, Glob, Grep)
  agent/           agent HTTP server, single-session hub, worker
  runtime/
    provider/      LLMProvider interface + Anthropic implementation
    context/       ContextLoader (CLAUDE.md, skills, agents, rules)
    prompt/        system prompt assembly
    session/       JSONL session persistence
    loop/          ConversationLoop (agentic loop)
  server/
    bus/           in-memory event pub/sub + session metadata
    backend/       Backend interface + tmux implementation
    gateway/       HTTP routes, SSE streaming, agent message forwarding
```

Go module: `github.com/jelmersnoeck/forge`

## Build & run

```bash
just build              # build server + cli + agent binaries
just build-agent        # build agent binary only
just dev-server         # build agent + server, run server (foreground)
just dev-server-daemon  # build agent + server, run in daemon mode
just stop-server        # stop daemon server
just tail-server        # tail daemon server logs
just dev-cli            # build + run CLI (interactive mode)
just test               # go test ./...
just vet                # go vet ./...
just up                 # docker compose up --build -d
just down               # docker compose down
just logs               # tail server logs (docker)
just clean              # remove binaries
```

## Running

### Interactive mode (default)
```bash
export ANTHROPIC_API_KEY=sk-...
./forge-cli   # spawns local agent, ephemeral session
```

### Server mode (persistent sessions)
```bash
cp .env.example .env        # set ANTHROPIC_API_KEY
just dev-server             # local dev foreground (reads .env, builds agent first)
just dev-server-daemon      # local dev daemon mode

# In another terminal
./forge-cli --server http://localhost:3000
./forge-cli --server http://localhost:3000 --resume <session-id>

# Manual daemon control:
./forge-server -daemon                           # default: /tmp/forge/sessions/forge.{pid,log}
./forge-server -daemon -pid-file /path/to/file   # custom paths
kill $(cat /tmp/forge/sessions/forge.pid)        # stop
```

## How it works

### Interactive Mode (default)
1. CLI spawns agent as background process (`forge-agent --port 0`)
2. Agent emits `{"port": 12345}` to stdout
3. CLI parses port, connects directly via HTTP
4. CLI sends messages to agent's `/messages` endpoint
5. CLI subscribes to agent's `/events` SSE stream
6. Agent runs ConversationLoop, executes tools, talks to Anthropic
7. On CLI exit, agent process is killed (ephemeral session)

### Server Mode (persistent)
1. CLI sends HTTP requests to the server (`--server` flag)
2. Server creates sessions and manages metadata via an in-memory bus
3. On first message, server spawns an agent in a tmux session via the backend
4. Server forwards messages to the agent's HTTP API
5. Server relays agent SSE events back to CLI subscribers via the bus
6. Agent runs a ConversationLoop that drives the Anthropic Messages API
7. Agent executes tools natively (file ops, exec, ripgrep)
8. Sessions persist as JSONL for resume

## Key files

- `internal/agent/server.go` — agent HTTP server (health, messages, events)
- `internal/agent/hub.go` — single-session message queue + event pub/sub
- `internal/agent/worker.go` — conversation loop runner
- `internal/runtime/loop/loop.go` — agentic conversation loop
- `internal/runtime/context/loader.go` — CLAUDE.md, skills, agents, rules discovery
- `internal/runtime/prompt/prompt.go` — system prompt assembly
- `internal/runtime/provider/anthropic.go` — Anthropic Messages API streaming
- `internal/tools/registry.go` — tool registry + NewDefaultRegistry()
- `internal/tools/*.go` — individual tool implementations
- `internal/server/backend/backend.go` — Backend interface
- `internal/server/backend/tmux.go` — tmux backend implementation
- `internal/server/bus/bus.go` — in-memory event pub/sub + session metadata
- `internal/server/gateway/gateway.go` — HTTP routes, SSE streaming, agent proxy
- `internal/types/types.go` — shared contracts

## API endpoints

### Server (gateway) — server mode only

```
POST   /sessions                      create session (accepts metadata)
GET    /sessions/{sessionId}          get session info
POST   /sessions/{sessionId}/messages send message (forwards to agent)
GET    /sessions/{sessionId}/events   SSE stream of OutboundEvents (relayed from agent)
```

### Agent (per-session) — both modes

```
GET    /health                        health check
POST   /messages                      receive message (from CLI or server)
GET    /events                        SSE stream of OutboundEvents
POST   /interrupt                     interrupt current work
```

## Testing

- Tests use `testing` + `testify/require`
- Table-driven: `tests := map[string]struct{...}`, loop var `tc`
- All tests use real filesystem (`t.TempDir()`), real exec — no mocks
- Grep tests require `rg` (ripgrep) on PATH
- Backend integration tests require `tmux` on PATH and a built `forge-agent` binary

## Environment variables

- `ANTHROPIC_API_KEY` — required by the agent (not the server)
- `GATEWAY_PORT` — server listen port (default: 3000)
- `GATEWAY_HOST` — server listen host (default: 0.0.0.0)
- `WORKSPACE_DIR` — default working directory (default: /tmp/forge/workspace)
- `SESSIONS_DIR` — JSONL session storage (default: /tmp/forge/sessions)
- `AGENT_BIN` — path to forge-agent binary (default: forge-agent)

## Gotchas

- `~/.claude/settings.json` contains model aliases like `opus[1m]` that the
  Anthropic API doesn't understand. Agent filters these — only values
  starting with `claude-` are passed through.
- Server loads `.env` from project root at startup (custom loader in
  `cmd/server/main.go`). Explicit env vars take precedence.
- Anthropic API requires `tool_result` blocks immediately after `tool_use` in
  the message history. The ConversationLoop persists these to session JSONL so
  resume reconstructs valid history.
- The server no longer needs `ANTHROPIC_API_KEY` — the agent handles all LLM
  communication. The key must be set in the environment where agents run.

## Test data

Use TV show Community references for fake data (Troy Barnes, Greendale
Community College, etc.).

## Conventions

- Go, standard library where possible
- `internal/` for all packages (no public API yet)
- Platform-agnostic API: `source` is a free-form string, `metadata` is opaque
- Adapters are external HTTP clients — the server has no platform-specific code
