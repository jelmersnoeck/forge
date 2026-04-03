package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jelmersnoeck/forge/internal/mcp"
	"github.com/jelmersnoeck/forge/internal/types"
)

// MCPManager manages connections to external MCP servers.
type MCPManager struct {
	clients map[string]*mcp.Client
}

// NewMCPManager creates a new MCP manager.
func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients: make(map[string]*mcp.Client),
	}
}

// Global MCP manager instance
var mcpManager = NewMCPManager()

// Connect connects to an MCP server and initializes it.
func (m *MCPManager) Connect(name, command string, args []string, env []string) error {
	if _, exists := m.clients[name]; exists {
		return fmt.Errorf("MCP server %q already connected", name)
	}

	client, err := mcp.ConnectSTDIO(name, command, args, env)
	if err != nil {
		return fmt.Errorf("failed to connect to MCP server: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("failed to initialize MCP server: %w", err)
	}

	m.clients[name] = client
	return nil
}

// Disconnect disconnects from an MCP server.
func (m *MCPManager) Disconnect(name string) error {
	client, exists := m.clients[name]
	if !exists {
		return fmt.Errorf("MCP server %q not connected", name)
	}

	delete(m.clients, name)
	return client.Close()
}

// GetClient returns the client for a named MCP server.
func (m *MCPManager) GetClient(name string) (*mcp.Client, error) {
	client, exists := m.clients[name]
	if !exists {
		return nil, fmt.Errorf("MCP server %q not connected", name)
	}
	return client, nil
}

// ListConnections returns the names of all connected MCP servers.
func (m *MCPManager) ListConnections() []string {
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}

// Close closes all MCP connections.
func (m *MCPManager) Close() {
	for _, client := range m.clients {
		client.Close()
	}
	m.clients = nil
}

// NewMCPConnectTool creates a tool for connecting to MCP servers.
func NewMCPConnectTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MCPConnect",
		Description: "Connect to an external MCP server",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name for this MCP server connection",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Command to execute (e.g., 'node')",
				},
				"args": map[string]any{
					"type":        "array",
					"description": "Command arguments",
					"items": map[string]any{
						"type": "string",
					},
				},
				"env": map[string]any{
					"type":        "array",
					"description": "Environment variables (optional)",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
			"required": []string{"name", "command", "args"},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			name, _ := input["name"].(string)
			command, _ := input["command"].(string)
			argsRaw, _ := input["args"].([]any)
			envRaw, _ := input["env"].([]any)

			args := make([]string, 0, len(argsRaw))
			for _, arg := range argsRaw {
				if s, ok := arg.(string); ok {
					args = append(args, s)
				}
			}

			env := make([]string, 0, len(envRaw))
			for _, e := range envRaw {
				if s, ok := e.(string); ok {
					env = append(env, s)
				}
			}

			if err := mcpManager.Connect(name, command, args, env); err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("Failed to connect to MCP server: %v", err),
					}},
					IsError: true,
				}, nil
			}

			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: fmt.Sprintf("Successfully connected to MCP server %q", name),
				}},
			}, nil
		},
	}
}

// NewMCPListToolsTool creates a tool for listing tools from an MCP server.
func NewMCPListToolsTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MCPListTools",
		Description: "List tools available from an MCP server",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Name of the MCP server",
				},
			},
			"required": []string{"server"},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			serverName, _ := input["server"].(string)

			client, err := mcpManager.GetClient(serverName)
			if err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("MCP server not found: %v", err),
					}},
					IsError: true,
				}, nil
			}

			ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tools, err := client.ListTools(ctxTimeout)
			if err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("Failed to list tools: %v", err),
					}},
					IsError: true,
				}, nil
			}

			text := fmt.Sprintf("MCP server %q has %d tools:\n\n", serverName, len(tools))
			for _, tool := range tools {
				text += fmt.Sprintf("• %s: %s\n", tool.Name, tool.Description)
			}

			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: text,
				}},
			}, nil
		},
	}
}

// NewMCPCallToolTool creates a tool for calling tools on an MCP server.
func NewMCPCallToolTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MCPCallTool",
		Description: "Call a tool on an MCP server",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Name of the MCP server",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Name of the tool to call",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments to pass to the tool",
				},
			},
			"required": []string{"server", "tool"},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			serverName, _ := input["server"].(string)
			toolName, _ := input["tool"].(string)
			arguments, _ := input["arguments"].(map[string]any)
			if arguments == nil {
				arguments = make(map[string]any)
			}

			client, err := mcpManager.GetClient(serverName)
			if err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("MCP server not found: %v", err),
					}},
					IsError: true,
				}, nil
			}

			ctxTimeout, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			result, err := client.CallTool(ctxTimeout, toolName, arguments)
			if err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("Tool call failed: %v", err),
					}},
					IsError: true,
				}, nil
			}

			// Convert MCP content blocks to Forge content blocks
			blocks := make([]types.ToolResultContent, len(result.Content))
			for i, block := range result.Content {
				blocks[i] = types.ToolResultContent{
					Type: block.Type,
					Text: block.Text,
				}
			}

			return types.ToolResult{
				Content: blocks,
				IsError: result.IsError,
			}, nil
		},
	}
}

// NewMCPListConnectionsTool creates a tool for listing connected MCP servers.
func NewMCPListConnectionsTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MCPListConnections",
		Description: "List all connected MCP servers",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			connections := mcpManager.ListConnections()

			if len(connections) == 0 {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: "No MCP servers connected",
					}},
				}, nil
			}

			text := fmt.Sprintf("Connected MCP servers (%d):\n\n", len(connections))
			for _, name := range connections {
				text += fmt.Sprintf("• %s\n", name)
			}

			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: text,
				}},
			}, nil
		},
	}
}

// NewMCPDisconnectTool creates a tool for disconnecting from an MCP server.
func NewMCPDisconnectTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MCPDisconnect",
		Description: "Disconnect from an MCP server",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Name of the MCP server to disconnect",
				},
			},
			"required": []string{"server"},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			serverName, _ := input["server"].(string)

			if err := mcpManager.Disconnect(serverName); err != nil {
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: fmt.Sprintf("Failed to disconnect: %v", err),
					}},
					IsError: true,
				}, nil
			}

			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: fmt.Sprintf("Disconnected from MCP server %q", serverName),
				}},
			}, nil
		},
	}
}
