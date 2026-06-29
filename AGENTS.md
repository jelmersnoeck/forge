# forge

Async coding agent — headless HTTP API with an Anthropic backend.

## Architecture

Forge uses a unified binary architecture with subcommands:

- **Unified Binary** (`cmd/forge/`) — single entry point with subcommands:
  - **`forge` (default)** — interactive REPL (spawns agent subprocess)
  - **`forge agent`** — run agent server (spawned by interactive mode)
  - **`forge stats`** — cost analytics (daily/monthly/session breakdowns)

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

## Sub-Agent Roles

Sub-agents (spawned via the `Agent` tool) can request a named role instead of
hand-listing tools. The role's preset is applied AND enforced at execution time
(`Registry.WithPermissions`) — a denied tool reaching `Execute` via resumed
history or hallucination is rejected as an error `ToolResult`, not just hidden
from the schema. Lookup is case-insensitive. Explicit `tools`/`disallowed_tools`
on the `Agent` call always override the preset. Unknown roles fall back to
freeform tool lists (no error).

Role-to-tool matrix (presets in `internal/types/types.go` `AgentRolePermissions`;
enumerate at runtime via `types.SupportedRoles()`):

| Role     | Allow                            | Deny                          |
|----------|----------------------------------|-------------------------------|
| reviewer | Read, Glob, Grep, WebSearch      | Write, Edit, Bash             |
| explorer | Read, Glob, Grep                 | Write, Edit, Bash, Agent      |
| planner  | Read, Glob, Grep, WebSearch, Bash| Write, Edit                   |
| coder    | `*` (all)                        | (none)                        |

Semantics: `"*"` in Allow means all tools permitted; Deny always wins over Allow;
`"*"` is only meaningful in Allow (a `"*"` in Deny is a literal name matching no
real tool). An empty allow + empty deny enforces nothing.

## Spec-Driven Development

Forge is spec-driven. The agent writes a spec before implementing any feature.
Specs live in `.forge/specs/` (configurable via `.forge/config.json` `specsDir`).
Use `forge --spec path/to/spec.md` to implement an existing spec directly.

Key files:
- `internal/spec/spec.go` — parser and loader
- `internal/config/config.go` — forge config loader
- `internal/runtime/prompt/prompt.go` — spec instructions in system prompt

## Configuration

All forge configuration lives under `.forge/`:
- `.forge/settings.json` — project settings (model, permissions, env)
- `.forge/settings.local.json` — local overrides (gitignored)
- `.forge/rules/` — additional rule files (`.md`)
- `.forge/skills/` — skill definitions (`SKILL.md` with frontmatter)
- `.forge/agents/` — agent definitions (`.md` with frontmatter)
- `.forge/specs/` — feature specifications
- `.forge/learnings/` — actionable gotchas and discoveries from agent sessions
- `.forge/config.json` — forge-level config (specsDir, etc.)

For backward compatibility, `.claude/` is also checked as a fallback if `.forge/` doesn't exist.

User-level config: `~/.forge/` (settings, rules, skills).

## Repository layout

```
cmd/
  forge/           unified binary (cli + agent + stats)
internal/
  mcp/             MCP client (Go) — connects to remote MCP servers
    client.go      JSON-RPC over Streamable HTTP transport
    oauth.go       OAuth 2.1 + DCR + PKCE
    bridge.go      bridges MCP tools into Forge's tool registry
    config.go      config loading (~/.forge/mcp.json)
    token_store.go persistent OAuth token storage
  config/          forge-level configuration (.forge/config.json), user config (~/.forge/config.toml)
  attribution/     commit trailer hooks and PR body attribution
  envutil/         shared .env file loading
  spec/            feature specification loader + parser
  types/           shared contracts (messages, events, tools, context, tasks)
  tools/           tool registry + implementations
  agent/           agent HTTP server, single-session hub, worker
  runtime/
    provider/      LLMProvider interface + Anthropic implementation
    context/       ContextLoader (AGENTS.md, specs, skills, agents, rules)
    prompt/        system prompt assembly
    session/       JSONL session persistence
    loop/          ConversationLoop (agentic loop)
    cost/          cost calculation + SQLite tracker
    task/          background task & sub-agent management
```

Go module: `github.com/jelmersnoeck/forge`

## Build & run

