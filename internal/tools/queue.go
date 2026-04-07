package tools

import (
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// QueueImmediateTool queues a bash command to run after the next tool execution.
var QueueImmediateTool = makeQueueTool(
	"QueueImmediate",
	"Queue a bash command to run immediately after the next tool execution. Useful for running checks, tests, or validations after making changes. The command will run after EACH subsequent tool call until cleared.",
	"queue_immediate",
	"after each tool",
)

// QueueOnCompleteTool queues a bash command to run after all work is finished.
var QueueOnCompleteTool = makeQueueTool(
	"QueueOnComplete",
	"Queue a bash command to run once all current work is complete. Useful for final cleanup, commits, deployments, or notifications. The command will run only once, after the entire conversation turn is finished.",
	"queue_on_complete",
	"on completion",
)

func makeQueueTool(name, description, eventType, timing string) types.ToolDefinition {
	return types.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute " + timing,
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Human-readable description of why this is being queued (optional)",
				},
			},
			"required": []string{"command"},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			command, err := requireString(input, "command")
			if err != nil {
				return errResult("Error: command is required")
			}

			if target := commandAccessesEnvFile(command); target != "" {
				return envFileError(target), nil
			}

			ctx.Emit(types.OutboundEvent{
				Type:     eventType,
				Content:  command,
				ToolName: "Bash",
			})

			msg := fmt.Sprintf("Queued to run %s: %s", timing, command)
			if desc := optionalString(input, "description", ""); desc != "" {
				msg += fmt.Sprintf("\n  Reason: %s", desc)
			}
			return textResult(msg), nil
		},
		ReadOnly: false,
	}
}
