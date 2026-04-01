// Package tools implements the tool registry and built-in tools.
package tools

import (
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// ProgressEvent represents a progress update from a tool.
type ProgressEvent struct {
	ToolUseID string
	Type      string
	Data      any
}

// BashProgress tracks bash command execution progress.
type BashProgress struct {
	Command  string `json:"command"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// GrepProgress tracks grep search progress.
type GrepProgress struct {
	Pattern      string `json:"pattern"`
	FilesScanned int    `json:"filesScanned"`
	MatchCount   int    `json:"matchCount"`
}

// WebSearchProgress tracks web search progress.
type WebSearchProgress struct {
	Query       string `json:"query"`
	ResultCount int    `json:"resultCount"`
	Status      string `json:"status"`
}

// EmitBashProgress emits a bash progress event.
func EmitBashProgress(ctx types.ToolContext, toolUseID string, progress BashProgress) {
	ctx.Emit(types.OutboundEvent{
		SessionID: ctx.SessionID,
		Type:      "tool_progress",
		ToolName:  "Bash",
		Content:   "",
		Timestamp: time.Now().UnixMilli(),
	})
}

// EmitGrepProgress emits a grep progress event.
func EmitGrepProgress(ctx types.ToolContext, toolUseID string, progress GrepProgress) {
	ctx.Emit(types.OutboundEvent{
		SessionID: ctx.SessionID,
		Type:      "tool_progress",
		ToolName:  "Grep",
		Content:   "",
		Timestamp: time.Now().UnixMilli(),
	})
}

// EmitWebSearchProgress emits a web search progress event.
func EmitWebSearchProgress(ctx types.ToolContext, toolUseID string, progress WebSearchProgress) {
	ctx.Emit(types.OutboundEvent{
		SessionID: ctx.SessionID,
		Type:      "tool_progress",
		ToolName:  "WebSearch",
		Content:   "",
		Timestamp: time.Now().UnixMilli(),
	})
}
