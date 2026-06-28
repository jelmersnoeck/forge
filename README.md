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

```
┌──────────┐
│   CLI    │ ──spawns──→ ┌──────────┐
│  (TUI)   │ ←──HTTP──── │  Agent   │
└──────────┘             │ (process)│
                         └──────────┘
```

CLI spawns agent as background process, connects directly via HTTP. Ephemeral sessions.

## Project Structure

```
cmd/
  forge/           Unified binary (cli + agent + stats)
internal/
  agent/           Agent HTTP server, hub, worker
  runtime/
    provider/      LLM provider (Anthropic)
    context/       AGENTS.md loader
    prompt/        System prompt assembly
    session/       JSONL persistence
    loop/          Conversation loop
    cost/          Cost tracking + SQLite database
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
```

## Development

```bash
# Build unified binary
just build

# Run in development mode
just dev               # Interactive CLI

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
- ripgrep (for Grep tool)
- Terminal with ANSI color support (for CLI)
