# Forge

Async coding agent — headless Claude Code behind a platform-agnostic HTTP API.

## Features

- **Full-screen TUI** - Always-available input, message queuing, real-time events
- **Message Queuing** - Queue multiple messages while agent is working
- **Task Queues** - Agent can queue commands to run after each tool or on completion
- **Session Persistence** - Resume conversations anytime
- **Cost Tracking** - Automatic API cost tracking with analytics (daily/monthly/session breakdowns)
- **Streaming** - Real-time event stream via Server-Sent Events (SSE)
- **Tool Execution** - Read, Write, Edit, Bash, Glob, Grep, and Queue tools
- **MCP Integration** - Connect to external MCP servers and use their tools (MCP client + server)

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

### Server Mode (Multi-Session)

For persistent sessions and multi-user deployments:

```bash
# Terminal 1: Start the server
just dev-server

# Terminal 2: Connect CLI to server
./forge --server http://localhost:3000

# Resume a session later
./forge --server http://localhost:3000 --resume <session-id>
```

Server mode gives you:
- Session persistence (resume anytime)
- Multiple concurrent sessions
- Remote server support

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

## CLI Usage

The Forge CLI is a full-screen TUI (Terminal User Interface):

```
┌─────────────────────────────────────────────────────────┐
│ forge cli — session abc-123                              │
│                                                          │
│ Agent responses appear here...                           │
│   [Write] feature.go                                     │
│   [Bash] go test                                         │
│                                                          │
├─────────────────────────────────────────────────────────┤
│ Queued messages:                                        │
│ → Current message (processing)                          │
│   Next message (waiting)                                │
├─────────────────────────────────────────────────────────┤
│  ╭─────────────────────────────────────────────────╮    │
│  │ > Type your message here...                     │    │
│  ╰─────────────────────────────────────────────────╯    │
└─────────────────────────────────────────────────────────┘
```

### Features

- **Always-available input** - Type at any time, even while agent is working
- **Message queuing** - Press Enter to queue messages, they send automatically
- **Queue visibility** - See what's pending above the input
- **Real-time events** - Agent responses stream in as they happen

### Keyboard Shortcuts

- **Enter** - Queue/send message
- **Backspace** - Delete character
- **Ctrl+C** (once) - Interrupt running work
- **Ctrl+C** (twice) - Exit CLI
- **Up/Down or Mouse Wheel** - Scroll output

### CLI Modes

#### Interactive Mode (Default)
```bash
./forge
# Spawns local agent, ephemeral session
```

#### Server Mode (Persistent Sessions)
```bash
./forge --server http://localhost:3000
# Connect to remote server, persistent sessions

./forge --server http://localhost:3000 --resume <session-id>
# Resume a previous session
```

## Agent Queue System

The agent can queue commands to run at specific times:

### Immediate Queue
Runs after EVERY tool execution:

```
User: "Create a feature and test after each file change"

Agent:
  ⏱  Queued immediate: go test ./...
  [Write] feature.go
  [queued] go test ./...
  PASS
  [Write] feature_test.go
  [queued] go test ./...
  PASS
```

### Completion Queue
Runs ONCE when all work is done:

```
User: "Refactor the code, commit when done"

Agent:
  ⏱  Queued on complete: git commit -am 'Refactor'
  [Edit] file1.go
  [Edit] file2.go
  [done]
  [queued] git commit -am 'Refactor'
  [main abc123] Refactor
```

### How It Works

The agent has two tools:
- **QueueImmediate** - Queue a command to run after each tool execution
- **QueueOnComplete** - Queue a command to run when work finishes

Claude uses these tools automatically based on natural language:
- "test after each change" → `QueueImmediate`
- "commit when done" → `QueueOnComplete`

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

### Server Mode (Gateway)
```
┌──────────┐         ┌──────────┐         ┌──────────┐
│   CLI    │ ──HTTP─→│  Server  │ ──HTTP─→│  Agent   │
│  (TUI)   │ ←─SSE──┤ (Gateway)│ ←─SSE──┤  (tmux)  │
└──────────┘         └──────────┘         └──────────┘
```

Server manages multiple sessions, spawns agents in tmux, persists history.

## Project Structure

```
cmd/
  forge/           Unified binary (cli + server + agent + stats)
  server/          Legacy server (use 'forge server')
  agent/           Agent binary (still used by server backend)
  cli/             Legacy CLI (use 'forge')
internal/
  agent/           Agent HTTP server, hub, worker
  runtime/
    provider/      LLM provider (Anthropic)
    context/       CLAUDE.md loader
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
docs/              Documentation
```

## Tools

Built-in tools available to the agent:

| Tool | Description |
|------|-------------|
| **File Operations** | |
| `Read` | Read file contents with line numbers |
| `Write` | Create or overwrite files |
| `Edit` | String replacement in files |
| **Execution** | |
| `Bash` | Execute shell commands |
| `Glob` | Fast file pattern matching |
| `Grep` | Search using ripgrep |
| **External** | |
| `WebSearch` | Search the web for information (DuckDuckGo or Brave) |
| **Queuing** | |
| `QueueImmediate` | Queue command after each tool |
| `QueueOnComplete` | Queue command on completion |
| **Background Tasks** | |
| `TaskCreate` | Create background task |
| `TaskGet` | Get task status |
| `TaskList` | List all tasks |
| `TaskStop` | Stop a running task |
| `TaskOutput` | Get task output |
| **Sub-Agents** | |
| `Agent` | Spawn a sub-agent |
| `AgentGet` | Get sub-agent status |
| `AgentList` | List all sub-agents |
| `AgentStop` | Stop a sub-agent |
| **MCP Integration** | |
| `MCPConnect` | Connect to an MCP server |
| `MCPListTools` | List tools from an MCP server |
| `MCPCallTool` | Call a tool on an MCP server |
| `MCPListConnections` | List connected MCP servers |
| `MCPDisconnect` | Disconnect from an MCP server |

