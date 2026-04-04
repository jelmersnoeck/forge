// Package mcp implements an MCP (Model Context Protocol) client for connecting
// to remote MCP servers over HTTP. It handles JSON-RPC transport, OAuth 2.1
// with Dynamic Client Registration, and bridges MCP tools into Forge's
// tool registry.
//
//	┌─────────┐   JSON-RPC/HTTP    ┌──────────────┐
//	│  Forge   │─────────────────→ │  Remote MCP  │
//	│  Agent   │   tools/list      │  Server      │
//	│          │   tools/call      │              │
//	│ Registry │                   │  OAuth 2.1 + │
//	│ ┌──────┐ │  ←── responses    │  DCR         │
//	│ │MCP   │ │                   └──────────────┘
//	│ │Tools │ │
//	│ └──────┘ │
//	└─────────┘
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MCPConfig holds all configured MCP server connections.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfig describes a single remote MCP server.
type MCPServerConfig struct {
	// URL is the MCP server endpoint (Streamable HTTP transport).
	URL string `json:"url"`

	// Auth is the authentication method. "oauth" enables OAuth 2.1 + DCR.
	// Empty string means no auth or use static headers.
	Auth string `json:"auth,omitempty"`

	// Headers are static HTTP headers sent with every request.
	// Useful for API keys or bearer tokens that don't need OAuth.
	Headers map[string]string `json:"headers,omitempty"`
}

// LoadConfig merges MCP configuration from user (~/.forge/mcp.json) and
// project (.forge/mcp.json) levels. Project config overrides user config
// for servers with the same name.
func LoadConfig(cwd string) (MCPConfig, error) {
	merged := MCPConfig{
		Servers: make(map[string]MCPServerConfig),
	}

	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".forge", "mcp.json")
		if err := mergeConfigFile(&merged, userPath); err != nil {
			return merged, fmt.Errorf("load user mcp config: %w", err)
		}
	}

	projectPath := filepath.Join(cwd, ".forge", "mcp.json")
	if err := mergeConfigFile(&merged, projectPath); err != nil {
		return merged, fmt.Errorf("load project mcp config: %w", err)
	}

	return merged, nil
}

func mergeConfigFile(merged *MCPConfig, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	for name, server := range cfg.Servers {
		merged.Servers[name] = server
	}

	return nil
}
