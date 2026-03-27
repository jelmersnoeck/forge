# Forge: Decouple from Agent SDK

Replace `@anthropic-ai/claude-agent-sdk` with forge-owned packages that talk
directly to the Anthropic Messages API while preserving Claude Code compatibility
(CLAUDE.md, skills, agents, rules, same tool set).

## Goals

- Drop the SDK dependency entirely — forge owns the conversation loop
- Load the same filesystem context Claude Code does (CLAUDE.md, skills, agents, rules)
- Provide the same ~30 built-in tools with identical schemas
- Abstract the LLM provider so Anthropic is swappable later
- Keep gateway/bus/CLI unchanged — only worker.ts changes its import

## Non-goals

- Hooks system (add later as a separate concern)
- File checkpointing / rewind
- Agent teams (TeamCreate, SendMessage, multi-agent coordination)
- Sandbox / permission UI (forge runs with bypass; permission is a client concern)
- Bridge transport (claude.ai embedding)
- Full parity with every SDK feature on day one

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  @forge/server  (gateway — unchanged API surface)    │
├─────────────────────────────────────────────────────┤
│  @forge/runtime  (conversation loop + context)       │
│    - Drives Anthropic Messages API                   │
│    - Assembles system prompt + tool definitions       │
│    - Loads CLAUDE.md, skills, agents, rules           │
│    - Session persistence (JSONL)                      │
│    - Provider interface (Anthropic now, swap later)   │
├─────────────────────────────────────────────────────┤
│  @forge/tools  (tool registry + implementations)      │
│    - Each tool: schema + handler + prompt fragment    │
│    - In-process execution (fs, child_process, rg)    │
│    - MCP client for external tool servers             │
├─────────────────────────────────────────────────────┤
│  @forge/types  (shared contracts — expanded)          │
└─────────────────────────────────────────────────────┘
```

Build order: `types -> tools -> runtime -> server -> cli`

---

## Package: `@forge/types`

Expand the existing types package with new contracts.

### New types

```typescript
// --- LLM Provider ---

interface ChatRequest {
  model: string;
  system: SystemBlock[];
  messages: MessageParam[];
  tools: ToolSchema[];
  maxTokens: number;
  thinking?: ThinkingConfig;
  stream: true;
}

interface ChatDelta =
  | { type: "text_delta"; text: string }
  | { type: "thinking_delta"; thinking: string }
  | { type: "tool_use_start"; id: string; name: string }
  | { type: "tool_use_delta"; id: string; partialJson: string }
  | { type: "tool_use_end"; id: string }
  | { type: "message_stop"; stopReason: string }

type ChatStream = AsyncIterable<ChatDelta>;

type ThinkingConfig =
  | { type: "adaptive" }
  | { type: "enabled"; budgetTokens: number }
  | { type: "disabled" };

interface SystemBlock {
  type: "text";
  text: string;
  cacheControl?: { type: "ephemeral" };
}

// --- Tool System ---

interface ToolSchema {
  name: string;
  description: string;
  inputSchema: JSONSchema;
}

interface ToolResult {
  content: ToolResultContent[];
  isError?: boolean;
}

type ToolResultContent =
  | { type: "text"; text: string }
  | { type: "image"; data: string; mimeType: string }
  | { type: "resource"; resource: { uri: string; text?: string; blob?: string; mimeType?: string } };

interface ToolContext {
  cwd: string;
  sessionId: string;
  threadId: string;
  signal: AbortSignal;
  emit: (event: OutboundEvent) => void;
  // Access to runtime for tools that need it (Agent, Skill)
  runtime?: RuntimeHandle;
}

interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: JSONSchema;
  promptFragment?: string;     // instructions appended to tool description
  handler: (input: Record<string, unknown>, ctx: ToolContext) => Promise<ToolResult>;
  annotations?: {
    readOnly?: boolean;
    destructive?: boolean;
  };
}

// --- Context Loading ---

interface ContextBundle {
  claudeMd: ClaudeMdEntry[];
  rules: RuleEntry[];
  skillDescriptions: SkillDescription[];
  agentDefinitions: Record<string, AgentDefinition>;
  settings: MergedSettings;
  mcpServerConfigs: Record<string, McpServerConfig>;
}

