# forge

Async coding agent — headless Claude Code behind a platform-agnostic HTTP API.

## Architecture

Forge uses a unified binary architecture with subcommands:

- **Unified Binary** (`cmd/forge/`) — single entry point with subcommands:
  - **`forge` (default)** — interactive REPL (spawns agent subprocess)
  - **`forge agent`** — run agent server (spawned by interactive mode or server mode)
  - **`forge server`** — session management gateway (spawns agents via `forge agent`)
  - **`forge stats`** — cost analytics (daily/monthly/session breakdowns)
- **Legacy binaries** (`cmd/{cli,agent,server}/`) — deprecated, for backward compatibility only

## Cost Tracking

All API usage is tracked to `~/.forge/costs.db` (SQLite):
- Every API call records: timestamp, session_id, model, tokens, cost
- Query historical costs by day/week/month
- Per-session breakdowns with duration tracking
- Zero overhead — failures don't block sessions

Usage:
```bash
forge stats                    # current month summary + daily breakdown
forge stats --month 2026-04    # specific month
forge stats --week             # current week
forge stats --sessions         # per-session breakdown
forge stats --daily --sessions # both views
```

## MCP Integration

### MCP Server (exposing Forge's tools)

Forge includes a Model Context Protocol (MCP) server that exposes Forge's tools to any MCP-compatible client (Claude Desktop, Claude Code, VS Code, etc.).

See `mcp-server/README.md` for setup and usage.

Quick start:
```bash
just build-mcp
just mcp         # STDIO mode (local)
just mcp-http    # HTTP mode (remote)
```

### MCP Client (connecting to remote MCP servers)

The agent can connect to remote MCP servers over HTTP (Streamable HTTP transport),
discover their tools, and expose them to the LLM alongside built-in tools.
Tools from MCP servers are namespaced as `mcp__<serverName>__<toolName>`.

Configuration in `~/.forge/mcp.json` (user-level) or `.forge/mcp.json` (project-level):
```json
{
  "mcpServers": {
    "my-server": {
      "url": "https://example.com/mcp",
      "auth": "oauth"
    },
    "simple-server": {
      "url": "https://simple.example.com/mcp",
      "headers": { "Authorization": "Bearer sk-..." }
    }
  }
}
```

Authentication modes:
- `"auth": "oauth"` — Full OAuth 2.1 with Dynamic Client Registration (RFC 7591),
  PKCE authorization, and automatic token refresh. Tokens stored at `~/.forge/mcp-tokens.json`.
- `"headers"` — Static headers (API keys, pre-configured bearer tokens).

**Lazy tool loading:** MCP tool schemas are NOT sent to the LLM on every API call.
Instead, a single `UseMCPTool` gateway tool (~300 tokens) lets the model discover
and invoke MCP tools on demand via `list_servers` → `list_tools` → `call`. This
avoids the ~15K+ token overhead per MCP server that would otherwise bloat every
request's tool definitions.

Key files:
- `internal/mcp/client.go` — JSON-RPC 2.0 over Streamable HTTP, SSE support
- `internal/mcp/oauth.go` — OAuth 2.1 + DCR + PKCE flow
- `internal/mcp/bridge.go` — connects to MCP servers, caches tool catalogs in Store
- `internal/mcp/store.go` — holds MCP clients + tool catalogs for lazy access
- `internal/mcp/config.go` — config loading (user + project merge)
- `internal/mcp/token_store.go` — persistent OAuth token storage
- `internal/tools/mcp_gateway.go` — UseMCPTool: single gateway tool for lazy MCP access

## Spec-Driven Development

Forge is spec-driven. The agent writes a spec before implementing any feature.
Specs live in `.forge/specs/` (configurable via `.forge/config.json` `specsDir`).
Use `forge --spec path/to/spec.md` to implement an existing spec directly.

Key files:
- `internal/spec/spec.go` — parser and loader
- `internal/config/config.go` — forge config loader
- `internal/runtime/prompt/prompt.go` — spec instructions in system prompt

## Repository layout

