// Package tools - task management tools
package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	description, err := requireString(input, "description")
	if err != nil {
		return errResult("description must be a string")
	}
	command, err := requireString(input, "command")
	if err != nil {
		return errResult("command must be a string")
	}

	if target := commandAccessesEnvFile(command); target != "" {
		return envFileError(target), nil
	}

	timeout := int(optionalFloat(input, "timeout", 0))

	task, err := taskMgr.CreateBashTask(ctx.SessionID, description, command, ctx.CWD, timeout)
	if err != nil {
		return errResultf("failed to create task: %v", err)
	}

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

	var responseText strings.Builder
	fmt.Fprintf(&responseText, "Task created successfully:\n%s\n\n", resultJSON)

	if timeout == 0 {
		responseText.WriteString("WARNING: This task has no timeout and may run indefinitely if stuck.\n")
		responseText.WriteString("   Consider setting a timeout (e.g., timeout: 300 for 5 minutes) or use TaskStop() if needed.\n\n")
	}

	fmt.Fprintf(&responseText, "Use TaskGet(\"%s\") to check status or TaskOutput(\"%s\") to retrieve output when complete.", task.ID, task.ID)

	return textResult(responseText.String()), nil
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
	taskID, err := requireString(input, "task_id")
	if err != nil {
		return errResult("task_id must be a string")
	}

	snap, found := taskMgr.GetTaskSnapshot(taskID)
	if !found {
		return errResultf("task not found: %s", taskID)
	}

	// Emit task_status for the CLI's inline progress display.
	emitTaskStatus(ctx, snap.ID, snap.Description, string(snap.Status), snap.Output, snap.StartTime, snap.EndTime)

	result := map[string]any{
		"id":          snap.ID,
		"type":        string(snap.Type),
		"status":      string(snap.Status),
		"description": snap.Description,
		"startTime":   snap.StartTime.Format("2006-01-02 15:04:05"),
	}

	if snap.EndTime != nil {
		result["endTime"] = snap.EndTime.Format("2006-01-02 15:04:05")
		result["duration"] = snap.EndTime.Sub(snap.StartTime).String()
	}
	if snap.ExitCode != nil {
		result["exitCode"] = *snap.ExitCode
	}
	if snap.Error != "" {
		result["error"] = snap.Error
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(resultJSON)), nil
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
		return textResult("No background tasks found for this session."), nil
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
	return textResult(string(resultJSON)), nil
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
	taskID, err := requireString(input, "task_id")
	if err != nil {
		return errResult("task_id must be a string")
	}

	if err := taskMgr.StopTask(taskID); err != nil {
		return errResultf("failed to stop task: %v", err)
	}

	ctx.Emit(types.OutboundEvent{
		Type:      "task_stopped",
		SessionID: ctx.SessionID,
		Content:   fmt.Sprintf("Task stopped: %s", taskID),
	})

	return textResult(fmt.Sprintf("Task %s stopped successfully.", taskID)), nil
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
	taskID, err := requireString(input, "task_id")
	if err != nil {
		return errResult("task_id must be a string")
	}

	snap, found := taskMgr.GetTaskSnapshot(taskID)
	if !found {
		return errResultf("task not found: %s", taskID)
	}

	if !snap.Status.IsTerminal() {
		return textResult(fmt.Sprintf("Task is still %s. Wait for completion before retrieving output.", snap.Status)), nil
	}

	output := snap.Output
	if output == "" {
		output = "(no output)"
	}

	result := fmt.Sprintf("Task %s (%s):\nStatus: %s\n", snap.ID, snap.Description, snap.Status)
	if snap.ExitCode != nil {
		result += fmt.Sprintf("Exit Code: %d\n", *snap.ExitCode)
	}
	if snap.Error != "" {
		result += fmt.Sprintf("Error: %s\n", snap.Error)
	}
	result += fmt.Sprintf("\nOutput:\n%s", output)

	return textResult(result), nil
}

// emitTaskStatus sends a task_status event for the CLI's inline progress
// display. The Content field is a JSON object with id, description, status,
// and the last 5 lines of output (outputTail). The CLI uses this to render
// a live-updating spinner block instead of repeated [TaskGet] lines.
func emitTaskStatus(ctx types.ToolContext, id, description, status, output string, startTime time.Time, endTime *time.Time) {
	// Extract last 5 non-empty lines of output.
	var tail []string
	if output != "" {
		lines := strings.Split(output, "\n")
		// Walk backwards to collect up to 5 non-empty lines.
		for i := len(lines) - 1; i >= 0 && len(tail) < 5; i-- {
			if strings.TrimSpace(lines[i]) != "" {
				tail = append([]string{lines[i]}, tail...)
			}
		}
	}

	payload := map[string]any{
		"id":          id,
		"description": description,
		"status":      status,
		"outputTail":  tail,
		"startTime":   startTime.Format("2006-01-02 15:04:05"),
	}
	if endTime != nil {
		payload["duration"] = endTime.Sub(startTime).String()
	}

	data, _ := json.Marshal(payload)
	ctx.Emit(types.OutboundEvent{
		Type:      "task_status",
		SessionID: ctx.SessionID,
		Content:   string(data),
		Timestamp: time.Now().Unix(),
	})
}
