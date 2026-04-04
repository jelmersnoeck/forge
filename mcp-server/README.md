# Forge MCP Server

Exposes Forge's agent capabilities as an [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server, allowing any MCP-compatible client to use Forge's tools.

## Features

- **STDIO transport** — for local clients (Claude Desktop, Claude Code, VS Code)
- **HTTP transport** — for remote deployments (Streamable HTTP + legacy SSE)
- **Auto-spawning** — starts local forge agent subprocess automatically
- **Remote mode** — connect to existing forge server

## Setup

```bash
cd mcp-server
npm install
npm run build
```

## Usage

### STDIO (local)

```bash
node dist/index.js
```

### HTTP (remote)

```bash
PORT=3000 node dist/http.js

# With authentication
MCP_API_KEY=your-secret-token PORT=3000 node dist/http.js
```

## Configuration

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "forge": {
      "command": "node",
      "args": ["/absolute/path/to/forge/mcp-server/dist/index.js"],
      "env": {
        "WORKSPACE_DIR": "/path/to/your/workspace"
      }
    }
  }
}
```

### VS Code (GitHub Copilot)

Add to `.vscode/mcp.json`:

```json
{
  "servers": {
    "forge": {
      "type": "stdio",
      "command": "node",
      "args": ["${workspaceFolder}/mcp-server/dist/index.js"],
      "env": {
        "WORKSPACE_DIR": "${workspaceFolder}"
      }
    }
  }
}
```

### Remote HTTP

```json
{
  "mcpServers": {
    "forge": {
      "url": "https://your-server.example.com/mcp",
      "headers": {
        "Authorization": "Bearer your-secret-token"
      }
    }
  }
}
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `FORGE_BIN` | `forge` | Path to forge binary |
| `FORGE_SERVER_URL` | _(none)_ | Remote forge server URL (skips local spawn) |
| `WORKSPACE_DIR` | `cwd` | Working directory for forge agent |
| `PORT` | `3000` | HTTP server port |
| `MCP_API_KEY` | _(none)_ | Bearer token for HTTP auth |

## Available Tools

The MCP server exposes all of Forge's built-in tools:

- `Read` — Read files with line numbers
- `Write` — Write files
- `Edit` — String replacement in files
- `Bash` — Execute shell commands
- `Grep` — Search with ripgrep
- `Glob` — File pattern matching
- `WebSearch` — Web search
- `Reflect` — Capture session learnings
- `TaskCreate`, `TaskGet`, `TaskList`, `TaskStop` — Background task management
- `Agent`, `AgentGet`, `AgentList`, `AgentStop` — Sub-agent management

## Resources

- `forge://readme` — Project README
- `forge://agents` — AGENTS.md instructions and learnings

## Architecture

```
mcp-server/
├── src/
│   ├── server.ts    — MCP server implementation (transport-agnostic)
│   ├── index.ts     — STDIO entrypoint
│   └── http.ts      — HTTP entrypoint
├── package.json
└── tsconfig.json
```

The server connects to a Forge agent via:
1. **Local mode** — spawns `forge agent --port 0` as subprocess
2. **Remote mode** — connects to existing forge server via HTTP

Tool calls are proxied through the Forge agent's HTTP API.

## Limitations

**Current implementation is a prototype.** The MCP server can list tools but doesn't yet properly proxy tool execution through Forge's conversation loop. This requires:

1. Adding a direct tool execution endpoint to the Forge agent API
2. Or wrapping tool calls in a proper message format that the agent understands

For now, this demonstrates the integration architecture.

## Development

```bash
npm run dev    # Watch mode
npm run build  # Compile TypeScript
npm start      # Run STDIO server
npm run start:http  # Run HTTP server
```

## Next Steps

To make this production-ready:

1. **Implement proper tool execution proxy** — currently returns stub responses
2. **Add session management** — allow multiple concurrent MCP clients
3. **Add streaming support** — stream tool progress back to MCP client
4. **Add more resources** — expose project docs, AGENTS.md, etc.
5. **Add prompts** — similar to claude-code-explorer's guided prompts
