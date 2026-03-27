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