```bash
just build              # build unified forge binary
just build-all          # build all binaries
just dev                # build + run interactive CLI
just test               # go test ./...
just vet                # go vet ./...
just clean              # remove binaries
```

## Running

### Interactive mode (default)
```bash
export ANTHROPIC_API_KEY=sk-...
forge                    # spawns local agent, ephemeral session
forge --skip-worktree    # skip worktree creation, run in current directory
forge --issue #42        # start session from GitHub issue
forge --issue https://github.com/o/r/issues/42  # issue by full URL
```

**Git Worktree Isolation**: By default, when running in interactive mode from a git repository (and not already in a worktree), Forge automatically:
- Creates a temporary worktree in `/tmp/forge/worktrees/<session-id>`
- Creates a branch `jelmer/<session-id>` from your current HEAD
- Runs the agent in that isolated worktree
- Cleans up the worktree and branch on exit

This ensures each Forge session has its own isolated workspace, preventing conflicts when running multiple sessions or when your main working tree is in use.

Use `--skip-worktree` to disable this behavior and run directly in your current directory.

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

## Key files

- `internal/agent/server.go` — agent HTTP server (health, messages, events)
- `internal/agent/hub.go` — single-session message queue + event pub/sub
- `internal/agent/worker.go` — conversation loop runner
- `internal/runtime/loop/loop.go` — agentic conversation loop
- `internal/runtime/context/loader.go` — AGENTS.md, skills, agents, rules discovery
- `internal/runtime/prompt/prompt.go` — system prompt assembly
- `internal/runtime/provider/anthropic.go` — Anthropic Messages API streaming
- `internal/runtime/task/manager.go` — background task & sub-agent manager
- `internal/tools/registry.go` — tool registry + NewDefaultRegistry()
- `internal/tools/*.go` — tool implementations (Read, Write, Edit, Bash, Grep, Glob, WebSearch, Reflect, TaskCreate, TaskGet, TaskList, TaskStop, TaskOutput, Agent, AgentGet, AgentList, AgentStop, UseMCPTool)
- `internal/types/types.go` — shared contracts
- `internal/types/task.go` — task & sub-agent types

## Self-Improvement Loop

Forge includes a built-in learning mechanism for capturing actionable gotchas:

1. **AGENTS.md Support**: The agent loads `AGENTS.md` files from:
   - `~/AGENTS.md` (user-level instructions)
   - `$PROJECT/AGENTS.md` or `$PROJECT/.forge/AGENTS.md` (project-level)
   - Parent directories (inheritable)
   - `$PROJECT/AGENTS.local.md` (session-specific)

2. **Reflect Tool**: Agents use the `Reflect` tool to save non-obvious discoveries:
   ```json
   {
     "summary": "Short filename label",
     "learnings": [
       "DuckDuckGo API returns HTTP 202 for bot-detected requests — use server-side search instead",
       "git rebase in worktrees requires --onto with explicit SHAs; interactive mode hangs"
     ]
   }
   ```
   Only call Reflect when the session uncovered something genuinely useful for
   future sessions. Routine work with no surprises needs no reflection.

3. **Automatic Loading**: Learnings from `.forge/learnings/` are injected into
   the system prompt so future sessions benefit from past discoveries.

## API endpoints

### Agent (per-session)

```
GET    /health                        health check
POST   /messages                      receive message (from CLI)
GET    /events                        SSE stream of OutboundEvents
POST   /model                         switch model mid-session
GET    /models                        list available models from all providers
POST   /interrupt                     interrupt current work
```

## Testing

- Tests use `testing` + `testify/require`
- Table-driven: `tests := map[string]struct{...}`, loop var `tc`
- All tests use real filesystem (`t.TempDir()`), real exec — no mocks
- Grep tests require `rg` (ripgrep) on PATH

## Environment variables

- `ANTHROPIC_API_KEY` — Anthropic provider key (the default provider).
- `OPENAI_API_KEY` — OpenAI provider key (when `FORGE_PROVIDER=openai` or
  auto-detected). Either key satisfies the agent; the Claude CLI provider needs
  no key. The startup warning names the env var for the *resolved* provider, not
  always Anthropic. Each provider supplies its own default model and lightweight
  (cheap-call) model list via the optional `types.ModelDefaulter` /
  `types.LightweightModeler` interfaces, so classification, session naming,
  and summarization no longer assume Claude models.

