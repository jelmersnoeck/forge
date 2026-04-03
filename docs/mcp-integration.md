# MCP Integration — Claude Code Comparison

This document compares Forge's MCP implementation with Claude Code's approach.

## Architecture Comparison

### Claude Code MCP Architecture

Claude Code implements MCP in two ways:

1. **MCP Client** (`src/services/mcp/`) — Connects to external MCP servers
   - Uses `@modelcontextprotocol/sdk` (TypeScript)
   - Supports STDIO, SSE, Streamable HTTP, and WebSocket transports
   - Manages multiple concurrent MCP server connections
   - Exposes external MCP tools as Claude Code tools (`MCPTool`)
   - Handles OAuth authentication for MCP servers
   - Implements permission checks for MCP tool calls
   
2. **MCP Server** (`mcp-server/`) — Exposes Claude Code source as MCP server
   - Standalone TypeScript server
   - Provides tools to explore the Claude Code codebase
   - Supports both STDIO and HTTP transports
   - Example of exposing domain-specific knowledge via MCP

### Forge MCP Architecture

Forge's initial MCP implementation focuses on **exposing Forge as an MCP server**:

1. **MCP Server** (`mcp-server/`) — Exposes Forge's agent capabilities
   - TypeScript server using `@modelcontextprotocol/sdk`
   - Supports STDIO and HTTP (Streamable HTTP + SSE) transports
   - Proxies tool calls to the Forge agent HTTP API
   - Auto-spawns local forge agent subprocess or connects to remote server
   - Exposes all of Forge's built-in tools (Read, Write, Edit, Bash, etc.)

## Key Differences

| Aspect | Claude Code | Forge |
|--------|-------------|-------|
| **MCP Client** | ✅ Full implementation | ❌ Not yet (future work) |
| **MCP Server** | ✅ Source explorer only | ✅ Full agent capabilities |
| **Language** | TypeScript (Bun) | Go + TypeScript MCP wrapper |
| **Transport** | All (STDIO, SSE, HTTP, WS) | STDIO + HTTP |
| **Authentication** | OAuth, JWT, API keys | API keys (HTTP mode) |
| **Tool Execution** | Native (in-process) | Proxied to agent API |
| **Session Management** | Stateful + stateless modes | Stateful (per-session agents) |

## Claude Code's MCP Client Features

Claude Code's MCP client implementation (`src/services/mcp/client.ts`) provides:

- **Multi-server management** — connect to multiple MCP servers simultaneously
- **Transport auto-selection** — tries Streamable HTTP, falls back to SSE, then STDIO
- **OAuth flow** — full OAuth 2.0 support for authenticated MCP servers
- **Permission system** — user approval required before calling MCP tools
- **Resource handling** — fetch and display MCP resources
- **Prompt templates** — support for MCP prompt templates
- **Error handling** — graceful degradation and retry logic
- **Tool result formatting** — smart truncation and image handling
- **WebSocket support** — for real-time bidirectional communication

Key files:
- `src/services/mcp/client.ts` (119K lines) — core client implementation
- `src/services/mcp/config.ts` (51K lines) — connection configuration
- `src/services/mcp/auth.ts` (89K lines) — OAuth authentication
- `src/tools/MCPTool/MCPTool.ts` — wraps external MCP tools as Claude tools

## Future Work for Forge

To match Claude Code's MCP capabilities, Forge would need:

### Phase 1: Server Improvements (Current)
- ✅ Basic MCP server implementation
- ⚠️ Proper tool execution proxy (currently returns stubs)
- ⚠️ Stream tool progress back to MCP clients
- ⚠️ Add more resources (AGENTS.md, session history, etc.)
- ⚠️ Add prompt templates for guided workflows

### Phase 2: MCP Client Implementation
- ✅ Implement MCP client in Go (JSON-RPC protocol directly)
- ✅ Add MCP tools to Forge's tool registry (MCPConnect, MCPListTools, MCPCallTool, etc.)
- ✅ Manage multiple concurrent MCP server connections
- ⚠️ Add MCP server configuration file support (similar to `.mcp.json`)
- ⚠️ Add slash commands for MCP management (`/mcp add`, `/mcp list`, etc.)

### Phase 3: Advanced Features
- ❌ OAuth authentication for MCP servers
- ❌ Permission system for external MCP tools
- ❌ WebSocket transport support
- ❌ Resource and prompt template support
- ❌ Smart caching and result truncation

## Why MCP Matters

MCP provides a standardized way for AI agents to:

1. **Access external tools** — databases, APIs, code analysis, etc.
2. **Share context** — expose project-specific knowledge
3. **Compose capabilities** — chain multiple specialized agents
4. **Extend without modification** — add new tools without forking

By implementing MCP, Forge can:
- Be used by other agents (Claude Desktop, Claude Code, etc.)
- Use tools from other MCP servers (expand capabilities)
- Integrate into larger multi-agent workflows

## Implementation Notes

### Tool Execution Proxy Challenge

The current MCP server implementation can list Forge's tools but doesn't properly execute them yet. The challenge:

1. **Forge's agent API** expects full conversation messages, not isolated tool calls
2. **MCP clients** send isolated tool calls (JSON-RPC `tools/call` method)
3. **Solution needed**: Add a direct tool execution endpoint to the Forge agent API

Options:
- **A)** Add `/tools/{name}` endpoint to agent HTTP API for direct tool execution
- **B)** Wrap tool calls in synthetic conversation messages with tool_use/tool_result blocks
- **C)** Implement a stateless tool execution mode in the agent

### Go MCP Client Strategy

When implementing an MCP client in Go:

1. **Use official spec** — https://spec.modelcontextprotocol.io/
2. **Start with STDIO** — simplest transport, easiest to test
3. **JSON-RPC core** — implement the JSON-RPC 2.0 message format first
4. **Incremental transports** — add SSE, then Streamable HTTP, then WebSocket
5. **Test against real servers** — use claude-code-explorer as reference

Recommended approach: **Option C** (implement JSON-RPC protocol directly)
- Most control and flexibility
- No TypeScript bridge overhead
- Can be optimized for Go idioms
- Reference implementations exist (claude-code/src/services/mcp/client.ts)

## References

- [Model Context Protocol Specification](https://spec.modelcontextprotocol.io/)
- [MCP TypeScript SDK](https://github.com/modelcontextprotocol/typescript-sdk)
- [Claude Code Source](https://github.com/nirholas/claude-code)
- [Claude Code MCP Client](../claude-code/src/services/mcp/client.ts)
- [Forge MCP Server](./README.md)
