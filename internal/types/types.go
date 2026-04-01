// Package types defines shared contracts for the forge agent system.
package types

import (
	"context"
	"time"
)

// ── API Messages ─────────────────────────────────────────────

// InboundMessage is a normalized message from any event source.
// Adapters translate platform-specific events into this shape.
type InboundMessage struct {
	SessionID string         `json:"sessionId"`
	Text      string         `json:"text"`
	User      string         `json:"user"`
	Source    string         `json:"source"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Timestamp int64          `json:"timestamp"`
}

// OutboundEvent is streamed back to subscribers via pub/sub.
//
// Type is one of: "text", "tool_use", "done", "error", "thinking",
// "compact", "retry", "usage", "queued_task_result", "queued_task_error",
// "queue_immediate", "queue_on_complete".
type OutboundEvent struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Type      string      `json:"type"`
	Content   string      `json:"content,omitempty"`
	ToolName  string      `json:"toolName,omitempty"`
	Usage     *TokenUsage `json:"usage,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// SessionMeta is stored per conversation session.
type SessionMeta struct {
	SessionID    string         `json:"sessionId"`
	HistoryID    string         `json:"historyId,omitempty"`
	CWD          string         `json:"cwd,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    int64          `json:"createdAt"`
	LastActiveAt int64          `json:"lastActiveAt"`
}

// ── LLM Provider ─────────────────────────────────────────────

// SystemBlock is a block in the system prompt.
type SystemBlock struct {
	Type         string        `json:"type"` // always "text"
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl sets caching behavior on a system block.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// ChatRequest is sent to the LLM provider.
type ChatRequest struct {
	Model     string        `json:"model"`
	System    []SystemBlock `json:"system"`
	Messages  []ChatMessage `json:"messages"`
	Tools     []ToolSchema  `json:"tools"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream"`
}

// ChatMessage is a single message in the conversation history.
type ChatMessage struct {
	Role    string             `json:"role"` // "user" or "assistant"
	Content []ChatContentBlock `json:"content"`
}

// ChatContentBlock is a block within a message.
type ChatContentBlock struct {
	Type      string              `json:"type"` // "text", "tool_use", "tool_result"
	Text      string              `json:"text,omitempty"`
	ID        string              `json:"id,omitempty"`
	Name      string              `json:"name,omitempty"`
	Input     map[string]any      `json:"input,omitempty"`
	ToolUseID string              `json:"tool_use_id,omitempty"`
	Content   []ToolResultContent `json:"content,omitempty"`
}

// ChatDelta is a streaming event from the LLM.
type ChatDelta struct {
	Type        string      `json:"type"` // "text_delta", "tool_use_start", "tool_use_delta", "tool_use_end", "message_stop"
	Text        string      `json:"text,omitempty"`
	ID          string      `json:"id,omitempty"`
	Name        string      `json:"name,omitempty"`
	PartialJSON string      `json:"partialJson,omitempty"`
	StopReason  string      `json:"stopReason,omitempty"`
	Usage       *TokenUsage `json:"usage,omitempty"`
}

// TokenUsage tracks token consumption for a single LLM call.
type TokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// LLMProvider abstracts the LLM API.
type LLMProvider interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan ChatDelta, error)
}

// ── Tool System ──────────────────────────────────────────────

// ToolSchema is sent to the LLM so it knows which tools are available.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolResult is returned by a tool handler.
type ToolResult struct {
	Content []ToolResultContent `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

// ToolResultContent is a block within a tool result.
type ToolResultContent struct {
	Type   string       `json:"type"` // "text" or "image"
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource carries base64 image data.
type ImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// ToolContext is passed to every tool handler.
type ToolContext struct {
	Ctx       context.Context
	CWD       string
	SessionID string
	HistoryID string
	Emit      func(OutboundEvent)
}

// ToolHandler executes a tool.
type ToolHandler func(input map[string]any, ctx ToolContext) (ToolResult, error)

// ToolDefinition registers a tool with the registry.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     ToolHandler
	ReadOnly    bool
	Destructive bool
}

// ── Audit Logging ────────────────────────────────────────────

// ToolCallEvent records a single tool invocation for audit/observability.
type ToolCallEvent struct {
	SessionID string
	ToolName  string
	Input     map[string]any
	Duration  time.Duration
	Error     error
}

// AuditLogger receives structured events about agent activity.
// Implementations must be safe for concurrent use.
type AuditLogger interface {
	LogToolCall(ToolCallEvent)
}

// ── Context Loading ──────────────────────────────────────────

// ContextBundle holds all discovered project context.
type ContextBundle struct {
	ClaudeMD          []ClaudeMDEntry
	Rules             []RuleEntry
	SkillDescriptions []SkillDescription
	AgentDefinitions  map[string]AgentDefinition
	Settings          MergedSettings
}

// ClaudeMDEntry is a single CLAUDE.md file.
type ClaudeMDEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Level   string `json:"level"` // "user", "project", "local", "parent"
}

// RuleEntry is a single rule file.
type RuleEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Level   string `json:"level"` // "user", "project"
}

// SkillDescription is a discovered skill (lazy — content loaded on demand).
type SkillDescription struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Path            string `json:"path"`
	IsUserInvocable bool   `json:"isUserInvocable"`
}

// AgentDefinition is a custom agent from .claude/agents/.
type AgentDefinition struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Model           string   `json:"model,omitempty"`
	MaxTurns        int      `json:"maxTurns,omitempty"`
}

// MergedSettings is the merged result of user + project + local settings.
type MergedSettings struct {
	Model       string            `json:"model,omitempty"`
	Permissions *PermissionConfig `json:"permissions,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// PermissionConfig holds allow/deny lists.
type PermissionConfig struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// ── Session Persistence ──────────────────────────────────────

// SessionMessage is a single entry in the session JSONL.
type SessionMessage struct {
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid,omitempty"`
	SessionID  string `json:"sessionId"`
	Type       string `json:"type"` // "user", "assistant", "system"
	Message    any    `json:"message"`
	Timestamp  int64  `json:"timestamp"`
}
