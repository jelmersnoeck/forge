package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMCPClient tests the MCP client against the forge MCP server.
func TestMCPClient(t *testing.T) {
	t.Skip("Requires forge MCP server to be built and forge agent to be available")
	
	r := require.New(t)

	// Skip if mcp-server is not built
	// In CI or local dev, run: cd mcp-server && npm install && npm run build
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to the forge MCP server
	client, err := ConnectSTDIO(
		"forge",
		"node",
		[]string{"../../mcp-server/dist/index.js"},
		nil,
	)
	r.NoError(err)
	defer client.Close()

	// Initialize
	initResult, err := client.Initialize(ctx)
	r.NoError(err)
	r.NotNil(initResult)
	r.Equal("forge", initResult.ServerInfo.Name)
	r.NotEmpty(initResult.ProtocolVersion)

	// List tools
	tools, err := client.ListTools(ctx)
	r.NoError(err)
	r.NotEmpty(tools)

	// Verify we have expected tools
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	r.True(toolNames["Read"], "should have Read tool")
	r.True(toolNames["Write"], "should have Write tool")
	r.True(toolNames["Bash"], "should have Bash tool")

	// List resources
	resources, err := client.ListResources(ctx)
	r.NoError(err)
	r.NotEmpty(resources)

	// Verify we have expected resources
	resourceURIs := make(map[string]bool)
	for _, resource := range resources {
		resourceURIs[resource.URI] = true
	}
	r.True(resourceURIs["forge://readme"], "should have readme resource")

	// Read a resource
	contents, err := client.ReadResource(ctx, "forge://readme")
	r.NoError(err)
	r.NotEmpty(contents)
	r.Equal("text", contents[0].Type)
	r.Contains(contents[0].Text, "Forge")
}

// TestMCPClientCallTool tests calling an MCP tool.
// Note: This currently won't work because the forge MCP server
// doesn't implement proper tool execution yet.
func TestMCPClientCallTool(t *testing.T) {
	t.Skip("Forge MCP server doesn't implement tool execution yet")

	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ConnectSTDIO(
		"forge",
		"node",
		[]string{"../../mcp-server/dist/index.js"},
		nil,
	)
	r.NoError(err)
	defer client.Close()

	_, err = client.Initialize(ctx)
	r.NoError(err)

	// Try to call the Read tool
	result, err := client.CallTool(ctx, "Read", map[string]any{
		"file_path": "README.md",
	})
	r.NoError(err)
	r.NotNil(result)
	r.False(result.IsError)
}