```
cmd/
  forge/           unified binary (cli + server + agent + stats)
  server/          legacy server binary (use 'forge server' instead)
  agent/           agent binary (still needed by server backend)
  cli/             legacy CLI binary (use 'forge' instead)
mcp-server/        MCP server (TypeScript) — exposes Forge as MCP server
  src/
    server.ts      MCP server implementation
    index.ts       STDIO entrypoint
    http.ts        HTTP entrypoint
internal/
  mcp/             MCP client (Go) — connects to remote MCP servers
    client.go      JSON-RPC over Streamable HTTP transport
    oauth.go       OAuth 2.1 + DCR + PKCE
    bridge.go      bridges MCP tools into Forge's tool registry
    config.go      config loading (~/.forge/mcp.json)
    token_store.go persistent OAuth token storage
  config/          forge-level configuration (.forge/config.json)
  spec/            feature specification loader + parser
  types/           shared contracts (messages, events, tools, context, tasks)
  tools/           tool registry + implementations
  agent/           agent HTTP server, single-session hub, worker
  runtime/
    provider/      LLMProvider interface + Anthropic implementation
    context/       ContextLoader (CLAUDE.md, AGENTS.md, specs, skills, agents, rules)
    prompt/        system prompt assembly
    session/       JSONL session persistence
    loop/          ConversationLoop (agentic loop)
    cost/          cost calculation + SQLite tracker
    task/          background task & sub-agent management
  server/
    bus/           in-memory event pub/sub + session metadata
    backend/       Backend interface + tmux implementation
    gateway/       HTTP routes, SSE streaming, agent message forwarding
```

Go module: `github.com/jelmersnoeck/forge`

## Build & run

```bash
just build              # build unified forge binary
just build-all          # build forge + legacy binaries
just dev                # build + run interactive CLI
just dev-server         # build + run server (foreground)
just dev-server-daemon  # build + run server daemon
just stop-server        # stop daemon server
just tail-server        # tail daemon server logs
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
forge                    # spawns local agent, ephemeral session
forge --skip-worktree    # skip worktree creation, run in current directory
```

**Git Worktree Isolation**: By default, when running in interactive mode from a git repository (and not already in a worktree), Forge automatically:
- Creates a temporary worktree in `/tmp/forge/worktrees/<session-id>`
- Creates a branch `jelmer/<session-id>` from your current HEAD
- Runs the agent in that isolated worktree
- Cleans up the worktree and branch on exit

This ensures each Forge session has its own isolated workspace, preventing conflicts when running multiple sessions or when your main working tree is in use.

Use `--skip-worktree` to disable this behavior and run directly in your current directory.

### Server mode (persistent sessions)
```bash
cp .env.example .env        # set ANTHROPIC_API_KEY
just dev-server             # local dev foreground (reads .env, builds agent first)
just dev-server-daemon      # local dev daemon mode

# In another terminal
forge --server http://localhost:3000
forge --server http://localhost:3000 --resume <session-id>

# Manual daemon control:
forge server -daemon                           # default: /tmp/forge/sessions/forge.{pid,log}
forge server -daemon -pid-file /path/to/file   # custom paths
kill $(cat /tmp/forge/sessions/forge.pid)      # stop
```

### Cost analytics
```bash
forge stats                    # current month
forge stats --month 2026-04    # specific month
forge stats --week             # current week
forge stats --sessions         # per-session breakdown
```

## How it works

### Interactive Mode (default)
1. CLI spawns agent as subprocess: `forge agent --port 0`
2. Agent emits `{"port": 12345}` to stdout
3. CLI parses port, connects directly via HTTP
4. CLI sends messages to agent's `/messages` endpoint
5. CLI subscribes to agent's `/events` SSE stream
6. Agent runs ConversationLoop, executes tools, talks to Anthropic
7. On CLI exit, agent subprocess is terminated (ephemeral session)

### Server Mode (persistent)
1. CLI sends HTTP requests to the gateway server (`--server` flag)
2. Server creates sessions and manages metadata via an in-memory bus
3. On first message, server spawns agent in tmux: `forge agent --port X`
4. Server forwards messages to the agent's HTTP API
5. Server relays agent SSE events back to CLI subscribers via the bus
6. Agent runs ConversationLoop, executes tools, talks to Anthropic
7. Sessions persist as JSONL for resume

## Key files

