// Package types - task definitions
package types

import (
	"context"
	"time"
)

// TaskType identifies the kind of background task.
type TaskType string

const (
	TaskTypeBash  TaskType = "bash"
	TaskTypeAgent TaskType = "agent"
)

// TaskStatus tracks the lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusKilled    TaskStatus = "killed"
)

// IsTerminal returns true if the task is in a final state.
func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusCompleted || s == TaskStatusFailed || s == TaskStatusKilled
}

// Task represents a background task (bash command or sub-agent).
type Task struct {
	ID          string                 `json:"id"`
	Type        TaskType               `json:"type"`
	Status      TaskStatus             `json:"status"`
	Description string                 `json:"description"`
	StartTime   time.Time              `json:"startTime"`
	EndTime     *time.Time             `json:"endTime,omitempty"`
	Output      string                 `json:"output,omitempty"`    // Captured stdout/stderr
	Error       string                 `json:"error,omitempty"`     // Error message if failed
	ExitCode    *int                   `json:"exitCode,omitempty"`  // Exit code for bash tasks
	ToolUseID   string                 `json:"toolUseId,omitempty"` // Originating tool_use ID
	Metadata    map[string]interface{} `json:"metadata,omitempty"`  // Arbitrary metadata
	SessionID   string                 `json:"sessionId"`           // Parent session
	AgentID     string                 `json:"agentId,omitempty"`   // For agent tasks
	Command     string                 `json:"command,omitempty"`   // For bash tasks
	CWD         string                 `json:"cwd,omitempty"`       // Working directory
	Timeout     int                    `json:"timeout,omitempty"`   // Timeout in seconds
	Cancel      context.CancelFunc     `json:"-"`                   // Cancel function
}

// SubAgent represents a spawned sub-agent with tool restrictions.
type SubAgent struct {
	ID              string                 `json:"id"`
	SessionID       string                 `json:"sessionId"`       // Sub-agent's own session
	ParentSessionID string                 `json:"parentSessionId"` // Parent agent's session
	Type            string                 `json:"type"`            // Agent type name
	Description     string                 `json:"description"`     // User-facing description
	Status          TaskStatus             `json:"status"`
	StartTime       time.Time              `json:"startTime"`
	EndTime         *time.Time             `json:"endTime,omitempty"`
	Prompt          string                 `json:"prompt"`                    // System prompt
	Model           string                 `json:"model,omitempty"`           // Model override
	Tools           []string               `json:"tools,omitempty"`           // Allowlist
	DisallowedTools []string               `json:"disallowedTools,omitempty"` // Denylist
	MaxTurns        int                    `json:"maxTurns,omitempty"`        // Max conversation turns
	TurnCount       int                    `json:"turnCount"`                 // Current turn count
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Output          string                 `json:"output,omitempty"`    // Final output
	Error           string                 `json:"error,omitempty"`     // Error message
	ToolUseID       string                 `json:"toolUseId,omitempty"` // Originating tool_use ID
	Messages        []ChatMessage          `json:"messages,omitempty"`  // Conversation history
	Cancel          context.CancelFunc     `json:"-"`                   // Cancel function
}
