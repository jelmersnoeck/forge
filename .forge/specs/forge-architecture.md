---
id: forge-architecture
status: active
---
# Async coding agent with headless HTTP API

## Description
Forge is a platform-agnostic async coding agent that runs behind an HTTP API.
It provides interactive REPL mode and cost analytics through a unified binary
with subcommands. The standalone gateway server (and its bus/backend packages)
was removed in #235; the `gateway` spec is superseded. A vestigial `--gateway URL`
client flag remains for connecting to a remote gateway endpoint, but the binary
no longer ships a `forge gateway` server subcommand.

## Context
- `cmd/forge/` — unified binary entry point (main, agent, stats, mcp subcommands).
  Interactive REPL (Bubble Tea) split into focused subsystems (implemented):
  `output.go` (OutputBuffer), `tasks.go` (TaskTrackerSet), `cost.go`
  (CostAccumulator), `events.go` (EventHandler — owns event switch + streaming
  state + flush), `session.go` (spawnLocalAgent + worktree/PR lifecycle). See
  `.forge/specs/cli-subsystem-extraction.md`.
- `internal/agent/` — agent HTTP server, single-session hub, conversation worker
- `internal/runtime/loop/` — agentic conversation loop (tool execution cycle)
- `internal/runtime/provider/` — LLM provider interface + Anthropic implementation
- `internal/runtime/context/` — context loader (AGENTS.md, specs, skills, agents, rules)
- `internal/runtime/prompt/` — system prompt assembly from context bundles
- `internal/runtime/session/` — JSONL session persistence
- `internal/runtime/cost/` — cost calculation + SQLite tracker
- `internal/runtime/task/` — background task & sub-agent management
- `internal/tools/` — tool registry + all built-in tool implementations
- `internal/review/` — multi-agent review findings + dedup
- `internal/types/` — shared contracts (messages, events, tools, context, tasks)
- `internal/mcp/` — MCP client (JSON-RPC over HTTP, OAuth 2.1, tool bridge)
- `internal/config/` — forge-level configuration loader
- `internal/spec/` — feature specification loader and parser

## Behavior
- `forge` (no args) — interactive REPL; spawns agent subprocess on ephemeral port
- `forge agent --port N` — run agent HTTP server on port N (0 = random)
- `forge stats` — cost analytics with daily/monthly/session breakdowns
- `forge mcp` — MCP server management subcommands
- `forge --gateway URL` — connect to a remote gateway endpoint (vestigial client
  flag; no gateway server ships in this binary — see superseded `.forge/specs/gateway.md`)
- `forge --resume SESSION` — resume an existing session
- `forge --skip-worktree` — disable git worktree isolation
- Git worktree isolation: auto-creates `/tmp/forge/worktrees/<session>` with branch `jelmer/<session>`
- Agent emits SSE events: text, tool_use, done, error, thinking, usage, etc.
- Tool results exceeding 30K chars are head+tail truncated (40%/40%)
- System prompt uses up to 4 cache_control blocks (2 system + 1 tool + 1 message)
- MCP tools are lazy-loaded via a single gateway tool (~300 tokens overhead)
- Session history persisted as JSONL; tool_result blocks follow tool_use for valid replay
- Cost tracking to `~/.forge/costs.db` (SQLite) — every API call recorded

## Constraints
- No public Go API — all packages under `internal/`
- Model aliases from `~/.forge/settings.json` (e.g. `opus[1m]`) must be filtered;
  only values starting with `claude-` pass through to Anthropic API
- `tool_result` blocks must immediately follow `tool_use` in message history
- Deterministic tool schema ordering required for prompt cache stability
- No mocks in tests — use real filesystem, real exec, real HTTP (httptest)
- Agent binary path configurable via `FORGE_BIN` env var
- Specs default to `.forge/specs/` but configurable via `.forge/config.json` `specsDir`
- Background tasks must have timeouts to prevent stuck commands

## Interfaces
```go
// Core message types
type InboundMessage struct {
    SessionID string
    Text      string
    User      string
    Source    string
    Metadata  map[string]any
    Timestamp int64
}

type OutboundEvent struct {
    ID        string
    SessionID string
    Type      string      // text, tool_use, done, error, thinking, usage, ...
    Content   string
    ToolName  string
    Timestamp int64
    Usage     *TokenUsage
    Model     string
}

// LLM provider
type LLMProvider interface {
    Chat(ctx context.Context, req ChatRequest) (<-chan ChatDelta, error)
}

// Tool system
type ToolDefinition struct {
    Name        string
    Description string
    InputSchema map[string]any
    Handler     ToolHandler
    ReadOnly    bool
    Destructive bool
}

type ToolHandler func(input map[string]any, ctx ToolContext) (ToolResult, error)

// Context bundle
type ContextBundle struct {
    ClaudeMD          []ClaudeMDEntry
    AgentsMD          []AgentsMDEntry
    Rules             []RuleEntry
    SkillDescriptions []SkillDescription
    AgentDefinitions  map[string]AgentDefinition
    Specs             []SpecEntry
    Settings          MergedSettings
}

// Spec system
type SpecDocument struct {
    ID, Status                                          string
    Header, Description, Context, Behavior              string
    Constraints, Interfaces, EdgeCases                  string
    Path                                                string
}

type SpecEntry struct {
    Path, Content, ID, Status, Header string
}

// Agent HTTP endpoints
// GET  /health
// POST /messages
// GET  /events  (SSE)
// POST /interrupt
//
// The standalone gateway server and its /sessions/* endpoints were removed
// in #235 (see superseded .forge/specs/gateway.md).
```

## Edge Cases
- Anthropic API returns 429 (rate limit) or 529 (overload) — agent must handle retry with backoff
- Session resume with corrupted JSONL — graceful degradation, not crash
- Multiple agents writing to same worktree simultaneously — worktree isolation prevents this
- MCP server unreachable during tool discovery — non-fatal, agent continues without MCP tools
- Cost DB locked by concurrent sessions — SQLite WAL mode handles this
- Tool execution exceeds timeout — Bash tool has 120s default, 600s max
- Agent subprocess dies unexpectedly — CLI detects via the event stream closing and reports the error
- Git worktree creation fails (not in a git repo) — falls back to current directory
- Settings.json contains non-`claude-` model aliases — filtered before API call
- Spec with duplicate ID — last-loaded wins (directory listing order)
