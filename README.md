# Forge

Async coding agent — headless Claude Code behind a platform-agnostic HTTP API.

## Features

- **Full-screen TUI** - Always-available input, message queuing, real-time events
- **Message Queuing** - Queue multiple messages while agent is working
- **Task Queues** - Agent can queue commands to run after each tool or on completion
- **Session Persistence** - Resume conversations anytime
- **Streaming** - Real-time event stream via Server-Sent Events (SSE)
- **Tool Execution** - Read, Write, Edit, Bash, Glob, Grep, and Queue tools

## Quick Start

```bash
# Install dependencies
go mod download

# Start the server
just dev-server

# In another terminal, start the CLI
just dev-cli

# Or build and run manually
just build
./forge-server &
./forge-cli
```

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
- **Ctrl+C** - Exit (shows resume command)

### Resume a Session

```bash
forge-cli --resume <session-id>
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

Forge uses a 3-tier architecture:

```
┌──────────┐         ┌──────────┐         ┌──────────┐
│   CLI    │ ──HTTP─→│  Server  │ ──HTTP─→│  Agent   │
│  (TUI)   │ ←─SSE──┤ (Gateway)│ ←─SSE──┤  (tmux)  │
└──────────┘         └──────────┘         └──────────┘
```

- **CLI** - Interactive TUI, queues messages, displays events
- **Server** - Session management, spawns agents, forwards messages
- **Agent** - Conversation loop, tool execution, LLM interaction

## Project Structure

```
cmd/
  server/          Server entry point
  agent/           Agent binary (runs in tmux)
  cli/             Interactive TUI client
internal/
  agent/           Agent HTTP server, hub, worker
  runtime/
    provider/      LLM provider (Anthropic)
    context/       CLAUDE.md loader
    prompt/        System prompt assembly
    session/       JSONL persistence
    loop/          Conversation loop
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
| `Read` | Read file contents with line numbers |
| `Write` | Create or overwrite files |
| `Edit` | String replacement in files |
| `Bash` | Execute shell commands |
| `Glob` | Fast file pattern matching |
| `Grep` | Search using ripgrep |
| `WebSearch` | Search the web for information (DuckDuckGo or Brave) |
| `QueueImmediate` | Queue command after each tool |
| `QueueOnComplete` | Queue command on completion |

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
# Server
GATEWAY_PORT=3000           # Server listen port
GATEWAY_HOST=0.0.0.0        # Server listen host
WORKSPACE_DIR=/tmp/forge/workspace  # Working directory
SESSIONS_DIR=/tmp/forge/sessions    # Session storage
AGENT_BIN=forge-agent       # Agent binary path

# Agent (required)
ANTHROPIC_API_KEY=sk-...    # Anthropic API key

# Web Search (optional)
SEARCH_PROVIDER=duckduckgo  # Search provider: "duckduckgo" or "brave"
BRAVE_API_KEY=your_key      # Required if using Brave Search

# CLI
FORGE_URL=http://localhost:3000  # Server URL
```

## Development

```bash
# Build all binaries
just build

# Build specific binary
just build-server
just build-agent
just build-cli

# Run in development mode
just dev-server    # Build agent + server, run server
just dev-cli       # Build + run CLI

# Tests
just test          # Run all tests
just vet           # Run go vet

# Docker
just up            # Start with docker-compose
just down          # Stop containers
just logs          # Tail logs
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

### Example 1: Rapid Task Queuing
```bash
forge-cli

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

## Documentation

- **[CLAUDE.md](CLAUDE.md)** - Project instructions and architecture
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

**Tip:** Start with `just dev-server` in one terminal and `just dev-cli` in another for the best development experience.
