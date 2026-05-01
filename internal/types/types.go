// Package types defines shared contracts for the forge agent system.
package types

import (
	"context"
	"sync"
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
// Type is one of: "text", "tool_use", "done", "error", "interrupted", "thinking",
// "compact", "retry", "usage", "queued_task_result", "queued_task_error",
// "queue_immediate", "queue_on_complete", "pr_monitor", "pr_url",
// "task_status", "intent_classified", "ideation_start", "ideation_candidate",
// "clarification_start", "clarification_question", "planning_start",
// "planning_selection", "staleness_warning", "staleness_error", "phase_error".
type OutboundEvent struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Type      string      `json:"type"`
	Content   string      `json:"content,omitempty"`
	ToolName  string      `json:"toolName,omitempty"`
	Timestamp int64       `json:"timestamp"`
	Usage     *TokenUsage `json:"usage,omitempty"` // for "usage" events
	Model     string      `json:"model,omitempty"` // for "usage" events
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
	Type  string `json:"type"`            // "ephemeral"
	TTL   string `json:"ttl,omitempty"`   // "1h" for extended cache lifetime
	Scope string `json:"scope,omitempty"` // "global" for cross-session caching
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
	Type         string              `json:"type"` // "text", "tool_use", "tool_result"
	Text         string              `json:"text,omitempty"`
	ID           string              `json:"id,omitempty"`
	Name         string              `json:"name,omitempty"`
	Input        map[string]any      `json:"input,omitempty"`
	ToolUseID    string              `json:"tool_use_id,omitempty"`
	Content      []ToolResultContent `json:"content,omitempty"`
	CacheControl *CacheControl       `json:"cache_control,omitempty"`
}

// ChatDelta is a streaming event from the LLM.
type ChatDelta struct {
	Type        string      `json:"type"` // "text_delta", "tool_use_start", "tool_use_delta", "tool_use_end", "message_stop", "error"
	Text        string      `json:"text,omitempty"`
	ID          string      `json:"id,omitempty"`
	Name        string      `json:"name,omitempty"`
	PartialJSON string      `json:"partialJson,omitempty"`
	StopReason  string      `json:"stopReason,omitempty"`
	Usage       *TokenUsage `json:"usage,omitempty"`
	StatusCode  int         `json:"statusCode,omitempty"` // HTTP status from API errors (e.g. 429, 529)
}

// TokenUsage tracks token consumption for a single LLM call.
type TokenUsage struct {
	InputTokens         int `json:"inputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheCreationTokens int `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens     int `json:"cacheReadTokens,omitempty"`
}

// LLMProvider abstracts the LLM API.
type LLMProvider interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan ChatDelta, error)
}

// LightweightModels is a prioritized list of cheap/fast models for auxiliary
// LLM calls (session naming, intent classification, etc.). Callsites should
// try each model in order, falling through on error.
//
// Priority order:
//   - claude-haiku-4-5: alias → latest Haiku; fast path, may 404 during rollouts
//   - claude-haiku-4-5-20251001: pinned release as stability fallback
var LightweightModels = []string{
	"claude-haiku-4-5",
	"claude-haiku-4-5-20251001",
}

// ── Tool System ──────────────────────────────────────────────

// ToolSchema is sent to the LLM so it knows which tools are available.
type ToolSchema struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"`
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

// ReadFileEntry tracks the state of a previously read file for dedup.
//
//	Read("foo.go", offset=1, limit=2000)
//	   → store {MtimeUnix, Offset:1, Limit:2000}
//	Read("foo.go", offset=1, limit=2000)  // same params, file untouched
//	   → return stub instead of 25K tokens of content
//
// Edit/Write delete the entry so the next Read sees fresh content.
type ReadFileEntry struct {
	MtimeUnix int64 // from os.Stat, seconds
	Offset    int
	Limit     int
}

// ReadState tracks per-file read state for dedup within a session.
// Thread-safe for concurrent access from multiple tool goroutines.
type ReadState struct {
	mu      sync.RWMutex
	entries map[string]ReadFileEntry
}

// NewReadState returns an initialized ReadState ready for use.
func NewReadState() *ReadState {
	return &ReadState{entries: make(map[string]ReadFileEntry)}
}

// Get returns the entry and whether it exists.
func (rs *ReadState) Get(path string) (ReadFileEntry, bool) {
	if rs == nil {
		return ReadFileEntry{}, false
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	e, ok := rs.entries[path]
	return e, ok
}

// Set stores a dedup entry for the given path.
func (rs *ReadState) Set(path string, entry ReadFileEntry) {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.entries[path] = entry
}

// Delete removes the entry for the given path (used by Edit/Write).
func (rs *ReadState) Delete(path string) {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.entries, path)
}

// ToolContext is passed to every tool handler.
type ToolContext struct {
	Ctx       context.Context
	CWD       string
	SessionID string
	HistoryID string
	Emit      func(OutboundEvent)
	ReadState *ReadState // per-session file read dedup (nil-safe: tools check before use)
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
	AgentsMD          []AgentsMDEntry
	Rules             []RuleEntry
	SkillDescriptions []SkillDescription
	AgentDefinitions  map[string]AgentDefinition
	Specs             []SpecEntry
	Settings          MergedSettings
}

// AgentsMDEntry is a single AGENTS.md file carrying project instructions
// and/or self-improvement learnings.
type AgentsMDEntry struct {
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

// AgentDefinition is a custom agent from .forge/agents/.
type AgentDefinition struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Model           string   `json:"model,omitempty"`
	MaxTurns        int      `json:"maxTurns,omitempty"`
}

// SpecDocument represents a feature specification — the source of truth
// for implementation and acceptance testing.
type SpecDocument struct {
	// Metadata from YAML frontmatter
	ID     string `json:"id" yaml:"id"`         // unique identifier (slug)
	Status string `json:"status" yaml:"status"` // draft, active, implemented, deprecated

	// Spec content sections (parsed from markdown)
	Header       string `json:"header"`       // summary, max 15 words
	Description  string `json:"description"`  // short description
	Context      string `json:"context"`      // files/systems/interfaces to change
	Behavior     string `json:"behavior"`     // desired behaviour and UX
	Constraints  string `json:"constraints"`  // things to avoid
	Interfaces   string `json:"interfaces"`   // types, signatures, schemas
	EdgeCases    string `json:"edgeCases"`    // known edge cases
	Alternatives string `json:"alternatives"` // alternative approaches considered

	Path string `json:"path"` // filesystem path
}

// SpecEntry is a discovered spec for inclusion in the context bundle.
type SpecEntry struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ID          string `json:"id"`
	Status      string `json:"status"`
	Header      string `json:"header"`      // the 15-word summary
	Description string `json:"description"` // short description (2-4 sentences)
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
	UUID       string             `json:"uuid"`
	ParentUUID string             `json:"parentUuid,omitempty"`
	SessionID  string             `json:"sessionId"`
	Type       string             `json:"type"` // "user", "assistant", "system", "reflection"
	Message    any                `json:"message"`
	Timestamp  int64              `json:"timestamp"`
	Reflection *SessionReflection `json:"reflection,omitempty"` // Metadata for self-improvement
}

// SessionReflection tracks learnings from a session for self-improvement.
type SessionReflection struct {
	Summary     string   `json:"summary"`     // Brief summary of what was accomplished
	Mistakes    []string `json:"mistakes"`    // Things that went wrong or could be better
	Successes   []string `json:"successes"`   // Patterns that worked well
	Suggestions []string `json:"suggestions"` // Ideas for future improvement
}