LLM API keys are resolved through `internal/credentials` (logical keys like
`anthropic.api_key` → env vars). The source order is configurable via
`~/.forge/config.toml`:

```toml
[credentials]
sources = ["env"]   # default; tried in order, first non-empty hit wins
```

Unset `[credentials]` → env-only (identical to reading the env vars directly).
- `WORKSPACE_DIR` — default working directory (default: /tmp/forge/workspace)
- `SESSIONS_DIR` — JSONL session storage (default: /tmp/forge/sessions)
- `FORGE_BIN` — path to forge binary (default: forge)
- `FORGE_SESSION_ID` — set by the agent in bash tool env for commit hook attribution
- `FORGE_COAUTHOR` — set by the agent in bash tool env for Co-authored-by trailer
- `FORGE_GENERATED_BY` — set by the agent in bash tool env for Generated-by trailer

## Gotchas

- Anthropic streams two `usage` deltas per call: `message_start` carries
  cache_read/cache_creation/input; `message_delta` carries output tokens only
  (cache fields zero). Cache-health detection must run ONLY on the cache-bearing
  delta — gate via `hasCacheSignal` in `internal/runtime/loop/loop.go`. Feeding
  the output-only delta to `checkCacheHealth` produces bogus `[CACHE BREAK] X→0`
  warnings every call and resets the baseline to 0, blinding real-break detection.
- `~/.forge/settings.json` may contain model aliases like `opus[1m]` that the
  Anthropic API doesn't understand. Agent filters these — only values
  starting with `claude-` are passed through.
- Anthropic API requires `tool_result` blocks immediately after `tool_use` in
  the message history. The ConversationLoop persists these to session JSONL so
  resume reconstructs valid history.
- When referencing `httptest.Server.URL` inside its own handler closure, the
  variable isn't assigned yet — assign the handler after creating the server,
  or use a pointer indirection
- Race condition pattern: when a Stop function sets status to 'killed' but a
  goroutine's deferred completion handler overwrites it to 'failed', check
  `IsTerminal` before overwriting
- DuckDuckGo Instant Answer API is a knowledge graph, not a search engine —
  returns HTTP 202 for bot-detected requests and empty results for real queries
- The Anthropic SDK (v1.27.1+) supports `web_search_20250305` and
  `web_search_20260209` server tools — use the `20260209` variant
- For server-side tools, prefer the sub-call pattern (client tool wrapping a
  server tool) over injecting server tools into the main conversation loop —
  keeps history clean, allows cheaper models
- Check Claude Code's source at `~/Projects/claude-code/` when implementing
  features that interact with external APIs — they likely solved it already
- MCP tool namespacing as `mcp__server__tool` prevents collisions with built-in
  tools
- Sub-agents share the parent's ContextBundle and LLM provider unchanged — keep
  this in mind for isolation and rate-limiting concerns
- The task manager uses package-level `SetTaskManager` — not ideal, but changing
  to ToolContext injection is a larger refactor
- `exec.CommandContext` in Go only sends SIGKILL to the direct child process —
  grandchildren survive as orphans. Use `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`
  + `cmd.Cancel` to kill the entire process group via `syscall.Kill(-pgid, SIGKILL)`
- `cmd.Run()`/`cmd.Wait()` can block indefinitely if child processes hold
  stdout/stderr pipes open. `cmd.WaitDelay` (Go 1.20+) provides a backstop
  timeout for pipe cleanup
- Go's `cmd.Cancel` and `cmd.WaitDelay` (Go 1.20+) are the modern way to handle
  process tree cleanup — prefer over manual goroutine-based `cmd.Process.Kill()`
- Setting `cmd.Stderr` to a `strings.Builder`/`bytes.Buffer` creates a data race:
  `exec.Cmd` starts an internal goroutine doing `io.Copy` to the writer, racing
  with any read of the buffer. Use `cmd.StderrPipe()` + drain goroutine with a
  done channel to synchronize access
- Claude CLI `--output-format stream-json` emits NDJSON with top-level types:
  `system`, `stream_event`, `assistant`, `result`. Tool use is internal to the
  CLI and not surfaced separately
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
- Configuration in `.forge/` directory (fallback: `.claude/` for backward compat)
