# Forge

Async coding agent — headless HTTP API with an Anthropic backend.

## Features

- **Full-screen TUI** - Always-available input, message queuing, real-time events
- **Message Queuing** - Queue multiple messages while agent is working
- **Task Queues** - Agent can queue commands to run after each tool or on completion
- **Session Persistence** - Resume conversations anytime
- **Cost Tracking** - Automatic API cost tracking with analytics (daily/monthly/session breakdowns)
- **Streaming** - Real-time event stream via Server-Sent Events (SSE)
- **Tool Execution** - Read, Write, Edit, Bash, Glob, Grep, WebSearch, and Queue tools
- **MCP Client** - Connect to remote MCP servers over HTTP (Streamable HTTP transport)

## Quick Start

### Interactive Mode (Default)

The simplest way to use Forge — everything runs locally in a single command:

```bash
# Set your API key
export ANTHROPIC_API_KEY=sk-...

# Run interactive CLI (spawns agent automatically)
just dev

# Or build and run manually
just build
./forge
```

The CLI automatically spawns a background agent process and connects directly to it. Sessions are ephemeral (no persistence between runs).

### Gateway Mode (Multi-Session)

For persistent sessions and multi-user deployments:

```bash
# Terminal 1: Start the gateway
just dev-gateway

# Terminal 2: Connect CLI to gateway
./forge --gateway http://localhost:3000

# Resume a session later
./forge --gateway http://localhost:3000 --resume <session-id>
```

Gateway mode gives you:
- Session persistence (resume anytime)
- Multiple concurrent sessions
- Remote gateway support

### Cost Analytics

Track your API usage across all sessions:

```bash
# Current month summary
./forge stats

# Specific month
./forge stats --month 2026-04

# Current week
./forge stats --week

# Per-session breakdown
./forge stats --sessions

# Both daily and session views
./forge stats --daily --sessions
```

Cost data is stored in `~/.forge/costs.db` and tracked automatically for every API call.

## Architecture

Forge supports two deployment modes:

### Interactive Mode (Local)
```
┌──────────┐
│   CLI    │ ──spawns──→ ┌──────────┐
│  (TUI)   │ ←──HTTP──── │  Agent   │
└──────────┘             │ (process)│
                         └──────────┘
```

CLI spawns agent as background process, connects directly via HTTP. Ephemeral sessions.

### Gateway Mode
```
┌──────────┐         ┌──────────┐         ┌──────────┐
│   CLI    │ ──HTTP─→│ Gateway  │ ──HTTP─→│  Agent   │
│  (TUI)   │ ←─SSE──┤          │ ←─SSE──┤  (tmux)  │
└──────────┘         └──────────┘         └──────────┘
```

Gateway manages multiple sessions, spawns agents in tmux, persists history.

## Project Structure

```
cmd/
  forge/           Unified binary (cli + gateway + agent + stats)
  server/          Legacy server (use 'forge gateway')
  agent/           Agent binary (still used by server backend)
  cli/             Legacy CLI (use 'forge')
internal/
  agent/           Agent HTTP server, hub, worker
  runtime/
    provider/      LLM provider (Anthropic)
    context/       AGENTS.md loader
    prompt/        System prompt assembly
    session/       JSONL persistence
    loop/          Conversation loop
    cost/          Cost tracking + SQLite database
  server/
    bus/           Event pub/sub
    backend/       Backend interface (tmux)
    gateway/       HTTP routes, SSE
  tools/           Tool registry + implementations
  types/           Shared contracts
```

## Tools

Built-in tools available to the agent:

| Tool | Description |
|------|-------------|
| `Read` | Read file contents with line numbers |
| `Write` | Create or overwrite files |
| `Edit` | String replacement in files |
| `Bash` | Execute shell commands |
| `Glob` | Fast file pattern matching |
| `Grep` | Search using ripgrep |
| `WebSearch` | Search the web for information |
| `Reflect` | Capture session learnings for self-improvement |
| `QueueImmediate` | Queue command after each tool |
| `QueueOnComplete` | Queue command on completion |
| `TaskCreate` | Run background tasks asynchronously |
| `Agent` | Spawn sub-agents with tool restrictions |
| `UseMCPTool` | Gateway to external MCP tool servers |

## API Endpoints

### Gateway
```
POST   /sessions                      Create session
GET    /sessions/{sessionId}          Get session info
POST   /sessions/{sessionId}/messages Send message
GET    /sessions/{sessionId}/events   SSE event stream
```

### Agent (Per-Session)
```
GET    /health                        Health check
POST   /messages                      Receive message
GET    /events                        SSE event stream
POST   /interrupt                     Interrupt current work
```

## Environment Variables

```bash
# Required (for agent)
ANTHROPIC_API_KEY=sk-...    # Anthropic API key

# Gateway Mode Only
GATEWAY_PORT=3000           # Gateway listen port
GATEWAY_HOST=0.0.0.0        # Gateway listen host
WORKSPACE_DIR=/tmp/forge/workspace  # Working directory
SESSIONS_DIR=/tmp/forge/sessions    # Session storage
FORGE_BIN=forge             # Forge binary path
```

## Development

```bash
# Build unified binary
just build

# Build all binaries (including legacy)
just build-all

# Run in development mode
just dev               # Interactive CLI
just dev-gateway       # Gateway mode
just dev-gateway-daemon # Gateway daemon mode

# Tests
just test              # Run all tests
just vet               # Run go vet
```

## Configuration

### .env File
```bash
cp .env.example .env
# Edit .env with your ANTHROPIC_API_KEY
```

The gateway loads `.env` from the project root at startup.

### AGENTS.md Files

The agent loads context from `AGENTS.md` files:

- `~/AGENTS.md` - User-level instructions
- `./AGENTS.md` - Project-level instructions  
- `./AGENTS.local.md` - Local overrides (gitignored)

## Testing

All tests use real filesystem and processes (no mocks):

```bash
go test ./...
```

Test data uses Community TV show references (Troy Barnes, Greendale, etc.).

## Requirements

- Go 1.26.1+
- tmux (for gateway backend)
- ripgrep (for Grep tool)
- Terminal with ANSI color support (for CLI)
