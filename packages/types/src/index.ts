// Inbound message — normalized from any event source.
// Adapters translate platform-specific events into this shape.
export interface InboundMessage {
  threadId: string;
  text: string;
  user: string;
  source: string;
  metadata?: Record<string, unknown>;
  timestamp: number;
}

// Outbound event — streamed back to subscribers via pub/sub.
// Adapters consume these and translate to their platform's format.
//
//   text        → agent's natural-language output
//   tool_use    → agent is calling a tool (name in toolName)
//   done        → agent finished processing this message
//   error       → something went wrong
export interface OutboundEvent {
  id: string;
  threadId: string;
  type: "text" | "tool_use" | "done" | "error";
  content?: string;
  toolName?: string;
  timestamp: number;
}

// Thread metadata stored in Redis.
// `metadata` is an opaque bag owned by the creating adapter — the core
// never inspects it. Adapters use it to store platform-specific
// correlation data (e.g. Slack channel+thread_ts, Linear issue ID).
export interface ThreadMeta {
  threadId: string;
  sessionId?: string;
  metadata: Record<string, unknown>;
  createdAt: number;
  lastActiveAt: number;
}

// ── LLM Provider ─────────────────────────────────────────────

export interface SystemBlock {
  type: "text";
  text: string;
  cacheControl?: { type: "ephemeral" };
}

export type ThinkingConfig =
  | { type: "adaptive" }
  | { type: "enabled"; budgetTokens: number }
  | { type: "disabled" };

export interface ChatRequest {
  model: string;
  system: SystemBlock[];
  messages: ChatMessage[];
  tools: ToolSchema[];
  maxTokens: number;
  thinking?: ThinkingConfig;
  stream: true;
}

export type ChatMessage =
  | { role: "user"; content: ChatContentBlock[] }
  | { role: "assistant"; content: ChatContentBlock[] };

export type ChatContentBlock =
  | { type: "text"; text: string }
  | { type: "tool_use"; id: string; name: string; input: Record<string, unknown> }
  | { type: "tool_result"; tool_use_id: string; content: ToolResultContent[] };

export type ChatDelta =
  | { type: "text_delta"; text: string }
  | { type: "thinking_delta"; thinking: string }
  | { type: "tool_use_start"; id: string; name: string }
  | { type: "tool_use_delta"; id: string; partialJson: string }
  | { type: "tool_use_end"; id: string }
  | { type: "message_stop"; stopReason: string };

export type ChatStream = AsyncIterable<ChatDelta>;

export interface LLMProvider {
  chat(request: ChatRequest): ChatStream;
}

// ── Tool System ──────────────────────────────────────────────

export interface ToolSchema {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
}

export interface ToolResult {
  content: ToolResultContent[];
  isError?: boolean;
}

export type ToolResultContent =
  | { type: "text"; text: string }
  | { type: "image"; source: { type: "base64"; media_type: string; data: string } };

export interface ToolContext {
  cwd: string;
  sessionId: string;
  threadId: string;
  signal: AbortSignal;
  emit: (event: OutboundEvent) => void;
  runtime?: RuntimeHandle;
}

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  handler: (input: Record<string, unknown>, ctx: ToolContext) => Promise<ToolResult>;
  annotations?: { readOnly?: boolean; destructive?: boolean };
}

// ── Context Loading ──────────────────────────────────────────

export interface ContextBundle {
  claudeMd: ClaudeMdEntry[];
  rules: RuleEntry[];
  skillDescriptions: SkillDescription[];
  agentDefinitions: Record<string, AgentDefinition>;
  settings: MergedSettings;
}

export interface ClaudeMdEntry {
  path: string;
  content: string;
  level: "user" | "project" | "local" | "parent";
}

export interface RuleEntry {
  path: string;
  content: string;
  level: "user" | "project";
}

export interface SkillDescription {
  name: string;
  description: string;
  path: string;
  isUserInvocable: boolean;
}

export interface AgentDefinition {
  name: string;
  description: string;
  prompt: string;
  tools?: string[];
  disallowedTools?: string[];
  model?: "sonnet" | "opus" | "haiku" | "inherit";
  maxTurns?: number;
}

export interface MergedSettings {
  permissions?: { allow: string[]; deny: string[] };
  env?: Record<string, string>;
  model?: string;
}

// ── Session Persistence ──────────────────────────────────────

export interface SessionMessage {
  uuid: string;
  parentUuid?: string;
  sessionId: string;
  type: "user" | "assistant" | "system";
  message: unknown;
  timestamp: number;
}

export interface SessionMeta {
  sessionId: string;
  threadId: string;
  cwd: string;
  createdAt: number;
  lastActiveAt: number;
  title?: string;
}

// ── Runtime Handle (for Skill/Agent tools) ───────────────────

export interface RuntimeHandle {
  loadSkillContent(name: string): Promise<string>;
  spawnSubagent(opts: SubagentOptions): Promise<ToolResult>;
}

export interface SubagentOptions {
  prompt: string;
  agentType: string;
  model?: string;
  tools?: string[];
  maxTurns?: number;
}
