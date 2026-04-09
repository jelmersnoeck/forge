// Package tools - Agent tool for spawning sub-agents
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// AgentTool spawns sub-agents with tool restrictions.
func AgentTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Agent",
		Description: "Spawn a sub-agent to handle a specific task with tool restrictions. Use this to delegate work to specialized agents with limited capabilities.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Agent type identifier (e.g., 'code_reviewer', 'test_writer')",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Brief description of what this agent should do",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Initial prompt/instructions for the sub-agent",
				},
				"tools": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"description": "List of allowed tools (empty = all tools allowed)",
				},
				"disallowed_tools": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"description": "List of tools this agent cannot use",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model override (optional, defaults to parent's model)",
				},
				"max_turns": map[string]any{
					"type":        "integer",
					"description": "Maximum conversation turns before auto-terminating (0 = unlimited)",
				},
			},
			"required": []string{"type", "description", "prompt"},
		},
		Handler:  handleAgent,
		ReadOnly: false,
	}
}

func handleAgent(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	agentType, err := requireString(input, "type")
	if err != nil {
		return errResult("type must be a string")
	}
	description, err := requireString(input, "description")
	if err != nil {
		return errResult("description must be a string")
	}
	prompt, err := requireString(input, "prompt")
	if err != nil {
		return errResult("prompt must be a string")
	}

	var tools, disallowedTools []string
	model := optionalString(input, "model", "")
	maxTurns := int(optionalFloat(input, "max_turns", -1))

	if t, ok := input["tools"].([]interface{}); ok {
		tools = make([]string, len(t))
		for i, v := range t {
			if s, ok := v.(string); ok {
				tools[i] = s
			}
		}
	}
	if t, ok := input["disallowed_tools"].([]interface{}); ok {
		disallowedTools = make([]string, len(t))
		for i, v := range t {
			if s, ok := v.(string); ok {
				disallowedTools[i] = s
			}
		}
	}

	agent, err := taskMgr.CreateAgent(ctx.SessionID, agentType, description, prompt, model, tools, disallowedTools, maxTurns)
	if err != nil {
		return errResultf("failed to create agent: %v", err)
	}

	ctx.Emit(types.OutboundEvent{
		Type:      "agent_created",
		SessionID: ctx.SessionID,
		Content:   fmt.Sprintf("Sub-agent created: %s (ID: %s, Session: %s)", description, agent.ID, agent.SessionID),
	})

	if err := taskMgr.RunAgent(agent.ID); err != nil {
		return errResultf("agent created but failed to start: %v", err)
	}

	result := map[string]any{
		"agentId":   agent.ID,
		"sessionId": agent.SessionID,
		"type":      agent.Type,
		"status":    string(agent.Status),
		"maxTurns":  agent.MaxTurns,
		"turnCount": agent.TurnCount,
	}
	if len(agent.Tools) > 0 {
		result["allowedTools"] = agent.Tools
	}
	if len(agent.DisallowedTools) > 0 {
		result["disallowedTools"] = agent.DisallowedTools
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return textResult(fmt.Sprintf("Sub-agent created and running:\n%s\n\nUse AgentGet(\"%s\") to check status or AgentStop(\"%s\") to stop.", resultJSON, agent.ID, agent.ID)), nil
}

// AgentGetTool retrieves sub-agent status.
func AgentGetTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "AgentGet",
		Description: "Get the current status of a sub-agent.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The agent ID returned by Agent tool",
				},
			},
			"required": []string{"agent_id"},
		},
		Handler:  handleAgentGet,
		ReadOnly: true,
	}
}

func handleAgentGet(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	agentID, err := requireString(input, "agent_id")
	if err != nil {
		return errResult("agent_id must be a string")
	}

	agent, found := taskMgr.GetAgent(agentID)
	if !found {
		return errResultf("agent not found: %s", agentID)
	}

	// Emit task_status for the CLI's inline progress display.
	emitTaskStatus(ctx, agent.ID, agent.Description, string(agent.Status), agent.Output, agent.StartTime, agent.EndTime)

	result := map[string]any{
		"id":          agent.ID,
		"sessionId":   agent.SessionID,
		"type":        agent.Type,
		"status":      string(agent.Status),
		"description": agent.Description,
		"turnCount":   agent.TurnCount,
		"maxTurns":    agent.MaxTurns,
		"startTime":   agent.StartTime.Format("2006-01-02 15:04:05"),
	}
	if agent.EndTime != nil {
		result["endTime"] = agent.EndTime.Format("2006-01-02 15:04:05")
		result["duration"] = agent.EndTime.Sub(agent.StartTime).String()
	}
	if agent.Error != "" {
		result["error"] = agent.Error
	}
	if agent.Output != "" {
		result["output"] = agent.Output
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(resultJSON)), nil
}

// AgentListTool lists all sub-agents for the current session.
func AgentListTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "AgentList",
		Description: "List all sub-agents spawned by the current session.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler:  handleAgentList,
		ReadOnly: true,
	}
}

func handleAgentList(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	agents := taskMgr.ListAgents(ctx.SessionID)

	if len(agents) == 0 {
		return textResult("No sub-agents found for this session."), nil
	}

	result := make([]map[string]any, len(agents))
	for i, agent := range agents {
		item := map[string]any{
			"id":          agent.ID,
			"sessionId":   agent.SessionID,
			"type":        agent.Type,
			"status":      string(agent.Status),
			"description": agent.Description,
			"turnCount":   agent.TurnCount,
			"startTime":   agent.StartTime.Format("2006-01-02 15:04:05"),
		}
		if agent.EndTime != nil {
			item["endTime"] = agent.EndTime.Format("2006-01-02 15:04:05")
		}
		result[i] = item
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(resultJSON)), nil
}

// AgentStopTool stops a running sub-agent.
func AgentStopTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "AgentStop",
		Description: "Stop a running sub-agent.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The agent ID to stop",
				},
			},
			"required": []string{"agent_id"},
		},
		Handler:  handleAgentStop,
		ReadOnly: false,
	}
}

func handleAgentStop(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	agentID, err := requireString(input, "agent_id")
	if err != nil {
		return errResult("agent_id must be a string")
	}

	if err := taskMgr.StopAgent(agentID); err != nil {
		return errResultf("failed to stop agent: %v", err)
	}

	ctx.Emit(types.OutboundEvent{
		Type:      "agent_stopped",
		SessionID: ctx.SessionID,
		Content:   fmt.Sprintf("Agent stopped: %s", agentID),
	})

	return textResult(fmt.Sprintf("Agent %s stopped successfully.", agentID)), nil
}