interface ClaudeMdEntry {
  path: string;
  content: string;
  level: "user" | "project" | "local" | "parent";
}

interface RuleEntry {
  path: string;
  content: string;
  level: "user" | "project";
}

interface SkillDescription {
  name: string;
  description: string;
  path: string;           // resolved path to SKILL.md
  isUserInvocable: boolean;
}

interface AgentDefinition {
  name: string;
  description: string;
  prompt: string;
  tools?: string[];
  disallowedTools?: string[];
  model?: "sonnet" | "opus" | "haiku" | "inherit";
  maxTurns?: number;
}

interface MergedSettings {
  permissions?: { allow: string[]; deny: string[] };
  env?: Record<string, string>;
  mcpServers?: Record<string, McpServerConfig>;
  model?: string;
}

type McpServerConfig =
  | { type: "stdio"; command: string; args?: string[]; env?: Record<string, string> }
  | { type: "sse"; url: string; headers?: Record<string, string> }
  | { type: "http"; url: string; headers?: Record<string, string> };

// --- Session Persistence ---

interface SessionMessage {
  uuid: string;
  parentUuid?: string;
  sessionId: string;
  type: "user" | "assistant" | "system";
  message: unknown;       // raw API message payload
  timestamp: number;
}

interface SessionMeta {
  sessionId: string;
  threadId: string;
  cwd: string;
  createdAt: number;
  lastActiveAt: number;
  title?: string;
}

// --- Provider Interface ---

interface LLMProvider {
  chat(request: ChatRequest): ChatStream;
  countTokens?(messages: MessageParam[]): Promise<number>;
}

// --- Runtime Handle (for tools that need runtime access) ---

interface RuntimeHandle {
  loadSkillContent(name: string): Promise<string>;
  spawnSubagent(opts: SubagentOptions): Promise<ToolResult>;
}

interface SubagentOptions {
  prompt: string;
  agentType: string;
  model?: string;
  tools?: string[];
  maxTurns?: number;
}
```

### Existing types (unchanged)

`InboundMessage`, `OutboundEvent`, `ThreadMeta` remain as-is.

---

## Package: `@forge/tools`

### File structure

```
packages/tools/
  package.json
  tsconfig.json
  src/
    index.ts              re-exports
    registry.ts           ToolRegistry class
    tools/
      read.ts             Read tool
      write.ts            Write tool
      edit.ts             Edit tool
      bash.ts             Bash tool
      glob.ts             Glob tool
      grep.ts             Grep tool
      web-search.ts       WebSearch tool
      web-fetch.ts        WebFetch tool
      skill.ts            Skill tool (lazy loader)
      agent.ts            Agent tool (subagent launcher)
      ask-user.ts         AskUserQuestion tool
      plan.ts             EnterPlanMode + ExitPlanMode
      notebook-edit.ts    NotebookEdit tool
      task.ts             TaskCreate/Get/Update/List + TodoWrite
      task-output.ts      TaskOutput tool
      task-stop.ts        TaskStop tool
      config.ts           Config tool
      worktree.ts         EnterWorktree tool
      mcp.ts              ListMcpResources + ReadMcpResource
```

### ToolRegistry

```typescript
class ToolRegistry {
  private tools = new Map<string, ToolDefinition>();

  register(tool: ToolDefinition): void;
  get(name: string): ToolDefinition | undefined;
  all(): ToolDefinition[];
  schemas(): ToolSchema[];     // for sending to the API
  execute(name: string, input: Record<string, unknown>, ctx: ToolContext): Promise<ToolResult>;
}

