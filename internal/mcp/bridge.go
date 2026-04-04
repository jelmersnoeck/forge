package mcp

import (
	"context"
	"fmt"
	"log"
)

// ConnectAndStore connects to an MCP server, caches its tool catalog in the
// store, and returns the client. Tools are NOT registered with the main tool
// registry — they're accessed lazily through the UseMCPTool gateway.
func ConnectAndStore(ctx context.Context, store *Store, serverName string, cfg MCPServerConfig, tokenStore *TokenStore) (*Client, error) {
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

	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		client.Close(ctx)
		return nil, fmt.Errorf("list tools from %s: %w", serverName, err)
	}

	store.Add(client, mcpTools)

	log.Printf("[mcp:%s] stored %d tools (lazy — not registered with LLM)", serverName, len(mcpTools))
	return client, nil
}