- `internal/agent/server.go` — agent HTTP server (health, messages, events)
- `internal/agent/hub.go` — single-session message queue + event pub/sub
- `internal/agent/worker.go` — conversation loop runner
- `internal/runtime/loop/loop.go` — agentic conversation loop
- `internal/runtime/context/loader.go` — CLAUDE.md, AGENTS.md, skills, agents, rules discovery
- `internal/runtime/prompt/prompt.go` — system prompt assembly
- `internal/runtime/provider/anthropic.go` — Anthropic Messages API streaming
- `internal/runtime/task/manager.go` — background task & sub-agent manager
- `internal/tools/registry.go` — tool registry + NewDefaultRegistry()
- `internal/tools/*.go` — tool implementations (Read, Write, Edit, Bash, Grep, Glob, WebSearch, Reflect, TaskCreate, TaskGet, TaskList, TaskStop, TaskOutput, Agent, AgentGet, AgentList, AgentStop, UseMCPTool)
- `internal/server/backend/backend.go` — Backend interface
- `internal/server/backend/tmux.go` — tmux backend implementation
- `internal/server/bus/bus.go` — in-memory event pub/sub + session metadata
- `internal/server/gateway/gateway.go` — HTTP routes, SSE streaming, agent proxy
- `internal/types/types.go` — shared contracts
- `internal/types/task.go` — task & sub-agent types

## Self-Improvement Loop

Forge includes a built-in self-improvement mechanism:

1. **AGENTS.md Support**: The agent loads `AGENTS.md` files (similar to `CLAUDE.md`) from:
   - `~/AGENTS.md` (user-level learnings)
   - `$PROJECT/AGENTS.md` or `$PROJECT/.claude/AGENTS.md` (project-level)
   - Parent directories (inheritable learnings)
   - `$PROJECT/AGENTS.local.md` (session-specific)

2. **Reflect Tool**: Agents can use the `Reflect` tool to capture session learnings:
   ```json
   {
     "summary": "What was accomplished",
     "mistakes": ["Things that went wrong"],
     "successes": ["Patterns that worked well"],
     "suggestions": ["Ideas for future improvement"]
   }
   ```

3. **Automatic Loading**: Learnings from `AGENTS.md` are injected into the system prompt with cache control, so the agent remembers and applies lessons from previous sessions.

4. **Continuous Learning**: Each reflection is appended to `AGENTS.md`, creating a growing knowledge base that improves agent behavior over time.

Example workflow:
- Agent completes a task
- Agent calls `Reflect` tool with session summary
- Learnings are saved to `AGENTS.md`
- Next session loads `AGENTS.md` and applies learnings

The system prompt encourages agents to reflect at the end of sessions.

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
- `FORGE_BIN` — path to forge binary (default: forge)

## Gotchas

- `~/.claude/settings.json` contains model aliases like `opus[1m]` that the
  Anthropic API doesn't understand. Agent filters these — only values
  starting with `claude-` are passed through.
- Server loads `.env` from project root at startup (custom loader in
  `cmd/forge/server.go`). Explicit env vars take precedence.
- Anthropic API requires `tool_result` blocks immediately after `tool_use` in
  the message history. The ConversationLoop persists these to session JSONL so
  resume reconstructs valid history.
- The server spawns agents using the unified `forge agent` subcommand. The
  binary path can be customized via `FORGE_BIN` env var.

## Test data

Use TV show Community references for fake data (Troy Barnes, Greendale
Community College, etc.).

## Git commands

- **NEVER use git commands with the -i flag** (like `git rebase -i`, `git add -i`, `git commit --interactive`) since they require interactive input which is not supported
- The Bash tool sets `GIT_EDITOR=true` to prevent git from opening editors, so commands like `git rebase` or `git commit --amend` will work non-interactively
- Always use `-m` flag for `git commit` to provide messages inline
- For complex rebases, use explicit commands like `git rebase --onto` with SHA arguments rather than interactive mode

## Conventions

- Go, standard library where possible
- `internal/` for all packages (no public API yet)
- Platform-agnostic API: `source` is a free-form string, `metadata` is opaque
- Adapters are external HTTP clients — the server has no platform-specific code