function createDefaultRegistry(cwd: string): ToolRegistry;
```

`createDefaultRegistry` registers all built-in tools. The registry is passed
to the conversation loop.

### Tool implementation tiers

**Tier 1 — MVP (required for a functioning agent):**

| Tool | Implementation |
|------|---------------|
| Read | `fs.readFile` with line numbering, image detection (check magic bytes, return base64), PDF via `pdf-parse` or shell `pdftotext` |
| Write | `fs.writeFile` with directory creation |
| Edit | String replacement with uniqueness check, `replace_all` flag |
| Bash | `child_process.spawn` with timeout, stdout/stderr capture, background mode via detached process |
| Glob | `fast-glob` package or `globby` |
| Grep | Shell out to `rg` (ripgrep) — it's fast and already required by Claude Code |
| WebSearch | Anthropic's server-side tool (`type: "web_search_20250305"`) — no implementation needed, passed as server tool in API request |
| WebFetch | `fetch()` + html-to-markdown conversion, truncation, prompt processing |

**Tier 2 — Orchestration (needed for skills/agents to work):**

| Tool | Implementation |
|------|---------------|
| Skill | Reads SKILL.md content from discovered path, returns as text for model context |
| Agent | Spawns a sub-ConversationLoop with restricted tools/prompt, collects result |
| AskUserQuestion | Emits a structured question event; answer comes back via the message queue |
| EnterPlanMode / ExitPlanMode | State flag on the conversation loop; restricts tool execution in plan mode |
| TaskCreate/Get/Update/List | In-memory task store per thread (Map-based), same semantics as SDK |
| TodoWrite | Wrapper around task store for backward compat |

**Tier 3 — Extended (implement as needed):**

| Tool | Implementation |
|------|---------------|
| NotebookEdit | Parse .ipynb JSON, modify cell, write back |
| TaskOutput | Read from background task output buffer |
| TaskStop | Kill background child process by task ID |
| Config | Get/set runtime config values |
| EnterWorktree | `git worktree add` via Bash |
| MCP tools | MCP client (stdio/SSE/HTTP transport) |

### Tool schema source

Tool schemas (name, description, inputSchema) are extracted from the Claude Code
open-source CLI and maintained as static JSON objects in each tool file. The
prompt fragments that teach the model how to use each tool are also extracted
and stored alongside the schema.

When Claude Code updates its tools, we update our snapshot. This is a manual
process — intentionally so, to avoid silent breakage from upstream changes.

---

## Package: `@forge/runtime`

### File structure

```
packages/runtime/
  package.json
  tsconfig.json
  src/
    index.ts              re-exports
    loop.ts               ConversationLoop class
    context.ts            ContextLoader
    prompt.ts             system prompt assembly
    session.ts            JSONL session persistence
    provider/
      interface.ts        LLMProvider (re-exported from types)
      anthropic.ts        AnthropicProvider implementation
