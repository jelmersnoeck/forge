package tools

import (
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// QueueOnCompleteTool queues a bash command to run after all work is finished.
var QueueOnCompleteTool = types.ToolDefinition{
	Name:        "QueueOnComplete",
	Description: "Queue a bash command to run once all current work is complete. Useful for final cleanup, commits, deployments, or notifications. The command will run only once, after the entire conversation turn is finished.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute when work is complete",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable description of why this is being queued (optional)",
			},
		},
		"required": []string{"command"},
	},
	Handler:  handleQueueOnComplete,
	ReadOnly: false, // Queuing tasks is a side effect
}

func handleQueueOnComplete(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	command, ok := input["command"].(string)
	if !ok || command == "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{
				{Type: "text", Text: "Error: command is required"},
			},
			IsError: true,
		}, fmt.Errorf("command is required")
	}

	description := ""
	if desc, ok := input["description"].(string); ok {
		description = desc
	}

	// Emit a special event that the worker can intercept
	ctx.Emit(types.OutboundEvent{
		Type:    "queue_on_complete",
		Content: command,
		ToolName: "Bash",
	})

	resultMsg := fmt.Sprintf("✓ Queued to run on completion: %s", command)
	if description != "" {
		resultMsg += fmt.Sprintf("\n  Reason: %s", description)
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{Type: "text", Text: resultMsg},
		},
	}, nil
}