## API Endpoints

### Server (Gateway)
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
```

## Environment Variables

```bash
# Required (for agent)
ANTHROPIC_API_KEY=sk-...    # Anthropic API key

# Server Mode Only
GATEWAY_PORT=3000           # Server listen port
GATEWAY_HOST=0.0.0.0        # Server listen host
WORKSPACE_DIR=/tmp/forge/workspace  # Working directory
SESSIONS_DIR=/tmp/forge/sessions    # Session storage
AGENT_BIN=forge-agent       # Agent binary path

# Optional Features
SEARCH_PROVIDER=duckduckgo  # Web search: "duckduckgo" or "brave"
BRAVE_API_KEY=your_key      # Required if using Brave Search
```

## Development

```bash
# Build unified binary
just build

# Build all binaries (including legacy)
just build-all

# Build specific binary
just build-agent       # Still needed by server backend

# Run in development mode
just dev               # Interactive CLI
just dev-server        # Server mode
just dev-server-daemon # Server daemon mode

# Tests
just test              # Run all tests
just vet               # Run go vet

# Docker
just up                # Start with docker-compose
just down              # Stop containers
just logs              # Tail logs
```

## Configuration

### .env File
```bash
cp .env.example .env
# Edit .env with your ANTHROPIC_API_KEY
```

The server loads `.env` from the project root at startup.

### CLAUDE.md Files

The agent loads context from `CLAUDE.md` files:

- `~/.claude/CLAUDE.md` - User-level instructions
- `./CLAUDE.md` - Project-level instructions  
- `./.local/CLAUDE.md` - Local overrides (gitignored)

## Examples

### Example 1: Rapid Task Queuing (Interactive Mode)
```bash
./forge

> Create user authentication module
> Add tests for auth
> Add database migrations
> Commit all changes

# All 4 messages queued
# Agent processes them sequentially
```

### Example 2: Test-Driven Development
```bash
> Implement a calculator with add and multiply functions.
  Run tests after each file you create.

Agent:
  ⏱  Queued immediate: go test ./...
  [Write] calculator.go
  [queued] go test ./...
  PASS
  [Write] calculator_test.go  
  [queued] go test ./...
  PASS
```

### Example 3: Auto-Commit Workflow
```bash
> Refactor the database code to use connection pooling.
  When finished, commit with a descriptive message.

Agent:
  ⏱  Queued on complete: git commit -am 'Refactor: Add connection pooling'
  [Read] database.go
  [Edit] database.go
  [Bash] go test ./database
  PASS
  [done]
  [queued] git commit -am 'Refactor: Add connection pooling'
  [main 7f8d9a] Refactor: Add connection pooling
```

### Example 4: Web Search for Documentation
```bash
> What's the latest stable version of Go and how do I upgrade?

Agent:
  [WebSearch] query="Go programming language latest stable version"
  
  Search results for: Go programming language latest stable version
  1. Go Releases
     https://go.dev/doc/devel/release
     Go 1.22 is the latest stable version...
  
  The latest stable version is Go 1.22. To upgrade:
  
  [Bash] go version
  [Bash] brew upgrade go  # or your package manager
```

### Example 5: Using MCP Servers
```bash
> Connect to the claude-code-explorer MCP server and list its tools

Agent:
  [MCPConnect] name="claude-code" command="node" args=["../claude-code/mcp-server/dist/index.js"]
  Successfully connected to MCP server "claude-code"
  
  [MCPListTools] server="claude-code"
  MCP server "claude-code" has 8 tools:
  
  • list_tools: List all Claude Code agent tools
  • list_commands: List all Claude Code slash commands
  • get_tool_source: Read tool implementation
  • search_source: Regex search across source tree
  ...
  
> Use the search_source tool to find how permissions are handled

Agent:
  [MCPCallTool] server="claude-code" tool="search_source" arguments={"pattern":"permission.*check"}
  Found 42 matches for "permission.*check" in claude-code source...
```

## Documentation

- **[CLAUDE.md](CLAUDE.md)** - Project instructions and architecture
- **[docs/mcp-integration.md](docs/mcp-integration.md)** - MCP integration details and comparison with Claude Code
- **[docs/CLI_TUI.md](docs/CLI_TUI.md)** - CLI TUI user guide
- **[docs/CLI_TUI_VISUAL.md](docs/CLI_TUI_VISUAL.md)** - Visual examples
- **[docs/QUEUE_SYSTEM.md](docs/QUEUE_SYSTEM.md)** - Queue implementation details
- **[docs/QUEUE_UX_EXAMPLES.md](docs/QUEUE_UX_EXAMPLES.md)** - Queue usage patterns
- **[docs/QUEUE_QUICKSTART.md](docs/QUEUE_QUICKSTART.md)** - Queue quick reference
- **[docs/QUEUE_DIAGRAMS.md](docs/QUEUE_DIAGRAMS.md)** - Queue flow diagrams

## Testing

All tests use real filesystem and processes (no mocks):

```bash
go test ./...
```

Test data uses Community TV show references (Troy Barnes, Greendale, etc.).

## Requirements

- Go 1.26.1+
- tmux (for backend)
- ripgrep (for Grep tool)
- Terminal with ANSI color support (for CLI)

## License

See LICENSE file.

## Contributing

This is a personal project, but issues and PRs are welcome.

---

**Quick Start:** Run `forge` for instant local development, or `just dev-server` + `forge --server http://localhost:3000` for persistent sessions.

**Cost Tracking:** Use `forge stats` to see your API usage and costs.