```

### ContextLoader

Crawls the filesystem on session start. Follows the same resolution order as
the Agent SDK:

```
 Load order (all additive, no overrides):

 1. User level   (~/.claude/)
    ├─ CLAUDE.md
    ├─ rules/*.md
    ├─ settings.json
    └─ skills/*/SKILL.md

 2. Parent dirs  (walk from cwd upward, stop at fs root or .claude/ found)
    └─ CLAUDE.md in each ancestor

 3. Project level (<cwd>/)
    ├─ CLAUDE.md  (or .claude/CLAUDE.md)
    ├─ .claude/rules/*.md
    ├─ .claude/settings.json
    ├─ .claude/skills/*/SKILL.md
    └─ .claude/agents/*.md

 4. Local level  (<cwd>/)
    ├─ CLAUDE.local.md
    └─ .claude/settings.local.json
```

Settings merge with precedence: local > project > user (local wins).

Skills are discovered but only descriptions are loaded (lazy). Full SKILL.md
content is loaded on-demand when the Skill tool is invoked.

Agent definitions are parsed from `.claude/agents/*.md` frontmatter (YAML)
plus markdown body as the prompt.

```typescript
class ContextLoader {
  constructor(private cwd: string);

  async load(sources: ("user" | "project" | "local")[]): Promise<ContextBundle>;

  // Called by Skill tool at invocation time
  async loadSkillContent(name: string): Promise<string>;
}
```

### System prompt assembly

```typescript
function assembleSystemPrompt(
  context: ContextBundle,
  toolRegistry: ToolRegistry,
): SystemBlock[] {
  // Returns an array of SystemBlocks:
  // 1. Base coding agent prompt (extracted from Claude Code)
  // 2. Tool usage instructions (from registry promptFragments)
  // 3. CLAUDE.md content (wrapped in system-reminder tags)
  // 4. Rules content
  // 5. Skill descriptions (names + when-to-use)
  // 6. Agent descriptions (names + when-to-use)
  // 7. Environment info (cwd, platform, date)
}
```

The base system prompt is a static string extracted from Claude Code's
`claude_code` preset. It contains the core coding agent personality, safety
guidelines, and general tool usage patterns.

CLAUDE.md and rules content are injected as `<system-reminder>` blocks,
matching how the SDK injects them.

### ConversationLoop

The core agentic loop. Replaces `query()` from the SDK.

```typescript
class ConversationLoop {
  constructor(opts: {
    provider: LLMProvider;
    tools: ToolRegistry;
    context: ContextBundle;
    cwd: string;
    sessionStore: SessionStore;
    threadId: string;
    model?: string;
    thinking?: ThinkingConfig;
    maxTurns?: number;
    signal?: AbortSignal;
  });

  // Send a message and stream responses
  async *send(prompt: string): AsyncIterable<OutboundEvent>;

  // Resume from existing session
  async *resume(sessionId: string, prompt: string): AsyncIterable<OutboundEvent>;

  // Current session ID
  get sessionId(): string;
}
```

Internal flow for `send()`:

```
1. Append user message to conversation history
2. Persist user message to session JSONL
3. Loop:
   a. Assemble ChatRequest (system prompt, history, tools)
   b. Call provider.chat(request) -> stream
   c. Collect assistant message from stream deltas
      - Emit text deltas as OutboundEvent { type: "text" }
      - Collect tool_use blocks
   d. Persist assistant message to session JSONL
   e. If no tool_use blocks -> emit "done", break
   f. For each tool_use block:
      - Emit OutboundEvent { type: "tool_use", toolName }
      - Execute tool via registry
      - Append tool_result to history
      - Persist tool_result to session JSONL
   g. Increment turn counter; if maxTurns reached -> emit "done", break
   h. Continue loop (go to 3a)
```

### AnthropicProvider

```typescript
class AnthropicProvider implements LLMProvider {
  constructor(opts: { apiKey: string; baseUrl?: string });

  async *chat(request: ChatRequest): ChatStream {
    // Uses @anthropic-ai/sdk Messages API with streaming
    // Maps Anthropic stream events to ChatDelta
    // Handles:
    //   - message_start / content_block_start / content_block_delta / message_stop
    //   - thinking blocks (if thinking config enabled)
    //   - tool_use content blocks
    //   - Rate limiting (retry with backoff)
    //   - Server tool passthrough (web_search)
  }
}
```

The Anthropic SDK (`@anthropic-ai/sdk`) is a direct dependency of
`@forge/runtime` — it's a lightweight HTTP client, not the agent SDK.

### SessionStore

```typescript
class SessionStore {
  constructor(private baseDir: string);

  // Append a message to session transcript
  async append(sessionId: string, message: SessionMessage): Promise<void>;

  // Load all messages for a session
  async load(sessionId: string): Promise<SessionMessage[]>;

  // Get session metadata
  async getMeta(sessionId: string): Promise<SessionMeta | undefined>;

  // List sessions for a directory
  async list(cwd: string): Promise<SessionMeta[]>;
}
```

Storage format: JSONL file at `<baseDir>/<threadId>/<sessionId>.jsonl`.
Each line is a JSON-serialized `SessionMessage`.

---

## Package: `@forge/server` (changes)

### worker.ts

The only file that changes. Before:

```typescript
import { query } from "@anthropic-ai/claude-agent-sdk";
```

After:

```typescript
import { ConversationLoop, ContextLoader, AnthropicProvider, SessionStore } from "@forge/runtime";
import { createDefaultRegistry } from "@forge/tools";

export async function startWorker(threadId: string): Promise<void> {
  const cwd = config.worker.workspaceDir;
  const provider = new AnthropicProvider({ apiKey: process.env.ANTHROPIC_API_KEY! });
  const tools = createDefaultRegistry(cwd);
  const contextLoader = new ContextLoader(cwd);
  const context = await contextLoader.load(["user", "project", "local"]);
  const sessionStore = new SessionStore(config.worker.sessionsDir);

  let sessionId: string | undefined;

  // Restore session from thread metadata
  const meta = getThread(threadId);
  if (meta?.sessionId) {
    sessionId = meta.sessionId;
  }

  while (true) {
    const msg = await pullMessage(threadId);

    const loop = new ConversationLoop({
      provider,
      tools,
      context,
      cwd,
      sessionStore,
      threadId,
      model: "claude-sonnet-4-5-20250929",
    });

    const generator = sessionId
      ? loop.resume(sessionId, msg.text)
      : loop.send(msg.text);

    for await (const event of generator) {
      publishEvent(threadId, event);
    }

    // Persist session for resume
    sessionId = loop.sessionId;
    const thread = getThread(threadId)!;
    thread.sessionId = sessionId;
    thread.lastActiveAt = Date.now();
    setThread(thread);
  }
}
```

### package.json

Remove `@anthropic-ai/claude-agent-sdk`. Add:
- `@forge/runtime`
- `@forge/tools`

---

## Dependencies

### New external dependencies

| Package | Used by | Purpose |
|---------|---------|---------|
| `@anthropic-ai/sdk` | `@forge/runtime` | Anthropic Messages API client (HTTP only, not the agent SDK) |
| `fast-glob` | `@forge/tools` | Glob tool file matching |

### Existing external dependency reuse

| Tool | External binary | Notes |
|------|----------------|-------|
| Grep | `rg` (ripgrep) | Expected on PATH; Claude Code requires it too |

### Removed dependencies

| Package | Reason |
|---------|--------|
| `@anthropic-ai/claude-agent-sdk` | Replaced by `@forge/runtime` + `@forge/tools` |

---

## Implementation order

Phase 1: Foundation
1. Expand `@forge/types` with new type definitions
2. Create `@forge/tools` package scaffold + ToolRegistry
3. Implement Tier 1 tools (Read, Write, Edit, Bash, Glob, Grep, WebSearch, WebFetch)

Phase 2: Runtime
4. Create `@forge/runtime` package scaffold
5. Implement AnthropicProvider (Messages API streaming)
6. Implement ContextLoader (CLAUDE.md, skills, agents, rules discovery)
7. Implement system prompt assembly
8. Implement ConversationLoop (agentic loop with tool execution)
9. Implement SessionStore (JSONL persistence)

Phase 3: Integration
10. Update `@forge/server` worker.ts to use new runtime
11. Remove `@anthropic-ai/claude-agent-sdk` dependency
12. End-to-end test: CLI -> gateway -> runtime -> Anthropic API -> tool execution

Phase 4: Orchestration tools
13. Implement Skill tool (lazy loading from ContextBundle)
14. Implement Agent tool (sub-ConversationLoop)
15. Implement AskUserQuestion (event-based, answer via message queue)
16. Implement plan mode tools (EnterPlanMode, ExitPlanMode)
17. Implement task management tools (TaskCreate/Get/Update/List)

Phase 5: Extended tools
18. Remaining Tier 3 tools as needed

---

## Testing strategy

- Each tool gets unit tests with real filesystem operations (tmpdir)
- Bash tool tests use actual child_process (no mocks)
- ContextLoader tests use a fixture directory tree with CLAUDE.md, skills, agents
- ConversationLoop tests use a mock LLMProvider that returns canned responses
  with tool_use blocks to verify the execute-and-continue cycle
- Integration test: spin up gateway, send message via CLI, verify tool execution
  produces expected file changes
- Fake data uses Community references per project conventions

---

## Implementation notes

- `config.worker.sessionsDir` must be added to `config.ts` (e.g. default
  `/tmp/forge/sessions` or `~/.forge/sessions`)
- WebSearch uses Anthropic's server-side tool (`type: "web_search_20250305"`),
  passed directly in the API request as a server tool — but the tool registry
  still needs a schema entry so the model sees it in the tool list and the
  gateway can emit `tool_use` events for it
- WebFetch needs an html-to-markdown library; `node-html-markdown` is small
  and well-maintained
- The Skill tool needs access to `ContextLoader.loadSkillContent()` via the
  `ToolContext.runtime` handle — this is the only tool that reaches back into
  the runtime (Agent tool also does, for spawning sub-loops)
- Tool schemas should be extracted from Claude Code v1.0.x (current stable)
  and pinned; we update manually, not automatically

## Open questions

None — all design decisions are resolved. Implementation can begin.
