package tools

import (
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// QueueImmediateTool queues a bash command to run after the next tool execution.
var QueueImmediateTool = types.ToolDefinition{
	Name:        "QueueImmediate",
	Description: "Queue a bash command to run immediately after the next tool execution. Useful for running checks, tests, or validations after making changes. The command will run after EACH subsequent tool call until cleared.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute after each tool call",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable description of why this is being queued (optional)",
			},
		},
		"required": []string{"command"},
	},
	Handler:  handleQueueImmediate,
	ReadOnly: false, // Queuing tasks is a side effect
}

func handleQueueImmediate(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	command, ok := input["command"].(string)
	if !ok || command == "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{
				{Type: "text", Text: "Error: command is required"},
			},
			IsError: true,
		}, fmt.Errorf("command is required")
	}

	// Check for .env file access
	if target := commandAccessesEnvFile(command); target != "" {
		return envFileError(target), nil
	}

	description := ""
	if desc, ok := input["description"].(string); ok {
		description = desc
	}

	// Emit a special event that the worker can intercept
	ctx.Emit(types.OutboundEvent{
		Type:     "queue_immediate",
		Content:  command,
		ToolName: "Bash",
	})

	resultMsg := fmt.Sprintf("✓ Queued to run after each tool: %s", command)
	if description != "" {
		resultMsg += fmt.Sprintf("\n  Reason: %s", description)
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{Type: "text", Text: resultMsg},
		},
	}, nil
}
