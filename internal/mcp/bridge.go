package mcp

import (
	"context"
	"fmt"
	"log"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// ToolPrefix formats a namespaced tool name for MCP tools.
// Format: mcp__<serverName>__<toolName>
func ToolPrefix(serverName, toolName string) string {
	return fmt.Sprintf("mcp__%s__%s", serverName, toolName)
}

// RegisterMCPTools discovers tools from an MCP client and registers them
// into Forge's tool registry with namespaced names.
func RegisterMCPTools(registry *tools.Registry, client *Client) error {
	mcpTools, err := client.ListTools(context.Background())
	if err != nil {
		return fmt.Errorf("list tools from %s: %w", client.ServerName(), err)
	}

	for _, tool := range mcpTools {
		name := ToolPrefix(client.ServerName(), tool.Name)
		desc := fmt.Sprintf("[MCP: %s] %s", client.ServerName(), tool.Description)

		// Capture for closure
		mcpClient := client
		mcpToolName := tool.Name

		registry.Register(types.ToolDefinition{
			Name:        name,
			Description: desc,
			InputSchema: tool.InputSchema,
			Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
				return callMCPTool(ctx.Ctx, mcpClient, mcpToolName, input)
			},
		})

		log.Printf("[mcp:%s] registered tool: %s", client.ServerName(), name)
	}

	log.Printf("[mcp:%s] registered %d tools", client.ServerName(), len(mcpTools))
	return nil
}

func callMCPTool(ctx context.Context, client *Client, toolName string, input map[string]any) (types.ToolResult, error) {
	result, err := client.CallTool(ctx, toolName, input)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("MCP tool %s: %w", toolName, err)
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
			// Unknown content types get stringified
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

// ConnectAndRegister connects to a configured MCP server and registers its tools.
// This is the high-level entry point for MCP integration.
func ConnectAndRegister(ctx context.Context, registry *tools.Registry, serverName string, cfg MCPServerConfig, tokenStore *TokenStore) (*Client, error) {
	var opts []ClientOption

	switch cfg.Auth {
	case "oauth":
		if tokenStore == nil {
			return nil, fmt.Errorf("OAuth auth requires a token store")
		}
		opts = append(opts, WithOAuth(tokenStore))
	}

	if len(cfg.Headers) > 0 {
		opts = append(opts, WithHeaders(cfg.Headers))
	}

	client := NewClient(serverName, cfg.URL, opts...)

	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", serverName, err)
	}

	if err := RegisterMCPTools(registry, client); err != nil {
		client.Close(ctx)
		return nil, fmt.Errorf("register tools from %s: %w", serverName, err)
	}

	return client, nil
}
