// Package tools - task management tools
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/jelmersnoeck/forge/internal/types"
)

// taskMgr is set by SetTaskManager during worker init.
// Falls back to a default manager if not set.
var taskMgr *task.Manager

func init() {
	taskMgr = task.NewManager()
}

// SetTaskManager configures the task manager used by task and agent tools.
// Must be called before any task/agent tool invocations (typically during worker setup).
func SetTaskManager(m *task.Manager) {
	taskMgr = m
}

// TaskCreateTool creates background tasks (bash commands).
func TaskCreateTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "TaskCreate",
		Description: "Create a background task that runs asynchronously. Use this to run long-running commands without blocking the conversation. IMPORTANT: Always set a timeout to prevent stuck commands.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{
					"type":        "string",
					"description": "A brief description of what the task does (shown to user)",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to run in the background",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Maximum execution time in seconds (0 = no timeout, but NOT recommended). Suggest: 300 for builds, 600 for tests, 60 for quick tasks.",
				},
			},
			"required": []string{"description", "command"},
		},
		Handler:  handleTaskCreate,
		ReadOnly: false,
	}
}

func handleTaskCreate(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	description, ok := input["description"].(string)
	if !ok {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: "description must be a string"}},
			IsError: true,
		}, nil
	}

	command, ok := input["command"].(string)
	if !ok {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: "command must be a string"}},
			IsError: true,
		}, nil
	}

	timeout := 0
	if t, ok := input["timeout"].(float64); ok {
		timeout = int(t)
	}

	task, err := taskMgr.CreateBashTask(ctx.SessionID, description, command, ctx.CWD, timeout)
	if err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: fmt.Sprintf("failed to create task: %v", err)}},
			IsError: true,
		}, nil
	}

	// Emit task created event
	ctx.Emit(types.OutboundEvent{
		Type:      "task_created",
		SessionID: ctx.SessionID,
		Content:   fmt.Sprintf("Background task created: %s (ID: %s)", description, task.ID),
	})

	result := map[string]any{
		"taskId":      task.ID,
		"description": task.Description,
		"status":      string(task.Status),
		"command":     task.Command,
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	// Build response message
	var responseText strings.Builder
	responseText.WriteString(fmt.Sprintf("Task created successfully:\n%s\n\n", resultJSON))

	// Warn if no timeout is set for potentially long-running commands
	if timeout == 0 {
		responseText.WriteString("⚠️  WARNING: This task has no timeout and may run indefinitely if stuck.\n")
		responseText.WriteString("   Consider setting a timeout (e.g., timeout: 300 for 5 minutes) or use TaskStop() if needed.\n\n")
	}

	responseText.WriteString(fmt.Sprintf("Use TaskGet(\"%s\") to check status or TaskOutput(\"%s\") to retrieve output when complete.", task.ID, task.ID))

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{
				Type: "text",
				Text: responseText.String(),
			},
		},
	}, nil
}

// TaskGetTool retrieves task status.
func TaskGetTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "TaskGet",
		Description: "Get the current status of a background task.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task ID returned by TaskCreate",
				},
			},
			"required": []string{"task_id"},
		},
		Handler:  handleTaskGet,
		ReadOnly: true,
	}
}

func handleTaskGet(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	taskID, ok := input["task_id"].(string)
	if !ok {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: "task_id must be a string"}},
			IsError: true,
		}, nil
	}

	task, found := taskMgr.GetTask(taskID)
	if !found {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: fmt.Sprintf("task not found: %s", taskID)}},
			IsError: true,
		}, nil
	}

	result := map[string]any{
		"id":          task.ID,
		"type":        string(task.Type),
		"status":      string(task.Status),
		"description": task.Description,
		"startTime":   task.StartTime.Format("2006-01-02 15:04:05"),
	}

	if task.EndTime != nil {
		result["endTime"] = task.EndTime.Format("2006-01-02 15:04:05")
		result["duration"] = task.EndTime.Sub(task.StartTime).String()
	}

	if task.ExitCode != nil {
		result["exitCode"] = *task.ExitCode
	}

	if task.Error != "" {
		result["error"] = task.Error
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{
				Type: "text",
				Text: string(resultJSON),
			},
		},
	}, nil
}

// TaskListTool lists all tasks for the current session.
func TaskListTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "TaskList",
		Description: "List all background tasks for the current session.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler:  handleTaskList,
		ReadOnly: true,
	}
}

func handleTaskList(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	tasks := taskMgr.ListTasks(ctx.SessionID)

	if len(tasks) == 0 {
		return types.ToolResult{
			Content: []types.ToolResultContent{
				{
					Type: "text",
					Text: "No background tasks found for this session.",
				},
			},
		}, nil
	}

	result := make([]map[string]any, len(tasks))
	for i, task := range tasks {
		item := map[string]any{
			"id":          task.ID,
			"status":      string(task.Status),
			"description": task.Description,
			"startTime":   task.StartTime.Format("2006-01-02 15:04:05"),
		}
		if task.EndTime != nil {
			item["endTime"] = task.EndTime.Format("2006-01-02 15:04:05")
		}
		result[i] = item
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{
				Type: "text",
				Text: string(resultJSON),
			},
		},
	}, nil
}

// TaskStopTool stops a running task.
func TaskStopTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "TaskStop",
		Description: "Stop a running background task.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task ID to stop",
				},
			},
			"required": []string{"task_id"},
		},
		Handler:  handleTaskStop,
		ReadOnly: false,
	}
}

func handleTaskStop(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	taskID, ok := input["task_id"].(string)
	if !ok {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: "task_id must be a string"}},
			IsError: true,
		}, nil
	}

	if err := taskMgr.StopTask(taskID); err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: fmt.Sprintf("failed to stop task: %v", err)}},
			IsError: true,
		}, nil
	}

	ctx.Emit(types.OutboundEvent{
		Type:      "task_stopped",
		SessionID: ctx.SessionID,
		Content:   fmt.Sprintf("Task stopped: %s", taskID),
	})

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{
				Type: "text",
				Text: fmt.Sprintf("Task %s stopped successfully.", taskID),
			},
		},
	}, nil
}

// TaskOutputTool retrieves task output.
func TaskOutputTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "TaskOutput",
		Description: "Get the output from a completed background task.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task ID",
				},
			},
			"required": []string{"task_id"},
		},
		Handler:  handleTaskOutput,
		ReadOnly: true,
	}
}

func handleTaskOutput(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	taskID, ok := input["task_id"].(string)
	if !ok {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: "task_id must be a string"}},
			IsError: true,
		}, nil
	}

	task, found := taskMgr.GetTask(taskID)
	if !found {
		return types.ToolResult{
			Content: []types.ToolResultContent{{Type: "text", Text: fmt.Sprintf("task not found: %s", taskID)}},
			IsError: true,
		}, nil
	}

	if !task.Status.IsTerminal() {
		return types.ToolResult{
			Content: []types.ToolResultContent{
				{
					Type: "text",
					Text: fmt.Sprintf("Task is still %s. Wait for completion before retrieving output.", task.Status),
				},
			},
		}, nil
	}

	output := task.Output
	if output == "" {
		output = "(no output)"
	}

	result := fmt.Sprintf("Task %s (%s):\nStatus: %s\n", task.ID, task.Description, task.Status)
	if task.ExitCode != nil {
		result += fmt.Sprintf("Exit Code: %d\n", *task.ExitCode)
	}
	if task.Error != "" {
		result += fmt.Sprintf("Error: %s\n", task.Error)
	}
	result += fmt.Sprintf("\nOutput:\n%s", output)

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{
				Type: "text",
				Text: result,
			},
		},
	}, nil
}
