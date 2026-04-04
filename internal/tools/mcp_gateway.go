package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jelmersnoeck/forge/internal/mcp"
	"github.com/jelmersnoeck/forge/internal/types"
)

// mcpGatewayStore is set by SetMCPStore during worker init.
// The gateway tool reads from it at call time.
var mcpGatewayStore *mcp.Store

// SetMCPStore configures the MCP store used by the UseMCPTool gateway.
// Must be called before the tool is invoked (typically during worker setup).
func SetMCPStore(store *mcp.Store) {
	mcpGatewayStore = store
}

// UseMCPTool returns the gateway tool definition for lazy MCP tool access.
//
// Instead of registering all MCP tools with the LLM (which can cost 15K+
// tokens per server), we expose a single ~300-token tool that lets the model
// discover and call MCP tools on demand.
//
//	UseMCPTool(action="list_servers")
//	  → [{name: "datadog", tool_count: 25}]
//
//	UseMCPTool(action="list_tools", server="datadog")
//	  → [{name: "search_logs", description: "Search raw log entries..."}, ...]
//
//	UseMCPTool(action="call", server="datadog", tool="search_logs", arguments={...})
//	  → (tool result from MCP server)
func UseMCPTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "UseMCPTool",
		Description: `Gateway to external MCP (Model Context Protocol) tool servers. Use this to discover and call tools from connected services like Datadog, Sentry, etc.

Actions:
- list_servers: show connected MCP servers and their tool counts
- list_tools: show available tools on a server (with full schemas for calling)
- call: invoke a specific tool on a server

Workflow: list_servers → list_tools → call. You can skip straight to call if you already know the server, tool name, and required arguments from a previous turn.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list_servers", "list_tools", "call"},
					"description": "Action to perform.",
				},
				"server": map[string]any{
					"type":        "string",
					"description": "MCP server name (required for list_tools and call).",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Tool name on the server (required for call).",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments to pass to the tool (required for call). Use the schema from list_tools to construct these.",
				},
			},
			"required": []string{"action"},
		},
		ReadOnly: true,
		Handler:  handleUseMCPTool,
	}
}

func handleUseMCPTool(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	if mcpGatewayStore == nil || mcpGatewayStore.Empty() {
		return textResult("No MCP servers connected."), nil
	}

	action, _ := input["action"].(string)
	server, _ := input["server"].(string)
	tool, _ := input["tool"].(string)
	arguments, _ := input["arguments"].(map[string]any)

	switch action {
	case "list_servers":
		return handleListServers()
	case "list_tools":
		return handleListTools(server)
	case "call":
		return handleCallTool(ctx, server, tool, arguments)
	default:
		return errResult("Unknown action %q. Use list_servers, list_tools, or call.", action), nil
	}
}

func handleListServers() (types.ToolResult, error) {
	servers := mcpGatewayStore.ListServers()
	var b strings.Builder
	b.WriteString("Connected MCP servers:\n\n")
	for _, s := range servers {
		fmt.Fprintf(&b, "- **%s** (%d tools)\n", s.Name, s.ToolCount)
	}
	return textResult(b.String()), nil
}

func handleListTools(server string) (types.ToolResult, error) {
	if server == "" {
		return errResult("\"server\" is required for list_tools."), nil
	}

	tools, err := mcpGatewayStore.ListTools(server)
	if err != nil {
		return errResult("%v", err), nil
	}

	type toolSummary struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema any    `json:"input_schema"`
	}

	summaries := make([]toolSummary, len(tools))
	for i, t := range tools {
		summaries[i] = toolSummary{
			Name:        t.Name,
			Description: firstSentence(t.Description),
			InputSchema: t.InputSchema,
		}
	}

	b, _ := json.MarshalIndent(summaries, "", "  ")
	return textResult(string(b)), nil
}

func handleCallTool(ctx types.ToolContext, server, tool string, arguments map[string]any) (types.ToolResult, error) {
	if server == "" {
		return errResult("\"server\" is required for call."), nil
	}
	if tool == "" {
		return errResult("\"tool\" is required for call."), nil
	}
	if arguments == nil {
		arguments = make(map[string]any)
	}

	result, err := mcpGatewayStore.CallTool(ctx.Ctx, server, tool, arguments)
	if err != nil {
		return errResult("MCP call failed: %v", err), nil
	}

	content := make([]types.ToolResultContent, 0, len(result.Content))
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			content = append(content, types.ToolResultContent{
				Type: "text",
				Text: c.Text,
			})
		case "image":
			content = append(content, types.ToolResultContent{
				Type: "image",
				Source: &types.ImageSource{
					Type:      "base64",
					MediaType: c.MimeType,
					Data:      c.Data,
				},
			})
		default:
			content = append(content, types.ToolResultContent{
				Type: "text",
				Text: fmt.Sprintf("[%s content: %s]", c.Type, c.Text),
			})
		}
	}

	return types.ToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// firstSentence returns text up to the first period followed by a space or
// end-of-string. Falls back to the first 200 chars if no sentence boundary.
func firstSentence(s string) string {
	if idx := strings.Index(s, ". "); idx >= 0 {
		return s[:idx+1]
	}
	if idx := strings.LastIndex(s, "."); idx >= 0 && idx == len(s)-1 {
		return s
	}
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func textResult(msg string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ToolResultContent{{Type: "text", Text: msg}},
	}
}

func errResult(format string, args ...any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ToolResultContent{{Type: "text", Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}
